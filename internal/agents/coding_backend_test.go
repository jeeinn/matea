package agents

import (
	"context"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface compliance check.
var _ CodingBackend = (*BuiltinCodingBackend)(nil)

// mockLLMProvider is a minimal llm.Provider used by BuiltinCodingBackend tests.
// It returns a fixed assistant message with no tool calls, so AgentLoop.Run
// terminates after a single iteration.
type mockLLMProvider struct {
	content string
	usage   llm.Usage
}

func (m *mockLLMProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{
		Content:      m.content,
		FinishReason: "stop",
		Usage:        m.usage,
	}, nil
}

func newBuiltinTestFactory(t *testing.T, providerName string, provider llm.Provider) *RunnerFactory {
	t.Helper()
	registry := llm.NewRegistry(nil)
	if provider != nil {
		registry.Register(providerName, provider)
	}
	factory := NewRunnerFactory(registry, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	return factory
}

func TestBuiltinCodingBackendName(t *testing.T) {
	b := NewBuiltinCodingBackend(newBuiltinTestFactory(t, "mock", nil))
	assert.Equal(t, "builtin", b.Name())
}

func TestBuiltinCodingBackendAbort(t *testing.T) {
	b := NewBuiltinCodingBackend(newBuiltinTestFactory(t, "mock", nil))
	// Abort is a no-op for the builtin backend; must not error and must not
	// depend on the handle argument.
	err := b.Abort(context.Background(), "any-handle")
	require.NoError(t, err)
}

// TestBuiltinCodingBackendRunNoProvider verifies that Run surfaces a
// provider-not-found error from the registry rather than panicking.
func TestBuiltinCodingBackendRunNoProvider(t *testing.T) {
	factory := newBuiltinTestFactory(t, "mock", nil) // no provider registered under "missing"
	b := NewBuiltinCodingBackend(factory)

	sb := newMinimalSandbox(t)
	_, err := b.Run(context.Background(), CodingRequest{
		WorkDir:      sb.WorkDir,
		Sandbox:      sb,
		Task:         &store.Task{ID: 1, Repo: "owner/repo"},
		Agent:        &store.Agent{Provider: "missing", Model: "m"},
		Prompt:       "user prompt",
		SystemPrompt: "system prompt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
}

// TestBuiltinCodingBackendRunSuccess exercises the happy path end-to-end:
// a registered mock provider returns a non-empty content with no tool calls,
// AgentLoop.Run terminates after one iteration, and Run returns a CodingResult
// carrying the summary and the provider instance for reuse by finalize.
//
// No ModelMetaProvider is attached (meta=nil): the model-level supports_tools
// gate must not reject unregistered models; only an explicit SupportsTools=false
// meta blocks coder runs (see TestBuiltinCodingBackendRunModelNoTools).
func TestBuiltinCodingBackendRunSuccess(t *testing.T) {
	mock := &mockLLMProvider{
		content: "Implemented the requested change.",
		usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
	factory := newBuiltinTestFactory(t, "mock", mock)
	b := NewBuiltinCodingBackend(factory)

	sb := newMinimalSandbox(t)
	task := &store.Task{ID: 42, Repo: "owner/repo"}
	agent := &store.Agent{Provider: "mock", Model: "mock-model"}

	result, err := b.Run(context.Background(), CodingRequest{
		WorkDir:      sb.WorkDir,
		Sandbox:      sb,
		Task:         task,
		Agent:        agent,
		Prompt:       "Fix the bug described in the issue body.",
		SystemPrompt: "You are a senior software engineer.",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
	assert.Equal(t, "Implemented the requested change.", result.Summary)
	assert.Empty(t, result.RemoteSessionID, "builtin backend must not set a remote session id")
	assert.NotNil(t, result.Provider, "Provider must be returned for reuse by finalize")
}

func TestBuiltinCodingBackendRunModelNoTools(t *testing.T) {
	mock := &mockLLMProvider{content: "should not run"}
	factory := newBuiltinTestFactory(t, "deepseek", mock)
	factory.SetModelMetaProvider(&stubModelMeta{
		defs: map[string]*config.ModelDefinition{
			"deepseek/deepseek-reasoner": {
				ID:            "deepseek-reasoner",
				SupportsTools: false,
			},
		},
	})
	b := NewBuiltinCodingBackend(factory)

	sb := newMinimalSandbox(t)
	_, err := b.Run(context.Background(), CodingRequest{
		WorkDir:      sb.WorkDir,
		Sandbox:      sb,
		Task:         &store.Task{ID: 1, Repo: "owner/repo"},
		Agent:        &store.Agent{Provider: "deepseek", Model: "deepseek-reasoner"},
		Prompt:       "fix it",
		SystemPrompt: "You are a coder.",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "supports_tools=false")
}

// Sparse API-discovery meta (ID only, supports_tools zero-value false) must not
// block coder runs — same policy as meta=nil.
func TestBuiltinCodingBackendRunSparseUnknownAllows(t *testing.T) {
	mock := &mockLLMProvider{content: "done"}
	factory := newBuiltinTestFactory(t, "custom-gw", mock)
	factory.SetModelMetaProvider(&stubModelMeta{
		defs: map[string]*config.ModelDefinition{
			"custom-gw/vendor-mystery-v1": {
				ID:            "vendor-mystery-v1",
				Name:          "vendor-mystery-v1",
				SupportsTools: false, // zero-value shaped; not in builtin
			},
		},
	})
	b := NewBuiltinCodingBackend(factory)

	sb := newMinimalSandbox(t)
	result, err := b.Run(context.Background(), CodingRequest{
		WorkDir:      sb.WorkDir,
		Sandbox:      sb,
		Task:         &store.Task{ID: 2, Repo: "owner/repo"},
		Agent:        &store.Agent{Provider: "custom-gw", Model: "vendor-mystery-v1"},
		Prompt:       "implement it",
		SystemPrompt: "You are a coder.",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Success)
}

// newMinimalSandbox builds a throwaway sandbox with a tiny initial commit so
// that LoadCodeContext / DefaultTools have a valid working tree to reference.
// The sandbox is cleaned up automatically via t.Cleanup.
func newMinimalSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	cfg := sandbox.SandboxConfig{
		Mode:           sandbox.ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      4096,
		MaxFileSize:    4096,
	}
	s := sandbox.New(cfg, 9901)
	require.NoError(t, s.Setup())
	t.Cleanup(func() { s.Cleanup() })

	// Initialise an empty git repo so DefaultTools has a valid work tree.
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	require.NoError(t, s.WriteFile("README.md", []byte("hello")))
	git := sandbox.NewGit(s)
	require.NoError(t, git.Add().Error)
	require.NoError(t, git.Commit("initial").Error)
	return s
}
