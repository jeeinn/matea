package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B4 coverage: the deploy-key lifecycle hook (sweep). runViaHub revokes at
// every terminal state; the sweep is the backstop for revoke failures, the
// Prepare→persist crash window, and future handle-row deletions.

func TestDeployKeyTaskID(t *testing.T) {
	id, ok := DeployKeyTaskID("matea-hub-task-42")
	assert.True(t, ok)
	assert.Equal(t, int64(42), id)

	for _, foreign := range []string{"", "deploy-key", "matea-hub-task-", "matea-hub-task-abc", "matea-hub-task--1", "matea-hub-task-0"} {
		_, ok := DeployKeyTaskID(foreign)
		assert.False(t, ok, "%q must not parse", foreign)
	}
}

// sweepFixture stands up a fake Gitea serving a mutable deploy-key list per
// repo (populate fx.keys after creating handle rows — key titles embed the
// auto-assigned task IDs), a recording issuer, and a test DB.
type sweepFixture struct {
	keys    map[string][]gitea.DeployKey // repo → keys
	issuer  *fakeDeployKeyIssuer
	db      *store.DB
	client  *gitea.Client
	agentID int64 // tasks reference agents via FK
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	fx := &sweepFixture{keys: map[string][]gitea.DeployKey{}, issuer: &fakeDeployKeyIssuer{}, db: newHubRunTestDB(t)}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/repos/{owner}/{repo}/keys → ["", "api", "v1", "repos", o, r, "keys"]
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 7 || parts[3] != "repos" || parts[6] != "keys" {
			http.NotFound(w, r)
			return
		}
		ks, ok := fx.keys[parts[4]+"/"+parts[5]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(ks)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	fx.client = gitea.NewClient(srv.URL, "")
	agent := &store.Agent{
		Name: "sweep", GiteaUsername: "sweep", GiteaToken: "tok",
		Provider: "deepseek", Model: "deepseek-chat", SystemPrompt: "x", Role: store.RoleCoder, Status: "active",
	}
	require.NoError(t, fx.db.CreateAgent(agent))
	fx.agentID = agent.ID
	return fx
}

// addHandle creates a task + hub handle row and returns the task ID (which
// production key titles embed via matea-hub-task-<task.ID>).
func (fx *sweepFixture) addHandle(t *testing.T, repo, status string) int64 {
	t.Helper()
	task := &store.Task{Event: "x", Repo: repo, IssueID: 1, AgentID: fx.agentID, TaskType: "solve_issue", Status: "pending"}
	require.NoError(t, fx.db.CreateTask(task))
	require.NoError(t, fx.db.SaveHubHandle(&store.HubHandle{
		TaskID: task.ID, Backend: "h", RemoteID: fmt.Sprintf("r-%d", task.ID), Status: status,
	}))
	return task.ID
}

func TestSweepOrphanedDeployKeys(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	fresh := now.Add(-1 * time.Minute)

	fx := newSweepFixture(t)
	terminalTask := fx.addHandle(t, "o/r", store.HubHandleStatusDone)
	runningTask := fx.addHandle(t, "o/r", store.HubHandleStatusRunning)
	fx.keys["o/r"] = []gitea.DeployKey{
		{ID: 11, Title: fmt.Sprintf("matea-hub-task-%d", terminalTask), CreatedAt: old}, // terminal handle → sweep (revoke-failure backstop)
		{ID: 12, Title: fmt.Sprintf("matea-hub-task-%d", runningTask), CreatedAt: old},  // running handle → PROTECTED
		{ID: 13, Title: "matea-hub-task-777777", CreatedAt: fresh},                      // no row but fresh → grace window, keep
		{ID: 14, Title: "matea-hub-task-888888", CreatedAt: old},                        // no row (crash window) → sweep
		{ID: 15, Title: "operator-key", CreatedAt: old},                                 // foreign title → never touch
		{ID: 16, Title: "matea-hub-task-abc", CreatedAt: old},                           // malformed matea-ish title → never touch
	}

	n, err := SweepOrphanedDeployKeys(context.Background(), fx.db, fx.client, fx.issuer, DeployKeySweepGrace, now)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, []int64{11, 14}, fx.issuer.revoked, "terminal-handle key + crash-window key swept; running/fresh/foreign kept")

	// Audit rows landed.
	logs, lerr := fx.db.ListOperationLogs(10, 0)
	require.NoError(t, lerr)
	swept := 0
	for _, l := range logs {
		if l.Action == "git_sync_key_swept" {
			swept++
			assert.Contains(t, l.Detail, "o/r")
		}
	}
	assert.Equal(t, 2, swept)
}

func TestSweepOrphanedDeployKeysSkipsBrokenRepo(t *testing.T) {
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	fx := newSweepFixture(t)
	fx.addHandle(t, "o/broken", store.HubHandleStatusDone) // no keys entry → fake 404s its list call
	goodTask := fx.addHandle(t, "o/good", store.HubHandleStatusDone)
	fx.keys["o/good"] = []gitea.DeployKey{
		{ID: 21, Title: fmt.Sprintf("matea-hub-task-%d", goodTask), CreatedAt: old},
	}

	n, err := SweepOrphanedDeployKeys(context.Background(), fx.db, fx.client, fx.issuer, DeployKeySweepGrace, now)
	require.NoError(t, err, "one broken repo must not fail the sweep")
	assert.Equal(t, 1, n, "the healthy repo is still swept")
	assert.Equal(t, []int64{21}, fx.issuer.revoked)
}

func TestSweepOrphanedDeployKeysNilArgs(t *testing.T) {
	_, err := SweepOrphanedDeployKeys(context.Background(), nil, nil, nil, time.Minute, time.Now())
	require.Error(t, err)
}
