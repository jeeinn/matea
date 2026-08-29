package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B2.3 coverage: the hub-side session continuation contract. A continuation
// task's hub branches its NEW per-task draft branch from the session's
// recorded LastHead (GitSyncInfo.AnchorHEAD), validation anchors on it, and
// memories (repo/issue table + rolling session summary) reach the hub prompt.

// --- validation unit tests (anchor semantics) ------------------------------

func TestValidateGitSyncDraftContinuationAnchor(t *testing.T) {
	info := testGitSyncInfo()
	info.AnchorHEAD = "eeee4444" // session LastHead; BaseHEAD is the fresh base head
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "ffff5555"}
	fetched := &fetchedDraft{
		DraftHEAD:  "ffff5555",
		BaseHEAD:   "aaaa0000", // base did not drift during THIS task
		IsAncestor: true,       // descends from the anchor (fetchDraft computes vs anchor)
		NewCommitMsgs: []string{
			// Only THIS task's commits are in the anchor..head range; the
			// previous task's commits (footer matea-task-id: 41) are excluded
			// by construction in fetchDraft.
			"feat: continued change\n\nmatea-task-id: 42\n",
		},
	}
	require.NoError(t, validateGitSyncDraft(info, result, fetched, DiffPolicy{}))
}

func TestValidateGitSyncDraftContinuationWrongStartPoint(t *testing.T) {
	info := testGitSyncInfo()
	info.AnchorHEAD = "eeee4444"
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "ffff5555"}
	fetched := &fetchedDraft{
		DraftHEAD:  "ffff5555",
		BaseHEAD:   "aaaa0000",
		IsAncestor: false, // branched off the new base tip, not the anchor
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Contains(t, err.Error(), "eeee4444", "error must name the continuation anchor")
}

func TestValidateGitSyncDraftContinuationNoNewCommits(t *testing.T) {
	info := testGitSyncInfo()
	info.AnchorHEAD = "eeee4444"
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "eeee4444"}
	fetched := &fetchedDraft{DraftHEAD: "eeee4444", BaseHEAD: "aaaa0000", IsAncestor: true}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no new commits", "draft head == anchor means the hub pushed nothing new")
}

// --- instructions ----------------------------------------------------------

func TestBuildGitSyncInstructionsContinuationAnchor(t *testing.T) {
	info := testGitSyncInfo()
	info.CloneURL = "ssh://git@example.com/o/r.git"
	info.PrivateKey = "PRIVKEY"
	info.AnchorHEAD = "eeee4444eeee4444eeee4444eeee4444eeee4444"

	withAnchor := BuildGitSyncInstructions(info, "matea-hub-42")
	assert.Contains(t, withAnchor, "git checkout -b matea/hub-42 eeee4444eeee4444eeee4444eeee4444eeee4444")
	assert.Contains(t, withAnchor, "CONTINUES prior session work")
	// F3: the start point is enforced, not merely suggested — the checkout is
	// followed by a self-check the hub cannot skip without failing loudly.
	assert.Contains(t, withAnchor, "git merge-base --is-ancestor eeee4444eeee4444eeee4444eeee4444eeee4444 HEAD")
	assert.Contains(t, withAnchor, "git clone --no-single-branch")
	assert.Contains(t, withAnchor, "--depth")

	info.AnchorHEAD = ""
	without := BuildGitSyncInstructions(info, "matea-hub-42")
	assert.Contains(t, without, "git checkout -b matea/hub-42 &&")
	assert.Contains(t, without, "git merge-base --is-ancestor aaaa0000 HEAD", "fresh sessions self-check against the base head")
	assert.NotContains(t, without, "CONTINUES prior session work")
	assert.NotContains(t, without, "eeee4444")
}

// --- shared memory renderer -------------------------------------------------

func TestBuildMemoryContext(t *testing.T) {
	assert.Equal(t, "", BuildMemoryContext(nil))
	assert.Equal(t, "", BuildMemoryContext(&TaskContext{}))

	tc := &TaskContext{MemoryKeys: map[string]string{
		"review_summary":   "looks ok",
		"analysis_summary": "root cause found",
	}}
	got := BuildMemoryContext(tc)
	assert.Contains(t, got, "## Previously remembered context (repo/issue memory)")
	assert.Less(t, strings.Index(got, "analysis_summary"), strings.Index(got, "review_summary"),
		"keys render sorted for deterministic prompt bytes")
	assert.NotContains(t, got, "Session continuation")

	tc.SessionMemory = "task 41: pushed matea/hub-41"
	got = BuildMemoryContext(tc)
	assert.Contains(t, got, "## Session continuation memory")
	assert.Contains(t, got, "task 41: pushed matea/hub-41")
	assert.Less(t, strings.Index(got, "Session continuation memory"),
		strings.Index(got, "repo/issue memory"), "session block renders before repo/issue keys")
}

// --- real-git Approve: continuation across base movement --------------------

// continuationApproveRemote builds the two-task continuation scenario on a
// real remote: main@A, session draft branch (task 1) off A with head X, then
// main advances to B. Task 2's draft is pushed by the test (simulating the
// hub) branching from startPoint. Returns everything Approve needs.
func continuationApproveRemote(t *testing.T, task1ID, task2ID int64, startFromAnchor bool) (remote, baseB, anchorX, draft2HEAD string) {
	t.Helper()
	remote, work, mainA, run := initGitSyncBase(t)

	// Task 1 (previous session task): draft branch off main@A, signed.
	run(work, "checkout", "-q", "-b", DraftBranchName(task1ID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "part1.go"), []byte("package p1\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: session task 1", "-m", RequiredFooter(task1ID))
	run(work, "push", "-q", "origin", DraftBranchName(task1ID))
	anchorX = strings.TrimSpace(run(work, "rev-parse", "HEAD"))

	// Main moves on (another PR merged) BEFORE task 2 is prepared.
	run(work, "checkout", "-q", "main")
	require.NoError(t, os.WriteFile(filepath.Join(work, "other.go"), []byte("package other\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "unrelated merge")
	run(work, "push", "-q", "origin", "main")
	baseB = strings.TrimSpace(run(work, "rev-parse", "HEAD"))
	require.NotEqual(t, mainA, baseB)

	// Task 2 (continuation): branch from the anchor (honest hub) or from the
	// new base tip (contract violator).
	start := anchorX
	if !startFromAnchor {
		start = baseB
	}
	run(work, "checkout", "-q", "-b", DraftBranchName(task2ID), start)
	require.NoError(t, os.WriteFile(filepath.Join(work, "part2.go"), []byte("package p2\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: session task 2", "-m", RequiredFooter(task2ID))
	run(work, "push", "-q", "origin", DraftBranchName(task2ID))
	draft2HEAD = strings.TrimSpace(run(work, "rev-parse", "HEAD"))
	return remote, baseB, anchorX, draft2HEAD
}

func TestGitSyncApproveContinuationAcrossBaseMovement(t *testing.T) {
	task1ID, task2ID := int64(9401), int64(9402)
	remote, baseB, anchorX, draft2HEAD := continuationApproveRemote(t, task1ID, task2ID, true)
	transport, fake, _ := newApproveTransport(t, remote, baseB)

	// Prepare for task 2 sees base@B; the session anchor is task 1's head.
	info := &GitSyncInfo{
		DraftBranch:    DraftBranchName(task2ID),
		BaseBranch:     "main",
		BaseHEAD:       baseB,
		AnchorHEAD:     anchorX,
		RequiredFooter: RequiredFooter(task2ID),
		HubPush:        true,
	}
	// The hub reported no head (trailer missing) — Approve must still pass and
	// must normalize the result head to the fetched authoritative value.
	result := &GitSyncResult{DraftBranch: info.DraftBranch}
	res, err := transport.Approve(context.Background(), gitSyncApproveTask(task2ID), &store.Agent{}, "o", "r",
		info, result, "continued")
	require.NoError(t, err, "continuation across base movement must pass: ancestor+footer range anchor on LastHead, drift window is this task only")
	assert.Equal(t, 77, res.PRID)
	assert.Equal(t, draft2HEAD, result.DraftHEAD, "Approve normalizes the reported head to the fetched one")
	require.NotNil(t, fake.prCreated)
	assert.Equal(t, info.DraftBranch, fake.prCreated.Head)
}

func TestGitSyncApproveContinuationRejectsBaseTipStart(t *testing.T) {
	task1ID, task2ID := int64(9411), int64(9412)
	// Hub ignored the anchor and branched from the new base tip instead.
	remote, baseB, anchorX, _ := continuationApproveRemote(t, task1ID, task2ID, false)
	transport, fake, _ := newApproveTransport(t, remote, baseB)

	info := &GitSyncInfo{
		DraftBranch:    DraftBranchName(task2ID),
		BaseBranch:     "main",
		BaseHEAD:       baseB,
		AnchorHEAD:     anchorX,
		RequiredFooter: RequiredFooter(task2ID),
		HubPush:        true,
	}
	_, err := transport.Approve(context.Background(), gitSyncApproveTask(task2ID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Nil(t, fake.prCreated)
}

// --- runViaHub continuation wiring ------------------------------------------

func TestRunViaHubGitSyncContinuationEndToEnd(t *testing.T) {
	task1ID, task2ID := int64(9501), int64(9502)
	remote, baseB, anchorX, draft2HEAD := continuationApproveRemote(t, task1ID, task2ID, true)
	fake := newGitSyncFakeGitea(t, remote, baseB)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	// Session state left behind by task 1.
	continuationSession(t, db, "sess-hub-cont", DraftBranchName(task1ID), anchorX)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "continued the fix"}}
	task := gitSyncApproveTask(task2ID)
	task.SessionID = "sess-hub-cont"
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: task2ID}

	res, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	assert.Equal(t, 77, res.PRID)

	// The hub received the continuation anchor in its git_sync contract.
	require.NotNil(t, hub.gotTC)
	require.NotNil(t, hub.gotTC.GitSync)
	assert.Equal(t, anchorX, hub.gotTC.GitSync.AnchorHEAD, "hub must branch from the session LastHead")
	assert.Equal(t, baseB, hub.gotTC.GitSync.BaseHEAD)

	// The anchor was persisted on the handle row for restart re-attach.
	handle, err := db.GetHubHandle(task2ID)
	require.NoError(t, err)
	require.NotNil(t, handle)
	assert.Equal(t, anchorX, handle.AnchorHEAD)
	assert.Equal(t, baseB, handle.BaseHEAD)

	// Session continuation state advanced to task 2's pushed head.
	session, err := db.GetSession("sess-hub-cont")
	require.NoError(t, err)
	assert.Equal(t, DraftBranchName(task2ID), session.Branch)
	assert.Equal(t, draft2HEAD, session.LastHead, "LastHead = authoritative fetched head, ready for the next continuation")
	assert.Contains(t, session.Memory, "continued the fix", "rolling session summary recorded for prompt injection")
	assert.Contains(t, session.Memory, "task 9502")

	// Key lifecycle unchanged.
	assert.Equal(t, []int64{1}, issuer.revoked)
}

func TestRunViaHubGitSyncFreshSessionHasNoAnchor(t *testing.T) {
	taskID := int64(9601)
	cloneURL, mainHEAD, _ := setupGitSyncRemote(t, taskID)
	fake := newGitSyncFakeGitea(t, cloneURL, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, cloneURL, issuer)

	// Session exists but has no continuation state yet.
	continuationSession(t, db, "sess-hub-fresh", "", "")

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "done"}}
	task := gitSyncApproveTask(taskID)
	task.SessionID = "sess-hub-fresh"
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{}, hub, tc)
	require.NoError(t, err)
	require.NotNil(t, hub.gotTC.GitSync)
	assert.Empty(t, hub.gotTC.GitSync.AnchorHEAD, "fresh session branches from the base tip")
}

// --- prompt injection: buildHubWriteTaskContext -----------------------------

func TestBuildHubWriteTaskContextInjectsMemory(t *testing.T) {
	db := newHubRunTestDB(t)
	require.NoError(t, db.SetMemory("o/r", 12, AnalyzeMemoryKey, "root cause is X"))
	continuationSession(t, db, "sess-mem", "matea/hub-1", "aaaa")
	sess, err := db.GetSession("sess-mem")
	require.NoError(t, err)
	sess.Memory = "task 1: did part one"
	require.NoError(t, db.UpdateSession(sess))

	f := &RunnerFactory{db: db}
	task := &store.Task{ID: 9701, Repo: "o/r", IssueID: 12, TaskType: "solve_issue", Event: "title", Context: "body", SessionID: "sess-mem"}
	tc := buildHubWriteTaskContext(f, task, &store.Agent{}, "gs-opencode", "dev")

	assert.Equal(t, "root cause is X", tc.MemoryKeys[AnalyzeMemoryKey], "memories table reaches the hub write prompt (B2.3)")
	assert.Equal(t, "task 1: did part one", tc.SessionMemory)

	// No session → no session memory, repo/issue memory still flows.
	task2 := &store.Task{ID: 9702, Repo: "o/r", IssueID: 12, TaskType: "solve_issue", Event: "t", Context: "b"}
	tc2 := buildHubWriteTaskContext(f, task2, &store.Agent{}, "gs-opencode", "dev")
	assert.Equal(t, "root cause is X", tc2.MemoryKeys[AnalyzeMemoryKey])
	assert.Empty(t, tc2.SessionMemory)
}

// --- prompt injection: OpenCode Submit --------------------------------------

func TestOpenCodeSubmitInjectsMemoryContext(t *testing.T) {
	var capturedBody string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodPost {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				raw, _ := json.Marshal(body)
				capturedBody = string(raw)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant", "content": "ok"})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
				json.NewEncoder(w).Encode([]any{map[string]any{
					"info":  map[string]any{"id": "msg-2", "role": "assistant"},
					"parts": []any{map[string]any{"type": "text", "text": "Done."}},
				}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	b := newTestBackend(t, srv.URL)

	_, err := b.Submit(context.Background(), &TaskContext{
		TaskType:      "analyze_issue",
		Repo:          "o/r",
		IssueID:       3,
		TaskID:        9801,
		UserPrompt:    "Analyze it",
		SandboxPath:   t.TempDir(),
		MemoryKeys:    map[string]string{AnalyzeMemoryKey: "prior analysis"},
		SessionMemory: "task 0: explored the repo",
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedBody)
	assert.Contains(t, capturedBody, "prior analysis", "OpenCode must receive repo/issue memory (previously dropped)")
	assert.Contains(t, capturedBody, "task 0: explored the repo", "session memory reaches OpenCode")
	assert.Contains(t, capturedBody, "Session continuation memory")
}

// A git_sync write task must keep the memory block AND receive the mandatory
// git workflow, with memory first (recency: the workflow stays last). Before
// the 2026-08-29 fix the git branch rebuilt the prompt from tc.UserPrompt and
// silently dropped memory — task 16's hub never learned what task 14 had done.
func TestOpenCodeSubmitKeepsMemoryBeforeGitSyncContract(t *testing.T) {
	var capturedBody string
	srv := newTestOpenCodeServer(t, map[string]http.HandlerFunc{
		"/session/": func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodPost {
				var body map[string]any
				json.NewDecoder(r.Body).Decode(&body)
				raw, _ := json.Marshal(body)
				capturedBody = string(raw)
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(map[string]any{"id": "msg-1", "role": "assistant", "content": "ok"})
				return
			}
			if strings.HasSuffix(r.URL.Path, "/message") && r.Method == http.MethodGet {
				json.NewEncoder(w).Encode([]any{map[string]any{
					"info":  map[string]any{"id": "msg-2", "role": "assistant"},
					"parts": []any{map[string]any{"type": "text", "text": "Done."}},
				}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
		},
	})
	b := newTestBackend(t, srv.URL)

	_, err := b.Submit(context.Background(), &TaskContext{
		TaskType:      "solve_issue",
		Repo:          "o/r",
		IssueID:       3,
		TaskID:        9802,
		UserPrompt:    "Continue the work",
		MemoryKeys:    map[string]string{AnalyzeMemoryKey: "prior analysis"},
		SessionMemory: "task 14: pushed matea/hub-14",
		GitSync: &GitSyncInfo{
			CloneURL:       "ssh://git@example.com/o/r.git",
			PrivateKey:     "PRIVKEY",
			DraftBranch:    DraftBranchName(9802),
			BaseBranch:     "main",
			BaseHEAD:       "aaaa0000",
			AnchorHEAD:     "eeee4444",
			CommitAuthor:   "Matea Hub <hub@matea.local>",
			RequiredFooter: RequiredFooter(9802),
			HubPush:        true,
		},
	})
	require.NoError(t, err)
	require.NotEmpty(t, capturedBody)
	assert.Contains(t, capturedBody, "Session continuation memory")
	assert.Contains(t, capturedBody, "Git workflow (MANDATORY")
	assert.Less(t, strings.Index(capturedBody, "Session continuation memory"),
		strings.Index(capturedBody, "Git workflow (MANDATORY"),
		"memory must survive the git_sync append and stay before the workflow")
	assert.Contains(t, capturedBody, "eeee4444", "the continuation anchor reaches the hub")
}

// saveSessionMemory / saveSessionProgress guard rails.
func TestSaveSessionMemoryAndProgressGuards(t *testing.T) {
	db := newHubRunTestDB(t)
	continuationSession(t, db, "sess-guard", "", "")
	f := &RunnerFactory{db: db}

	// Empty head never clobbers a recorded anchor.
	saveSessionProgress(f, &store.Task{ID: 1, SessionID: "sess-guard"}, "matea/hub-1", "aaaa")
	saveSessionProgress(f, &store.Task{ID: 2, SessionID: "sess-guard"}, "matea/hub-2", "")
	sess, err := db.GetSession("sess-guard")
	require.NoError(t, err)
	assert.Equal(t, "matea/hub-1", sess.Branch)
	assert.Equal(t, "aaaa", sess.LastHead)

	// Memory: latest-wins, bounded, prefixed with the task id.
	saveSessionMemory(f, &store.Task{ID: 3, SessionID: "sess-guard"}, "first outcome")
	saveSessionMemory(f, &store.Task{ID: 4, SessionID: "sess-guard"}, strings.Repeat("x", sessionMemoryMaxLen+100))
	sess, err = db.GetSession("sess-guard")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sess.Memory, "task 4: "))
	assert.LessOrEqual(t, len([]rune(sess.Memory)), sessionMemoryMaxLen)

	// Empty summary / no session are no-ops.
	saveSessionMemory(f, &store.Task{ID: 5, SessionID: "sess-guard"}, "")
	saveSessionMemory(f, &store.Task{ID: 6}, "x")
	sess, err = db.GetSession("sess-guard")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(sess.Memory, "task 4: "))
}
