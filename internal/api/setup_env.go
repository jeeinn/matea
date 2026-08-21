package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/jeeinn/matea/internal/config"
)

// --- Environment-variable detection & one-click apply (Phase 2.5: C-21) ---
//
// Many deployments run Matea with secrets already in the environment
// (GITEA_URL, OPENAI_API_KEY, ...). This lets the setup wizard detect those
// variables and absorb them into config in one click, instead of retyping
// every secret. Values are NEVER returned to the client — only the variable
// name, whether it is present, and the config target it maps to.

// envVarDescriptor describes one environment variable Matea can absorb.
type envVarDescriptor struct {
	Env          string `json:"env"`
	ConfigKey    string `json:"config_key,omitempty"` // scalar keys only
	Kind         string `json:"kind"`                 // "scalar" | "llm_provider" | "hub_backend"
	ProviderName string `json:"provider_name,omitempty"`
	ProviderType string `json:"provider_type,omitempty"`
	ProviderURL  string `json:"provider_url,omitempty"`
	BackendType  string `json:"backend_type,omitempty"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

// envCatalog lists every variable the wizard knows how to absorb.
var envCatalog = []envVarDescriptor{
	{Env: "GITEA_URL", ConfigKey: "gitea.url", Kind: "scalar",
		Title: "Gitea 地址", Description: "Gitea 实例的 Base URL，例如 http://localhost:3000"},
	{Env: "GITEA_ADMIN_TOKEN", ConfigKey: "gitea.admin_token", Kind: "scalar",
		Title: "Gitea 管理员 Token", Description: "具有管理员权限的 Gitea 访问令牌"},
	{Env: "GITEA_WEBHOOK_SECRET", ConfigKey: "gitea.webhook_secret", Kind: "scalar",
		Title: "Webhook 密钥", Description: "Gitea Webhook 回调签名密钥"},
	{Env: "OPENAI_API_KEY", Kind: "llm_provider", ProviderName: "openai",
		ProviderType: "openai_compatible", ProviderURL: "https://api.openai.com/v1",
		Title: "OpenAI API Key", Description: "创建/更新 openai provider（openai_compatible）"},
	{Env: "DEEPSEEK_API_KEY", Kind: "llm_provider", ProviderName: "deepseek",
		ProviderType: "openai_compatible", ProviderURL: "https://api.deepseek.com",
		Title: "DeepSeek API Key", Description: "创建/更新 deepseek provider（openai_compatible）"},
	{Env: "ANTHROPIC_API_KEY", Kind: "llm_provider", ProviderName: "anthropic",
		ProviderType: "anthropic", ProviderURL: "",
		Title: "Anthropic API Key", Description: "创建/更新 anthropic provider"},
	{Env: "SENSENOVA_API_KEY", Kind: "llm_provider", ProviderName: "sensenova",
		ProviderType: "openai_compatible", ProviderURL: "https://api.sensenova.cn/v1",
		Title: "阶跃星辰 API Key", Description: "创建/更新 sensenova provider（openai_compatible）"},
	{Env: "OPENCODE_URL", Kind: "hub_backend", ProviderName: "opencode",
		BackendType: config.BackendTypeHubOpenCode,
		Title: "OpenCode Hub 地址", Description: "创建/更新 hub-opencode 编码后端"},
	{Env: "HERMES_URL", Kind: "hub_backend", ProviderName: "hermes",
		BackendType: config.BackendTypeHubHermes,
		Title: "Hermes Hub 地址", Description: "创建/更新 hub-hermes 编码后端"},
}

// envDetectItem is one catalog entry annotated with live presence.
type envDetectItem struct {
	envVarDescriptor
	Present bool `json:"present"`
}

// handleEnvDetection handles GET /api/setup/env-detection (setup-token gated).
// Returns which known variables are set in the process environment and the
// config target each maps to. Values are never exposed.
func (h *Handler) handleEnvDetection(w http.ResponseWriter, r *http.Request) {
	items := make([]envDetectItem, 0, len(envCatalog))
	count := 0
	for _, d := range envCatalog {
		_, present := os.LookupEnv(d.Env)
		if present {
			count++
		}
		items = append(items, envDetectItem{envVarDescriptor: d, Present: present})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"detected": items,
		"count":    count,
	})
}

// envApplyRequest selects which variables to absorb. When Keys is empty, every
// detected variable is applied.
type envApplyRequest struct {
	Keys []string `json:"keys"`
}

// handleApplyEnv handles POST /api/setup/apply-env (setup-token gated).
// Absorbs the selected (or all detected) environment variables into config.
func (h *Handler) handleApplyEnv(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, http.StatusInternalServerError, "config manager not initialized")
		return
	}
	var req envApplyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	want := map[string]bool{}
	if len(req.Keys) == 0 {
		// apply-all: every present variable qualifies
		for _, d := range envCatalog {
			if _, present := os.LookupEnv(d.Env); present {
				want[d.Env] = true
			}
		}
	} else {
		for _, k := range req.Keys {
			want[strings.TrimSpace(k)] = true
		}
	}

	var applied []string
	var skipped []string
	var applyErrs []string

	for _, d := range envCatalog {
		if !want[d.Env] {
			continue
		}
		value, present := os.LookupEnv(d.Env)
		if !present {
			skipped = append(skipped, d.Env+" (环境变量未设置)")
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			skipped = append(skipped, d.Env+" (值为空)")
			continue
		}
		if err := h.applyEnvDescriptor(d, value); err != nil {
			applyErrs = append(applyErrs, d.Env+": "+err.Error())
			continue
		}
		applied = append(applied, d.Env)
	}

	if len(applied) > 0 {
		h.notifyConfigChange()
		if h.db != nil {
			h.db.LogOperation(0, 0, "config_apply_env", strings.Join(applied, "; "))
		}
	}

	status := http.StatusOK
	if len(applied) == 0 && len(applyErrs) > 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]interface{}{
		"applied":  applied,
		"skipped":  skipped,
		"errors":   applyErrs,
		"message":  buildApplyMessage(len(applied), len(skipped), len(applyErrs)),
	})
}

// applyEnvDescriptor writes one descriptor's value into config according to its
// kind (scalar key, LLM provider, or hub backend).
func (h *Handler) applyEnvDescriptor(d envVarDescriptor, value string) error {
	switch d.Kind {
	case "scalar":
		return h.cfgManager.Update(d.ConfigKey, value)
	case "llm_provider":
		return h.applyEnvLLMProvider(d, value)
	case "hub_backend":
		return h.applyEnvHubBackend(d, value)
	default:
		return nil
	}
}

// applyEnvLLMProvider merges the env-provided key into llm.providers and, when
// no default provider is set yet, points llm.defaults.provider at it.
func (h *Handler) applyEnvLLMProvider(d envVarDescriptor, apiKey string) error {
	providers := h.cfgManager.Get().LLM.Providers
	if providers == nil {
		providers = map[string]config.ProviderConfig{}
	}
	pc := providers[d.ProviderName]
	pc.Type = d.ProviderType
	if d.ProviderURL != "" {
		pc.BaseURL = d.ProviderURL
	}
	pc.APIKey = apiKey
	providers[d.ProviderName] = pc

	raw, err := json.Marshal(providers)
	if err != nil {
		return err
	}
	if err := h.cfgManager.Update("llm.providers", string(raw)); err != nil {
		return err
	}
	// Only set the default provider when none is configured yet, so we never
	// clobber an existing choice.
	if h.cfgManager.Get().LLM.Defaults.Provider == "" {
		if err := h.cfgManager.Update("llm.defaults.provider", d.ProviderName); err != nil {
			return err
		}
	}
	return nil
}

// applyEnvHubBackend merges the env-provided URL into agents.backends and, when
// no default backend is set, points the default at it.
func (h *Handler) applyEnvHubBackend(d envVarDescriptor, baseURL string) error {
	backends := h.cfgManager.Get().Agents.Backends
	if backends.Backends == nil {
		backends.Backends = map[string]config.BackendConfig{}
	}
	bc := backends.Backends[d.ProviderName]
	bc.Type = d.BackendType
	bc.BaseURL = baseURL
	backends.Backends[d.ProviderName] = bc
	if backends.Default == "" {
		backends.Default = d.ProviderName
	}
	raw, err := json.Marshal(backends)
	if err != nil {
		return err
	}
	return h.cfgManager.Update("agents.backends", string(raw))
}

// buildApplyMessage summarizes an apply result for the UI.
func buildApplyMessage(applied, skipped, errs int) string {
	switch {
	case applied > 0 && errs == 0:
		return fmt.Sprintf("已应用 %d 个环境变量", applied)
	case applied > 0 && errs > 0:
		return fmt.Sprintf("已应用 %d 个，%d 个失败", applied, errs)
	case applied == 0 && errs > 0:
		return fmt.Sprintf("应用失败：%d 个错误", errs)
	default:
		return "没有可应用的环境变量"
	}
}
