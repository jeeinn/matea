package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadResult holds the loaded config and whether a bootstrap file was created.
type LoadResult struct {
	Config           *Config
	BootstrapCreated bool
	BootstrapPath    string
}

// Load reads the configuration from the given YAML file path.
// If the file does not exist, a minimal bootstrap config is written first
// (random jwt_secret), then loaded. Environment variables (${VAR} / ${VAR:-default})
// are expanded as usual.
func Load(path string) (*Config, error) {
	res, err := LoadWithBootstrap(path)
	if err != nil {
		return nil, err
	}
	return res.Config, nil
}

// LoadWithBootstrap is like Load but reports whether a bootstrap file was created.
func LoadWithBootstrap(path string) (*LoadResult, error) {
	created := false
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := WriteBootstrapConfig(path); err != nil {
			return nil, fmt.Errorf("bootstrap config: %w", err)
		}
		created = true
	} else if err != nil {
		return nil, fmt.Errorf("stat config file: %w", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	expanded := expandEnvVars(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config file: %w", err)
	}

	applyDefaults(&cfg)
	if err := ValidateAgentLoopConfig(cfg.Agents.Loop); err != nil {
		return nil, err
	}
	return &LoadResult{
		Config:           &cfg,
		BootstrapCreated: created,
		BootstrapPath:    path,
	}, nil
}

// ValidateAgentLoopConfig checks agents.loop ranges after defaults are applied.
// max_iterations: 1–100; total_timeout: parseable duration in [1m, 1h].
// no_progress_limit: 0 (off) or 1–100.
func ValidateAgentLoopConfig(loop AgentLoopConfig) error {
	if loop.MaxIterations < 1 || loop.MaxIterations > 100 {
		return fmt.Errorf("agents.loop.max_iterations must be 1-100, got %d", loop.MaxIterations)
	}
	if loop.TotalTimeout == "" {
		return fmt.Errorf("agents.loop.total_timeout is required")
	}
	d, err := time.ParseDuration(loop.TotalTimeout)
	if err != nil {
		return fmt.Errorf("agents.loop.total_timeout: %w", err)
	}
	if d < time.Minute || d > time.Hour {
		return fmt.Errorf("agents.loop.total_timeout must be between 1m and 1h, got %s", loop.TotalTimeout)
	}
	if loop.NoProgressLimit < 0 || loop.NoProgressLimit > 100 {
		return fmt.Errorf("agents.loop.no_progress_limit must be 0-100, got %d", loop.NoProgressLimit)
	}
	return nil
}

// expandEnvVars replaces ${VAR} or ${VAR:-default} patterns with env values.
func expandEnvVars(s string) string {
	return os.Expand(s, func(key string) string {
		if idx := strings.Index(key, ":-"); idx >= 0 {
			envKey := key[:idx]
			defaultVal := key[idx+2:]
			if val := os.Getenv(envKey); val != "" {
				return val
			}
			return defaultVal
		}
		return os.Getenv(key)
	})
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Workspace.BaseDir == "" {
		cfg.Workspace.BaseDir = "./data/work"
	}
	if cfg.Workspace.CleanupAfter == "" {
		cfg.Workspace.CleanupAfter = "24h"
	}
	if cfg.Dispatcher.MaxConcurrent == 0 {
		cfg.Dispatcher.MaxConcurrent = 3
	}
	// Migrate deprecated dispatcher.retry_count → task_retry_count
	if cfg.Dispatcher.TaskRetryCount == 0 && cfg.Dispatcher.RetryCount > 0 {
		cfg.Dispatcher.TaskRetryCount = cfg.Dispatcher.RetryCount
	}
	if cfg.Dispatcher.TaskRetryCount == 0 {
		cfg.Dispatcher.TaskRetryCount = 1
	}
	cfg.Dispatcher.RetryCount = 0 // clear deprecated field after migration
	switch cfg.Dispatcher.AgentConcurrency {
	case "", AgentConcurrencyParallel:
		cfg.Dispatcher.AgentConcurrency = AgentConcurrencyParallel
	case AgentConcurrencySerialQueue:
		// ok
	default:
		cfg.Dispatcher.AgentConcurrency = AgentConcurrencyParallel
	}
	if cfg.LLM.RateLimitRetries == 0 {
		cfg.LLM.RateLimitRetries = 1
	}
	if cfg.Dispatcher.QueueSize == 0 {
		cfg.Dispatcher.QueueSize = 100
	}
	if cfg.Database.Path == "" {
		cfg.Database.Path = "./data/matea.db"
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.LLM.Defaults.Provider == "" {
		cfg.LLM.Defaults.Provider = "deepseek"
	}
	if cfg.LLM.Defaults.Model == "" {
		cfg.LLM.Defaults.Model = "deepseek-v4-flash"
	}
	defs := DefaultAgentDefaults()
	if cfg.Agents.Defaults.Provider == "" {
		cfg.Agents.Defaults.Provider = defs.Provider
	}
	if cfg.Agents.Defaults.Model == "" {
		cfg.Agents.Defaults.Model = defs.Model
	}
	if cfg.Agents.Defaults.MaxOutputTokens == 0 {
		cfg.Agents.Defaults.MaxOutputTokens = defs.MaxOutputTokens
	}
	if cfg.Agents.Defaults.MaxInputTokens == 0 {
		cfg.Agents.Defaults.MaxInputTokens = defs.MaxInputTokens
	}
	if cfg.Agents.Defaults.Temperature == 0 {
		cfg.Agents.Defaults.Temperature = defs.Temperature
	}
	if cfg.Agents.Defaults.Timeout == "" {
		cfg.Agents.Defaults.Timeout = defs.Timeout
	}
	if cfg.Agents.Loop.MaxIterations <= 0 {
		cfg.Agents.Loop.MaxIterations = DefaultAgentLoopConfig().MaxIterations
	}
	if cfg.Agents.Loop.TotalTimeout == "" {
		cfg.Agents.Loop.TotalTimeout = DefaultAgentLoopConfig().TotalTimeout
	}
	if cfg.Auth.JWTSecret == "" {
		cfg.Auth.JWTSecret = "change-this-in-production"
	}
	if cfg.Auth.JWTExpiration == "" {
		cfg.Auth.JWTExpiration = "24h"
	}
	if cfg.Auth.DefaultAdminPassword == "" {
		cfg.Auth.DefaultAdminPassword = "admin123"
	}
	if cfg.Debug.ConversationLog.MaxContentChars == 0 && !cfg.Debug.ConversationLog.Enabled {
		cfg.Debug.ConversationLog = DefaultConversationLogConfig()
	} else if cfg.Debug.ConversationLog.MaxContentChars == 0 {
		cfg.Debug.ConversationLog.MaxContentChars = DefaultConversationLogConfig().MaxContentChars
	}
	ApplyToolPackDefaults(&cfg.Agents.ToolPacks)
	ApplyBackendDefaults(&cfg.Agents.Backends)
	applySandboxDefaults(&cfg.Sandbox)
	alignWorkspacePaths(cfg)
}

// alignWorkspacePaths makes sandbox.base_dir inherit workspace.base_dir when
// unset or still at the historical default ("./workspace"). Session workspaces
// use workspace.base_dir/sessions/...; task sandboxes use sandbox.base_dir/task_*.
// Sharing one root avoids the dual-base_dir split noted in Path A / P1.6.
func alignWorkspacePaths(cfg *Config) {
	legacyDefault := DefaultSandboxConfig().BaseDir // "./workspace"
	if cfg.Sandbox.BaseDir == "" || cfg.Sandbox.BaseDir == legacyDefault {
		cfg.Sandbox.BaseDir = cfg.Workspace.BaseDir
	}
}

// DefaultToolPacks returns the built-in tool pack definitions.
// These are used when the config does not override them.
func DefaultToolPacks() ToolPacksConfig {
	return ToolPacksConfig{
		Packs: map[string]ToolPackConfig{
			"coder-default": {
				Tools: []string{
					"read_file", "write_file", "list_files", "search_code", "rg",
					"run_command", "apply_diff", "tree", "git_log", "git_blame",
				},
			},
			"analyze-readonly": {
				Tools: []string{
					"list_files", "rg", "search_code", "read_file", "tree", "git_log",
				},
			},
		},
	}
}

// ApplyToolPackDefaults fills in built-in packs when the config is empty.
// User-defined packs in config override built-in ones with the same name.
func ApplyToolPackDefaults(tpc *ToolPacksConfig) {
	defaults := DefaultToolPacks()
	if tpc.Packs == nil {
		tpc.Packs = make(map[string]ToolPackConfig)
	}
	for name, def := range defaults.Packs {
		if _, ok := tpc.Packs[name]; !ok {
			tpc.Packs[name] = def
		}
	}
}

// ApplyBackendDefaults normalizes legacy backend identifiers (internal →
// builtin, opencode_http → hub-opencode) and ensures the builtin backend
// exists and is the default when none is set. Non-write tasks always use the
// builtin backend regardless. Exported for use by runners / other packages
// that construct backends independently.
func ApplyBackendDefaults(backends *AgentBackendsConfig) {
	backends.Default = NormalizeBackend(backends.Default)
	if backends.Default == "" {
		backends.Default = BackendNameBuiltin
	}
	if backends.Backends == nil {
		backends.Backends = map[string]BackendConfig{}
	}
	// Normalize legacy map keys and legacy backend types. If a legacy key and
	// a canonical key both exist (e.g. user defined both `internal:` and
	// `builtin:`), whichever the map yields first wins — such configs are
	// pathological and not supported.
	normalized := make(map[string]BackendConfig, len(backends.Backends)+1)
	for name, b := range backends.Backends {
		b.Type = NormalizeBackend(b.Type)
		canonical := NormalizeBackend(name)
		if _, exists := normalized[canonical]; !exists {
			normalized[canonical] = b
		}
	}
	backends.Backends = normalized
	// Ensure the builtin backend entry exists and is typed.
	if b, ok := backends.Backends[BackendNameBuiltin]; ok {
		if b.Type == "" {
			b.Type = BackendTypeBuiltin
			backends.Backends[BackendNameBuiltin] = b
		}
	} else {
		backends.Backends[BackendNameBuiltin] = BackendConfig{Type: BackendTypeBuiltin}
	}
}

func applySandboxDefaults(cfg *SandboxConfig) {
	def := DefaultSandboxConfig()
	if cfg.Mode == "" {
		cfg.Mode = def.Mode
	}
	if cfg.BaseDir == "" {
		cfg.BaseDir = def.BaseDir
	}
	if cfg.CommandTimeout == "" {
		cfg.CommandTimeout = def.CommandTimeout
	}
	if cfg.TaskTimeout == "" {
		cfg.TaskTimeout = def.TaskTimeout
	}
	if cfg.MaxOutput == 0 {
		cfg.MaxOutput = def.MaxOutput
	}
	if cfg.MaxFileSize == 0 {
		cfg.MaxFileSize = def.MaxFileSize
	}
	if cfg.CleanupAfter == "" {
		cfg.CleanupAfter = def.CleanupAfter
	}
}
