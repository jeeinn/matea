package config

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockConfigStore struct {
	data map[string]string
}

func (m *mockConfigStore) GetConfig(key string) (string, error) {
	return m.data[key], nil
}

func (m *mockConfigStore) SetConfig(key, value string) error {
	m.data[key] = value
	return nil
}

func (m *mockConfigStore) DeleteConfig(key string) error {
	delete(m.data, key)
	return nil
}

func (m *mockConfigStore) ListConfigs() (map[string]string, error) {
	out := make(map[string]string, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out, nil
}

func TestGetDisplayMapPrefersDBAndFallsBackToFile(t *testing.T) {
	fileCfg := &Config{
		Gitea: GiteaConfig{
			URL:           "http://file-gitea:3000",
			AdminToken:    "file-token",
			WebhookSecret: "file-secret",
		},
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{
				Provider: "deepseek",
				Model:    "deepseek-chat",
			},
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-file-key-12345"},
			},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: AgentDefaultsConfig{
				Provider:        "deepseek",
				Model:           "deepseek-chat",
				MaxOutputTokens: 4096,
				MaxInputTokens:  8192,
				Temperature:     0.3,
				Timeout:         "5m",
			},
		},
	}

	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{
		data: map[string]string{
			"gitea.url": "http://db-gitea:3000",
		},
	})

	display, err := m.GetDisplayMap()
	require.NoError(t, err)

	assert.Equal(t, "http://db-gitea:3000", display["gitea.url"])
	assert.Equal(t, "file-token", display["gitea.admin_token"])
	assert.Equal(t, "deepseek-chat", display["llm.defaults.model"])

	// Providers in display should be REAL values (not masked) — for editing
	providers := display["llm.providers"].(map[string]ProviderConfig)
	assert.Equal(t, "sk-file-key-12345", providers["deepseek"].APIKey, "display providers should have real api_key for editing")
	assert.Equal(t, "https://api.deepseek.com/v1", providers["deepseek"].BaseURL)

	meta := display["_meta"].(map[string]interface{})
	sources := meta["sources"].(map[string]string)
	assert.Equal(t, "db", sources["gitea.url"])
	assert.Equal(t, "file", sources["gitea.admin_token"])
	assert.Equal(t, "file", sources["llm.providers"])
}

func TestGetDisplayMapTreatsEmptyDBValueAsUnset(t *testing.T) {
	fileCfg := &Config{
		Gitea: GiteaConfig{URL: "http://file-gitea:3000"},
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-chat"},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: AgentDefaultsConfig{
				Provider:        "deepseek",
				Model:           "deepseek-chat",
				MaxOutputTokens: 4096,
				MaxInputTokens:  8192,
				Temperature:     0.3,
				Timeout:         "5m",
			},
		},
	}

	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{
		data: map[string]string{
			"gitea.url": "",
		},
	})

	display, err := m.GetDisplayMap()
	require.NoError(t, err)
	assert.Equal(t, "http://file-gitea:3000", display["gitea.url"])

	meta := display["_meta"].(map[string]interface{})
	sources := meta["sources"].(map[string]string)
	assert.Equal(t, "file", sources["gitea.url"])
}

func TestGetDisplayMapSemanticEmptyJSONFallsBack(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-chat"},
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-file-abcde"},
				"openai":   {BaseURL: "https://api.openai.com/v1", APIKey: "sk-open-key"},
			},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: AgentDefaultsConfig{
				Provider:        "deepseek",
				Model:           "deepseek-chat",
				MaxOutputTokens: 4096,
				MaxInputTokens:  8192,
				Temperature:     0.3,
				Timeout:         "5m",
			},
		},
	}

	tests := []struct {
		name    string
		dbVal   string
		wantSrc string
	}{
		{"empty JSON object {}", "{}", "file"},
		{"null string", "null", "file"},
		{"whitespace-padded empty JSON", "  {}  ", "file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewConfigManager(fileCfg)
			m.SetStore(&mockConfigStore{
				data: map[string]string{
					"llm.providers": tt.dbVal,
				},
			})

			display, err := m.GetDisplayMap()
			require.NoError(t, err)

			meta := display["_meta"].(map[string]interface{})
			sources := meta["sources"].(map[string]string)
			assert.Equal(t, tt.wantSrc, sources["llm.providers"], "source for llm.providers")

			// Display should show file config providers with REAL api_key (for editing)
			providers := display["llm.providers"].(map[string]ProviderConfig)
			assert.Equal(t, 2, len(providers))
			assert.Equal(t, "sk-file-abcde", providers["deepseek"].APIKey)
			assert.Equal(t, "sk-open-key", providers["openai"].APIKey)
		})
	}
}

func TestGetDisplayMapDBProvidersMasked(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-chat"},
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-file-key"},
			},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: AgentDefaultsConfig{
				Provider:        "deepseek",
				Model:           "deepseek-chat",
				MaxOutputTokens: 4096,
				MaxInputTokens:  8192,
				Temperature:     0.3,
				Timeout:         "5m",
			},
		},
	}

	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{
		data: map[string]string{
			"llm.providers": `{"deepseek":{"base_url":"https://api.deepseek.com/v1","api_key":"sk-db-long-key-1234567890"}}`,
		},
	})

	display, err := m.GetDisplayMap()
	require.NoError(t, err)

	// Display should have REAL values (not masked) — for editing
	providers := display["llm.providers"].(map[string]ProviderConfig)
	assert.Equal(t, "sk-db-long-key-1234567890", providers["deepseek"].APIKey, "display should show real api_key for editing")
	assert.Equal(t, "https://api.deepseek.com/v1", providers["deepseek"].BaseURL)

	meta := display["_meta"].(map[string]interface{})
	sources := meta["sources"].(map[string]string)
	assert.Equal(t, "db", sources["llm.providers"])
}

func TestIsSemanticallyEmptyConfigValue(t *testing.T) {
	tests := []struct {
		key  string
		val  string
		want bool
	}{
		{"llm.providers", "{}", true},
		{"llm.providers", "null", true},
		{"llm.providers", "  {}  ", true},
		{"llm.providers", `{"deepseek":{"base_url":"https://api.deepseek.com"}}`, false},
		{"llm.providers", "", false}, // empty string is handled by TrimSpace check, not this func
		{"gitea.url", "{}", false},   // non-JSON key — not semantically empty
		{"gitea.url", "http://example.com", false},
		{"agents.loop.verify_commands", "null", true},
		{"agents.loop.verify_commands", "[]", false}, // empty array = explicit disable
		{"agents.loop.verify_commands", `["go test ./..."]`, false},
	}
	for _, tt := range tests {
		t.Run(tt.key+"="+tt.val, func(t *testing.T) {
			assert.Equal(t, tt.want, isSemanticallyEmptyConfigValue(tt.key, tt.val))
		})
	}
}

func TestDebugConversationLogConfigRoundTrip(t *testing.T) {
	fileCfg := &Config{
		Debug: DebugConfig{ConversationLog: DefaultConversationLogConfig()},
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-chat"},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: DefaultAgentDefaults(),
			Loop:     DefaultAgentLoopConfig(),
		},
	}

	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{data: map[string]string{}})

	require.NoError(t, m.Update("debug.conversation_log.enabled", "true"))
	require.NoError(t, m.Update("debug.conversation_log.max_content_chars", "5000"))

	display, err := m.GetDisplayMap()
	require.NoError(t, err)
	assert.Equal(t, true, display["debug.conversation_log.enabled"])
	assert.Equal(t, 5000, display["debug.conversation_log.max_content_chars"])
	assert.True(t, m.Get().Debug.ConversationLog.Enabled)
}

func TestCommentHistoryLimitConfigRoundTrip(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Defaults: LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-chat"},
		},
		Dispatcher: DispatcherConfig{MaxConcurrent: 2, TaskRetryCount: 1},
		Agents: AgentsConfig{
			Defaults: DefaultAgentDefaults(),
			Loop:     DefaultAgentLoopConfig(),
		},
	}

	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{data: map[string]string{}})

	// Explicit value round-trips through display map and live config.
	require.NoError(t, m.Update("dispatcher.comment_history_limit", "3"))
	display, err := m.GetDisplayMap()
	require.NoError(t, err)
	assert.Equal(t, 3, display["dispatcher.comment_history_limit"])
	assert.Equal(t, 3, m.Get().Dispatcher.CommentHistoryLimit)

	// 0 normalizes to the system default.
	require.NoError(t, m.Update("dispatcher.comment_history_limit", "0"))
	assert.Equal(t, DefaultCommentHistoryLimit, m.Get().Dispatcher.CommentHistoryLimit)

	// Negative disables injection (kept as-is).
	require.NoError(t, m.Update("dispatcher.comment_history_limit", "-1"))
	assert.Equal(t, -1, m.Get().Dispatcher.CommentHistoryLimit)

	// Non-numeric rejected.
	require.Error(t, m.Update("dispatcher.comment_history_limit", "abc"))
}

func TestGetProviderModelsReturnsBuiltinCatalog(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	models, source, err := m.GetProviderModels("deepseek")
	require.NoError(t, err)
	assert.Equal(t, "builtin", source)
	require.GreaterOrEqual(t, len(models), 1, "deepseek should have builtin models")

	// Verify known model exists
	found := false
	for _, mm := range models {
		if mm.ID == "deepseek-v4-flash" {
			found = true
			assert.Equal(t, "DeepSeek V4 Flash", mm.Name)
			assert.Equal(t, 1000000, mm.ContextWindow)
			assert.True(t, mm.SupportsTools)
			assert.False(t, mm.IsReasoning)
		}
	}
	assert.True(t, found, "deepseek-v4-flash should be in builtin catalog")
}

func TestGetProviderModelsReturnsCustomModels(t *testing.T) {
	customModels := []ModelDefinition{
		{ID: "custom-model", Name: "Custom Model", ContextWindow: 32000, SupportsTools: true},
	}
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"myprovider": {
					BaseURL: "https://example.com",
					APIKey:  "sk-test",
					Models:  customModels,
				},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	models, source, err := m.GetProviderModels("myprovider")
	require.NoError(t, err)
	assert.Equal(t, "custom", source)
	require.Len(t, models, 1)
	assert.Equal(t, "custom-model", models[0].ID)
	assert.Equal(t, "Custom Model", models[0].Name)
}

func TestGetProviderModelsReturnsEmptyForUnknownProvider(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{},
		},
	}
	m := NewConfigManager(fileCfg)

	_, _, err := m.GetProviderModels("unknown")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetDisplayMapIncludesModelsMeta(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
				"openai":   {BaseURL: "https://api.openai.com/v1", APIKey: "sk-test"},
			},
		},
		Agents: AgentsConfig{
			Defaults: DefaultAgentDefaults(),
			Loop:     DefaultAgentLoopConfig(),
		},
	}
	m := NewConfigManager(fileCfg)

	display, err := m.GetDisplayMap()
	require.NoError(t, err)

	meta := display["_meta"].(map[string]interface{})
	modelsMeta := meta["models"].(map[string][]ModelDefinition)
	require.NotNil(t, modelsMeta)
	assert.GreaterOrEqual(t, len(modelsMeta["deepseek"]), 1, "deepseek models should be present")
	assert.GreaterOrEqual(t, len(modelsMeta["openai"]), 1, "openai models should be present")
}

func TestProviderConfigFromMapAllFields(t *testing.T) {
	m := map[string]interface{}{
		"base_url": "https://api.example.com/v1",
		"api_key":  "sk-test-123",
		"type":     "openai_compatible",
		"default_params": map[string]interface{}{
			"temperature":       0.7,
			"top_p":             0.9,
			"max_output_tokens": float64(2048),
		},
		"models": []interface{}{
			map[string]interface{}{
				"id":             "model-1",
				"name":           "Model One",
				"context_window": float64(128000),
				"max_output":     float64(4096),
				"supports_tools": true,
				"is_reasoning":   false,
			},
			map[string]interface{}{
				"id":   "model-2",
				"name": "Model Two",
			},
		},
	}

	pc := ProviderConfigFromMap(m)
	assert.Equal(t, "https://api.example.com/v1", pc.BaseURL)
	assert.Equal(t, "sk-test-123", pc.APIKey)
	assert.Equal(t, "openai_compatible", pc.Type)

	// default_params
	assert.NotNil(t, pc.DefaultParams.Temperature)
	assert.Equal(t, 0.7, *pc.DefaultParams.Temperature)
	assert.NotNil(t, pc.DefaultParams.TopP)
	assert.Equal(t, 0.9, *pc.DefaultParams.TopP)
	assert.NotNil(t, pc.DefaultParams.MaxOutputTokens)
	assert.Equal(t, 2048, *pc.DefaultParams.MaxOutputTokens)

	// models
	require.Len(t, pc.Models, 2)
	assert.Equal(t, "model-1", pc.Models[0].ID)
	assert.Equal(t, "Model One", pc.Models[0].Name)
	assert.Equal(t, 128000, pc.Models[0].ContextWindow)
	assert.Equal(t, 4096, pc.Models[0].MaxOutput)
	assert.True(t, pc.Models[0].SupportsTools)
	assert.False(t, pc.Models[0].IsReasoning)
	assert.Equal(t, "model-2", pc.Models[1].ID)
}

func TestProviderConfigFromMapPascalCase(t *testing.T) {
	m := map[string]interface{}{
		"BaseURL": "https://api.example.com/v1",
		"APIKey":  "sk-pascal",
		"Type":    "anthropic",
	}

	pc := ProviderConfigFromMap(m)
	assert.Equal(t, "https://api.example.com/v1", pc.BaseURL)
	assert.Equal(t, "sk-pascal", pc.APIKey)
	assert.Equal(t, "anthropic", pc.Type)
}

func TestCopyConfigDeepCopiesModels(t *testing.T) {
	src := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"test": {
					BaseURL: "https://example.com",
					APIKey:  "sk-test",
					Models: []ModelDefinition{
						{ID: "m1", Name: "Model 1"},
						{ID: "m2", Name: "Model 2"},
					},
				},
			},
		},
	}

	dst := copyConfig(src)

	// Modify src models
	src.LLM.Providers["test"].Models[0].Name = "Modified"

	// dst should be unchanged
	assert.Equal(t, "Model 1", dst.LLM.Providers["test"].Models[0].Name, "dst models should not be affected by src changes")
}

func TestConfigManagerGetReturnsIsolatedCopy(t *testing.T) {
	m := NewConfigManager(&Config{
		Server: ServerConfig{Host: "127.0.0.1", Port: 8080},
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"openai": {Type: "openai_compatible", BaseURL: "http://x", APIKey: "k"},
			},
		},
	})

	snap := m.Get()
	snap.Server.Port = 9999
	snap.LLM.Providers["openai"] = ProviderConfig{Type: "anthropic", APIKey: "mutated"}

	live := m.Get()
	assert.Equal(t, 8080, live.Server.Port)
	assert.Equal(t, "openai_compatible", live.LLM.Providers["openai"].Type)
	assert.Equal(t, "k", live.LLM.Providers["openai"].APIKey)
}

func TestInvalidateModelCache(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	// First call populates cache
	_, _, err := m.GetProviderModels("deepseek")
	require.NoError(t, err)

	// Verify cache exists
	m.mu.RLock()
	_, ok := m.modelCache["deepseek"]
	m.mu.RUnlock()
	assert.True(t, ok, "cache should exist after first call")

	// Invalidate
	m.InvalidateModelCache("deepseek")

	// Verify cache is gone
	m.mu.RLock()
	_, ok = m.modelCache["deepseek"]
	m.mu.RUnlock()
	assert.False(t, ok, "cache should be cleared after invalidation")
}

func TestInvalidateAllModelCache(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
				"openai":   {BaseURL: "https://api.openai.com/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	// Populate caches
	m.GetProviderModels("deepseek")
	m.GetProviderModels("openai")

	m.mu.RLock()
	cacheCount := len(m.modelCache)
	m.mu.RUnlock()
	assert.Equal(t, 2, cacheCount, "should have 2 cached entries")

	// Invalidate all
	m.InvalidateAllModelCache()

	m.mu.RLock()
	cacheCount = len(m.modelCache)
	m.mu.RUnlock()
	assert.Equal(t, 0, cacheCount, "all caches should be cleared")
}

func TestUpdateProvidersInvalidatesCache(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)
	m.SetStore(&mockConfigStore{data: map[string]string{}})

	// Populate cache
	m.GetProviderModels("deepseek")

	m.mu.RLock()
	_, ok := m.modelCache["deepseek"]
	m.mu.RUnlock()
	assert.True(t, ok, "cache should exist")

	// Update providers
	err := m.Update("llm.providers", `{"deepseek":{"base_url":"https://new-url.com/v1","api_key":"sk-new"}}`)
	require.NoError(t, err)

	m.mu.RLock()
	_, ok = m.modelCache["deepseek"]
	m.mu.RUnlock()
	assert.False(t, ok, "cache should be invalidated after llm.providers update")
}

func TestGetModelMetaUsesFallbackWhenDiscoveryFails(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {
					BaseURL: "https://api.deepseek.com/v1",
					APIKey:  "sk-test",
					Models:  []ModelDefinition{}, // empty → discovery
				},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	prev := modelDiscoveryFn
	SetModelDiscoveryFunc(func(name, baseURL, apiKey, providerType string) ([]string, error) {
		return nil, fmt.Errorf("connection refused")
	})
	t.Cleanup(func() { SetModelDiscoveryFunc(prev) })

	// Discovery fails but GetModelMeta should still resolve from builtin fallback
	meta := m.GetModelMeta("deepseek", "deepseek-v4-flash")
	require.NotNil(t, meta)
	assert.Equal(t, 1000000, meta.ContextWindow)
}

func TestGetModelMeta(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	// Known model
	meta := m.GetModelMeta("deepseek", "deepseek-v4-flash")
	require.NotNil(t, meta)
	assert.Equal(t, "DeepSeek V4 Flash", meta.Name)
	assert.Equal(t, 1000000, meta.ContextWindow)

	// Legacy alias still resolvable
	meta = m.GetModelMeta("deepseek", "deepseek-chat")
	require.NotNil(t, meta)
	assert.True(t, meta.SupportsTools)

	// Unknown model
	meta = m.GetModelMeta("deepseek", "nonexistent-model")
	assert.Nil(t, meta)

	// Unknown provider
	meta = m.GetModelMeta("unknown", "deepseek-v4-flash")
	assert.Nil(t, meta)
}

// SenseNova E2E hosts DeepSeek model IDs; 0.11.2 tool gate must not treat
// API-discovered sparse entries as supports_tools=false.
func TestGetModelMetaEnrichesDiscoveredDeepSeekOnSenseNova(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"sensenova": {
					BaseURL: "https://token.sensenova.cn/v1",
					APIKey:  "sk-test",
					Models:  []ModelDefinition{}, // empty → discovery
				},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	prev := modelDiscoveryFn
	SetModelDiscoveryFunc(func(name, baseURL, apiKey, providerType string) ([]string, error) {
		return []string{"deepseek-v4-flash", "nova-6.7"}, nil
	})
	t.Cleanup(func() { SetModelDiscoveryFunc(prev) })

	meta := m.GetModelMeta("sensenova", "deepseek-v4-flash")
	require.NotNil(t, meta)
	assert.True(t, meta.SupportsTools)
	assert.Equal(t, 1000000, meta.ContextWindow)
	assert.Equal(t, "DeepSeek V4 Flash", meta.Name)
}

func TestGetModelMetaEnrichesSparseCustomModel(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"sensenova": {
					BaseURL: "https://token.sensenova.cn/v1",
					APIKey:  "sk-test",
					// Web UI custom mode often stores ID-only without supports_tools.
					Models: []ModelDefinition{{ID: "deepseek-v4-flash", Name: "deepseek-v4-flash"}},
				},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	meta := m.GetModelMeta("sensenova", "deepseek-v4-flash")
	require.NotNil(t, meta)
	assert.True(t, meta.SupportsTools)
	assert.Equal(t, 1000000, meta.ContextWindow)
}

func TestSenseNovaBuiltinIncludesDeepSeekFlash(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"sensenova": {BaseURL: "https://token.sensenova.cn/v1", APIKey: "sk-test"},
			},
		},
	}
	m := NewConfigManager(fileCfg)

	meta := m.GetModelMeta("sensenova", "deepseek-v4-flash")
	require.NotNil(t, meta)
	assert.True(t, meta.SupportsTools)
}

func TestDiscoverUnknownModelOptimisticTools(t *testing.T) {
	fileCfg := &Config{
		LLM: LLMConfig{
			Providers: map[string]ProviderConfig{
				"my-gw": {
					BaseURL: "https://gw.example/v1",
					APIKey:  "sk-test",
					Models:  []ModelDefinition{},
				},
			},
		},
	}
	m := NewConfigManager(fileCfg)
	prev := modelDiscoveryFn
	SetModelDiscoveryFunc(func(name, baseURL, apiKey, providerType string) ([]string, error) {
		return []string{"totally-unknown-model"}, nil
	})
	t.Cleanup(func() { SetModelDiscoveryFunc(prev) })

	meta := m.GetModelMeta("my-gw", "totally-unknown-model")
	require.NotNil(t, meta)
	assert.True(t, meta.SupportsTools, "unknown API-discovered IDs must not default to tools=false")
	assert.False(t, ModelToolsDenied(meta))
}

func TestParseProvidersFromInterface(t *testing.T) {
	// Test with map[string]ProviderConfig
	providers := map[string]ProviderConfig{
		"test": {BaseURL: "https://example.com", APIKey: "sk-test"},
	}
	result, err := ParseProvidersFromInterface(providers)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "https://example.com", result["test"].BaseURL)

	// Test with JSON string
	jsonStr := `{"test":{"base_url":"https://api.example.com","api_key":"sk-json"}}`
	result, err = ParseProvidersFromInterface(jsonStr)
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "sk-json", result["test"].APIKey)
}
