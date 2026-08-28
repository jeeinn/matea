package gitea

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreateIssueCommentReturnsID covers the status-card prerequisite: the
// create call must hand back Gitea's comment ID so the card can be PATCHed
// later instead of being re-posted on every state change.
func TestCreateIssueCommentReturnsID(t *testing.T) {
	var gotMethod, gotPath string
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &payload))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":4242,"body":"card","created_at":"2026-08-28T12:00:00Z"}`))
	}))
	defer server.Close()

	comment, err := NewClient(server.URL, "agent-token").CreateIssueComment("jeeinn", "rust-study", 5, "card")

	require.NoError(t, err)
	require.NotNil(t, comment)
	assert.Equal(t, 4242, comment.ID)
	assert.Equal(t, "card", comment.Body)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/repos/jeeinn/rust-study/issues/5/comments", gotPath)
	require.NotNil(t, payload)
	assert.Equal(t, "card", payload["body"])
}

// TestCreateIssueCommentPropagatesAPIError ensures a rejected create surfaces an
// error rather than a zero-ID comment that would later be PATCHed against the
// wrong target.
func TestCreateIssueCommentPropagatesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"token does not have required scope"}`))
	}))
	defer server.Close()

	comment, err := NewClient(server.URL, "bad-token").CreateIssueComment("jeeinn", "rust-study", 5, "card")

	require.Error(t, err)
	assert.Nil(t, comment)
	assert.Contains(t, err.Error(), "403")
}

// TestEditIssueCommentPatchesInPlace pins the endpoint and method used to update
// a comment in place — the mechanism that keeps a task to a single status card.
func TestEditIssueCommentPatchesInPlace(t *testing.T) {
	var gotMethod, gotPath string
	var payload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &payload))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":4242,"body":"updated"}`))
	}))
	defer server.Close()

	err := NewClient(server.URL, "agent-token").EditIssueComment("jeeinn", "rust-study", 4242, "updated")

	require.NoError(t, err)
	assert.Equal(t, http.MethodPatch, gotMethod)
	assert.Equal(t, "/api/v1/repos/jeeinn/rust-study/issues/comments/4242", gotPath)
	require.NotNil(t, payload)
	assert.Equal(t, "updated", payload["body"])
}

// TestEditIssueCommentPropagatesAPIError guards the case where the card exists
// but the identity editing it may not modify it (e.g. agent A created it, agent
// B tries to update): the caller must see the failure, not a silent no-op.
func TestEditIssueCommentPropagatesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"not permitted"}`))
	}))
	defer server.Close()

	err := NewClient(server.URL, "other-agent-token").EditIssueComment("jeeinn", "rust-study", 4242, "updated")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
}
