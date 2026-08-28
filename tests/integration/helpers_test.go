package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/api"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/dispatcher"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"

	_ "modernc.org/sqlite"
)

// TestEnv contains the test environment.
type TestEnv struct {
	DB           *store.DB
	Config       *config.Config
	Dispatcher   *dispatcher.Dispatcher
	Mux          *http.ServeMux
	Server       *httptest.Server
	GiteaMock    *httptest.Server
	HubMock      *MockHub // generic hub backend mock (task 1.2.7)
	CleanupFuncs []func()
}

// NewTestEnv creates a new test environment.
func NewTestEnv(t *testing.T) *TestEnv {
	t.Helper()

	// Create temp database
	tmpDB, err := os.CreateTemp("", "test-*.db")
	require.NoError(t, err)
	tmpDB.Close()

	db, err := store.Open(tmpDB.Name())
	require.NoError(t, err)

	// Create mock Gitea server
	giteaMock := newGiteaMock()

	// Create config
	cfg := &config.Config{
		Gitea: config.GiteaConfig{
			URL:        giteaMock.URL,
			AdminToken: "test-admin-token",
		},
		Dispatcher: config.DispatcherConfig{
			MaxConcurrent:  2,
			TaskRetryCount: 0,
			QueueSize:      10,
		},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaultsConfig{
				Provider:        "mock",
				Model:           "mock-model",
				MaxOutputTokens: 1024,
				MaxInputTokens:  8192,
				Temperature:     0.3,
				Timeout:         "5m",
			},
		},
		API: config.APIConfig{
			AuthToken: "test-api-token",
		},
	}

	// Create LLM registry with mock provider
	llmRegistry := &llm.Registry{}
	llmRegistry.Register("mock", &mockLLMProvider{response: "Mock AI response"})

	sandboxCfg := parseSandboxConfig(&cfg.Sandbox)

	// Create dispatcher
	d := dispatcher.NewDispatcher(db, &cfg.Gitea, &cfg.Dispatcher, llmRegistry, &cfg.Agents, sandboxCfg, cfg.MCP)

	// Create API handler
	manager := agents.NewManager(db, &cfg.Gitea)
	apiHandler := api.NewHandler(db, manager, cfg, nil, nil, nil)
	apiHandler.SetIssueController(d)

	// Create mux
	mux := http.NewServeMux()
	apiHandler.RegisterRoutes(mux)

	// Create webhook handler
	webhookHandler := giteaingress.NewHandler(&cfg.Gitea, db.DB, d.HandleEvent)
	mux.Handle("POST /webhook/gitea", webhookHandler)

	// Create test server
	server := httptest.NewServer(mux)

	// Create mock hub (1.2.7): validates HubBackend testability and paves the
	// way for Phase 2 hub dispatch tests. Default scenario: normal, no auth.
	hubMock := NewMockHub(t)

	env := &TestEnv{
		DB:         db,
		Config:     cfg,
		Dispatcher: d,
		Mux:        mux,
		Server:     server,
		GiteaMock:  giteaMock,
		HubMock:    hubMock,
		CleanupFuncs: []func(){
			func() { db.Close() },
			func() { os.Remove(tmpDB.Name()) },
			func() { server.Close() },
			func() { giteaMock.Close() },
			func() { hubMock.Close() },
		},
	}

	return env
}

// Cleanup cleans up the test environment.
func (e *TestEnv) Cleanup() {
	for _, fn := range e.CleanupFuncs {
		fn()
	}
}

func parseSandboxConfig(cfg *config.SandboxConfig) sandbox.SandboxConfig {
	cmdTimeout, _ := time.ParseDuration(cfg.CommandTimeout)
	taskTimeout, _ := time.ParseDuration(cfg.TaskTimeout)
	cleanupAfter, _ := time.ParseDuration(cfg.CleanupAfter)

	return sandbox.SandboxConfig{
		Mode:           sandbox.SandboxMode(cfg.Mode),
		BaseDir:        cfg.BaseDir,
		CommandTimeout: cmdTimeout,
		TaskTimeout:    taskTimeout,
		MaxOutput:      cfg.MaxOutput,
		MaxFileSize:    cfg.MaxFileSize,
		CleanupAfter:   cleanupAfter,
	}
}

// APIURL returns the full API URL for the given path.
func (e *TestEnv) APIURL(path string) string {
	return e.Server.URL + path
}

// APIRequest makes an authenticated API request.
func (e *TestEnv) APIRequest(method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, e.APIURL(path), reqBody)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer test-api-token")
	req.Header.Set("Content-Type", "application/json")

	return http.DefaultClient.Do(req)
}

// WebhookURL returns the webhook endpoint URL.
func (e *TestEnv) WebhookURL() string {
	return e.Server.URL + "/webhook/gitea"
}

// SendWebhook sends a webhook event to the test server.
func (e *TestEnv) SendWebhook(event, deliveryID string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", e.WebhookURL(), bytes.NewReader(data))
	if err != nil {
		return err
	}

	req.Header.Set("X-Gitea-Event", event)
	req.Header.Set("X-Gitea-Delivery", deliveryID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// CreateTestAgent creates a test agent in the database.
func (e *TestEnv) CreateTestAgent(t *testing.T) *store.Agent {
	t.Helper()

	agent := &store.Agent{
		Name:            "test-agent",
		GiteaUsername:   "ai-agent",
		GiteaToken:      "test-gitea-token",
		Provider:        "mock",
		Model:           "mock-model",
		MaxOutputTokens: 1024,
		MaxInputTokens:  8192,
		Temperature:     0.3,
		SystemPrompt:    "You are a helpful AI assistant.",
		Status:          "active",
	}

	err := e.DB.CreateAgent(agent)
	require.NoError(t, err)

	return agent
}

// EnableWorkflowV2 wires up the v2 workflow components for the dispatcher.
func (e *TestEnv) EnableWorkflowV2(t *testing.T) *agents.Registry {
	t.Helper()

	registry := agents.NewRegistry()
	err := registry.LoadFromDB(e.DB)
	require.NoError(t, err)

	resolver := workflow.NewResolver(registry)
	wfMgr := workflow.NewWorkflowManager(e.DB)
	l1Gate := workflow.NewL1Gate(e.DB)
	sessionSvc := workflow.NewSessionService(e.DB, "")
	lifecycle := workflow.NewSessionLifecycle(e.DB, wfMgr, sessionSvc, nil, "")

	e.Dispatcher.SetWorkflowComponents(registry, resolver, wfMgr, l1Gate, sessionSvc, nil, lifecycle)
	return registry
}

// EnableWorkflowV2WithPolicy wires v2 components and sets an L2 workflow policy.
func (e *TestEnv) EnableWorkflowV2WithPolicy(t *testing.T, policy *workflow.WorkflowPolicy) *agents.Registry {
	t.Helper()
	registry := e.EnableWorkflowV2(t)
	e.Dispatcher.SetWorkflowPolicy(policy)
	return registry
}

// GiteaUnassignCall records a DELETE .../assignees request.
type GiteaUnassignCall struct {
	Path      string
	Assignees []string
}

// GiteaCommentCall records a POST .../comments request.
type GiteaCommentCall struct {
	Path string
	Body string
}

// RecordingGiteaMock is a Gitea API mock that records unassign/comment calls.
type RecordingGiteaMock struct {
	Server *httptest.Server

	mu           sync.Mutex
	unassigns    []GiteaUnassignCall
	comments     []GiteaCommentCall
	FailUnassign bool
}

// InstallRecordingGitea replaces the dispatcher Gitea base URL with a recording mock.
func (e *TestEnv) InstallRecordingGitea(t *testing.T) *RecordingGiteaMock {
	t.Helper()
	rec := newRecordingGiteaMock()
	e.GiteaMock = rec.Server
	e.Config.Gitea.URL = rec.Server.URL
	e.CleanupFuncs = append(e.CleanupFuncs, func() { rec.Server.Close() })
	return rec
}

func newRecordingGiteaMock() *RecordingGiteaMock {
	rec := &RecordingGiteaMock{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/assignees"):
			var body struct {
				Assignees []string `json:"assignees"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rec.mu.Lock()
			rec.unassigns = append(rec.unassigns, GiteaUnassignCall{
				Path:      r.URL.Path,
				Assignees: body.Assignees,
			})
			fail := rec.FailUnassign
			rec.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]string{"message": "unassign failed"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return

		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/comments"):
			var body struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rec.mu.Lock()
			rec.comments = append(rec.comments, GiteaCommentCall{Path: r.URL.Path, Body: body.Body})
			rec.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 1})
			return

		case r.URL.Path == "/api/v1/version":
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "1.26.0"})

		default:
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
	return rec
}

func (r *RecordingGiteaMock) UnassignCalls() []GiteaUnassignCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]GiteaUnassignCall, len(r.unassigns))
	copy(out, r.unassigns)
	return out
}

func (r *RecordingGiteaMock) CommentCalls() []GiteaCommentCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]GiteaCommentCall, len(r.comments))
	copy(out, r.comments)
	return out
}

// CreateTestAgentWithRole creates a test agent with a specific role.
func (e *TestEnv) CreateTestAgentWithRole(t *testing.T, name, username, role string) *store.Agent {
	t.Helper()

	agent := &store.Agent{
		Name:            name,
		GiteaUsername:   username,
		GiteaToken:      "test-gitea-token",
		Provider:        "mock",
		Model:           "mock-model",
		MaxOutputTokens: 1024,
		MaxInputTokens:  8192,
		Temperature:     0.3,
		SystemPrompt:    "You are a helpful AI assistant.",
		Role:            role,
		Status:          "active",
	}

	err := e.DB.CreateAgent(agent)
	require.NoError(t, err)

	return agent
}

// WaitForTask waits for a task to reach the given status.
func (e *TestEnv) WaitForTask(t *testing.T, taskID int64, status string, timeout time.Duration) *store.Task {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := e.DB.GetTask(taskID)
		if err == nil && task.Status == status {
			return task
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatalf("Task %d did not reach status '%s' within %v", taskID, status, timeout)
	return nil
}

// newGiteaMock creates a mock Gitea API server.
func newGiteaMock() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.URL.Path == "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "1.26.0"})

		case r.URL.Path == "/api/v1/repos/owner/repo" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             1,
				"name":           "repo",
				"full_name":      "owner/repo",
				"clone_url":      "http://localhost:3000/owner/repo.git",
				"default_branch": "main",
			})

		case r.URL.Path == "/api/v1/repos/owner/repo/pulls" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       1,
				"number":   1,
				"title":    "AI PR",
				"html_url": "http://localhost:3000/owner/repo/pulls/1",
			})

		case r.URL.Path == "/api/v1/repos/owner/repo/issues" && r.Method == "POST":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     1,
				"number": 1,
				"title":  "Comment",
			})

		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/api/v1/users/"):
			// Users do not exist by default in the mock — return 404 so
			// EnsureGiteaAccount takes the create-user branch (real Gitea
			// behavior; the previous fall-through 200 made GetUser always
			// report the user as existing).
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"message": "user not found"})

		default:
			json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		}
	}))
}

// mockLLMProvider is a mock LLM provider for testing.
type mockLLMProvider struct {
	response string
}

func (m *mockLLMProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: m.response,
		Usage: llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// parseJSON parses a JSON response body.
func parseJSON(resp *http.Response, v interface{}) error {
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}
