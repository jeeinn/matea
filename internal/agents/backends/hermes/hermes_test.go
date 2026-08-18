package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestBackend constructs a Hermes Backend bound to a local httptest server.
func newTestBackend(t *testing.T, serverURL string) *Backend {
	t.Helper()
	cfg := config.BackendConfig{
		Type:    config.BackendTypeHubHermes,
		BaseURL: serverURL,
		Auth:    config.BackendAuthConfig{Password: "test-token"},
		Timeout: "10s",
	}
	b, err := NewBackend("hermes-test", cfg)
	require.NoError(t, err)
	return b
}

// startHermesMock starts an httptest.Server that simulates the Hermes Runs API.
// The returned control struct lets the test drive run completion.
func startHermesMock(t *testing.T) (*httptest.Server, *mockHermesControl) {
	t.Helper()
	ctrl := &mockHermesControl{
		runs: make(map[string]*mockHermesRun),
	}
	server := httptest.NewServer(http.HandlerFunc(ctrl.serve))
	t.Cleanup(server.Close)
	return server, ctrl
}

// mockHermesControl drives the mock server behavior.
type mockHermesControl struct {
	mu       sync.Mutex
	runs     map[string]*mockHermesRun
	counter  int
	token    string // if set, require Bearer <token>
	fail502  bool
}

type mockHermesRun struct {
	status string
	output string
}

func (c *mockHermesControl) serve(w http.ResponseWriter, r *http.Request) {
	if c.fail502 {
		http.Error(w, `{"error":"bad gateway"}`, http.StatusBadGateway)
		return
	}
	if c.token != "" && r.Header.Get("Authorization") != "Bearer "+c.token {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	writeJSON := func(status int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}

	switch {
	case r.URL.Path == "/v1/capabilities" && r.Method == http.MethodGet:
		writeJSON(http.StatusOK, map[string]any{"tools": true})

	case r.URL.Path == "/v1/runs" && r.Method == http.MethodPost:
		var req hermesRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.counter++
		runID := fmt.Sprintf("hermes-run-%d", c.counter)
		// Default: async (running), test must call ReleaseRun to complete.
		run := &mockHermesRun{status: "started"}
		c.runs[runID] = run
		c.mu.Unlock()
		writeJSON(http.StatusOK, hermesRunResponse{RunID: runID, Status: "started"})

	case strings.HasPrefix(r.URL.Path, "/v1/runs/") && r.Method == http.MethodGet:
		runID := strings.TrimPrefix(r.URL.Path, "/v1/runs/")
		c.mu.Lock()
		run, ok := c.runs[runID]
		c.mu.Unlock()
		if !ok {
			http.Error(w, `{"error":"unknown run"}`, http.StatusNotFound)
			return
		}
		writeJSON(http.StatusOK, hermesPollResponse{
			Status:  run.status,
			Output:  run.output,
			Session: "matea:test/repo",
		})

	default:
		http.NotFound(w, r)
	}
}

// ReleaseRun flips a mock run to completed with the given output.
func (c *mockHermesControl) ReleaseRun(runID, output string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if run, ok := c.runs[runID]; ok {
		run.status = "completed"
		run.output = output
	}
}

// MarkFailed marks a run as failed.
func (c *mockHermesControl) MarkFailed(runID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if run, ok := c.runs[runID]; ok {
		run.status = "failed"
	}
}

// --- Tests -------------------------------------------------------------------

func TestNewBackend_Validation(t *testing.T) {
	// Missing base_url → error.
	_, err := NewBackend("hermes", config.BackendConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base_url is required")

	// Valid config.
	b, err := NewBackend("hermes", config.BackendConfig{
		BaseURL: "http://localhost:8080",
	})
	require.NoError(t, err)
	assert.Equal(t, "hermes", b.Name())
}

func TestBackend_Capabilities(t *testing.T) {
	b, err := NewBackend("hermes", config.BackendConfig{BaseURL: "http://localhost"})
	require.NoError(t, err)
	caps := b.Capabilities()
	assert.True(t, caps.SupportsToolUse)
	assert.True(t, caps.SupportsMemory)
	assert.True(t, caps.HasIMChannels)
}

func TestBackend_HealthCheck(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	err := b.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestBackend_SubmitAndPoll(t *testing.T) {
	server, ctrl := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	tc := &agents.TaskContext{
		TaskType:    "analyze_issue",
		Role:        "analyze",
		Repo:        "test/repo",
		IssueID:     123,
		IssueTitle:  "Test issue",
		IssueBody:   "Issue body",
		SystemPrompt: "You are an analyst.",
		UserPrompt:  "Please analyze this issue.",
	}

	// Submit returns a handle quickly (async).
	h, err := b.Submit(context.Background(), tc)
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "hermes-test", h.Backend)
	assert.NotEmpty(t, h.RemoteID)
	assert.Contains(t, h.IdempotencyKey, "analyze_issue:test/repo:123:0")

	// First Poll: still running (async). Result may be non-nil but empty.
	result, state, err := b.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateRunning, state)
	if result != nil {
		assert.Empty(t, result.Summary)
	}

	// Release the run and poll again → completed.
	ctrl.ReleaseRun(h.RemoteID, "Analysis complete: issue is about X")
	result, state, err = b.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateDone, state)
	require.NotNil(t, result)
	assert.Contains(t, result.Summary, "Analysis complete")
	assert.Contains(t, result.Summary, "session:matea:test/repo")
}

func TestBackend_Submit_NilTaskContext(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	h, err := b.Submit(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "nil TaskContext")
}

func TestBackend_Poll_NilHandle(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	result, state, err := b.Poll(context.Background(), nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, agents.State(""), state)
}

func TestBackend_Poll_WrongBackend(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	result, state, err := b.Poll(context.Background(), &agents.Handle{
		Backend:  "other-backend",
		RemoteID: "run-1",
	})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, agents.State(""), state)
}

func TestBackend_Poll_FailedRun(t *testing.T) {
	server, ctrl := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	tc := &agents.TaskContext{
		TaskType: "analyze_issue",
		Repo:     "test/repo",
	}
	h, err := b.Submit(context.Background(), tc)
	require.NoError(t, err)

	ctrl.MarkFailed(h.RemoteID)

	result, state, err := b.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, agents.StateFailed, state)
	assert.NotNil(t, result)
}

func TestBackend_Poll_UnknownRun(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	result, state, err := b.Poll(context.Background(), &agents.Handle{
		Backend:  "hermes-test",
		RemoteID: "nonexistent-run",
	})
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, agents.State(""), state)
}

func TestBackend_Cancel_IsNoOp(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	// Cancel on a valid handle: no-op (Hermes has no cancel endpoint).
	err := b.Cancel(context.Background(), &agents.Handle{Backend: "hermes-test", RemoteID: "run-1"})
	assert.NoError(t, err)

	// Cancel on nil handle: error.
	err = b.Cancel(context.Background(), nil)
	assert.Error(t, err)
}

func TestBackend_Submit_WithConversationHistory(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	tc := &agents.TaskContext{
		TaskType: "reply_comment",
		Repo:     "test/repo",
		IssueID:  456,
		Comments: []agents.CommentSnapshot{
			{Author: "alice", Body: "What do you think?", CreatedAt: time.Now()},
			{Author: "matea", Body: "I think it's fine.", CreatedAt: time.Now()},
		},
		UserPrompt: "Please reply.",
	}

	h, err := b.Submit(context.Background(), tc)
	require.NoError(t, err)
	assert.NotEmpty(t, h.RemoteID)
}

func TestBackend_Submit_WithDiff(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	tc := &agents.TaskContext{
		TaskType: "review_pr",
		Repo:     "test/repo",
		PRID:     42,
		Diff:     "--- a/file.go\n+++ b/file.go\n@@ -1,3 +1,3 @@\n-foo\n+bar",
		UserPrompt: "Review this PR.",
	}

	h, err := b.Submit(context.Background(), tc)
	require.NoError(t, err)
	assert.Contains(t, h.IdempotencyKey, "review_pr:test/repo:0:42")
}

func TestBackend_SessionIDCorrelation(t *testing.T) {
	server, _ := startHermesMock(t)
	b := newTestBackend(t, server.URL)

	// Same repo → same session_id.
	tc1 := &agents.TaskContext{TaskType: "analyze_issue", Repo: "test/repo", IssueID: 1}
	tc2 := &agents.TaskContext{TaskType: "review_pr", Repo: "test/repo", PRID: 2}

	h1, err := b.Submit(context.Background(), tc1)
	require.NoError(t, err)
	h2, err := b.Submit(context.Background(), tc2)
	require.NoError(t, err)

	// Both should derive the same session ID (matea:test/repo).
	// We can't directly inspect the session_id from the handle, but we can
	// verify both submissions succeeded and share the repo.
	assert.Equal(t, h1.Backend, h2.Backend)
}

func TestBackend_AuthFailure(t *testing.T) {
	server, ctrl := startHermesMock(t)
	ctrl.token = "correct-token"
	b := newTestBackend(t, server.URL) // uses "test-token"

	tc := &agents.TaskContext{TaskType: "analyze_issue", Repo: "test/repo"}
	_, err := b.Submit(context.Background(), tc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestBackend_Server502(t *testing.T) {
	server, ctrl := startHermesMock(t)
	ctrl.fail502 = true
	b := newTestBackend(t, server.URL)

	tc := &agents.TaskContext{TaskType: "analyze_issue", Repo: "test/repo"}
	_, err := b.Submit(context.Background(), tc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestMapHermesStatus(t *testing.T) {
	tests := []struct {
		hermes string
		want   agents.State
	}{
		{"completed", agents.StateDone},
		{"failed", agents.StateFailed},
		{"cancelled", agents.StateCanceled},
		{"canceled", agents.StateCanceled},
		{"started", agents.StateRunning},
		{"pending", agents.StateRunning},
		{"running", agents.StateRunning},
		{"unknown", agents.StateRunning},
		{"", agents.StateRunning},
	}
	for _, tt := range tests {
		t.Run(tt.hermes, func(t *testing.T) {
			assert.Equal(t, tt.want, mapHermesStatus(tt.hermes))
		})
	}
}

func TestDeriveSessionID(t *testing.T) {
	assert.Equal(t, "matea:owner/repo", deriveSessionID(&agents.TaskContext{Repo: "owner/repo"}))
	assert.Equal(t, "", deriveSessionID(&agents.TaskContext{}))
}

// TestBuildRunRequestGitSyncInjection (task B1): a write task carrying
// GitSyncInfo must inject the shared hub-push contract into the run input —
// byte-identical to what OpenCode receives (task A4), because both hubs get
// the same BuildGitSyncInstructions block. The markers asserted here mirror
// TestOpenCodeSubmitGitSyncInjectsInstructions in the agents package.
func TestBuildRunRequestGitSyncInjection(t *testing.T) {
	var captured hermesRunRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/runs" && r.Method == http.MethodPost {
			json.NewDecoder(r.Body).Decode(&captured)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(hermesRunResponse{RunID: "run-1", Status: "started"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	t.Cleanup(srv.Close)
	b := newTestBackend(t, srv.URL)

	h, err := b.Submit(context.Background(), &agents.TaskContext{
		TaskType:    "solve_issue",
		Repo:        "o/r",
		IssueID:     5,
		TaskID:      42,
		UserPrompt:  "Fix it",
		MemoryKeys: map[string]string{"analysis_summary": "prior finding"},
		GitSync: &agents.GitSyncInfo{
			CloneURL:       "ssh://git@example.com/o/r.git",
			PrivateKey:     "PRIVKEY",
			DraftBranch:    "matea/hub-42",
			BaseBranch:     "main",
			BaseHEAD:       "aaaa",
			RequiredFooter: "matea-task-id: 42",
			HubPush:        true,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	// Original prompt and memory are preserved.
	assert.Contains(t, captured.Input, "Fix it")
	assert.Contains(t, captured.Input, "prior finding")

	// The injected git_sync contract carries all five elements.
	assert.Contains(t, captured.Input, "Git workflow (MANDATORY")
	assert.Contains(t, captured.Input, "base64 -d > key")
	assert.Contains(t, captured.Input, "UFJJVktFWQ==") // base64("PRIVKEY")
	assert.Contains(t, captured.Input, "matea/hub-42")
	assert.Contains(t, captured.Input, "matea-task-id: 42")
	assert.Contains(t, captured.Input, "matea-draft-head: ")
	assert.Contains(t, captured.Input, "ssh://git@example.com/o/r.git")
	assert.Contains(t, captured.Input, "matea-hub-42", "per-task work subdir")

	// Recency: the mandatory git workflow sits after the memory block.
	assert.Greater(t, strings.Index(captured.Input, "Git workflow (MANDATORY"),
		strings.Index(captured.Input, "prior finding"))
}

// TestBuildRunRequestNoGitSyncUnchanged pins the read/reply path: without
// GitSyncInfo the request carries no git contract at all.
func TestBuildRunRequestNoGitSyncUnchanged(t *testing.T) {
	b := newTestBackend(t, "http://unused")
	req := b.buildRunRequest(&agents.TaskContext{
		TaskType:   "analyze_issue",
		Repo:       "o/r",
		TaskID:     7,
		UserPrompt: "Analyze it",
	})
	assert.NotContains(t, req.Input, "Git workflow (MANDATORY")
	assert.NotContains(t, req.Input, "matea-draft-head")
}
