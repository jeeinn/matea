package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeBackend verifies legacy identifier mapping (task 1.2.6a).
func TestNormalizeBackend(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"internal", "builtin"},
		{"opencode_http", "hub-opencode"},
		{"builtin", "builtin"},               // already canonical
		{"hub-opencode", "hub-opencode"},     // already canonical
		{"opencode-local", "opencode-local"}, // user-defined names pass through
		{"hub-hermes", "hub-hermes"},
		{"", ""}, // empty passes through
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeBackend(tt.input))
		})
	}
}

// TestApplyBackendDefaultsNormalizesLegacyYAML verifies a config file written
// with pre-1.2.6 identifiers still resolves correctly after loading.
func TestApplyBackendDefaultsNormalizesLegacyYAML(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Backends: AgentBackendsConfig{
				Default: "internal", // legacy default
				Backends: map[string]BackendConfig{
					"internal":       {Type: "builtin"},
					"opencode-local": {Type: "opencode_http", BaseURL: "http://127.0.0.1:4096"},
				},
			},
		},
	}
	applyDefaults(cfg)

	assert.Equal(t, "builtin", cfg.Agents.Backends.Default)
	assert.Contains(t, cfg.Agents.Backends.Backends, "builtin")
	assert.NotContains(t, cfg.Agents.Backends.Backends, "internal")
	assert.Equal(t, BackendTypeHubOpenCode, cfg.Agents.Backends.Backends["opencode-local"].Type)
	assert.Equal(t, "http://127.0.0.1:4096", cfg.Agents.Backends.Backends["opencode-local"].BaseURL)
}
