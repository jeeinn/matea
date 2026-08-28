package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/config"
)

// newTestHandler builds an api.Handler with an in-memory config manager (no
// DB store) — sufficient for unit-testing the health/env/export logic without
// a live database.
func newTestHandler(cfg *config.Config) *Handler {
	return &Handler{
		cfg:        cfg,
		cfgManager: config.NewConfigManager(cfg),
	}
}

func decodeHealth(t *testing.T, rec *httptest.ResponseRecorder) healthSummary {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var s healthSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatalf("decode health summary: %v", err)
	}
	return s
}

func TestHealthSummaryUnconfigured(t *testing.T) {
	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health/summary", nil)
	h.handleHealthSummary(rec, req)
	s := decodeHealth(t, rec)

	// With nothing configured, the system is still "healthy" (no errors), but
	// each dependency reports its unconfigured/disabled state.
	if !s.Healthy {
		t.Errorf("expected healthy=true for unconfigured deps, got false")
	}
	want := map[string]string{
		"gitea":        healthStatusUnconfigured,
		"llm":          healthStatusUnconfigured,
		"hub_backends": healthStatusDisabled,
		"deliver":      healthStatusDisabled,
		"database":     healthStatusDisabled,
	}
	for name, status := range want {
		c, ok := s.Components[name]
		if !ok {
			t.Errorf("missing component %q", name)
			continue
		}
		if c.Status != status {
			t.Errorf("component %q: want status %q, got %q", name, status, c.Status)
		}
	}
	if _, ok := s.Components["disk"]; !ok {
		t.Errorf("missing disk component")
	}
}

func TestHealthSummaryLLMProbeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o"}]}`))
	}))
	defer srv.Close()

	cfg := &config.Config{}
	cfg.LLM.Defaults.Provider = "openai"
	cfg.LLM.Providers = map[string]config.ProviderConfig{
		"openai": {Type: "openai_compatible", BaseURL: srv.URL, APIKey: "sk-test"},
	}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health/summary", nil)
	h.handleHealthSummary(rec, req)
	s := decodeHealth(t, rec)

	c := s.Components["llm"]
	if c.Status != healthStatusOK {
		t.Errorf("llm: want %q, got %q (%s)", healthStatusOK, c.Status, c.Message)
	}
}

func TestHealthSummaryLLMProbeUnreachable(t *testing.T) {
	cfg := &config.Config{}
	cfg.LLM.Defaults.Provider = "openai"
	// Point at a closed port so the probe fails fast.
	cfg.LLM.Providers = map[string]config.ProviderConfig{
		"openai": {Type: "openai_compatible", BaseURL: "http://127.0.0.1:1", APIKey: "sk-test"},
	}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/health/summary", nil)
	h.handleHealthSummary(rec, req)
	s := decodeHealth(t, rec)

	c := s.Components["llm"]
	if c.Status != healthStatusDegraded {
		t.Errorf("llm unreachable: want %q, got %q", healthStatusDegraded, c.Status)
	}
	if s.Healthy {
		t.Errorf("system should be unhealthy when default LLM is unreachable")
	}
}

func TestEnvDetection(t *testing.T) {
	t.Setenv("GITEA_URL", "http://localhost:3000")
	t.Setenv("OPENAI_API_KEY", "sk-xxx")
	t.Setenv("HERMES_URL", "http://localhost:9000")
	// SENSENOVA_API_KEY intentionally unset

	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/setup/env-detection", nil)
	h.handleEnvDetection(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Detected []envDetectItem `json:"detected"`
		Count    int             `json:"count"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 3 {
		t.Errorf("expected 3 detected, got %d", body.Count)
	}
	present := map[string]bool{}
	for _, d := range body.Detected {
		present[d.Env] = d.Present
	}
	for _, want := range []string{"GITEA_URL", "OPENAI_API_KEY", "HERMES_URL"} {
		if !present[want] {
			t.Errorf("expected %s present", want)
		}
	}
	if present["SENSENOVA_API_KEY"] {
		t.Errorf("SENSENOVA_API_KEY should be absent")
	}
}

func TestApplyEnvScalarAndProvider(t *testing.T) {
	t.Setenv("GITEA_URL", "http://localhost:3000")
	t.Setenv("OPENAI_API_KEY", "sk-apply-test")

	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply-env",
		strings.NewReader(`{"keys":["GITEA_URL","OPENAI_API_KEY"]}`))
	h.handleApplyEnv(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Applied []string `json:"applied"`
		Errors  []string `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Errors) != 0 {
		t.Errorf("unexpected apply errors: %v", body.Errors)
	}
	// GITEA_URL without an accompanying webhook secret triggers the C-6 guard:
	// a secret is auto-generated and reported as an extra applied entry.
	if len(body.Applied) != 3 {
		t.Errorf("expected 3 applied (incl. auto webhook_secret), got %v", body.Applied)
	}

	got := h.cfgManager.Get()
	if got.Gitea.WebhookSecret == "" {
		t.Errorf("C-6 guard: webhook_secret must be auto-generated when Gitea is configured")
	}
	if got.Gitea.URL != "http://localhost:3000" {
		t.Errorf("gitea.url not applied: %q", got.Gitea.URL)
	}
	pc, ok := got.LLM.Providers["openai"]
	if !ok {
		t.Fatalf("openai provider not created")
	}
	if pc.APIKey != "sk-apply-test" {
		t.Errorf("openai api_key not applied: %q", pc.APIKey)
	}
	if pc.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("openai base_url default not set: %q", pc.BaseURL)
	}
	if got.LLM.Defaults.Provider != "openai" {
		t.Errorf("default provider not set: %q", got.LLM.Defaults.Provider)
	}
}

func TestApplyEnvMissingSkipped(t *testing.T) {
	// OPENAI_API_KEY not set in env.
	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/setup/apply-env",
		strings.NewReader(`{"keys":["OPENAI_API_KEY"]}`))
	h.handleApplyEnv(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var body struct {
		Applied []string `json:"applied"`
		Skipped []string `json:"skipped"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Applied) != 0 {
		t.Errorf("expected nothing applied, got %v", body.Applied)
	}
	if len(body.Skipped) == 0 {
		t.Errorf("expected OPENAI_API_KEY in skipped")
	}
}

func TestConfigExportImportRoundTrip(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gitea.URL = "http://original:3000"
	cfg.Gitea.AdminToken = "secret-token"
	h := newTestHandler(cfg)

	// Export.
	expRec := httptest.NewRecorder()
	h.handleConfigExport(expRec, httptest.NewRequest(http.MethodGet, "/api/config/export", nil))
	if expRec.Code != http.StatusOK {
		t.Fatalf("export: expected 200, got %d", expRec.Code)
	}
	var exp struct {
		Format string            `json:"format"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(expRec.Body.Bytes(), &exp); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if exp.Format != configExportFormat {
		t.Errorf("unexpected export format %q", exp.Format)
	}
	if exp.Config["gitea.url"] != "http://original:3000" {
		t.Errorf("export did not include gitea.url: %v", exp.Config)
	}
	// Default export masks secrets (C-20 spec: 敏感字段脱敏).
	if exp.Config["gitea.admin_token"] != "********" {
		t.Errorf("default export must mask admin_token, got %q", exp.Config["gitea.admin_token"])
	}

	// A masked export re-imports cleanly: placeholder = keep current value.
	// (Re-import only the gitea keys — the zero-valued numeric entries of this
	// artificial test config would rightly fail range validation.)
	subset := map[string]string{
		"gitea.url":            exp.Config["gitea.url"],
		"gitea.admin_token":    exp.Config["gitea.admin_token"],
		"gitea.webhook_secret": exp.Config["gitea.webhook_secret"],
	}
	subsetBody, _ := json.Marshal(map[string]interface{}{"format": configExportFormat, "config": subset})
	reimpRec := httptest.NewRecorder()
	h.handleConfigImport(reimpRec, httptest.NewRequest(http.MethodPost, "/api/config/import",
		strings.NewReader(string(subsetBody))))
	if reimpRec.Code != http.StatusOK {
		t.Fatalf("re-import masked export: expected 200, got %d: %s", reimpRec.Code, reimpRec.Body.String())
	}
	if got := h.cfgManager.Get(); got.Gitea.AdminToken != "secret-token" {
		t.Errorf("masked re-import clobbered admin_token: %q", got.Gitea.AdminToken)
	}

	// Explicit opt-in yields a restore-ready plaintext backup.
	expRec2 := httptest.NewRecorder()
	h.handleConfigExport(expRec2, httptest.NewRequest(http.MethodGet, "/api/config/export?include_secrets=1", nil))
	var exp2 struct {
		Config map[string]string `json:"config"`
		Masked bool              `json:"masked"`
	}
	if err := json.Unmarshal(expRec2.Body.Bytes(), &exp2); err != nil {
		t.Fatalf("decode export2: %v", err)
	}
	if exp2.Masked {
		t.Errorf("include_secrets=1 must set masked=false")
	}
	if exp2.Config["gitea.admin_token"] != "secret-token" {
		t.Errorf("include_secrets=1 should include real admin_token, got %q", exp2.Config["gitea.admin_token"])
	}

	// Import a changed gitea.url; admin_token stays real.
	importBody := `{"format":"matea-config-v1","config":{"gitea.url":"http://imported:3000","gitea.admin_token":"secret-token"}}`
	impRec := httptest.NewRecorder()
	h.handleConfigImport(impRec, httptest.NewRequest(http.MethodPost, "/api/config/import",
		strings.NewReader(importBody)))
	if impRec.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d: %s", impRec.Code, impRec.Body.String())
	}
	got := h.cfgManager.Get()
	if got.Gitea.URL != "http://imported:3000" {
		t.Errorf("import did not update gitea.url: %q", got.Gitea.URL)
	}
	if got.Gitea.AdminToken != "secret-token" {
		t.Errorf("import clobbered admin_token: %q", got.Gitea.AdminToken)
	}
}

func TestConfigImportMaskKeepsCurrent(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gitea.AdminToken = "real-secret"
	h := newTestHandler(cfg)

	// Import with the masked placeholder for admin_token: must be skipped so the
	// real value is preserved.
	importBody := `{"config":{"gitea.admin_token":"********"}}`
	impRec := httptest.NewRecorder()
	h.handleConfigImport(impRec, httptest.NewRequest(http.MethodPost, "/api/config/import",
		strings.NewReader(importBody)))
	if impRec.Code != http.StatusOK {
		t.Fatalf("import: expected 200, got %d", impRec.Code)
	}
	if got := h.cfgManager.Get(); got.Gitea.AdminToken != "real-secret" {
		t.Errorf("masked admin_token must keep current value, got %q", got.Gitea.AdminToken)
	}
}

func TestConfigImportRejectsInvalidKey(t *testing.T) {
	cfg := &config.Config{}
	h := newTestHandler(cfg)

	importBody := `{"config":{"not.a.real.key":"x"}}`
	impRec := httptest.NewRecorder()
	h.handleConfigImport(impRec, httptest.NewRequest(http.MethodPost, "/api/config/import",
		strings.NewReader(importBody)))
	if impRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid key, got %d", impRec.Code)
	}
}

// D3 regression: re-absorbing an env key must update the api_key but never
// reset an existing provider's custom base_url/type to catalog defaults.
func TestApplyEnvPreservesExistingProviderBaseURL(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "sk-new-key")

	cfg := &config.Config{}
	cfg.LLM.Providers = map[string]config.ProviderConfig{
		"deepseek": {Type: "openai_compatible", BaseURL: "https://my-proxy.internal/v1", APIKey: "sk-old"},
	}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	h.handleApplyEnv(rec, httptest.NewRequest(http.MethodPost, "/api/setup/apply-env",
		strings.NewReader(`{"keys":["DEEPSEEK_API_KEY"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	pc := h.cfgManager.Get().LLM.Providers["deepseek"]
	if pc.APIKey != "sk-new-key" {
		t.Errorf("api_key should be updated: %q", pc.APIKey)
	}
	if pc.BaseURL != "https://my-proxy.internal/v1" {
		t.Errorf("custom base_url must be preserved, got %q", pc.BaseURL)
	}
}

// applyEnvHubBackend coverage: OPENCODE_URL creates a hub-opencode backend
// and becomes the default when none is set.
func TestApplyEnvHubBackend(t *testing.T) {
	t.Setenv("OPENCODE_URL", "http://opencode.local:4096")

	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	h.handleApplyEnv(rec, httptest.NewRequest(http.MethodPost, "/api/setup/apply-env",
		strings.NewReader(`{"keys":["OPENCODE_URL"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	backends := h.cfgManager.Get().Agents.Backends
	bc, ok := backends.Backends["opencode"]
	if !ok {
		t.Fatalf("opencode backend not created: %+v", backends.Backends)
	}
	if bc.Type != config.BackendTypeHubOpenCode || bc.BaseURL != "http://opencode.local:4096" {
		t.Errorf("backend mismatch: %+v", bc)
	}
	if backends.Default != "opencode" {
		t.Errorf("default backend not set: %q", backends.Default)
	}
}

// C-6 parity: an env-absorbed webhook secret must NOT be replaced by the
// auto-generation guard when Gitea coordinates are absorbed alongside it.
func TestApplyEnvKeepsAbsorbedWebhookSecret(t *testing.T) {
	t.Setenv("GITEA_URL", "http://localhost:3000")
	t.Setenv("GITEA_WEBHOOK_SECRET", "env-secret-keep-me")

	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	h.handleApplyEnv(rec, httptest.NewRequest(http.MethodPost, "/api/setup/apply-env",
		strings.NewReader(`{"keys":["GITEA_URL","GITEA_WEBHOOK_SECRET"]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := h.cfgManager.Get().Gitea.WebhookSecret; got != "env-secret-keep-me" {
		t.Errorf("env-absorbed webhook_secret must survive, got %q", got)
	}
}

func TestConfigImportRejectsUnknownFormat(t *testing.T) {
	cfg := &config.Config{}
	h := newTestHandler(cfg)

	importBody := `{"format":"other-system-v9","config":{"gitea.url":"http://x:3000"}}`
	impRec := httptest.NewRecorder()
	h.handleConfigImport(impRec, httptest.NewRequest(http.MethodPost, "/api/config/import",
		strings.NewReader(importBody)))
	if impRec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unknown format, got %d", impRec.Code)
	}
	if got := h.cfgManager.Get(); got.Gitea.URL != "" {
		t.Errorf("rejected import must not apply anything, got gitea.url=%q", got.Gitea.URL)
	}
}

// TestHealthSummaryUsesLiveConfig is the R1 regression test: the health panel
// must reflect hot-updated config, not the startup snapshot. After a config
// write, gitea must leave "unconfigured" (it becomes "error" here because the
// pointed-at server does not exist — which proves the probe used the NEW cfg).
func TestHealthSummaryUsesLiveConfig(t *testing.T) {
	cfg := &config.Config{}
	h := newTestHandler(cfg)

	rec := httptest.NewRecorder()
	h.handleHealthSummary(rec, httptest.NewRequest(http.MethodGet, "/api/health/summary", nil))
	if got := decodeHealth(t, rec).Components["gitea"].Status; got != healthStatusUnconfigured {
		t.Fatalf("empty config: expected gitea unconfigured, got %q", got)
	}

	if err := h.cfgManager.Update("gitea.url", "http://127.0.0.1:1"); err != nil {
		t.Fatalf("update gitea.url: %v", err)
	}
	if err := h.cfgManager.Update("gitea.admin_token", "x"); err != nil {
		t.Fatalf("update gitea.admin_token: %v", err)
	}

	rec2 := httptest.NewRecorder()
	h.handleHealthSummary(rec2, httptest.NewRequest(http.MethodGet, "/api/health/summary", nil))
	got := decodeHealth(t, rec2).Components["gitea"].Status
	if got == healthStatusUnconfigured {
		t.Fatalf("after hot config update gitea still shows unconfigured — stale startup snapshot (R1)")
	}
	if got != healthStatusError {
		t.Fatalf("expected gitea error (unreachable 127.0.0.1:1), got %q", got)
	}
}
