package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/jeeinn/matea/internal/config"
)

// --- Config export / import (Phase 2.5: C-20) ---
//
// A faithful backup of the admin-managed flat config. Export emits a
// string-keyed map that round-trips through applyConfigEntry on import, so a
// downloaded file can be re-uploaded verbatim. Secrets are MASKED by default
// (the original C-20 spec: 敏感字段脱敏); an operator who needs a true
// restore-ready backup must explicitly pass ?include_secrets=1, which is
// audit-logged. Masked exports re-import cleanly: the ******** placeholder
// means "keep current value" (see resolveMaskedEntries).

const configExportFormat = "matea-config-v1"

// configImportMaxBody bounds the upload size of a config import document.
const configImportMaxBody = 1 << 20 // 1 MiB

// configToFlatMap renders the active config as a flat string map suitable for
// export/import. Every value is stringified to match what applyConfigEntry
// expects.
func configToFlatMap(cfg *config.Config) map[string]string {
	m := map[string]string{
		"gitea.url":             cfg.Gitea.URL,
		"gitea.admin_token":     cfg.Gitea.AdminToken,
		"gitea.webhook_secret":  cfg.Gitea.WebhookSecret,
		"gitea.auto_provision":  strconv.FormatBool(cfg.Gitea.AutoProvision),
		"server.public_url":     cfg.Server.PublicURL,
		"llm.defaults.provider": cfg.LLM.Defaults.Provider,
		"llm.defaults.model":    cfg.LLM.Defaults.Model,
		"dispatcher.max_concurrent":     strconv.Itoa(cfg.Dispatcher.MaxConcurrent),
		"dispatcher.task_retry_count":   strconv.Itoa(cfg.Dispatcher.TaskRetryCount),
		"dispatcher.rate_limit_backoff": strconv.Itoa(cfg.Dispatcher.RateLimitBackoff),
		"dispatcher.comment_history_limit": strconv.Itoa(cfg.Dispatcher.CommentHistoryLimit),
		"dispatcher.agent_concurrency":  cfg.Dispatcher.AgentConcurrency,
		"llm.rate_limit_retries":        strconv.Itoa(cfg.LLM.RateLimitRetries),
		"agents.defaults.provider":      cfg.Agents.Defaults.Provider,
		"agents.defaults.model":         cfg.Agents.Defaults.Model,
		"agents.defaults.max_output_tokens": strconv.Itoa(cfg.Agents.Defaults.MaxOutputTokens),
		"agents.defaults.max_input_tokens":  strconv.Itoa(cfg.Agents.Defaults.MaxInputTokens),
		"agents.defaults.temperature":       strconv.FormatFloat(cfg.Agents.Defaults.Temperature, 'f', -1, 64),
		"agents.defaults.timeout":           cfg.Agents.Defaults.Timeout,
		"agents.loop.max_iterations":        strconv.Itoa(cfg.Agents.Loop.MaxIterations),
		"agents.loop.total_timeout":         cfg.Agents.Loop.TotalTimeout,
		"agents.loop.iteration_interval":    strconv.Itoa(cfg.Agents.Loop.IterationInterval),
		"agents.loop.no_progress_limit":     strconv.Itoa(cfg.Agents.Loop.NoProgressLimit),
		"agents.loop.independent_checker":   strconv.FormatBool(cfg.Agents.Loop.IndependentChecker),
		"debug.conversation_log.enabled":          strconv.FormatBool(cfg.Debug.ConversationLog.Enabled),
		"debug.conversation_log.max_content_chars": strconv.Itoa(cfg.Debug.ConversationLog.MaxContentChars),
		"workflow.preset":      cfg.Workflow.Preset,
		"deliver.webhook_url":  cfg.Deliver.WebhookURL,
		"deliver.timeout":      cfg.Deliver.Timeout,
		"deliver.max_retries":  strconv.Itoa(cfg.Deliver.MaxRetries),
	}
	if b, err := json.Marshal(cfg.LLM.Providers); err == nil {
		m["llm.providers"] = string(b)
	}
	if b, err := json.Marshal(cfg.Agents.Backends); err == nil {
		m["agents.backends"] = string(b)
	}
	if b, err := json.Marshal(cfg.Agents.Loop.VerifyCommands); err == nil {
		m["agents.loop.verify_commands"] = string(b)
	}
	if b, err := json.Marshal(cfg.Workflow.Gates); err == nil {
		m["workflow.gates"] = string(b)
	}
	return m
}

// handleConfigExport handles GET /api/config/export (JWT required).
// Secrets are masked unless ?include_secrets=1 is passed explicitly; every
// export is audit-logged because this endpoint can read out every credential
// in the system at once.
func (h *Handler) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, http.StatusInternalServerError, "config manager not initialized")
		return
	}
	includeSecrets := r.URL.Query().Get("include_secrets") == "1" ||
		r.URL.Query().Get("include_secrets") == "true"
	flat := configToFlatMap(h.cfgManager.Get())
	if !includeSecrets {
		flat = maskFlatSecrets(flat)
	}
	if h.db != nil {
		h.db.LogOperation(0, 0, "config_export", fmt.Sprintf("keys=%d include_secrets=%v", len(flat), includeSecrets))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"format": configExportFormat,
		"config": flat,
		"masked": !includeSecrets,
	})
}

// maskFlatSecrets redacts sensitive values in an export map: scalar
// token/secret/api_key/password keys and deliver.webhook_url collapse to the
// placeholder; llm.providers and agents.backends keep their structure with
// only the credential fields masked.
func maskFlatSecrets(flat map[string]string) map[string]string {
	masked := make(map[string]string, len(flat))
	for key, value := range flat {
		switch {
		case value == "":
			masked[key] = value
		// The export key set is a hardcoded whitelist (configToFlatMap), so
		// exact matching is precise: the only scalar secrets are the two
		// gitea credentials. (Substring matching would over-mask e.g.
		// agents.defaults.max_input_tokens and break round-trip re-import.)
		case sensitiveConfigKeys[key] || strings.ToLower(key) == "deliver.webhook_url":
			masked[key] = configMaskedPlaceholder
		case key == "llm.providers":
			masked[key] = maskProvidersJSON(value)
		case key == "agents.backends":
			masked[key] = config.MaskSensitiveInBackendsJSON(value)
		default:
			masked[key] = value
		}
	}
	return masked
}

// maskProvidersJSON masks every api_key inside a llm.providers JSON document.
func maskProvidersJSON(payload string) string {
	providers, err := config.ParseProvidersJSON(payload)
	if err != nil {
		return configMaskedPlaceholder // unparseable — don't risk leaking
	}
	for name, pc := range providers {
		if pc.APIKey != "" {
			pc.APIKey = configMaskedPlaceholder
			providers[name] = pc
		}
	}
	data, err := json.Marshal(providers)
	if err != nil {
		return configMaskedPlaceholder
	}
	return string(data)
}

// configImportRequest carries the uploaded flat config.
type configImportRequest struct {
	Format string                 `json:"format"`
	Config map[string]interface{} `json:"config"`
}

// handleConfigImport handles POST /api/config/import (JWT required).
// Accepts the same shape as export (or a hand-edited equivalent) and applies
// every entry through the normal config pipeline, honouring "keep current"
// masks for secrets.
func (h *Handler) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, http.StatusInternalServerError, "config manager not initialized")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, configImportMaxBody)
	var req configImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Format != "" && req.Format != configExportFormat {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unsupported config format: %q (expect %s)", req.Format, configExportFormat))
		return
	}
	if len(req.Config) == 0 {
		writeError(w, http.StatusBadRequest, "no config entries provided")
		return
	}

	// Normalize to string→string, validating every key.
	entries := map[string]string{}
	for key, raw := range req.Config {
		if !config.IsConfigKey(key) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid config key: %s", key))
			return
		}
		entries[key] = stringifyEntry(raw)
	}

	entries = h.resolveMaskedEntries(entries)
	if len(entries) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"applied": []string{}, "errors": []string{},
			"message": "导入内容均为占位符（保持当前值），无实际变更",
		})
		return
	}

	// C-6 parity with PUT /api/config: importing Gitea coordinates without a
	// webhook_secret must not silently leave signature verification off.
	h.ensureWebhookSecret(entries)

	// Atomic batch: validated as one merged snapshot, applied all-or-nothing
	// (no partial-failure window, deterministic key order).
	if err := h.cfgManager.UpdateBatch(entries); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"applied": []string{}, "errors": []string{err.Error()},
			"message": "导入校验失败，未应用任何变更",
		})
		return
	}

	applied := make([]string, 0, len(entries))
	masked := make([]string, 0, len(entries))
	for key, value := range entries {
		applied = append(applied, key)
		masked = append(masked, maskConfigValue(key, value))
	}
	sort.Strings(applied)

	h.notifyConfigChange()
	if h.db != nil {
		h.db.LogOperation(0, 0, "config_import", strings.Join(masked, "; "))
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied": applied,
		"errors":  []string{},
		"message": fmt.Sprintf("已导入 %d 项", len(applied)),
	})
}

// ensureWebhookSecret adds a generated gitea.webhook_secret to the batch when
// Gitea coordinates are being written and no secret exists anywhere — an
// empty secret silently disables webhook signature verification (C-6).
func (h *Handler) ensureWebhookSecret(entries map[string]string) {
	_, touchesURL := entries["gitea.url"]
	_, touchesToken := entries["gitea.admin_token"]
	if !touchesURL && !touchesToken {
		return
	}
	if _, has := entries["gitea.webhook_secret"]; has {
		return
	}
	if h.cfgManager.Get().Gitea.WebhookSecret != "" {
		return
	}
	secret, err := config.GenerateWebhookSecret()
	if err != nil {
		return // best-effort; UpdateBatch validation still applies
	}
	entries["gitea.webhook_secret"] = secret
}

// stringifyEntry turns a decoded JSON value into the string form that
// applyConfigEntry expects. Numbers arrive as float64, booleans as bool, nested
// structures as json.Number/slice/map.
func stringifyEntry(v interface{}) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.Itoa(int(t))
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", t)
	}
}
