package agents

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// BuiltinHubBackend exposes the in-process agent execution through the
// HubBackend interface (task 1.2.3): write task types (solve_issue /
// solve_comment / fix_bug) run the existing AgentLoop via
// BuiltinCodingBackend; all other task types run a single-shot LLM
// completion, mirroring Analyze/Review/Reply runners. It is the "builtin"
// entry of the HubBackendRegistry, so runners see one abstraction across
// builtin and hub-* backends.
//
// Execution is synchronous, which the HubBackend contract explicitly allows:
// Submit runs to completion under ctx and caches the outcome, so the first
// Poll reports a terminal state. Cancel is a no-op beyond ctx cancellation
// (same contract as BuiltinCodingBackend.Abort).
//
// Restart semantics: because execution completes inside Submit, a non-terminal
// builtin Handle can never outlive the process — a crash mid-Submit is
// recovered by the task queue's existing crash-replay (the task is re-run from
// scratch), never by re-polling a persisted builtin Handle. The 1.2.1
// persist-and-reattach requirement therefore binds only async hub backends.
//
// Phase 1 note: production runners still dispatch write tasks through the
// CodingBackend path; this adapter proves the HubBackend seam with a real
// second implementation and backs the 1.2.4 dispatch branch.
type BuiltinHubBackend struct {
	factory *RunnerFactory

	mu      sync.Mutex
	results map[string]*builtinOutcome // RemoteID → terminal outcome (in-memory; see restart note above)
}

type builtinOutcome struct {
	result *BackendResult
	err    error
}

// NewBuiltinHubBackend constructs the builtin HubBackend bound to a
// RunnerFactory (LLM registry, tool packs, sandbox config, usage recording).
func NewBuiltinHubBackend(factory *RunnerFactory) *BuiltinHubBackend {
	return &BuiltinHubBackend{
		factory: factory,
		results: make(map[string]*builtinOutcome),
	}
}

// Compile-time interface compliance check.
var _ HubBackend = (*BuiltinHubBackend)(nil)

// Name returns "builtin".
func (b *BuiltinHubBackend) Name() string { return "builtin" }

// Capabilities declares the builtin feature set: full tool use and MCP client
// support; no hub-side memory, skill evolution, IM channels, or git ownership
// (Matea finalizes git/PR from the sandbox).
func (b *BuiltinHubBackend) Capabilities() HubCapabilities {
	return HubCapabilities{
		SupportsToolUse:   true,
		SupportsMCPClient: true,
	}
}

// HealthCheck always succeeds: the builtin backend is in-process.
func (b *BuiltinHubBackend) HealthCheck(ctx context.Context) error {
	_ = ctx
	return nil
}

// Submit runs the task synchronously and returns a Handle whose first Poll
// reports a terminal state. Submission/validation errors (nil context,
// missing provider, missing sandbox path for write tasks) return a nil
// Handle with the error; execution errors also fail Submit directly because
// the synchronous builtin already knows the outcome — no Handle worth
// persisting exists.
func (b *BuiltinHubBackend) Submit(ctx context.Context, tc *TaskContext) (*Handle, error) {
	if tc == nil {
		return nil, fmt.Errorf("builtin backend: nil TaskContext")
	}
	result, err := b.execute(ctx, tc)
	if err != nil {
		return nil, err
	}
	h := &Handle{
		Backend:        b.Name(),
		RemoteID:       fmt.Sprintf("builtin-%d", time.Now().UnixNano()),
		IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", tc.TaskType, tc.Repo, tc.IssueID, tc.PRID),
	}
	b.mu.Lock()
	b.results[h.RemoteID] = &builtinOutcome{result: result}
	b.mu.Unlock()
	return h, nil
}

// Poll returns the cached terminal outcome for a Handle produced by Submit.
// Handles are in-memory: an unknown RemoteID means the handle was never
// submitted to this process (or predates a restart — impossible for a
// synchronous backend; see the type doc) and is reported as an error.
func (b *BuiltinHubBackend) Poll(ctx context.Context, h *Handle) (*BackendResult, State, error) {
	_ = ctx
	if h == nil {
		return nil, "", fmt.Errorf("builtin backend: nil Handle")
	}
	if h.Backend != "" && h.Backend != b.Name() {
		return nil, "", fmt.Errorf("builtin backend: handle belongs to backend %q", h.Backend)
	}
	b.mu.Lock()
	out, ok := b.results[h.RemoteID]
	b.mu.Unlock()
	if !ok {
		return nil, "", fmt.Errorf("builtin backend: unknown handle %q", h.RemoteID)
	}
	if out.err != nil {
		return nil, StateFailed, out.err
	}
	return out.result, StateDone, nil
}

// Cancel is a no-op: execution finished (or failed) inside Submit, so there
// is nothing running to cancel. Unknown handles are tolerated — cancellation
// is idempotent by nature.
func (b *BuiltinHubBackend) Cancel(ctx context.Context, h *Handle) error {
	_ = ctx
	_ = h
	return nil
}

// execute dispatches on task type: write tasks run the tool-use loop, all
// others run a single completion.
func (b *BuiltinHubBackend) execute(ctx context.Context, tc *TaskContext) (*BackendResult, error) {
	switch tc.TaskType {
	case "solve_issue", "solve_comment", "fix_bug":
		return b.executeWrite(ctx, tc)
	default:
		return b.executeSingleShot(ctx, tc)
	}
}

// executeSingleShot mirrors the Analyze/Review/Reply runners: one completion
// with the Matea-rendered prompts carried by the TaskContext.
func (b *BuiltinHubBackend) executeSingleShot(ctx context.Context, tc *TaskContext) (*BackendResult, error) {
	provider, err := b.resolveProvider(tc)
	if err != nil {
		return nil, err
	}

	messages := make([]llm.Message, 0, 2)
	if tc.SystemPrompt != "" {
		messages = append(messages, llm.Message{Role: "system", Content: tc.SystemPrompt})
	}
	messages = append(messages, llm.Message{Role: "user", Content: tc.UserPrompt})

	resp, err := provider.ChatCompletion(ctx, &llm.ChatRequest{
		Model:       tc.Model,
		Messages:    messages,
		MaxTokens:   b.factory.defaultMaxOutput,
		Temperature: b.factory.defaultTemp,
	})
	if err != nil {
		return nil, fmt.Errorf("llm completion: %w", err)
	}
	return &BackendResult{Summary: resp.Content}, nil
}

// executeWrite delegates to the existing BuiltinCodingBackend (AgentLoop +
// sandbox tools), synthesizing the store-level request objects from the
// serializable TaskContext. The sandbox adopts the Matea-prepared workspace
// via NewWithPath (persistent: cleanup stays with the caller).
func (b *BuiltinHubBackend) executeWrite(ctx context.Context, tc *TaskContext) (*BackendResult, error) {
	if tc.SandboxPath == "" {
		return nil, fmt.Errorf("builtin backend: TaskContext.SandboxPath is required for %s tasks", tc.TaskType)
	}
	if _, err := b.resolveProvider(tc); err != nil {
		return nil, err
	}

	subType := "dev"
	if tc.TaskType == "fix_bug" {
		subType = "bugfix"
	}
	sb := sandbox.NewWithPath(b.factory.sandboxCfg, tc.TaskID, tc.SandboxPath)
	if err := sb.Setup(); err != nil {
		return nil, fmt.Errorf("sandbox setup: %w", err)
	}

	res, err := b.factory.builtinBackend.Run(ctx, CodingRequest{
		WorkDir:      tc.SandboxPath,
		Sandbox:      sb,
		Task:         &store.Task{ID: tc.TaskID, Repo: tc.Repo, Event: tc.IssueTitle, Context: tc.IssueBody},
		Agent:        &store.Agent{Provider: tc.Provider, Model: tc.Model},
		TaskSubType:  subType,
		Prompt:       tc.UserPrompt,
		SystemPrompt: tc.SystemPrompt,
	})
	if err != nil {
		return nil, err
	}
	return &BackendResult{Summary: res.Summary}, nil
}

// resolveProvider looks up the LLM provider named by the TaskContext.
func (b *BuiltinHubBackend) resolveProvider(tc *TaskContext) (llm.Provider, error) {
	if tc.Provider == "" {
		return nil, fmt.Errorf("builtin backend: TaskContext.Provider is required")
	}
	if b.factory.llmRegistry == nil {
		return nil, fmt.Errorf("builtin backend: no LLM registry configured")
	}
	provider, err := b.factory.llmRegistry.Get(tc.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	return provider, nil
}
