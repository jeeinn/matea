package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
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

// TestOpenCodeHubSubmitAcceptsReadOrWrite maps every task type opencodeSubType
// currently accepts and asserts Submit succeeds. Read tasks (analyze/review/reply)
// are allowed as of D7 first cut (task 2.2.1): they carry the "dev" sub-type and
// a prepared sandbox, and the sandbox is cleaned up by the caller.
func TestOpenCodeHubSubmitAcceptsReadOrWrite(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	for _, taskType := range []string{"solve_issue", "solve_comment", "fix_bug", "analyze_issue", "review_pr", "reply_comment"} {
		h, err := backend.Submit(context.Background(), &TaskContext{
			TaskType:    taskType,
			Provider:    "mock",
			Model:       "m",
			UserPrompt:  "hi",
			SandboxPath: t.TempDir(),
		})
		require.NoError(t, err, "task type %s must be accepted", taskType)
		assert.NotNil(t, h)
	}
}

// TestOpenCodeHubSubmitRejectsUnknownTaskType pins the contract that
// unsupported task types (e.g. "trigger" or any future type without a mapping)
// are rejected at the hub seam with an actionable error — never silently
// accepted with a wrong sub-type.
func TestOpenCodeHubSubmitRejectsUnknownTaskType(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	for _, taskType := range []string{"trigger", "unknown_type", ""} {
		h, err := backend.Submit(context.Background(), &TaskContext{
			TaskType:    taskType,
			Provider:    "mock",
			Model:       "m",
			UserPrompt:  "hi",
			SandboxPath: t.TempDir(),
		})
		require.Error(t, err, "task type %q must be rejected", taskType)
		assert.Nil(t, h)
		assert.Contains(t, err.Error(), "unsupported task type")
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

// --- D7 first cut: AnalyzeRunner via hub-opencode (task 2.2.1) ----------------

// makeTestGitRepo creates a local git repository with a single "main" branch and
// one commit, then returns its absolute path. Used as the clone source for
// analyze-workspace tests.
func makeTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "main", dir)
	require.NoError(t, cmd.Run(), "git init")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# test\n"), 0o644))
	cmd = exec.Command("git", "-C", dir, "config", "user.email", "test@matea.local")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "config", "user.name", "test")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "add", ".")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "commit", "-m", "init")
	require.NoError(t, cmd.Run(), "git commit")
	return dir
}

// TestAnalyzeRunnerViaOpenCode verifies the D7 first-cut routing contract:
// when the agent's backend is hub-opencode, AnalyzeRunner resolves it, prepares
// a workspace, and forwards the sandbox path to OpenCode. The assertion accepts
// both outcomes of workspace prep — opencode success or single-shot fallback —
// since both are valid D7 first-cut behaviors (the workspace prep depends on a
// reachable git server, which is environment-sensitive in CI).
func TestAnalyzeRunnerViaOpenCode(t *testing.T) {
	repoPath := makeTestGitRepo(t)

	// Mock Gitea: return a file:// clone URL pointing at the local repo so the
	// shallow clone works in the sandbox. Encoded via json.Marshal so Windows
	// backslashes in the path are escaped correctly.
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/repos/o/r":
			resp, _ := json.Marshal(map[string]string{
				"default_branch": "main",
				"clone_url":      "file://" + repoPath,
			})
			w.Write(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gs.Close)

	// Probe: record that the OpenCode sidecar actually received a session
	// create (POST /session). This is the contract we must verify — the
	// analyze task must route through hub-opencode's Submit, not silently
	// degrade to single-shot LLM (which also "passed" the old loose
	// assertion). A zero CommandTimeout in the sandbox config previously made
	// the workspace clone fail instantly and masked this.
	opencodeSessionCreated := false
	hs := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session": func(w http.ResponseWriter, r *http.Request) {
			opencodeSessionCreated = true
			defaultSessionCreateHandler(w, r)
		},
	})
	f := newOpenCodeTestFactory(t, hs.URL, gs.URL)
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewAnalyzeRunner(f)

	task := &store.Task{ID: 42, Repo: "o/r", IssueID: 7, TaskType: "analyze_issue", Event: "Bug", Context: "something is broken"}
	agent := &store.Agent{Backend: "opencode-local", Provider: "mock", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	// The routing contract: AnalyzeRunner must reach hub-opencode's Submit
	// (createSession), not fall back to single-shot analysis.
	assert.True(t, opencodeSessionCreated,
		"analyze_issue with hub-opencode backend must route through OpenCode Submit, not degrade to single-shot")
	// And the sidecar's assistant message is returned verbatim.
	assert.Equal(t, "Done.", res.Content,
		"OpenCode path must return the sidecar assistant message, not single-shot output")
}

// TestAnalyzeRunnerViaOpenCodeFallsBackOnWorkspaceFailure verifies that when
// workspace preparation fails (no gitea reachable), the hub-opencode path
// degrades to a single-shot result rather than failing the task outright.
func TestAnalyzeRunnerViaOpenCodeFallsBackOnWorkspaceFailure(t *testing.T) {
	// Gitea unreachable — workspace prep will fail.
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(gs.Close)

	hs := newTestOpenCodeServer(t, nil)
	f := newOpenCodeTestFactory(t, hs.URL, gs.URL)

	// llmRegistry must be set for the single-shot fallback to work.
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewAnalyzeRunner(f)

	task := &store.Task{ID: 43, Repo: "o/r", IssueID: 7, TaskType: "analyze_issue", Event: "Bug", Context: "fallback"}
	agent := &store.Agent{Backend: "opencode-local", Provider: "mock", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	assert.Contains(t, res.Content, "mock-single-shot")
}

// newOpenCodeTestFactory builds a RunnerFactory with a hub-opencode backend
// named "opencode-local" registered, plus the supplied gitea/mock URLs.
func newOpenCodeTestFactory(t *testing.T, opencodeURL, giteaURL string) *RunnerFactory {
	t.Helper()
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:               config.BackendTypeHubOpenCode,
				BaseURL:            opencodeURL,
				Auth:               config.BackendAuthConfig{Password: "test-key"},
				WorkspaceTransport: config.WorkspaceTransportSharedPath,
			},
		},
	}
	var gf GiteaClientFactory
	if giteaURL != "" {
		gf = &opencodeMockGiteaFactory{url: giteaURL}
	}
	// Real sandbox config (non-zero CommandTimeout + temp mode) so the
	// workspace clone can actually run. A zero-value SandboxConfig{} made the
	// clone fail instantly (context.WithTimeout(ctx, 0)) and silently routed
	// analyze through single-shot — masking the hub-opencode path.
	sbCfg := sandbox.DefaultSandboxConfig()
	sbCfg.Mode = sandbox.ModeTemp
	sbCfg.BaseDir = ""
	return NewRunnerFactory(nil, gf, nil, config.AgentDefaultsConfig{},
		config.DefaultAgentLoopConfig(), nil, backends, nil, sbCfg, nil, "")
}

// opencodeMockGiteaFactory returns a real gitea.Client pointed at the mock URL.
type opencodeMockGiteaFactory struct{ url string }

func (m *opencodeMockGiteaFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient(m.url, token)
}

func (m *opencodeMockGiteaFactory) GetAdminGiteaClient() *gitea.Client {
	return gitea.NewClient(m.url, "")
}

// opencodeMockLLMProvider implements llm.Provider for single-shot fallback tests.
type opencodeMockLLMProvider struct{}

func (m *opencodeMockLLMProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: "mock-single-shot", Usage: llm.Usage{TotalTokens: 10}}, nil
}

func (m *opencodeMockLLMProvider) SupportsTools() bool { return false }
func (m *opencodeMockLLMProvider) Name() string        { return "mock" }

// newOpencodeMockLLMRegistry returns a registry that always serves opencodeMockLLMProvider.
func newOpencodeMockLLMRegistry(t *testing.T) *llm.Registry {
	t.Helper()
	reg := &llm.Registry{}
	reg.Register("mock", &opencodeMockLLMProvider{})
	return reg
}
