package agents

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHubRunTestDB creates a temporary SQLite database for runViaHub tests.
func newHubRunTestDB(t *testing.T) *store.DB {
	t.Helper()
	tmp, err := os.CreateTemp("", "hub-run-test-*.db")
	require.NoError(t, err)
	tmp.Close()
	db, err := store.Open(tmp.Name())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmp.Name())
	})
	return db
}

// testHubBackend is a controllable HubBackend for runViaHub tests. It records
// how many times Submit is called and returns a scripted Poll outcome.
type testHubBackend struct {
	name        string
	mu          sync.Mutex
	submitCalls int
	pollState   State
	pollRes     *BackendResult
	pollErr     error
}

func (b *testHubBackend) Name() string { return b.name }
func (b *testHubBackend) Submit(ctx context.Context, tc *TaskContext) (*Handle, error) {
	b.mu.Lock()
	b.submitCalls++
	b.mu.Unlock()
	return &Handle{Backend: b.name, RemoteID: fmt.Sprintf("remote-%d", tc.TaskID), IdempotencyKey: "k"}, nil
}
func (b *testHubBackend) Poll(ctx context.Context, h *Handle) (*BackendResult, State, error) {
	return b.pollRes, b.pollState, b.pollErr
}
func (b *testHubBackend) Cancel(ctx context.Context, h *Handle) error { return nil }
func (b *testHubBackend) Capabilities() HubCapabilities               { return HubCapabilities{} }
func (b *testHubBackend) HealthCheck(ctx context.Context) error       { return nil }

// TestRunViaHubPersistsHandleAndMarksTerminal verifies that the first execution
// submits once, persists the Handle, and marks it terminal on completion.
func TestRunViaHubPersistsHandleAndMarksTerminal(t *testing.T) {
	db := newHubRunTestDB(t)
	backend := &testHubBackend{name: "test-hub", pollState: StateDone, pollRes: &BackendResult{Summary: "ok"}}
	f := &RunnerFactory{db: db}

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 1, TaskType: "analyze_issue"}
	tc := &TaskContext{TaskType: "analyze_issue", Repo: "o/r", IssueID: 1, TaskID: 1, SandboxPath: t.TempDir()}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, backend, tc)
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Content)
	assert.Equal(t, 1, backend.submitCalls, "first execution should submit exactly once")

	h, err := db.GetHubHandle(1)
	require.NoError(t, err)
	require.NotNil(t, h, "handle must be persisted after Submit")
	assert.Equal(t, "test-hub", h.Backend)
	assert.Equal(t, "remote-1", h.RemoteID)
	assert.Equal(t, store.HubHandleStatusDone, h.Status, "terminal handle must be marked done")
}

// TestRunViaHubReattachesToPersistedHandle simulates a Matea restart: a prior
// run submitted and was interrupted with a non-terminal handle left in the DB.
// The resumed runViaHub must reuse that Handle (no re-Submit) and complete.
func TestRunViaHubReattachesToPersistedHandle(t *testing.T) {
	db := newHubRunTestDB(t)
	// Simulate the interrupted prior run: handle persisted as running, no terminal.
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID: 1, Backend: "test-hub", RemoteID: "remote-1", IdempotencyKey: "k", Status: store.HubHandleStatusRunning,
	}))

	backend := &testHubBackend{name: "test-hub", pollState: StateDone, pollRes: &BackendResult{Summary: "recovered"}}
	f := &RunnerFactory{db: db}

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 1, TaskType: "analyze_issue"}
	tc := &TaskContext{TaskType: "analyze_issue", Repo: "o/r", IssueID: 1, TaskID: 1, SandboxPath: t.TempDir()}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, backend, tc)
	require.NoError(t, err)
	assert.Equal(t, "recovered", res.Content)
	assert.Equal(t, 0, backend.submitCalls, "re-attach must reuse the persisted handle, never re-submit")
}

// TestRunViaHubReusesHandleOnFailure verifies that a remote failure reuses the
// persisted handle (no re-Submit) and marks it failed.
func TestRunViaHubReusesHandleOnFailure(t *testing.T) {
	db := newHubRunTestDB(t)
	require.NoError(t, db.SaveHubHandle(&store.HubHandle{
		TaskID: 1, Backend: "test-hub", RemoteID: "remote-1", IdempotencyKey: "k", Status: store.HubHandleStatusRunning,
	}))

	backend := &testHubBackend{name: "test-hub", pollState: StateFailed, pollErr: fmt.Errorf("boom")}
	f := &RunnerFactory{db: db}

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 1, TaskType: "analyze_issue"}
	tc := &TaskContext{TaskType: "analyze_issue", Repo: "o/r", IssueID: 1, TaskID: 1, SandboxPath: t.TempDir()}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, backend, tc)
	require.Error(t, err)
	assert.Equal(t, 0, backend.submitCalls, "failure path must not re-submit")
	h, _ := db.GetHubHandle(1)
	require.NotNil(t, h)
	assert.Equal(t, store.HubHandleStatusFailed, h.Status, "failed run must mark handle failed")
}

// TestRunViaHubNilDBNoPersistence verifies the in-memory-only behavior is
// preserved when no DB is configured (backward compatible for tests/embedded).
func TestRunViaHubNilDBNoPersistence(t *testing.T) {
	backend := &testHubBackend{name: "test-hub", pollState: StateDone, pollRes: &BackendResult{Summary: "ok"}}
	f := &RunnerFactory{db: nil}

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 1, TaskType: "analyze_issue"}
	tc := &TaskContext{TaskType: "analyze_issue", Repo: "o/r", IssueID: 1, TaskID: 1, SandboxPath: t.TempDir()}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, backend, tc)
	require.NoError(t, err)
	assert.Equal(t, "ok", res.Content)
	assert.Equal(t, 1, backend.submitCalls)
}

// TestRunViaHubAbortMarksHandleCanceled verifies E-1: when a hub run is aborted
// (executor cancel / poll safety timeout) before reaching a terminal state,
// abortHubRun marks the persisted Handle terminal (canceled). Without this, a
// Matea restart would re-attach and re-run the orphaned hub task. The backend
// here never reports a terminal state, so the poll loop drops into the
// pollCtx.Done() branch and calls abortHubRun.
func TestRunViaHubAbortMarksHandleCanceled(t *testing.T) {
	db := newHubRunTestDB(t)
	// Never-terminal poll outcome forces the loop into the cancel branch.
	backend := &testHubBackend{name: "test-hub", pollState: StateRunning, pollRes: nil, pollErr: nil}
	f := &RunnerFactory{db: db}

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 1, TaskType: "analyze_issue"}
	tc := &TaskContext{TaskType: "analyze_issue", Repo: "o/r", IssueID: 1, TaskID: 1, SandboxPath: t.TempDir()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate an executor-side abort before polling begins
	_, err := f.runViaHub(ctx, task, &store.Agent{}, backend, tc)
	require.Error(t, err, "aborted run must return an error")

	h, gerr := db.GetHubHandle(1)
	require.NoError(t, gerr)
	require.NotNil(t, h, "handle must remain persisted after abort")
	assert.Equal(t, store.HubHandleStatusCanceled, h.Status, "aborted run must mark handle canceled (E-1)")
}
