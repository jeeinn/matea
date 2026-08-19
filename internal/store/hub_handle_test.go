package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHubHandleSaveGetUpdate(t *testing.T) {
	db := newTestDB(t)

	h := &HubHandle{
		TaskID:         7,
		Backend:        "hub-opencode",
		RemoteID:       "sess-abc",
		IdempotencyKey: "analyze_issue:o/r:1:0",
		Status:         HubHandleStatusRunning,
	}
	require.NoError(t, db.SaveHubHandle(h))

	got, err := db.GetHubHandle(7)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(7), got.TaskID)
	assert.Equal(t, "hub-opencode", got.Backend)
	assert.Equal(t, "sess-abc", got.RemoteID)
	assert.Equal(t, "analyze_issue:o/r:1:0", got.IdempotencyKey)
	assert.Equal(t, HubHandleStatusRunning, got.Status)

	// Update to terminal.
	require.NoError(t, db.UpdateHubHandleStatus(7, HubHandleStatusDone))
	got, err = db.GetHubHandle(7)
	require.NoError(t, err)
	assert.Equal(t, HubHandleStatusDone, got.Status)
}

func TestHubHandleGetMissing(t *testing.T) {
	db := newTestDB(t)
	got, err := db.GetHubHandle(999)
	require.NoError(t, err)
	assert.Nil(t, got, "missing handle should return nil, nil")
}

func TestHubHandleSaveReplaces(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 3, Backend: "a", RemoteID: "r1", Status: HubHandleStatusRunning}))
	// Re-submission overwrites the same task_id (at most one live handle per task).
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 3, Backend: "a", RemoteID: "r2", Status: HubHandleStatusRunning}))
	got, err := db.GetHubHandle(3)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "r2", got.RemoteID, "second SaveHubHandle must overwrite the first")
}

func TestHubHandleListAndHasNonTerminal(t *testing.T) {
	db := newTestDB(t)
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 1, Backend: "a", RemoteID: "r1", Status: HubHandleStatusRunning}))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 2, Backend: "a", RemoteID: "r2", Status: HubHandleStatusPending}))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 3, Backend: "a", RemoteID: "r3", Status: HubHandleStatusDone}))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 4, Backend: "a", RemoteID: "r4", Status: HubHandleStatusFailed}))

	list, err := db.ListNonTerminalHubHandles()
	require.NoError(t, err)
	ids := []int64{}
	for _, h := range list {
		ids = append(ids, h.TaskID)
	}
	assert.ElementsMatch(t, []int64{1, 2}, ids, "terminal handles (done/failed) must be excluded")

	has, err := db.HasNonTerminalHubHandle(1)
	require.NoError(t, err)
	assert.True(t, has)
	has, err = db.HasNonTerminalHubHandle(3)
	require.NoError(t, err)
	assert.False(t, has, "terminal handle must report false")
	has, err = db.HasNonTerminalHubHandle(999)
	require.NoError(t, err)
	assert.False(t, has, "no handle must report false")
}

func TestFailOrphanedRunningTasksExceptHub(t *testing.T) {
	db := newTestDB(t)
	agent := &Agent{
		Name: "hub-fail-test", GiteaUsername: "hub-fail-test", GiteaToken: "tok",
		Provider: "deepseek", Model: "deepseek-chat", SystemPrompt: "x", Role: RoleAnalyze, Status: "active",
	}
	require.NoError(t, db.CreateAgent(agent))
	// Hub task in flight: running + non-terminal handle.
	hubTask := &Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: StatusRunning, Repo: "o/r", IssueID: 1}
	require.NoError(t, db.CreateTask(hubTask))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: hubTask.ID, Backend: "hub-opencode", RemoteID: "s1", Status: HubHandleStatusRunning}))

	// Plain running task: no handle — should be failed on restart.
	plainTask := &Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: StatusRunning, Repo: "o/r", IssueID: 2}
	require.NoError(t, db.CreateTask(plainTask))

	n, err := db.FailOrphanedRunningTasksExceptHub("restart")
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the non-hub running task should be failed")

	gotHub, _ := db.GetTask(hubTask.ID)
	assert.Equal(t, StatusRunning, gotHub.Status, "hub task must be preserved for re-attach")
	gotPlain, _ := db.GetTask(plainTask.ID)
	assert.Equal(t, StatusFailed, gotPlain.Status, "plain running task must be failed")
}

func TestResetStaleRunningTasksExceptHub(t *testing.T) {
	db := newTestDB(t)
	agent := &Agent{
		Name: "hub-stale-test", GiteaUsername: "hub-stale-test", GiteaToken: "tok",
		Provider: "deepseek", Model: "deepseek-chat", SystemPrompt: "x", Role: RoleAnalyze, Status: "active",
	}
	require.NoError(t, db.CreateAgent(agent))
	// Hub running task, started long ago.
	hubTask := &Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: StatusRunning, Repo: "o/r", IssueID: 1}
	require.NoError(t, db.CreateTask(hubTask))
	_, err := db.Exec(`UPDATE tasks SET started_at = datetime('now','-2 hours') WHERE id = ?`, hubTask.ID)
	require.NoError(t, err)
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: hubTask.ID, Backend: "hub-opencode", RemoteID: "s1", Status: HubHandleStatusRunning}))

	// Plain stale running task.
	plainTask := &Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: StatusRunning, Repo: "o/r", IssueID: 2}
	require.NoError(t, db.CreateTask(plainTask))
	_, err = db.Exec(`UPDATE tasks SET started_at = datetime('now','-2 hours') WHERE id = ?`, plainTask.ID)
	require.NoError(t, err)

	n, err := db.ResetStaleRunningTasksExceptHub(1 * time.Hour)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "only the non-hub stale task should be reset")

	gotHub, _ := db.GetTask(hubTask.ID)
	assert.Equal(t, StatusRunning, gotHub.Status, "hub task must be left to its poll loop")
	gotPlain, _ := db.GetTask(plainTask.ID)
	assert.Equal(t, StatusPending, gotPlain.Status, "plain stale task must be reset to pending")
}

func TestHubHandleGitSyncFields(t *testing.T) {
	db := newTestDB(t)

	// git_sync write task: draft-branch contract state round-trips (task A2).
	h := &HubHandle{
		TaskID:         42,
		Backend:        "hub-opencode",
		RemoteID:       "sess-gitsync",
		IdempotencyKey: "solve_issue:o/r:12:0",
		Status:         HubHandleStatusRunning,
		DraftBranch:    "matea/hub-42",
		BaseHEAD:       "aaaa0000bbbb1111",
		DeployKeyID:    1234,
	}
	require.NoError(t, db.SaveHubHandle(h))

	got, err := db.GetHubHandle(42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "matea/hub-42", got.DraftBranch)
	assert.Equal(t, "aaaa0000bbbb1111", got.BaseHEAD)
	assert.Equal(t, int64(1234), got.DeployKeyID)

	// Read/reply task: git_sync fields stay at zero values.
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: 43, Backend: "hub-hermes", RemoteID: "run-1", Status: HubHandleStatusRunning}))
	got, err = db.GetHubHandle(43)
	require.NoError(t, err)
	assert.Equal(t, "", got.DraftBranch)
	assert.Equal(t, "", got.BaseHEAD)
	assert.Equal(t, int64(0), got.DeployKeyID)
}

// B4: sweep query coverage — the deploy-key sweep protects keys whose task
// owns a non-terminal handle and scans exactly the repos with handle rows.
func TestHubHandleSweepQueries(t *testing.T) {
	db := newTestDB(t)
	agent := &Agent{
		Name: "sweep-q", GiteaUsername: "sweep-q", GiteaToken: "tok",
		Provider: "deepseek", Model: "deepseek-chat", SystemPrompt: "x", Role: RoleCoder, Status: "active",
	}
	require.NoError(t, db.CreateAgent(agent))

	mkTask := func(repo string) int64 {
		task := &Task{Event: "x", Repo: repo, IssueID: 1, AgentID: agent.ID, TaskType: "solve_issue", Status: "pending"}
		require.NoError(t, db.CreateTask(task))
		return task.ID
	}

	// Two repos with handles; one task running (protected), one done, one failed.
	runA := mkTask("o/a")
	doneA := mkTask("o/a")
	failB := mkTask("o/b")
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: runA, Backend: "h", RemoteID: "r1", Status: HubHandleStatusRunning}))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: doneA, Backend: "h", RemoteID: "r2", Status: HubHandleStatusDone}))
	require.NoError(t, db.SaveHubHandle(&HubHandle{TaskID: failB, Backend: "h", RemoteID: "r3", Status: HubHandleStatusFailed}))

	repos, err := db.ListHubHandleRepos()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"o/a", "o/b"}, repos)

	protectedA, err := db.ListNonTerminalHubTaskIDsByRepo("o/a")
	require.NoError(t, err)
	assert.Equal(t, []int64{runA}, protectedA, "only the running task's key is in use")

	protectedB, err := db.ListNonTerminalHubTaskIDsByRepo("o/b")
	require.NoError(t, err)
	assert.Empty(t, protectedB, "failed handle's key is sweep-eligible")

	none, err := db.ListNonTerminalHubTaskIDsByRepo("o/never")
	require.NoError(t, err)
	assert.Empty(t, none)
}
