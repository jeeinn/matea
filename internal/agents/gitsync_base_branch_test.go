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

// Base-branch resolution for git_sync (2026-08-29 start-point-anchoring
// post-mortem, F1/F2/F5).
//
// The bug: Prepare trusted store.Task.BaseBranch, which holds the PR head /
// session working branch. For a PR-conversation continuation that value is the
// PREVIOUS task's draft branch (matea/hub-14), so Matea computed the next
// draft's merge target as matea/hub-14 and anchored drift detection there
// instead of on the PR's real base (main). These tests pin the corrected
// resolution: PR base → repo default → main, never task.BaseBranch, never a
// draft branch.

// newBaseBranchTransport wires a transport against a fake Gitea whose repo
// default branch and (optional) PR base ref are configurable. queried records
// every branch GetBranch was called for, so a test can prove Prepare anchors
// on the branch it actually targets.
func newBaseBranchTransport(t *testing.T, defaultBranch, prBaseRef string) (WorkspaceTransport, *[]string, *fakeDeployKeyIssuer) {
	t.Helper()
	queried := &[]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"default_branch": defaultBranch,
			"clone_url":      "http://x/o/r.git",
			"ssh_url":        "ssh://git@example.com/o/r.git",
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/branches/{name...}", func(w http.ResponseWriter, r *http.Request) {
		*queried = append(*queried, r.PathValue("name"))
		json.NewEncoder(w).Encode(map[string]any{
			"name":   r.PathValue("name"),
			"commit": map[string]any{"id": "1111111111111111111111111111111111111111"},
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/{index}", func(w http.ResponseWriter, r *http.Request) {
		if prBaseRef == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"message": "not found"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"number": 8,
			"base":   map[string]any{"ref": prBaseRef},
			"head":   map[string]any{"ref": "matea/hub-14"},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	issuer := &fakeDeployKeyIssuer{}
	factory := &gitSyncTestGiteaFactory{client: gitea.NewClient(srv.URL, "")}
	return NewGitSyncTransport(factory, issuer, t.TempDir(), DiffPolicy{}), queried, issuer
}

// prContinuationTask reproduces the incident's task 16: a second
// @code-opencode mention on PR #8, continuing the session of task 14.
func prContinuationTask(taskID int64) *store.Task {
	task := gitSyncApproveTask(taskID)
	task.PRID = 8
	task.BaseBranch = DraftBranchName(14) // what pipeline.go actually stores for this task
	return task
}

func TestGitSyncPrepareResolvesBaseFromPRBase(t *testing.T) {
	transport, queried, _ := newBaseBranchTransport(t, "trunk", "release/1.0")

	info, _, err := transport.Prepare(context.Background(), prContinuationTask(9700), "o", "r")
	require.NoError(t, err)
	assert.Equal(t, "release/1.0", info.BaseBranch,
		"PR base ref wins over both task.BaseBranch (matea/hub-14) and the repo default (trunk)")
	assert.Equal(t, []string{"release/1.0"}, *queried, "Prepare anchors on the branch it actually targets")
}

func TestGitSyncPrepareIgnoresTaskBaseBranch(t *testing.T) {
	transport, queried, _ := newBaseBranchTransport(t, "develop", "") // no PR endpoint → 404
	task := gitSyncApproveTask(9701)
	task.BaseBranch = DraftBranchName(9700) // session draft branch — must be ignored

	info, _, err := transport.Prepare(context.Background(), task, "o", "r")
	require.NoError(t, err)
	assert.Equal(t, "develop", info.BaseBranch, "no PR → repository default branch")
	assert.Equal(t, []string{"develop"}, *queried)
}

func TestGitSyncPrepareFallsBackWhenPRBaseIsDraftBranch(t *testing.T) {
	// A legacy PR opened with the buggy base (matea/hub-14) must not propagate:
	// the new draft targets the repository default instead of a scratch branch.
	transport, _, _ := newBaseBranchTransport(t, "main", DraftBranchName(14))

	info, _, err := transport.Prepare(context.Background(), prContinuationTask(9702), "o", "r")
	require.NoError(t, err)
	assert.Equal(t, "main", info.BaseBranch)
}

func TestGitSyncPrepareFailsWhenOnlyDraftBasesAvailable(t *testing.T) {
	transport, _, issuer := newBaseBranchTransport(t, DraftBranchName(99), DraftBranchName(14))

	_, key, err := transport.Prepare(context.Background(), prContinuationTask(9703), "o", "r")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no non-draft base branch")
	assert.Nil(t, key)
	assert.Empty(t, issuer.issued, "no deploy key is issued when the base branch cannot be resolved")
}

// --- runViaHub: the continuation PR must target the PR's own base ------------

func TestRunViaHubGitSyncContinuationTargetsPRBase(t *testing.T) {
	task1ID, task2ID := int64(9521), int64(9522)
	remote, baseB, anchorX, _ := continuationApproveRemote(t, task1ID, task2ID, true)

	// PR #8 targets main while the repository default is trunk: only the PR
	// base ref can produce the right answer.
	fake := newGitSyncFakeGitea(t, remote, baseB)
	fake.defaultBranch = "trunk"
	fake.prBaseRef = "main"

	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)
	continuationSession(t, db, "sess-pr-cont", DraftBranchName(task1ID), anchorX)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "continued"}}
	task := prContinuationTask(task2ID)
	task.SessionID = "sess-pr-cont"
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: task2ID}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	assert.Equal(t, 77, res.PRID)

	require.NotNil(t, hub.gotTC.GitSync)
	assert.Equal(t, "main", hub.gotTC.GitSync.BaseBranch, "hub contract carries the PR's merge target")
	assert.Equal(t, anchorX, hub.gotTC.GitSync.AnchorHEAD, "continuation still anchors on the session LastHead")
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, DraftBranchName(task2ID), fake.prCreated.Head)
	assert.Equal(t, "main", fake.prCreated.Base,
		"PR must open against the PR under discussion's base, not the previous draft branch")

	// The resolved base is persisted so a restart re-attach does not re-derive
	// it from task.BaseBranch.
	handle, err := db.GetHubHandle(task2ID)
	require.NoError(t, err)
	require.NotNil(t, handle)
	assert.Equal(t, "main", handle.BaseBranch)
}

// A handle persisted before hub_handles.base_branch existed must re-resolve the
// base with Prepare's own rules instead of trusting task.BaseBranch.
func TestRunViaHubGitSyncReattachResolvesMissingBaseBranch(t *testing.T) {
	task1ID, task2ID := int64(9531), int64(9532)
	remote, baseB, anchorX, _ := continuationApproveRemote(t, task1ID, task2ID, true)

	fake := newGitSyncFakeGitea(t, remote, baseB)
	fake.defaultBranch = "trunk"
	fake.prBaseRef = "main"

	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, &fakeDeployKeyIssuer{})
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID:      task2ID,
		Backend:     "gs-opencode",
		RemoteID:    "remote-legacy",
		Status:      store.HubHandleStatusRunning,
		DraftBranch: DraftBranchName(task2ID),
		BaseHEAD:    baseB,
		AnchorHEAD:  anchorX,
		// BaseBranch intentionally empty: row written before the column existed.
	}))

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "continued"}}
	task := prContinuationTask(task2ID)
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: task2ID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	assert.Nil(t, hub.gotTC, "re-attach must not re-submit the task")
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, "main", fake.prCreated.Base)
}

// --- Approve diagnostics (F5) -----------------------------------------------

func TestGitSyncApproveRejectsBaseTipStartNamesLostWork(t *testing.T) {
	task1ID, task2ID := int64(9541), int64(9542)
	// Hub branched from the base tip instead of the continuation anchor — the
	// exact shape of the production failure.
	remote, baseB, anchorX, _ := continuationApproveRemote(t, task1ID, task2ID, false)
	transport, fake, _ := newApproveTransport(t, remote, baseB)

	info := &GitSyncInfo{
		DraftBranch:    DraftBranchName(task2ID),
		BaseBranch:     "main",
		BaseHEAD:       baseB,
		AnchorHEAD:     anchorX,
		RequiredFooter: RequiredFooter(task2ID),
		HubPush:        true,
	}
	_, err := transport.Approve(context.Background(), gitSyncApproveTask(task2ID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Contains(t, err.Error(), "branched from base", "operator must learn the hub started from the base tip")
	assert.Contains(t, err.Error(), "previous session's work is NOT in this branch")
	assert.Nil(t, fake.prCreated)
}
