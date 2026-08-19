package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A7: Approve-layer adversarial coverage against a REAL git remote (file://
// bare repo) — complements the pure validateGitSyncDraft unit tests in
// workspace_transport_test.go by exercising fetchDraft + validation together,
// plus Prepare failure modes and the runViaHub orchestration rejection path.

// initGitSyncBase builds a bare remote with one commit on main and returns
// (cloneURL, workDir, mainHEAD, run). Individual tests then push adversarial
// draft branches from workDir.
func initGitSyncBase(t *testing.T) (string, string, string, func(dir string, args ...string) string) {
	t.Helper()
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return string(out)
	}

	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	run(base, "init", "--bare", "-q", remote)
	run(base, "init", "-q", work)
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "remote", "add", "origin", remote)
	run(work, "push", "-q", "origin", "main")
	mainHEAD := strings.TrimSpace(run(work, "rev-parse", "HEAD"))
	return remote, work, mainHEAD, run
}

// newApproveTransport wires a gitSyncTransport against the fake Gitea with a
// recording issuer, returning the transport plus the fakes for assertions.
func newApproveTransport(t *testing.T, cloneURL, mainHEAD string) (WorkspaceTransport, *gitSyncFakeGitea, *fakeDeployKeyIssuer) {
	t.Helper()
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(fake.server.URL, "")}
	return NewGitSyncTransport(factory, issuer, t.TempDir(), DiffPolicy{}), fake, issuer
}

func gitSyncApproveTask(taskID int64) *store.Task {
	return &store.Task{ID: taskID, Repo: "o/r", IssueID: 12, TaskType: "solve_issue", Event: "Fix the bug"}
}

func TestGitSyncApproveHappyPathOpensPR(t *testing.T) {
	taskID := int64(9101)
	cloneURL, mainHEAD, draftHEAD := setupGitSyncRemote(t, taskID)
	transport, fake, _ := newApproveTransport(t, cloneURL, mainHEAD)

	info := &GitSyncInfo{
		CloneURL:       "ssh://unused",
		DraftBranch:    DraftBranchName(taskID),
		BaseBranch:     "main",
		BaseHEAD:       mainHEAD,
		RequiredFooter: RequiredFooter(taskID),
		HubPush:        true,
	}
	res, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch, DraftHEAD: draftHEAD}, "hub did the work")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "pr", res.Action)
	assert.Equal(t, 77, res.PRID)
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, info.DraftBranch, fake.prCreated.Head)
	assert.Equal(t, "main", fake.prCreated.Base)
}

func TestGitSyncApproveRejectsMissingFooter(t *testing.T) {
	taskID := int64(9102)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	// Hub pushes a draft branch off main but signs NOTHING.
	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: unsigned hub change")
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	transport, fake, _ := newApproveTransport(t, remote, mainHEAD)
	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}

	_, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required footer")
	assert.Nil(t, fake.prCreated, "no PR for unsigned commits")
}

func TestGitSyncApproveRejectsWrongStartPoint(t *testing.T) {
	taskID := int64(9103)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	// Hub branches off an ORPHAN root — not descended from the base anchor.
	run(work, "checkout", "-q", "--orphan", DraftBranchName(taskID))
	run(work, "rm", "-rf", ".")
	require.NoError(t, os.WriteFile(filepath.Join(work, "evil.go"), []byte("package evil\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: orphan change", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	transport, fake, _ := newApproveTransport(t, remote, mainHEAD)
	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}

	_, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Nil(t, fake.prCreated)
}

func TestGitSyncApproveRejectsBaseDrift(t *testing.T) {
	taskID := int64(9104)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	// Hub anchors on the current main, pushes a properly-signed draft…
	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: hub change", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	// …then main DRIFTS (someone else merges) before Matea approves.
	run(work, "checkout", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "other.go"), []byte("package other\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "unrelated merge")
	run(work, "push", "-q", "origin", "main")

	transport, fake, _ := newApproveTransport(t, remote, mainHEAD)
	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}

	_, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drifted", "v3.1 policy: drift = fail + warn, no auto rebase")
	assert.Nil(t, fake.prCreated)
}

func TestGitSyncApproveRejectsHubBranchDiscrepancy(t *testing.T) {
	taskID := int64(9105)
	cloneURL, mainHEAD, _ := setupGitSyncRemote(t, taskID)
	transport, fake, _ := newApproveTransport(t, cloneURL, mainHEAD)

	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}
	// Hub reports having pushed a DIFFERENT branch than assigned.
	_, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: "matea/hub-OTHER"}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch exclusivity")
	assert.Nil(t, fake.prCreated)
}

func TestGitSyncPrepareRequiresSSHURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"default_branch": "main", "clone_url": "http://x/o/r.git"}) // no ssh_url
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(srv.URL, "")}
	transport := NewGitSyncTransport(factory, &fakeDeployKeyIssuer{}, t.TempDir(), DiffPolicy{})

	_, _, err := transport.Prepare(context.Background(), gitSyncApproveTask(9201), "o", "r", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no ssh_url")
}

func TestGitSyncPrepareIssueFailurePropagates(t *testing.T) {
	cloneURL, _, mainHEAD, _ := initGitSyncBase(t)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{issueErr: errors.New("gitea 422 duplicate key")}
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(fake.server.URL, "")}
	transport := NewGitSyncTransport(factory, issuer, t.TempDir(), DiffPolicy{})

	info, key, err := transport.Prepare(context.Background(), gitSyncApproveTask(9202), "o", "r", "main")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "issue deploy key")
	assert.Nil(t, info)
	assert.Nil(t, key)
}

func TestGitSyncPrepareHappyPath(t *testing.T) {
	taskID := int64(9203)
	cloneURL, _, mainHEAD, _ := initGitSyncBase(t)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(fake.server.URL, "")}
	transport := NewGitSyncTransport(factory, issuer, t.TempDir(), DiffPolicy{})

	info, key, err := transport.Prepare(context.Background(), gitSyncApproveTask(taskID), "o", "r", "")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.NotNil(t, key)
	assert.Equal(t, "ssh://git@example.com/o/r.git", info.CloneURL, "hub must get the SSH url, not the https clone url")
	assert.Equal(t, DraftBranchName(taskID), info.DraftBranch)
	assert.Equal(t, "main", info.BaseBranch, "falls back to repo default branch")
	assert.Equal(t, mainHEAD, info.BaseHEAD)
	assert.Equal(t, RequiredFooter(taskID), info.RequiredFooter)
	assert.True(t, info.HubPush)
	assert.Equal(t, key.PrivateKey, info.PrivateKey, "task-scoped key is handed to the hub")
	assert.Equal(t, []string{"matea-hub-task-9203"}, issuer.issued)
}

func TestGitSyncCleanupNilKeyIsNoop(t *testing.T) {
	cloneURL, _, mainHEAD, _ := initGitSyncBase(t)
	transport, _, issuer := newApproveTransport(t, cloneURL, mainHEAD)
	require.NoError(t, transport.Cleanup(context.Background(), "o", "r", nil))
	assert.Empty(t, issuer.revoked)
}

// TestRunViaHubGitSyncRejectsMissingFooter ties the transport rejection into
// the runViaHub orchestration: the error surfaces, no PR is opened, and the
// deploy key is STILL revoked on the failure path.
func TestRunViaHubGitSyncRejectsMissingFooter(t *testing.T) {
	taskID := int64(9301)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: unsigned hub change")
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	fake := newGitSyncFakeGitea(t, remote, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "pushed, trust me"}}
	task := gitSyncApproveTask(taskID)
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required footer")
	assert.Nil(t, fake.prCreated, "unsigned draft must not produce a PR")
	assert.Equal(t, []int64{1}, issuer.revoked, "key must be revoked even when approve rejects")
}
