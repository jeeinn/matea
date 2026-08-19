package agents

import (
	"context"
	"fmt"
	"log"
	"time"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// CodingBackend executes the coding phase of a write task on a prepared workspace.
//
// Contract (see server-runtime-design-v4.md §3):
//   - Run is called AFTER prepareWriteWorkspace and BEFORE finalizeWriteChanges.
//   - The backend must not clone, push, or open PRs; it only modifies files in
//     the prepared WorkDir. finalize decides whether to commit based on
//     git.HasChanges().
//   - The returned Provider is reused by finalizeWriteChanges for the
//     commit-message LLM call so the same provider instance is shared across
//     the write task (matches pre-A3 behavior).
type CodingBackend interface {
	Name() string
	Run(ctx context.Context, req CodingRequest) (*CodingResult, error)
	// Abort cancels a running coding session. For the builtin backend this is a
	// no-op (cancellation is via the ctx passed to Run). For hub-opencode it
	// issues POST /session/:id/abort.
	Abort(ctx context.Context, handle string) error
}

// HealthCheckableBackend is an optional interface for backends that support
// an up-front health probe. When implemented, runWriteTask calls HealthCheck
// BEFORE prepareWriteWorkspace. Failure returns an error (task → failed) unless
// allow_fallback_builtin is set on the backend config.
type HealthCheckableBackend interface {
	HealthCheck(ctx context.Context) error
}

// CodingRequest is the input to CodingBackend.Run.
//
// Prompts (Prompt / SystemPrompt) are pre-built by runWriteTask so that all
// backends share the same prompt pipeline (BuildDevPrompt/BuildBugfixPrompt +
// MergeAgentSystemPrompt + code context). Backends just consume them as
// user/system messages (builtin) or as the message body (hub-opencode).
type CodingRequest struct {
	// Workspace
	WorkDir string           // absolute path to the prepared repo working tree
	Sandbox *sandbox.Sandbox // sandbox for tool execution / audit (builtin backend)

	// Task context
	Task        *store.Task
	Agent       *store.Agent
	TaskSubType string // "dev" | "bugfix"

	// Prompts (pre-built by runWriteTask; backend just consumes)
	Prompt       string // user message: raw task.Context (issue body)
	SystemPrompt string // system message: BuildDevPrompt / BuildBugfixPrompt + MergeAgentSystemPrompt

	// Session
	SessionID string // Gateway session id (for continue semantics)
	Continue  bool   // true if continuing an existing session

	// Limits
	Timeout time.Duration

	// Backend-specific options (Agent.BackendOptions)
	BackendOptions map[string]any

	// ToolPack selects the tool pack for this task. Empty defaults to
	// "coder-default" for write tasks and "analyze-readonly" for analyze tasks.
	ToolPack string
}

// CodingResult is the output of CodingBackend.Run.
type CodingResult struct {
	Summary         string       // coder summary, used as PR body / comment content
	Success         bool         // false → finalize returns a comment with the error
	RemoteSessionID string       // opencode session id (empty for internal)
	Provider        llm.Provider // LLM provider used (reused by finalize for commit message)
}

// BuiltinCodingBackend wraps the existing AgentLoop + DefaultTools as the
// default coding backend. Used by all non-write tasks (forced) and by write
// tasks whose agent.backend resolves to "builtin".
type BuiltinCodingBackend struct {
	factory *RunnerFactory
}

// NewBuiltinCodingBackend constructs a BuiltinCodingBackend bound to a
// RunnerFactory (for LLM registry, token resolution, usage recording, debug).
func NewBuiltinCodingBackend(factory *RunnerFactory) *BuiltinCodingBackend {
	return &BuiltinCodingBackend{factory: factory}
}

// Name returns "builtin".
func (b *BuiltinCodingBackend) Name() string { return config.BackendNameBuiltin }

// Run executes the AgentLoop with DefaultTools on the prepared workspace.
//
// The prompts (Prompt / SystemPrompt) are pre-built by runWriteTask, so this
// method only handles provider resolution, loop configuration, tool registry,
// and the LLM message loop. Behavior matches the pre-A3.1 inline coding phase:
// identical provider lookup, token resolution, tool registry, loop config merge,
// and recorder wiring.
func (b *BuiltinCodingBackend) Run(ctx context.Context, req CodingRequest) (*CodingResult, error) {
	factory := b.factory
	agentCfg := req.Agent
	task := req.Task
	sb := req.Sandbox

	provider, err := factory.llmRegistry.Get(agentCfg.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}
	if !llm.SupportsTools(provider) {
		return nil, fmt.Errorf("provider %q does not support tool calls (Anthropic adapter has no tools yet); use an openai_compatible provider for coder tasks", agentCfg.Provider)
	}
	// Unknown/sparse meta (API ID-only) is not treated as supports_tools=false.
	if config.ModelToolsDenied(factory.getModelMeta(agentCfg.Provider, agentCfg.Model)) {
		return nil, fmt.Errorf("model %q/%q does not support tool calls (supports_tools=false); use a tool-capable model for coder tasks", agentCfg.Provider, agentCfg.Model)
	}

	maxInput := factory.resolveMaxInputTokens(agentCfg.MaxInputTokens, agentCfg.Provider, agentCfg.Model)
	maxOutput := factory.resolveMaxOutputTokens(agentCfg.MaxOutputTokens, agentCfg.Provider, agentCfg.Model)
	sampling := factory.resolveSamplingParams(agentCfg.Temperature, agentCfg.Provider, agentCfg.Model)
	mergedLoop := MergeLoopConfig(agentCfg.LoopConfig, factory.defaultLoop)

	// Resolve tool pack and assemble registry
	packID := req.ToolPack
	if packID == "" {
		packID = "coder-default"
	}
	packCfg, ok := factory.toolPacks.Packs[packID]
	if !ok {
		return nil, fmt.Errorf("tool pack %q not found", packID)
	}
	toolRegistry, err := agentpkg.AssembleToolRegistry(packCfg.Tools, sb)
	if err != nil {
		return nil, fmt.Errorf("assemble tool registry for pack %q: %w", packID, err)
	}

	// Register MCP tools if enabled for this agent
	if len(agentCfg.McpServers) > 0 && factory.mcpRegistry != nil {
		if err := toolRegistry.RegisterMCPTools(ctx, factory.mcpRegistry, agentCfg.McpServers); err != nil {
			return nil, fmt.Errorf("register mcp tools: %w", err)
		}
	}

	// Register skill discovery tools (coder may load script-backed skill tools)
	skillReg := agentpkg.NewSkillRegistry(sb, factory.gatewayDir)
	toolRegistry.Register(agentpkg.NewListSkillsTool(skillReg))
	toolRegistry.Register(agentpkg.NewLoadSkillTool(skillReg, toolRegistry, true))

	loop := agentpkg.NewAgentLoopWithConfig(
		provider,
		toolRegistry,
		agentCfg.Model,
		maxOutput,
		maxInput,
		sampling.Temperature,
		mergedLoop,
	)

	loop.SetSamplingParams(sampling.TopP, sampling.FrequencyPenalty, sampling.PresencePenalty)
	loop.SetModelMeta(factory.getModelMeta(agentCfg.Provider, agentCfg.Model))
	loop.SetProviderName(agentCfg.Provider)
	loop.SetUsageRecorder(func(p, m string, usage llm.Usage) {
		factory.recordTaskUsage(task.ID, p, m, usage)
	})
	if mergedLoop.NoProgressLimit > 0 {
		loop.SetNoProgressGuard(mergedLoop.NoProgressLimit, func() string {
			return workspaceProgressSnapshot(sb)
		})
	}

	if factory.getDebugConfig != nil {
		debugCfg := factory.getDebugConfig()
		if debugCfg.ConversationLog.Enabled && factory.db != nil {
			loop.SetConversationRecorder(
				newConversationRecorder(factory.db, debugCfg.ConversationLog.MaxContentChars),
				task.ID,
			)
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: req.SystemPrompt},
		{Role: "user", Content: req.Prompt},
	}

	result, err := loop.Run(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("agent loop: %w", err)
	}

	log.Printf("[INFO] Task %d builtin coding backend completed", task.ID)

	return &CodingResult{
		Summary:  result,
		Success:  true,
		Provider: provider,
	}, nil
}

// Abort is a no-op for the builtin backend; cancellation is done via the
// context passed to Run. The handle argument is unused.
func (b *BuiltinCodingBackend) Abort(ctx context.Context, handle string) error {
	_ = ctx
	_ = handle
	return nil
}

// --- Backend resolution ----------------------------------------------------

// ResolveCodingBackend determines which CodingBackend to use for a write task.
//
// Resolution order (server-runtime-design-v4.md §3.2):
//  1. Non-write tasks → always "builtin" (enforced by caller, not here)
//  2. Write tasks: agent.Backend != "" → use that name
//  3. Otherwise → agents.backends.default (default: "builtin")
//  4. If the named backend is not found in config → error
//  5. If the backend type is unknown → error
//
// Since A5 this resolver effectively only returns the builtin backend: hub
// backends (hub-opencode/hub-hermes) must carry workspace_transport=git_sync
// and are diverted into runViaHub's write channel before this resolver runs
// (see runWriteTask). A hub backend that reaches here lacks git_sync — a
// configuration error, surfaced with a migration hint instead of the generic
// "unsupported type".
func (f *RunnerFactory) ResolveCodingBackend(agent *store.Agent) (CodingBackend, error) {
	// Normalize legacy identifiers (internal → builtin, opencode_http →
	// hub-opencode) so DB rows and configs written before 1.2.6 keep working.
	name := config.NormalizeBackend(agent.Backend)
	if name == "" {
		name = config.NormalizeBackend(f.backends.Default)
	}
	if name == "" {
		name = config.BackendNameBuiltin
	}
	// The builtin backend is always available, even if missing from config
	if name == config.BackendNameBuiltin {
		return f.builtinBackend, nil
	}

	cfg, ok := f.backends.Backends[name]
	if !ok {
		return nil, fmt.Errorf("coding backend %q not found in agents.backends config", name)
	}

	// Normalize the backend type too: ApplyBackendDefaults already normalizes
	// config-loaded values, but normalize again here for configs constructed
	// directly (e.g. in tests).
	switch config.NormalizeBackend(cfg.Type) {
	case config.BackendTypeBuiltin:
		return f.builtinBackend, nil
	case config.BackendTypeHubOpenCode, config.BackendTypeHubHermes:
		// A5: the shared_path CodingBackend.Run write path was removed. Hub
		// write tasks only run through runViaHub's git_sync channel; reaching
		// here means the backend is missing workspace_transport=git_sync.
		return nil, fmt.Errorf("hub backend %q (type %q) no longer serves write tasks via a local workspace — set workspace_transport: %s (shared_path removed in A5)",
			name, cfg.Type, config.WorkspaceTransportGitSync)
	default:
		return nil, fmt.Errorf("unsupported coding backend type %q for %q", cfg.Type, name)
	}
}
