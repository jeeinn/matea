package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/agents"
)

// mock_hub.go (test-only) — Mock Hub scaffolding for task 1.2.7.
//
// MockHub implements a canonical hub HTTP API so the HubBackend interface is
// exercised over the wire in tests, and Phase 2 hub dispatch tests have a
// ready-made counterpart:
//
//	GET  /hub/health              → 200 {"status":"ok"}
//	POST /hub/tasks               → 202 agents.Handle        (body: agents.TaskContext)
//	GET  /hub/tasks/{id}          → 200 {state, result?}
//	POST /hub/tasks/{id}/cancel   → 200
//
// Scenario knobs cover the Phase 2 failure matrix: normal, timeout (response
// delay beyond the client timeout), 502, auth failure (Bearer token), and
// async long tasks (stay running until ReleaseTask).

// MockHub is a controllable fake hub backend server.
type MockHub struct {
	Server *httptest.Server

	mu      sync.Mutex
	tasks   map[string]*mockHubTask
	counter int

	// Scenario knobs (set before use; not concurrency-safe by design — tests
	// configure the scenario up front).
	Token         string        // if set, require "Authorization: Bearer <Token>" (else 401)
	Fail502       bool          // every endpoint returns 502
	ResponseDelay time.Duration // sleep before every response (timeout scenario)
	AsyncTasks    bool          // tasks stay StateRunning until ReleaseTask

	releases map[string]chan struct{}
}

type mockHubTask struct {
	handle agents.Handle
	state  agents.State
	result *agents.BackendResult
}

// NewMockHub starts a MockHub with the default (normal) scenario.
func NewMockHub(t *testing.T) *MockHub {
	t.Helper()
	h := &MockHub{
		tasks:    make(map[string]*mockHubTask),
		releases: make(map[string]chan struct{}),
	}
	h.Server = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.Close)
	return h
}

// Close shuts the mock server down (registered with t.Cleanup in NewMockHub).
func (h *MockHub) Close() { h.Server.Close() }

// URL returns the mock hub base URL.
func (h *MockHub) URL() string { return h.Server.URL }

// ReleaseTask flips an async long task to done, unblocking the next Poll.
func (h *MockHub) ReleaseTask(remoteID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ch, ok := h.releases[remoteID]; ok {
		select {
		case <-ch: // already released
		default:
			close(ch)
		}
	}
}

func (h *MockHub) serve(w http.ResponseWriter, r *http.Request) {
	if h.Fail502 {
		http.Error(w, `{"error":"bad gateway"}`, http.StatusBadGateway)
		return
	}
	if h.ResponseDelay > 0 {
		time.Sleep(h.ResponseDelay)
	}
	if h.Token != "" && r.Header.Get("Authorization") != "Bearer "+h.Token {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	switch {
	case r.URL.Path == "/hub/health" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})

	case r.URL.Path == "/hub/tasks" && r.Method == http.MethodPost:
		var tc agents.TaskContext
		if err := json.NewDecoder(r.Body).Decode(&tc); err != nil {
			http.Error(w, `{"error":"bad task context"}`, http.StatusBadRequest)
			return
		}
		h.mu.Lock()
		h.counter++
		remoteID := fmt.Sprintf("hub-task-%d", h.counter)
		handle := agents.Handle{
			Backend:        "hub-mock",
			RemoteID:       remoteID,
			IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", tc.TaskType, tc.Repo, tc.IssueID, tc.PRID),
		}
		task := &mockHubTask{
			handle: handle,
			state:  agents.StateDone,
			result: &agents.BackendResult{Summary: "mock hub completed: " + tc.TaskType},
		}
		if h.AsyncTasks {
			task.state = agents.StateRunning
			task.result = nil
			h.releases[remoteID] = make(chan struct{})
		}
		h.tasks[remoteID] = task
		h.mu.Unlock()
		writeJSON(w, http.StatusAccepted, handle)

	case len(r.URL.Path) > len("/hub/tasks/") && r.URL.Path[:len("/hub/tasks/")] == "/hub/tasks/" && r.Method == http.MethodGet:
		remoteID := r.URL.Path[len("/hub/tasks/"):]
		h.mu.Lock()
		task, ok := h.tasks[remoteID]
		if !ok {
			h.mu.Unlock()
			http.Error(w, `{"error":"unknown task"}`, http.StatusNotFound)
			return
		}
		if h.AsyncTasks && task.state == agents.StateRunning {
			if ch, has := h.releases[remoteID]; has {
				select {
				case <-ch:
					task.state = agents.StateDone
					task.result = &agents.BackendResult{Summary: "mock hub completed (async)"}
				default:
				}
			}
		}
		state, result := task.state, task.result
		h.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"state": state, "result": result})

	case len(r.URL.Path) > len("/hub/tasks/") && r.URL.Path[:len("/hub/tasks/")] == "/hub/tasks/" && r.Method == http.MethodPost:
		remoteID := r.URL.Path[len("/hub/tasks/"):]
		if !strings.HasSuffix(remoteID, "/cancel") {
			http.NotFound(w, r)
			return
		}
		remoteID = strings.TrimSuffix(remoteID, "/cancel")
		h.mu.Lock()
		task, ok := h.tasks[remoteID]
		if ok {
			task.state = agents.StateCanceled
			task.result = nil
		}
		h.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unknown task"}`, http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// --- test-only HTTP HubBackend client -----------------------------------------
//
// httpHubClient implements agents.HubBackend against MockHub, proving the
// interface is implementable over plain HTTP (testability validation for
// 1.2.7). A production-grade generic hub client ("hub-api") is Phase 2.

type httpHubClient struct {
	name    string
	baseURL string
	token   string
	client  *http.Client
}

var _ agents.HubBackend = (*httpHubClient)(nil)

func newHTTPHubClient(baseURL, token string, timeout time.Duration) *httpHubClient {
	return &httpHubClient{
		name:    "hub-mock",
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *httpHubClient) Name() string { return c.name }

func (c *httpHubClient) Capabilities() agents.HubCapabilities {
	return agents.HubCapabilities{SupportsToolUse: true}
}

func (c *httpHubClient) Submit(ctx context.Context, tc *agents.TaskContext) (*agents.Handle, error) {
	var handle agents.Handle
	if err := c.do(ctx, http.MethodPost, "/hub/tasks", tc, &handle); err != nil {
		return nil, err
	}
	return &handle, nil
}

func (c *httpHubClient) Poll(ctx context.Context, h *agents.Handle) (*agents.BackendResult, agents.State, error) {
	var resp struct {
		State  agents.State          `json:"state"`
		Result *agents.BackendResult `json:"result"`
	}
	if err := c.do(ctx, http.MethodGet, "/hub/tasks/"+h.RemoteID, nil, &resp); err != nil {
		return nil, "", err
	}
	return resp.Result, resp.State, nil
}

func (c *httpHubClient) Cancel(ctx context.Context, h *agents.Handle) error {
	return c.do(ctx, http.MethodPost, "/hub/tasks/"+h.RemoteID+"/cancel", nil, nil)
}

func (c *httpHubClient) HealthCheck(ctx context.Context) error {
	return c.do(ctx, http.MethodGet, "/hub/health", nil, nil)
}

func (c *httpHubClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("hub request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("hub %s %s returned %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode hub response: %w", err)
		}
	}
	return nil
}
