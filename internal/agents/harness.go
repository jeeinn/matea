package agents

import (
	"context"
	"fmt"
	"sync"

	"github.com/jeeinn/matea/internal/config"
)

// Harness is the unified pluggable execution kernel interface (D10).
// It consolidates the three existing interfaces (Runner sync / CodingBackend write /
// HubBackend async Submit-Poll) into a single turn-based contract so that
// builtin, OpenCode, Hermes and future harnesses (Pi/Codex/Claude) are
// interchangeable "brains" behind one abstraction.
//
// Transport model (D12 2x2):
//
//	              toolTransport
//	            ┌──────────────┬──────────────────┐
//	control      │ submit-      │   mcp            │
//	transport    │ contract     │   (Phase 3)      │
//	┌───────────┼──────────────┼──────────────────┤
//	│ in-process│ builtin(Go)  │ (不需要)          │
//	│ out-of-   │ hub-opencode │ hub-* 实时工具    │
//	│ process   │ hub-hermes   │ 调用(Claude/Codex)│
//	└───────────┴──────────────┴──────────────────┘
//
// Phase 2 implements only: in-process (builtin) + out-of-process (submit-contract).
// The interface declares controlTransport/toolTransport metadata for documentation
// and future-proofing, but concrete adapters only need to implement the subset
// they actually use.
//
// Selection granularity: per-task / per-agent (never dynamic per-turn). A task
// binds to a harness at creation and sticks with it; Matea tasks carry state
// (session / workspace / Handle) that cannot be migrated mid-flight.

// TransportKind identifies how a harness receives control and exposes tools.
type TransportKind string

const (
	// TransportInProcess means the harness runs in the same Go process
	// (the builtin adapter). Tools are direct Go function calls.
	TransportInProcess TransportKind = "in_process"

	// TransportOutOfProcess means the harness is an external process/service
	// reached over HTTP / stdio (OpenCode, Hermes, future).
	TransportOutOfProcess TransportKind = "out_of_process"
)

// ControlTransport describes how Matea delivers work to the harness.
type ControlTransport string

const (
	// ControlSubmitContract: Matea submits context, harness returns result/action,
	// Matea executes tools. This is the Phase 2 default for all out-of-process hubs.
	ControlSubmitContract ControlTransport = "submit_contract"

	// ControlMCP: harness calls back into Matea's MCP server for tools in real-time
	// (Phase 3, e.g. Claude Code).
	ControlMCP ControlTransport = "mcp"
)

// ToolTransport describes how the harness accesses tools.
type ToolTransport string

const (
	// ToolDirect: harness calls Go functions directly (in-process only).
	ToolDirect ToolTransport = "tool_direct"

	// ToolViaSubmit: tools are Matea's; harness returns actions, Matea executes.
	// Out-of-process default.
	ToolViaSubmit ToolTransport = "tool_via_submit"

	// ToolViaMCP: harness reaches Matea MCP server for tool calls (Phase 3).
	ToolViaMCP ToolTransport = "tool_via_mcp"
)

// HarnessProfile declares a harness's capabilities and transport metadata.
// It is the single source of truth for what a harness can do; Matea routes
// and gates by reading it, never by assumption.
type HarnessProfile struct {
	// ID is the harness identifier, e.g. "builtin", "hub-opencode", "hub-hermes".
	ID string `json:"id"`

	// DisplayName is a human-readable name for UI display.
	DisplayName string `json:"display_name"`

	// ControlTransport is how Matea delivers work to this harness.
	ControlTransport ControlTransport `json:"control_transport"`

	// ToolTransport is how this harness accesses tools.
	ToolTransport ToolTransport `json:"tool_transport"`

	// SupportsToolUse indicates multi-turn tool-use loop (builtin only in Phase 2).
	SupportsToolUse bool `json:"supports_tool_use"`

	// SupportsMemory indicates cross-session memory (hub-managed).
	SupportsMemory bool `json:"supports_memory"`

	// HandlesGit indicates the harness performs git/PR itself (rare; default false).
	HandlesGit bool `json:"handles_git"`

	// HasIMChannels indicates the harness brings its own IM channels.
	HasIMChannels bool `json:"has_im_channels"`

	// OwnsWorkspace indicates the harness manages its own workspace (e.g. Hermes
	// runs in its own sandbox). When false, Matea prepares the workspace.
	OwnsWorkspace bool `json:"owns_workspace"`
}

// HarnessTurnInput is the package Matea delivers to a harness for one turn.
// It is the single input shape for Harness.RunTurn across all harness types.
type HarnessTurnInput struct {
	// Task is the pre-fetched Gitea context + Matea prompts (same as HubBackend.TaskContext).
	Task *TaskContext

	// Model selects which LLM the harness should prefer (builtin-only; hub-* ignore).
	Model string

	// SessionID identifies a continuing conversation (reply_comment multi-turn).
	SessionID string

	// Continue marks whether this turn continues an existing session.
	Continue bool
}

// HarnessAction is what a harness asks Matea to do after a turn.
// Matea remains the sole executor of these actions (Gitea write-back etc.).
type HarnessAction string

const (
	// ActionComment asks Matea to post a comment to Gitea.
	ActionComment HarnessAction = "comment"

	// ActionCreatePR asks Matea to finalize git changes and create a PR.
	ActionCreatePR HarnessAction = "create_pr"

	// ActionNone means no follow-up action needed.
	ActionNone HarnessAction = "none"
)

// HarnessTurnResult is the output of one Harness.RunTurn.
type HarnessTurnResult struct {
	// Reply is the harness's textual response (comment body, summary, etc.).
	Reply string `json:"reply"`

	// Action asks Matea to perform a follow-up on Gitea.
	Action HarnessAction `json:"action"`

	// PendingApprovals is a list of actions waiting for human approval before execution.
	// Phase 3+: when a harness produces high-risk actions (e.g. push to main).
	PendingApprovals []PendingApproval `json:"pending_approvals,omitempty"`

	// Deliver optionally asks Matea to emit an outbound deliver event.
	Deliver *DeliverRequest `json:"deliver,omitempty"`

	// SessionID is the harness-side session identifier for continuation.
	SessionID string `json:"session_id,omitempty"`

	// ExternallyHandled is true when the harness already performed git/PR itself
	// (only honored when Profile().HandlesGit is true).
	ExternallyHandled bool `json:"externally_handled,omitempty"`
}

// PendingApproval is an action that needs human confirmation.
type PendingApproval struct {
	Kind        string `json:"kind"`                  // "comment" | "create_pr" | "push"
	Description string `json:"description"`           // human-readable summary
	Payload     string `json:"payload"`               // opaque data for execution
	ExpiresAt   int64  `json:"expires_at,omitempty"`  // unix seconds, 0 = no expiry
}

// Harness is the unified pluggable execution kernel.
type Harness interface {
	// Profile declares this harness's capabilities and transport metadata.
	Profile() HarnessProfile

	// RunTurn executes one turn and returns the result. For single-shot tasks
	// (analyze/review/reply) this is one call; for multi-turn write tasks the
	// harness internally loops until completion then returns the final result.
	RunTurn(ctx context.Context, input *HarnessTurnInput) (*HarnessTurnResult, error)

	// Close releases any resources held by the harness (HTTP connections, etc.).
	Close() error

	// ResetSession clears any server-side state for a session id.
	ResetSession(sessionID string) error
}

// ----------------------------------------------------------------------------
// harnessRouter: the name → Harness registry (D10)
// ----------------------------------------------------------------------------

// harnessRouter is the single registry for all harnesses. Adding a new brain
// = implement Harness + one Register call.
type harnessRouter struct {
	mu       sync.RWMutex
	harnesses map[string]Harness
}

// newHarnessRouter creates an empty router.
func newHarnessRouter() *harnessRouter {
	return &harnessRouter{harnesses: make(map[string]Harness)}
}

// Register adds or replaces a harness under its Profile().ID.
// The harness must have a non-empty ID; registration panics otherwise
// (programmer error, fail fast).
func (r *harnessRouter) Register(h Harness) {
	prof := h.Profile()
	if prof.ID == "" {
		panic(fmt.Sprintf("harness.Register: harness has empty ID (type %T)", h))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.harnesses[prof.ID] = h
}

// Lookup returns a harness by id, or an error if unknown.
// Unknown ids fail loudly — never fall back to builtin (a typo like
// "hub_opencode" must not silently burn the user's LLM quota).
func (r *harnessRouter) Lookup(id string) (Harness, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.harnesses[id]
	if !ok {
		return nil, fmt.Errorf("unknown harness %q", id)
	}
	return h, nil
}

// Names returns all registered harness ids (unsorted).
func (r *harnessRouter) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.harnesses))
	for id := range r.harnesses {
		names = append(names, id)
	}
	return names
}

// GetHarness resolves a harness by agent backend name, normalizing legacy
// identifiers first. Unknown harnesses return an error.
func (r *harnessRouter) GetHarness(backendName string) (Harness, error) {
	name := config.NormalizeBackend(backendName)
	if name == "" {
		name = config.BackendNameBuiltin
	}
	return r.Lookup(name)
}
