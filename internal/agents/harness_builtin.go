package agents

import (
	"context"
	"fmt"

	"github.com/jeeinn/matea/internal/config"
)

// BuiltinHarness adapts the existing in-process agent execution (BuiltinHubBackend)
// to the unified Harness interface (D10 proof 1: in-process transport).
//
// It wraps the existing BuiltinHubBackend so that the Harness interface is
// validated against a real second implementation. Behavior is identical to the
// current builtin path — this is purely an adapter, not a rewrite.
//
// Transport: in_process (control) + tool_direct (tools). The builtin harness
// runs the AgentLoop with Go-implemented sandbox tools directly in the Matea
// process.
type BuiltinHarness struct {
	factory *RunnerFactory
}

// NewBuiltinHarness creates the builtin in-process harness.
func NewBuiltinHarness(factory *RunnerFactory) *BuiltinHarness {
	return &BuiltinHarness{factory: factory}
}

// Compile-time interface compliance check.
var _ Harness = (*BuiltinHarness)(nil)

// Profile declares the builtin harness capabilities.
func (h *BuiltinHarness) Profile() HarnessProfile {
	return HarnessProfile{
		ID:                config.BackendNameBuiltin, // "builtin"
		DisplayName:       "Builtin Agent Loop",
		ControlTransport:  ControlSubmitContract, // synchronous submit-contract (in-process)
		ToolTransport:     ToolDirect,            // direct Go function calls
		SupportsToolUse:   true,                  // multi-turn AgentLoop
		SupportsMemory:    false,                 // no cross-session memory (Matea-managed)
		HandlesGit:        false,                 // Matea finalizes git/PR
		HasIMChannels:     false,                 // no built-in IM
		OwnsWorkspace:     false,                 // Matea prepares workspace
	}
}

// RunTurn executes one turn. For single-shot tasks (analyze/review/reply) this
// is one LLM completion; for write tasks (solve_issue/fix_bug) the builtin
// internally runs the multi-turn AgentLoop to completion.
//
// The result is always terminal (ActionComment for analyze/review/reply,
// ActionCreatePR for write tasks that produced changes, ActionNone otherwise).
func (h *BuiltinHarness) RunTurn(ctx context.Context, input *HarnessTurnInput) (*HarnessTurnResult, error) {
	if input == nil || input.Task == nil {
		return nil, fmt.Errorf("builtin harness: nil input or task")
	}

	tc := input.Task

	// Delegate to the existing BuiltinHubBackend for execution.
	backend := NewBuiltinHubBackend(h.factory)
	handle, err := backend.Submit(ctx, tc)
	if err != nil {
		return nil, fmt.Errorf("builtin harness submit: %w", err)
	}

	result, state, err := backend.Poll(ctx, handle)
	if err != nil {
		return nil, fmt.Errorf("builtin harness poll: %w", err)
	}

	if state == StateFailed {
		return nil, fmt.Errorf("builtin harness: %s", result.Summary)
	}

	// Map BackendResult to HarnessTurnResult
	harnessResult := &HarnessTurnResult{
		Reply:  result.Summary,
		Action: ActionNone,
	}

	// Determine action based on task type and result
	switch tc.TaskType {
	case "analyze_issue", "review_pr", "reply_comment", "trigger":
		harnessResult.Action = ActionComment
	case "solve_issue", "solve_comment", "fix_bug":
		// Write tasks: if there are git actions or a PR was created, signal it
		if len(result.GiteaActions) > 0 {
			for _, ga := range result.GiteaActions {
				if ga.Kind == "create_pr" {
					harnessResult.Action = ActionCreatePR
					break
				}
			}
		}
		// If the backend indicates external handling (rare for builtin), honor it
		if result.ExternallyHandled {
			harnessResult.ExternallyHandled = true
		}
	}

	// Pass through deliver request if present
	if result.Deliver != nil {
		harnessResult.Deliver = result.Deliver
	}

	return harnessResult, nil
}

// Close is a no-op for the builtin harness (in-process, no resources to release).
func (h *BuiltinHarness) Close() error {
	return nil
}

// ResetSession is a no-op for the builtin harness (session state is managed
// by Matea's session/workspace layer, not the builtin backend).
func (h *BuiltinHarness) ResetSession(sessionID string) error {
	_ = sessionID
	return nil
}
