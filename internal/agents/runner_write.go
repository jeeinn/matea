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

	// Phase 1: prepare workspace (sandbox / clone / branch)
	wwc, err := prepareWriteWorkspace(ctx, task, agentCfg, factory, taskSubType)
	if err != nil {
		return nil, err
	}
	// All write workspaces are task-level since B2.2 (session continuation is
	// anchored on LastHead, not an on-disk workspace) — always clean up.
	defer wwc.Sandbox.Cleanup()

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

	// Since A5, only the builtin backend reaches this point: hub backends are
	// either diverted into runViaHub's git_sync channel above or rejected by
	// ResolveCodingBackend. The hub-opencode CodingBackend.Run dual-track
	// (Handle persistence + re-attach around a synchronous Run) went away with
	// it — hub Handles are owned by runViaHub exclusively now.
	cr, rerr := backend.Run(ctx, codingReq)
	if rerr != nil {
		return nil, fmt.Errorf("coding backend %s: %w", backend.Name(), rerr)
	}
	if !cr.Success {
		return nil, fmt.Errorf("coding backend %s reported failure: %s", backend.Name(), cr.Summary)
	}
	codingResult := cr

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
			return nil, err
		}
	}
	if err := runHarnessVerify(sb, mergedLoop.VerifyCommands); err != nil {
		return nil, err
	}

	// Phase 3: finalize (commit / push / PR)
	//
	// codingResult.Provider is the LLM provider used during coding, which we
	// reuse for the commit message LLM call; when nil, finalize looks the
	// provider up again from the registry — a minor overhead but keeps the
	// contract simple.
	if provider == nil {
		provider, _ = factory.llmRegistry.Get(agentCfg.Provider)
	}

	finalResult, ferr := finalizeWriteChanges(ctx, wwc, task, agentCfg, factory, provider, taskSubType, codingResult.Summary)
	if ferr != nil {
		return nil, ferr
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

// saveSessionBranch persists the working branch on the session for continuation.
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

// saveSessionProgress records the session's continuation state after a
// successful push (B2.2): the working branch plus its head SHA (LastHead),
// which the next continuation task anchors its fresh clone on.
func saveSessionProgress(factory *RunnerFactory, task *store.Task, branchName, headSHA string) {
	if task.SessionID == "" || factory.db == nil {
		return
	}
	session, err := factory.db.GetSession(task.SessionID)
	if err != nil {
		return
	}
	if session.Branch == branchName && session.LastHead == headSHA {
		return
	}
	session.Branch = branchName
	session.LastHead = headSHA
	factory.db.UpdateSession(session)
}
