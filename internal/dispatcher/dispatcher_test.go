package dispatcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/workflow"
)

// mockLLMProvider returns a fixed response for testing.
type mockLLMProvider struct{}

func (m *mockLLMProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: "This is a mock AI response for testing.",
		Usage: llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// setupTestDB creates a temporary SQLite database for testing.
func setupTestDB(t *testing.T) (*store.DB, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "test-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	db, err := store.Open(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("Failed to open database: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.Remove(tmpFile.Name())
	}

	return db, cleanup
}

// createTestAgent creates a test agent in the database.
func createTestAgent(t *testing.T, db *store.DB) *store.Agent {
	t.Helper()

	agent := &store.Agent{
		Name:            "test-agent",
		GiteaUsername:   "ai-agent",
		GiteaToken:      "test-token",
		Provider:        "mock",
		Model:           "mock-model",
		MaxOutputTokens: 1024,
		MaxInputTokens:  8192,
		Temperature:     0.3,
		SystemPrompt:    "You are a helpful AI assistant.",
		Role:            store.RoleAnalyze,
		Status:          "active",
	}

	if err := db.CreateAgent(agent); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}

	return agent
}

func TestDispatcherHandleEvent(t *testing.T) {
	// Setup
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := createTestAgent(t, db)

	// Create mock Gitea server
	giteaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer giteaServer.Close()

	// Create dispatcher
	giteaCfg := &config.GiteaConfig{
		URL: giteaServer.URL,
	}
	dispatcherCfg := &config.DispatcherConfig{
		MaxConcurrent:  1,
		TaskRetryCount: 0,
		QueueSize:      10,
	}

	llmRegistry := &llm.Registry{}
	llmRegistry.Register("mock", &mockLLMProvider{})

	agentsCfg := &config.AgentsConfig{}
	sandboxCfg := sandbox.DefaultConfig()
	d := NewDispatcher(db, giteaCfg, dispatcherCfg, llmRegistry, agentsCfg, sandboxCfg, config.DefaultMCPConfig())

	// Wire v2 components
	registry := agents.NewRegistry()
	registry.Refresh(agent)
	resolver := workflow.NewResolver(registry)
	wfMgr := workflow.NewWorkflowManager(db)
	l1Gate := workflow.NewL1Gate(db)
	sessionSvc := workflow.NewSessionService(db, "")
	d.SetWorkflowComponents(registry, resolver, wfMgr, l1Gate, sessionSvc, nil, nil)

	// Create test event (v2 uses assignee field)
	evt := &giteaingress.WebhookEvent{
		DeliveryID: "test-delivery-001",
		Event:      "issues",
		Action:     "assigned",
		Repo: giteaingress.Repository{
			FullName: "admin/test-repo",
		},
		Issue: &giteaingress.Issue{
			Number: 1,
			Title:  "Test Issue",
			Body:   "This is a test issue",
			User:   giteaingress.User{Login: "admin"},
		},
		Assignee: &giteaingress.User{Login: "ai-agent"},
		Sender:   giteaingress.User{Login: "admin"},
	}

	// Test HandleEvent
	result := d.HandleEvent(evt)
	if !result {
		t.Error("HandleEvent returned false, expected true")
	}

	// Verify task was created
	tasks, err := db.ListPendingTasks()
	if err != nil {
		t.Fatalf("Failed to list tasks: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.AgentID != agent.ID {
		t.Errorf("Expected agent_id=%d, got %d", agent.ID, task.AgentID)
	}
	if task.Repo != "admin/test-repo" {
		t.Errorf("Expected repo=admin/test-repo, got %s", task.Repo)
	}
	if task.IssueID != 1 {
		t.Errorf("Expected issue_id=1, got %d", task.IssueID)
	}

	t.Logf("Task created successfully: id=%d, agent=%d, repo=%s",
		task.ID, task.AgentID, task.Repo)
}

func TestDispatcherDuplicateDelivery(t *testing.T) {
	// Setup
	db, cleanup := setupTestDB(t)
	defer cleanup()

	agent := createTestAgent(t, db)

	giteaCfg := &config.GiteaConfig{URL: "http://localhost:0"}
	dispatcherCfg := &config.DispatcherConfig{
		MaxConcurrent: 1,
		QueueSize:     10,
	}

	sandboxCfg := sandbox.DefaultConfig()
	d := NewDispatcher(db, giteaCfg, dispatcherCfg, nil, nil, sandboxCfg, config.DefaultMCPConfig())

	// Wire v2 components
	registry := agents.NewRegistry()
	registry.Refresh(agent)
	resolver := workflow.NewResolver(registry)
	d.SetWorkflowComponents(registry, resolver, nil, nil, nil, nil, nil)

	evt := &giteaingress.WebhookEvent{
		DeliveryID: "test-delivery-dup",
		Event:      "issues",
		Action:     "assigned",
		Repo:       giteaingress.Repository{FullName: "admin/test-repo"},
		Issue: &giteaingress.Issue{
			Number: 1,
			Title:  "Test",
			User:   giteaingress.User{Login: "admin"},
		},
		Assignee: &giteaingress.User{Login: "ai-agent"},
		Sender:   giteaingress.User{Login: "admin"},
	}

	// First call should succeed
	if !d.HandleEvent(evt) {
		t.Error("First HandleEvent should succeed")
	}

	// Second call with same delivery should fail (duplicate)
	if d.HandleEvent(evt) {
		t.Error("Second HandleEvent with same delivery should fail")
	}

	t.Logf("Duplicate delivery correctly rejected")
}

func TestPurePRCommentsUseDistinctEffectiveKeys(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	coder := &store.Agent{
		Name: "coder", GiteaUsername: "coder-ds", GiteaToken: "tok",
		Provider: "mock", Model: "m", Role: store.RoleCoder, Status: "active",
		MaxOutputTokens: 1024, MaxInputTokens: 8192, Temperature: 0.3,
	}
	if err := db.CreateAgent(coder); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	commented := make(map[int]int) // issue/PR number → comment posts
	giteaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// POST /api/v1/repos/{owner}/{repo}/issues/{n}/comments
		if r.Method == http.MethodPost {
			var n int
			if _, err := fmt.Sscanf(r.URL.Path, "/api/v1/repos/owner/repo/issues/%d/comments", &n); err == nil && n > 0 {
				commented[n]++
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}))
	defer giteaServer.Close()

	d := NewDispatcher(db, &config.GiteaConfig{URL: giteaServer.URL}, &config.DispatcherConfig{
		MaxConcurrent: 2, QueueSize: 10,
	}, nil, nil, sandbox.DefaultConfig(), config.DefaultMCPConfig())

	registry := agents.NewRegistry()
	registry.Refresh(coder)
	d.SetWorkflowComponents(
		registry,
		workflow.NewResolver(registry),
		workflow.NewWorkflowManager(db),
		workflow.NewL1Gate(db),
		workflow.NewSessionService(db, ""),
		nil, nil,
	)

	mkEvt := func(delivery string, prNum int) *giteaingress.WebhookEvent {
		return &giteaingress.WebhookEvent{
			DeliveryID: delivery,
			Event:      "pull_request_comment",
			Action:     "created",
			Repo:       giteaingress.Repository{FullName: "owner/repo"},
			PR:         &giteaingress.PullRequest{Number: prNum, Body: "no linked issue"},
			Comment:    &giteaingress.Comment{Body: "@coder-ds please continue", User: giteaingress.User{Login: "human"}},
			Sender:     giteaingress.User{Login: "human"},
		}
	}

	if !d.HandleEvent(mkEvt("pure-pr-20", 20)) {
		t.Fatal("HandleEvent PR#20 failed")
	}
	if !d.HandleEvent(mkEvt("pure-pr-21", 21)) {
		t.Fatal("HandleEvent PR#21 should succeed (must not collide on issue_id=0)")
	}

	tasks, err := db.ListPendingTasks()
	if err != nil {
		t.Fatalf("ListPendingTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("expected 2 pending tasks, got %d", len(tasks))
	}

	byIssue := map[int]*store.Task{}
	for _, task := range tasks {
		byIssue[task.IssueID] = task
		if task.PRID != task.IssueID {
			t.Fatalf("pure PR task issue_id=%d pr_id=%d; want effective_key==pr_id", task.IssueID, task.PRID)
		}
	}
	if byIssue[20] == nil || byIssue[21] == nil {
		t.Fatalf("tasks keyed by PR numbers missing: %+v", byIssue)
	}

	s20, err := db.GetSessionByRepoIssueAgentRole("owner/repo", 20, coder.ID, store.RoleCoder)
	if err != nil {
		t.Fatalf("session 20: %v", err)
	}
	s21, err := db.GetSessionByRepoIssueAgentRole("owner/repo", 21, coder.ID, store.RoleCoder)
	if err != nil {
		t.Fatalf("session 21: %v", err)
	}
	if s20.ID == s21.ID {
		t.Fatal("PR#20 and PR#21 must not share a session")
	}
	if byIssue[20].SessionID != s20.ID || byIssue[21].SessionID != s21.ID {
		t.Fatalf("task session mismatch: t20=%s s20=%s t21=%s s21=%s",
			byIssue[20].SessionID, s20.ID, byIssue[21].SessionID, s21.ID)
	}

	// Progress comments must land on each PR (not silent no-op on issue_id=0).
	if commented[20] == 0 || commented[21] == 0 {
		t.Fatalf("expected gate comments on PR 20 and 21, got %v", commented)
	}

	id, ok := writebackTargetID(byIssue[20])
	if !ok || id != 20 {
		t.Fatalf("writebackTargetID for pure PR#20 = %d ok=%v", id, ok)
	}
}

func TestTaskQueuePersistence(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create an agent first (foreign key constraint)
	agent := createTestAgent(t, db)

	queue := NewTaskQueue(db, 10)

	// Enqueue a task
	task := &store.Task{
		Event:    "issues",
		Repo:     "test/repo",
		IssueID:  1,
		AgentID:  agent.ID,
		TaskType: "test",
		Context:  "test context",
		Status:   "pending",
	}

	if err := queue.Enqueue(task); err != nil {
		t.Fatalf("Failed to enqueue task: %v", err)
	}

	// Verify task was persisted
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM tasks WHERE status='pending'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count tasks: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 pending task, got %d", count)
	}

	// Test LoadPending
	queue2 := NewTaskQueue(db, 10)
	if err := queue2.LoadPending(); err != nil {
		t.Fatalf("Failed to load pending tasks: %v", err)
	}

	// Verify task is in the channel
	select {
	case loadedTask := <-queue2.Dequeue():
		if loadedTask.ID != task.ID {
			t.Errorf("Loaded task ID %d doesn't match original %d", loadedTask.ID, task.ID)
		}
	case <-time.After(time.Second):
		t.Error("Timeout waiting for task from queue")
	}

	t.Logf("Task persistence and recovery working correctly")
}
