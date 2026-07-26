package dispatcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// fakeGiteaFactory points all clients (agent + admin) at a configurable
// httptest.Server so tests can drive IssueComment success/failure.
type fakeGiteaFactory struct {
	serverURL string
}

func (f *fakeGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient(f.serverURL, token)
}

func (f *fakeGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	return gitea.NewClient(f.serverURL, "admin-token")
}

func TestWritebackTargetID(t *testing.T) {
	t.Run("review_pr uses PRID when issue missing", func(t *testing.T) {
		id, ok := writebackTargetID(&store.Task{TaskType: "review_pr", PRID: 3, IssueID: 0})
		if !ok || id != 3 {
			t.Fatalf("got id=%d ok=%v, want 3 true", id, ok)
		}
	})
	t.Run("review_pr prefers PRID over issue", func(t *testing.T) {
		id, ok := writebackTargetID(&store.Task{TaskType: "review_pr", PRID: 3, IssueID: 2})
		if !ok || id != 3 {
			t.Fatalf("got id=%d ok=%v, want 3 true", id, ok)
		}
	})
	t.Run("issue task uses issue ID", func(t *testing.T) {
		id, ok := writebackTargetID(&store.Task{TaskType: "solve_comment", IssueID: 2})
		if !ok || id != 2 {
			t.Fatalf("got id=%d ok=%v, want 2 true", id, ok)
		}
	})
	t.Run("pure PR solve_comment falls back to PRID", func(t *testing.T) {
		id, ok := writebackTargetID(&store.Task{TaskType: "solve_comment", IssueID: 0, PRID: 20})
		if !ok || id != 20 {
			t.Fatalf("got id=%d ok=%v, want 20 true", id, ok)
		}
	})
	t.Run("no target when both zero", func(t *testing.T) {
		_, ok := writebackTargetID(&store.Task{TaskType: "review_pr", IssueID: 0, PRID: 0})
		if ok {
			t.Fatal("expected no writeback target")
		}
	})
}

func TestFormatFailureComment(t *testing.T) {
	task := &store.Task{
		ID:       23,
		AgentID:  3,
		TaskType: "analyze_issue",
	}
	err := errors.New(`runner execution: LLM call: API error 404: {"error":{"message":"model is not found"}}`)

	body := formatFailureComment(task, err)

	if !strings.Contains(body, "任务执行失败") {
		t.Fatalf("missing failure title: %s", body)
	}
	if !strings.Contains(body, "model is not found") {
		t.Fatalf("missing error detail: %s", body)
	}
	if !strings.Contains(body, "Task ID: 23") {
		t.Fatalf("missing task metadata: %s", body)
	}
}

func TestHandleTaskPanicMarksTaskFailed(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := createTestAgent(t, db)
	task := &store.Task{
		AgentID:  agent.ID,
		TaskType: "analyze_issue",
		Status:   "running",
		Repo:     "owner/repo",
		IssueID:  1,
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	var failedTask *store.Task
	e := NewExecutor(1, 0, config.AgentConcurrencyParallel, nil, db, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.DefaultConfig(), config.DefaultMCPConfig())
	e.SetOnFailed(func(t *store.Task) {
		failedTask = t
	})

	e.handleTaskPanic(task, "slice bounds out of range [49:45]")

	updated, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("status=%q, want failed", updated.Status)
	}
	if !strings.Contains(updated.Error, "task panicked") {
		t.Fatalf("error=%q, want panic message", updated.Error)
	}
	if failedTask == nil || failedTask.ID != task.ID {
		t.Fatal("onFailed callback not invoked")
	}
}

// newWritebackExecutor builds an Executor wired with a fake Gitea factory
// pointing at the given httptest server. Callbacks record invocations via
// atomic counters so the test can assert which path fired.
func newWritebackExecutor(t *testing.T, serverURL string) (*Executor, *store.Task, *int32, *int32) {
	t.Helper()
	db, cleanup := setupTestDB(t)
	t.Cleanup(cleanup)

	agent := createTestAgent(t, db)
	task := &store.Task{
		AgentID:  agent.ID,
		TaskType: "analyze_issue",
		Status:   "running",
		Repo:     "owner/repo",
		IssueID:  1,
		Result:   "mock LLM result body",
	}
	if err := db.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	var completeCalls, failedCalls int32
	e := NewExecutor(1, 0, config.AgentConcurrencyParallel, nil, db, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.DefaultConfig(), config.DefaultMCPConfig())
	e.SetGiteaClientFactory(&fakeGiteaFactory{serverURL: serverURL}, func() config.DebugConfig { return config.DebugConfig{} }, nil)
	e.SetOnComplete(func(*store.Task) { atomic.AddInt32(&completeCalls, 1) })
	e.SetOnFailed(func(*store.Task) { atomic.AddInt32(&failedCalls, 1) })
	return e, task, &completeCalls, &failedCalls
}

func TestFinalizeTaskResultWritebackFailureMarksPartial(t *testing.T) {
	// Gitea returns 500 for every comment POST — both agent-token writeback
	// and admin-token partial-failure notice will fail.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gitea unavailable", http.StatusInternalServerError)
	}))
	defer server.Close()

	e, task, completeCalls, failedCalls := newWritebackExecutor(t, server.URL)

	e.finalizeTaskResult(task, nil)

	if task.Status != store.StatusPartial {
		t.Fatalf("status=%q, want partial", task.Status)
	}
	if !strings.Contains(task.Error, "writeback failed") {
		t.Fatalf("task.Error=%q, want 'writeback failed' substring", task.Error)
	}
	if task.Result == "" {
		t.Fatal("task.Result should be preserved on partial for human inspection")
	}
	if atomic.LoadInt32(completeCalls) != 0 {
		t.Fatal("onComplete must NOT fire on partial (workflow must not advance)")
	}
	if atomic.LoadInt32(failedCalls) != 1 {
		t.Fatal("onFailed must fire on partial so lock is released / workflow rolls back")
	}

	// Verify DB persisted partial + result + error.
	got, err := e.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != store.StatusPartial {
		t.Fatalf("db status=%q, want partial", got.Status)
	}
	if got.Result != task.Result {
		t.Fatalf("db result=%q, want %q", got.Result, task.Result)
	}
	if !strings.Contains(got.Error, "writeback failed") {
		t.Fatalf("db error=%q, want 'writeback failed' substring", got.Error)
	}
}

func TestFinalizeTaskResultWritebackSuccessMarksSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer server.Close()

	e, task, completeCalls, failedCalls := newWritebackExecutor(t, server.URL)

	e.finalizeTaskResult(task, nil)

	if task.Status != store.StatusSuccess {
		t.Fatalf("status=%q, want success", task.Status)
	}
	if task.Error != "" {
		t.Fatalf("task.Error=%q, want empty", task.Error)
	}
	if atomic.LoadInt32(completeCalls) != 1 {
		t.Fatal("onComplete must fire on success")
	}
	if atomic.LoadInt32(failedCalls) != 0 {
		t.Fatal("onFailed must NOT fire on success")
	}

	got, err := e.db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != store.StatusSuccess {
		t.Fatalf("db status=%q, want success", got.Status)
	}
}

func TestFinalizeTaskResultRunnerFailureMarksFailed(t *testing.T) {
	// Server returns success — writeback would succeed, but runErr is non-nil
	// so the failure path must take precedence (no writeback attempted).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":"1"}`))
	}))
	defer server.Close()

	e, task, completeCalls, failedCalls := newWritebackExecutor(t, server.URL)

	runErr := errors.New("runner execution: LLM call: API error 500")
	e.finalizeTaskResult(task, runErr)

	if task.Status != store.StatusFailed {
		t.Fatalf("status=%q, want failed", task.Status)
	}
	if !strings.Contains(task.Error, "LLM call") {
		t.Fatalf("task.Error=%q, want runner error", task.Error)
	}
	if atomic.LoadInt32(completeCalls) != 0 {
		t.Fatal("onComplete must NOT fire on runner failure")
	}
	if atomic.LoadInt32(failedCalls) != 1 {
		t.Fatal("onFailed must fire on runner failure")
	}
}

func TestFormatPartialFailureComment(t *testing.T) {
	task := &store.Task{
		ID:       77,
		AgentID:  4,
		TaskType: "solve_issue",
	}
	wbErr := errors.New("post comment: API error 401: token expired")

	body := formatPartialFailureComment(task, wbErr)

	if !strings.Contains(body, "任务已执行但写回失败") {
		t.Fatalf("missing partial-failure title: %s", body)
	}
	if !strings.Contains(body, "token expired") {
		t.Fatalf("missing writeback error detail: %s", body)
	}
	if !strings.Contains(body, "Task ID: 77") {
		t.Fatalf("missing task metadata: %s", body)
	}
	if !strings.Contains(body, "Type: solve_issue") {
		t.Fatalf("missing task type: %s", body)
	}
}

func TestExecutorCancelByIssue(t *testing.T) {
	e := NewExecutor(1, 0, config.AgentConcurrencyParallel, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.SandboxConfig{}, config.MCPConfig{})
	defer e.Shutdown()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	e.registerRunning(&store.Task{ID: 1, Repo: "o/r", IssueID: 4}, cancel1)
	e.registerRunning(&store.Task{ID: 2, Repo: "o/r", IssueID: 5}, cancel2)

	if n := e.CancelByIssue("o/r", 4); n != 1 {
		t.Fatalf("CancelByIssue count=%d, want 1", n)
	}
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("expected task 1 context cancelled")
	}
	select {
	case <-ctx2.Done():
		t.Fatal("task 2 should still be running")
	default:
	}

	if !e.unregisterRunning(1) {
		t.Fatal("task 1 should be marked external")
	}
	if e.unregisterRunning(2) {
		t.Fatal("task 2 should not be external")
	}
}

func TestSerialQueueAgentSlot(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	agent := createTestAgent(t, db)

	e := NewExecutor(2, 0, config.AgentConcurrencySerialQueue, nil, db, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.DefaultConfig(), config.DefaultMCPConfig())
	defer e.Shutdown()

	t1 := &store.Task{ID: 101, AgentID: agent.ID}
	t2 := &store.Task{ID: 102, AgentID: agent.ID}

	if !e.tryAcquireAgentSlot(t1) {
		t.Fatal("first acquire should succeed")
	}
	if e.tryAcquireAgentSlot(t2) {
		t.Fatal("second acquire for same agent should defer")
	}
	e.releaseAgentSlot(agent.ID)
	if !e.tryAcquireAgentSlot(t2) {
		t.Fatal("after release, acquire should succeed")
	}
}

func TestSerialQueueDefersWhenDBRunning(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()
	agent := createTestAgent(t, db)

	running := &store.Task{
		AgentID: agent.ID, TaskType: "solve_issue", Status: store.StatusRunning,
		Repo: "o/r", IssueID: 1, DeliveryID: "serial-run",
	}
	if err := db.CreateTask(running); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	e := NewExecutor(2, 0, config.AgentConcurrencySerialQueue, nil, db, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.DefaultConfig(), config.DefaultMCPConfig())
	defer e.Shutdown()

	pending := &store.Task{ID: 999, AgentID: agent.ID}
	if e.tryAcquireAgentSlot(pending) {
		t.Fatal("should defer while another task is running in DB")
	}
}

func TestParallelIgnoresAgentSlot(t *testing.T) {
	e := NewExecutor(2, 0, config.AgentConcurrencyParallel, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), sandbox.SandboxConfig{}, config.MCPConfig{})
	defer e.Shutdown()

	t1 := &store.Task{ID: 1, AgentID: 7}
	t2 := &store.Task{ID: 2, AgentID: 7}
	if !e.tryAcquireAgentSlot(t1) || !e.tryAcquireAgentSlot(t2) {
		t.Fatal("parallel mode must not serialize by agent")
	}
}
