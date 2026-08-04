package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test helpers ----------------------------------------------------------

// newTestOpenCodeServer creates an httptest server that handles OpenCode API
// endpoints and returns canned responses. The handler map lets individual
// tests override specific endpoints.
func newTestOpenCodeServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	// Default handlers — tests override via the handlers map (no double-registration)
	defaultHandlers := map[string]http.HandlerFunc{
		"/health":   defaultHealthHandler,
		"/session":  defaultSessionCreateHandler,
		"/session/": defaultSessionSubHandler(),
	}

	// Merge: test handlers override defaults
	for path, h := range defaultHandlers {
		if _, ok := handlers[path]; !ok {
			mux.HandleFunc(path, h)
		}
	}
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func defaultHealthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "version": "test"})
}

func defaultSessionCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{"id": "sess-test-123"})
}

// defaultSessionSubHandler handles everything under /session/{id}/...
func defaultSessionSubHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		switch {
		case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"id":      "msg-1",
				"role":    "assistant",
				"content": "Here is the fix.",
			})

		case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]any{
				map[string]any{
					"info":  map[string]any{"id": "msg-1", "role": "user"},
					"parts": []any{},
				},
				map[string]any{
					"info": map[string]any{"id": "msg-2", "role": "assistant"},
					"parts": []any{
						map[string]any{"type": "text", "text": "Done."},
					},
				},
			})

		case strings.HasSuffix(path, "/abort") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})

		default:
			http.NotFound(w, r)
		}
	}
}

func newTestBackend(t *testing.T, baseURL string) *OpenCodeHTTPBackend {
	t.Helper()
	cfg := config.BackendConfig{
		Type:    config.BackendTypeOpenCodeHTTP,
		BaseURL: baseURL,
		Timeout: "10s",
		Auth: config.BackendAuthConfig{
			Username: "testuser",
			Password: "testpass",
		},
	}
	b, err := NewOpenCodeHTTPBackend("test-opencode", cfg)
	require.NoError(t, err)
	return b
}

// --- HealthCheck -----------------------------------------------------------

func TestOpenCodeHTTPHealthCheckOK(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	err := backend.HealthCheck(context.Background())
	require.NoError(t, err)
}

func TestOpenCodeHTTPHealthCheckNotFound(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		},
	})
	backend := newTestBackend(t, srv.URL)

	err := backend.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "health check")
	assert.Contains(t, err.Error(), "404")
}

func TestOpenCodeHTTPHealthCheckConnectionRefused(t *testing.T) {
	cfg := config.BackendConfig{
		Type:    config.BackendTypeOpenCodeHTTP,
		BaseURL: "http://127.0.0.1:1", // nothing listening
		Timeout: "100ms",
	}
	b, err := NewOpenCodeHTTPBackend("test", cfg)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err = b.HealthCheck(ctx)
	require.Error(t, err)
}

func TestOpenCodeHTTPHealthCheckContextTimeout(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		},
	})
	backend := newTestBackend(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err := backend.HealthCheck(ctx)
	require.Error(t, err)
}

// --- createSession ---------------------------------------------------------

func TestOpenCodeHTTPCreateSession(t *testing.T) {
	var receivedBody map[string]any
	var receivedQuery string
	var receivedDirHeader string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			receivedQuery = r.URL.Query().Get("directory")
			receivedDirHeader = r.Header.Get("X-Opencode-Directory")
			json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{"id": "sess-abc"})
		},
	})
	backend := newTestBackend(t, srv.URL)

	sessionID, err := backend.createSession(context.Background(), CodingRequest{
		WorkDir: "/tmp/test-repo",
		Task:    &store.Task{ID: 42},
	})
	require.NoError(t, err)
	assert.Equal(t, "sess-abc", sessionID)
	assert.Equal(t, "matea-task-42", receivedBody["title"])
	assert.Equal(t, "/tmp/test-repo", receivedQuery)
	assert.Equal(t, "/tmp/test-repo", receivedDirHeader)
}

// --- sendMessage -----------------------------------------------------------

func TestOpenCodeHTTPSendMessage(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	ctx := context.Background()
	sessionID, err := backend.createSession(ctx, CodingRequest{WorkDir: "/tmp/test", Task: &store.Task{ID: 1}})
	require.NoError(t, err)

	summary, err := backend.sendMessage(ctx, sessionID, CodingRequest{
		SystemPrompt: "You are helpful.",
		Prompt:       "Fix the bug.",
		Agent:        &store.Agent{Provider: "mock", Model: "gpt-test"},
		Task:         &store.Task{ID: 1},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, summary)
}

// --- Abort -----------------------------------------------------------------

func TestOpenCodeHTTPAbort(t *testing.T) {
	var abortedSession string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/test-sess/abort": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			abortedSession = "test-sess"
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	backend := newTestBackend(t, srv.URL)

	err := backend.Abort(context.Background(), "test-sess")
	require.NoError(t, err)
	assert.Equal(t, "test-sess", abortedSession)
}

// --- Basic auth ------------------------------------------------------------

func TestOpenCodeHTTPBasicAuthSent(t *testing.T) {
	var authHeader string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			authHeader = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		},
	})
	backend := newTestBackend(t, srv.URL)

	err := backend.HealthCheck(context.Background())
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(authHeader, "Basic "), "expected Basic auth header, got %q", authHeader)
}

// --- Run end-to-end through mock ------------------------------------------

func TestOpenCodeHTTPRunEndToEnd(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	result, err := backend.Run(context.Background(), CodingRequest{
		WorkDir:      "/tmp/test-repo",
		Prompt:       "Fix issue #1",
		SystemPrompt: "You are a coder.",
		Agent:        &store.Agent{Provider: "mock", Model: "test-model"},
		Task:         &store.Task{ID: 10},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, result.Summary)
	assert.NotEmpty(t, result.RemoteSessionID)
	assert.True(t, result.Success)
}

// --- NewOpenCodeHTTPBackend validation ------------------------------------

func TestNewOpenCodeHTTPBackendRequiresBaseURL(t *testing.T) {
	_, err := NewOpenCodeHTTPBackend("test", config.BackendConfig{
		Type: config.BackendTypeOpenCodeHTTP,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestNewOpenCodeHTTPBackendRejectsUnsupportedWorkspaceMode(t *testing.T) {
	_, err := NewOpenCodeHTTPBackend("test", config.BackendConfig{
		Type:          config.BackendTypeOpenCodeHTTP,
		BaseURL:       "http://localhost:8080",
		WorkspaceMode: "volume",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_mode")
}

// --- ResolveCodingBackend tests -------------------------------------------

func TestResolveCodingBackendInternal(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: ""} // default

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "internal", backend.Name())
}

func TestResolveCodingBackendExplicitInternal(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "internal"}

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "internal", backend.Name())
}

func TestResolveCodingBackendOpenCodeHTTP(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Default: "opencode-local",
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:    config.BackendTypeOpenCodeHTTP,
				BaseURL: srv.URL,
				Timeout: "10s",
			},
		},
	}
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "opencode-local"}

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "opencode-local", backend.Name())

	hc, ok := backend.(HealthCheckableBackend)
	require.True(t, ok, "opencode backend should implement HealthCheckableBackend")
	require.NoError(t, hc.HealthCheck(context.Background()))
}

func TestResolveCodingBackendNotFound(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "nonexistent"}

	_, err := factory.ResolveCodingBackend(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestResolveCodingBackendUsesDefault(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Default: "opencode-local",
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:    config.BackendTypeOpenCodeHTTP,
				BaseURL: srv.URL,
			},
		},
	}
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: ""} // should use default

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "opencode-local", backend.Name())
}

// TestResolveCodingBackendNormalizesLegacyIdentifiers verifies pre-1.2.6
// identifiers still route correctly (task 1.2.6a acceptance: rows with
// backend='internal' and configs with type='opencode_http' keep working).
func TestResolveCodingBackendNormalizesLegacyIdentifiers(t *testing.T) {
	// Legacy agent.Backend="internal" resolves to the builtin backend.
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	backend, err := factory.ResolveCodingBackend(&store.Agent{Backend: "internal"})
	require.NoError(t, err)
	assert.Equal(t, "internal", backend.Name(), "legacy internal should resolve to the builtin backend (Name() unchanged until 1.2.6b)")

	// Canonical agent.Backend="builtin" resolves identically.
	backend2, err := factory.ResolveCodingBackend(&store.Agent{Backend: "builtin"})
	require.NoError(t, err)
	assert.Equal(t, backend.Name(), backend2.Name())

	// Legacy type "opencode_http" still selects the OpenCode backend.
	srv := newTestOpenCodeServer(t, nil)
	legacyBackends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"my-opencode": {Type: "opencode_http", BaseURL: srv.URL},
		},
	}
	factory2 := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, legacyBackends, nil, sandbox.DefaultConfig(), nil, "")
	ocBackend, err := factory2.ResolveCodingBackend(&store.Agent{Backend: "my-opencode"})
	require.NoError(t, err)
	assert.Equal(t, "my-opencode", ocBackend.Name())

	// Canonical type "hub-opencode" works the same way.
	canonicalBackends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"my-opencode": {Type: config.BackendNameHubOpenCode, BaseURL: srv.URL},
		},
	}
	factory3 := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, canonicalBackends, nil, sandbox.DefaultConfig(), nil, "")
	ocBackend2, err := factory3.ResolveCodingBackend(&store.Agent{Backend: "my-opencode"})
	require.NoError(t, err)
	assert.Equal(t, "my-opencode", ocBackend2.Name())
}

// --- Health check (runWriteTask: fail before prepare unless allow_fallback_internal) ---

func TestOpenCodeBackendUnhealthyReturnsFriendlyError(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"status":"unhealthy"}`, http.StatusServiceUnavailable)
		},
	})
	cfg := config.BackendConfig{
		Type:    config.BackendTypeOpenCodeHTTP,
		BaseURL: srv.URL,
		Timeout: "10s",
	}
	b, err := NewOpenCodeHTTPBackend("sick-backend", cfg)
	require.NoError(t, err)

	hc, ok := interface{}(b).(HealthCheckableBackend)
	require.True(t, ok)
	err = hc.HealthCheck(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "503")
	assert.Contains(t, err.Error(), "health check")
	// Default: no silent fallback — Executor must mark failed, not success.
	assert.False(t, allowsInternalFallback(b))
}

func TestAllowsInternalFallbackFlag(t *testing.T) {
	b, err := NewOpenCodeHTTPBackend("opencode-local", config.BackendConfig{
		Type:                  config.BackendTypeOpenCodeHTTP,
		BaseURL:               "http://127.0.0.1:9",
		AllowFallbackInternal: true,
	})
	require.NoError(t, err)
	assert.True(t, allowsInternalFallback(b))
}
