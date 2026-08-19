package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// C4 performance budget: git_sync Approve cost under large-repo stress.
//
// Approve's git-side cost is dominated by fetchDraft: a FULL-history fetch of
// the draft + base branches, then merge-base / log / diff over anchor..head.
// Network latency is deployment-specific, so these tests pin the part Matea
// controls — the local git machinery — against a file:// bare remote whose
// history is synthesized with git fast-import (thousands of commits in ~1s).
//
// Benchmarks are opt-in: go test ./internal/agents/ -run XXX -bench BenchmarkGitSyncApprove
// The SLO they back lives in docs/HUB-BACKENDS.md §性能预算.

// perfKV is one file mutation in a fast-import commit.
type perfKV struct{ path, content string }

// writeFastCommit appends one commit to a fast-import stream.
func writeFastCommit(sb *strings.Builder, branch string, mark int, msg, from string, files []perfKV) {
	sb.WriteString("commit refs/heads/" + branch + "\n")
	fmt.Fprintf(sb, "mark :%d\n", mark)
	sb.WriteString("author T <t@t> 1700000000 +0000\n")
	sb.WriteString("committer T <t@t> 1700000000 +0000\n")
	fmt.Fprintf(sb, "data %d\n%s", len(msg), msg)
	if from != "" {
		sb.WriteString("from " + from + "\n")
	}
	for _, f := range files {
		fmt.Fprintf(sb, "M 100644 inline %s\ndata %d\n%s", f.path, len(f.content), f.content)
	}
	sb.WriteString("\n")
}

// buildPerfRepo synthesizes a bare remote: baseCommits on main, then a draft
// branch off the main tip carrying draftCommits footered commits, each touching
// filesPerCommit distinct paths. Returns (cloneURL, mainHEAD, draftHEAD).
func buildPerfRepo(tb testing.TB, taskID int64, baseCommits, draftCommits, filesPerCommit int) (string, string, string) {
	tb.Helper()
	base := tb.TempDir()
	remote := base + "/remote.git"

	run := func(dir string, stdin string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if stdin != "" {
			cmd.Stdin = strings.NewReader(stdin)
		}
		out, err := cmd.CombinedOutput()
		require.NoError(tb, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}

	run(base, "", "init", "--bare", "-q", remote)

	// Stream 1: main history.
	var sb strings.Builder
	for i := 1; i <= baseCommits; i++ {
		writeFastCommit(&sb, "main", i,
			fmt.Sprintf("base commit %d\n", i), "",
			[]perfKV{{fmt.Sprintf("src/mod/file_%d.go", i%200), fmt.Sprintf("package mod // rev %d\n", i)}})
	}
	run(remote, sb.String(), "fast-import", "--quiet")
	mainHEAD := run(remote, "", "rev-parse", "refs/heads/main")

	// Stream 2: draft branch with the required footer on every commit.
	// Only the FIRST commit sets `from refs/heads/main`; subsequent commits in
	// the same stream chain onto it (a `from` on every commit would re-parent
	// each one to main and leave a 1-commit branch).
	sb.Reset()
	for j := 1; j <= draftCommits; j++ {
		files := make([]perfKV, 0, filesPerCommit)
		for k := 0; k < filesPerCommit; k++ {
			files = append(files, perfKV{
				fmt.Sprintf("draft/changes_%d_%d.go", j, k),
				fmt.Sprintf("package draft // commit %d change %d\n", j, k),
			})
		}
		from := ""
		if j == 1 {
			from = "refs/heads/main"
		}
		writeFastCommit(&sb, DraftBranchName(taskID), baseCommits+j,
			fmt.Sprintf("draft work %d\n\n%s\n", j, RequiredFooter(taskID)), from, files)
	}
	run(remote, sb.String(), "fast-import", "--quiet")
	draftHEAD := run(remote, "", "rev-parse", "refs/heads/"+DraftBranchName(taskID))

	return remote, mainHEAD, draftHEAD
}

// perfFakeGitea serves the exact API surface Approve touches (repo lookup,
// base branch, PR list/create) — the B-compatible twin of newGitSyncFakeGitea.
func perfFakeGitea(tb testing.TB, cloneURL, mainHEAD string) *httptest.Server {
	tb.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "main",
			"clone_url":      cloneURL,
			"ssh_url":        "ssh://git@example.com/o/r.git",
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/branches/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"name": "main", "commit": map[string]any{"id": mainHEAD}})
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]any{})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"number": 77, "html_url": "http://fake/pr/77"})
	})
	srv := httptest.NewServer(mux)
	tb.Cleanup(srv.Close)
	return srv
}

func perfApproveTransport(tb testing.TB, cloneURL, mainHEAD string) WorkspaceTransport {
	tb.Helper()
	srv := perfFakeGitea(tb, cloneURL, mainHEAD)
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(srv.URL, "")}
	return NewGitSyncTransport(factory, &fakeDeployKeyIssuer{}, tb.TempDir(), DiffPolicy{})
}

// TestGitSyncApproveLargeRepoStress is the CI-safe stress case: a repo with a
// few hundred commits and a draft touching 150 paths across 5 commits must
// validate correctly (and the B3 evidence must cover every changed path).
// Timing is asserted in the opt-in benchmark, not here, to stay flake-free.
func TestGitSyncApproveLargeRepoStress(t *testing.T) {
	taskID := int64(99001)
	const baseCommits, draftCommits, filesPer = 300, 5, 30
	remote, mainHEAD, draftHEAD := buildPerfRepo(t, taskID, baseCommits, draftCommits, filesPer)

	transport := perfApproveTransport(t, remote, mainHEAD)
	info := &GitSyncInfo{
		DraftBranch:    DraftBranchName(taskID),
		BaseBranch:     "main",
		BaseHEAD:       mainHEAD,
		RequiredFooter: RequiredFooter(taskID),
		HubPush:        true,
	}
	result := &GitSyncResult{DraftBranch: info.DraftBranch}

	res, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, result, "stress run")
	require.NoError(t, err)
	assert.Equal(t, 77, res.PRID)
	assert.Equal(t, draftHEAD, result.DraftHEAD, "head normalized to the fetched value even at scale")

	// The fetch evidence must see every changed path through the full draft
	// range (guards against range-narrowing regressions on long histories).
	client := gitea.NewClient(perfFakeGitea(t, remote, mainHEAD).URL, "")
	tr := NewGitSyncTransport(&gitSyncTestGiteaFactory{client: client}, &fakeDeployKeyIssuer{}, t.TempDir(), DiffPolicy{})
	gst, ok := tr.(*gitSyncTransport)
	require.True(t, ok)
	fetched, ferr := gst.fetchDraft(context.Background(), client, "o", "r", info)
	require.NoError(t, ferr)
	assert.Len(t, fetched.NewCommitMsgs, draftCommits)
	assert.Len(t, fetched.ChangedPaths, draftCommits*filesPer)
}

// BenchmarkGitSyncApprove measures end-to-end Approve latency (fetchDraft +
// four-element validation + PR open against a fake Gitea) at three scales.
// Run: go test ./internal/agents/ -run XXX -bench BenchmarkGitSyncApprove -benchtime 3x
func BenchmarkGitSyncApprove(b *testing.B) {
	scales := []struct {
		name                             string
		baseCommits, draftCommits, files int
	}{
		{"small-50base-1draft", 50, 1, 5},
		{"medium-1000base-10draft", 1000, 10, 20},
		{"large-5000base-50draft", 5000, 50, 10},
	}
	for _, s := range scales {
		b.Run(s.name, func(b *testing.B) {
			taskID := int64(99002)
			remote, mainHEAD, _ := buildPerfRepo(b, taskID, s.baseCommits, s.draftCommits, s.files)
			transport := perfApproveTransport(b, remote, mainHEAD)
			info := &GitSyncInfo{
				DraftBranch:    DraftBranchName(taskID),
				BaseBranch:     "main",
				BaseHEAD:       mainHEAD,
				RequiredFooter: RequiredFooter(taskID),
				HubPush:        true,
			}
			task := gitSyncApproveTask(taskID)
			agent := &store.Agent{}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result := &GitSyncResult{DraftBranch: info.DraftBranch}
				if _, err := transport.Approve(context.Background(), task, agent, "o", "r", info, result, "bench"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
