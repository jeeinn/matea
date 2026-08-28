package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
//
// Note: since Poll now re-attaches from the sidecar on a cache miss, the
// "unknown handle" case requires the sidecar itself to reject the session
// (404) — a realistic sidecar returns 404 for a non-existent session, so the
// error surfaces; a sidecar that answers for any session id would instead
// recover the result (see TestOpenCodeHubPollReattachesAfterCacheMiss).
func TestOpenCodeHubPollErrors(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		// Realistic sidecar: an unknown session id 404s, so Poll's re-attach
		// correctly reports "unknown handle" rather than fabricating a result.
		"/session/sess-ghost/message": func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		},
	})
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

// TestOpenCodeHubPollReattachesAfterCacheMiss pins the restart-recovery half of
// the Handle persistence contract: when the instance-local outcome cache is gone
// (a Matea restart), Poll must recover the result from the still-living opencode
// sidecar via GET /session/{id}/message, rather than reporting "unknown handle"
// and losing the already-completed run.
func TestOpenCodeHubPollReattachesAfterCacheMiss(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:    "solve_issue",
		Provider:    "mock",
		Model:       "m",
		UserPrompt:  "fix it",
		SandboxPath: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	// Simulate a Matea restart: wipe the instance-local outcome cache.
	backend.hubMu.Lock()
	backend.hubResults = map[string]hubOutcome{}
	backend.hubMu.Unlock()

	// Poll must re-attach to the sidecar and return the terminal result.
	res, state, err := backend.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, StateDone, state)
	require.NotNil(t, res)
	assert.Equal(t, "Done.", res.Summary, "re-attach must recover the sidecar's assistant message")
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

// makeTestGitRepoWithBranch creates a local git repository with a "main" branch
// (one commit, per makeTestGitRepo) plus an extra branch named branch carrying
// one additional commit. It returns the repo's absolute path, used as the clone
// source for review-workspace tests where the PR head points at the extra
// branch.
func makeTestGitRepoWithBranch(t *testing.T, branch string) string {
	t.Helper()
	dir := makeTestGitRepo(t)
	cmd := exec.Command("git", "-C", dir, "checkout", "-b", branch)
	require.NoError(t, cmd.Run(), "git checkout -b %s", branch)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package x\n"), 0o644))
	cmd = exec.Command("git", "-C", dir, "add", ".")
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "-C", dir, "commit", "-m", "feature work")
	require.NoError(t, cmd.Run(), "git commit on %s", branch)
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

// --- D7 second cut: ReviewRunner via hub-opencode (task 2.2.2) ----------------

// TestReviewRunnerViaOpenCode verifies the D7 second-cut routing contract:
// when the agent's backend is hub-opencode, ReviewRunner resolves it, prepares a
// workspace (shallow clone of the PR head branch), and forwards the sandbox
// path to OpenCode. The assertion requires that OpenCode's Submit (POST
// /session) was actually reached — degenerating to single-shot is NOT accepted.
func TestReviewRunnerViaOpenCode(t *testing.T) {
	const headBranch = "feature/review"
	repoPath := makeTestGitRepoWithBranch(t, headBranch)

	// Mock Gitea: serve repo info + PR detail (with the head ref pointing at
	// the branch created above) so prepareReviewWorkspace can clone it.
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/repos/o/r":
			resp, _ := json.Marshal(map[string]string{
				"default_branch": "main",
				"clone_url":      "file://" + repoPath,
			})
			w.Write(resp)
		case "/api/v1/repos/o/r/pulls/9":
			resp, _ := json.Marshal(map[string]any{
				"title": "Add feature",
				"body":  "please review",
				"head":  map[string]any{"ref": headBranch},
			})
			w.Write(resp)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gs.Close)

	// Probe: record that the OpenCode sidecar actually received a session
	// create (POST /session). This is the contract we must verify — the review
	// task must route through hub-opencode's Submit, not silently degrade to
	// single-shot LLM (which also "passed" the old loose assertion).
	opencodeSessionCreated := false
	hs := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session": func(w http.ResponseWriter, r *http.Request) {
			opencodeSessionCreated = true
			defaultSessionCreateHandler(w, r)
		},
	})
	f := newOpenCodeTestFactory(t, hs.URL, gs.URL)
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewReviewRunner(f)

	task := &store.Task{ID: 44, Repo: "o/r", IssueID: 7, PRID: 9, TaskType: "review_pr", Event: "Add feature", Context: "review this"}
	agent := &store.Agent{Backend: "opencode-local", Provider: "mock", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	// The routing contract: ReviewRunner must reach hub-opencode's Submit
	// (createSession), not fall back to single-shot review.
	assert.True(t, opencodeSessionCreated,
		"review_pr with hub-opencode backend must route through OpenCode Submit, not degrade to single-shot")
	// And the sidecar's assistant message is returned verbatim.
	assert.Equal(t, "Done.", res.Content,
		"OpenCode path must return the sidecar assistant message, not single-shot output")
}

// TestReviewRunnerViaOpenCodeFallsBackOnWorkspaceFailure verifies that when
// workspace preparation fails (no gitea reachable), the hub-opencode review path
// degrades to a single-shot result rather than failing the task outright.
func TestReviewRunnerViaOpenCodeFallsBackOnWorkspaceFailure(t *testing.T) {
	// Gitea unreachable — workspace prep will fail.
	gs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(gs.Close)

	hs := newTestOpenCodeServer(t, nil)
	f := newOpenCodeTestFactory(t, hs.URL, gs.URL)

	// llmRegistry must be set for the single-shot fallback to work.
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewReviewRunner(f)

	task := &store.Task{ID: 45, Repo: "o/r", IssueID: 7, PRID: 9, TaskType: "review_pr", Event: "Add feature", Context: "review this"}
	agent := &store.Agent{Backend: "opencode-local", Provider: "mock", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	assert.Contains(t, res.Content, "mock-single-shot")
}

// --- D7 third cut: InteractionRunner (reply) via hub-opencode (task 2.2.3) ----

// TestReplyRunnerViaOpenCode verifies the D7 third-cut routing contract: when
// the agent's backend is hub-opencode, InteractionRunner resolves it, prepares a
// minimal (empty) workspace purely to satisfy the OpenCode Submit SandboxPath
// contract (decision B), and forwards the sandbox path to OpenCode. The
// assertion requires that OpenCode's Submit (POST /session) was actually
// reached — degenerating to single-shot is NOT accepted. The reply path has no
// Gitea dependency, so giteaURL is empty here (proving it works without a gitea
// block configured).
func TestReplyRunnerViaOpenCode(t *testing.T) {
	// Probe: record that the OpenCode sidecar actually received a session
	// create (POST /session). This is the contract we must verify — the reply
	// task must route through hub-opencode's Submit, not silently degrade to
	// single-shot LLM.
	opencodeSessionCreated := false
	hs := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session": func(w http.ResponseWriter, r *http.Request) {
			opencodeSessionCreated = true
			defaultSessionCreateHandler(w, r)
		},
	})
	// giteaURL is "" — the reply-via-OpenCode path has no Gitea dependency
	// (minimal empty workspace, decision B).
	f := newOpenCodeTestFactory(t, hs.URL, "")
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewInteractionRunner(f)

	task := &store.Task{ID: 47, Repo: "o/r", IssueID: 7, TaskType: "reply_comment", Event: "Reply", Context: "please reply to this"}
	agent := &store.Agent{Backend: "opencode-local", Provider: "mock", SystemPrompt: "be concise"}

	res, err := runner.Run(context.Background(), task, agent)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "comment", res.Action)
	// The routing contract: InteractionRunner must reach hub-opencode's Submit
	// (createSession), not fall back to single-shot reply.
	assert.True(t, opencodeSessionCreated,
		"reply_comment with hub-opencode backend must route through OpenCode Submit, not degrade to single-shot")
	// And the sidecar's assistant message is returned verbatim.
	assert.Equal(t, "Done.", res.Content,
		"OpenCode path must return the sidecar assistant message, not single-shot output")
}

// TestReplyRunnerViaOpenCodeFallsBackOnWorkspaceFailure verifies that when the
// minimal workspace preparation fails (sandbox Setup cannot create the working
// directory), the hub-opencode reply path degrades to a single-shot result
// rather than failing the task outright. The failure is induced by pointing the
// fixed-mode sandbox base dir at a regular file, which makes os.MkdirAll fail
// reliably on every OS.
func TestReplyRunnerViaOpenCodeFallsBackOnWorkspaceFailure(t *testing.T) {
	// A fixed-mode base dir that is a regular file makes the sandbox Setup
	// (os.MkdirAll(.../task_N)) fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	sbCfg := sandbox.DefaultSandboxConfig()
	sbCfg.Mode = sandbox.ModeFixed
	sbCfg.BaseDir = blocker

	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:               config.BackendTypeHubOpenCode,
				BaseURL:            "http://unused",
				Auth:               config.BackendAuthConfig{Password: "test-key"},
				WorkspaceTransport: config.WorkspaceTransportGitSync,
			},
		},
	}
	f := NewRunnerFactory(nil, nil, nil, config.AgentDefaultsConfig{},
		config.DefaultAgentLoopConfig(), nil, backends, nil, sbCfg, nil, "")
	f.llmRegistry = newOpencodeMockLLMRegistry(t)
	runner := NewInteractionRunner(f)

	task := &store.Task{ID: 48, Repo: "o/r", IssueID: 7, TaskType: "reply_comment", Event: "Reply", Context: "fallback"}
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
				WorkspaceTransport: config.WorkspaceTransportGitSync,
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

// --- Conversation log tests (Phase 2 hub transcript passthrough) -------------

// conversationTestServer returns an OpenCode mock server whose message list
// contains a user echo, two assistant turns (with an intervening tool message),
// and a final assistant text message.
func conversationTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant"})
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info":  map[string]any{"id": "msg-1", "role": "user"},
						"parts": []any{map[string]any{"type": "text", "text": "implement the fix"}},
					},
					map[string]any{
						"info":  map[string]any{"id": "msg-2", "role": "assistant"},
						"parts": []any{map[string]any{"type": "text", "text": "I'll start by reading the files."}},
					},
					map[string]any{
						"info":  map[string]any{"id": "msg-3", "role": "tool"},
						"parts": []any{map[string]any{"type": "text", "text": "file contents"}},
					},
					map[string]any{
						"info":  map[string]any{"id": "msg-4", "role": "assistant"},
						"parts": []any{map[string]any{"type": "text", "text": "Done."}},
					},
				})
			default:
				http.NotFound(w, r)
			}
		},
	})
}

// newOpenCodeTestFactoryWithDB builds a RunnerFactory with an in-memory SQLite
// database and a debug-config getter wired for conversation-log tests.
func newOpenCodeTestFactoryWithDB(t *testing.T, enabled bool) (*RunnerFactory, *store.DB) {
	t.Helper()
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	cfg := config.DebugConfig{ConversationLog: config.ConversationLogConfig{Enabled: enabled, MaxContentChars: 100000}}
	getDebug := func() config.DebugConfig { return cfg }

	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {
				Type:               config.BackendTypeHubOpenCode,
				BaseURL:            "http://unused",
				Auth:               config.BackendAuthConfig{Password: "test-key"},
				WorkspaceTransport: config.WorkspaceTransportGitSync,
			},
		},
	}
	sbCfg := sandbox.DefaultSandboxConfig()
	sbCfg.Mode = sandbox.ModeTemp
	f := NewRunnerFactory(nil, nil, db, config.AgentDefaultsConfig{},
		config.DefaultAgentLoopConfig(), getDebug, backends, nil, sbCfg, nil, "")
	return f, db
}

func TestOpenCodeHubBackendResultMessages(t *testing.T) {
	srv := conversationTestServer(t)
	backend := newTestBackend(t, srv.URL)

	tc := &TaskContext{
		TaskType:     "analyze_issue",
		Repo:         "owner/repo",
		IssueID:      5,
		TaskID:       10,
		Provider:     "mock",
		Model:        "test-model",
		SystemPrompt: "You are a coder.",
		UserPrompt:   "Fix issue #5",
		SandboxPath:  t.TempDir(),
	}
	h, err := backend.Submit(context.Background(), tc)
	require.NoError(t, err)
	require.NotNil(t, h)

	res, state, err := backend.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, StateDone, state)
	require.NotNil(t, res)
	require.Len(t, res.Messages, 3, "user echo should be dropped")

	assert.Equal(t, "assistant", res.Messages[0].Role)
	assert.Contains(t, res.Messages[0].Content, "reading the files")
	assert.Equal(t, "tool", res.Messages[1].Role)
	assert.Equal(t, "assistant", res.Messages[2].Role)
	assert.Contains(t, res.Messages[2].Content, "Done.")
}

func TestOpenCodeHubSubmitFailureRetainsMessages(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant"})
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info": map[string]any{
							"id":   "msg-2",
							"role": "assistant",
							"error": map[string]any{
								"name": "APIError",
								"data": map[string]any{
									"message":     "No payment method",
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
		},
	})
	backend := newTestBackend(t, srv.URL)

	tc := &TaskContext{
		TaskType:     "analyze_issue",
		Repo:         "owner/repo",
		IssueID:      6,
		TaskID:       11,
		Provider:     "mock",
		Model:        "test-model",
		SystemPrompt: "You are a coder.",
		UserPrompt:   "Fix issue #6",
		SandboxPath:  t.TempDir(),
	}
	h, err := backend.Submit(context.Background(), tc)
	require.NoError(t, err, "Submit must return a Handle even on failure so the transcript survives")
	require.NotNil(t, h)

	res, state, err := backend.Poll(context.Background(), h)
	assert.Equal(t, StateFailed, state)
	require.Error(t, err)
	require.NotNil(t, res, "Poll must return the failed result with Messages")
	require.Len(t, res.Messages, 1)
	assert.Equal(t, "assistant", res.Messages[0].Role)
	assert.Contains(t, res.Messages[0].Content, "APIError")
	assert.Contains(t, res.Messages[0].Content, "401")
}

func TestOpenCodeHubPollReattachFillsMessages(t *testing.T) {
	srv := conversationTestServer(t)
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:    "analyze_issue",
		Repo:        "owner/repo",
		IssueID:     7,
		TaskID:      12,
		Provider:    "mock",
		Model:       "test-model",
		UserPrompt:  "Fix issue #7",
		SandboxPath: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	// Simulate a Matea restart: wipe the instance-local outcome cache.
	backend.hubMu.Lock()
	backend.hubResults = map[string]hubOutcome{}
	backend.hubMu.Unlock()

	res, state, err := backend.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, StateDone, state)
	require.NotNil(t, res)
	require.Len(t, res.Messages, 3, "re-attach must recover Messages, dropping user echo")
	assert.Equal(t, "assistant", res.Messages[0].Role)
	assert.Equal(t, "tool", res.Messages[1].Role)
	assert.Equal(t, "assistant", res.Messages[2].Role)
}

func TestOpenCodeHubPollReattachFailedRetainsMessages(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant"})
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info": map[string]any{
							"id":   "msg-2",
							"role": "assistant",
							"error": map[string]any{
								"name": "APIError",
								"data": map[string]any{
									"message":     "No payment method",
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
		},
	})
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:    "analyze_issue",
		Repo:        "owner/repo",
		IssueID:     8,
		TaskID:      13,
		Provider:    "mock",
		Model:       "test-model",
		UserPrompt:  "Fix issue #8",
		SandboxPath: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	// Simulate a Matea restart: wipe the instance-local outcome cache.
	backend.hubMu.Lock()
	backend.hubResults = map[string]hubOutcome{}
	backend.hubMu.Unlock()

	res, state, err := backend.Poll(context.Background(), h)
	assert.Equal(t, StateFailed, state)
	require.Error(t, err)
	require.NotNil(t, res, "re-attach to a failed session must return the result with Messages")
	require.Len(t, res.Messages, 1)
	assert.Equal(t, "assistant", res.Messages[0].Role)
	assert.Contains(t, res.Messages[0].Content, "APIError")
	assert.Contains(t, res.Messages[0].Content, "401")
}

func TestOpenCodeHubBackendResultMessagesSkipsEmpty(t *testing.T) {
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodPost:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant"})
			case strings.HasSuffix(path, "/message") && r.Method == http.MethodGet:
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode([]any{
					map[string]any{
						"info":  map[string]any{"id": "msg-1", "role": "user"},
						"parts": []any{map[string]any{"type": "text", "text": "implement the fix"}},
					},
					map[string]any{
						// Empty assistant message without text or error: must be skipped.
						"info":  map[string]any{"id": "msg-2", "role": "assistant"},
						"parts": []any{},
					},
					map[string]any{
						"info":  map[string]any{"id": "msg-3", "role": "assistant"},
						"parts": []any{map[string]any{"type": "text", "text": "Done."}},
					},
				})
			default:
				http.NotFound(w, r)
			}
		},
	})
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:    "analyze_issue",
		Repo:        "owner/repo",
		IssueID:     9,
		TaskID:      14,
		Provider:    "mock",
		Model:       "test-model",
		UserPrompt:  "Fix issue #9",
		SandboxPath: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, h)

	res, _, err := backend.Poll(context.Background(), h)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Len(t, res.Messages, 1, "empty assistant message should be skipped")
	assert.Equal(t, "Done.", res.Messages[0].Content)
}

func TestRecordHubConversation(t *testing.T) {
	f, db := newOpenCodeTestFactoryWithDB(t, true)

	agent := &store.Agent{Name: "test", GiteaUsername: "u1", GiteaToken: "t"}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{Repo: "o/r", IssueID: 1, TaskType: "analyze_issue", AgentID: agent.ID}
	require.NoError(t, db.CreateTask(task))

	tc := &TaskContext{
		SystemPrompt: "You are a coder.",
		UserPrompt:   "Fix issue #1",
	}
	res := &BackendResult{
		Summary: "Done.",
		Messages: []llm.Message{
			{Role: "assistant", Content: "Let me read the code."},
			{Role: "tool", Content: "file contents"},
			{Role: "assistant", Content: "Done."},
		},
	}

	f.recordHubConversation(task, tc, res, nil)

	logs, err := db.ListConversationLogs(task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 5, "iteration 0 (system+user) + iteration 1 (assistant+tool) + iteration 2 (assistant)")

	assert.Equal(t, 0, logs[0].Iteration)
	assert.Equal(t, "system", logs[0].Role)
	assert.Equal(t, 0, logs[1].Iteration)
	assert.Equal(t, "user", logs[1].Role)
	assert.Equal(t, 1, logs[2].Iteration)
	assert.Equal(t, "assistant", logs[2].Role)
	assert.Equal(t, 1, logs[3].Iteration)
	assert.Equal(t, "tool", logs[3].Role)
	assert.Equal(t, 2, logs[4].Iteration)
	assert.Equal(t, "assistant", logs[4].Role)
}

func TestRecordHubConversationSkipsDuplicateIter0(t *testing.T) {
	f, db := newOpenCodeTestFactoryWithDB(t, true)

	agent := &store.Agent{Name: "test", GiteaUsername: "u4", GiteaToken: "t"}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{Repo: "o/r", IssueID: 4, TaskType: "analyze_issue", AgentID: agent.ID}
	require.NoError(t, db.CreateTask(task))

	tc := &TaskContext{SystemPrompt: "sys", UserPrompt: "user"}
	res := &BackendResult{Summary: "Done.", Messages: []llm.Message{{Role: "assistant", Content: "Done."}}}

	f.recordHubConversation(task, tc, res, nil)
	// Simulate a re-run writing the same task again: iteration 0 (the input)
	// must be deduplicated, but assistant-side iterations are intentionally
	// appended because a real re-run produces a fresh transcript.
	f.recordHubConversation(task, tc, res, nil)

	logs, err := db.ListConversationLogs(task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 4, "iteration 0 once + two assistant iterations")
	assert.Equal(t, 0, logs[0].Iteration)
	assert.Equal(t, "system", logs[0].Role)
	assert.Equal(t, 0, logs[1].Iteration)
	assert.Equal(t, "user", logs[1].Role)
	assert.Equal(t, 1, logs[2].Iteration)
	assert.Equal(t, "assistant", logs[2].Role)
	assert.Equal(t, 2, logs[3].Iteration)
	assert.Equal(t, "assistant", logs[3].Role)
}

func TestRecordHubConversationFailureFallback(t *testing.T) {
	f, db := newOpenCodeTestFactoryWithDB(t, true)

	agent := &store.Agent{Name: "test", GiteaUsername: "u2", GiteaToken: "t"}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{Repo: "o/r", IssueID: 2, TaskType: "analyze_issue", AgentID: agent.ID}
	require.NoError(t, db.CreateTask(task))
	tc := &TaskContext{UserPrompt: "Fix issue #2"}

	f.recordHubConversation(task, tc, nil, fmt.Errorf("hub backend failed"))

	logs, err := db.ListConversationLogs(task.ID)
	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, "user", logs[0].Role)
	assert.Equal(t, "assistant", logs[1].Role)
	assert.Contains(t, logs[1].Content, "hub backend failed")
}

func TestRecordHubConversationDisabled(t *testing.T) {
	f, db := newOpenCodeTestFactoryWithDB(t, false)

	agent := &store.Agent{Name: "test", GiteaUsername: "u3", GiteaToken: "t"}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{Repo: "o/r", IssueID: 3, TaskType: "analyze_issue", AgentID: agent.ID}
	require.NoError(t, db.CreateTask(task))
	tc := &TaskContext{UserPrompt: "Fix issue #3"}
	res := &BackendResult{Summary: "Done.", Messages: []llm.Message{{Role: "assistant", Content: "Done."}}}

	f.recordHubConversation(task, tc, res, nil)

	count, err := db.CountConversationLogs(task.ID)
	require.NoError(t, err)
	assert.Zero(t, count, "conversation log must not be written when disabled")
}
