package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GetDisplayMap returns config for Web UI display.
// Per-field: non-empty DB value wins; otherwise file config (config.yaml) is used.
// JSON-type fields with semantically empty DB values (e.g. "{}") fall back to file config.
// Sensitive fields are returned as real values (for editing).
func (m *ConfigManager) GetDisplayMap() (map[string]interface{}, error) {
	dbEntries := map[string]string{}
	if m.store != nil {
		entries, err := m.store.ListConfigs()
		if err != nil {
			return nil, fmt.Errorf("load db configs: %w", err)
		}
		dbEntries = entries
	}

	m.mu.RLock()
	base := m.base
	m.mu.RUnlock()

	result := make(map[string]interface{}, len(configKeys)+1)
	sources := make(map[string]string, len(configKeys))

	for _, key := range configKeys {
		if dbVal, ok := dbEntries[key]; ok && strings.TrimSpace(dbVal) != "" && !isSemanticallyEmptyConfigValue(key, dbVal) {
			val, err := parseConfigValue(key, dbVal)
			if err != nil {
				return nil, fmt.Errorf("invalid db config %s: %w", key, err)
			}
			result[key] = val
			sources[key] = "db"
			continue
		}
		val := getConfigValueTyped(base, key)
		result[key] = val
		sources[key] = "file"
	}

	// Build models metadata from active (merged) providers so DB overlays are visible.
	modelsMeta := make(map[string][]ModelDefinition)
	m.mu.RLock()
	activeProviders := m.active.LLM.Providers
	activeBackends := m.active.Agents.Backends
	m.mu.RUnlock()
	for name, pc := range activeProviders {
		models, _ := m.resolveProviderModels(name, pc)
		modelsMeta[name] = models
	}

	backendsMeta := make([]map[string]interface{}, 0, len(activeBackends.Backends)+1)
	backendsMeta = append(backendsMeta, map[string]interface{}{
		"name": "builtin",
		"type": BackendTypeBuiltin,
	})
	for name, bc := range activeBackends.Backends {
		if name == "builtin" {
			continue
		}
		backendsMeta = append(backendsMeta, map[string]interface{}{
			"name": name,
			"type": bc.Type,
		})
	}

	result["_meta"] = map[string]interface{}{
		"sources":          sources,
		"models":           modelsMeta,
		"backends":         backendsMeta,
		"backends_default": activeBackends.Default,
		"workflow_presets": []map[string]interface{}{
			{"name": "free", "label": "自由模式", "description": "最小限制，允许跳过分析直接开发"},
			{"name": "standard", "label": "标准模式", "description": "平衡配置，开发中重新分析会警告"},
			{"name": "strict", "label": "严格模式", "description": "最大限制，强制分析后才能开发"},
		},
	}
	return result, nil
}

// isSemanticallyEmptyConfigValue checks whether a DB config value is semantically empty
// even though it is non-empty as a raw string. This prevents values like "{}" or "null"
// from blocking fallback to file config for JSON-type fields.
func isSemanticallyEmptyConfigValue(key, dbVal string) bool {
	trimmed := strings.TrimSpace(dbVal)
	switch key {
	case "llm.providers":
		// "{}" or "null" represent an empty providers map — treat as unset
		if trimmed == "{}" || trimmed == "null" {
			return true
		}
		// Also check parsed JSON: a valid map with zero entries is semantically empty
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(trimmed), &m); err == nil && len(m) == 0 {
			return true
		}
	case "agents.loop.verify_commands":
		// "null" means unset → fall back to file config.
		// "[]" is intentional disable and must NOT fall back.
		if trimmed == "null" {
			return true
		}
	}
	return false
}
