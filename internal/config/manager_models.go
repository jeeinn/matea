package config

import (
	"fmt"
	"time"
)

// modelDiscoveryCacheTTL is the cache duration for dynamically discovered models.
const modelDiscoveryCacheTTL = 1 * time.Hour

// resolveProviderModels determines the effective model list for a provider.
// Priority: user-defined (non-empty) > builtin catalog.
// Returns the models and their source ("custom" or "builtin").
func (m *ConfigManager) resolveProviderModels(name string, pc ProviderConfig) ([]ModelDefinition, string) {
	if len(pc.Models) > 0 {
		return pc.Models, "custom"
	}
	if builtin, ok := BuiltinModelCatalog[name]; ok {
		return builtin, "builtin"
	}
	return nil, ""
}

// GetProviderModels returns the effective model list for a provider.
// This is used by the Web UI for model selection dropdowns.
// getProviderConfig returns the ProviderConfig from the active (merged) config.
// NOTE: llm.providers is the LLM config for the **builtin** backend only; hub-*
// backends resolve their own LLM and do not consult this map.
func (m *ConfigManager) getProviderConfig(providerName string) (ProviderConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	pc, ok := m.active.LLM.Providers[providerName]
	return pc, ok
}

// Behavior depends on ProviderConfig.Models:
//   - nil / unset: returns BuiltinModelCatalog, source="builtin"
//   - empty slice []: attempts dynamic discovery via /models API; on failure falls back to builtin
//   - non-empty: returns user-defined models, source="custom"
func (m *ConfigManager) GetProviderModels(providerName string) ([]ModelDefinition, string, error) {
	pc, ok := m.getProviderConfig(providerName)
	if !ok {
		return nil, "", fmt.Errorf("provider not found: %s", providerName)
	}

	// Check in-memory cache
	m.mu.RLock()
	if cache, ok := m.modelCache[providerName]; ok && time.Now().Before(cache.expiresAt) {
		m.mu.RUnlock()
		return cache.models, cache.source, nil
	}
	m.mu.RUnlock()

	var models []ModelDefinition
	var source string
	var err error

	switch {
	case pc.Models == nil:
		// Unset → builtin
		models, source = m.resolveProviderModels(providerName, pc)

	case len(pc.Models) == 0:
		// Empty → dynamic discovery
		models, source, err = m.discoverModels(providerName, pc)
		if err != nil {
			// Fall back to builtin on error, but return error info
			models, source = m.resolveProviderModels(providerName, pc)
			if models == nil {
				models = []ModelDefinition{}
			}
			// Cache short-lived fallback result
			m.cacheModels(providerName, models, source, 5*time.Minute)
			return models, source, fmt.Errorf("dynamic discovery failed, using fallback: %w", err)
		}

	default:
		// Non-empty → custom
		models, source = m.resolveProviderModels(providerName, pc)
	}

	if models == nil {
		models = []ModelDefinition{}
	}

	m.cacheModels(providerName, models, source, modelDiscoveryCacheTTL)
	return models, source, nil
}

// cacheModels stores the model list in the in-memory cache.
func (m *ConfigManager) cacheModels(providerName string, models []ModelDefinition, source string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.modelCache == nil {
		m.modelCache = make(map[string]modelCacheEntry)
	}
	m.modelCache[providerName] = modelCacheEntry{
		models:    models,
		source:    source,
		expiresAt: time.Now().Add(ttl),
	}
}

// InvalidateModelCache clears the cached model list for a provider.
func (m *ConfigManager) InvalidateModelCache(providerName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.modelCache, providerName)
}

// InvalidateAllModelCache clears all cached model lists.
func (m *ConfigManager) InvalidateAllModelCache() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modelCache = nil
}

// GetModelMeta returns the metadata for a specific model in a provider.
// Returns nil if the model is not found.
// Discovery errors are ignored here: GetProviderModels already returns fallback
// models (builtin/custom) alongside the error, and callers still need metadata.
// Sparse definitions (API discovery / custom ID-only) are enriched from the
// builtin catalog by model ID so cross-vendor gateways keep supports_tools.
func (m *ConfigManager) GetModelMeta(providerName, modelID string) *ModelDefinition {
	models, _, _ := m.GetProviderModels(providerName)
	for i := range models {
		if models[i].ID == modelID {
			meta := EnrichModelMetaFromBuiltin(models[i])
			return &meta
		}
	}
	return nil
}

// GetProviderDefaultParams returns provider-level default_params for sampling.
func (m *ConfigManager) GetProviderDefaultParams(providerName string) ModelParams {
	pc, ok := m.getProviderConfig(providerName)
	if !ok {
		return ModelParams{}
	}
	return pc.DefaultParams
}

// discoverModels attempts to fetch the model list from the provider's API.
// Uses the injected ModelDiscoveryFunc (if set) to avoid circular imports.
func (m *ConfigManager) discoverModels(providerName string, pc ProviderConfig) ([]ModelDefinition, string, error) {
	if modelDiscoveryFn == nil {
		// No discovery function configured → fall back to builtin
		builtin, source := m.resolveProviderModels(providerName, pc)
		if builtin == nil {
			builtin = []ModelDefinition{}
		}
		return builtin, source, nil
	}

	providerType := pc.Type
	if providerType == "" {
		providerType = "openai_compatible"
	}

	ids, err := modelDiscoveryFn(providerName, pc.BaseURL, pc.APIKey, providerType)
	if err != nil {
		return nil, "", fmt.Errorf("list models: %w", err)
	}

	// Merge with provider builtin first, then any catalog entry with the same ID
	// (e.g. SenseNova /models returning deepseek-v4-flash).
	builtin := BuiltinModelCatalog[providerName]
	builtinMap := make(map[string]ModelDefinition, len(builtin))
	for _, bm := range builtin {
		builtinMap[bm.ID] = bm
	}

	result := make([]ModelDefinition, 0, len(ids))
	for _, id := range ids {
		if bm, ok := builtinMap[id]; ok {
			result = append(result, bm)
		} else if bm := LookupBuiltinModelByID(id); bm != nil {
			result = append(result, *bm)
		} else {
			// OpenAI-compatible /models only returns IDs. Treat tool support as
			// unknown-but-optimistic (true) so coder gates / UI do not confuse
			// "missing metadata" with an explicit supports_tools=false.
			result = append(result, ModelDefinition{
				ID:            id,
				Name:          id,
				SupportsTools: true,
			})
		}
	}
	return result, "api", nil
}

// DiscoverModels performs live model discovery for an arbitrary (possibly
// unsaved) provider given its connection details (C-12). It reuses the exact
// discovery path as saved providers so the wizard and SystemConfig can
// "pull models on select" before the provider is persisted to the DB.
func (m *ConfigManager) DiscoverModels(providerName, baseURL, apiKey, providerType string) ([]ModelDefinition, string, error) {
	if providerType == "" {
		providerType = "openai_compatible"
	}
	pc := ProviderConfig{
		Type:    providerType,
		BaseURL: baseURL,
		APIKey:  apiKey,
	}
	return m.discoverModels(providerName, pc)
}
