package config

// Config is the top-level application configuration.
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Gitea      GiteaConfig      `yaml:"gitea"`
	Workspace  WorkspaceConfig  `yaml:"workspace"`
	Dispatcher DispatcherConfig `yaml:"dispatcher"`
	Database   DatabaseConfig   `yaml:"database"`
	Logging    LoggingConfig    `yaml:"logging"`
	LLM        LLMConfig        `yaml:"llm"`
	API        APIConfig        `yaml:"api"`
	Auth       AuthConfig       `yaml:"auth"`
	Agents     AgentsConfig     `yaml:"agents"`
	Workflow   WorkflowConfig   `yaml:"workflow"`
	Session    SessionConfig    `yaml:"session"`
	Sandbox    SandboxConfig    `yaml:"sandbox"`
	MCP        MCPConfig        `yaml:"mcp"`
	Debug      DebugConfig      `yaml:"debug"`
}

// DebugConfig contains optional debug/diagnostic settings (default off).
type DebugConfig struct {
	ConversationLog ConversationLogConfig `yaml:"conversation_log"`
}

// ConversationLogConfig persists Agent Loop LLM messages to SQLite when enabled.
type ConversationLogConfig struct {
	Enabled         bool `yaml:"enabled"`
	MaxContentChars int  `yaml:"max_content_chars"` // 0 = no truncation
}

// DefaultConversationLogConfig returns default conversation log settings.
func DefaultConversationLogConfig() ConversationLogConfig {
	return ConversationLogConfig{
		Enabled:         false,
		MaxContentChars: 100000,
	}
}

// WorkflowConfig contains workflow policy configuration.
type WorkflowConfig struct {
	Preset string            `yaml:"preset"` // free | standard | strict
	Gates  map[string]string `yaml:"gates"`  // gate_id → off|soft|hard
	Notify NotifyConfig      `yaml:"notify"`
}

// NotifyConfig controls L3 comment notifications.
type NotifyConfig struct {
	OnAnalyzeDone   bool `yaml:"on_analyze_done"`
	OnCoderPROpened bool `yaml:"on_coder_pr_opened"`
	OnGateSoft      bool `yaml:"on_gate_soft"`
	OnGateHard      bool `yaml:"on_gate_hard"`
}

// SessionConfig contains session lifecycle configuration.
type SessionConfig struct {
	IdleTTL            string `yaml:"idle_ttl"`            // Duration string, e.g. "168h" (7 days)
	WorkspaceRetention string `yaml:"workspace_retention"` // Duration string, e.g. "24h"
	PRClosedRetention  string `yaml:"pr_closed_retention"` // Duration string, e.g. "168h"
	MaxDiskPerRepo     string `yaml:"max_disk_per_repo"`   // e.g. "5GB"
}

// DefaultSessionConfig returns default session configuration.
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		IdleTTL:            "168h", // 7 days
		WorkspaceRetention: "24h",  // 24 hours
		PRClosedRetention:  "168h", // 7 days
		MaxDiskPerRepo:     "5GB",
	}
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type GiteaConfig struct {
	URL           string `yaml:"url"`
	AdminToken    string `yaml:"admin_token"`
	WebhookSecret string `yaml:"webhook_secret"`
}

type WorkspaceConfig struct {
	BaseDir      string `yaml:"base_dir"`
	CleanupAfter string `yaml:"cleanup_after"`
	MaxDiskUsage string `yaml:"max_disk_usage"`
}

type DispatcherConfig struct {
	MaxConcurrent    int `yaml:"max_concurrent"`
	TaskRetryCount   int `yaml:"task_retry_count"` // whole-task retries after runner failure; 0 = no retry
	QueueSize        int `yaml:"queue_size"`
	RateLimitBackoff int `yaml:"rate_limit_backoff"` // seconds to wait on HTTP 429; 0 = disabled

	// AgentConcurrency controls whether one agent may run multiple issues at once.
	// "parallel" (default): only issue-level in-flight lock.
	// "serial_queue": at most one running task per agent_id; others stay pending and wait.
	AgentConcurrency string `yaml:"agent_concurrency"`

	// Deprecated: use TaskRetryCount. Kept for YAML/DB migration only.
	RetryCount int `yaml:"retry_count,omitempty"`
}

// Agent concurrency modes for DispatcherConfig.AgentConcurrency.
const (
	AgentConcurrencyParallel    = "parallel"
	AgentConcurrencySerialQueue = "serial_queue"
)

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type LoggingConfig struct {
	Level string `yaml:"level"`
	Path  string `yaml:"path"`
}

type LLMConfig struct {
	// Providers 是 **builtin** 后端的 LLM 配置来源。
	// hub-* 后端（如 hub-opencode）由 Hub 自身管理 LLM，不读取此处配置。
	Providers        map[string]ProviderConfig `yaml:"providers"`
	Defaults         LLMDefaultsConfig         `yaml:"defaults"`
	RateLimitRetries int                       `yaml:"rate_limit_retries"` // retries after HTTP 429; 0 = no retry (still needs rate_limit_backoff > 0)
}

type ProviderConfig struct {
	BaseURL       string            `yaml:"base_url" json:"base_url"`
	APIKey        string            `yaml:"api_key" json:"api_key"`
	Type          string            `yaml:"type" json:"type"` // openai_compatible | anthropic
	DefaultParams ModelParams       `yaml:"default_params" json:"default_params"`
	Models        []ModelDefinition `yaml:"models" json:"models"`
}

// ModelDefinition holds metadata for a single LLM model.
type ModelDefinition struct {
	ID            string      `yaml:"id" json:"id"`
	Name          string      `yaml:"name" json:"name"`
	ContextWindow int         `yaml:"context_window" json:"context_window"`
	MaxOutput     int         `yaml:"max_output" json:"max_output"`
	SupportsTools bool        `yaml:"supports_tools" json:"supports_tools"`
	IsReasoning   bool        `yaml:"is_reasoning" json:"is_reasoning"`
	DefaultParams ModelParams `yaml:"default_params" json:"default_params"`
	Description   string      `yaml:"description" json:"description"`
	InputPrice    float64     `yaml:"input_price" json:"input_price"`
	OutputPrice   float64     `yaml:"output_price" json:"output_price"`
}

// ModelParams holds per-model or per-provider default generation parameters.
type ModelParams struct {
	Temperature      *float64 `yaml:"temperature" json:"temperature,omitempty"`
	TopP             *float64 `yaml:"top_p" json:"top_p,omitempty"`
	MaxOutputTokens  *int     `yaml:"max_output_tokens" json:"max_output_tokens,omitempty"`
	FrequencyPenalty *float64 `yaml:"frequency_penalty" json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `yaml:"presence_penalty" json:"presence_penalty,omitempty"`
}

// LLMDefaultsConfig holds LLM connectivity defaults (provider/model only).
type LLMDefaultsConfig struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
}

// AgentDefaultsConfig holds defaults for new agents and single-shot task budgets.
type AgentDefaultsConfig struct {
	Provider        string  `yaml:"provider"`
	Model           string  `yaml:"model"`
	MaxOutputTokens int     `yaml:"max_output_tokens"`
	MaxInputTokens  int     `yaml:"max_input_tokens"`
	Temperature     float64 `yaml:"temperature"`
	Timeout         string  `yaml:"timeout"` // Go duration, e.g. "5m" — single-shot tasks
}

// APIConfig contains API server configuration.
type APIConfig struct {
	AuthToken string `yaml:"auth_token"`
}

// AuthConfig contains authentication configuration.
type AuthConfig struct {
	JWTSecret            string `yaml:"jwt_secret"`
	JWTExpiration        string `yaml:"jwt_expiration"`
	DefaultAdminPassword string `yaml:"default_admin_password"`
}

// AgentsConfig contains agent templates and defaults.
type AgentsConfig struct {
	Defaults  AgentDefaultsConfig            `yaml:"defaults"`
	Templates map[string]AgentTemplateConfig `yaml:"templates"`
	Loop      AgentLoopConfig                `yaml:"loop"`
	Backends  AgentBackendsConfig            `yaml:"backends"`
	ToolPacks ToolPacksConfig                `yaml:"tool_packs"`
}

// SandboxMode defines the workspace directory mode.
type SandboxMode string

const (
	// SandboxModeTemp uses os.MkdirTemp for automatic temporary directories.
	SandboxModeTemp SandboxMode = "temp"
	// SandboxModeFixed uses a fixed base directory with task subdirectories.
	SandboxModeFixed SandboxMode = "fixed"
)

// SandboxConfig contains sandbox configuration for isolation and safety.
type SandboxConfig struct {
	Mode           SandboxMode `yaml:"mode"`            // "temp" | "fixed"
	BaseDir        string      `yaml:"base_dir"`        // Fixed mode base directory
	CommandTimeout string      `yaml:"command_timeout"` // Single command timeout (duration string)
	TaskTimeout    string      `yaml:"task_timeout"`    // Total task timeout (duration string)
	MaxOutput      int         `yaml:"max_output"`      // Max output bytes per command
	MaxFileSize    int         `yaml:"max_file_size"`   // Max file size for write operations
	CleanupAfter   string      `yaml:"cleanup_after"`   // Failed task retention time (duration string)
}

// DefaultSandboxConfig returns default sandbox configuration.
func DefaultSandboxConfig() SandboxConfig {
	return SandboxConfig{
		Mode:           SandboxModeFixed,
		BaseDir:        "./workspace",
		CommandTimeout: "5m",
		TaskTimeout:    "30m",
		MaxOutput:      1024 * 1024, // 1MB
		MaxFileSize:    1024 * 1024, // 1MB
		CleanupAfter:   "24h",
	}
}

// ToolPacksConfig defines named tool packs that map pack IDs to ordered tool
// name lists. The runner uses these lists to assemble a ToolRegistry via
// AssembleToolRegistry. Built-in defaults (coder-default, analyze-readonly)
// are applied when the config is empty.
type ToolPacksConfig struct {
	Packs map[string]ToolPackConfig `yaml:"packs"`
}

// ToolPackConfig is one named pack: an ordered list of tool names.
type ToolPackConfig struct {
	Tools []string `yaml:"tools"`
}

// AgentBackendsConfig holds coding-backend definitions for write tasks.
// Non-write tasks (Analyze/Review/Reply) always use the implicit `builtin` backend
// regardless of this config. See server-runtime-design-v4.md §3 / §4.4.
type AgentBackendsConfig struct {
	Default  string                   `yaml:"default"`  // backend name; empty → "builtin"
	Backends map[string]BackendConfig `yaml:"backends"` // named backends; "builtin" is implicit
}

// BackendConfig describes one coding backend. Type distinguishes builtin vs opencode.
type BackendConfig struct {
	Type                  string                   `yaml:"type"`                    // builtin | hub-opencode
	BaseURL               string                   `yaml:"base_url"`                // hub-opencode only
	Auth                  BackendAuthConfig        `yaml:"auth"`                    // hub-opencode only
	Timeout               string                   `yaml:"timeout"`                 // e.g. "45m"
	WorkspaceMode         string                   `yaml:"workspace_mode"`          // first release: "matea_path" only
	HealthCheck           BackendHealthCheckConfig `yaml:"health_check"`            // hub-opencode only
	AllowFallbackBuiltin  bool                     `yaml:"allow_fallback_builtin"`  // default false
}

// BackendAuthConfig holds HTTP Basic auth credentials for a hub-opencode backend.
type BackendAuthConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
}

// BackendHealthCheckConfig configures a periodic readiness probe for a backend.
type BackendHealthCheckConfig struct {
	Path     string `yaml:"path"`     // e.g. "/global/health"
	Interval string `yaml:"interval"` // e.g. "30s"
}

// Backend type constants.
const (
	BackendTypeBuiltin     = "builtin"
	BackendTypeHubOpenCode = "hub-opencode"
)

// Canonical backend names (task 1.2.6).
const (
	// BackendNameBuiltin is the canonical name of the built-in coding backend.
	BackendNameBuiltin = "builtin"
)

// Legacy backend identifiers accepted during the 1.2.6 transition.
const (
	legacyBackendInternal     = "internal"
	legacyBackendOpenCodeHTTP = "opencode_http"
)

// NormalizeBackend maps legacy backend identifiers to canonical names:
// "internal" → "builtin", "opencode_http" → "hub-opencode". Everything else
// (including "") passes through unchanged. Applied at config load and at
// coding-backend resolution so both old and new identifiers are accepted
// (task 1.2.6a — lands first to remove ordering dependencies).
func NormalizeBackend(name string) string {
	switch name {
	case legacyBackendInternal:
		return BackendNameBuiltin
	case legacyBackendOpenCodeHTTP:
		return BackendTypeHubOpenCode
	default:
		return name
	}
}

// DefaultAgentBackends returns the default backends config: a single implicit builtin.
func DefaultAgentBackends() AgentBackendsConfig {
	return AgentBackendsConfig{
		Default: BackendNameBuiltin,
		Backends: map[string]BackendConfig{
			BackendNameBuiltin: {Type: BackendTypeBuiltin},
		},
	}
}

// AgentTemplateConfig is a template for creating agents.
type AgentTemplateConfig struct {
	Name         string   `yaml:"name"`
	Type         string   `yaml:"type"`
	SystemPrompt string   `yaml:"system_prompt"`
	UserTemplate string   `yaml:"user_template"`
	Permissions  []string `yaml:"permissions"`
}

// AgentLoopConfig contains agent loop configuration (multi-turn tasks only).
type AgentLoopConfig struct {
	MaxIterations      int      `yaml:"max_iterations"`      // Max iteration rounds (default 20)
	TotalTimeout       string   `yaml:"total_timeout"`       // Total loop task timeout (default "30m")
	IterationInterval  int      `yaml:"iteration_interval"`  // Seconds between loop rounds (default 0)
	NoProgressLimit    int      `yaml:"no_progress_limit"`   // Consecutive tool rounds with unchanged workspace → stop; 0 = disabled
	VerifyCommands     []string `yaml:"verify_commands"`     // Shell commands run after coding, before commit/PR; empty = skip
	IndependentChecker bool     `yaml:"independent_checker"` // After coding, fresh LLM PASS/FAIL on git diff (no loop history)
}

// DefaultAgentLoopConfig returns default agent loop configuration.
func DefaultAgentLoopConfig() AgentLoopConfig {
	return AgentLoopConfig{
		MaxIterations:      20,
		TotalTimeout:       "30m",
		IterationInterval:  0,
		NoProgressLimit:    3, // harness: stall detection on by default for write loops
		VerifyCommands:     nil,
		IndependentChecker: false, // opt-in: Maker≠Checker LLM gate before commit/PR
	}
}

// MCPConfig holds MCP (Model Context Protocol) server definitions.
// MCP tools are merged into the ToolRegistry per-agent based on the
// agent's mcp_servers enable list.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

// MCPServerConfig describes one MCP server connection.
// Transport is HTTP (Streamable HTTP / JSON-RPC) for remote servers;
// stdio transport can be added later.
type MCPServerConfig struct {
	BaseURL string `yaml:"base_url"` // e.g. "http://localhost:3000/mcp"
	APIKey  string `yaml:"api_key"`  // Bearer token auth; empty = no auth
	Timeout string `yaml:"timeout"`  // Go duration string, e.g. "30s"
}

// DefaultMCPConfig returns empty MCP config (no servers defined).
func DefaultMCPConfig() MCPConfig {
	return MCPConfig{
		Servers: make(map[string]MCPServerConfig),
	}
}

// Mainstream fallback token budgets when model metadata is unavailable.
// Aligned with typical 128K-context models (GPT-4o / Claude / many OpenAI-compatible APIs).
const (
	DefaultMaxOutputTokens = 8192
	DefaultMaxInputTokens  = 115200 // 128000 * 0.9
)

// DefaultAgentDefaults returns default agent budget/timeout settings.
// When model metadata is available, Agent max_*=0 resolves to that model instead.
func DefaultAgentDefaults() AgentDefaultsConfig {
	return AgentDefaultsConfig{
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		MaxOutputTokens: DefaultMaxOutputTokens,
		MaxInputTokens:  DefaultMaxInputTokens,
		Temperature:     0.3,
		Timeout:         "5m",
	}
}
