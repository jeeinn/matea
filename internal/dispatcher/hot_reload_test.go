package dispatcher

import (
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetAgentsConfigHotReloadsBackends covers the hot-reload gap that made
// WebUI-added hub backends invisible until restart: ConfigManager.Update swaps
// a new config snapshot, but the dispatcher/executor kept the startup one.
// SetAgentsConfig must rebuild the runner factory so a newly configured hub
// backend resolves immediately, and refresh agent defaults/loop alongside.
func TestSetAgentsConfigHotReloadsBackends(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	d := NewDispatcher(db, &config.GiteaConfig{}, &config.DispatcherConfig{MaxConcurrent: 1, QueueSize: 10},
		&llm.Registry{}, &config.AgentsConfig{}, sandbox.DefaultConfig(), config.DefaultMCPConfig())

	// Before reload: the hub backend does not resolve.
	_, err := d.executor.getRunnerFactory().ResolveHubBackend(&store.Agent{Backend: "hub-opencode-local"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured in agents.backends")

	// Hot apply a config carrying the hub backend — no restart.
	d.SetAgentsConfig(&config.AgentsConfig{
		Defaults: config.AgentDefaultsConfig{Timeout: "7m"},
		Backends: config.AgentBackendsConfig{
			Backends: map[string]config.BackendConfig{
				"hub-opencode-local": {
					Type:    config.BackendTypeHubOpenCode,
					BaseURL: "http://127.0.0.1:4096",
				},
			},
		},
	})

	backend, err := d.executor.getRunnerFactory().ResolveHubBackend(&store.Agent{Backend: "hub-opencode-local"})
	require.NoError(t, err)
	assert.Equal(t, "hub-opencode-local", backend.Name())

	// Agent defaults hot-applied too: non-loop task timeout comes from the new defaults.
	assert.Equal(t, 7*time.Minute, d.executor.resolveTaskTimeout("analyze_issue", &store.Agent{}))

	// Nil config is a no-op (never clears the live config, never panics).
	d.SetAgentsConfig(nil)
	_, err = d.executor.getRunnerFactory().ResolveHubBackend(&store.Agent{Backend: "hub-opencode-local"})
	assert.NoError(t, err)
}

// TestSetAgentsConfigRemovesDroppedBackend pins the symmetric case: a backend
// removed from config must stop resolving after hot reload.
func TestSetAgentsConfigRemovesDroppedBackend(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	d := NewDispatcher(db, &config.GiteaConfig{}, &config.DispatcherConfig{MaxConcurrent: 1, QueueSize: 10},
		&llm.Registry{}, &config.AgentsConfig{
			Backends: config.AgentBackendsConfig{
				Backends: map[string]config.BackendConfig{
					"hub-old": {Type: config.BackendTypeHubOpenCode, BaseURL: "http://127.0.0.1:4096"},
				},
			},
		}, sandbox.DefaultConfig(), config.DefaultMCPConfig())

	_, err := d.executor.getRunnerFactory().ResolveHubBackend(&store.Agent{Backend: "hub-old"})
	require.NoError(t, err)

	d.SetAgentsConfig(&config.AgentsConfig{}) // backend removed

	_, err = d.executor.getRunnerFactory().ResolveHubBackend(&store.Agent{Backend: "hub-old"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured in agents.backends")
}
