package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sysinfo"
)

// Health component status values.
const (
	healthStatusOK          = "ok"          // reachable / configured and working
	healthStatusDegraded    = "degraded"    // configured but incomplete or unreachable
	healthStatusUnconfigured = "unconfigured" // required input missing
	healthStatusDisabled    = "disabled"    // feature not in use (no-op, not an error)
	healthStatusError       = "error"       // configured but failed to reach
)

// diskWarnPct is the used-space threshold (percent) at or above which the
// disk component is flagged "warning". 85% mirrors common ops alerting
// practice. CheckDisk treats warnPct as the USED percentage, not free.
const diskWarnPct = 85

// probeTimeout bounds each external health probe so a single slow dependency
// cannot hang the summary endpoint.
const probeTimeout = 6 * time.Second

// healthComponent is one line in the health summary.
type healthComponent struct {
	Status  string                 `json:"status"`
	Message string                 `json:"message,omitempty"`
	Detail  map[string]interface{} `json:"detail,omitempty"`
}

// hubBackendHealth reports one coding backend's readiness.
type hubBackendHealth struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// healthSummary is the payload of GET /api/health/summary.
type healthSummary struct {
	Healthy     bool                       `json:"healthy"`
	GeneratedAt time.Time                  `json:"generated_at"`
	Components  map[string]healthComponent `json:"components"`
	HubBackends []hubBackendHealth         `json:"hub_backends,omitempty"`
	Warnings    []string                   `json:"warnings,omitempty"`
}

// handleHealthSummary reports the readiness of every external dependency.
// GET /api/health/summary (JWT required).
func (h *Handler) handleHealthSummary(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout*2)
	defer cancel()

	summary := healthSummary{
		GeneratedAt: time.Now(),
		Components:  map[string]healthComponent{},
	}

	summary.Components["gitea"] = h.checkGitea(ctx)
	llmComp, llmWarn := h.checkLLM(ctx)
	summary.Components["llm"] = llmComp
	if llmWarn != "" {
		summary.Warnings = append(summary.Warnings, llmWarn)
	}
	hubComp, hubs := h.checkHubBackends(ctx)
	summary.Components["hub_backends"] = hubComp
	summary.HubBackends = hubs
	summary.Components["deliver"] = h.checkDeliver()
	summary.Components["database"] = h.checkDatabase(ctx)
	diskComp, diskWarn := h.checkDisk()
	summary.Components["disk"] = diskComp
	if diskWarn != "" {
		summary.Warnings = append(summary.Warnings, diskWarn)
	}

	// Overall health: a critical component that is configured but broken
	// (error OR degraded) fails the whole system. An unconfigured component is
	// acceptable (server is up, just not wired up yet). Hub/deliver "degraded"
	// or "disabled" and disk "warning" do not fail overall health.
	summary.Healthy = true
	critical := map[string]bool{"gitea": true, "llm": true, "database": true}
	for name, c := range summary.Components {
		if name == "disk" {
			if c.Status == healthStatusError {
				summary.Healthy = false
			}
			continue
		}
		if critical[name] && (c.Status == healthStatusError || c.Status == healthStatusDegraded) {
			summary.Healthy = false
		}
	}

	writeJSON(w, http.StatusOK, summary)
}

// checkGitea verifies Gitea URL + token by calling the real API.
func (h *Handler) checkGitea(ctx context.Context) healthComponent {
	g := h.cfg.Gitea
	if g.URL == "" || g.AdminToken == "" {
		return healthComponent{
			Status:  healthStatusUnconfigured,
			Message: "Gitea 地址或管理员 Token 未配置",
		}
	}
	client := gitea.NewClient(g.URL, g.AdminToken)
	var res *gitea.ConnectionTestResult
	probeErr := withTimeout(probeTimeout, func() error {
		var err error
		res, err = client.TestConnection()
		return err
	})
	if probeErr != nil {
		return healthComponent{
			Status:  healthStatusError,
			Message: fmt.Sprintf("Gitea 连接失败: %s", probeErr.Error()),
		}
	}
	if res == nil || !res.OK {
		return healthComponent{
			Status:  healthStatusError,
			Message: res.Message,
		}
	}
	return healthComponent{
		Status:  healthStatusOK,
		Message: res.Message,
		Detail: map[string]interface{}{
			"username":   res.Username,
			"is_admin":   res.IsAdmin,
			"repo_count": res.RepoCount,
		},
	}
}

// checkLLM reports whether the default LLM provider is configured and, for
// OpenAI-compatible providers, whether it is actually reachable.
func (h *Handler) checkLLM(ctx context.Context) (healthComponent, string) {
	def := h.cfg.LLM.Defaults.Provider
	if def == "" {
		return healthComponent{
			Status:  healthStatusUnconfigured,
			Message: "未配置默认 LLM provider（llm.defaults.provider）",
		}, ""
	}
	prov, ok := h.cfg.LLM.Providers[def]
	if !ok {
		return healthComponent{
			Status:  healthStatusDegraded,
			Message: fmt.Sprintf("默认 provider %q 未在 llm.providers 中定义", def),
		}, ""
	}
	if prov.APIKey == "" {
		return healthComponent{
			Status:  healthStatusDegraded,
			Message: fmt.Sprintf("provider %q 缺少 api_key", def),
		}, ""
	}
	if prov.BaseURL == "" {
		return healthComponent{
			Status:  healthStatusDegraded,
			Message: fmt.Sprintf("provider %q 缺少 base_url", def),
		}, ""
	}

	// OpenAI-compatible providers expose a models list we can probe cheaply
	// (no token cost). Anthropic has no list endpoint, so "configured" is the
	// best we can assert without a chat round-trip.
	if prov.Type == "openai_compatible" {
		probeErr := withTimeout(probeTimeout, func() error {
			p := llm.NewOpenAICompatibleProvider(prov.BaseURL, prov.APIKey)
			p.HTTPClient.Timeout = probeTimeout
			c, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			_, err := p.ListModels(c)
			return err
		})
		if probeErr != nil {
			return healthComponent{
				Status:  healthStatusDegraded,
				Message: fmt.Sprintf("provider %q 配置完整但不可达: %s", def, probeErr.Error()),
				Detail:  map[string]interface{}{"provider": def, "reachable": false},
			}, fmt.Sprintf("LLM provider %q 不可达", def)
		}
		return healthComponent{
			Status:  healthStatusOK,
			Message: fmt.Sprintf("provider %q 配置完整且可达", def),
			Detail:  map[string]interface{}{"provider": def, "reachable": true},
		}, ""
	}

	// Non-probeable provider types: report configured.
	return healthComponent{
		Status:  healthStatusOK,
		Message: fmt.Sprintf("provider %q 已配置（type=%s，未做实探）", def, prov.Type),
		Detail:  map[string]interface{}{"provider": def, "reachable": nil},
	}, ""
}

// checkHubBackends probes each hub (non-builtin) coding backend's health
// endpoint and reports aggregate status.
func (h *Handler) checkHubBackends(ctx context.Context) (healthComponent, []hubBackendHealth) {
	cfg := h.cfg.Agents.Backends
	var hubs []hubBackendHealth
	for name, bc := range cfg.Backends {
		if bc.Type == config.BackendTypeBuiltin {
			continue
		}
		hb := hubBackendHealth{Name: name, Type: bc.Type}
		path := bc.HealthCheck.Path
		if path == "" {
			path = "/health"
		}
		u := strings.TrimRight(bc.BaseURL, "/") + path
		probeErr := withTimeout(probeTimeout, func() error {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
			if err != nil {
				return err
			}
			if bc.Auth.Username != "" || bc.Auth.Password != "" {
				req.SetBasicAuth(bc.Auth.Username, bc.Auth.Password)
			}
			client := &http.Client{Timeout: probeTimeout}
			resp, err := client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			return nil
		})
		if probeErr != nil {
			hb.Status = healthStatusError
			hb.Message = probeErr.Error()
		} else {
			hb.Status = healthStatusOK
		}
		hubs = append(hubs, hb)
	}

	if len(hubs) == 0 {
		return healthComponent{
			Status:  healthStatusDisabled,
			Message: "未配置 Hub 后端（仅使用内置 builtin 编码后端）",
		}, nil
	}

	allOK := true
	for _, hb := range hubs {
		if hb.Status != healthStatusOK {
			allOK = false
			break
		}
	}
	status := healthStatusOK
	msg := fmt.Sprintf("%d 个 Hub 后端全部健康", len(hubs))
	if !allOK {
		status = healthStatusDegraded
		msg = "部分 Hub 后端不可达"
	}
	return healthComponent{Status: status, Message: msg}, hubs
}

// checkDeliver reports whether outbound delivery is configured.
func (h *Handler) checkDeliver() healthComponent {
	if h.cfg.Deliver.WebhookURL == "" {
		return healthComponent{
			Status:  healthStatusDisabled,
			Message: "未配置出站投递 Webhook（deliver.webhook_url 为空）",
		}
	}
	return healthComponent{
		Status:  healthStatusOK,
		Message: fmt.Sprintf("出站投递已配置: %s", maskURL(h.cfg.Deliver.WebhookURL)),
	}
}

// checkDatabase pings the SQLite store.
func (h *Handler) checkDatabase(ctx context.Context) healthComponent {
	if h.db == nil {
		return healthComponent{
			Status:  healthStatusDisabled,
			Message: "数据库未初始化",
		}
	}
	c, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := h.db.PingContext(c); err != nil {
		return healthComponent{
			Status:  healthStatusError,
			Message: fmt.Sprintf("数据库 Ping 失败: %s", err.Error()),
		}
	}
	return healthComponent{
		Status:  healthStatusOK,
		Message: "数据库连接正常",
	}
}

// checkDisk reports free-space on the volume holding the data directory.
func (h *Handler) checkDisk() (healthComponent, string) {
	dir := healthDataDir(h.cfg)
	info, err := sysinfo.CheckDisk(dir, diskWarnPct)
	if err != nil {
		return healthComponent{
			Status:  healthStatusError,
			Message: fmt.Sprintf("磁盘检测失败: %s", err.Error()),
		}, ""
	}
	comp := healthComponent{
		Detail: map[string]interface{}{
			"path":        info.Path,
			"free_bytes":  info.FreeBytes,
			"total_bytes": info.TotalBytes,
			"used_bytes":  info.UsedBytes,
			"used_pct":    info.UsedPercent,
			"free_pct":    100 - info.UsedPercent,
		},
	}
	if info.Warning {
		comp.Status = healthStatusDegraded
		comp.Message = fmt.Sprintf("%s 磁盘使用率已达 %.1f%%（剩余 %.1f GB / 共 %.1f GB）",
			info.Path, info.UsedPercent, float64(info.FreeBytes)/1e9, float64(info.TotalBytes)/1e9)
		return comp, comp.Message
	}
	comp.Status = healthStatusOK
	comp.Message = fmt.Sprintf("%s 剩余 %.1f GB / 共 %.1f GB", info.Path, float64(info.FreeBytes)/1e9, float64(info.TotalBytes)/1e9)
	return comp, ""
}

// healthDataDir resolves the directory whose filesystem should be checked:
// the directory containing the SQLite file, or cwd when no path is set.
func healthDataDir(cfg *config.Config) string {
	p := cfg.Database.Path
	if p == "" {
		return "."
	}
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		return filepath.Dir(p)
	}
	return p
}

// maskURL redacts the userinfo / query of a webhook URL for safe display.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.User != nil {
		u.User = url.UserPassword("***", "***")
	}
	return u.String()
}

// withTimeout runs fn in a goroutine and returns its error, or a timeout error
// if fn does not finish within d. Used for probes whose underlying client does
// not honour a context (e.g. gitea.TestConnection).
func withTimeout(d time.Duration, fn func() error) error {
	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(d):
		return fmt.Errorf("timeout after %s", d)
	}
}
