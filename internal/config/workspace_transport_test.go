package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceTransportConstants(t *testing.T) {
	assert.Equal(t, "shared_path", WorkspaceTransportSharedPath)
	assert.Equal(t, "git_sync", WorkspaceTransportGitSync)
	assert.Equal(t, "mcp", WorkspaceTransportMCP)
}

func TestValidWorkspaceTransports(t *testing.T) {
	valid := ValidWorkspaceTransports()
	assert.Contains(t, valid, WorkspaceTransportSharedPath)
	assert.Contains(t, valid, WorkspaceTransportGitSync)
	assert.Contains(t, valid, WorkspaceTransportMCP)
}

func TestIsWorkspaceTransportValid(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"", true},             // empty defaults to shared_path
		{"shared_path", true},  // Phase 2 default
		{"git_sync", true},     // A1–A4 coexistence window accepts git_sync
		{"mcp", false},         // Phase 3, rejected in Phase 2
		{"unknown", false},     // unknown value rejected
		{"SHARED_PATH", false}, // case-sensitive
		{"GIT_SYNC", false},    // case-sensitive
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsWorkspaceTransportValid(tt.input))
		})
	}
}

func TestApplyBackendDefaultsSetsWorkspaceTransport(t *testing.T) {
	// Empty workspace_transport should default to shared_path
	backends := &AgentBackendsConfig{
		Default: "builtin",
		Backends: map[string]BackendConfig{
			"builtin": {Type: BackendTypeBuiltin},
			"opencode-local": {
				Type:   BackendTypeHubOpenCode,
				BaseURL: "http://localhost:8080",
			},
		},
	}

	ApplyBackendDefaults(backends)

	// Both backends should have workspace_transport defaulted to shared_path
	assert.Equal(t, WorkspaceTransportSharedPath, backends.Backends["builtin"].WorkspaceTransport)
	assert.Equal(t, WorkspaceTransportSharedPath, backends.Backends["opencode-local"].WorkspaceTransport)
}

func TestValidateBackendWorkspaceTransport(t *testing.T) {
	tests := []struct {
		name      string
		cfg       BackendConfig
		expectErr bool
	}{
		{
			name:      "empty transport is valid",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode},
			expectErr: false,
		},
		{
			name:      "shared_path is valid",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "shared_path"},
			expectErr: false,
		},
		{
			name:      "git_sync is valid in coexistence window",
			cfg:       BackendConfig{Type: BackendTypeHubOpenCode, WorkspaceTransport: "git_sync"},
			expectErr: false,
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
				assert.Contains(t, err.Error(), "not supported")
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
	// If a user explicitly sets workspace_transport to shared_path, keep it
	backends := &AgentBackendsConfig{
		Backends: map[string]BackendConfig{
			"opencode-local": {
				Type:               BackendTypeHubOpenCode,
				BaseURL:            "http://localhost:8080",
				WorkspaceTransport: "shared_path",
			},
		},
	}

	ApplyBackendDefaults(backends)
	assert.Equal(t, "shared_path", backends.Backends["opencode-local"].WorkspaceTransport)
}
