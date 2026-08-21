package config

import (
	"strings"
	"testing"
)

func TestParseAgentBackendsJSONValid(t *testing.T) {
	raw := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"p"},"workspace_transport":"git_sync"}}}`
	cfg, err := ParseAgentBackendsJSON(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Default != "oc1" {
		t.Fatalf("default=%q want oc1", cfg.Default)
	}
	if cfg.Backends["oc1"].Type != "hub-opencode" {
		t.Fatalf("type=%q", cfg.Backends["oc1"].Type)
	}
	if cfg.Backends["oc1"].Auth.Password != "p" {
		t.Fatalf("password=%q", cfg.Backends["oc1"].Auth.Password)
	}
}

func TestParseAgentBackendsJSONEmpty(t *testing.T) {
	for _, raw := range []string{"", "{}", "null", "  {}  "} {
		cfg, err := ParseAgentBackendsJSON(raw)
		if err != nil {
			t.Fatalf("raw=%q unexpected error: %v", raw, err)
		}
		if cfg.Default != "builtin" {
			t.Fatalf("raw=%q default=%q want builtin", raw, cfg.Default)
		}
		if len(cfg.Backends) != 0 {
			t.Fatalf("raw=%q backends=%v want empty", raw, cfg.Backends)
		}
	}
}

func TestParseAgentBackendsJSONInvalidType(t *testing.T) {
	raw := `{"default":"x","backends":{"x":{"type":"bogus","base_url":"http://x"}}}`
	if _, err := ParseAgentBackendsJSON(raw); err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestParseAgentBackendsJSONMissingBaseURL(t *testing.T) {
	raw := `{"default":"x","backends":{"x":{"type":"hub-opencode"}}}`
	if _, err := ParseAgentBackendsJSON(raw); err == nil {
		t.Fatal("expected error for missing base_url on hub backend")
	}
}

func TestParseAgentBackendsJSONDefaultNotExist(t *testing.T) {
	raw := `{"default":"ghost","backends":{"x":{"type":"hub-opencode","base_url":"http://x"}}}`
	if _, err := ParseAgentBackendsJSON(raw); err == nil {
		t.Fatal("expected error for default referencing missing backend")
	}
}

func TestParseAgentBackendsJSONInvalidTransport(t *testing.T) {
	raw := `{"default":"x","backends":{"x":{"type":"hub-opencode","base_url":"http://x","workspace_transport":"shared_path"}}}`
	if _, err := ParseAgentBackendsJSON(raw); err == nil {
		t.Fatal("expected error for removed shared_path transport")
	}
}

func TestMarshalAgentBackendsJSONMasksPassword(t *testing.T) {
	cfg := AgentBackendsConfig{
		Default: "oc1",
		Backends: map[string]BackendConfig{
			"oc1": {Type: BackendTypeHubOpenCode, BaseURL: "http://oc:8081", Auth: BackendAuthConfig{Username: "u", Password: "SECRET"}},
		},
	}
	s, err := MarshalAgentBackendsJSON(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(s, "SECRET") {
		t.Fatalf("marshal must not leak password, got %s", s)
	}
	if !strings.Contains(s, `"password":"********"`) {
		t.Fatalf("password should be masked, got %s", s)
	}
}

func TestMaskSensitiveInBackendsJSON(t *testing.T) {
	raw := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","auth":{"username":"u","password":"SECRET"}}}}`
	out := MaskSensitiveInBackendsJSON(raw)
	if strings.Contains(out, "SECRET") {
		t.Fatalf("must mask secret, got %s", out)
	}
	if !strings.Contains(out, `"password":"********"`) {
		t.Fatalf("password should be masked, got %s", out)
	}
}

func TestMaskSensitiveInBackendsJSONInvalid(t *testing.T) {
	// invalid JSON should be fully redacted rather than leaked.
	out := MaskSensitiveInBackendsJSON("not json{")
	if out != backendPasswordMask {
		t.Fatalf("invalid input should return mask, got %q", out)
	}
}

func TestApplyAgentBackendsJSONRestoresMask(t *testing.T) {
	base := &Config{
		Agents: AgentsConfig{
			Backends: AgentBackendsConfig{
				Default: "oc1",
				Backends: map[string]BackendConfig{
					"oc1": {Type: BackendTypeHubOpenCode, BaseURL: "http://oc:8081", Auth: BackendAuthConfig{Username: "u", Password: "REAL"}},
				},
			},
		},
	}
	masked := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"********"}}}}`
	if err := ApplyAgentBackendsJSON(base, masked); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.Agents.Backends.Backends["oc1"].Auth.Password != "REAL" {
		t.Fatalf("expected REAL restored, got %q", base.Agents.Backends.Backends["oc1"].Auth.Password)
	}
}

func TestApplyAgentBackendsJSONNewPassword(t *testing.T) {
	base := &Config{
		Agents: AgentsConfig{
			Backends: AgentBackendsConfig{
				Default:  "oc1",
				Backends: map[string]BackendConfig{"oc1": {Type: BackendTypeHubOpenCode, BaseURL: "http://oc:8081", Auth: BackendAuthConfig{Username: "u", Password: "OLD"}}},
			},
		},
	}
	// A new real password should replace the old one.
	updated := `{"default":"oc1","backends":{"oc1":{"type":"hub-opencode","base_url":"http://oc:8081","auth":{"username":"u","password":"NEW"}}}}`
	if err := ApplyAgentBackendsJSON(base, updated); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.Agents.Backends.Backends["oc1"].Auth.Password != "NEW" {
		t.Fatalf("expected NEW, got %q", base.Agents.Backends.Backends["oc1"].Auth.Password)
	}
}
