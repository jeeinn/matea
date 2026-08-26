package agents

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B2.2 coverage: session continuation for the builtin write path is
// git-native. A session task no longer reuses an on-disk workspace; it clones
// fresh (full history) and anchors its working branch on the session's
// recorded LastHead. All fixtures use a real git remote via file://.

// continuationRemote builds a bare remote where main has advanced PAST the
// session's draft branch tip: main carries MAIN.txt (added after the draft
// branched off), the draft branch matea/solve-issue-5 carries FIX.txt. Returns
// (cloneURL, draftHeadSHA).
func continuationRemote(t *testing.T) (string, string) {
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
	// The bare repo's HEAD defaults to refs/heads/master (init.defaultBranch);
	// point it at main so clones check out the default branch instead of an
	// unborn HEAD.
	run(remote, "symbolic-ref", "HEAD", "refs/heads/main")
	run(base, "init", "-q", work)
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "remote", "add", "origin", remote)

	// The session's draft branch: one commit on top of the original main.
	run(work, "checkout", "-q", "-b", "matea/solve-issue-5")
	require.NoError(t, os.WriteFile(filepath.Join(work, "FIX.txt"), []byte("session work\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "session task 1")
	run(work, "push", "-q", "origin", "matea/solve-issue-5")
	draftHead := strings.TrimSpace(run(work, "rev-parse", "HEAD"))

	// Main advances afterwards (the divergence a LastHead anchor must ignore).
	run(work, "checkout", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "MAIN.txt"), []byte("main moved\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "main advances")
	run(work, "push", "-q", "origin", "main")

	return remote, draftHead
}

// newContinuationFactory builds a RunnerFactory whose gitea client points at
// a fake API serving clone_url=file://<remote>, with a temp-mode sandbox.
func newContinuationFactory(t *testing.T, db *store.DB, remote string) *RunnerFactory {
	t.Helper()
	fake := newGitSyncFakeGitea(t, remote, "")
	sbCfg := sandbox.DefaultSandboxConfig()
	sbCfg.Mode = sandbox.ModeTemp
	sbCfg.CommandTimeout = 30 * time.Second
	return &RunnerFactory{
		db:           db,
		giteaFactory: &gitSyncTestGiteaFactory{client: gitea.NewClient(fake.server.URL, "")},
		sandboxCfg:   sbCfg,
	}
}

func continuationSession(t *testing.T, db *store.DB, id, branch, lastHead string) {
	t.Helper()
	require.NoError(t, db.CreateSession(&store.AgentSession{
		ID: id, Repo: "o/r", IssueID: 5, AgentID: 1, Role: store.RoleCoder,
		Status: store.SessionIdle, Branch: branch, LastHead: lastHead,
		LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}))
}

func TestPrepareWriteWorkspaceContinuationAnchorsOnLastHead(t *testing.T) {
	remote, draftHead := continuationRemote(t)
	db := newHubRunTestDB(t)
	continuationSession(t, db, "sess-cont", "matea/solve-issue-5", draftHead)
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7001, Repo: "o/r", IssueID: 5, TaskType: "solve_issue", Event: "continue", SessionID: "sess-cont"}
	wwc, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.NoError(t, err)
	defer wwc.Sandbox.Cleanup()

	assert.True(t, wwc.UseSession)
	assert.Equal(t, "matea/solve-issue-5", wwc.BranchName)
	assert.Equal(t, draftHead, wwc.Git.HeadSHA(), "workspace must be anchored on the session LastHead, not main")

	// Content proof: the draft branch's file is present, main's later commit is not.
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "FIX.txt"))
	assert.NoError(t, err, "draft branch content must be present")
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "MAIN.txt"))
	assert.True(t, os.IsNotExist(err), "main's post-branch commit must NOT leak into the continuation workspace")
}

func TestPrepareWriteWorkspaceContinuationMissingAnchorFails(t *testing.T) {
	remote, _ := continuationRemote(t)
	db := newHubRunTestDB(t)
	// LastHead that does not exist on the remote (branch deleted/rewound).
	continuationSession(t, db, "sess-gone", "matea/solve-issue-5", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7002, Repo: "o/r", IssueID: 5, TaskType: "solve_issue", Event: "x", SessionID: "sess-gone"}
	_, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session continuation anchor")
}

func TestPrepareWriteWorkspaceLegacySessionFallsBackToRemoteBranch(t *testing.T) {
	remote, draftHead := continuationRemote(t)
	db := newHubRunTestDB(t)
	// Pre-B2.2 row: branch recorded, no LastHead.
	continuationSession(t, db, "sess-legacy", "matea/solve-issue-5", "")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7003, Repo: "o/r", IssueID: 5, TaskType: "solve_issue", Event: "x", SessionID: "sess-legacy"}
	wwc, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.NoError(t, err)
	defer wwc.Sandbox.Cleanup()

	assert.Equal(t, "matea/solve-issue-5", wwc.BranchName)
	assert.Equal(t, draftHead, wwc.Git.HeadSHA(), "legacy session must anchor on the remote branch head")
}

// TestPrepareWriteWorkspaceSessionBranchLostStartsFresh reproduces the
// failed-first-task scenario: task 1 recorded the session branch at workspace
// prep but died (e.g. LLM rate limit) before any push, so the branch exists
// neither locally nor on the remote. Task 2 must start the branch fresh from
// the default-branch tip instead of failing with "not found locally or on
// remote".
func TestPrepareWriteWorkspaceSessionBranchLostStartsFresh(t *testing.T) {
	remote, _ := continuationRemote(t) // remote carries main + matea/solve-issue-5 only
	db := newHubRunTestDB(t)
	// Session branch recorded by a task that failed before its first push.
	continuationSession(t, db, "sess-lost", "matea/solve-issue-9", "")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7006, Repo: "o/r", IssueID: 9, TaskType: "solve_issue", Event: "x", SessionID: "sess-lost"}
	wwc, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.NoError(t, err)
	defer wwc.Sandbox.Cleanup()

	assert.Equal(t, "matea/solve-issue-9", wwc.BranchName)
	branch, err := wwc.Git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "matea/solve-issue-9", branch)

	// Fresh branch starts at main's tip: MAIN.txt present, FIX.txt (draft work
	// on the unrelated matea/solve-issue-5 branch) absent.
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "MAIN.txt"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "FIX.txt"))
	assert.True(t, os.IsNotExist(err))

	// Session keeps the same branch name for future continuation.
	sess, err := db.GetSession("sess-lost")
	require.NoError(t, err)
	assert.Equal(t, "matea/solve-issue-9", sess.Branch)
}

func TestPrepareWriteWorkspaceNewSessionCreatesFreshBranch(t *testing.T) {
	remote, _ := continuationRemote(t)
	db := newHubRunTestDB(t)
	// Session exists but carries no continuation state yet.
	continuationSession(t, db, "sess-new", "", "")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7004, Repo: "o/r", IssueID: 9, TaskType: "solve_issue", Event: "x", SessionID: "sess-new"}
	wwc, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.NoError(t, err)
	defer wwc.Sandbox.Cleanup()

	assert.Equal(t, "matea/solve-issue-9", wwc.BranchName)
	// Fresh branch starts at main's tip: MAIN.txt present, no FIX.txt.
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "MAIN.txt"))
	assert.NoError(t, err)

	// The generated branch name is recorded on the session for continuation.
	sess, err := db.GetSession("sess-new")
	require.NoError(t, err)
	assert.Equal(t, "matea/solve-issue-9", sess.Branch)
}

func TestSaveSessionProgressRecordsBranchAndHead(t *testing.T) {
	db := newHubRunTestDB(t)
	continuationSession(t, db, "sess-prog", "", "")
	f := &RunnerFactory{db: db}
	task := &store.Task{ID: 7005, SessionID: "sess-prog"}

	saveSessionProgress(f, task, "matea/solve-issue-9", "0123456789abcdef0123456789abcdef01234567")

	sess, err := db.GetSession("sess-prog")
	require.NoError(t, err)
	assert.Equal(t, "matea/solve-issue-9", sess.Branch)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", sess.LastHead)

	// Nil-DB / no-session callers are no-ops (never panic).
	saveSessionProgress(&RunnerFactory{}, &store.Task{ID: 1}, "b", "h")
	saveSessionProgress(f, &store.Task{ID: 2}, "b", "h") // no SessionID
}

// TestPrepareWriteWorkspaceSessionBranchViaBaseBranchStartsFresh covers the
// exact production failure seen on issue #5: the dispatcher copies the session
// branch into task.BaseBranch for solve_comment continuation when the webhook
// omits pull_request (pipeline.go). The fresh-start fallback must still apply —
// a session-derived BaseBranch is not a genuine PR head.
//
// The ai/dev/* names are deliberate: the session predates the matea/ branch
// rename, proving old-format session branches still continue correctly.
func TestPrepareWriteWorkspaceSessionBranchViaBaseBranchStartsFresh(t *testing.T) {
	remote, _ := continuationRemote(t) // remote carries main + matea/solve-issue-5 only
	db := newHubRunTestDB(t)
	continuationSession(t, db, "sess-base", "ai/dev/issue-9", "")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7007, Repo: "o/r", IssueID: 9, TaskType: "solve_comment", Event: "x",
		SessionID: "sess-base", BaseBranch: "ai/dev/issue-9"}
	wwc, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.NoError(t, err)
	defer wwc.Sandbox.Cleanup()

	branch, err := wwc.Git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "ai/dev/issue-9", branch)
	// Fresh branch starts at main's tip.
	_, err = os.Stat(filepath.Join(wwc.Sandbox.WorkDir, "MAIN.txt"))
	assert.NoError(t, err)
}

// TestPrepareWriteWorkspaceMissingPRHeadFails pins the fail-loud contract for a
// genuine PR head: a BaseBranch differing from the session branch comes from
// the webhook's pull_request payload and must exist — a missing one means the
// PR's head branch was deleted and continuing would resurrect confusing work.
func TestPrepareWriteWorkspaceMissingPRHeadFails(t *testing.T) {
	remote, _ := continuationRemote(t)
	db := newHubRunTestDB(t)
	continuationSession(t, db, "sess-pr", "matea/solve-issue-5", "")
	f := newContinuationFactory(t, db, remote)

	task := &store.Task{ID: 7008, Repo: "o/r", IssueID: 5, TaskType: "solve_comment", Event: "x",
		SessionID: "sess-pr", BaseBranch: "contributor/deleted-head"}
	_, err := prepareWriteWorkspace(context.Background(), task, &store.Agent{}, f)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found locally or on remote")
}
