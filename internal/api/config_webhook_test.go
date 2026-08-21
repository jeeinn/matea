package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
)

// newMockGitea starts an httptest server emulating the Gitea admin webhook
// API. existing are the hooks returned on GET; posts counts POST creations.
func newMockGitea(t *testing.T, existing []gitea.Webhook) (*httptest.Server, *int) {
	t.Helper()
	var posts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/hooks" {
			http.Error(w, "not found", 404)
			return
		}
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(existing)
		case http.MethodPost:
			posts++
			var req gitea.CreateWebhookRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(gitea.Webhook{ID: 99, Type: "gitea", Config: req.Config, Events: req.Events, Active: true})
		default:
			http.Error(w, "method not allowed", 405)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &posts
}

func doWebhook(t *testing.T, h *Handler, action string) map[string]interface{} {
	t.Helper()
	body := strings.NewReader(`{"action":"` + action + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config/gitea-webhook", body)
	rec := httptest.NewRecorder()
	h.giteaWebhookHandler(rec, req)
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
	}
	return out
}

// TestGiteaWebhookClosed verifies an empty server.public_url disables the
// inbound webhook and returns closed=true without calling Gitea.
func TestGiteaWebhookClosed(t *testing.T) {
	cm := config.NewConfigManager(&config.Config{})
	h := &Handler{cfgManager: cm}
	out := doWebhook(t, h, "check")
	if out["closed"] != true {
		t.Fatalf("expected closed=true, got %v", out)
	}
	if _, ok := out["callback_url"]; ok {
		t.Fatalf("closed response should not include callback_url, got %v", out)
	}
}

// TestGiteaWebhookRegister verifies the register action creates a new Gitea
// system webhook when none matches.
func TestGiteaWebhookRegister(t *testing.T) {
	srv, posts := newMockGitea(t, nil)
	cfg := &config.Config{
		Server: config.ServerConfig{PublicURL: "http://matea.example.com"},
		Gitea:  config.GiteaConfig{URL: srv.URL, AdminToken: "t", WebhookSecret: "s"},
	}
	cm := config.NewConfigManager(cfg)
	h := &Handler{cfgManager: cm}
	out := doWebhook(t, h, "register")
	if out["success"] != true {
		t.Fatalf("expected success, got %v", out)
	}
	if out["created"] != true {
		t.Fatalf("expected created=true, got %v", out)
	}
	if out["hook_id"] != float64(99) {
		t.Fatalf("expected hook_id=99, got %v", out["hook_id"])
	}
	if *posts != 1 {
		t.Fatalf("expected exactly 1 POST to Gitea, got %d", *posts)
	}
}

// TestGiteaWebhookCheckExisting verifies check reports an already-registered
// webhook by matching the callback URL.
func TestGiteaWebhookCheckExisting(t *testing.T) {
	srv, _ := newMockGitea(t, []gitea.Webhook{
		{ID: 11, Type: "gitea", Config: gitea.WebhookConfig{URL: "http://matea.example.com/webhook/gitea"}, Events: []string{"issues"}, Active: true},
	})
	cfg := &config.Config{
		Server: config.ServerConfig{PublicURL: "http://matea.example.com"},
		Gitea:  config.GiteaConfig{URL: srv.URL, AdminToken: "t", WebhookSecret: "s"},
	}
	cm := config.NewConfigManager(cfg)
	h := &Handler{cfgManager: cm}
	out := doWebhook(t, h, "check")
	if out["registered"] != true {
		t.Fatalf("expected registered=true, got %v", out)
	}
	if out["hook_id"] != float64(11) {
		t.Fatalf("expected hook_id=11, got %v", out["hook_id"])
	}
}

// TestGiteaWebhookMissingGitea verifies a configured public_url but missing
// Gitea credentials returns a 400 (no Gitea call made).
func TestGiteaWebhookMissingGitea(t *testing.T) {
	srv, posts := newMockGitea(t, nil)
	cfg := &config.Config{
		Server: config.ServerConfig{PublicURL: "http://matea.example.com"},
		// Gitea.URL intentionally empty
		Gitea: config.GiteaConfig{AdminToken: "t", WebhookSecret: "s"},
	}
	_ = srv
	cm := config.NewConfigManager(cfg)
	h := &Handler{cfgManager: cm}

	body := strings.NewReader(`{"action":"check"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config/gitea-webhook", body)
	rec := httptest.NewRecorder()
	h.giteaWebhookHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if *posts != 0 {
		t.Fatalf("expected no Gitea calls, got %d", *posts)
	}
}
