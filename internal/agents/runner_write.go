package agents

import (
	"context"
	"fmt"
	"log"
	"time"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/store"
)

// --- DevRunner ---

// DevRunner handles development tasks (read issue → write code → create PR).
type DevRunner struct {
	factory *RunnerFactory
}

// NewDevRunner creates a new DevRunner.
func NewDevRunner(factory *RunnerFactory) *DevRunner {
	return &DevRunner{factory: factory}
}

// Run executes the development task.
func (r *DevRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
	return runWriteTask(ctx, task, agent, r.factory, "dev")
}

// --- BugfixRunner ---

// BugfixRunner handles bug fix tasks (read bug → locate → fix → create PR).
type BugfixRunner struct {
	factory *RunnerFactory
}

// NewBugfixRunner creates a new BugfixRunner.
func NewBugfixRunner(factory *RunnerFactory) *BugfixRunner {
	return &BugfixRunner{factory: factory}
}

// Run executes the bugfix task.
func (r *BugfixRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
	return runWriteTask(ctx, task, agent, r.factory, "bugfix")
}

// runWriteTask is the shared implementation for write-type runners.
//
// Structure (after A3):
//
//	prepareWriteWorkspace → CodingBackend.Run → finalizeWriteChanges
//
// The coding backend is resolved from agent.Backend (or agents.backends.default).
// Non-write runners (Analyze/Review/Reply) never call this function — they
// always use the builtin LLM loop directly, which matches the "Analyze forced
// builtin" constraint from server-runtime-design-v4.md §3.2.
func runWriteTask(ctx context.Context, task *store.Task, agentCfg *store.Agent,
	factory *RunnerFactory, taskSubType string) (*Result, error) {

	// Phase 0: resolve backend + optional health probe BEFORE preparing the
	// workspace. Sidecar-down must not leave session clones / branches behind.
	//
	// Hub dispatch: validateHubDispatch rejects reserved hub-* names without an
	// implementation and unknown backends loudly. Hub backends with
	// workspace_transport=git_sync (hub-opencode A4, hub-hermes B1) dispatch
	// through HubBackend Submit/Poll with Handle persistence (1.2.1) via
	// runViaHub below; builtin and shared_path hub-opencode continue through
	// the CodingBackend path.
	if err := factory.validateHubDispatch(agentCfg); err != nil {
		return nil, err
	}

	// git_sync write path (tasks A4/B1): a hub backend (hub-opencode or
	// hub-hermes) configured with workspace_transport=git_sync runs through
	// runViaHub's write channel — Matea Prepares credentials and later Approves
	// the hub-pushed draft branch; NO local workspace is prepared (the hub
	// clones itself), and the CodingBackend.Run / harness-verify /
	// finalizeWriteChanges stages below are all skipped by design. shared_path
	// backends continue unchanged.
	//
	// This branch must precede ResolveCodingBackend: that resolver only knows
	// builtin/hub-opencode and hard-errors on hub-hermes, which would otherwise
	// never reach the git_sync channel.
	if hb, isHub := factory.resolveGitSyncWriteHub(agentCfg); isHub {
		// Fail fast when the hub is down, before runViaHub's Prepare issues a
		// task-scoped deploy key for a run that cannot start. allow_fallback_
		// builtin is deliberately NOT honored here: silently switching to the
		// builtin path under git_sync would replace the hub-push trust model
		// (task-scoped deploy key) with a Matea-side push using the agent's own
		// token — a privilege widening that must never happen silently.
		if hc, ok := hb.(HealthCheckableBackend); ok {
			hcCtx, hcCancel := context.WithTimeout(ctx, 5*time.Second)
			hcErr := hc.HealthCheck(hcCtx)
			hcCancel()
			if hcErr != nil {
				return nil, fmt.Errorf(
					"hub backend %q is not reachable (health check failed): %w",
					hb.Name(), hcErr,
				)
			}
		}
		tc := buildHubWriteTaskContext(task, agentCfg, hb.Name(), taskSubType)
		return factory.runViaHub(ctx, task, agentCfg, hb, tc)
	}

	backend, err := factory.ResolveCodingBackend(agentCfg)
	if err != nil {
		return nil, fmt.Errorf("resolve coding backend: %w", err)
	}
	log.Printf("[INFO] Task %d using coding backend: %s", task.ID, backend.Name())

	if hc, ok := backend.(HealthCheckableBackend); ok {
		hcCtx, hcCancel := context.WithTimeout(ctx, 5*time.Second)
		hcErr := hc.HealthCheck(hcCtx)
		hcCancel()
		if hcErr != nil {
			if allowsBuiltinFallback(backend) {
				log.Printf("[WARN] Task %d coding backend %s unhealthy (%v); allow_fallback_builtin=true → switching to builtin",
					task.ID, backend.Name(), hcErr)
				backend = factory.builtinBackend
			} else {
				// Return error so Executor marks failed (not success) and posts
				// a failure comment via writeFailureToGitea.
				return nil, fmt.Errorf(
					"coding backend %q is not reachable (health check failed): %w",
					backend.Name(), hcErr,
				)
			}
		}
	}

	// Phase 1: prepare workspace (sandbox / clone / branch)
	wwc, err := prepareWriteWorkspace(ctx, task, agentCfg, factory, taskSubType)
	if err != nil {
		return nil, err
	}
	// Only cleanup for non-session workspaces (session workspaces persist)
	if !wwc.UseSession {
		defer wwc.Sandbox.Cleanup()
	}

	sb := wwc.Sandbox

	// Phase 2: coding
	// Build prompts (shared by all backends)
	maxInput := factory.resolveMaxInputTokens(agentCfg.MaxInputTokens, agentCfg.Provider, agentCfg.Model)

	// Load code context for the prompt (best-effort; warn on failure)
	codeCtx, err := agentpkg.LoadCodeContext(sb, maxInput)
	if err != nil {
		log.Printf("[WARN] Failed to load code context: %v", err)
	}

	taskCtx := agentpkg.TaskContext{
		IssueTitle: task.Event,
		IssueBody:  task.Context,
		RepoName:   task.Repo,
		TaskType:   taskSubType,
	}

	var basePrompt string
	if taskSubType == "dev" {
		basePrompt = agentpkg.BuildDevPrompt(taskCtx, codeCtx)
	} else {
		basePrompt = agentpkg.BuildBugfixPrompt(taskCtx, codeCtx)
	}
	systemPrompt := agentpkg.MergeAgentSystemPrompt(basePrompt, agentCfg.SystemPrompt)
	systemPrompt += fmt.Sprintf("\n\n## Workspace\n\nYour working directory is `%s`. All file paths are relative to this directory; do not guess or use absolute paths like /workspace.\n", sb.WorkDir)

	codingReq := CodingRequest{
		WorkDir:        sb.WorkDir,
		Sandbox:        sb,
		Task:           task,
		Agent:          agentCfg,
		TaskSubType:    taskSubType,
		Prompt:         task.Context,
		SystemPrompt:   systemPrompt,
		SessionID:      task.SessionID,
		Continue:       task.SessionID != "",
		BackendOptions: agentCfg.BackendOptions,
		ToolPack:       factory.resolveToolPack(task.TaskType),
	}

	// hub-opencode write tasks are dispatched through the CodingBackend.Run
	// path (NOT runViaHub), so they need explicit Handle persistence + restart
	// re-attach to stay consistent with the read/reply hub paths (Phase 2 code
	// review Problem B: write-task dual-track). builtin and other backends are
	// untouched; hb is only referenced in the hub-opencode branch below.
	hb, isHubOpenCode := factory.ResolveHubOpenCode(agentCfg)

	// Idempotency / restart re-attach for hub-opencode write tasks. If a
	// non-terminal Handle was persisted for this task (from a prior run that
	// was interrupted or re-enqueued), recover the coding summary from the
	// still-living opencode sidecar session instead of starting a second
	// session — this closes the duplicate-sidecar-session hole that the
	// synchronous CodingBackend.Run path otherwise leaves open.
	var codingResult *CodingResult
	var opencodeHandle *store.HubHandle
	if isHubOpenCode && factory.db != nil {
		if existing, gerr := factory.db.GetHubHandle(task.ID); gerr == nil && existing != nil && !store.IsTerminalHubStatus(existing.Status) {
			h := &Handle{Backend: existing.Backend, RemoteID: existing.RemoteID, IdempotencyKey: existing.IdempotencyKey}
			res, state, perr := hb.Poll(ctx, h)
			if perr != nil || !state.IsTerminal() {
				// Sidecar session is gone (GC'd/restarted) — cannot recover;
				// mark failed rather than spin up a duplicate coding run.
				factory.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
				return nil, fmt.Errorf("hub-opencode write task %d: sidecar session %q no longer recoverable: %w", task.ID, existing.RemoteID, perr)
			}
			// For task-level (non-session) workspaces the sandbox is re-cloned
			// fresh on re-entry, so the sidecar's on-disk changes are not
			// present and committing would emit an empty PR. Fail cleanly so an
			// operator can retry; session workspaces reuse the existing dir and
			// do recover below.
			if task.SessionID == "" {
				factory.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
				return nil, fmt.Errorf("hub-opencode write task %d: interrupted non-session workspace would drop sidecar changes; marked failed for manual retry", task.ID)
			}
			codingResult = &CodingResult{Summary: res.Summary, Success: true, RemoteSessionID: existing.RemoteID}
			opencodeHandle = existing
			log.Printf("[INFO] write task %d re-attached to hub-opencode session %q", task.ID, existing.RemoteID)
		}
	}

	if codingResult == nil {
		cr, rerr := backend.Run(ctx, codingReq)
		if rerr != nil {
			return nil, fmt.Errorf("coding backend %s: %w", backend.Name(), rerr)
		}
		if !cr.Success {
			return nil, fmt.Errorf("coding backend %s reported failure: %s", backend.Name(), cr.Summary)
		}
		codingResult = cr
		// Persist a Handle so a crash during finalize is recoverable and
		// re-enqueues (stale scanner / restart) hit the idempotency guard above
		// instead of starting a duplicate sidecar session.
		if isHubOpenCode && factory.db != nil {
			if serr := factory.db.SaveHubHandle(&store.HubHandle{
				TaskID:         task.ID,
				Backend:        backend.Name(),
				RemoteID:       codingResult.RemoteSessionID,
				IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", task.TaskType, task.Repo, task.IssueID, task.PRID),
				Status:         store.HubHandleStatusRunning,
			}); serr != nil {
				log.Printf("[WARN] write task %d: failed to persist hub-opencode handle: %v", task.ID, serr)
			} else {
				opencodeHandle = &store.HubHandle{TaskID: task.ID, Backend: backend.Name(), RemoteID: codingResult.RemoteSessionID}
			}
		}
	}

	// Harness: independent checker (fresh LLM context) then optional shell verify.
	mergedLoop := MergeLoopConfig(agentCfg.LoopConfig, factory.defaultLoop)
	provider := codingResult.Provider
	if provider == nil {
		provider, _ = factory.llmRegistry.Get(agentCfg.Provider)
	}
	if mergedLoop.IndependentChecker {
		sampling := factory.resolveSamplingParams(agentCfg.Temperature, agentCfg.Provider, agentCfg.Model)
		maxOut := factory.resolveMaxOutputTokens(agentCfg.MaxOutputTokens, agentCfg.Provider, agentCfg.Model)
		if err := runIndependentChecker(ctx, sb, provider, agentCfg.Model, sampling, maxOut,
			task.Event, task.Context, codingResult.Summary); err != nil {
			if opencodeHandle != nil {
				factory.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
			}
			return nil, err
		}
	}
	if err := runHarnessVerify(sb, mergedLoop.VerifyCommands); err != nil {
		if opencodeHandle != nil {
			factory.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
		}
		return nil, err
	}

	// Phase 3: finalize (commit / push / PR)
	//
	// For the builtin backend, codingResult.Provider is the LLM provider
	// used during coding, which we reuse for the commit message LLM call.
	// For opencode backend, Provider is nil (LLM runs server-side), so
	// finalize will look up the provider again from the registry — a minor
	// overhead but keeps the contract simple.
	if provider == nil {
		provider, _ = factory.llmRegistry.Get(agentCfg.Provider)
	}

	finalResult, ferr := finalizeWriteChanges(ctx, wwc, task, agentCfg, factory, provider, taskSubType, codingResult.Summary)
	if ferr != nil {
		if opencodeHandle != nil {
			factory.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
		}
		return nil, ferr
	}
	if opencodeHandle != nil {
		factory.markHubHandleTerminal(task.ID, store.HubHandleStatusDone)
	}
	factory.emitBuiltinDeliver(task, finalResult)
	return finalResult, nil
}

// buildHubWriteTaskContext assembles the TaskContext for a git_sync write task
// (task A4). Unlike the CodingBackend path there is no local sandbox, so no
// code context is loaded — the hub clones the repo itself and builds its own
// context. Prompts are the same Dev/Bugfix bases the builtin path uses.
func buildHubWriteTaskContext(task *store.Task, agentCfg *store.Agent, backendName, taskSubType string) *TaskContext {
	taskCtx := agentpkg.TaskContext{
		IssueTitle: task.Event,
		IssueBody:  task.Context,
		RepoName:   task.Repo,
		TaskType:   taskSubType,
	}
	var basePrompt string
	if taskSubType == "dev" {
		basePrompt = agentpkg.BuildDevPrompt(taskCtx, nil)
	} else {
		basePrompt = agentpkg.BuildBugfixPrompt(taskCtx, nil)
	}
	systemPrompt := agentpkg.MergeAgentSystemPrompt(basePrompt, agentCfg.SystemPrompt)
	return &TaskContext{
		TaskType:     task.TaskType,
		Role:         "coder",
		Backend:      backendName,
		Repo:         task.Repo,
		IssueID:      task.IssueID,
		PRID:         task.PRID,
		IssueTitle:   task.Event,
		IssueBody:    task.Context,
		BaseBranch:   task.BaseBranch,
		Provider:     agentCfg.Provider,
		Model:        agentCfg.Model,
		TaskID:       task.ID,
		SystemPrompt: systemPrompt,
		UserPrompt:   task.Context,
	}
}

// saveSessionBranch persists the working branch on the session for workspace reuse.
func saveSessionBranch(factory *RunnerFactory, task *store.Task, branchName string) {
	if task.SessionID == "" || factory.db == nil {
		return
	}
	session, err := factory.db.GetSession(task.SessionID)
	if err != nil {
		return
	}
	if session.Branch == branchName {
		return
	}
	session.Branch = branchName
	factory.db.UpdateSession(session)
}
