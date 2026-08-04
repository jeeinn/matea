package agents

import (
	"context"
	"fmt"
	"log"
	"time"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/gitea"
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

	codingResult, err := backend.Run(ctx, codingReq)
	if err != nil {
		return nil, fmt.Errorf("coding backend %s: %w", backend.Name(), err)
	}
	if !codingResult.Success {
		return nil, fmt.Errorf("coding backend %s reported failure: %s", backend.Name(), codingResult.Summary)
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
			return nil, err
		}
	}
	if err := runHarnessVerify(sb, mergedLoop.VerifyCommands); err != nil {
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

	return finalizeWriteChanges(ctx, wwc, task, agentCfg, factory, provider, taskSubType, codingResult.Summary)
}

// finalizeWriteTaskPR comments on an existing open PR or creates one if the branch has no open PR yet.
func finalizeWriteTaskPR(adminClient *gitea.Client, owner, repo, branchName, baseBranch string, task *store.Task, taskSubType, agentResult string) (*Result, error) {
	existingPR, err := adminClient.FindOpenPRByHead(owner, repo, branchName)
	if err != nil {
		return nil, fmt.Errorf("find open PR: %w", err)
	}
	if existingPR != nil {
		log.Printf("[INFO] Task %d updated existing branch: %s (PR #%d)", task.ID, branchName, existingPR.Number)
		return &Result{
			Content: fmt.Sprintf("🔄 Updated PR branch `%s` with new changes.\n\n%s", branchName, agentResult),
			Action:  "comment",
			PRID:    existingPR.Number,
		}, nil
	}

	prTitle := fmt.Sprintf("AI Solution: %s", task.Event)
	if taskSubType == "bugfix" {
		prTitle = fmt.Sprintf("Bugfix: %s", task.Event)
	}
	contentPreview := agentResult
	if len(contentPreview) > 500 {
		contentPreview = contentPreview[:500] + "..."
	}
	issueLink := ""
	if task.IssueID > 0 {
		issueLink = fmt.Sprintf("\n\nFixes #%d", task.IssueID)
	}
	prBody := fmt.Sprintf("## AI Generated Solution\n\n%s\n\n---\n*Task ID: %d*%s", contentPreview, task.ID, issueLink)

	pr, err := adminClient.CreatePR(owner, repo, gitea.CreatePRRequest{
		Title: prTitle,
		Body:  prBody,
		Head:  branchName,
		Base:  baseBranch,
	})
	if err != nil {
		return nil, fmt.Errorf("create PR: %w", err)
	}

	log.Printf("[INFO] Task %d PR created: %s (PR #%d)", task.ID, pr.HTMLURL, pr.Number)
	return &Result{
		Content: fmt.Sprintf("✅ PR created: %s\n\n%s", pr.HTMLURL, agentResult),
		Action:  "pr",
		PRID:    pr.Number,
	}, nil
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
