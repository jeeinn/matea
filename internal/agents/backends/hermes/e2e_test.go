package hermes_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock Hermes Runs API -------------------------------------------------

type hermesTestServer struct {
	srv        *httptest.Server
	mu         sync.Mutex
	lastBody   string
	pollStatus string // status returned by GET /v1/runs/{id}
}

func (hs *hermesTestServer) lastRequestBody() string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.lastBody
}

func (hs *hermesTestServer) setPollStatus(s string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.pollStatus = s
}

func (hs *hermesTestServer) currentPollStatus() string {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	return hs.pollStatus
}

func newMockHermes(t *testing.T) *hermesTestServer {
	t.Helper()
	hs := &hermesTestServer{pollStatus: "completed"}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/runs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(r.Body)
			hs.mu.Lock()
			hs.lastBody = string(b)
			hs.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"run_id":"r1","status":"started"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/v1/runs/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":%q,"output":"hello from hermes","session_id":"matea:o/r"}`,
			hs.currentPollStatus())
	})
	mux.HandleFunc("/v1/capabilities", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	hs.srv = httptest.NewServer(mux)
	t.Cleanup(hs.srv.Close)
	return hs
}

// --- mock Gitea ----------------------------------------------------------

func newMockGitea(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "/pulls/") && strings.HasSuffix(p, ".diff"):
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("diff --git a/x.go b/x.go\n+added line"))
		case strings.Contains(p, "/pulls/") && strings.HasSuffix(p, "/files"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		case strings.Contains(p, "/pulls/"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"title":"PR title","body":"PR body"}`))
		case strings.Contains(p, "/issues/") && strings.HasSuffix(p, "/comments"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[{"id":1,"body":"please help","user":{"login":"alice"},"created_at":"2026-01-01T00:00:00Z"}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// --- factory / gitea factory mocks ---------------------------------------

type mockGiteaFactory struct{ url string }

func (m *mockGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient(m.url, token)
}

func (m *mockGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	return gitea.NewClient(m.url, "")
}

func newTestFactory(t *testing.T, hermesURL, giteaURL string, db *store.DB) *agents.RunnerFactory {
	t.Helper()
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"hermes-local": {
				Type:               config.BackendTypeHubHermes,
				BaseURL:            hermesURL,
				Auth:               config.BackendAuthConfig{Password: "test-key"},
				WorkspaceTransport: config.WorkspaceTransportSharedPath,
			},
		},
	}
	var gf agents.GiteaClientFactory
	if giteaURL != "" {
		gf = &mockGiteaFactory{url: giteaURL}
	}
	// llmRegistry / toolPacks / mcpReg are nil: hub execution paths never
	// touch them (Hermes owns its LLM and tools).
	return agents.NewRunnerFactory(nil, gf, db, config.AgentDefaultsConfig{},
		config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.SandboxConfig{}, nil, "")
}

func openMemoryDB(t *testing.T) *store.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mem.db")
	db, err := store.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// --- tests ---------------------------------------------------------------

// Task 2.1.1 wiring: a configured hub-hermes backend resolves to a Hermes
// HubBackend (the factory registered it via init()).
func TestResolveHubBackendHermes(t *testing.T) {
	hs := newMockHermes(t)
	f := newTestFactory(t, hs.srv.URL, "", nil)

	hb, err := f.ResolveHubBackend(&store.Agent{Backend: "hermes-local"})
	require.NoError(t, err)
	require.NotNil(t, hb)
	assert.Equal(t, "hermes-local", hb.Name())
}

// Task 2.1.2: analyze_issue routes through Hermes and returns its result.
func TestAnalyzeRunnerViaHermes(t *testing.T) {
	hs := newMockHermes(t)
	f := newTestFactory(t, hs.srv.URL, "", nil)
	runner := agents.NewAnalyzeRunner(f)

	task := &store.Task{ID: 1, Repo: "o/r", IssueID: 5, TaskType: "analyze_issue", Event: "Bug", Context: "something is broken"}
	agent := &store.Agent{Backend: "hermes-local", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	assert.Contains(t, res.Content, "hello from hermes")
}

// Task 2.1.3: review_pr routes through Hermes, carrying the pre-fetched diff.
func TestReviewRunnerViaHermes(t *testing.T) {
	hs := newMockHermes(t)
	gs := newMockGitea(t)
	f := newTestFactory(t, hs.srv.URL, gs.URL, nil)
	runner := agents.NewReviewRunner(f)

	task := &store.Task{ID: 2, Repo: "o/r", IssueID: 5, PRID: 7, TaskType: "review_pr"}
	agent := &store.Agent{Backend: "hermes-local", GiteaToken: "tok", SystemPrompt: "review carefully"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.Content, "hello from hermes")

	// The diff fetched from Gitea must have been forwarded to Hermes.
	assert.Contains(t, hs.lastRequestBody(), "added line")
}

// Task 2.1.4: reply_comment routes through Hermes, injecting the comment
// history as conversation_history (session continuation).
func TestInteractionRunnerViaHermes(t *testing.T) {
	hs := newMockHermes(t)
	gs := newMockGitea(t)
	f := newTestFactory(t, hs.srv.URL, gs.URL, nil)
	runner := agents.NewInteractionRunner(f)

	task := &store.Task{ID: 3, Repo: "o/r", IssueID: 5, TaskType: "reply_comment", Context: "how do I fix this?"}
	agent := &store.Agent{Backend: "hermes-local", GiteaToken: "tok", SystemPrompt: "helpful"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.Content, "hello from hermes")

	// The comment history must have been forwarded to Hermes.
	assert.Contains(t, hs.lastRequestBody(), "please help")
	assert.Contains(t, hs.lastRequestBody(), "alice")
}

// Regression (review finding 1): a hub-hermes reply must not panic when no
// Gitea client factory is configured. The comment history is best-effort for
// the hub path, so the task still completes — just without history.
func TestInteractionRunnerViaHermesWithoutGiteaFactory(t *testing.T) {
	hs := newMockHermes(t)
	f := newTestFactory(t, hs.srv.URL, "", nil) // no gitea factory
	runner := agents.NewInteractionRunner(f)

	task := &store.Task{ID: 4, Repo: "o/r", IssueID: 5, TaskType: "reply_comment", Context: "how do I fix this?"}
	agent := &store.Agent{Backend: "hermes-local", GiteaToken: "tok", SystemPrompt: "helpful"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Contains(t, res.Content, "hello from hermes")
	assert.NotContains(t, hs.lastRequestBody(), "conversation_history",
		"no comments were fetchable, so no history should be sent")
}

// Regression (review finding 1, builtin side): the same missing factory on the
// builtin path must produce a clear error rather than a nil dereference.
func TestInteractionRunnerBuiltinWithoutGiteaFactoryErrors(t *testing.T) {
	hs := newMockHermes(t)
	f := newTestFactory(t, hs.srv.URL, "", nil)
	runner := agents.NewInteractionRunner(f)

	task := &store.Task{ID: 5, Repo: "o/r", IssueID: 5, TaskType: "reply_comment", Context: "hi"}
	agent := &store.Agent{Backend: "", SystemPrompt: "helpful"} // builtin

	_, err := runner.Run(context.Background(), task, agent)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gitea client factory not configured")
}

// Regression (review finding 2): cancelling the caller's context must abort
// the poll loop promptly with an accurate reason, not wait out the 30-minute
// safety timeout.
func TestHubRunAbortsOnContextCancel(t *testing.T) {
	hs := newMockHermes(t)
	hs.setPollStatus("running") // never reaches a terminal state
	f := newTestFactory(t, hs.srv.URL, "", nil)
	runner := agents.NewAnalyzeRunner(f)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	task := &store.Task{ID: 6, Repo: "o/r", IssueID: 5, TaskType: "analyze_issue", Context: "stuck run"}
	start := time.Now()
	_, err := runner.Run(ctx, task, &store.Agent{Backend: "hermes-local"})
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hub run aborted")
	assert.Contains(t, err.Error(), "cancelled by executor")
	assert.Less(t, elapsed, 10*time.Second, "abort must be prompt, not bounded by hubPollTimeout")
}

// Task 2.1.5 (D3): analyze writes a repo/issue memory; a later review on the
// same repo+issue receives it in its Hermes request (memory sharing).
func TestHermesMemorySharing(t *testing.T) {
	hs := newMockHermes(t)
	db := openMemoryDB(t)
	f := newTestFactory(t, hs.srv.URL, "", db)
	analyzeRunner := agents.NewAnalyzeRunner(f)

	task := &store.Task{ID: 10, Repo: "o/r", IssueID: 5, TaskType: "analyze_issue", Event: "Bug", Context: "root cause found"}
	_, err := analyzeRunner.Run(context.Background(), task, &store.Agent{Backend: "hermes-local", SystemPrompt: "x"})
	require.NoError(t, err)

	mem, err := db.GetMemory("o/r", 5, agents.AnalyzeMemoryKey)
	require.NoError(t, err)
	assert.Contains(t, mem, "hello from hermes", "analyze must persist its conclusion")

	// A review on the same repo+issue should carry the remembered context.
	gs := newMockGitea(t)
	f2 := newTestFactory(t, hs.srv.URL, gs.URL, db)
	reviewTask := &store.Task{ID: 11, Repo: "o/r", IssueID: 5, PRID: 7, TaskType: "review_pr"}
	_, err = agents.NewReviewRunner(f2).Run(context.Background(), reviewTask, &store.Agent{Backend: "hermes-local", GiteaToken: "tok"})
	require.NoError(t, err)

	assert.Contains(t, hs.lastRequestBody(), "hello from hermes",
		"review request should carry the analyze-written memory")
}
