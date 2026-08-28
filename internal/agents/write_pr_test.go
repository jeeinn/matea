package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingGiteaFactory records how clients were built so tests can assert the
// identity a delivery is attributed to (agent token vs platform admin).
type recordingGiteaFactory struct {
	baseURL    string
	agentToks  []string
	adminCalls int
}

func (f *recordingGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	f.agentToks = append(f.agentToks, token)
	return gitea.NewClient(f.baseURL, token)
}

func (f *recordingGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	f.adminCalls++
	return gitea.NewClient(f.baseURL, "admin-token")
}

// TestResolveTaskGiteaClientPrefersAgentToken pins the attribution rule: a task's
// delivery is authored by the agent that ran it, so the PR (and every Gitea
// timeline event derived from it) names the worker instead of the platform admin.
func TestResolveTaskGiteaClientPrefersAgentToken(t *testing.T) {
	f := &recordingGiteaFactory{baseURL: "http://localhost:0"}
	agent := &store.Agent{Name: "code-opencode", GiteaToken: "agent-token"}

	client := resolveTaskGiteaClient(f, agent)

	require.NotNil(t, client)
	assert.Equal(t, "agent-token", client.Token)
	assert.Equal(t, []string{"agent-token"}, f.agentToks)
	assert.Zero(t, f.adminCalls, "admin client must not be used while the agent carries a token")
}

// TestResolveTaskGiteaClientFallsBackToAdmin covers the degrade path: a missing
// agent or empty token still delivers the PR (unattributed beats undelivered),
// but logs a warning so the misconfiguration stays visible.
func TestResolveTaskGiteaClientFallsBackToAdmin(t *testing.T) {
	t.Run("nil agent", func(t *testing.T) {
		f := &recordingGiteaFactory{baseURL: "http://localhost:0"}
		client := resolveTaskGiteaClient(f, nil)
		require.NotNil(t, client)
		assert.Equal(t, "admin-token", client.Token)
		assert.Equal(t, 1, f.adminCalls)
		assert.Empty(t, f.agentToks)
	})

	t.Run("empty token", func(t *testing.T) {
		f := &recordingGiteaFactory{baseURL: "http://localhost:0"}
		client := resolveTaskGiteaClient(f, &store.Agent{Name: "coder007", GiteaToken: ""})
		require.NotNil(t, client)
		assert.Equal(t, "admin-token", client.Token)
		assert.Equal(t, 1, f.adminCalls)
		assert.Empty(t, f.agentToks)
	})
}

// TestResolveTaskGiteaClientNilFactory guards the dispatch-only factories (tests,
// deployments without Gitea) that carry no client factory at all.
func TestResolveTaskGiteaClientNilFactory(t *testing.T) {
	assert.Nil(t, resolveTaskGiteaClient(nil, &store.Agent{Name: "a", GiteaToken: "t"}))
}

// TestResolveTaskGiteaClientTokenReachesHTTP proves the chosen token is not just
// stored on the struct but actually sent on the wire — the PR would otherwise be
// created by whichever identity Gitea falls back to.
func TestResolveTaskGiteaClientTokenReachesHTTP(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	f := &recordingGiteaFactory{baseURL: server.URL}
	client := resolveTaskGiteaClient(f, &store.Agent{Name: "code-opencode", GiteaToken: "agent-token"})
	require.NotNil(t, client)

	_, err := client.FindOpenPRByHead("owner", "repo", "matea/hub-1")
	require.NoError(t, err)
	assert.Equal(t, "token agent-token", gotAuth)
}

// newPRCaptureServer serves the two calls FinalizeWriteTaskPR makes and records
// the raw JSON body of the PR create request.
func newPRCaptureServer(t *testing.T, captured *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/repos/owner/repo/pulls":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]interface{}{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/repos/owner/repo/pulls":
			raw, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			*captured = string(raw)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(gitea.PRResponse{Number: 7, HTMLURL: "http://localhost/owner/repo/pulls/7"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

// TestFinalizeWriteTaskPRLinksIssueWithRefsNotFixes pins the issue-link keyword.
// "Fixes #N" is in Gitea's CLOSE_KEYWORDS, so merging the PR would silently
// auto-close the issue and take the close decision away from the human. "Refs
// #N" still records the cross-reference in the issue timeline but leaves the
// issue open.
func TestFinalizeWriteTaskPRLinksIssueWithRefsNotFixes(t *testing.T) {
	var prBody string
	server := newPRCaptureServer(t, &prBody)
	defer server.Close()

	task := &store.Task{ID: 35, Event: "Issue 5", IssueID: 5}
	_, err := FinalizeWriteTaskPR(gitea.NewClient(server.URL, "agent-token"),
		"owner", "repo", "matea/hub-35", "main", task, "dev", "done")
	require.NoError(t, err)

	assert.Contains(t, prBody, "Refs #5", "PR must still cross-reference the issue")
	assert.NotContains(t, prBody, "Fixes #5")
	assert.NotContains(t, prBody, "fixes #5")
	assert.NotContains(t, prBody, "Closes #5")
}

// TestFinalizeWriteTaskPROmitsIssueLinkWithoutIssue covers tasks that carry no
// issue (e.g. PR-triggered work): no dangling reference should be emitted.
func TestFinalizeWriteTaskPROmitsIssueLinkWithoutIssue(t *testing.T) {
	var prBody string
	server := newPRCaptureServer(t, &prBody)
	defer server.Close()

	task := &store.Task{ID: 36, Event: "Refactor", IssueID: 0}
	_, err := FinalizeWriteTaskPR(gitea.NewClient(server.URL, "agent-token"),
		"owner", "repo", "matea/hub-36", "main", task, "dev", "done")
	require.NoError(t, err)

	assert.NotContains(t, prBody, "Refs #")
	assert.NotContains(t, prBody, "Fixes #")
}
