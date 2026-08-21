package config

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is the interface for system_config CRUD (avoids circular dependency with store package).
type Store interface {
	GetConfig(key string) (string, error)
	SetConfig(key, value string) error
	DeleteConfig(key string) error
	ListConfigs() (map[string]string, error)
}

// ConfigManager manages configuration with DB overrides on top of file config.
type ConfigManager struct {
	mu         sync.RWMutex
	base       *Config // file-based config (immutable baseline)
	active     *Config // merged config (base + DB overrides)
	store      Store   // DB store for persistence
	modelCache map[string]modelCacheEntry
}

type modelCacheEntry struct {
	models    []ModelDefinition
	source    string // api | builtin | custom
	expiresAt time.Time
}

// ModelDiscoveryFunc is a function that can discover models for a provider.
// Returns model IDs and an error.
type ModelDiscoveryFunc func(providerName string, baseURL, apiKey string, providerType string) ([]string, error)

var modelDiscoveryFn ModelDiscoveryFunc

// SetModelDiscoveryFunc registers a function for dynamic model discovery.
// This is called at startup to avoid circular imports.
func SetModelDiscoveryFunc(fn ModelDiscoveryFunc) {
	modelDiscoveryFn = fn
}

// NewConfigManager creates a ConfigManager from a file-loaded config.
func NewConfigManager(fileCfg *Config) *ConfigManager {
	active := copyConfig(fileCfg)
	return &ConfigManager{
		base:   fileCfg,
		active: active,
	}
}

// SetStore sets the DB store for persistence. Must be called before ApplyDBOverrides.
func (m *ConfigManager) SetStore(s Store) {
	m.store = s
}

// ApplyDBOverrides loads DB config entries and merges them into the active config.
func (m *ConfigManager) ApplyDBOverrides() error {
	if m.store == nil {
		return nil
	}

	entries, err := m.store.ListConfigs()
	if err != nil {
		return fmt.Errorf("load db configs: %w", err)
	}

	if len(entries) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for key, value := range entries {
		if !IsConfigKey(key) {
			log.Printf("[WARN] Ignore non-config DB key %q (value: %s). If this is no longer needed, delete it via: DELETE /api/config/%s", key, maskValueForLog(key, value), key)
			continue
		}
		if err := applyConfigEntry(m.active, key, value); err != nil {
			log.Printf("[WARN] Skip invalid DB config %s=%s: %v. Fix via: PUT /api/config with correct value, or DELETE /api/config/%s", key, maskValueForLog(key, value), err, key)
		}
	}

	log.Printf("[INFO] Applied %d DB config overrides", len(entries))
	return nil
}

// Get returns a deep copy of the active (merged) config.
// Callers may retain the result without racing config hot-updates.
func (m *ConfigManager) Get() *Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return copyConfig(m.active)
}

// Update sets a config entry: writes to DB and applies to active config in memory.
func (m *ConfigManager) Update(key, value string) error {
	m.mu.RLock()
	testCfg := copyConfig(m.active)
	m.mu.RUnlock()
	if err := applyConfigEntry(testCfg, key, value); err != nil {
		return fmt.Errorf("invalid value for %s: %w", key, err)
	}

	// Persist to DB
	if m.store != nil {
		if err := m.store.SetConfig(key, value); err != nil {
			return fmt.Errorf("persist config: %w", err)
		}
	}

	// Apply to a new snapshot so concurrent Get() holders keep a stable copy.
	m.mu.Lock()
	next := copyConfig(m.active)
	if err := applyConfigEntry(next, key, value); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("apply config: %w", err)
	}
	m.active = next
	m.mu.Unlock()

	// Invalidate model cache when providers change
	if key == "llm.providers" {
		m.InvalidateAllModelCache()
	}

	log.Printf("[INFO] Config updated: %s = %s", key, maskValueForLog(key, value))
	return nil
}

// UpdateBatch validates and applies multiple entries as one unit: every entry
// is validated in deterministic (sorted-key) order against a single merged
// snapshot FIRST — so cross-field validators (e.g. agents.loop.*) see the
// final state and one bad entry fails the whole batch — and only then is the
// batch persisted and swapped in. This removes the per-key partial-failure
// window and map-iteration nondeterminism of repeated Update calls.
//
// Note: persistence itself is still per-key (the store has no transaction
// API), so a mid-batch DB error can leave a persisted-but-inactive subset;
// validation up front removes the dominant failure class (invalid values).
func (m *ConfigManager) UpdateBatch(entries map[string]string) error {
	if len(entries) == 0 {
		return nil
	}
	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	m.mu.RLock()
	testCfg := copyConfig(m.active)
	m.mu.RUnlock()
	for _, k := range keys {
		if err := applyConfigEntry(testCfg, k, entries[k]); err != nil {
			return fmt.Errorf("invalid value for %s: %w", k, err)
		}
	}

	if m.store != nil {
		for _, k := range keys {
			if err := m.store.SetConfig(k, entries[k]); err != nil {
				return fmt.Errorf("persist config %s: %w", k, err)
			}
		}
	}

	m.mu.Lock()
	m.active = testCfg
	m.mu.Unlock()

	for _, k := range keys {
		if k == "llm.providers" {
			m.InvalidateAllModelCache()
		}
		log.Printf("[INFO] Config updated: %s = %s", k, maskValueForLog(k, entries[k]))
	}
	return nil
}

// maskValueForLog redacts sensitive config values before they reach the
// runtime log. Audit logs already mask — the runtime log must not become the
// leak when secrets flow through Update (import / env-absorb / Web UI PUT).
func maskValueForLog(key, value string) string {
	lk := strings.ToLower(key)
	if lk == "llm.providers" || // JSON blob containing api keys
		lk == "agents.backends" || // JSON blob containing hub passwords
		lk == "deliver.webhook_url" || // may carry ?access_token= secrets
		isSensitiveKeyName(lk) {
		return "********"
	}
	return value
}

// isSensitiveKeyName matches secret-bearing key names precisely enough to
// catch gitea.admin_token / *.webhook_secret / *.api_key without over-masking
// lookalikes such as agents.defaults.max_input_tokens.
func isSensitiveKeyName(lk string) bool {
	if strings.Contains(lk, "secret") || strings.Contains(lk, "password") {
		return true
	}
	segs := strings.Split(lk, ".")
	last := segs[len(segs)-1]
	return last == "token" || strings.HasSuffix(last, "_token") || last == "api_key"
}

// Delete removes a DB override, reverting to file config default.
func (m *ConfigManager) Delete(key string) error {
	if m.store != nil {
		if err := m.store.DeleteConfig(key); err != nil {
			return fmt.Errorf("delete config: %w", err)
		}
	}

	// Reset to file config value on a new snapshot.
	m.mu.Lock()
	fileVal := getConfigEntry(m.base, key)
	next := copyConfig(m.active)
	if err := applyConfigEntry(next, key, fileVal); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("revert config: %w", err)
	}
	m.active = next
	m.mu.Unlock()

	// Invalidate model cache when providers change
	if key == "llm.providers" {
		m.InvalidateAllModelCache()
	}

	log.Printf("[INFO] Config reverted to default: %s", key)
	return nil
}

// GetMap returns the current config as a flat map for API response.
// Display values prefer DB entries; empty/missing DB values fall back to file config.
func (m *ConfigManager) GetMap() map[string]interface{} {
	display, err := m.GetDisplayMap()
	if err != nil {
		log.Printf("[WARN] GetDisplayMap failed, falling back to active config: %v", err)
		return m.getActiveMap()
	}
	return display
}

// GetMapLegacy returns the active merged config without display metadata.
func (m *ConfigManager) GetMapLegacy() map[string]interface{} {
	return m.getActiveMap()
}

func (m *ConfigManager) getActiveMap() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg := m.active
	return map[string]interface{}{
		"gitea.url":                                cfg.Gitea.URL,
		"gitea.admin_token":                        cfg.Gitea.AdminToken,
		"gitea.webhook_secret":                     cfg.Gitea.WebhookSecret,
		"gitea.auto_provision":                     cfg.Gitea.AutoProvision,
		"llm.defaults.provider":                    cfg.LLM.Defaults.Provider,
		"llm.defaults.model":                       cfg.LLM.Defaults.Model,
		"llm.providers":                            cfg.LLM.Providers,
		"dispatcher.max_concurrent":                cfg.Dispatcher.MaxConcurrent,
		"dispatcher.task_retry_count":              cfg.Dispatcher.TaskRetryCount,
		"dispatcher.rate_limit_backoff":            cfg.Dispatcher.RateLimitBackoff,
		"dispatcher.agent_concurrency":             cfg.Dispatcher.AgentConcurrency,
		"llm.rate_limit_retries":                   cfg.LLM.RateLimitRetries,
		"agents.defaults.provider":                 cfg.Agents.Defaults.Provider,
		"agents.defaults.model":                    cfg.Agents.Defaults.Model,
		"agents.defaults.max_output_tokens":        cfg.Agents.Defaults.MaxOutputTokens,
		"agents.defaults.max_input_tokens":         cfg.Agents.Defaults.MaxInputTokens,
		"agents.defaults.temperature":              cfg.Agents.Defaults.Temperature,
		"agents.defaults.timeout":                  cfg.Agents.Defaults.Timeout,
		"agents.loop.max_iterations":               cfg.Agents.Loop.MaxIterations,
		"agents.loop.total_timeout":                cfg.Agents.Loop.TotalTimeout,
		"agents.loop.iteration_interval":           cfg.Agents.Loop.IterationInterval,
		"agents.loop.no_progress_limit":            cfg.Agents.Loop.NoProgressLimit,
		"agents.loop.verify_commands":              cfg.Agents.Loop.VerifyCommands,
		"agents.loop.independent_checker":          cfg.Agents.Loop.IndependentChecker,
		"debug.conversation_log.enabled":           cfg.Debug.ConversationLog.Enabled,
		"debug.conversation_log.max_content_chars": cfg.Debug.ConversationLog.MaxContentChars,
	}
}
