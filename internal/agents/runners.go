package agents

import (
	"context"
	"log"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/deliver"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/mcp"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

const (
	fallbackMaxOutput = config.DefaultMaxOutputTokens
	fallbackMaxInput  = config.DefaultMaxInputTokens
	fallbackTemp      = 0.3
	fallbackTimeout   = "5m"
)

// Runner is the interface for task execution strategies.
type Runner interface {
	// Run executes the task and returns the result.
	Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error)
}

// Result contains the output of a task execution.
type Result struct {
	Content    string                 // Main content (comment body)
	Action     string                 // Optional action: "comment", "label", "pr"
	PRID       int                    // PR number created by DevRunner (0 if no PR created)
	ActionData map[string]interface{} // Additional data for the action
}

// GiteaClientFactory creates Gitea clients.
type GiteaClientFactory interface {
	GetGiteaClient(token string) *gitea.Client
	GetAdminGiteaClient() *gitea.Client
}

// ModelMetaProvider can return model metadata for a given provider+model.
type ModelMetaProvider interface {
	GetModelMeta(provider, model string) *config.ModelDefinition
	// GetProviderDefaultParams returns provider-level default_params (may be zero).
	GetProviderDefaultParams(provider string) config.ModelParams
}

// SamplingParams holds generation controls for ChatCompletion.
// Temperature always has a concrete value; TopP / penalties use 0 to mean "omit" (API default).
type SamplingParams struct {
	Temperature      float64
	TopP             float64
	FrequencyPenalty float64
	PresencePenalty  float64
}

// ApplyTo sets sampling fields on a chat request.
func (p SamplingParams) ApplyTo(req *llm.ChatRequest) {
	if req == nil {
		return
	}
	req.Temperature = p.Temperature
	req.TopP = p.TopP
	req.FrequencyPenalty = p.FrequencyPenalty
	req.PresencePenalty = p.PresencePenalty
}

// RunnerFactory creates runners based on task type.
type RunnerFactory struct {
	llmRegistry      *llm.Registry
	giteaFactory     GiteaClientFactory
	sandboxCfg       sandbox.Config
	db               *store.DB
	defaultMaxOutput int
	defaultMaxInput  int
	defaultTemp      float64
	defaultTimeout   string
	defaultLoop      config.AgentLoopConfig
	getDebugConfig   func() config.DebugConfig
	modelMeta        ModelMetaProvider
	backends         config.AgentBackendsConfig // coding backends (Path A)
	builtinBackend   *BuiltinCodingBackend      // always available, built from this factory
	hubRegistry      *HubBackendRegistry        // builtin + configured hub-* backends (1.2.4 dispatch)
	toolPacks        config.ToolPacksConfig     // built-in + user-defined tool packs
	mcpRegistry      *mcp.Registry              // MCP server registry (nil = no MCP)
	gatewayDir       string                     // gateway root directory for SKILL.md scanning
	deliverClient    *deliver.Client            // outbound event fan-out (task 2.3.3); nil = disabled
	deployKeyIssuer  DeployKeyIssuer            // git_sync deploy key issuer; nil until task A6 wiring (tests inject fakes)
}

// SetDeliverClient injects the outbound deliver client used to fan out
// DeliverRequest events returned by hub backends. A nil client disables
// deliver (the events are logged and dropped). Safe to call multiple times —
// the executor re-injects it whenever the runner factory is rebuilt.
func (f *RunnerFactory) SetDeliverClient(c *deliver.Client) {
	f.deliverClient = c
}

// SetDeployKeyIssuer injects the git_sync deploy key issuer (task A6 wires the
// Gitea implementation at startup; tests inject fakes). A nil issuer makes
// git_sync Prepare fail loudly, which keeps shared_path the working transport
// during the A1–A4 coexistence window.
func (f *RunnerFactory) SetDeployKeyIssuer(issuer DeployKeyIssuer) {
	f.deployKeyIssuer = issuer
}

// gitSyncTransportFor returns the git_sync WorkspaceTransport when the backend
// is configured with workspace_transport=git_sync, or nil otherwise. The
// transport struct is cheap to build per call and always reads the current
// issuer, so hot-injection via SetDeployKeyIssuer takes effect immediately.
func (f *RunnerFactory) gitSyncTransportFor(backend HubBackend) WorkspaceTransport {
	if backend == nil {
		return nil
	}
	cfg, ok := f.backends.Backends[backend.Name()]
	if !ok || cfg.WorkspaceTransport != config.WorkspaceTransportGitSync {
		return nil
	}
	return NewGitSyncTransport(f.giteaFactory, f.deployKeyIssuer, f.sandboxCfg.BaseDir)
}

// isWriteTaskType reports whether the task type produces code changes (and is
// therefore eligible for the git_sync write path under a hub backend).
func isWriteTaskType(taskType string) bool {
	switch taskType {
	case "solve_issue", "solve_comment", "fix_bug":
		return true
	default:
		return false
	}
}

// NewRunnerFactory creates a new RunnerFactory from agent defaults and loop config.
// The backends, toolPacks, and mcpReg configs are optional — nil/empty falls back to defaults.
func NewRunnerFactory(llmRegistry *llm.Registry, giteaFactory GiteaClientFactory, db *store.DB, defaults config.AgentDefaultsConfig, defaultLoop config.AgentLoopConfig, getDebugConfig func() config.DebugConfig, backends *config.AgentBackendsConfig, toolPacks *config.ToolPacksConfig, sandboxCfg sandbox.SandboxConfig, mcpReg *mcp.Registry, gatewayDir string) *RunnerFactory {
	maxOut := defaults.MaxOutputTokens
	if maxOut <= 0 {
		maxOut = fallbackMaxOutput
	}
	maxIn := defaults.MaxInputTokens
	if maxIn <= 0 {
		maxIn = fallbackMaxInput
	}
	temp := defaults.Temperature
	if temp <= 0 {
		temp = fallbackTemp
	}
	timeout := defaults.Timeout
	if timeout == "" {
		timeout = fallbackTimeout
	}
	if defaultLoop.MaxIterations <= 0 {
		defaultLoop = config.DefaultAgentLoopConfig()
	}

	beCfg := config.DefaultAgentBackends()
	if backends != nil {
		beCfg = *backends
		config.ApplyBackendDefaults(&beCfg)
	}

	tpCfg := config.DefaultToolPacks()
	if toolPacks != nil {
		tpCfg = *toolPacks
		config.ApplyToolPackDefaults(&tpCfg)
	}

	factory := &RunnerFactory{
		llmRegistry:      llmRegistry,
		giteaFactory:     giteaFactory,
		sandboxCfg:       sandboxCfg,
		db:               db,
		defaultMaxOutput: maxOut,
		defaultMaxInput:  maxIn,
		defaultTemp:      temp,
		defaultTimeout:   timeout,
		defaultLoop:      defaultLoop,
		getDebugConfig:   getDebugConfig,
		backends:         beCfg,
		toolPacks:        tpCfg,
		mcpRegistry:      mcpReg,
		gatewayDir:       gatewayDir,
	}
	factory.builtinBackend = NewBuiltinCodingBackend(factory)

	// Hub backend registry (1.2.4): builtin always registered; configured
	// hub-opencode instances registered as shared singletons so HubBackend
	// Submit→Poll affinity holds (their outcome cache is instance-local).
	// Instances that fail construction are skipped here — ResolveCodingBackend
	// surfaces the construction error when the backend is actually used.
	factory.hubRegistry = NewHubBackendRegistry()
	factory.hubRegistry.Register(NewBuiltinHubBackend(factory))
	for name, cfg := range beCfg.Backends {
		switch config.NormalizeBackend(cfg.Type) {
		case config.BackendTypeHubOpenCode:
			oc, err := NewOpenCodeHTTPBackend(name, cfg)
			if err != nil {
				log.Printf("[WARN] hub backend %q not registered: %v", name, err)
				continue
			}
			factory.hubRegistry.Register(oc)
		case config.BackendTypeHubHermes:
			hb, err := buildHubBackend(name, cfg)
			if err != nil {
				log.Printf("[WARN] hub backend %q not registered: %v", name, err)
				continue
			}
			factory.hubRegistry.Register(hb)
		}
	}
	return factory
}

// SetModelMetaProvider sets the model metadata provider for adaptive token limits.
func (f *RunnerFactory) SetModelMetaProvider(m ModelMetaProvider) {
	f.modelMeta = m
}

// resolveMaxOutputTokens priority: Agent explicit > model max_output > agents.defaults > fallback.
// agentMax == 0 means "use model default" (not agents.defaults).
func (f *RunnerFactory) resolveMaxOutputTokens(agentMax int, provider, model string) int {
	var meta *config.ModelDefinition
	if f.modelMeta != nil {
		meta = f.modelMeta.GetModelMeta(provider, model)
	}

	if agentMax > 0 {
		if meta != nil && meta.MaxOutput > 0 && agentMax > meta.MaxOutput {
			return meta.MaxOutput
		}
		return agentMax
	}

	if meta != nil && meta.MaxOutput > 0 {
		return meta.MaxOutput
	}
	if f.defaultMaxOutput > 0 {
		return f.defaultMaxOutput
	}
	return fallbackMaxOutput
}

// resolveMaxInputTokens priority: Agent explicit > model context_window*90% > agents.defaults > fallback.
// agentMax == 0 means "use model default" (not agents.defaults).
func (f *RunnerFactory) resolveMaxInputTokens(agentMax int, provider, model string) int {
	var meta *config.ModelDefinition
	if f.modelMeta != nil {
		meta = f.modelMeta.GetModelMeta(provider, model)
	}

	modelLimit := 0
	if meta != nil && meta.ContextWindow > 0 {
		modelLimit = int(float64(meta.ContextWindow) * 0.9)
	}

	if agentMax > 0 {
		if modelLimit > 0 && agentMax > modelLimit {
			return modelLimit
		}
		return agentMax
	}

	if modelLimit > 0 {
		return modelLimit
	}
	if f.defaultMaxInput > 0 {
		return f.defaultMaxInput
	}
	return fallbackMaxInput
}

// resolveTemperature returns agent.Temperature if explicitly set (> 0), otherwise the factory default.
// Note: Temperature=0 (deterministic output) is a valid value but rarely used in practice.
// Agents with Temperature=0 will fall back to default — set it via Agent edit if needed.
func (f *RunnerFactory) resolveTemperature(agentTemp float64, provider, model string) float64 {
	base := f.defaultTemp
	if agentTemp > 0 {
		base = agentTemp
	}
	if f.modelMeta != nil {
		if meta := f.modelMeta.GetModelMeta(provider, model); meta != nil {
			if p := meta.DefaultParams.Temperature; p != nil && *p > 0 && agentTemp <= 0 {
				return *p
			}
		}
	}
	if base <= 0 {
		return fallbackTemp
	}
	return base
}

// resolveSamplingParams resolves temperature plus optional top_p / penalty params.
// Optional floats: model default_params → provider default_params → 0 (omit from request).
func (f *RunnerFactory) resolveSamplingParams(agentTemp float64, provider, model string) SamplingParams {
	sp := SamplingParams{
		Temperature: f.resolveTemperature(agentTemp, provider, model),
	}
	var modelParams, providerParams config.ModelParams
	if f.modelMeta != nil {
		if meta := f.modelMeta.GetModelMeta(provider, model); meta != nil {
			modelParams = meta.DefaultParams
		}
		providerParams = f.modelMeta.GetProviderDefaultParams(provider)
	}
	sp.TopP = firstFloat(modelParams.TopP, providerParams.TopP)
	sp.FrequencyPenalty = firstFloat(modelParams.FrequencyPenalty, providerParams.FrequencyPenalty)
	sp.PresencePenalty = firstFloat(modelParams.PresencePenalty, providerParams.PresencePenalty)
	return sp
}

func firstFloat(values ...*float64) float64 {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return 0
}

func (f *RunnerFactory) getModelMeta(provider, model string) *config.ModelDefinition {
	if f.modelMeta == nil {
		return nil
	}
	return f.modelMeta.GetModelMeta(provider, model)
}

func (f *RunnerFactory) recordTaskUsage(taskID int64, provider, model string, usage llm.Usage) {
	if f.db == nil {
		return
	}
	go func() {
		cost := 0.0
		if f.modelMeta != nil {
			if meta := f.modelMeta.GetModelMeta(provider, model); meta != nil {
				// InputPrice/OutputPrice are $/1K tokens
				cost = (float64(usage.PromptTokens)*meta.InputPrice + float64(usage.CompletionTokens)*meta.OutputPrice) / 1000.0
			}
		}
		if err := f.db.CreateTaskUsage(&store.TaskUsage{
			TaskID:           taskID,
			Provider:         provider,
			Model:            model,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
			Cost:             cost,
		}); err != nil {
			log.Printf("[WARN] Failed to record task usage: %v", err)
		}
	}()
}

func (f *RunnerFactory) resolveTimeout(agentTimeout string) string {
	if agentTimeout != "" {
		return agentTimeout
	}
	if f.defaultTimeout != "" {
		return f.defaultTimeout
	}
	return fallbackTimeout
}

// resolveToolPack returns the tool pack ID for a task based on task type.
// Role-based defaults (agent-level override can be added later when the
// Agent schema gains a tool_pack column):
//   - write tasks (dev/bugfix) → "coder-default"
//   - analyze tasks → "analyze-readonly"
func (f *RunnerFactory) resolveToolPack(taskType string) string {
	switch taskType {
	case "analyze_issue", "trigger", "review_pr", "reply_comment":
		return "analyze-readonly"
	default:
		return "coder-default"
	}
}

// GetRunner returns the appropriate runner for the task type.
func (f *RunnerFactory) GetRunner(taskType string) Runner {
	switch taskType {
	case "review_pr":
		return NewReviewRunner(f)
	case "reply_comment":
		return NewInteractionRunner(f)
	case "analyze_issue", "trigger":
		return NewAnalyzeRunner(f)
	case "solve_issue", "solve_comment":
		return NewDevRunner(f)
	case "fix_bug":
		return NewBugfixRunner(f)
	default:
		return NewAnalyzeRunner(f)
	}
}
