package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
)

// --- System Config endpoints ---

// configMaskedPlaceholder is the sentinel shown in place of sensitive values
// by GET /api/config (C-17). On PUT, a key carrying this exact value is left
// unchanged ("keep current"), so the admin UI can round-trip a masked form
// without clobbering secrets.
const configMaskedPlaceholder = "********"

// sensitiveConfigKeys are masked in GET /api/config responses.
var sensitiveConfigKeys = map[string]bool{
	"gitea.admin_token":    true,
	"gitea.webhook_secret": true,
}

// maskDisplayMap returns a copy of the display map with sensitive values
// replaced by the placeholder. llm.providers keeps its structure but each
// provider's api_key is masked. Internal consumers (test endpoints) use the
// raw GetDisplayMap; only HTTP responses are masked.
func maskDisplayMap(display map[string]interface{}) map[string]interface{} {
	masked := make(map[string]interface{}, len(display))
	for k, v := range display {
		masked[k] = v
	}
	for key := range sensitiveConfigKeys {
		if v, ok := masked[key]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				masked[key] = configMaskedPlaceholder
			}
		}
	}
	if raw, ok := masked["llm.providers"]; ok {
		if providers, err := config.ParseProvidersFromInterface(raw); err == nil && len(providers) > 0 {
			cp := make(map[string]config.ProviderConfig, len(providers))
			for name, pc := range providers {
				if pc.APIKey != "" {
					pc.APIKey = configMaskedPlaceholder
				}
				cp[name] = pc
			}
			masked["llm.providers"] = cp
		}
	}
	// C-14: mask hub backend passwords in the agents.backends document so GET
	// /config never leaks credentials (matches llm.providers api_key masking).
	if raw, ok := masked["agents.backends"]; ok {
		if s, isStr := raw.(string); isStr {
			masked["agents.backends"] = config.MaskSensitiveInBackendsJSON(s)
		}
	}
	return masked
}

// resolveMaskedEntries rewrites an update payload so masked values mean
// "keep current": sensitive keys equal to the placeholder are dropped, and
// sentinel api_keys inside llm.providers are restored from the active config.
// Returns the filtered entries (may be empty).
func (h *Handler) resolveMaskedEntries(entries map[string]string) map[string]string {
	resolved := make(map[string]string, len(entries))
	for key, value := range entries {
		if sensitiveConfigKeys[key] && value == configMaskedPlaceholder {
			continue
		}
		if key == "llm.providers" {
			value = h.restoreMaskedProviderKeys(value)
		}
		if key == "agents.backends" {
			value = h.restoreMaskedBackendPasswords(value)
		}
		resolved[key] = value
	}
	return resolved
}

// restoreMaskedProviderKeys replaces placeholder api_keys in a providers JSON
// payload with the currently configured keys.
func (h *Handler) restoreMaskedProviderKeys(payload string) string {
	incoming, err := config.ParseProvidersJSON(payload)
	if err != nil {
		return payload // let Update() surface the validation error
	}
	needsRestore := false
	for _, pc := range incoming {
		if pc.APIKey == configMaskedPlaceholder {
			needsRestore = true
			break
		}
	}
	if !needsRestore {
		return payload
	}
	current := map[string]config.ProviderConfig{}
	if h.cfgManager != nil {
		current = h.cfgManager.Get().LLM.Providers
	}
	for name, pc := range incoming {
		if pc.APIKey == configMaskedPlaceholder {
			pc.APIKey = current[name].APIKey // zero value when provider is new
			incoming[name] = pc
		}
	}
	data, err := json.Marshal(incoming)
	if err != nil {
		return payload
	}
	return string(data)
}

// restoreMaskedBackendPasswords replaces placeholder passwords inside an
// agents.backends JSON payload with the currently configured passwords,
// mirroring restoreMaskedProviderKeys (C-14 / C-17). This keeps the DB from
// persisting masked values so a server restart does not corrupt credentials.
func (h *Handler) restoreMaskedBackendPasswords(payload string) string {
	incoming, err := config.ParseAgentBackendsJSON(payload)
	if err != nil {
		return payload // let Update() surface the validation error
	}
	current := config.AgentBackendsConfig{}
	if h.cfgManager != nil {
		current = h.cfgManager.Get().Agents.Backends
	}
	for name, bc := range incoming.Backends {
		if bc.Auth.Password == configMaskedPlaceholder {
			if cur, ok := current.Backends[name]; ok {
				bc.Auth.Password = cur.Auth.Password // zero value when backend is new
			}
			incoming.Backends[name] = bc
		}
	}
	data, err := json.Marshal(incoming)
	if err != nil {
		return payload
	}
	return string(data)
}

func (h *Handler) getConfig(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}
	display, err := h.cfgManager.GetDisplayMap()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, maskDisplayMap(display))
}

func (h *Handler) updateConfig(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}

	var entries map[string]string
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		writeError(w, 400, "invalid request body: expected {\"key\": \"value\", ...}")
		return
	}

	if len(entries) == 0 {
		writeError(w, 400, "no config entries provided")
		return
	}

	// C-17: masked placeholders mean "keep current value".
	entries = h.resolveMaskedEntries(entries)
	if len(entries) == 0 {
		// Everything submitted was a keep-current placeholder: no-op.
		display, err := h.cfgManager.GetDisplayMap()
		if err != nil {
			writeError(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, maskDisplayMap(display))
		return
	}

	// C-6: when Gitea is being configured and no webhook_secret exists
	// anywhere (not in this payload, not in current config), auto-generate
	// one — an empty secret silently disables webhook signature verification
	// in the ingress handler.
	_, touchesGiteaURL := entries["gitea.url"]
	_, touchesGiteaToken := entries["gitea.admin_token"]
	if touchesGiteaURL || touchesGiteaToken {
		if _, hasSecret := entries["gitea.webhook_secret"]; !hasSecret &&
			h.cfgManager.Get().Gitea.WebhookSecret == "" {
			secret, err := config.GenerateWebhookSecret()
			if err != nil {
				writeError(w, 500, "生成 webhook_secret 失败: "+err.Error())
				return
			}
			entries["gitea.webhook_secret"] = secret
		}
	}

	// Validate all keys first
	for key := range entries {
		if !config.IsConfigKey(key) {
			writeError(w, 400, fmt.Sprintf("invalid config key: %s", key))
			return
		}
	}

	// Apply all entries atomically (validated as one merged batch first).
	if err := h.cfgManager.UpdateBatch(entries); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var applied []string
	for key, value := range entries {
		applied = append(applied, fmt.Sprintf("%s=%s", key, maskConfigValue(key, value)))
	}

	h.notifyConfigChange()

	// C-16: audit config changes (values masked; never log secrets).
	if h.db != nil {
		h.db.LogOperation(0, 0, "config_update", strings.Join(applied, "; "))
	}

	display, err := h.cfgManager.GetDisplayMap()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, maskDisplayMap(display))
}

func (h *Handler) deleteConfigEntry(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}

	key := r.PathValue("key")
	if key == "" {
		writeError(w, 400, "missing config key")
		return
	}

	if err := h.cfgManager.Delete(key); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	h.notifyConfigChange()

	// C-16: audit the revert-to-file-default (no value to mask on delete).
	if h.db != nil {
		h.db.LogOperation(0, 0, "config_delete", key+" (reverted to file config)")
	}

	display, err := h.cfgManager.GetDisplayMap()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, maskDisplayMap(display))
}

func (h *Handler) getProviderModels(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}

	providerName := r.PathValue("name")
	if providerName == "" {
		writeError(w, 400, "missing provider name")
		return
	}

	models, source, err := h.cfgManager.GetProviderModels(providerName)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{
			"success":         false,
			"error":           err.Error(),
			"fallback_source": source,
			"source":          source,
			"models":          models,
		})
		return
	}

	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"source":  source,
		"models":  models,
	})
}

// listProviderPresets returns the built-in LLM provider presets (C-11).
// The single source of truth lives in config.DefaultProviderPresets; both the
// first-run wizard and SystemConfig fetch it instead of hardcoding the list.
func (h *Handler) listProviderPresets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{"presets": config.DefaultProviderPresets()})
}

// discoverModelsHandler live-discovers models for an arbitrary (possibly
// unsaved) provider from its connection details (C-12). Mirrors the response
// shape of getProviderModels so the frontends can share rendering.
func (h *Handler) discoverModelsHandler(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Type     string `json:"type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	baseURL := strings.TrimSpace(payload.BaseURL)
	if baseURL == "" {
		writeError(w, 400, "base_url 不能为空")
		return
	}
	providerType := strings.TrimSpace(payload.Type)
	if providerType == "" {
		providerType = "openai_compatible"
	}
	models, source, err := h.cfgManager.DiscoverModels(
		strings.TrimSpace(payload.Provider),
		baseURL,
		strings.TrimSpace(payload.APIKey),
		providerType,
	)
	success := err == nil
	resp := map[string]interface{}{
		"success": success,
		"source":  source,
		"models":  models,
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

// giteaWebhookHandler checks or registers the inbound (Gitea→Matea) system
// webhook (C-13). The callback URL is {server.public_url}/webhook/gitea.
//   - action "check": report whether such a webhook already exists.
//   - action "register": ensure it exists (idempotent), creating if missing.
//
// When server.public_url is empty the inbound webhook is considered closed and
// the endpoint returns 200 with closed=true (no Gitea call is made).
func (h *Handler) giteaWebhookHandler(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}
	var payload struct {
		Action string `json:"action"` // check | register
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	cfg := h.cfgManager.Get()
	publicURL := strings.TrimSpace(cfg.Server.PublicURL)
	if publicURL == "" {
		writeJSON(w, 200, map[string]interface{}{
			"success": true,
			"closed":  true,
			"message": "server.public_url 未配置，入站 Webhook 已关闭（请在「入站 Webhook」中填写 Matea 对外地址）",
		})
		return
	}
	if strings.TrimSpace(cfg.Gitea.URL) == "" || strings.TrimSpace(cfg.Gitea.AdminToken) == "" {
		writeError(w, 400, "Gitea 地址或管理员 Token 未配置，无法操作站点级 Webhook")
		return
	}

	callbackURL := strings.TrimRight(publicURL, "/") + "/webhook/gitea"
	client := gitea.NewClient(cfg.Gitea.URL, cfg.Gitea.AdminToken)

	switch strings.TrimSpace(payload.Action) {
	case "check":
		hooks, err := client.ListAdminWebhooks()
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "error": err.Error(), "callback_url": callbackURL})
			return
		}
		registered, hookID := false, int64(0)
		for _, wh := range hooks {
			if wh.Config.URL == callbackURL {
				registered, hookID = true, wh.ID
				break
			}
		}
		writeJSON(w, 200, map[string]interface{}{
			"success":      true,
			"registered":   registered,
			"hook_id":      hookID,
			"callback_url": callbackURL,
		})
	case "register":
		registered, created, hookID, err := client.EnsureWebhook(callbackURL, cfg.Gitea.WebhookSecret, nil)
		if err != nil {
			writeJSON(w, 200, map[string]interface{}{"success": false, "error": err.Error(), "callback_url": callbackURL})
			return
		}
		writeJSON(w, 200, map[string]interface{}{
			"success":      true,
			"registered":   registered,
			"created":      created,
			"hook_id":      hookID,
			"callback_url": callbackURL,
		})
	default:
		writeError(w, 400, "action 必须为 check 或 register")
	}
}

func (h *Handler) notifyConfigChange() {
	if h.onConfigChange != nil {
		h.onConfigChange(h.cfgManager.Get())
	}
}

func (h *Handler) testGiteaConfig(w http.ResponseWriter, r *http.Request) {
	var payload map[string]string
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	url := strings.TrimSpace(firstNonEmpty(payload["gitea.url"], h.stringConfigValue("gitea.url")))
	token := strings.TrimSpace(firstNonEmpty(payload["gitea.admin_token"], h.stringConfigValue("gitea.admin_token")))
	// C-17: the admin UI round-trips masked secrets — resolve the placeholder
	// against the saved (raw) value before testing.
	if token == configMaskedPlaceholder {
		token = h.stringConfigValue("gitea.admin_token")
	}

	client := gitea.NewClient(url, token)
	result, err := client.TestConnection()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	status := http.StatusOK
	if !result.OK {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (h *Handler) testLLMConfig(w http.ResponseWriter, r *http.Request) {
	var payload map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	providerName := strings.TrimSpace(firstNonEmpty(
		asString(payload["llm.defaults.provider"]),
		h.stringConfigValue("llm.defaults.provider"),
	))
	if providerName == "" {
		writeError(w, 400, "默认 Provider 不能为空")
		return
	}

	providers := h.resolveProvidersForTest(payload)
	pcfg, ok := providers[providerName]
	if !ok {
		writeError(w, 400, fmt.Sprintf("Provider %q 未配置 API Key / Base URL", providerName))
		return
	}
	if strings.TrimSpace(pcfg.APIKey) == "" {
		writeError(w, 400, fmt.Sprintf("Provider %q 的 api_key 不能为空", providerName))
		return
	}

	var provider llm.Provider
	if strings.EqualFold(providerName, "claude") || strings.EqualFold(providerName, "anthropic") {
		provider = llm.NewAnthropicProvider(pcfg.APIKey)
	} else {
		baseURL := pcfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		provider = llm.NewOpenAICompatibleProvider(baseURL, pcfg.APIKey)
	}

	model := strings.TrimSpace(firstNonEmpty(
		asString(payload["llm.defaults.model"]),
		h.stringConfigValue("llm.defaults.model"),
		"deepseek-v4-flash",
	))

	maxTokens := 8
	if v := firstNonEmpty(
		asString(payload["agents.defaults.max_output_tokens"]),
		h.stringConfigValue("agents.defaults.max_output_tokens"),
	); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			maxTokens = n
		}
	}
	if maxTokens > 16 {
		maxTokens = 16
	}
	if maxTokens <= 0 {
		maxTokens = 8
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	resp, err := provider.ChatCompletion(ctx, &llm.ChatRequest{
		Model: model,
		Messages: []llm.Message{
			{Role: "user", Content: "ping"},
		},
		MaxTokens:   maxTokens,
		Temperature: 0,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok":      false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       true,
		"provider": providerName,
		"model":    model,
		"message":  fmt.Sprintf("连接成功，模型响应: %s", strings.TrimSpace(resp.Content)),
	})
}

func (h *Handler) stringConfigValue(key string) string {
	if h.cfgManager == nil {
		return ""
	}
	display, err := h.cfgManager.GetDisplayMap()
	if err != nil {
		return ""
	}
	val, ok := display[key]
	if !ok {
		return ""
	}
	return asString(val)
}

func (h *Handler) resolveProvidersForTest(payload map[string]interface{}) map[string]config.ProviderConfig {
	if raw, ok := payload["llm.providers"]; ok {
		providers, err := config.ParseProvidersFromInterface(raw)
		if err == nil && len(providers) > 0 {
			// C-17: restore masked api_keys from the active config so a test
			// launched from the masked admin form hits the real credential.
			return h.unmaskTestProviders(providers)
		}
	}

	display, err := h.cfgManager.GetDisplayMap()
	if err != nil {
		return nil
	}
	if raw, ok := display["llm.providers"]; ok {
		providers, err := config.ParseProvidersFromInterface(raw)
		if err == nil {
			return providers
		}
	}
	return nil
}

// unmaskTestProviders swaps placeholder api_keys for the active config's real
// keys (same rule as restoreMaskedProviderKeys, on parsed providers).
func (h *Handler) unmaskTestProviders(providers map[string]config.ProviderConfig) map[string]config.ProviderConfig {
	var current map[string]config.ProviderConfig
	for name, pc := range providers {
		if pc.APIKey != configMaskedPlaceholder {
			continue
		}
		if current == nil && h.cfgManager != nil {
			current = h.cfgManager.Get().LLM.Providers
		}
		pc.APIKey = current[name].APIKey
		providers[name] = pc
	}
	return providers
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// maskConfigValue redacts sensitive values for the audit log (C-16): token /
// secret / api_key / password keys are fully masked; llm.providers keeps its
// structure but masks each provider's api_key.
func maskConfigValue(key, value string) string {
	lower := strings.ToLower(key)
	if strings.Contains(lower, "token") || strings.Contains(lower, "secret") ||
		strings.Contains(lower, "api_key") || strings.Contains(lower, "password") {
		return "****"
	}
	// deliver.webhook_url may carry IM secrets in the query (?access_token=).
	if lower == "deliver.webhook_url" {
		return "****"
	}
	if key == "llm.providers" {
		var providers map[string]map[string]interface{}
		if err := json.Unmarshal([]byte(value), &providers); err != nil {
			return "****" // unparseable — don't risk leaking
		}
		for _, fields := range providers {
			if v, ok := fields["api_key"]; ok && v != "" {
				fields["api_key"] = "****"
			}
		}
		data, err := json.Marshal(providers)
		if err != nil {
			return "****"
		}
		return string(data)
	}
	// C-14: redact hub backend passwords in the audit log (declared in config).
	if key == "agents.backends" {
		return config.MaskSensitiveInBackendsJSON(value)
	}
	return value
}

func asString(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return ""
	}
}
