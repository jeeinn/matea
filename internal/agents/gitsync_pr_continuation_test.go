package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PR-conversation continuation for git_sync (2026-08-30).
//
// Two bugs, both reproduced live on jeeinn/rust-study:
//
//  1. Mentioning a coder on PR #8 (head matea/hub-14) pushed a brand new
//     branch matea/hub-17 and opened PR #9. Prepare hardcoded the draft branch
//     to matea/hub-{taskID}, so FinalizeWriteTaskPR — which only looks for a
//     PR whose HEAD is the branch it is given — never found the existing one.
//     The builtin write path never had this bug: resolveWorkBranch reuses
//     task.BaseBranch, the PR head.
//  2. The new PR was titled "AI Solution: AI Solution: issues": the subject
//     came from PR #8's own title, which already carried the prefix, and whose
//     "issues" was itself the webhook event name left over from the previous
//     title bug.
//
// These tests pin both: a PR conversation must push the PR's head branch, and
// a title must neither stack prefixes nor inherit an event name.

// fakeObject is an issue or PR served by newPRContinuationServer. Gitea
// numbers issues and PRs in one space, and /issues/{index} answers for both.
type fakeObject struct {
	number  int
	title   string
	body    string
	headRef string // PRs only
	state   string // PRs only; empty means "not a PR" (404 on /pulls/{index})
}

// newPRContinuationServer serves the two lookups the fixes rely on:
// /pulls/{index} (draft-branch reuse) and /issues/{index} (PR title). counts
// records how many times each object was fetched, so a test can prove the
// resolver does not probe the same conversation id twice.
func newPRContinuationServer(t *testing.T, objects []fakeObject, counts *map[int]int) *httptest.Server {
	t.Helper()
	byNumber := make(map[int]fakeObject, len(objects))
	for _, o := range objects {
		byNumber[o.number] = o
	}
	record := func(n int) {
		if counts != nil {
			(*counts)[n]++
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r/issues/{index}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		require.NoError(t, json.Unmarshal([]byte(r.PathValue("index")), &n))
		record(n)
		o, ok := byNumber[n]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"message": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"number": o.number,
			"title":  o.title,
			"body":   o.body,
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/{index}", func(w http.ResponseWriter, r *http.Request) {
		var n int
		require.NoError(t, json.Unmarshal([]byte(r.PathValue("index")), &n))
		record(n)
		o, ok := byNumber[n]
		// A plain issue is not a PR: Gitea 404s the pulls route for it.
		if !ok || o.state == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"message": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"number": o.number,
			"state":  o.state,
			"head":   map[string]any{"ref": o.headRef},
			"base":   map[string]any{"ref": "main"},
		})
	})
	return httptest.NewServer(mux)
}

func TestResolveGitSyncDraftBranchReusesOpenPRHead(t *testing.T) {
	objects := []fakeObject{
		{number: 7, title: "更新项目的readme"},
		{number: 8, title: "AI Solution: issues", body: "body\n\nRefs #7", headRef: "matea/hub-14", state: "open"},
	}
	counts := map[int]int{}
	srv := newPRContinuationServer(t, objects, &counts)
	defer srv.Close()
	client := gitea.NewClient(srv.URL, "tok")

	// PR conversation: PRID carries the conversation.
	branch, note := resolveGitSyncDraftBranch(client, "o", "r", &store.Task{ID: 17, PRID: 8})
	assert.Equal(t, "matea/hub-14", branch, "a task on an open PR must push that PR's head branch, not matea/hub-17")
	assert.Contains(t, note, "#8")

	// The effective key carries it instead when the resolver found no linked
	// issue (task.IssueID == PR number, PRID unset).
	branch, _ = resolveGitSyncDraftBranch(client, "o", "r", &store.Task{ID: 17, IssueID: 8})
	assert.Equal(t, "matea/hub-14", branch, "PRID==0 must not hide the conversation: IssueID shares Gitea's numbering")
}

func TestResolveGitSyncDraftBranchReuseIsRefusedWhenUnsafe(t *testing.T) {
	cases := []struct {
		name    string
		objects []fakeObject
		task    *store.Task
	}{
		{
			// Reusing a human-authored branch would let the coding backend
			// rewrite someone else's work.
			name:    "human head branch is left alone",
			objects: []fakeObject{{number: 8, headRef: "feature/login", state: "open"}},
			task:    &store.Task{ID: 17, PRID: 8},
		},
		{
			name:    "closed PR cannot take commits",
			objects: []fakeObject{{number: 8, headRef: "matea/hub-14", state: "closed"}},
			task:    &store.Task{ID: 17, PRID: 8},
		},
		{
			name:    "merged PR cannot take commits",
			objects: []fakeObject{{number: 8, headRef: "matea/hub-14", state: "merged"}},
			task:    &store.Task{ID: 17, PRID: 8},
		},
		{
			// A plain issue has no head branch at all.
			name:    "plain issue is not a PR",
			objects: []fakeObject{{number: 7, title: "只是一个 issue"}},
			task:    &store.Task{ID: 17, IssueID: 7},
		},
		{
			name:    "unknown conversation id",
			objects: nil,
			task:    &store.Task{ID: 17, PRID: 99, IssueID: 99},
		},
		{
			name:    "no ids at all",
			objects: nil,
			task:    &store.Task{ID: 17},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPRContinuationServer(t, tc.objects, nil)
			defer srv.Close()
			branch, note := resolveGitSyncDraftBranch(gitea.NewClient(srv.URL, "tok"), "o", "r", tc.task)
			assert.Empty(t, branch, "must fall back to a fresh per-task branch")
			assert.Empty(t, note)
		})
	}
}

func TestResolveGitSyncDraftBranchProbesEachIdOnce(t *testing.T) {
	objects := []fakeObject{{number: 8, headRef: "matea/hub-14", state: "open"}}
	counts := map[int]int{}
	srv := newPRContinuationServer(t, objects, &counts)
	defer srv.Close()

	// PRID and IssueID both hold the conversation id for a PR with no linked
	// issue; probing twice would double the API calls for every task.
	branch, _ := resolveGitSyncDraftBranch(gitea.NewClient(srv.URL, "tok"), "o", "r", &store.Task{ID: 17, PRID: 8, IssueID: 8})
	assert.Equal(t, "matea/hub-14", branch)
	assert.Equal(t, 1, counts[8], "the same conversation id must be probed once, not once per field")
}

// newPRContinuationTransport wires a git_sync transport against a fake Gitea
// serving the repo, branch and PR routes Prepare needs.
func newPRContinuationTransport(t *testing.T, objects []fakeObject) WorkspaceTransport {
	t.Helper()
	byNumber := make(map[int]fakeObject, len(objects))
	for _, o := range objects {
		byNumber[o.number] = o
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"default_branch": "main",
			"ssh_url":        "ssh://git@example.com/o/r.git",
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/branches/{name...}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name":   r.PathValue("name"),
			"commit": map[string]any{"id": "1111111111111111111111111111111111111111"},
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/{index}", func(w http.ResponseWriter, r *http.Request) {
		n := pathIndex(t, r.PathValue("index"))
		o, ok := byNumber[n]
		if !ok || o.state == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"message": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"number": o.number,
			"state":  o.state,
			"head":   map[string]any{"ref": o.headRef},
			"base":   map[string]any{"ref": "main"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(srv.URL, "")}
	return NewGitSyncTransport(factory, &fakeDeployKeyIssuer{}, t.TempDir(), DiffPolicy{})
}

func pathIndex(t *testing.T, raw string) int {
	t.Helper()
	var n int
	require.NoError(t, json.Unmarshal([]byte(raw), &n))
	return n
}

func TestGitSyncPrepareReusesPRHeadBranch(t *testing.T) {
	// The contract handed to the hub must name PR #8's head branch: that is
	// what makes FinalizeWriteTaskPR report the commits on PR #8 instead of
	// opening PR #9 (jeeinn/rust-study, 2026-08-29).
	transport := newPRContinuationTransport(t, []fakeObject{
		{number: 8, title: "AI Solution: issues", headRef: "matea/hub-14", state: "open"},
	})

	info, _, err := transport.Prepare(context.Background(), &store.Task{ID: 17, Repo: "o/r", PRID: 8, IssueID: 8}, "o", "r")
	require.NoError(t, err)
	assert.Equal(t, "matea/hub-14", info.DraftBranch,
		"task 17 on PR #8 must push matea/hub-14, not a fresh matea/hub-17")
	assert.Equal(t, "main", info.BaseBranch)
	assert.Equal(t, "matea-task-id: 17", info.RequiredFooter,
		"the footer still names THIS task, so validation stays task-scoped even on a branch another task created")
}

func TestGitSyncPrepareUsesFreshBranchWithoutAnOpenPR(t *testing.T) {
	// A task on a plain issue has no PR head to continue; it keeps the
	// per-task branch and its own PR.
	transport := newPRContinuationTransport(t, []fakeObject{
		{number: 7, title: "只是一个 issue"},
	})

	info, _, err := transport.Prepare(context.Background(), &store.Task{ID: 18, Repo: "o/r", IssueID: 7}, "o", "r")
	require.NoError(t, err)
	assert.Equal(t, DraftBranchName(18), info.DraftBranch)
}

func TestEffectiveDraftBranchNeedsAnAnchor(t *testing.T) {
	// Reusing a PR head without a continuation anchor would make the hub
	// branch from the base tip and silently drop the PR's existing commits.
	assert.Equal(t, "matea/hub-14",
		effectiveDraftBranch("matea/hub-14", "148f6cc1", 17), "anchored reuse is kept")
	assert.Equal(t, DraftBranchName(17),
		effectiveDraftBranch("matea/hub-14", "", 17), "no anchor → the task keeps its own branch")
	assert.Equal(t, DraftBranchName(17),
		effectiveDraftBranch(DraftBranchName(17), "", 17), "a per-task branch is untouched")
	assert.Equal(t, "", effectiveDraftBranch("", "", 17))
}
