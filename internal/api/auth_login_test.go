package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/auth"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestEnsureDefaultAdminMustChangePassword(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))

	user, err := db.GetUserByUsername("admin")
	require.NoError(t, err)
	assert.True(t, user.MustChangePassword)
	assert.True(t, auth.CheckPassword("admin123", user.PasswordHash))

	// Idempotent
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))
	users, err := db.ListUsers()
	require.NoError(t, err)
	assert.Len(t, users, 1)
}

func TestLoginForcesPasswordChange(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))
	jwt := auth.NewJWTManager("test-secret", time.Hour)
	h := NewAuthHandler(db, jwt, "admin123")

	mux := http.NewServeMux()
	h.RegisterAuthRoutes(mux)

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["must_change_password"])
	user := resp["user"].(map[string]interface{})
	assert.Equal(t, true, user["must_change_password"])
	token := resp["token"].(string)
	require.NotEmpty(t, token)

	// Change password
	changeBody, _ := json.Marshal(map[string]string{
		"old_password": "admin123",
		"new_password": "new-secure-password",
	})
	req = httptest.NewRequest(http.MethodPut, "/api/auth/password", bytes.NewReader(changeBody))
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var changeResp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&changeResp))
	newToken, _ := changeResp["token"].(string)
	require.NotEmpty(t, newToken)
	claims, err := jwt.ValidateToken(newToken)
	require.NoError(t, err)
	assert.False(t, claims.MustChangePassword)

	u, err := db.GetUserByUsername("admin")
	require.NoError(t, err)
	assert.False(t, u.MustChangePassword)
	assert.True(t, auth.CheckPassword("new-secure-password", u.PasswordHash))
}

func TestLoginDefaultPasswordForceOnlyForAdminUsername(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))
	hash, err := auth.HashPassword("admin123")
	require.NoError(t, err)
	other := &store.User{
		Username:           "alice",
		PasswordHash:       hash,
		Role:               "viewer",
		DisplayName:        "Alice",
		IsActive:           true,
		MustChangePassword: false,
	}
	require.NoError(t, db.CreateUser(other))

	jwt := auth.NewJWTManager("test-secret", time.Hour)
	h := NewAuthHandler(db, jwt, "admin123")
	mux := http.NewServeMux()
	h.RegisterAuthRoutes(mux)

	body, _ := json.Marshal(map[string]string{"username": "alice", "password": "admin123"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["must_change_password"])
	token := resp["token"].(string)
	claims, err := jwt.ValidateToken(token)
	require.NoError(t, err)
	assert.False(t, claims.MustChangePassword)
}

func TestGetSetupStatus(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))
	admin, err := db.GetUserByUsername("admin")
	require.NoError(t, err)
	require.NoError(t, db.SetMustChangePassword(admin.ID, false))

	path := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, config.WriteBootstrapConfig(path))
	loaded, err := config.Load(path)
	require.NoError(t, err)
	cfgManager := config.NewConfigManager(loaded)

	jwt := auth.NewJWTManager(loaded.Auth.JWTSecret, time.Hour)
	h := NewHandler(db, nil, loaded, jwt, cfgManager, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	token, err := jwt.GenerateToken(admin.ID, admin.Username, admin.Role, false)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/setup/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)

	var st config.SetupStatus
	require.NoError(t, json.NewDecoder(w.Body).Decode(&st))
	assert.True(t, st.SetupRequired)
	assert.False(t, st.GiteaOK)
	assert.False(t, st.LLMOK)
}

func TestJwtWrapBlocksWhenMustChangePassword(t *testing.T) {
	db := openTestDB(t)
	require.NoError(t, EnsureDefaultAdmin(db, "admin123"))
	admin, err := db.GetUserByUsername("admin")
	require.NoError(t, err)
	assert.True(t, admin.MustChangePassword)

	path := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, config.WriteBootstrapConfig(path))
	loaded, err := config.Load(path)
	require.NoError(t, err)

	jwt := auth.NewJWTManager("secret", time.Hour)
	h := NewHandler(db, nil, loaded, jwt, nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	token, err := jwt.GenerateToken(admin.ID, "admin", "admin", true)
	require.NoError(t, err)

	// /api/setup/status is public since Phase 2.5 (C-3) — use a JWT-protected
	// endpoint to pin the must_change_password block.
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	assert.Equal(t, 403, w.Code)

	var resp map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "must_change_password", resp["code"])
}
