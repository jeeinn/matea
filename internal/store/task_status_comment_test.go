package store

import (
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusCardAgentSeq keeps agent names unique across tests (tasks have a
// foreign key on agents, so every task needs a real owner row).
var statusCardAgentSeq int64

// newTaskForStatusCard creates a minimal task row for the status-card tests,
// preceded by the agent row the task's foreign key requires.
func newTaskForStatusCard(t *testing.T, db *DB) *Task {
	t.Helper()
	seq := atomic.AddInt64(&statusCardAgentSeq, 1)
	agent := &Agent{
		Name:          fmt.Sprintf("status-card-agent-%d", seq),
		GiteaUsername: fmt.Sprintf("sc-agent-%d", seq),
		GiteaToken:    "token",
		Role:          "coder",
		Status:        "active",
	}
	require.NoError(t, db.CreateAgent(agent))

	task := &Task{Event: "Issue 5", Repo: "jeeinn/rust-study", IssueID: 5, AgentID: agent.ID, TaskType: "solve_issue", Status: StatusPending}
	require.NoError(t, db.CreateTask(task))
	return task
}

// TestTaskStatusCommentIDDefaultsToZero pins the migration contract: existing
// rows predate the column and must read back as 0 (no card yet), not as NULL —
// the caller branches on 0 to decide between create and PATCH.
func TestTaskStatusCommentIDDefaultsToZero(t *testing.T) {
	db := newTestDB(t)
	task := newTaskForStatusCard(t, db)

	got, err := db.GetTask(task.ID)
	require.NoError(t, err)
	assert.Zero(t, got.StatusCommentID)
}

// TestUpdateTaskStatusCommentIDRoundTrip covers the create-then-PATCH flow: the
// ID is written after the card is posted and read back by the next update.
func TestUpdateTaskStatusCommentIDRoundTrip(t *testing.T) {
	db := newTestDB(t)
	task := newTaskForStatusCard(t, db)

	require.NoError(t, db.UpdateTaskStatusCommentID(task.ID, 4242))

	got, err := db.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(4242), got.StatusCommentID)

	// A later state change rewrites the same field, not appends a new card.
	require.NoError(t, db.UpdateTaskStatusCommentID(task.ID, 4243))
	got, err = db.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(4243), got.StatusCommentID)
}

// TestUpdateTaskStatusCommentIDIsTaskScoped guards against a shared-card bug:
// updating one task must not touch another task's card.
func TestUpdateTaskStatusCommentIDIsTaskScoped(t *testing.T) {
	db := newTestDB(t)
	first := newTaskForStatusCard(t, db)
	second := newTaskForStatusCard(t, db)

	require.NoError(t, db.UpdateTaskStatusCommentID(first.ID, 111))

	gotSecond, err := db.GetTask(second.ID)
	require.NoError(t, err)
	assert.Zero(t, gotSecond.StatusCommentID, "second task must keep its own card id")
}

// TestTaskStatusCommentIDSurvivesReopen proves the column is really persisted
// (not just an in-memory default): a restart must still find the card so it
// PATCHes the existing one instead of posting a duplicate.
func TestTaskStatusCommentIDSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status-card.db")

	db, err := Open(path)
	require.NoError(t, err)
	task := newTaskForStatusCard(t, db)
	require.NoError(t, db.UpdateTaskStatusCommentID(task.ID, 999))
	require.NoError(t, db.Close())

	reopened, err := Open(path)
	require.NoError(t, err)
	defer reopened.Close()

	got, err := reopened.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(999), got.StatusCommentID)
}
