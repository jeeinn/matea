package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceTransportConstants(t *testing.T) {
	assert.Equal(t, "git_sync", WorkspaceTransportGitSync)
	assert.Equal(t, "mcp", WorkspaceTransportMCP)
	// A5: shared_path is gone — no constant may exist for it. Pin the literal
	// here so a stray re-introduction fails this test.
	assert.NotContains(t, ValidWorkspaceTransports(), "shared_path")
}

func TestValidWorkspaceTransports(t *testing.T) {
	valid := ValidWorkspaceTransports()
	assert.Contains(t, valid, WorkspaceTransportGitSync)
	assert.Contains(t, valid, WorkspaceTransportMCP)
	assert.Len(t, valid, 2)
}

func TestIsWorkspaceTransportValid(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", true},           // empty defaults to git_sync
		{"git_sync", true},   // the only hub write transport (A5+)
		{"shared_path", false}, // removed in A5 — stale configs must fail loud
		{"mcp", false},       // Phase 3 only
		{"unknown", false},   // unknown value rejected
		{"GIT_SYNC", false},  // case-sensitive
		{"SHARED_PATH", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsWorkspaceTransportValid(tt.input))
		})
	}
}

func TestApplyBackendDefaultsSetsWorkspaceTransport(t *testing.T) {
	// Empty workspace_transport should default to git_sync
	backends := &AgentBackendsConfig{
		Default: "builtin",
		Backends: map[string]BackendConfig{
			"builtin": {Type: BackendTypeBuiltin},
			"opencode-local": {
				Type:    BackendTypeHubOpenCode,
				BaseURL: "http://localhost:8080",
			},
		},
	}

	ApplyBackendDefaults(backends)

	// Both backends should have workspace_transport defaulted to git_sync
	assert.Equal(t, WorkspaceTransportGitSync, backends.Backends["builtin"].WorkspaceTransport)
	assert.Equal(t, WorkspaceTransportGitSync, backends.Backends["opencode-local"].WorkspaceTransport)
}

func TestValidateBackendWorkspaceTransport(t *testing.T) {
	tests := []struct {
		name      string
		cfg       BackendConfig
		expectErr bool
		errContains string
	}{
		{
			name:      "empty transport is valid",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode},
			expectErr: false,
		},
		{
			name:      "git_sync is valid",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "git_sync"},
			expectErr: false,
		},
		{
			name:        "shared_path removed in A5 gets a migration error",
			cfg:         BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "shared_path"},
			expectErr:   true,
			errContains: "removed in A5",
		},
		{
			name:        "shared_path on hermes also rejected",
			cfg:         BackendConfig{Type: BackendTypeHubHermes, WorkspaceTransport: "shared_path"},
			expectErr:   true,
			errContains: "removed in A5",
		},
		{
			name:      "mcp is rejected in Phase 2",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "mcp"},
			expectErr: true,
		},
		{
			name:      "unknown transport rejected",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "unknown"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBackendWorkspaceTransport(tt.cfg)
			if tt.expectErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				} else {
					assert.Contains(t, err.Error(), "not supported")
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBackendTypeHubHermes(t *testing.T) {
	// Verify the Hermes type constant is defined
	assert.Equal(t, "hub-hermes", BackendTypeHubHermes)
}

func TestApplyBackendDefaultsDoesNotOverrideExplicitTransport(t *testing.T) {
	// If a user explicitly sets workspace_transport, keep it
	backends := &AgentBackendsConfig{
		Backends: map[string]BackendConfig{
			"opencode-local": {
				Type:               BackendTypeHubOpenCode,
				BaseURL:            "http://localhost:8080",
				WorkspaceTransport: "git_sync",
			},
		},
	}
	ApplyBackendDefaults(backends)
	assert.Equal(t, "git_sync", backends.Backends["opencode-local"].WorkspaceTransport)
}
