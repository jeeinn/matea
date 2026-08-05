package integration

import (
	"context"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/agents"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHubTestContext returns a minimal TaskContext for hub submission tests.
func mockHubTestContext() *agents.TaskContext {
	return &agents.TaskContext{
		TaskType:   "analyze_issue",
		Role:       "analyze",
		Backend:    "hub-mock",
		Repo:       "owner/repo",
		IssueID:    9,
		UserPrompt: "Analyze issue #9",
	}
}

// TestTestEnvIncludesMockHub verifies the Mock Hub is wired into TestEnv and
// healthy under the default scenario (task 1.2.7 acceptance: TestEnv 增加
// Mock Hub，接口定完立刻验证可测性).
func TestTestEnvIncludesMockHub(t *testing.T) {
	env := NewTestEnv(t)
	defer env.Cleanup()

	require.NotNil(t, env.HubMock)
	client := newHTTPHubClient(env.HubMock.URL(), "", 2*time.Second)
	require.NoError(t, client.HealthCheck(context.Background()))
	assert.Equal(t, "hub-mock", client.Name())
	assert.True(t, client.Capabilities().SupportsToolUse)
}

// TestMockHubNormalTask covers the happy path: submit → handle → poll done.
func TestMockHubNormalTask(t *testing.T) {
	hub := NewMockHub(t)
	client := newHTTPHubClient(hub.URL(), "", 2*time.Second)

	h, err := client.Submit(context.Background(), mockHubTestContext())
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "hub-mock", h.Backend)
	assert.NotEmpty(t, h.RemoteID)
	assert.Equal(t, "analyze_issue:owner/repo:9:0", h.IdempotencyKey)

	result, state, err := client.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateDone, state)
	require.NotNil(t, result)
	assert.Equal(t, "mock hub completed: analyze_issue", result.Summary)
}

// TestMockHubTimeout covers the timeout scenario: the mock delays responses
// beyond the client timeout and Submit surfaces the failure.
func TestMockHubTimeout(t *testing.T) {
	hub := NewMockHub(t)
	hub.ResponseDelay = 300 * time.Millisecond
	client := newHTTPHubClient(hub.URL(), "", 50*time.Millisecond)

	_, err := client.Submit(context.Background(), mockHubTestContext())
	require.Error(t, err, "submit must fail when the hub exceeds the client timeout")
}

// TestMockHub502 covers the bad-gateway scenario on both task submission and
// health check.
func TestMockHub502(t *testing.T) {
	hub := NewMockHub(t)
	hub.Fail502 = true
	client := newHTTPHubClient(hub.URL(), "", 2*time.Second)

	_, err := client.Submit(context.Background(), mockHubTestContext())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")

	err = client.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

// TestMockHubAuthFailure covers the auth scenario: a token-protected hub
// rejects unauthenticated clients (401) and accepts the bearer token.
func TestMockHubAuthFailure(t *testing.T) {
	hub := NewMockHub(t)
	hub.Token = "secret-token"

	noAuth := newHTTPHubClient(hub.URL(), "", 2*time.Second)
	_, err := noAuth.Submit(context.Background(), mockHubTestContext())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")

	wrongAuth := newHTTPHubClient(hub.URL(), "wrong-token", 2*time.Second)
	_, err = wrongAuth.Submit(context.Background(), mockHubTestContext())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")

	authed := newHTTPHubClient(hub.URL(), "secret-token", 2*time.Second)
	h, err := authed.Submit(context.Background(), mockHubTestContext())
	require.NoError(t, err)
	_, state, err := authed.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateDone, state)
}

// TestMockHubAsyncLongTask covers the async long-task scenario: the task
// stays StateRunning across polls until released, then completes.
func TestMockHubAsyncLongTask(t *testing.T) {
	hub := NewMockHub(t)
	hub.AsyncTasks = true
	client := newHTTPHubClient(hub.URL(), "", 2*time.Second)

	h, err := client.Submit(context.Background(), mockHubTestContext())
	require.NoError(t, err)

	// Not yet released: polls report running, with no result.
	for i := 0; i < 3; i++ {
		result, state, err := client.Poll(context.Background(), h)
		require.NoError(t, err)
		assert.Equal(t, agents.StateRunning, state)
		assert.Nil(t, result)
		assert.False(t, state.IsTerminal())
	}

	hub.ReleaseTask(h.RemoteID)

	result, state, err := client.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateDone, state)
	require.NotNil(t, result)
	assert.Equal(t, "mock hub completed (async)", result.Summary)
}

// TestMockHubCancel covers cancellation of a running task and the unknown
// task error path.
func TestMockHubCancel(t *testing.T) {
	hub := NewMockHub(t)
	hub.AsyncTasks = true
	client := newHTTPHubClient(hub.URL(), "", 2*time.Second)

	h, err := client.Submit(context.Background(), mockHubTestContext())
	require.NoError(t, err)

	require.NoError(t, client.Cancel(context.Background(), h))

	_, state, err := client.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateCanceled, state)
	assert.True(t, state.IsTerminal())

	// Unknown tasks error on both poll and cancel — never silently pending.
	ghost := &agents.Handle{Backend: "hub-mock", RemoteID: "hub-task-999"}
	_, _, err = client.Poll(context.Background(), ghost)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	err = client.Cancel(context.Background(), ghost)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}
