package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateAgentLoopConfig(t *testing.T) {
	assert.NoError(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 20, TotalTimeout: "30m"}))
	assert.NoError(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 1, TotalTimeout: "1m"}))
	assert.NoError(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 100, TotalTimeout: "1h"}))

	assert.Error(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 0, TotalTimeout: "30m"}))
	assert.Error(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 101, TotalTimeout: "30m"}))
	assert.Error(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 20, TotalTimeout: "30s"}))
	assert.Error(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 20, TotalTimeout: "2h"}))
	assert.Error(t, ValidateAgentLoopConfig(AgentLoopConfig{MaxIterations: 20, TotalTimeout: "not-a-duration"}))
}

func TestAlignWorkspacePathsInheritsSandboxBaseDir(t *testing.T) {
	cfg := &Config{
		Workspace: WorkspaceConfig{BaseDir: "./data/work"},
		Sandbox:   SandboxConfig{BaseDir: "./workspace"}, // legacy default
	}
	applySandboxDefaults(&cfg.Sandbox)
	alignWorkspacePaths(cfg)
	assert.Equal(t, "./data/work", cfg.Sandbox.BaseDir)
}

func TestAlignWorkspacePathsPreservesExplicitSandbox(t *testing.T) {
	cfg := &Config{
		Workspace: WorkspaceConfig{BaseDir: "./data/work"},
		Sandbox:   SandboxConfig{BaseDir: "./data/sandbox-custom"},
	}
	alignWorkspacePaths(cfg)
	assert.Equal(t, "./data/sandbox-custom", cfg.Sandbox.BaseDir)
}

func TestLoadRejectsInvalidLoopConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agents:
  loop:
    max_iterations: 999
    total_timeout: "30m"
`), 0644))
	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_iterations")
}

func TestLoadEmptyYAMLAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "0.0.0.0", cfg.Server.Host)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "./data/work", cfg.Workspace.BaseDir)
	assert.Equal(t, "./data/matea.db", cfg.Database.Path)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.Equal(t, "deepseek", cfg.LLM.Defaults.Provider)
	assert.Equal(t, "deepseek-v4-flash", cfg.LLM.Defaults.Model)
	assert.Equal(t, "builtin", cfg.Agents.Backends.Default)
	assert.Equal(t, "./data/work", cfg.Sandbox.BaseDir) // aligned from workspace
	assert.Equal(t, DefaultAgentLoopConfig().MaxIterations, cfg.Agents.Loop.MaxIterations)
	assert.Equal(t, DefaultAgentLoopConfig().TotalTimeout, cfg.Agents.Loop.TotalTimeout)
}

func TestLoadMinimalYAMLAppliesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
gitea:
  url: "http://localhost:3000"
llm:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com/v1"
      api_key: "test-key"
  defaults:
    provider: "deepseek"
    model: "deepseek-v4-flash"
auth:
  jwt_secret: "test-secret"
`), 0644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://localhost:3000", cfg.Gitea.URL)
	assert.Equal(t, "./data/work", cfg.Workspace.BaseDir)
	assert.Equal(t, "./data/matea.db", cfg.Database.Path)
	assert.Equal(t, "test-secret", cfg.Auth.JWTSecret)
	assert.Contains(t, cfg.LLM.Providers, "deepseek")
}

func TestApplyBackendDefaultsSetsInternal(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)

	assert.Equal(t, "builtin", cfg.Agents.Backends.Default)
	assert.Contains(t, cfg.Agents.Backends.Backends, "builtin")
	assert.Equal(t, BackendTypeBuiltin, cfg.Agents.Backends.Backends["builtin"].Type)
}

func TestApplyBackendDefaultsPreservesExplicitDefault(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Backends: AgentBackendsConfig{
				Default: "opencode-local",
				Backends: map[string]BackendConfig{
					"opencode-local": {Type: BackendTypeOpenCodeHTTP, BaseURL: "http://127.0.0.1:4096"},
				},
			},
		},
	}
	applyDefaults(cfg)

	// Explicit default preserved; builtin still ensured; legacy type normalized
	assert.Equal(t, "opencode-local", cfg.Agents.Backends.Default)
	assert.Contains(t, cfg.Agents.Backends.Backends, "builtin")
	assert.Contains(t, cfg.Agents.Backends.Backends, "opencode-local")
	assert.Equal(t, BackendNameHubOpenCode, cfg.Agents.Backends.Backends["opencode-local"].Type)
}

func TestApplyBackendDefaultsBackfillsInternalType(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Backends: AgentBackendsConfig{
				Backends: map[string]BackendConfig{
					"internal": {}, // legacy key, type empty
				},
			},
		},
	}
	applyDefaults(cfg)

	// Legacy key normalized to canonical "builtin"; type backfilled
	assert.Equal(t, "builtin", cfg.Agents.Backends.Default)
	assert.Equal(t, BackendTypeBuiltin, cfg.Agents.Backends.Backends["builtin"].Type)
	assert.NotContains(t, cfg.Agents.Backends.Backends, "internal")
}

func TestLoadGeneratesBootstrapIfMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	res, err := LoadWithBootstrap(path)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.BootstrapCreated)
	assert.Equal(t, path, res.BootstrapPath)
	require.FileExists(t, path)

	cfg := res.Config
	require.NotNil(t, cfg)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Equal(t, "./data/matea.db", cfg.Database.Path)
	assert.Equal(t, "./data/work", cfg.Workspace.BaseDir)
	assert.Equal(t, "info", cfg.Logging.Level)
	assert.NotEmpty(t, cfg.Auth.JWTSecret)
	assert.NotContains(t, cfg.Auth.JWTSecret, "change-me")
	assert.NotEqual(t, "change-this-in-production", cfg.Auth.JWTSecret)
	assert.Equal(t, "admin123", cfg.Auth.DefaultAdminPassword)

	// Second load must not recreate
	res2, err := LoadWithBootstrap(path)
	require.NoError(t, err)
	assert.False(t, res2.BootstrapCreated)
	assert.Equal(t, cfg.Auth.JWTSecret, res2.Config.Auth.JWTSecret)
}

func TestWriteBootstrapConfigRandomSecret(t *testing.T) {
	dir := t.TempDir()
	path1 := filepath.Join(dir, "a.yaml")
	path2 := filepath.Join(dir, "b.yaml")
	require.NoError(t, WriteBootstrapConfig(path1))
	require.NoError(t, WriteBootstrapConfig(path2))

	c1, err := Load(path1)
	require.NoError(t, err)
	c2, err := Load(path2)
	require.NoError(t, err)
	assert.NotEqual(t, c1.Auth.JWTSecret, c2.Auth.JWTSecret)
	assert.GreaterOrEqual(t, len(c1.Auth.JWTSecret), 32)
}

func TestCheckSetupIncomplete(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	st := CheckSetup(cfg)
	assert.True(t, st.SetupRequired)
	assert.False(t, st.GiteaOK)
	assert.False(t, st.LLMOK)
	assert.Contains(t, st.Missing, "gitea.url")
	assert.Contains(t, st.Missing, "gitea.admin_token")
	assert.Contains(t, st.Missing, "gitea.webhook_secret")
	assert.Contains(t, st.Missing, "llm.providers")
}

func TestCheckSetupComplete(t *testing.T) {
	cfg := &Config{
		Gitea: GiteaConfig{
			URL:           "http://localhost:3000",
			AdminToken:    "token",
			WebhookSecret: "secret",
		},
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {
					BaseURL: "https://api.deepseek.com/v1",
					APIKey:  "sk-test",
				},
			},
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-v4-flash"},
		},
	}
	st := CheckSetup(cfg)
	assert.False(t, st.SetupRequired)
	assert.True(t, st.GiteaOK)
	assert.True(t, st.LLMOK)
	assert.Empty(t, st.Missing)
}

func TestCheckSetupLocalLLMWithoutAPIKey(t *testing.T) {
	cfg := &Config{
		Gitea: GiteaConfig{
			URL:           "http://gitea",
			AdminToken:    "t",
			WebhookSecret: "s",
		},
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"ollama": {BaseURL: "http://127.0.0.1:11434/v1"},
			},
			Defaults: LLMDefaultsConfig{Provider: "ollama", Model: "llama3"},
		},
	}
	st := CheckSetup(cfg)
	assert.False(t, st.SetupRequired)
	assert.True(t, st.LLMOK)
}

func TestIsLikelyLocalLLM(t *testing.T) {
	assert.True(t, isLikelyLocalLLM("http://127.0.0.1:11434/v1"))
	assert.True(t, isLikelyLocalLLM("http://localhost:11434"))
	assert.True(t, isLikelyLocalLLM("http://[::1]:11434/v1"))
	assert.True(t, isLikelyLocalLLM("http://192.168.1.10:11434"))
	assert.True(t, isLikelyLocalLLM("http://10.0.0.5:11434"))
	assert.True(t, isLikelyLocalLLM("http://172.16.0.2:11434"))
	assert.False(t, isLikelyLocalLLM("https://api.deepseek.com/v1"))
	assert.False(t, isLikelyLocalLLM(""))
}
