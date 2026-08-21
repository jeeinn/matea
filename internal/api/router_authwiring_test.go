package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/auth"
	"github.com/jeeinn/matea/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Phase 2.5 P2 routes must sit behind their auth middleware: a mis-wired
// registration (bare handler instead of jwtWrap/requireSetupToken) would make
// the endpoint public, and no handler-level unit test would notice because
// those tests invoke handlers directly. These tests exercise the real mux.

func wiringTestHandler() *Handler {
	cfg := &config.Config{}
	return &Handler{
		cfg:        cfg,
		cfgManager: config.NewConfigManager(cfg),
		jwtManager: auth.NewJWTManager("wiring-test-secret", time.Hour),
	}
}

func TestP2RoutesRequireJWT(t *testing.T) {
	h := wiringTestHandler()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	jwtRoutes := []struct{ method, path string }{
		{"GET", "/api/health/summary"},
		{"GET", "/api/config/export"},
		{"POST", "/api/config/import"},
	}
	for _, rt := range jwtRoutes {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code,
			"%s %s without token must be 401 (route must be jwtWrap'ed)", rt.method, rt.path)
	}

	// A valid token passes the middleware (handler logic itself is covered
	// elsewhere; here we only prove the wiring).
	token, err := h.jwtManager.GenerateToken(1, "admin", "admin", false)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/health/summary", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, "valid JWT must reach the health handler")
}

func TestP2SetupRoutesRequireSetupToken(t *testing.T) {
	h := wiringTestHandler() // empty config → setupRequired() is true
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	setupRoutes := []struct{ method, path, body string }{
		{"GET", "/api/setup/env-detection", ""},
		{"POST", "/api/setup/apply-env", `{"keys":[]}`},
	}
	for _, rt := range setupRoutes {
		var rdr *strings.Reader
		if rt.body != "" {
			rdr = strings.NewReader(rt.body)
		} else {
			rdr = strings.NewReader("")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(rt.method, rt.path, rdr))
		// setupTokens is nil here, so the gate must reject with 403; a public
		// (mis-wired) route would instead run the handler and return 200.
		assert.Equal(t, http.StatusForbidden, rec.Code,
			"%s %s without setup token must be rejected (route must be requireSetupToken'ed)", rt.method, rt.path)
	}
}
