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
		"gitea":       healthStatusUnconfigured,
		"llm":         healthStatusUnconfigured,
		"hub_backends": healthStatusDisabled,
		"deliver":     healthStatusDisabled,
		"database":    healthStatusDisabled,
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
	if len(body.Applied) != 2 {
		t.Errorf("expected 2 applied, got %v", body.Applied)
	}

	got := h.cfgManager.Get()
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
	if exp.Config["gitea.admin_token"] != "secret-token" {
		t.Errorf("export should include real admin_token for restore")
	}

	// Import a changed gitea.url; admin_token stays real.
	importBody := `{"config":{"gitea.url":"http://imported:3000","gitea.admin_token":"secret-token"}}`
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
