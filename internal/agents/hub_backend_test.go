package agents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateIsTerminal(t *testing.T) {
	assert.False(t, StatePending.IsTerminal())
	assert.False(t, StateRunning.IsTerminal())
	assert.True(t, StateDone.IsTerminal())
	assert.True(t, StateFailed.IsTerminal())
	assert.True(t, StateCanceled.IsTerminal())
}

func TestHandleJSONRoundTrip(t *testing.T) {
	// Handle is persisted to SQLite as JSON — verify the round trip is lossless.
	h := &Handle{
		Backend:        "hub-opencode",
		RemoteID:       "sess-abc-123",
		IdempotencyKey: "task-42-attempt-1",
	}
	data, err := json.Marshal(h)
	require.NoError(t, err)

	var got Handle
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, *h, got)
}

func TestTaskContextJSONOmitsEmptyFields(t *testing.T) {
	// Optional sections must be omitted when empty so hub payloads stay lean.
	tc := &TaskContext{
		TaskType: "review_pr",
		Role:     "review",
		Backend:  "hub-hermes",
		Repo:     "owner/repo",
		PRID:     7,
		Diff:     "diff --git ...",
	}
	data, err := json.Marshal(tc)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "review_pr", raw["task_type"])
	assert.Equal(t, "diff --git ...", raw["diff"])
	_, hasComments := raw["comments"]
	assert.False(t, hasComments, "empty comments should be omitted")
	_, hasToolAccess := raw["tool_access"]
	assert.False(t, hasToolAccess, "nil tool_access should be omitted")
}

// fakeHubBackend is a minimal HubBackend implementation for registry tests.
type fakeHubBackend struct {
	name string
	caps HubCapabilities
}

func (f *fakeHubBackend) Name() string { return f.name }
func (f *fakeHubBackend) Submit(ctx context.Context, task *TaskContext) (*Handle, error) {
	return &Handle{Backend: f.name, RemoteID: "r-1", IdempotencyKey: "k-1"}, nil
}
func (f *fakeHubBackend) Poll(ctx context.Context, h *Handle) (*BackendResult, State, error) {
	return &BackendResult{Summary: "ok"}, StateDone, nil
}
func (f *fakeHubBackend) Cancel(ctx context.Context, h *Handle) error { return nil }
func (f *fakeHubBackend) Capabilities() HubCapabilities               { return f.caps }
func (f *fakeHubBackend) HealthCheck(ctx context.Context) error       { return nil }

func TestHubBackendRegistry_RegisterAndLookup(t *testing.T) {
	reg := NewHubBackendRegistry()
	b := &fakeHubBackend{name: "hub-opencode", caps: HubCapabilities{SupportsToolUse: true}}
	reg.Register(b)

	got, err := reg.Lookup("hub-opencode")
	require.NoError(t, err)
	assert.Equal(t, "hub-opencode", got.Name())
	assert.True(t, got.Capabilities().SupportsToolUse)
}

func TestHubBackendRegistry_UnknownBackendMustError(t *testing.T) {
	reg := NewHubBackendRegistry()
	reg.Register(&fakeHubBackend{name: "builtin"})

	// Typos must fail loudly — never silently fall back to builtin.
	for _, bad := range []string{"hub_opencode", "Hub-hermes", "opencode", ""} {
		_, err := reg.Lookup(bad)
		require.Error(t, err, "Lookup(%q) should fail", bad)
		assert.Contains(t, err.Error(), "unknown backend")
	}
}

func TestHubBackendRegistry_Names(t *testing.T) {
	reg := NewHubBackendRegistry()
	reg.Register(&fakeHubBackend{name: "builtin"})
	reg.Register(&fakeHubBackend{name: "hub-opencode"})

	names := reg.Names()
	assert.ElementsMatch(t, []string{"builtin", "hub-opencode"}, names)
}

// erringHealthBackend verifies HealthCheck errors propagate through the interface.
type erringHealthBackend struct{ fakeHubBackend }

func (e *erringHealthBackend) HealthCheck(ctx context.Context) error {
	return errors.New("connection refused")
}

func TestHubBackend_HealthCheckErrorPropagates(t *testing.T) {
	var b HubBackend = &erringHealthBackend{fakeHubBackend{name: "hub-broken"}}
	err := b.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestToolAccessGrantFields(t *testing.T) {
	expiry := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	grant := &ToolAccessGrant{
		Endpoint:     "http://127.0.0.1:8082/mcp",
		Token:        "tok-123",
		AllowedTools: []string{"read_file", "write_file"},
		ExpiresAt:    expiry,
	}
	data, err := json.Marshal(grant)
	require.NoError(t, err)

	var got ToolAccessGrant
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, grant.Endpoint, got.Endpoint)
	assert.Equal(t, grant.AllowedTools, got.AllowedTools)
	assert.True(t, grant.ExpiresAt.Equal(got.ExpiresAt))
}
