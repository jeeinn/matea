package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenCodeHubSubmitPollSuccess verifies the HubBackend path end-to-end
// against the mock sidecar: Submit runs the session synchronously, the Handle
// carries the opencode session id, and Poll reports the terminal result.
func TestOpenCodeHubSubmitPollSuccess(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:     "solve_issue",
		Role:         "coder",
		Repo:         "owner/repo",
		IssueID:      5,
		TaskID:       10,
		Provider:     "mock",
		Model:        "test-model",
		SystemPrompt: "You are a coder.",
		UserPrompt:   "Fix issue #5",
		SandboxPath:  t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "test-opencode", h.Backend)
	assert.Equal(t, "sess-test-123", h.RemoteID, "RemoteID should be the opencode session id")
	assert.Equal(t, "solve_issue:owner/repo:5:0", h.IdempotencyKey)

	for i := 0; i < 2; i++ { // Poll is repeatable and terminal
		result, state, err := backend.Poll(context.Background(), h)
		require.NoError(t, err)
		assert.Equal(t, StateDone, state)
		require.NotNil(t, result)
		assert.Equal(t, "Done.", result.Summary)
		assert.False(t, result.ExternallyHandled)
	}
}

// TestOpenCodeHubSubmitRejectsNonWrite pins the write-only constraint at the
// hub seam (Analyze/Review/Reply never run on OpenCode).
func TestOpenCodeHubSubmitRejectsNonWrite(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	for _, taskType := range []string{"analyze_issue", "review_pr", "reply_comment"} {
		h, err := backend.Submit(context.Background(), &TaskContext{
			TaskType:    taskType,
			Provider:    "mock",
			Model:       "m",
			UserPrompt:  "hi",
			SandboxPath: t.TempDir(),
		})
		require.Error(t, err, "task type %s must be rejected", taskType)
		assert.Nil(t, h)
		assert.Contains(t, err.Error(), "write task types only")
	}
}

// TestOpenCodeHubSubmitValidation pins submission-time errors: nil context,
// missing sandbox path, and sidecar-side session creation failure.
func TestOpenCodeHubSubmitValidation(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "nil TaskContext")

	h, err = backend.Submit(context.Background(), &TaskContext{
		TaskType:   "fix_bug",
		Provider:   "mock",
		Model:      "m",
		UserPrompt: "fix it",
	})
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "SandboxPath")

	// Sidecar failure on session creation fails Submit (no orphan Handle).
	badSrv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		},
	})
	badBackend := newTestBackend(t, badSrv.URL)
	h, err = badBackend.Submit(context.Background(), &TaskContext{
		TaskType:    "solve_issue",
		Provider:    "mock",
		Model:       "m",
		UserPrompt:  "fix it",
		SandboxPath: t.TempDir(),
	})
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "create opencode session")
}

// TestOpenCodeHubPollErrors pins Poll strictness: nil handles, handles owned
// by another backend, and unknown session ids are all errors.
func TestOpenCodeHubPollErrors(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	_, _, err := backend.Poll(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil Handle")

	_, _, err = backend.Poll(context.Background(), &Handle{Backend: "builtin", RemoteID: "builtin-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `backend "builtin"`)

	_, _, err = backend.Poll(context.Background(), &Handle{Backend: "test-opencode", RemoteID: "sess-ghost"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown handle")
}

// TestOpenCodeHubCancelAbortsSession verifies Cancel maps to the sidecar's
// abort endpoint for known sessions and is an idempotent no-op otherwise.
func TestOpenCodeHubCancelAbortsSession(t *testing.T) {
	var aborted string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/sess-test-123/abort": func(w http.ResponseWriter, r *http.Request) {
			aborted = "sess-test-123"
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:    "solve_issue",
		Provider:    "mock",
		Model:       "m",
		UserPrompt:  "fix it",
		SandboxPath: t.TempDir(),
	})
	require.NoError(t, err)

	require.NoError(t, backend.Cancel(context.Background(), h))
	assert.Equal(t, "sess-test-123", aborted, "Cancel should abort the opencode session")

	// Idempotent no-ops: nil handle and empty RemoteID must not error.
	assert.NoError(t, backend.Cancel(context.Background(), nil))
	assert.NoError(t, backend.Cancel(context.Background(), &Handle{Backend: "test-opencode"}))
}

// TestOpenCodeHubCapabilities pins the capability declaration: server-side
// tool use; Matea retains git/PR finalization.
func TestOpenCodeHubCapabilities(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	backend := newTestBackend(t, srv.URL)

	caps := backend.Capabilities()
	assert.True(t, caps.SupportsToolUse)
	assert.False(t, caps.SupportsMemory)
	assert.False(t, caps.SupportsSkillEvolution)
	assert.False(t, caps.SupportsMCPClient)
	assert.False(t, caps.HasIMChannels)
	assert.False(t, caps.HandlesGit)
}
