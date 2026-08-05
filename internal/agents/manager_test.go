package agents

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

func newTestManager(t *testing.T, handler http.HandlerFunc) *Manager {
	t.Helper()
	tmpDB, err := os.CreateTemp("", "agent-mgr-test-*.db")
	require.NoError(t, err)
	tmpDB.Close()

	db, err := store.Open(tmpDB.Name())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpDB.Name())
	})

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	cfg := &config.GiteaConfig{URL: server.URL, AdminToken: "admin-token", AutoProvision: true}
	return NewManager(db, cfg)
}

func TestEnsureGiteaAccountCreatesMissingUser(t *testing.T) {
	var created bool
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/agent-bot":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/users":
			created = true
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 1, Login: "agent-bot"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tokens"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.TokenResponse{SHA1: "new-token-sha1"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	token, userCreated, err := mgr.EnsureGiteaAccount("agent-bot", "")
	require.NoError(t, err)
	assert.True(t, created)
	assert.True(t, userCreated)
	assert.Equal(t, "new-token-sha1", token)
}

func TestEnsureGiteaAccountKeepsValidToken(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/agent-bot":
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 1, Login: "agent-bot"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			if r.Header.Get("Authorization") == "token valid-token" {
				json.NewEncoder(w).Encode(gitea.CurrentUser{ID: 1, Login: "agent-bot"})
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	token, userCreated, err := mgr.EnsureGiteaAccount("agent-bot", "valid-token")
	require.NoError(t, err)
	assert.False(t, userCreated)
	assert.Equal(t, "valid-token", token)
}

func TestEnsureGiteaAccountRefreshesInvalidToken(t *testing.T) {
	var passwordReset bool
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/agent-bot":
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 1, Login: "agent-bot"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/users/agent-bot":
			passwordReset = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 1, Login: "agent-bot"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tokens"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.TokenResponse{SHA1: "refreshed-token"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	// Pre-create a managed agent so the account can be refreshed
	agent := &store.Agent{
		Name:           "bot-agent",
		GiteaUsername:  "agent-bot",
		GiteaToken:     "stale-localhost-token",
		Role:           store.RoleAnalyze,
		Status:         "active",
		Provider:       "deepseek",
		Model:          "deepseek-v4-flash",
		ManagedByMatea: true, // Mark as managed
	}
	require.NoError(t, mgr.db.CreateAgent(agent))

	token, userCreated, err := mgr.EnsureGiteaAccount("agent-bot", "stale-localhost-token")
	require.NoError(t, err)
	assert.True(t, passwordReset)
	assert.False(t, userCreated)
	assert.Equal(t, "refreshed-token", token)
}

func TestUpdateAgentProvisionsGiteaUser(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/issue-analyze":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/users":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 2, Login: "issue-analyze"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tokens"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.TokenResponse{SHA1: "remote-token"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	agent := &store.Agent{
		Name:            "issue分析",
		GiteaUsername:   "issue-analyze",
		GiteaToken:      "old-localhost-token",
		Provider:        "deepseek",
		Model:           "deepseek-v4-flash",
		MaxOutputTokens: 2048,
		MaxInputTokens:  8192,
		Role:            store.RoleAnalyze,
		Status:          "active",
	}
	require.NoError(t, mgr.db.CreateAgent(agent))

	require.NoError(t, mgr.UpdateAgent(agent))

	got, err := mgr.db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "remote-token", got.GiteaToken)
}

// TestEnsureGiteaAccount_PreventAccountTakeover tests that existing non-Matea accounts cannot be taken over
func TestEnsureGiteaAccount_PreventAccountTakeover(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/real-person":
			// Return existing user
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 999, Login: "real-person"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			// Token validation fails
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	// Attempt to create account for existing non-managed user
	_, _, err := mgr.EnsureGiteaAccount("real-person", "")

	// Should fail with clear error message
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists and is not managed by Matea")
	assert.Contains(t, err.Error(), "choose a different username")
}

// TestEnsureGiteaAccount_AllowManagedAccountRefresh tests that Matea-managed accounts can be refreshed
func TestEnsureGiteaAccount_AllowManagedAccountRefresh(t *testing.T) {
	var passwordReset bool
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/matea-test":
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 2, Login: "matea-test"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/user":
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/v1/admin/users/matea-test":
			passwordReset = true
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 2, Login: "matea-test"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tokens"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.TokenResponse{SHA1: "new-token"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	// Create a Matea-managed agent first
	agent := &store.Agent{
		Name:           "test-agent",
		GiteaUsername:  "matea-test",
		GiteaToken:     "old-token",
		Role:           store.RoleAnalyze,
		Status:         "active",
		Provider:       "deepseek",
		Model:          "deepseek-v4-flash",
		ManagedByMatea: true, // Key: marked as managed
	}
	err := mgr.db.CreateAgent(agent)
	require.NoError(t, err)

	// Attempt to refresh the account (token invalid)
	newToken, userCreated, err := mgr.EnsureGiteaAccount("matea-test", "invalid-token")

	// Should succeed because it's managed by Matea
	require.NoError(t, err)
	assert.False(t, userCreated)
	assert.NotEmpty(t, newToken)
	assert.True(t, passwordReset, "Password should be reset for managed account")
}

// TestCreateAgent_MarksNewAccountsAsManaged tests that new agents are marked as managed
func TestCreateAgent_MarksNewAccountsAsManaged(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/users/matea-analyst":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/admin/users":
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.UserResponse{ID: 3, Login: "matea-analyst"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tokens"):
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(gitea.TokenResponse{SHA1: "agent-token"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	// Create a new agent
	req := CreateAgentRequest{
		Name:          "new-agent",
		GiteaUsername: "matea-analyst",
		Role:          store.RoleAnalyze,
		Provider:      "deepseek",
		Model:         "deepseek-v4-flash",
	}

	agent, err := mgr.CreateAgent(req)
	require.NoError(t, err)

	// Verify the agent is marked as managed
	assert.True(t, agent.ManagedByMatea, "New agent should be marked as managed by Matea")

	// Verify it was saved to DB correctly
	savedAgent, err := mgr.db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.True(t, savedAgent.ManagedByMatea, "Saved agent should retain managed flag")
}

// TestCreateAgent_SkipsProvisionWhenDisabled verifies that with
// gitea.auto_provision=false, Matea never calls the Gitea Admin API and the
// agent is created with no token and not marked as managed by Matea.
func TestCreateAgent_SkipsProvisionWhenDisabled(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Gitea API must not be called when auto_provision is disabled: %s %s", r.Method, r.URL.Path)
	})
	mgr.cfg.AutoProvision = false

	req := CreateAgentRequest{
		Name:          "no-prov-agent",
		GiteaUsername: "no-prov",
		Role:          store.RoleAnalyze,
		Provider:      "deepseek",
		Model:         "deepseek-v4-flash",
	}

	agent, err := mgr.CreateAgent(req)
	require.NoError(t, err)
	assert.False(t, agent.ManagedByMatea, "agent must not be marked managed when provisioning disabled")
	assert.Empty(t, agent.GiteaToken, "no token should be set when provisioning disabled")

	saved, err := mgr.db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Empty(t, saved.GiteaToken)
}

// TestUpdateAgent_SkipsProvisionWhenDisabled verifies that an existing agent's
// token is preserved (not refreshed) when auto_provision is disabled.
func TestUpdateAgent_SkipsProvisionWhenDisabled(t *testing.T) {
	mgr := newTestManager(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("Gitea API must not be called when auto_provision is disabled: %s %s", r.Method, r.URL.Path)
	})
	mgr.cfg.AutoProvision = false

	agent := &store.Agent{
		Name:          "keep-token-agent",
		GiteaUsername: "keep-token",
		GiteaToken:    "manual-token",
		Role:          store.RoleAnalyze,
		Status:        "active",
		Provider:      "deepseek",
		Model:         "deepseek-v4-flash",
	}
	require.NoError(t, mgr.db.CreateAgent(agent))

	require.NoError(t, mgr.UpdateAgent(agent))

	got, err := mgr.db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "manual-token", got.GiteaToken, "existing token must be preserved when provisioning disabled")
}
