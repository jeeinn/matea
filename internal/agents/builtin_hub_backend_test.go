package agents

import (
	"context"
	"testing"

	"github.com/jeeinn/matea/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuiltinHubBackendInterface pins the basic HubBackend conformance:
// name, capabilities, and the always-healthy in-process probe.
func TestBuiltinHubBackendInterface(t *testing.T) {
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", nil))

	assert.Equal(t, "builtin", b.Name())

	caps := b.Capabilities()
	assert.True(t, caps.SupportsToolUse)
	assert.True(t, caps.SupportsMCPClient)
	assert.False(t, caps.SupportsMemory)
	assert.False(t, caps.SupportsSkillEvolution)
	assert.False(t, caps.HasIMChannels)
	assert.False(t, caps.HandlesGit, "Matea finalizes git/PR from the sandbox")

	assert.NoError(t, b.HealthCheck(context.Background()))
}

// TestBuiltinHubBackendSingleShot verifies the non-write path: a TaskContext
// with Matea-rendered prompts runs one completion and Poll reports StateDone.
func TestBuiltinHubBackendSingleShot(t *testing.T) {
	mock := &mockLLMProvider{content: "Analysis: the issue is well-specified."}
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", mock))

	h, err := b.Submit(context.Background(), &TaskContext{
		TaskType:     "analyze_issue",
		Role:         "analyze",
		Repo:         "owner/repo",
		IssueID:      3,
		Provider:     "mock",
		Model:        "mock-model",
		SystemPrompt: "You are an analyst.",
		UserPrompt:   "Analyze issue #3.",
	})
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "builtin", h.Backend)
	assert.NotEmpty(t, h.RemoteID)
	assert.Equal(t, "analyze_issue:owner/repo:3:0", h.IdempotencyKey)

	// Poll is repeatable and terminal.
	for i := 0; i < 2; i++ {
		result, state, err := b.Poll(context.Background(), h)
		require.NoError(t, err)
		assert.Equal(t, StateDone, state)
		require.NotNil(t, result)
		assert.Equal(t, "Analysis: the issue is well-specified.", result.Summary)
		assert.False(t, result.ExternallyHandled)
	}
}

// TestBuiltinHubBackendWriteTask verifies the write path: a solve_issue task
// with a prepared SandboxPath runs the AgentLoop through BuiltinCodingBackend.
func TestBuiltinHubBackendWriteTask(t *testing.T) {
	mock := &mockLLMProvider{content: "Implemented the change."}
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", mock))

	sb := newMinimalSandbox(t) // prepared git workspace; cleanup via t.Cleanup
	h, err := b.Submit(context.Background(), &TaskContext{
		TaskType:     "solve_issue",
		Role:         "coder",
		Repo:         "owner/repo",
		IssueID:      42,
		TaskID:       7,
		Provider:     "mock",
		Model:        "mock-model",
		SystemPrompt: "You are a senior software engineer.",
		UserPrompt:   "Fix the bug described in the issue body.",
		SandboxPath:  sb.WorkDir,
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	result, state, err := b.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, StateDone, state)
	require.NotNil(t, result)
	assert.Equal(t, "Implemented the change.", result.Summary)
}

// TestBuiltinHubBackendSubmitValidation pins submission-time errors:
// nil context, missing provider, and write tasks without a sandbox path all
// fail Submit with no Handle.
func TestBuiltinHubBackendSubmitValidation(t *testing.T) {
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", &mockLLMProvider{content: "x"}))

	h, err := b.Submit(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "nil TaskContext")

	h, err = b.Submit(context.Background(), &TaskContext{
		TaskType:   "analyze_issue",
		UserPrompt: "hi",
	})
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "Provider")

	h, err = b.Submit(context.Background(), &TaskContext{
		TaskType:   "solve_issue",
		Provider:   "mock",
		Model:      "mock-model",
		UserPrompt: "fix it",
	})
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "SandboxPath")

	h, err = b.Submit(context.Background(), &TaskContext{
		TaskType:   "analyze_issue",
		Provider:   "missing",
		Model:      "m",
		UserPrompt: "hi",
	})
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "provider")
}

// TestBuiltinHubBackendPollErrors pins Poll strictness: nil handles, handles
// owned by another backend, and unknown RemoteIDs are all errors.
func TestBuiltinHubBackendPollErrors(t *testing.T) {
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", &mockLLMProvider{content: "ok"}))

	_, _, err := b.Poll(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil Handle")

	_, _, err = b.Poll(context.Background(), &Handle{Backend: "hub-hermes", RemoteID: "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `backend "hub-hermes"`)

	_, _, err = b.Poll(context.Background(), &Handle{Backend: "builtin", RemoteID: "never-submitted"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown handle")
}

// TestBuiltinHubBackendCancelNoop pins Cancel as an idempotent no-op for the
// synchronous builtin backend (known, unknown, and nil handles alike).
func TestBuiltinHubBackendCancelNoop(t *testing.T) {
	b := NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", &mockLLMProvider{content: "done"}))

	h, err := b.Submit(context.Background(), &TaskContext{
		TaskType:   "reply_comment",
		Provider:   "mock",
		Model:      "mock-model",
		UserPrompt: "reply",
	})
	require.NoError(t, err)

	assert.NoError(t, b.Cancel(context.Background(), h))
	assert.NoError(t, b.Cancel(context.Background(), &Handle{Backend: "builtin", RemoteID: "ghost"}))
	assert.NoError(t, b.Cancel(context.Background(), nil))
}

// TestBuiltinHubBackendRegisteredInRegistry verifies the builtin backend
// participates in the HubBackendRegistry like any hub implementation.
func TestBuiltinHubBackendRegisteredInRegistry(t *testing.T) {
	reg := NewHubBackendRegistry()
	reg.Register(NewBuiltinHubBackend(newBuiltinTestFactory(t, "mock", &mockLLMProvider{content: "ok"})))

	got, err := reg.Lookup("builtin")
	require.NoError(t, err)
	assert.Equal(t, "builtin", got.Name())

	_, err = reg.Lookup("hub-openclaw")
	require.Error(t, err, "unimplemented hub backends must fail loudly")
}

var _ llm.Provider = (*mockLLMProvider)(nil)
