package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
)

// --- First-run setup endpoints (Phase 2.5: C-1..C-8) ---
//
// /api/setup/status is PUBLIC (no JWT, no token): the unauthenticated Web UI
// needs it to decide between the setup wizard and the login page. It reveals
// only which config keys are missing — never values.
//
// All other /api/setup/* endpoints are gated by the Setup Token (C-2): a
// one-time random token printed to the console at startup while setup is
// incomplete. They auto-disable (403) once CheckSetup reports complete.

// setupStatusResponse extends config.SetupStatus with a hint for the wizard
// that token entry is required before any mutation endpoint answers.
type setupStatusResponse struct {
	config.SetupStatus
	TokenRequired bool `json:"token_required"`
}

func (h *Handler) getSetupStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.cfg
	if h.cfgManager != nil {
		cfg = h.cfgManager.Get()
	}
	if cfg == nil {
		writeError(w, 500, "config not available")
		return
	}
	st := config.CheckSetup(cfg)
	writeJSON(w, 200, setupStatusResponse{
		SetupStatus:   st,
		TokenRequired: st.SetupRequired,
	})
}

// setupRequired reports whether setup is still incomplete (used to
// auto-disable the mutation endpoints after completion).
func (h *Handler) setupRequired() bool {
	cfg := h.cfg
	if h.cfgManager != nil {
		cfg = h.cfgManager.Get()
	}
	if cfg == nil {
		return false
	}
	return config.CheckSetup(cfg).SetupRequired
}

// requireSetupToken gates an endpoint on (a) setup still being required and
// (b) a valid Setup Token in X-Setup-Token / Authorization: Bearer.
func (h *Handler) requireSetupToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.setupRequired() {
			writeError(w, 403, "初始化已完成，setup 端点已关闭")
			return
		}
		if h.setupTokens == nil {
			writeError(w, 403, "setup token 未启用")
			return
		}
		token := setupTokenFromRequest(r.Header.Get("Authorization"), r.Header.Get("X-Setup-Token"))
		if !h.setupTokens.Validate(token) {
			writeError(w, 401, "Setup Token 无效或已过期（新 Token 已打印到服务控制台）")
			return
		}
		next(w, r)
	}
}

// verifySetupToken lets the wizard validate the token before step 1.
func (h *Handler) verifySetupToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]interface{}{
		"ok":         true,
		"expires_at": h.setupTokens.ExpiresAt(),
	})
}

// --- test endpoints (wizard "测试连接" buttons) ---

// testSetupGitea validates a candidate Gitea URL + token (C-7: reports the
// admin-scope warning via TestConnection).
func (h *Handler) testSetupGitea(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	url := strings.TrimRight(strings.TrimSpace(payload.URL), "/")
	token := strings.TrimSpace(payload.Token)
	if url == "" || token == "" {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "Gitea 地址与 Token 均不能为空"})
		return
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "Gitea 地址必须以 http:// 或 https:// 开头"})
		return
	}

	result, err := gitea.NewClient(url, token).TestConnection()
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

// --- complete ---

var setupProviderNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

type setupCompleteRequest struct {
	Gitea struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"gitea"`
	LLM struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
		Type     string `json:"type"`
	} `json:"llm"`
	// WebhookSecret is optional; auto-generated (32-byte hex) when empty (C-6).
	WebhookSecret string `json:"webhook_secret"`
}

// completeSetup is the wizard finish: validate → re-test Gitea → batch-write
// config to DB (hot-reload via onConfigChange, C-8) → audit (C-16) →
// invalidate the setup token.
func (h *Handler) completeSetup(w http.ResponseWriter, r *http.Request) {
	if h.cfgManager == nil {
		writeError(w, 500, "config manager not initialized")
		return
	}

	var req setupCompleteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}

	giteaURL := strings.TrimRight(strings.TrimSpace(req.Gitea.URL), "/")
	giteaToken := strings.TrimSpace(req.Gitea.Token)
	provider := strings.TrimSpace(req.LLM.Provider)
	model := strings.TrimSpace(req.LLM.Model)
	baseURL := strings.TrimRight(strings.TrimSpace(req.LLM.BaseURL), "/")
	apiKey := strings.TrimSpace(req.LLM.APIKey)
	providerType := strings.TrimSpace(req.LLM.Type)
	if providerType == "" {
		providerType = "openai_compatible"
	}

	// Shape validation (all errors collected so the wizard can show them at once).
	var problems []string
	if giteaURL == "" {
		problems = append(problems, "gitea.url 不能为空")
	} else if !strings.HasPrefix(giteaURL, "http://") && !strings.HasPrefix(giteaURL, "https://") {
		problems = append(problems, "gitea.url 必须以 http:// 或 https:// 开头")
	}
	if giteaToken == "" {
		problems = append(problems, "gitea token 不能为空")
	}
	if !setupProviderNameRe.MatchString(provider) {
		problems = append(problems, "provider 名称只能包含小写字母、数字、连字符")
	}
	if model == "" {
		problems = append(problems, "llm model 不能为空")
	}
	if baseURL == "" {
		problems = append(problems, "llm base_url 不能为空")
	}
	if apiKey == "" && !isLikelyLocalBaseURL(baseURL) {
		problems = append(problems, "远程 LLM 必须填写 api_key")
	}
	if len(problems) > 0 {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": strings.Join(problems, "；")})
		return
	}

	// Authoritative re-test of Gitea before persisting (cheap, catches typos
	// even if the wizard's own test step was skipped).
	testRes, err := gitea.NewClient(giteaURL, giteaToken).TestConnection()
	if err != nil {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "Gitea 连接测试失败: " + err.Error()})
		return
	}
	if !testRes.OK {
		writeJSON(w, 400, map[string]interface{}{"ok": false, "message": "Gitea 连接测试未通过: " + testRes.Message})
		return
	}

	webhookSecret := strings.TrimSpace(req.WebhookSecret)
	secretGenerated := false
	if webhookSecret == "" {
		webhookSecret, err = config.GenerateWebhookSecret()
		if err != nil {
			writeError(w, 500, "生成 webhook_secret 失败: "+err.Error())
			return
		}
		secretGenerated = true
	}

	// Merge the provider into the existing providers map so other configured
	// providers survive the wizard.
	active := h.cfgManager.Get()
	providers := map[string]config.ProviderConfig{}
	for name, pc := range active.LLM.Providers {
		providers[name] = pc
	}
	merged := providers[provider] // keep existing models metadata if re-running
	merged.BaseURL = baseURL
	merged.APIKey = apiKey
	merged.Type = providerType
	providers[provider] = merged
	providersJSON, err := json.Marshal(providers)
	if err != nil {
		writeError(w, 500, "序列化 providers 失败: "+err.Error())
		return
	}

	entries := map[string]string{
		"gitea.url":            giteaURL,
		"gitea.admin_token":    giteaToken,
		"gitea.webhook_secret": webhookSecret,
		"llm.providers":        string(providersJSON),
		"llm.defaults.provider": provider,
		"llm.defaults.model":    model,
	}
	for key, value := range entries {
		if err := h.cfgManager.Update(key, value); err != nil {
			writeError(w, 500, fmt.Sprintf("写入配置 %s 失败: %v", key, err))
			return
		}
	}

	// Hot-reload LLM registry / Gitea clients / webhook ingress (C-8).
	h.notifyConfigChange()

	// Audit (C-16): never log secrets.
	if h.db != nil {
		h.db.LogOperation(0, 0, "setup_complete",
			fmt.Sprintf("初始化完成: gitea.url=%s gitea_user=%s llm.provider=%s llm.model=%s webhook_secret=%s",
				giteaURL, testRes.Username, provider, model, maskSecret(webhookSecret)))
	}

	// Close the setup surface.
	if h.setupTokens != nil {
		h.setupTokens.Invalidate()
	}

	msg := "初始化完成，配置已生效"
	if !testRes.IsAdmin {
		msg += "；警告：Gitea 用户非管理员，自动创建 Agent 账号需要 write:admin 权限"
	}
	resp := map[string]interface{}{
		"ok":      true,
		"message": msg,
	}
	if secretGenerated {
		// Returned once so the operator can paste it into Gitea webhook
		// settings; afterwards it is only visible via System Config.
		resp["webhook_secret"] = webhookSecret
		resp["webhook_secret_generated"] = true
	}
	writeJSON(w, 200, resp)
}

// maskSecret keeps only the last 4 chars for audit/display.
func maskSecret(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// isLikelyLocalBaseURL mirrors config.isLikelyLocalLLM for payload-time
// validation (loopback / RFC1918 endpoints may omit api_key).
func isLikelyLocalBaseURL(baseURL string) bool {
	u := strings.ToLower(strings.TrimSpace(baseURL))
	if u == "" {
		return false
	}
	if strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") ||
		strings.Contains(u, "0.0.0.0") || strings.Contains(u, "[::1]") || strings.Contains(u, "://::1") {
		return true
	}
	for _, p := range []string{
		"://10.", "://192.168.",
		"://172.16.", "://172.17.", "://172.18.", "://172.19.",
		"://172.20.", "://172.21.", "://172.22.", "://172.23.",
		"://172.24.", "://172.25.", "://172.26.", "://172.27.",
		"://172.28.", "://172.29.", "://172.30.", "://172.31.",
	} {
		if strings.Contains(u, p) {
			return true
		}
	}
	return false
}
