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
		Type:    config.BackendTypeHubOpenCode,
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
		Type:    config.BackendTypeHubOpenCode,
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

	summary, messages, err := backend.sendMessage(ctx, sessionID, CodingRequest{
		SystemPrompt: "You are helpful.",
		Prompt:       "Fix the bug.",
		Agent:        &store.Agent{Provider: "mock", Model: "gpt-test"},
		Task:         &store.Task{ID: 1},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, summary)
	assert.NotNil(t, messages)
}

// captureMessageBodyServer records the POST /session/{id}/message body and
// replies with a normal assistant message.
func captureMessageBodyServer(t *testing.T, captured *map[string]any) *httptest.Server {
	t.Helper()
	return newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
				json.NewDecoder(r.Body).Decode(captured)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-2", "role": "assistant"})
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info":  map[string]any{"id": "msg-2", "role": "assistant"},
						"parts": []any{map[string]any{"type": "text", "text": "Done."}},
					},
				})
			default:
				http.NotFound(w, r)
			}
		},
	})
}

func TestOpenCodeHTTPModelOmittedWithoutOverride(t *testing.T) {
	var body map[string]any
	srv := captureMessageBodyServer(t, &body)
	backend := newTestBackend(t, srv.URL)

	// Agent Provider/Model must NOT leak into the opencode request: they are
	// matea's builtin-LLM namespace and can map to paid/unknown models server-side.
	_, _, err := backend.sendMessage(context.Background(), "sess-1", CodingRequest{
		Prompt: "Fix the bug.",
		Agent:  &store.Agent{Provider: "opencode", Model: "gemini-3-flash"},
		Task:   &store.Task{ID: 2},
	})
	require.NoError(t, err)
	_, hasModel := body["modelID"]
	_, hasProvider := body["providerID"]
	assert.False(t, hasModel, "modelID must be omitted without explicit override, body: %v", body)
	assert.False(t, hasProvider, "providerID must be omitted without explicit override, body: %v", body)
}

func TestOpenCodeHTTPModelSentWithBothOverrides(t *testing.T) {
	var body map[string]any
	srv := captureMessageBodyServer(t, &body)
	backend := newTestBackend(t, srv.URL)

	_, _, err := backend.sendMessage(context.Background(), "sess-1", CodingRequest{
		Prompt: "Fix the bug.",
		Agent:  &store.Agent{Provider: "ignored", Model: "ignored"},
		Task:   &store.Task{ID: 3},
		BackendOptions: map[string]interface{}{
			"opencode_model":    "big-pickle",
			"opencode_provider": "opencode",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "big-pickle", body["modelID"])
	assert.Equal(t, "opencode", body["providerID"])
}

func TestOpenCodeHTTPSingleOverrideIgnored(t *testing.T) {
	var body map[string]any
	srv := captureMessageBodyServer(t, &body)
	backend := newTestBackend(t, srv.URL)

	// Only opencode_model set (no provider): the pair is incomplete, so the
	// override is ignored and the server default applies.
	_, _, err := backend.sendMessage(context.Background(), "sess-1", CodingRequest{
		Prompt:         "Fix the bug.",
		Agent:          &store.Agent{Provider: "mock", Model: "gpt-test"},
		Task:           &store.Task{ID: 4},
		BackendOptions: map[string]interface{}{"opencode_model": "big-pickle"},
	})
	require.NoError(t, err)
	_, hasModel := body["modelID"]
	_, hasProvider := body["providerID"]
	assert.False(t, hasModel, "incomplete override must be ignored, body: %v", body)
	assert.False(t, hasProvider, "incomplete override must be ignored, body: %v", body)
}

// errorSessionHandler serves a session whose only assistant message carries a
// provider-side run failure (info.error) and no text parts — the shape a 401
// CreditsError / unknown-model run produces on a real opencode server.
func errorSessionHandler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{"id": "msg-2", "role": "assistant"})
	case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]any{
			map[string]any{
				"info":  map[string]any{"id": "msg-1", "role": "user"},
				"parts": []any{map[string]any{"type": "text", "text": "implement the fix"}},
			},
			map[string]any{
				"info": map[string]any{
					"id":   "msg-2",
					"role": "assistant",
					"error": map[string]any{
						"name": "APIError",
						"data": map[string]any{
							"message":     "Unauthorized: CreditsError: No payment method",
							"statusCode":  401,
							"isRetryable": false,
						},
					},
				},
				"parts": []any{},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

func TestOpenCodeHTTPSurfacesRunError(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{"/session/": errorSessionHandler})
	backend := newTestBackend(t, srv.URL)

	_, _, err := backend.getLastAssistantMessage(context.Background(), "sess-err")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "APIError")
	assert.Contains(t, err.Error(), "401")
	assert.Contains(t, err.Error(), "No payment method")
	assert.NotContains(t, err.Error(), "no assistant text message found")
}

func TestOpenCodeHTTPRunErrorPropagatesThroughRun(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{"/session/": errorSessionHandler})
	backend := newTestBackend(t, srv.URL)

	res, err := backend.Run(context.Background(), CodingRequest{
		WorkDir: "/tmp/test-repo",
		Prompt:  "Fix issue #1",
		Agent:   &store.Agent{Provider: "opencode", Model: "gemini-3-flash"},
		Task:    &store.Task{ID: 11},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.Success)
	assert.Contains(t, res.Summary, "APIError (status 401)")
	assert.NotEmpty(t, res.Messages)
	assert.Equal(t, "assistant", res.Messages[0].Role)
}

func TestOpenCodeHTTPNoAssistantMessageKeepsFallback(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info":  map[string]any{"id": "msg-1", "role": "user"},
						"parts": []any{map[string]any{"type": "text", "text": "hi"}},
					},
				})
				return
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	backend := newTestBackend(t, srv.URL)

	_, _, err := backend.getLastAssistantMessage(context.Background(), "sess-empty")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no assistant text message found")
}

func TestFormatOpencodeError(t *testing.T) {
	withStatus := &opencodeMessageError{Name: "APIError"}
	withStatus.Data.Message = "boom"
	withStatus.Data.StatusCode = 500
	assert.Equal(t, "APIError (status 500): boom", formatOpencodeError(withStatus))

	noStatus := &opencodeMessageError{Name: "ProviderError"}
	noStatus.Data.Message = "quota exceeded"
	assert.Equal(t, "ProviderError: quota exceeded", formatOpencodeError(noStatus))

	nameOnly := &opencodeMessageError{Name: "UnknownError"}
	assert.Equal(t, "UnknownError", formatOpencodeError(nameOnly))

	empty := &opencodeMessageError{}
	assert.Equal(t, "error", formatOpencodeError(empty))
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
		Type: config.BackendTypeHubOpenCode,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url")
}

func TestNewOpenCodeHTTPBackendRejectsUnsupportedWorkspaceMode(t *testing.T) {
	_, err := NewOpenCodeHTTPBackend("test", config.BackendConfig{
		Type:          config.BackendTypeHubOpenCode,
		BaseURL:       "http://localhost:8080",
		WorkspaceMode: "volume",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_mode")
}

// --- ResolveCodingBackend tests -------------------------------------------

func TestResolveCodingBackendBuiltin(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: ""} // default

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "builtin", backend.Name())
}

func TestResolveCodingBackendExplicitBuiltin(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "builtin"}

	backend, err := factory.ResolveCodingBackend(agent)
	require.NoError(t, err)
	assert.Equal(t, "builtin", backend.Name())
}

func TestResolveCodingBackendOpenCodeHTTPRejected(t *testing.T) {
	// A5: hub backends no longer serve write tasks through the CodingBackend
	// (shared_path) path — they must run via runViaHub's git_sync channel.
	// ResolveCodingBackend refuses with a migration hint.
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Default: "opencode-local",
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:    config.BackendTypeHubOpenCode,
				BaseURL: srv.URL,
				Timeout: "10s",
			},
		},
	}
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "opencode-local"}

	_, err := factory.ResolveCodingBackend(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_transport")
	assert.Contains(t, err.Error(), "git_sync")
}

func TestResolveCodingBackendNotFound(t *testing.T) {
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: "nonexistent"}

	_, err := factory.ResolveCodingBackend(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestResolveCodingBackendUsesDefault(t *testing.T) {
	// A5: a hub backend configured as the agents.backends.default is refused
	// for write tasks too — the default only makes sense as builtin.
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Default: "opencode-local",
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:    config.BackendTypeHubOpenCode,
				BaseURL: srv.URL,
			},
		},
	}
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.DefaultConfig(), nil, "")
	agent := &store.Agent{Backend: ""} // should use default

	_, err := factory.ResolveCodingBackend(agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_transport")
}

// TestResolveCodingBackendNormalizesLegacyIdentifiers verifies pre-1.2.6
// identifiers still route correctly (task 1.2.6a acceptance: rows with
// backend='internal' and configs with type='opencode_http' keep working).
func TestResolveCodingBackendNormalizesLegacyIdentifiers(t *testing.T) {
	// Legacy agent.Backend="internal" resolves to the builtin backend.
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")
	backend, err := factory.ResolveCodingBackend(&store.Agent{Backend: "internal"})
	require.NoError(t, err)
	assert.Equal(t, "builtin", backend.Name(), "legacy internal should resolve to the builtin backend")

	// Canonical agent.Backend="builtin" resolves identically.
	backend2, err := factory.ResolveCodingBackend(&store.Agent{Backend: "builtin"})
	require.NoError(t, err)
	assert.Equal(t, backend.Name(), backend2.Name())

	// Legacy type "opencode_http" normalizes to hub-opencode — and since A5
	// is refused by ResolveCodingBackend with the git_sync migration hint
	// (normalization still works; the CodingBackend path is what went away).
	srv := newTestOpenCodeServer(t, nil)
	legacyBackends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"my-opencode": {Type: "opencode_http", BaseURL: srv.URL},
		},
	}
	factory2 := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, legacyBackends, nil, sandbox.DefaultConfig(), nil, "")
	_, err = factory2.ResolveCodingBackend(&store.Agent{Backend: "my-opencode"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_transport")

	// Canonical type "hub-opencode" behaves identically.
	canonicalBackends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"my-opencode": {Type: config.BackendTypeHubOpenCode, BaseURL: srv.URL},
		},
	}
	factory3 := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, canonicalBackends, nil, sandbox.DefaultConfig(), nil, "")
	_, err = factory3.ResolveCodingBackend(&store.Agent{Backend: "my-opencode"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "workspace_transport")
}

// --- Health check (the git_sync write branch probes the hub before Prepare) ---

func TestOpenCodeBackendUnhealthyReturnsFriendlyError(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/health": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"status":"unhealthy"}`, http.StatusServiceUnavailable)
		},
	})
	cfg := config.BackendConfig{
		Type:    config.BackendTypeHubOpenCode,
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
}
