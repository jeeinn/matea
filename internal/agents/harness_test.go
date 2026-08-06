package agents

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Harness for testing ---

type mockHarness struct {
	profile      HarnessProfile
	turnCalled   bool
	turnInput    *HarnessTurnInput
	turnResult   *HarnessTurnResult
	turnErr      error
	closeCalled  bool
	resetCalled  string
}

func (m *mockHarness) Profile() HarnessProfile {
	return m.profile
}

func (m *mockHarness) RunTurn(ctx context.Context, input *HarnessTurnInput) (*HarnessTurnResult, error) {
	m.turnCalled = true
	m.turnInput = input
	return m.turnResult, m.turnErr
}

func (m *mockHarness) Close() error {
	m.closeCalled = true
	return nil
}

func (m *mockHarness) ResetSession(sessionID string) error {
	m.resetCalled = sessionID
	return nil
}

// --- Tests ---

func TestHarnessRouterRegisterAndLookup(t *testing.T) {
	r := newHarnessRouter()

	h := &mockHarness{
		profile: HarnessProfile{
			ID:                "test-harness",
			DisplayName:       "Test Harness",
			ControlTransport:  ControlSubmitContract,
			ToolTransport:     ToolViaSubmit,
			SupportsToolUse:   true,
			SupportsMemory:    false,
			HandlesGit:        false,
			HasIMChannels:     false,
			OwnsWorkspace:     false,
		},
	}

	// Register
	r.Register(h)

	// Lookup
	got, err := r.Lookup("test-harness")
	require.NoError(t, err)
	assert.Equal(t, "test-harness", got.Profile().ID)

	// Names
	names := r.Names()
	assert.Contains(t, names, "test-harness")
}

func TestHarnessRouterLookupUnknown(t *testing.T) {
	r := newHarnessRouter()

	_, err := r.Lookup("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown harness")
}

func TestHarnessRouterRegisterEmptyIDPanics(t *testing.T) {
	r := newHarnessRouter()

	h := &mockHarness{
		profile: HarnessProfile{ID: ""},
	}

	assert.Panics(t, func() {
		r.Register(h)
	})
}

func TestHarnessRouterGetHarnessNormalizesBackend(t *testing.T) {
	r := newHarnessRouter()

	h := &mockHarness{
		profile: HarnessProfile{
			ID:               "builtin",
			ControlTransport: ControlSubmitContract,
			ToolTransport:    ToolDirect,
		},
	}
	r.Register(h)

	// Empty backend name should resolve to builtin
	got, err := r.GetHarness("")
	require.NoError(t, err)
	assert.Equal(t, "builtin", got.Profile().ID)
}

func TestHarnessRouterGetHarnessUnknown(t *testing.T) {
	r := newHarnessRouter()

	_, err := r.GetHarness("nonexistent")
	assert.Error(t, err)
}

func TestHarnessProfileTransportMetadata(t *testing.T) {
	// Verify the transport constants are well-defined
	assert.Equal(t, TransportKind("in_process"), TransportInProcess)
	assert.Equal(t, TransportKind("out_of_process"), TransportOutOfProcess)

	assert.Equal(t, ControlTransport("submit_contract"), ControlSubmitContract)
	assert.Equal(t, ControlTransport("mcp"), ControlMCP)

	assert.Equal(t, ToolTransport("tool_direct"), ToolDirect)
	assert.Equal(t, ToolTransport("tool_via_submit"), ToolViaSubmit)
	assert.Equal(t, ToolTransport("tool_via_mcp"), ToolViaMCP)
}

func TestHarnessActionConstants(t *testing.T) {
	assert.Equal(t, HarnessAction("comment"), ActionComment)
	assert.Equal(t, HarnessAction("create_pr"), ActionCreatePR)
	assert.Equal(t, HarnessAction("none"), ActionNone)
}

func TestMockHarnessRunTurn(t *testing.T) {
	h := &mockHarness{
		profile: HarnessProfile{ID: "mock"},
		turnResult: &HarnessTurnResult{
			Reply:  "test reply",
			Action: ActionComment,
		},
	}

	input := &HarnessTurnInput{
		Task: &TaskContext{
			TaskType: "analyze_issue",
			Repo:     "owner/repo",
		},
		Model: "gpt-4",
	}

	result, err := h.RunTurn(context.Background(), input)
	require.NoError(t, err)
	assert.True(t, h.turnCalled)
	assert.Equal(t, "test reply", result.Reply)
	assert.Equal(t, ActionComment, result.Action)
	assert.Equal(t, input, h.turnInput)
}

func TestMockHarnessRunTurnError(t *testing.T) {
	h := &mockHarness{
		profile: HarnessProfile{ID: "mock"},
		turnErr: errors.New("test error"),
	}

	result, err := h.RunTurn(context.Background(), &HarnessTurnInput{})
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestMockHarnessClose(t *testing.T) {
	h := &mockHarness{profile: HarnessProfile{ID: "mock"}}
	err := h.Close()
	assert.NoError(t, err)
	assert.True(t, h.closeCalled)
}

func TestMockHarnessResetSession(t *testing.T) {
	h := &mockHarness{profile: HarnessProfile{ID: "mock"}}
	err := h.ResetSession("session-123")
	assert.NoError(t, err)
	assert.Equal(t, "session-123", h.resetCalled)
}

func TestHarnessTurnResultPendingApprovals(t *testing.T) {
	// Verify PendingApprovals field works
	result := &HarnessTurnResult{
		Reply:  "done",
		Action: ActionNone,
		PendingApprovals: []PendingApproval{
			{
				Kind:        "push",
				Description: "Push to main",
				Payload:     "branch:main",
			},
		},
	}
	assert.Len(t, result.PendingApprovals, 1)
	assert.Equal(t, "push", result.PendingApprovals[0].Kind)
}

func TestHarnessTurnResultDeliver(t *testing.T) {
	// Verify Deliver field works
	result := &HarnessTurnResult{
		Reply:  "done",
		Action: ActionComment,
		Deliver: &DeliverRequest{
			Event:   "task_completed",
			Channel: "feishu",
			Content: "Task completed successfully",
		},
	}
	assert.NotNil(t, result.Deliver)
	assert.Equal(t, "task_completed", result.Deliver.Event)
}

func TestHarnessRouterReplaceExisting(t *testing.T) {
	r := newHarnessRouter()

	h1 := &mockHarness{profile: HarnessProfile{ID: "test"}}
	h2 := &mockHarness{profile: HarnessProfile{ID: "test"}}

	r.Register(h1)
	r.Register(h2)

	got, err := r.Lookup("test")
	require.NoError(t, err)
	// Should be h2 (replaced)
	assert.Equal(t, h2, got)
}
