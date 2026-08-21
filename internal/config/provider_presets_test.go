package config

import "testing"

func TestDefaultProviderPresets(t *testing.T) {
	presets := DefaultProviderPresets()
	if len(presets) != 6 {
		t.Fatalf("expected 6 presets, got %d", len(presets))
	}

	byKey := make(map[string]ProviderPreset, len(presets))
	for _, p := range presets {
		if p.Key == "" || p.Label == "" || p.Type == "" {
			t.Errorf("preset %q missing a required field: %+v", p.Key, p)
		}
		if _, dup := byKey[p.Key]; dup {
			t.Errorf("duplicate preset key %q", p.Key)
		}
		byKey[p.Key] = p
	}

	// The "custom" preset must be blank so the add-provider form starts empty.
	if byKey["custom"].BaseURL != "" || byKey["custom"].Provider != "" || byKey["custom"].Model != "" {
		t.Errorf("custom preset should be empty, got %+v", byKey["custom"])
	}

	// Spot-check a couple of well-known presets.
	if byKey["deepseek"].BaseURL != "https://api.deepseek.com/v1" {
		t.Errorf("deepseek base_url = %q", byKey["deepseek"].BaseURL)
	}
	if byKey["deepseek"].Model != "deepseek-v4-flash" {
		t.Errorf("deepseek model = %q", byKey["deepseek"].Model)
	}
	if byKey["ollama"].Model != "" {
		t.Errorf("ollama should have no default model, got %q", byKey["ollama"].Model)
	}
	if byKey["anthropic"].Type != "anthropic" {
		t.Errorf("anthropic type = %q", byKey["anthropic"].Type)
	}
}
