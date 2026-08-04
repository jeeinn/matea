package agents

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// This file defines the HubBackend abstraction (Phase 1.2, tasks 1.2.1/1.2.2):
// the pluggable agent-backend interface plus the types exchanged between Matea
// and hub backends (OpenCode / Hermes / OpenClaw / custom).
//
// Design constraints (frozen decisions #12, #5, #6):
//   - Async handle form: Submit returns a persistable Handle; Poll waits for
//     completion. A hub task may run for tens of minutes — a synchronous call
//     would lose the task on Matea restart and orphan the hub-side session.
//   - Interface-async != restart recovery: the Handle MUST be persisted to the
//     task queue immediately after Submit, and the Executor MUST re-attach
//     polling for non-terminal Handles on startup (replay). Implementations
//     and runners are required to honor this; see TASKS.md 1.2.1.
//   - Matea always owns Gitea write-back and the workflow gates; hubs return
//     BackendResult and never talk to Gitea directly (unless HandlesGit).

// State represents the lifecycle state of a hub-side task.
type State string

const (
	StatePending  State = "pending"
	StateRunning  State = "running"
	StateDone     State = "done"
	StateFailed   State = "failed"
	StateCanceled State = "canceled"
)

// IsTerminal reports whether the state is final (no further progress expected).
func (s State) IsTerminal() bool {
	return s == StateDone || s == StateFailed || s == StateCanceled
}

// Handle is a persistable reference to a task submitted to a hub backend.
// It is stored alongside the task in SQLite so polling can resume after a
// Matea restart, and so duplicate submissions can be detected.
type Handle struct {
	Backend        string `json:"backend"`         // backend name, e.g. "hub-opencode"
	RemoteID       string `json:"remote_id"`       // hub-side session/job id
	IdempotencyKey string `json:"idempotency_key"` // dedup key for safe resubmission
}

// CommentSnapshot is a point-in-time copy of a Gitea comment, pre-fetched by
// Matea so the hub never needs direct Gitea API access.
type CommentSnapshot struct {
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// ToolAccessGrant describes how a hub may call back into Matea's sandbox
// tools (Phase 2b: Matea MCP tools registered into the hub session).
// Hubs must NOT receive inline tool implementations — only this grant.
type ToolAccessGrant struct {
	Endpoint     string    `json:"endpoint"`      // MCP endpoint exposed by Matea
	Token        string    `json:"token"`         // short-lived session token
	AllowedTools []string  `json:"allowed_tools"` // tool name whitelist
	ExpiresAt    time.Time `json:"expires_at"`    // grant TTL
}

// TaskContext is the package Matea delivers to a hub backend. It covers all
// task types; Matea pre-fetches every piece of Gitea context so the hub only
// consumes this struct.
type TaskContext struct {
	TaskType string `json:"task_type"` // analyze_issue | review_pr | reply_comment | solve_issue | solve_comment | fix_bug
	Role     string `json:"role"`      // analyze | coder | review
	Backend  string `json:"backend"`   // builtin | hub-opencode | hub-hermes | ...
	Repo     string `json:"repo"`      // owner/repo
	IssueID  int    `json:"issue_id"`  // logic issue (0 for pure PR without linked issue)
	PRID     int    `json:"pr_id"`     // 0 for plain issues

	// Matea pre-fetched Gitea context (hub does not call Gitea directly).
	IssueTitle string            `json:"issue_title,omitempty"`
	IssueBody  string            `json:"issue_body,omitempty"`
	Comments   []CommentSnapshot `json:"comments,omitempty"`
	Diff       string            `json:"diff,omitempty"`        // review_pr only
	BaseBranch string            `json:"base_branch,omitempty"`

	// Execution target for the builtin backend: which local LLM to run.
	// Hub backends own their model config server-side and ignore these.
	// (Added in 1.2.3 when the builtin loop was wrapped as a HubBackend —
	// the interface refined from a real implementation.)
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	TaskID   int64  `json:"task_id,omitempty"` // local task id (logging / usage attribution)

	// Matea prompt layer output (system_prompt / user_template rendered).
	SystemPrompt string `json:"system_prompt,omitempty"`
	UserPrompt   string `json:"user_prompt,omitempty"`

	// Memory correlation keys for hub-side recall (matea.repo / matea.issue / ...).
	MemoryKeys map[string]string `json:"memory_keys,omitempty"`

	// Code tasks only: sandbox prepared by Matea; hub operates files via ToolAccess.
	SandboxPath string           `json:"sandbox_path,omitempty"`
	ToolAccess  *ToolAccessGrant `json:"tool_access,omitempty"`

	// Channel routing for deliver events (IM bridges).
	Channel  string `json:"channel,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

// GiteaAction is an action the hub asks Matea to perform on Gitea.
// Matea remains the sole writer to Gitea.
type GiteaAction struct {
	Kind    string `json:"kind"` // "comment" | "create_pr"
	Content string `json:"content,omitempty"`

	// create_pr only:
	Title  string `json:"title,omitempty"`
	Branch string `json:"branch,omitempty"`
}

// DeliverRequest asks Matea to emit a standardized deliver event to an
// external channel (IM bridge or hub receiver).
type DeliverRequest struct {
	Event    string `json:"event"` // e.g. "task_completed"
	Channel  string `json:"channel"`
	ThreadID string `json:"thread_id,omitempty"`
	Repo     string `json:"repo,omitempty"`
	IssueID  int    `json:"issue_id,omitempty"`
	PRID     int    `json:"pr_id,omitempty"`
	Action   string `json:"action"` // e.g. "comment"
	Content  string `json:"content"`
}

// BackendResult is what a hub backend returns for a completed task.
type BackendResult struct {
	Summary      string        `json:"summary"`
	GiteaActions []GiteaAction `json:"gitea_actions,omitempty"`
	Deliver      *DeliverRequest `json:"deliver,omitempty"`

	// ExternallyHandled is true when the hub already performed git/PR itself
	// (only honored when Capabilities().HandlesGit is true). Default false:
	// Matea finalizes git/PR from the sandbox.
	ExternallyHandled bool `json:"externally_handled,omitempty"`
}

// HubCapabilities declares what a hub backend can do. Matea calls by contract
// and never assumes capabilities beyond this declaration.
type HubCapabilities struct {
	SupportsToolUse        bool `json:"supports_tool_use"`        // multi-turn tool-use
	SupportsMemory         bool `json:"supports_memory"`          // cross-session memory
	SupportsSkillEvolution bool `json:"supports_skill_evolution"` // Skill / E-A-A-S self-evolution
	SupportsMCPClient      bool `json:"supports_mcp_client"`      // can call Matea MCP tools
	HasIMChannels          bool `json:"has_im_channels"`          // brings its own IM channels
	HandlesGit             bool `json:"handles_git"`              // performs git/PR itself (default false)
}

// HubBackend is the pluggable agent-backend interface. OpenCode, Hermes,
// OpenClaw and custom HTTP hubs are all unified behind this contract; the
// builtin backend implements it too, so runners see one abstraction.
//
// Async contract (decision #12): Submit returns quickly with a Handle; the
// caller persists the Handle and uses Poll to await completion. Implementations
// may complete synchronously inside Submit (returning a Handle whose first
// Poll reports StateDone) — but callers must not assume it.
type HubBackend interface {
	// Name returns the backend identifier, e.g. "builtin", "hub-opencode".
	Name() string

	// Submit hands a task to the backend and returns a persistable Handle.
	Submit(ctx context.Context, task *TaskContext) (*Handle, error)

	// Poll reports the current result and state for a Handle. It must be
	// safe to call repeatedly and after a Matea restart (given the persisted
	// Handle).
	Poll(ctx context.Context, h *Handle) (*BackendResult, State, error)

	// Cancel asks the backend to stop the task (best effort).
	Cancel(ctx context.Context, h *Handle) error

	// Capabilities declares the backend's feature set.
	Capabilities() HubCapabilities

	// HealthCheck probes backend availability.
	HealthCheck(ctx context.Context) error
}

// HubBackendRegistry is an explicit name → backend registry. Lookup of an
// unknown name must fail loudly — never fall back silently to builtin
// (a typo like "hub_opencode" would otherwise burn the user's own LLM quota
// while they believe a hub is in use).
type HubBackendRegistry struct {
	mu       sync.RWMutex
	backends map[string]HubBackend
}

// NewHubBackendRegistry creates an empty registry.
func NewHubBackendRegistry() *HubBackendRegistry {
	return &HubBackendRegistry{backends: make(map[string]HubBackend)}
}

// Register adds or replaces a backend under its Name().
func (r *HubBackendRegistry) Register(b HubBackend) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.backends[b.Name()] = b
}

// Lookup returns the backend for a name, or an error if unknown.
func (r *HubBackendRegistry) Lookup(name string) (HubBackend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.backends[name]
	if !ok {
		return nil, fmt.Errorf("unknown backend %q", name)
	}
	return b, nil
}

// Names returns all registered backend names (unsorted).
func (r *HubBackendRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.backends))
	for name := range r.backends {
		names = append(names, name)
	}
	return names
}
