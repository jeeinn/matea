package dispatcher

import (
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReattachHubHandlesEnqueuesOrphanedRunning verifies the restart-recovery
// path: an orphaned (running) hub task is reset to pending and enqueued, a
// pending hub task is left to LoadPending (no double-enqueue), and a non-hub
// running task is ignored by ReattachHubHandles (FailOrphanedRunningTasksExceptHub
// fails it separately).
func TestReattachHubHandlesEnqueuesOrphanedRunning(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	agent := createTestAgent(t, db)

	// Orphaned running hub task: should be re-attached.
	hubTask := &store.Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: store.StatusRunning, Repo: "o/r", IssueID: 1}
	require.NoError(t, db.CreateTask(hubTask))
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID: hubTask.ID, Backend: "hub-opencode", RemoteID: "sess-1", IdempotencyKey: "k1", Status: store.HubHandleStatusRunning,
	}))

	// Non-hub running task: not a hub handle — ignored here (failed elsewhere).
	plainTask := &store.Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: store.StatusRunning, Repo: "o/r", IssueID: 2}
	require.NoError(t, db.CreateTask(plainTask))

	// Pending hub task: already loaded by LoadPending — skip to avoid double enqueue.
	pendingHub := &store.Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: store.StatusPending, Repo: "o/r", IssueID: 3}
	require.NoError(t, db.CreateTask(pendingHub))
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID: pendingHub.ID, Backend: "hub-opencode", RemoteID: "sess-3", IdempotencyKey: "k3", Status: store.HubHandleStatusRunning,
	}))

	queue := NewTaskQueue(db, 16)
	e := &Executor{db: db}
	e.ReattachHubHandles(queue)

	// The orphaned running hub task must now be pending.
	gotHub, err := db.GetTask(hubTask.ID)
	require.NoError(t, err)
	assert.Equal(t, store.StatusPending, gotHub.Status, "orphaned running hub task must be reset to pending")

	// Drain the channel: exactly the orphaned hub task should be enqueued.
	var received []int64
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case task := <-queue.Dequeue():
			received = append(received, task.ID)
		case <-timeout:
			break loop
		}
	}
	assert.ElementsMatch(t, []int64{hubTask.ID}, received, "only the orphaned running hub task should be enqueued")

	// The non-hub running task is untouched by ReattachHubHandles.
	gotPlain, _ := db.GetTask(plainTask.ID)
	assert.Equal(t, store.StatusRunning, gotPlain.Status)
}

// TestReattachHubHandlesReconcilesTerminalTask verifies that a terminal task
// with a stale non-terminal handle is reconciled (handle marked terminal) and
// not enqueued.
func TestReattachHubHandlesReconcilesTerminalTask(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	agent := createTestAgent(t, db)

	// Terminal (success) task whose handle was never marked done.
	doneTask := &store.Task{AgentID: agent.ID, TaskType: "analyze_issue", Status: store.StatusSuccess, Repo: "o/r", IssueID: 1, Result: "x"}
	require.NoError(t, db.CreateTask(doneTask))
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID: doneTask.ID, Backend: "hub-opencode", RemoteID: "sess-9", IdempotencyKey: "k9", Status: store.HubHandleStatusRunning,
	}))

	queue := NewTaskQueue(db, 16)
	e := &Executor{db: db}
	e.ReattachHubHandles(queue)

	h, err := db.GetHubHandle(doneTask.ID)
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, store.HubHandleStatusDone, h.Status, "stale handle for a terminal task must be reconciled to done")

	// Nothing should be enqueued for a terminal task.
	select {
	case task := <-queue.Dequeue():
		t.Fatalf("unexpected task enqueued: %d", task.ID)
	case <-time.After(200 * time.Millisecond):
	}
}
