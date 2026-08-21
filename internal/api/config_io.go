package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jeeinn/matea/internal/config"
)

// --- Config export / import (Phase 2.5: C-20) ---
//
// A faithful backup of the admin-managed flat config. Export emits a
// string-keyed map that round-trips through applyConfigEntry on import, so a
// downloaded file can be re-uploaded verbatim. Secrets are included in the
// export (needed for a restore to actually work) — the endpoint is JWT
// protected and the file is the operator's responsibility.

const configExportFormat = "matea-config-v1"

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
func (h *Handler) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, http.StatusInternalServerError, "config manager not initialized")
		return
	}
	flat := configToFlatMap(h.cfgManager.Get())
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"format": configExportFormat,
		"config": flat,
	})
}

// configImportRequest carries the uploaded flat config.
type configImportRequest struct {
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
	var req configImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
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

	var applied, applyErrs []string
	for key, value := range entries {
		if err := h.cfgManager.Update(key, value); err != nil {
			applyErrs = append(applyErrs, fmt.Sprintf("%s: %s", key, err.Error()))
			continue
		}
		applied = append(applied, key)
	}

	if len(applied) > 0 {
		h.notifyConfigChange()
		if h.db != nil {
			masked := make([]string, 0, len(applied))
			for _, k := range applied {
				masked = append(masked, maskConfigValue(k, entries[k]))
			}
			h.db.LogOperation(0, 0, "config_import", strings.Join(masked, "; "))
		}
	}

	status := http.StatusOK
	if len(applied) == 0 && len(applyErrs) > 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]interface{}{
		"applied": applied,
		"errors":  applyErrs,
		"message": fmt.Sprintf("已导入 %d 项，%d 项失败", len(applied), len(applyErrs)),
	})
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
