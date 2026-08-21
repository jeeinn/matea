package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// backendPasswordMask mirrors the provider api_key placeholder: a saved
// password shown as this exact string in GET /config responses, and meaning
// "keep current value" when round-tripped back through PUT /config.
const backendPasswordMask = "********"

// Valid backend type values for hub backends.
var validBackendTypes = map[string]bool{
	BackendTypeBuiltin:     true,
	BackendTypeHubOpenCode: true,
	BackendTypeHubHermes:   true,
}

// ParseAgentBackendsJSON parses the agents.backends config document. It
// validates backend types and the workspace transport, and normalizes the
// default backend. It does NOT resolve password placeholders — that happens at
// apply time against the previously saved config.
func ParseAgentBackendsJSON(raw string) (AgentBackendsConfig, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return AgentBackendsConfig{Default: BackendNameBuiltin, Backends: map[string]BackendConfig{}}, nil
	}
	var cfg AgentBackendsConfig
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return AgentBackendsConfig{}, fmt.Errorf("invalid agents.backends JSON: %w", err)
	}
	if cfg.Backends == nil {
		cfg.Backends = map[string]BackendConfig{}
	}
	for name, bc := range cfg.Backends {
		if !validBackendTypes[bc.Type] {
			return AgentBackendsConfig{}, fmt.Errorf("backend %q: invalid type %q (want builtin|hub-opencode|hub-hermes)", name, bc.Type)
		}
		if bc.Type != BackendTypeBuiltin && strings.TrimSpace(bc.BaseURL) == "" {
			return AgentBackendsConfig{}, fmt.Errorf("backend %q: base_url is required for type %q", name, bc.Type)
		}
		if !IsWorkspaceTransportValid(bc.WorkspaceTransport) {
			return AgentBackendsConfig{}, fmt.Errorf("backend %q: invalid workspace_transport %q", name, bc.WorkspaceTransport)
		}
	}
	// Normalize default: empty → builtin; must reference an existing backend
	// (builtin is always valid) or be empty.
	if cfg.Default == "" {
		cfg.Default = BackendNameBuiltin
	}
	if cfg.Default != BackendNameBuiltin {
		if _, ok := cfg.Backends[cfg.Default]; !ok {
			return AgentBackendsConfig{}, fmt.Errorf("default backend %q does not exist", cfg.Default)
		}
	}
	return cfg, nil
}

// MarshalAgentBackendsJSON serializes the backends config to JSON, masking any
// non-empty password with backendPasswordMask so GET /config never leaks
// credentials (mirrors the llm.providers api_key masking, C-17).
func MarshalAgentBackendsJSON(cfg AgentBackendsConfig) (string, error) {
	cp := cfg
	if cp.Backends == nil {
		cp.Backends = map[string]BackendConfig{}
	}
	for name, bc := range cp.Backends {
		if bc.Auth.Password != "" {
			bc.Auth.Password = backendPasswordMask
			cp.Backends[name] = bc
		}
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return "", fmt.Errorf("marshal agents.backends: %w", err)
	}
	return string(data), nil
}

// MaskSensitiveInBackendsJSON masks password fields inside an already-serialized
// agents.backends JSON string (used for display and audit redaction). If the
// input is not valid JSON it returns backendPasswordMask to avoid leaking.
func MaskSensitiveInBackendsJSON(raw string) string {
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return backendPasswordMask
	}
	if backends, ok := doc["backends"].(map[string]interface{}); ok {
		for name, rawB := range backends {
			b, ok := rawB.(map[string]interface{})
			if !ok {
				continue
			}
			if auth, ok := b["auth"].(map[string]interface{}); ok {
				if pw, ok := auth["password"].(string); ok && pw != "" {
					auth["password"] = backendPasswordMask
					b["auth"] = auth
					backends[name] = b
				}
			}
		}
		doc["backends"] = backends
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return backendPasswordMask
	}
	return string(out)
}

// ApplyAgentBackendsJSON parses raw and merges it into cfg.Agents.Backends,
// restoring the real password for any backend whose password equals the mask
// placeholder (so the admin UI can keep a masked field unchanged).
func ApplyAgentBackendsJSON(cfg *Config, raw string) error {
	next, err := ParseAgentBackendsJSON(raw)
	if err != nil {
		return err
	}
	// Restore passwords that were sent as the mask placeholder.
	for name, bc := range next.Backends {
		if bc.Auth.Password == backendPasswordMask {
			if cur, ok := cfg.Agents.Backends.Backends[name]; ok {
				bc.Auth.Password = cur.Auth.Password
				next.Backends[name] = bc
			}
		}
	}
	cfg.Agents.Backends = next
	return nil
}
