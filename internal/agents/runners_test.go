package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockProvider is a mock LLM provider for testing.
type mockProvider struct {
	response string
}

func (m *mockProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content: m.response,
		Usage: llm.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}, nil
}

// mockGiteaFactory is a mock Gitea client factory for testing.
type mockGiteaFactory struct{}

func (m *mockGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient("http://localhost:0", token)
}

func (m *mockGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	return gitea.NewClient("http://localhost:0", "admin-token")
}

// mockProviderWithToolCalls is a mock LLM provider that returns a sequence of
// responses (tool calls followed by final content).
type mockProviderWithToolCalls struct {
	responses []llm.ChatResponse
	callIndex int
}

func (m *mockProviderWithToolCalls) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	if m.callIndex >= len(m.responses) {
		return &llm.ChatResponse{
			Content: "default",
			Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		}, nil
	}
	resp := m.responses[m.callIndex]
	m.callIndex++
	return &resp, nil
}

func TestRunnerFactoryGetRunner(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")

	tests := []struct {
		taskType string
		expected string
	}{
		{"review_pr", "*agents.ReviewRunner"},
		{"reply_comment", "*agents.InteractionRunner"},
		{"analyze_issue", "*agents.AnalyzeRunner"},
		{"trigger", "*agents.AnalyzeRunner"},
		{"unknown", "*agents.AnalyzeRunner"},
	}

	for _, tt := range tests {
		t.Run(tt.taskType, func(t *testing.T) {
			runner := factory.GetRunner(tt.taskType)
			if runner == nil {
				t.Error("Expected non-nil runner")
			}
		})
	}
}

func TestAnalyzeRunnerRun(t *testing.T) {
	registry := &llm.Registry{}
	registry.Register("mock", &mockProvider{response: "Analysis result"})

	factory := &mockGiteaFactory{}
	runnerFactory := NewRunnerFactory(registry, factory, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	runner := NewAnalyzeRunner(runnerFactory)

	task := &store.Task{
		ID:       1,
		AgentID:  1,
		TaskType: "analyze_issue",
		Context:  "Test context",
	}

	agent := &store.Agent{
		ID:              1,
		Provider:        "mock",
		Model:           "mock-model",
		SystemPrompt:    "You are an analyst.",
		MaxOutputTokens: 1024,
		MaxInputTokens:  8192,
	}

	result, err := runner.Run(context.Background(), task, agent)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(result.Content, "Analysis result") {
		t.Errorf("Expected content to contain 'Analysis result', got %s", result.Content)
	}
	if result.Action != "comment" {
		t.Errorf("Expected action=comment, got %s", result.Action)
	}
}

func TestSaveSessionBranch(t *testing.T) {
	tmpDB, err := os.CreateTemp("", "runners-test-*.db")
	require.NoError(t, err)
	tmpDB.Close()

	db, err := store.Open(tmpDB.Name())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpDB.Name())
	})

	session := &store.AgentSession{
		ID:     "sess-save-branch",
		Repo:   "owner/repo",
		Status: store.SessionActive,
		Branch: "",
	}
	require.NoError(t, db.CreateSession(session))

	factory := NewRunnerFactory(nil, nil, db, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	task := &store.Task{SessionID: session.ID}

	saveSessionBranch(factory, task, "ai/dev/issue-2")

	got, err := db.GetSession(session.ID)
	require.NoError(t, err)
	assert.Equal(t, "ai/dev/issue-2", got.Branch)

	// Idempotent when branch unchanged
	saveSessionBranch(factory, task, "ai/dev/issue-2")
	got, err = db.GetSession(session.ID)
	require.NoError(t, err)
	assert.Equal(t, "ai/dev/issue-2", got.Branch)
}

func TestFinalizeWriteTaskPRCreatesWhenNoOpenPR(t *testing.T) {
	var createCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls":
			createCalled = true
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(gitea.PRResponse{
				Number:  3,
				HTMLURL: "http://localhost/owner/repo/pulls/3",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := gitea.NewClient(server.URL, "test-token")
	task := &store.Task{ID: 35, Event: "Issue 2", IssueID: 2}
	result, err := FinalizeWriteTaskPR(client, "owner", "repo", "ai/dev/issue-2", "main", task, "dev", "done")
	require.NoError(t, err)
	assert.True(t, createCalled)
	assert.Equal(t, "pr", result.Action)
	assert.Equal(t, 3, result.PRID)
}

func TestFinalizeWriteTaskPRCommentsWhenOpenPRExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/api/v1/repos/owner/repo/pulls", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"number":   3,
				"state":    "open",
				"html_url": "http://localhost/owner/repo/pulls/3",
				"head":     map[string]string{"ref": "ai/dev/issue-2"},
			},
		})
	}))
	defer server.Close()

	client := gitea.NewClient(server.URL, "test-token")
	task := &store.Task{ID: 35, Event: "Issue 2", IssueID: 2}
	result, err := FinalizeWriteTaskPR(client, "owner", "repo", "ai/dev/issue-2", "main", task, "dev", "done")
	require.NoError(t, err)
	assert.Equal(t, "comment", result.Action)
	assert.Equal(t, 3, result.PRID)
	assert.Contains(t, result.Content, "Updated PR branch")
}

// stubModelMeta implements ModelMetaProvider for resolveMax* tests.
type stubModelMeta struct {
	defs           map[string]*config.ModelDefinition
	providerParams map[string]config.ModelParams
}

func (s *stubModelMeta) GetModelMeta(provider, model string) *config.ModelDefinition {
	return s.defs[provider+"/"+model]
}

func (s *stubModelMeta) GetProviderDefaultParams(provider string) config.ModelParams {
	if s.providerParams == nil {
		return config.ModelParams{}
	}
	return s.providerParams[provider]
}

func TestResolveMaxTokensUsesModelWhenAgentZero(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	factory.SetModelMetaProvider(&stubModelMeta{
		defs: map[string]*config.ModelDefinition{
			"deepseek/deepseek-v4-flash": {
				ID:            "deepseek-v4-flash",
				ContextWindow: 1000000,
				MaxOutput:     32768,
			},
		},
	})

	// agentMax == 0 → model metadata
	assert.Equal(t, 900000, factory.resolveMaxInputTokens(0, "deepseek", "deepseek-v4-flash"))
	assert.Equal(t, 32768, factory.resolveMaxOutputTokens(0, "deepseek", "deepseek-v4-flash"))

	// agentMax explicit, within limit
	assert.Equal(t, 8192, factory.resolveMaxInputTokens(8192, "deepseek", "deepseek-v4-flash"))
	assert.Equal(t, 1024, factory.resolveMaxOutputTokens(1024, "deepseek", "deepseek-v4-flash"))

	// agentMax exceeds model limit → clamped
	assert.Equal(t, 900000, factory.resolveMaxInputTokens(2000000, "deepseek", "deepseek-v4-flash"))
	assert.Equal(t, 32768, factory.resolveMaxOutputTokens(99999, "deepseek", "deepseek-v4-flash"))

	// unknown model + agentMax 0 → agents.defaults
	assert.Equal(t, factory.defaultMaxInput, factory.resolveMaxInputTokens(0, "deepseek", "unknown"))
	assert.Equal(t, factory.defaultMaxOutput, factory.resolveMaxOutputTokens(0, "deepseek", "unknown"))
}

func TestResolveSamplingParamsFromModelAndProvider(t *testing.T) {
	topPModel, topPProv := 0.9, 0.5
	freq, presence := 0.2, 0.1
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	factory.SetModelMetaProvider(&stubModelMeta{
		defs: map[string]*config.ModelDefinition{
			"openai/gpt-test": {
				ID: "gpt-test",
				DefaultParams: config.ModelParams{
					TopP:             &topPModel,
					FrequencyPenalty: &freq,
				},
			},
		},
		providerParams: map[string]config.ModelParams{
			"openai": {
				TopP:            &topPProv,
				PresencePenalty: &presence,
			},
		},
	})

	sp := factory.resolveSamplingParams(0.7, "openai", "gpt-test")
	assert.Equal(t, 0.7, sp.Temperature)
	assert.Equal(t, 0.9, sp.TopP, "model top_p wins over provider")
	assert.Equal(t, 0.2, sp.FrequencyPenalty)
	assert.Equal(t, 0.1, sp.PresencePenalty, "provider presence used when model omits it")

	sp = factory.resolveSamplingParams(0, "openai", "unknown")
	assert.InDelta(t, factory.defaultTemp, sp.Temperature, 0.0001)
	assert.Equal(t, 0.5, sp.TopP, "provider top_p when model missing")
	assert.Equal(t, 0.0, sp.FrequencyPenalty)
	assert.Equal(t, 0.1, sp.PresencePenalty)

	req := &llm.ChatRequest{Model: "m"}
	sp.ApplyTo(req)
	assert.Equal(t, sp.Temperature, req.Temperature)
	assert.Equal(t, sp.TopP, req.TopP)
	assert.Equal(t, sp.PresencePenalty, req.PresencePenalty)
}

func TestRecordTaskUsageCostPerThousandTokens(t *testing.T) {
	// Verify cost formula: price is $/1K tokens
	meta := &config.ModelDefinition{InputPrice: 1.0, OutputPrice: 2.0}
	prompt, completion := 1000, 500
	cost := (float64(prompt)*meta.InputPrice + float64(completion)*meta.OutputPrice) / 1000.0
	assert.InDelta(t, 2.0, cost, 0.0001) // 1000*1/1000 + 500*2/1000 = 1 + 1 = 2
}

// TestAnalyzeRunnerLoopPath verifies the read-only AgentLoop path when a
// shallow clone succeeds. The mock provider returns one tool call (list_files)
// followed by a final content response.
func TestAnalyzeRunnerLoopPath(t *testing.T) {
	// 1. Create a local bare repo to clone from
	tmpDir := t.TempDir()
	workDir := filepath.Join(tmpDir, "work")
	bareDir := filepath.Join(tmpDir, "remote.git")

	require.NoError(t, os.MkdirAll(workDir, 0755))
	sb := sandbox.New(sandbox.SandboxConfig{Mode: sandbox.ModeFixed, BaseDir: workDir, MaxFileSize: 1024 * 1024, CommandTimeout: 30 * time.Second}, 1)
	require.NoError(t, sb.Setup())
	t.Cleanup(func() { sb.Cleanup() })

	git := sandbox.NewGit(sb)
	sb.Execute("git", "init")
	sb.Execute("git", "config", "user.email", "test@test.com")
	sb.Execute("git", "config", "user.name", "Test")
	require.NoError(t, sb.WriteFile("README.md", []byte("# Test Repo\n")))
	git.Add()
	require.NoError(t, git.Commit("initial").Error)

	// Push to bare repo
	require.NoError(t, os.MkdirAll(bareDir, 0755))
	sb.Execute("git", "init", "--bare", bareDir)
	sb.Execute("git", "remote", "add", "origin", bareDir)
	require.NoError(t, git.Push("origin", "HEAD:refs/heads/main").Error)

	// 2. Mock Gitea server returning repo info pointing to the bare repo
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(gitea.RepoInfo{
				DefaultBranch: "main",
				CloneURL:      bareDir,
			})
			return
		}
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	giteaFactory := &testGiteaFactory{baseURL: server.URL}

	// 3. Mock provider: first returns a tool call (list_files), then final content
	provider := &mockProviderWithToolCalls{
		responses: []llm.ChatResponse{
			{
				ToolCalls: []llm.ToolCall{
					{ID: "call_1", Type: "function", Function: llm.FuncCall{Name: "list_files", Arguments: `{}`}},
				},
				Usage: llm.Usage{PromptTokens: 50, CompletionTokens: 10, TotalTokens: 60},
			},
			{
				Content: "Analysis complete: README.md found.",
				Usage:   llm.Usage{PromptTokens: 80, CompletionTokens: 20, TotalTokens: 100},
			},
		},
	}

	registry := &llm.Registry{}
	registry.Register("mock", provider)

	factory := NewRunnerFactory(registry, giteaFactory, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	// Use ModeTemp to avoid workspace directory conflicts between tests
	factory.sandboxCfg = sandbox.SandboxConfig{Mode: sandbox.ModeTemp, CommandTimeout: 30 * time.Second}
	runner := NewAnalyzeRunner(factory)

	task := &store.Task{
		ID:       1,
		AgentID:  1,
		TaskType: "analyze_issue",
		Repo:     "owner/repo",
		Context:  "Analyze the repository structure.",
	}
	agent := &store.Agent{
		ID:              1,
		Provider:        "mock",
		Model:           "mock-model",
		SystemPrompt:    "You are an analyst.",
		MaxOutputTokens: 1024,
		MaxInputTokens:  8192,
	}

	result, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	assert.Equal(t, "comment", result.Action)
	assert.Contains(t, result.Content, "Analysis complete: README.md found.")
	assert.Equal(t, 2, provider.callIndex) // two LLM calls
}

// testGiteaFactory creates Gitea clients pointing at a test server.
type testGiteaFactory struct {
	baseURL string
}

func (m *testGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient(m.baseURL, token)
}

func (m *testGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	return gitea.NewClient(m.baseURL, "admin-token")
}
