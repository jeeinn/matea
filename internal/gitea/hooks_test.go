package gitea

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, NewClient(srv.URL, "test-token")
}

func TestListAdminWebhooks(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/admin/hooks" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token test-token" {
			t.Errorf("missing auth header")
		}
		_ = json.NewEncoder(w).Encode([]Webhook{
			{ID: 1, Type: "gitea", Config: WebhookConfig{URL: "https://matea.example.com/webhook/gitea", ContentType: "json"}, Events: defaultWebhookEvents, Active: true},
			{ID: 2, Type: "gitea", Config: WebhookConfig{URL: "https://other.example.com/hook", ContentType: "json"}, Events: defaultWebhookEvents, Active: true},
		})
	})

	hooks, err := client.ListAdminWebhooks()
	if err != nil {
		t.Fatalf("ListAdminWebhooks: %v", err)
	}
	if len(hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(hooks))
	}
	if hooks[0].Config.URL != "https://matea.example.com/webhook/gitea" {
		t.Errorf("unexpected first hook url: %s", hooks[0].Config.URL)
	}
}

func TestEnsureWebhookExisting(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/hooks":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Webhook{
					{ID: 42, Type: "gitea", Config: WebhookConfig{URL: "https://matea.example.com/webhook/gitea", ContentType: "json"}, Events: defaultWebhookEvents, Active: true},
				})
				return
			}
			t.Errorf("unexpected %s on /admin/hooks", r.Method)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	registered, created, hookID, err := client.EnsureWebhook("https://matea.example.com/webhook/gitea", "secret", nil)
	if err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	if !registered || created || hookID != 42 {
		t.Errorf("expected registered=true created=false hookID=42, got registered=%v created=%v hookID=%d", registered, created, hookID)
	}
}

func TestEnsureWebhookCreate(t *testing.T) {
	var createdBody CreateWebhookRequest
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/admin/hooks":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode([]Webhook{})
				return
			}
			if r.Method == http.MethodPost {
				_ = json.NewDecoder(r.Body).Decode(&createdBody)
				_ = json.NewEncoder(w).Encode(Webhook{ID: 7, Type: "gitea", Config: createdBody.Config, Events: createdBody.Events, Active: true})
				return
			}
			t.Errorf("unexpected method %s", r.Method)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})

	registered, created, hookID, err := client.EnsureWebhook("https://matea.example.com/webhook/gitea", "mysecret", nil)
	if err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	if !registered || !created || hookID != 7 {
		t.Errorf("expected registered=true created=true hookID=7, got registered=%v created=%v hookID=%d", registered, created, hookID)
	}
	if createdBody.Config.URL != "https://matea.example.com/webhook/gitea" {
		t.Errorf("unexpected created url: %s", createdBody.Config.URL)
	}
	if createdBody.Config.Secret != "mysecret" {
		t.Errorf("unexpected secret: %s", createdBody.Config.Secret)
	}
	if createdBody.Config.ContentType != "json" {
		t.Errorf("unexpected content_type: %s", createdBody.Config.ContentType)
	}
	if len(createdBody.Events) != len(defaultWebhookEvents) {
		t.Errorf("expected default events, got %d", len(createdBody.Events))
	}
}

func TestEnsureWebhookEmptyCallback(t *testing.T) {
	_, client := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {})
	_, _, _, err := client.EnsureWebhook("  ", "secret", nil)
	if err == nil {
		t.Fatal("expected error for empty callback URL")
	}
}
