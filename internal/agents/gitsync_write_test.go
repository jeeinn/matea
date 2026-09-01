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
	"sync"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A4 write-channel coverage: a hub-opencode backend with
// workspace_transport=git_sync runs a write task through runViaHub — Matea
// Prepares (fake issuer), the "hub" pre-pushes a draft branch to a local bare
// remote (file://), Approve fetches + validates + opens the PR against a fake
// Gitea, and Cleanup revokes the key. No local Matea workspace is involved.

// fakeDeployKeyIssuer records Issue/Revoke and returns deterministic keys.
type fakeDeployKeyIssuer struct {
	mu       sync.Mutex
	issued   []string // titles
	revoked  []int64
	nextID   int64
	issueErr error
}

func (f *fakeDeployKeyIssuer) Issue(ctx context.Context, owner, repo, title string) (*IssuedDeployKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.issueErr != nil {
		return nil, f.issueErr
	}
	f.nextID++
	f.issued = append(f.issued, title)
	return &IssuedDeployKey{KeyID: f.nextID, PrivateKey: "FAKE-PRIVATE-KEY", PublicKey: "ssh-ed25519 FAKE"}, nil
}

func (f *fakeDeployKeyIssuer) Revoke(ctx context.Context, owner, repo string, keyID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, keyID)
	return nil
}

// gitSyncTestHub captures the submitted TaskContext and reports a done state.
type gitSyncTestHub struct {
	name      string
	mu        sync.Mutex
	gotTC     *TaskContext
	pollRes   *BackendResult
	pollErr   error
	pollState State
	healthErr error
}

func (b *gitSyncTestHub) Name() string { return b.name }
func (b *gitSyncTestHub) Submit(ctx context.Context, tc *TaskContext) (*Handle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gotTC = tc
	return &Handle{Backend: b.name, RemoteID: "remote-gs-1", IdempotencyKey: "k"}, nil
}
func (b *gitSyncTestHub) Poll(ctx context.Context, h *Handle) (*BackendResult, State, error) {
	return b.pollRes, b.pollState, b.pollErr
}
func (b *gitSyncTestHub) Cancel(ctx context.Context, h *Handle) error { return nil }
func (b *gitSyncTestHub) Capabilities() HubCapabilities               { return HubCapabilities{} }
func (b *gitSyncTestHub) HealthCheck(ctx context.Context) error       { return b.healthErr }

// gitSyncFakeGitea serves the API surface Approve touches. The repo's
// clone_url points at a local bare repo via file:// so fetchDraft runs real git.
type gitSyncFakeGitea struct {
	t        *testing.T
	cloneURL string
	mainHEAD string
	// defaultBranch overrides the repo metadata answer (empty = "main").
	defaultBranch string
	// prBaseRef, when set, makes GET /pulls/{n} answer with a PR whose base
	// ref is this value — the PR a continuation task runs on.
	prBaseRef string
	// prState and prHeadSHA complete that PR's detail so its head branch is
	// reusable as a draft branch (open PR, nameable head tip). Both default to
	// empty on purpose: the base-branch tests predate draft-branch reuse and
	// assert the legacy shape, where an unreadable state means "do not reuse".
	prState   string
	prHeadSHA string
	// prHeadRef overrides the head branch name (default: matea/hub-14, the
	// branch the base-branch tests were written around).
	prHeadRef string
	prCreated *gitea.CreatePRRequest
	server    *httptest.Server
}

func (g *gitSyncFakeGitea) headRefOrLegacy() string {
	if strings.TrimSpace(g.prHeadRef) != "" {
		return g.prHeadRef
	}
	return "matea/hub-14"
}

func (g *gitSyncFakeGitea) defaultBranchOrMain() string {
	if strings.TrimSpace(g.defaultBranch) != "" {
		return g.defaultBranch
	}
	return "main"
}

func newGitSyncFakeGitea(t *testing.T, cloneURL, mainHEAD string) *gitSyncFakeGitea {
	g := &gitSyncFakeGitea{t: t, cloneURL: cloneURL, mainHEAD: mainHEAD}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"default_branch": g.defaultBranchOrMain(),
			"clone_url":      g.cloneURL,
			"ssh_url":        "ssh://git@example.com/o/r.git",
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/branches/{name...}", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"name":   r.PathValue("name"),
			"commit": map[string]any{"id": g.mainHEAD},
		})
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls/{index}", func(w http.ResponseWriter, r *http.Request) {
		if g.prBaseRef == "" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]any{"message": "pull request does not exist"})
			return
		}
		head := map[string]any{"ref": g.headRefOrLegacy()}
		if g.prHeadSHA != "" {
			head["sha"] = g.prHeadSHA
		}
		pr := map[string]any{
			"number": 8,
			"base":   map[string]any{"ref": g.prBaseRef},
			"head":   head,
		}
		if g.prState != "" {
			pr["state"] = g.prState
		}
		json.NewEncoder(w).Encode(pr)
	})
	mux.HandleFunc("/api/v1/repos/o/r/pulls", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode([]any{}) // no open PRs
			return
		}
		var req gitea.CreatePRRequest
		json.NewDecoder(r.Body).Decode(&req)
		g.prCreated = &req
		json.NewEncoder(w).Encode(map[string]any{"number": 77, "title": req.Title, "html_url": "http://fake/pr/77"})
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

type gitSyncTestGiteaFactory struct{ client *gitea.Client }

func (f *gitSyncTestGiteaFactory) GetGiteaClient(token string) *gitea.Client { return f.client }
func (f *gitSyncTestGiteaFactory) GetAdminGiteaClient() *gitea.Client        { return f.client }

// setupGitSyncRemote builds a bare remote with one commit on main, then adds a
// hub-pushed draft branch carrying the required footer. Returns (cloneURL,
// mainHEAD, draftHEAD).
func setupGitSyncRemote(t *testing.T, taskID int64) (string, string, string) {
	t.Helper()
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return string(out)
	}

	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	run(base, "init", "--bare", "-q", remote)
	run(base, "init", "-q", work)
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "remote", "add", "origin", remote)
	run(work, "push", "-q", "origin", "main")
	mainHEAD := strings.TrimSpace(run(work, "rev-parse", "HEAD"))

	// Simulate the hub: branch off main, commit with the required footer, push.
	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: hub change", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", DraftBranchName(taskID))
	draftHEAD := strings.TrimSpace(run(work, "rev-parse", "HEAD"))
	return remote, mainHEAD, draftHEAD
}

func newGitSyncFactory(db *store.DB, giteaURL, cloneURL string, issuer DeployKeyIssuer) *RunnerFactory {
	return &RunnerFactory{
		db:           db,
		giteaFactory: &gitSyncTestGiteaFactory{client: gitea.NewClient(giteaURL, "")},
		backends: config.AgentBackendsConfig{
			Backends: map[string]config.BackendConfig{
				"gs-opencode": {
					Type:               config.BackendTypeHubOpenCode,
					BaseURL:            "http://unused",
					WorkspaceTransport: config.WorkspaceTransportGitSync,
				},
			},
		},
		deployKeyIssuer: issuer,
		sandboxCfg:      sandbox.Config{BaseDir: os.TempDir()},
	}
}

func TestRunViaHubGitSyncWritePath(t *testing.T) {
	taskID := int64(9001)
	cloneURL, mainHEAD, draftHEAD := setupGitSyncRemote(t, taskID)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, cloneURL, issuer)

	hub := &gitSyncTestHub{
		name:      "gs-opencode",
		pollState: StateDone,
		pollRes:   &BackendResult{Summary: "fixed it\n\n" + DraftHeadTrailer + draftHEAD},
	}

	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 12, TaskType: "solve_issue", Event: "Fix the bug"}
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: taskID}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, "pr", res.Action)
	assert.Equal(t, 77, res.PRID)

	// Submit received the prepared GitSyncInfo (deploy key + draft contract).
	require.NotNil(t, hub.gotTC)
	require.NotNil(t, hub.gotTC.GitSync, "Submit must carry GitSyncInfo")
	assert.Equal(t, DraftBranchName(taskID), hub.gotTC.GitSync.DraftBranch)
	assert.Equal(t, mainHEAD, hub.gotTC.GitSync.BaseHEAD)
	assert.Equal(t, "FAKE-PRIVATE-KEY", hub.gotTC.GitSync.PrivateKey)
	assert.True(t, hub.gotTC.GitSync.HubPush)

	// Handle persisted the draft contract + key id.
	h, err := db.GetHubHandle(taskID)
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, DraftBranchName(taskID), h.DraftBranch)
	assert.Equal(t, mainHEAD, h.BaseHEAD)
	assert.Equal(t, int64(1), h.DeployKeyID)
	assert.Equal(t, store.HubHandleStatusDone, h.Status)

	// PR opened against the draft branch.
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, DraftBranchName(taskID), fake.prCreated.Head)
	assert.Equal(t, "main", fake.prCreated.Base)

	// Key issued once, revoked once (cleanup after success).
	assert.Equal(t, []string{fmt.Sprintf("matea-hub-task-%d", taskID)}, issuer.issued)
	assert.Equal(t, []int64{1}, issuer.revoked)
}

func TestRunViaHubGitSyncWritePathHubPushedNothing(t *testing.T) {
	taskID := int64(9002)
	// Remote WITHOUT a draft branch: the hub reported done but never pushed.
	base := t.TempDir()
	remote := filepath.Join(base, "remote.git")
	work := filepath.Join(base, "work")
	run := func(dir string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return string(out)
	}
	run(base, "init", "--bare", "-q", remote)
	run(base, "init", "-q", work)
	run(work, "config", "user.email", "t@t")
	run(work, "config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(work, "README.md"), []byte("init\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "init")
	run(work, "branch", "-M", "main")
	run(work, "remote", "add", "origin", remote)
	run(work, "push", "-q", "origin", "main")
	mainHEAD := strings.TrimSpace(run(work, "rev-parse", "HEAD"))

	fake := newGitSyncFakeGitea(t, remote, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "done but pushed nothing"}}
	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 1, TaskType: "solve_issue", Event: "x"}
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 1, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on remote")
	assert.Nil(t, fake.prCreated, "no PR may be opened when nothing was pushed")
	assert.Equal(t, []int64{1}, issuer.revoked, "key must still be revoked on the failure path")

	// B5: the hub reported Done but validation failed — handle must be Failed.
	h, herr := db.GetHubHandle(taskID)
	require.NoError(t, herr)
	require.NotNil(t, h)
	assert.Equal(t, store.HubHandleStatusFailed, h.Status, "handle must be terminal Failed so it is never re-attached")
}

// TestRunViaHubGitSyncWrongBranchRejected covers the "hub 越权分支" adversarial
// case: the hub pushes valid-looking commits, but to a branch name it chose
// itself instead of the mandated matea/hub-{taskID}. Approve must reject and
// the handle must be marked Failed (not Done).
func TestRunViaHubGitSyncWrongBranchRejected(t *testing.T) {
	taskID := int64(9003)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	// Hub creates its own branch and pushes a correctly-footered commit there.
	run(work, "checkout", "-q", "-b", "hub-chose-this")
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: valid commit", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", "hub-chose-this")

	fake := newGitSyncFakeGitea(t, remote, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "pushed to my own branch"}}
	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 1, TaskType: "solve_issue", Event: "x"}
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 1, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on remote", "mandated draft branch is missing")
	assert.Nil(t, fake.prCreated, "no PR when hub ignored the draft branch contract")
	assert.Equal(t, []int64{1}, issuer.revoked, "key revoked on adversarial failure")

	h, herr := db.GetHubHandle(taskID)
	require.NoError(t, herr)
	require.NotNil(t, h)
	assert.Equal(t, store.HubHandleStatusFailed, h.Status)
}

// TestRunViaHubGitSyncMissingFooterRejected covers the "required footer"
// adversarial case at the runViaHub level. The hub pushes to the mandated
// branch but omits the matea-task-id footer; Approve rejects and the handle
// is marked Failed.
func TestRunViaHubGitSyncMissingFooterRejected(t *testing.T) {
	taskID := int64(9004)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: unsigned hub change")
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	fake := newGitSyncFakeGitea(t, remote, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "pushed without footer"}}
	task := &store.Task{ID: taskID, Repo: "o/r", IssueID: 1, TaskType: "solve_issue", Event: "x"}
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 1, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required footer")
	assert.Nil(t, fake.prCreated)
	assert.Equal(t, []int64{1}, issuer.revoked)

	h, herr := db.GetHubHandle(taskID)
	require.NoError(t, herr)
	require.NotNil(t, h)
	assert.Equal(t, store.HubHandleStatusFailed, h.Status)
}

func TestOpenCodeSubmitGitSyncInjectsInstructions(t *testing.T) {
	var capturedPrompt string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodPost {
				var body struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				if len(body.Parts) > 0 {
					capturedPrompt = body.Parts[0].Text
				}
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant"})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
				json.NewEncoder(w).Encode([]any{
					map[string]any{"info": map[string]any{"id": "m2", "role": "assistant"},
						"parts": []any{map[string]any{"type": "text", "text": "done\n\nmatea-draft-head: deadbeefcafe"}}},
				})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	backend := newTestBackend(t, srv.URL)

	h, err := backend.Submit(context.Background(), &TaskContext{
		TaskType:   "solve_issue",
		Repo:       "o/r",
		IssueID:    5,
		TaskID:     42,
		UserPrompt: "Fix it",
		// no SandboxPath — git_sync must not require a Matea workspace
		GitSync: &GitSyncInfo{
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

	// Instructions carry the base64 key, draft branch, footer and trailer contract.
	assert.Contains(t, capturedPrompt, "base64 -d > key")
	assert.Contains(t, capturedPrompt, "UFJJVktFWQ==") // base64("PRIVKEY")
	assert.Contains(t, capturedPrompt, "matea/hub-42")
	assert.Contains(t, capturedPrompt, "matea-task-id: 42")
	assert.Contains(t, capturedPrompt, "matea-draft-head: ")
	assert.Contains(t, capturedPrompt, "ssh://git@example.com/o/r.git")

	// Poll reports the git_sync result with the parsed draft head.
	res, state, err := backend.Poll(context.Background(), h)
	require.NoError(t, err)
	assert.Equal(t, StateDone, state)
	require.NotNil(t, res.GitSync)
	assert.Equal(t, "matea/hub-42", res.GitSync.DraftBranch)
	assert.Equal(t, "deadbeefcafe", res.GitSync.DraftHEAD)
}

func TestParseDraftHeadTrailer(t *testing.T) {
	assert.Equal(t, "abc123", ParseDraftHeadTrailer("work summary\n\nmatea-draft-head: abc123\n"))
	assert.Equal(t, "", ParseDraftHeadTrailer("no trailer here"))
	assert.Equal(t, "", ParseDraftHeadTrailer(""))
}
