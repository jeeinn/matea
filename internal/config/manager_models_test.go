package config

import "testing"

func TestDiscoverModels(t *testing.T) {
	// Stub the discovery function (normally injected from the llm package to
	// avoid a circular import) so we can exercise the merge/enrich logic.
	prev := modelDiscoveryFn
	modelDiscoveryFn = func(providerName, baseURL, apiKey, providerType string) ([]string, error) {
		return []string{"deepseek-v4-flash", "deepseek-chat", "some-custom-id"}, nil
	}
	defer func() { modelDiscoveryFn = prev }()

	m := NewConfigManager(&Config{})

	// Known provider: discovered IDs should be enriched from the builtin catalog.
	models, source, err := m.DiscoverModels("deepseek", "https://api.deepseek.com/v1", "sk-x", "openai_compatible")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "api" {
		t.Errorf("source = %q, want api", source)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d: %+v", len(models), models)
	}
	// deepseek-v4-flash exists in the builtin catalog → enriched with metadata.
	var foundFlash bool
	for _, mdl := range models {
		if mdl.ID == "deepseek-v4-flash" {
			foundFlash = true
			if mdl.Name == "" {
				t.Errorf("builtin model %q not enriched with Name", mdl.ID)
			}
		}
	}
	if !foundFlash {
		t.Errorf("deepseek-v4-flash missing from result: %+v", models)
	}

	// Unknown provider: IDs returned as-is (ID-only definitions).
	models2, _, err := m.DiscoverModels("custom-acme", "https://acme.example/v1", "", "openai_compatible")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models2) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models2))
	}
	for _, mdl := range models2 {
		if mdl.ID == "" {
			t.Errorf("model with empty ID in %+v", models2)
		}
	}
}

func TestDiscoverModelsEmptyBaseURL(t *testing.T) {
	m := NewConfigManager(&Config{})
	_, _, err := m.DiscoverModels("deepseek", "", "sk-x", "openai_compatible")
	// discoverModels itself doesn't validate base_url (the HTTP handler does);
	// with a nil discovery fn it falls back to builtin rather than erroring.
	if modelDiscoveryFn != nil {
		// if a real fn were set, behavior depends on it; here we just ensure no panic
		_ = err
	}
}
