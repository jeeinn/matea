package agents

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B1 write-channel coverage: a hub-hermes backend with
// workspace_transport=git_sync takes the SAME runViaHub write channel the
// hub-opencode A4 path uses — Matea Prepares (fake issuer), the hub-pushed
// draft branch is fetched from a local bare remote (file://), Approve
// validates the three elements and opens the PR against a fake Gitea, and
// Cleanup revokes the key. The fixtures are shared with gitsync_write_test.go
// on purpose: the contract is hub-type-agnostic.

// newGitSyncHermesFactory mirrors newGitSyncFactory but registers a
// hub-hermes-typed config entry and a hub registry, so tests can exercise the
// full runWriteTask routing (which looks the backend up by name).
func newGitSyncHermesFactory(db *store.DB, giteaURL string, issuer DeployKeyIssuer, hubs ...HubBackend) *RunnerFactory {
	reg := NewHubBackendRegistry()
	for _, h := range hubs {
		reg.Register(h)
	}
	return &RunnerFactory{
		db:           db,
		giteaFactory: &gitSyncTestGiteaFactory{client: gitea.NewClient(giteaURL, "")},
		hubRegistry:  reg,
		backends: config.AgentBackendsConfig{
			Backends: map[string]config.BackendConfig{
				"gs-hermes": {
					Type:               config.BackendTypeHubHermes,
					BaseURL:            "http://unused",
					WorkspaceTransport: config.WorkspaceTransportGitSync,
				},
				"gs-hermes-shared": {
					Type:               config.BackendTypeHubHermes,
					BaseURL:            "http://unused",
					WorkspaceTransport: config.WorkspaceTransportSharedPath,
				},
			},
		},
		deployKeyIssuer: issuer,
		sandboxCfg:      sandbox.Config{BaseDir: os.TempDir()},
	}
}

// TestRunWriteTaskGitSyncHermesRoutesViaHub is the B1 acceptance test: a
// solve_issue task bound to a hub-hermes+git_sync backend runs end-to-end
// through runWriteTask — which previously hard-errored at
// ResolveCodingBackend ("unsupported coding backend type hub-hermes").
func TestRunWriteTaskGitSyncHermesRoutesViaHub(t *testing.T) {
	taskID := int64(9101)
	cloneURL, mainHEAD, draftHEAD := setupGitSyncRemote(t, taskID)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)

	hub := &gitSyncTestHub{
		name:      "gs-hermes",
		pollState: StateDone,
		pollRes:   &BackendResult{Summary: "fixed it\n\n" + DraftHeadTrailer + draftHEAD},
	}
	f := newGitSyncHermesFactory(db, fake.server.URL, issuer, hub)

	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 21, TaskType: "solve_issue", Event: "Fix the bug"}
	agent := &store.Agent{Backend: "gs-hermes"}

	res, err := runWriteTask(context.Background(), task, agent, f, "dev")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "pr", res.Action)
	assert.Equal(t, 77, res.PRID)

	// Submit received the prepared GitSyncInfo (deploy key + draft contract).
	require.NotNil(t, hub.gotTC)
	require.NotNil(t, hub.gotTC.GitSync, "Submit must carry GitSyncInfo")
	assert.Equal(t, DraftBranchName(taskID), hub.gotTC.GitSync.DraftBranch)
	assert.Equal(t, mainHEAD, hub.gotTC.GitSync.BaseHEAD)
	assert.Equal(t, "FAKE-PRIVATE-KEY", hub.gotTC.GitSync.PrivateKey)
	assert.True(t, hub.gotTC.GitSync.HubPush)

	// Handle persisted the draft contract + key id, marked done.
	h, err := db.GetHubHandle(taskID)
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "gs-hermes", h.Backend)
	assert.Equal(t, DraftBranchName(taskID), h.DraftBranch)
	assert.Equal(t, mainHEAD, h.BaseHEAD)
	assert.Equal(t, int64(1), h.DeployKeyID)
	assert.Equal(t, store.HubHandleStatusDone, h.Status)

	// PR opened against the draft branch; key issued once, revoked once.
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, DraftBranchName(taskID), fake.prCreated.Head)
	assert.Equal(t, "main", fake.prCreated.Base)
	assert.Equal(t, []string{fmt.Sprintf("matea-hub-task-%d", taskID)}, issuer.issued)
	assert.Equal(t, []int64{1}, issuer.revoked)
}

// TestRunWriteTaskGitSyncHermesUnhealthyFailsBeforePrepare pins the fail-fast
// health probe: a down hub must error BEFORE runViaHub's Prepare issues a
// task-scoped deploy key, and the builtin fallback must NOT silently engage
// under git_sync (it would replace the hub-push trust model with a Matea-side
// push under the agent's own token — a silent privilege widening).
func TestRunWriteTaskGitSyncHermesUnhealthyFailsBeforePrepare(t *testing.T) {
	taskID := int64(9102)
	cloneURL, mainHEAD, _ := setupGitSyncRemote(t, taskID)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)

	hub := &gitSyncTestHub{name: "gs-hermes", healthErr: fmt.Errorf("connection refused")}
	f := newGitSyncHermesFactory(db, fake.server.URL, issuer, hub)

	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 22, TaskType: "solve_issue", Event: "x"}
	agent := &store.Agent{Backend: "gs-hermes"}

	_, err := runWriteTask(context.Background(), task, agent, f, "dev")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
	assert.Empty(t, issuer.issued, "no deploy key may be issued when the hub is down")
	assert.Nil(t, fake.prCreated)
}

// TestRunWriteTaskGitSyncSharedPathHermesNotRouted pins the coexistence
// window: hub-hermes with the legacy shared_path transport does NOT enter the
// git_sync channel (write tasks on it still fail loudly at
// ResolveCodingBackend, exactly as before B1).
func TestRunWriteTaskGitSyncSharedPathHermesNotRouted(t *testing.T) {
	hub := &gitSyncTestHub{name: "gs-hermes-shared"}
	f := newGitSyncHermesFactory(nil, "http://unused", &fakeDeployKeyIssuer{}, hub)

	_, ok := f.resolveGitSyncWriteHub(&store.Agent{Backend: "gs-hermes-shared"})
	assert.False(t, ok, "shared_path hub-hermes must not enter the git_sync write channel")

	_, ok = f.resolveGitSyncWriteHub(&store.Agent{Backend: ""})
	assert.False(t, ok, "builtin must not enter the git_sync write channel")
}

// TestRunViaHubGitSyncPartialResultFilledFromPrepare covers the B1 partial-
// result hardening: a hub that reports a non-nil git_sync result with empty
// fields gets them filled from the authoritative Prepare-side contract (draft
// branch) and the trailer (draft head), so Approve still validates and opens
// the PR.
func TestRunViaHubGitSyncPartialResultFilledFromPrepare(t *testing.T) {
	taskID := int64(9103)
	cloneURL, mainHEAD, draftHEAD := setupGitSyncRemote(t, taskID)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, cloneURL, issuer)

	hub := &gitSyncTestHub{
		name:      "gs-opencode",
		pollState: StateDone,
		pollRes: &BackendResult{
			Summary: "done\n\n" + DraftHeadTrailer + draftHEAD,
			GitSync: &GitSyncResult{}, // non-nil but empty — filled by runViaHub
		},
	}

	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 23, TaskType: "solve_issue", Event: "x"}
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 23, TaskID: taskID}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "pr", res.Action)
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, DraftBranchName(taskID), fake.prCreated.Head,
		"empty DraftBranch in the hub result must be filled from the Prepare contract")
}
