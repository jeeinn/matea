package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

// incompleteConfig returns a config that fails CheckSetup (no Gitea, no LLM).
func incompleteConfig() *config.Config {
	return &config.Config{}
}

// completeConfig returns a config that passes CheckSetup.
func completeConfig() *config.Config {
	return &config.Config{
		Gitea: config.GiteaConfig{URL: "http://gitea.local", AdminToken: "tok"},
		LLM: config.LLMConfig{
			Providers: map[string]config.ProviderConfig{
				"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-x"},
			},
			Defaults: config.LLMDefaultsConfig{Provider: "deepseek", Model: "deepseek-v4-flash"},
		},
	}
}

func newSetupTestHandler(t *testing.T, cfg *config.Config) (*Handler, *store.DB, *config.ConfigManager, *SetupTokenManager) {
	t.Helper()
	db := openTestDB(t)
	mgr := config.NewConfigManager(cfg)
	mgr.SetStore(db)
	h := NewHandler(db, nil, cfg, nil, mgr, nil)
	tokens := NewSetupTokenManager()
	// Keep test output clean: swallow the console banner.
	tokens.announce = func(string, ...interface{}) {}
	h.SetSetupTokenManager(tokens)
	return h, db, mgr, tokens
}

func setupMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/setup/status", h.getSetupStatus)
	mux.HandleFunc("POST /api/setup/verify", h.requireSetupToken(h.verifySetupToken))
	mux.HandleFunc("GET /api/setup/detect", h.requireSetupToken(h.detectLocalServices))
	mux.HandleFunc("POST /api/setup/test/gitea", h.requireSetupToken(h.testSetupGitea))
	mux.HandleFunc("POST /api/setup/test/llm", h.requireSetupToken(h.testSetupLLM))
	mux.HandleFunc("POST /api/setup/complete", h.requireSetupToken(h.completeSetup))
	return mux
}

// fakeSetupGitea serves the API surface gitea.TestConnection touches:
// GET /user and GET /repos/search.
func fakeSetupGitea(t *testing.T, user string, isAdmin bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"login": user, "is_admin": isAdmin})
	})
	mux.HandleFunc("/api/v1/repos/search", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"data": []map[string]string{{"full_name": "o/r"}}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func doSetupReq(t *testing.T, mux *http.ServeMux, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("X-Setup-Token", token)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// --- C-2: setup token manager ---

func TestSetupTokenManagerLifecycle(t *testing.T) {
	m := NewSetupTokenManager()
	m.announce = func(string, ...interface{}) {}

	tok := m.Token()
	require.Len(t, tok, 48, "24 bytes hex-encoded")
	assert.True(t, m.Validate(tok))
	assert.False(t, m.Validate("wrong"))
	assert.False(t, m.Validate(""))

	// Expiry: jump past the TTL — validation fails and a NEW token is issued.
	m.now = func() time.Time { return time.Now().Add(SetupTokenTTL + time.Minute) }
	assert.False(t, m.Validate(tok))
	newTok := m.Token()
	assert.NotEqual(t, tok, newTok, "expired token must be regenerated")
	assert.True(t, m.Validate(newTok))

	// Invalidate: setup surface closed.
	m.Invalidate()
	assert.False(t, m.Validate(newTok))
	assert.Equal(t, "", m.Token())
}

// --- public status + token gate ---

func TestSetupStatusPublic(t *testing.T) {
	h, _, _, _ := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	w := doSetupReq(t, mux, "GET", "/api/setup/status", "", nil)
	require.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["setup_required"])
	assert.Equal(t, true, resp["token_required"])
	missing, _ := resp["missing"].([]interface{})
	assert.NotEmpty(t, missing)
	// Must never leak config values — only key names.
	assert.NotContains(t, w.Body.String(), "admin123")
}

func TestSetupStatusCompleteConfig(t *testing.T) {
	h, _, _, _ := newSetupTestHandler(t, completeConfig())
	mux := setupMux(h)

	w := doSetupReq(t, mux, "GET", "/api/setup/status", "", nil)
	require.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["setup_required"])
	assert.Equal(t, false, resp["token_required"])
}

func TestSetupEndpointsRequireToken(t *testing.T) {
	h, _, _, tokens := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	// No token → 401
	w := doSetupReq(t, mux, "POST", "/api/setup/verify", "", nil)
	assert.Equal(t, 401, w.Code)

	// Wrong token → 401
	w = doSetupReq(t, mux, "POST", "/api/setup/verify", "nope", nil)
	assert.Equal(t, 401, w.Code)

	// Correct token → 200
	w = doSetupReq(t, mux, "POST", "/api/setup/verify", tokens.Token(), nil)
	require.Equal(t, 200, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["ok"])

	// Bearer header also accepted.
	req := httptest.NewRequest("POST", "/api/setup/verify", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer "+tokens.Token())
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 200, w.Code)
}

func TestSetupEndpointsDisabledWhenComplete(t *testing.T) {
	h, _, _, tokens := newSetupTestHandler(t, completeConfig())
	mux := setupMux(h)

	// Even a valid token cannot use setup endpoints after initialization.
	w := doSetupReq(t, mux, "POST", "/api/setup/verify", tokens.Token(), nil)
	assert.Equal(t, 403, w.Code)
}

// --- complete flow (C-6 auto webhook secret, C-8 hot write, C-16 audit) ---

func TestSetupCompleteFlow(t *testing.T) {
	giteaSrv := fakeSetupGitea(t, "root", true)
	h, db, mgr, tokens := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	body := map[string]interface{}{
		"gitea": map[string]string{"url": giteaSrv.URL, "token": "gitea-admin-tok"},
		"llm": map[string]string{
			"provider": "deepseek",
			"model":    "deepseek-v4-flash",
			"base_url": "https://api.deepseek.com/v1",
			"api_key":  "sk-test",
			"type":     "openai_compatible",
		},
	}
	w := doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), body)
	require.Equal(t, 200, w.Code, w.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["ok"])
	assert.Equal(t, true, resp["webhook_secret_generated"])
	secret, _ := resp["webhook_secret"].(string)
	require.Len(t, secret, 64, "32-byte hex webhook secret")

	// Config landed in the active config (C-8).
	active := mgr.Get()
	assert.Equal(t, giteaSrv.URL, active.Gitea.URL)
	assert.Equal(t, "gitea-admin-tok", active.Gitea.AdminToken)
	assert.Equal(t, secret, active.Gitea.WebhookSecret)
	assert.Equal(t, "deepseek", active.LLM.Defaults.Provider)
	assert.Equal(t, "deepseek-v4-flash", active.LLM.Defaults.Model)
	require.Contains(t, active.LLM.Providers, "deepseek")
	assert.Equal(t, "sk-test", active.LLM.Providers["deepseek"].APIKey)

	// And persisted to the DB (survives restart).
	v, err := db.GetConfig("gitea.url")
	require.NoError(t, err)
	assert.Equal(t, giteaSrv.URL, v)

	// Setup no longer required → endpoints closed, token invalidated.
	assert.False(t, h.setupRequired())
	assert.False(t, tokens.Validate(tokens.Token()))
	w = doSetupReq(t, mux, "POST", "/api/setup/verify", "", nil)
	assert.Equal(t, 403, w.Code)

	// Audit row (C-16): secret masked.
	logs, err := db.ListOperationLogs(10, 0)
	require.NoError(t, err)
	found := false
	for _, l := range logs {
		if l.Action == "setup_complete" {
			found = true
			assert.Contains(t, l.Detail, giteaSrv.URL)
			assert.Contains(t, l.Detail, "root")
			assert.Contains(t, l.Detail, "deepseek")
			assert.NotContains(t, l.Detail, secret, "raw secret must never be logged")
			assert.NotContains(t, l.Detail, "gitea-admin-tok")
			assert.NotContains(t, l.Detail, "sk-test")
		}
	}
	assert.True(t, found, "setup_complete audit row missing: %v", logs)
}

func TestSetupCompleteExplicitWebhookSecret(t *testing.T) {
	giteaSrv := fakeSetupGitea(t, "root", true)
	h, _, mgr, tokens := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	body := map[string]interface{}{
		"gitea":          map[string]string{"url": giteaSrv.URL, "token": "tok"},
		"llm":            map[string]string{"provider": "ollama", "model": "qwen3", "base_url": "http://localhost:11434/v1"},
		"webhook_secret": "my-own-secret",
	}
	w := doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), body)
	require.Equal(t, 200, w.Code, w.Body.String())

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	_, generated := resp["webhook_secret_generated"]
	assert.False(t, generated, "explicit secret must not be flagged as generated")
	assert.Equal(t, "my-own-secret", mgr.Get().Gitea.WebhookSecret)
	// Local LLM without api_key is allowed.
	assert.Equal(t, "", mgr.Get().LLM.Providers["ollama"].APIKey)
}

func TestSetupCompleteGiteaRejected(t *testing.T) {
	// Gitea that 401s everything.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	t.Cleanup(srv.Close)

	h, _, mgr, tokens := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	body := map[string]interface{}{
		"gitea": map[string]string{"url": srv.URL, "token": "bad"},
		"llm":   map[string]string{"provider": "deepseek", "model": "m", "base_url": "https://api.deepseek.com/v1", "api_key": "sk"},
	}
	w := doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), body)
	assert.Equal(t, 400, w.Code)
	assert.Equal(t, "", mgr.Get().Gitea.URL, "nothing may be persisted when the Gitea test fails")
}

func TestSetupCompleteValidation(t *testing.T) {
	h, _, _, tokens := newSetupTestHandler(t, incompleteConfig())
	mux := setupMux(h)

	// Missing almost everything.
	w := doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), map[string]interface{}{})
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "gitea")

	// Remote LLM without api_key.
	giteaSrv := fakeSetupGitea(t, "root", true)
	body := map[string]interface{}{
		"gitea": map[string]string{"url": giteaSrv.URL, "token": "tok"},
		"llm":   map[string]string{"provider": "deepseek", "model": "m", "base_url": "https://api.deepseek.com/v1"},
	}
	w = doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), body)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "api_key")

	// Bad provider name.
	body["llm"] = map[string]string{"provider": "Deep Seek!", "model": "m", "base_url": "https://x", "api_key": "k"}
	w = doSetupReq(t, mux, "POST", "/api/setup/complete", tokens.Token(), body)
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "provider")
}

// --- C-17: masking ---

func TestMaskDisplayMap(t *testing.T) {
	display := map[string]interface{}{
		"gitea.url":            "http://gitea.local",
		"gitea.admin_token":    "super-secret-token",
		"gitea.webhook_secret": "whsec",
		"llm.providers": map[string]config.ProviderConfig{
			"deepseek": {BaseURL: "https://api.deepseek.com/v1", APIKey: "sk-real"},
			"ollama":   {BaseURL: "http://localhost:11434/v1"},
		},
	}
	masked := maskDisplayMap(display)

	assert.Equal(t, "http://gitea.local", masked["gitea.url"])
	assert.Equal(t, configMaskedPlaceholder, masked["gitea.admin_token"])
	assert.Equal(t, configMaskedPlaceholder, masked["gitea.webhook_secret"])

	providers, err := config.ParseProvidersFromInterface(masked["llm.providers"])
	require.NoError(t, err)
	assert.Equal(t, configMaskedPlaceholder, providers["deepseek"].APIKey)
	assert.Equal(t, "", providers["ollama"].APIKey, "empty keys stay empty")
	// Original map untouched.
	assert.Equal(t, "super-secret-token", display["gitea.admin_token"])
}

func TestResolveMaskedEntriesKeepsSecrets(t *testing.T) {
	db := openTestDB(t)
	cfg := completeConfig()
	mgr := config.NewConfigManager(cfg)
	mgr.SetStore(db)
	h := NewHandler(db, nil, cfg, nil, mgr, nil)

	entries := map[string]string{
		"gitea.url":         "http://new-gitea.local",
		"gitea.admin_token": configMaskedPlaceholder, // keep current
		"llm.providers":     `{"deepseek":{"base_url":"https://api.deepseek.com/v1","api_key":"` + configMaskedPlaceholder + `"}}`,
	}
	resolved := h.resolveMaskedEntries(entries)

	_, hasToken := resolved["gitea.admin_token"]
	assert.False(t, hasToken, "placeholder token entry must be dropped")
	assert.Equal(t, "http://new-gitea.local", resolved["gitea.url"])

	providers, err := config.ParseProvidersJSON(resolved["llm.providers"])
	require.NoError(t, err)
	assert.Equal(t, "sk-x", providers["deepseek"].APIKey, "placeholder api_key restored from active config")
}

func TestRestoreMaskedProviderKeysNewProvider(t *testing.T) {
	db := openTestDB(t)
	cfg := completeConfig()
	mgr := config.NewConfigManager(cfg)
	mgr.SetStore(db)
	h := NewHandler(db, nil, cfg, nil, mgr, nil)

	// Placeholder on a provider that doesn't exist yet → empty key (no crash).
	out := h.restoreMaskedProviderKeys(`{"newp":{"base_url":"http://x","api_key":"` + configMaskedPlaceholder + `"}}`)
	providers, err := config.ParseProvidersJSON(out)
	require.NoError(t, err)
	assert.Equal(t, "", providers["newp"].APIKey)
}

// --- C-16: audit masking helper ---

func TestMaskConfigValue(t *testing.T) {
	assert.Equal(t, "****", maskConfigValue("gitea.admin_token", "secret"))
	assert.Equal(t, "****", maskConfigValue("gitea.webhook_secret", "secret"))
	assert.Equal(t, "http://x", maskConfigValue("gitea.url", "http://x"))

	masked := maskConfigValue("llm.providers", `{"deepseek":{"base_url":"https://x","api_key":"sk-real"}}`)
	assert.Contains(t, masked, `"api_key":"****"`)
	assert.NotContains(t, masked, "sk-real")

	// Unparseable providers JSON is fully masked (no leak risk).
	assert.Equal(t, "****", maskConfigValue("llm.providers", "{not json"))
}

func TestMaskSecret(t *testing.T) {
	assert.Equal(t, "****", maskSecret("abc"))
	assert.Equal(t, "****cdef", maskSecret("abcdef"))
	assert.True(t, strings.HasPrefix(maskSecret("abcdef"), "****"))
}

// --- PUT /api/config: auto webhook secret (C-6) + masked round-trip (C-17) ---

func TestUpdateConfigAutoGeneratesWebhookSecret(t *testing.T) {
	db := openTestDB(t)
	cfg := completeConfig() // gitea set, WebhookSecret empty
	mgr := config.NewConfigManager(cfg)
	mgr.SetStore(db)
	h := NewHandler(db, nil, cfg, nil, mgr, nil)

	body, _ := json.Marshal(map[string]string{
		"gitea.url":         "http://gitea2.local",
		"gitea.admin_token": "brand-new-token",
	})
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.updateConfig(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	active := mgr.Get()
	assert.Equal(t, "brand-new-token", active.Gitea.AdminToken)
	require.Len(t, active.Gitea.WebhookSecret, 64, "webhook secret must be auto-generated when Gitea is configured without one")

	// Response is masked (C-17).
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, configMaskedPlaceholder, resp["gitea.admin_token"])
	assert.Equal(t, configMaskedPlaceholder, resp["gitea.webhook_secret"])

	// Audit row written with masked values (C-16).
	logs, err := db.ListOperationLogs(10, 0)
	require.NoError(t, err)
	require.NotEmpty(t, logs)
	assert.Equal(t, "config_update", logs[0].Action)
	assert.NotContains(t, logs[0].Detail, "brand-new-token")
	assert.NotContains(t, logs[0].Detail, active.Gitea.WebhookSecret)
}

func TestUpdateConfigMaskedRoundTripKeepsSecrets(t *testing.T) {
	db := openTestDB(t)
	cfg := completeConfig()
	cfg.Gitea.WebhookSecret = "existing-secret"
	mgr := config.NewConfigManager(cfg)
	mgr.SetStore(db)
	h := NewHandler(db, nil, cfg, nil, mgr, nil)

	// Submit the masked form: placeholders + a changed URL.
	body, _ := json.Marshal(map[string]string{
		"gitea.url":            "http://gitea3.local",
		"gitea.admin_token":    configMaskedPlaceholder,
		"gitea.webhook_secret": configMaskedPlaceholder,
		"llm.providers":        `{"deepseek":{"base_url":"https://api.deepseek.com/v1","api_key":"` + configMaskedPlaceholder + `"}}`,
	})
	req := httptest.NewRequest("PUT", "/api/config", bytes.NewReader(body))
	w := httptest.NewRecorder()
	h.updateConfig(w, req)
	require.Equal(t, 200, w.Code, w.Body.String())

	active := mgr.Get()
	assert.Equal(t, "http://gitea3.local", active.Gitea.URL, "non-masked field updated")
	assert.Equal(t, "tok", active.Gitea.AdminToken, "masked token kept")
	assert.Equal(t, "existing-secret", active.Gitea.WebhookSecret, "masked secret kept (no regeneration)")
	assert.Equal(t, "sk-x", active.LLM.Providers["deepseek"].APIKey, "masked provider key restored")
}
