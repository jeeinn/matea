package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/store"
)

// --- ReviewRunner ---

// ReviewRunner handles PR review tasks.
type ReviewRunner struct {
	factory *RunnerFactory
}

// NewReviewRunner creates a new ReviewRunner.
func NewReviewRunner(factory *RunnerFactory) *ReviewRunner {
	return &ReviewRunner{factory: factory}
}

// Run executes the review task with an independent Checker context (no coder
// conversation history — only PR metadata + diff).
func (r *ReviewRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
	// Hub dispatch branch (1.2.4): reserved hub-* / unknown backends fail
	// loudly. Review stays on the builtin LLM by design (forced builtin).
	if err := r.factory.validateHubDispatch(agent); err != nil {
		return nil, err
	}

	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	prID := task.PRID
	if prID == 0 {
		prID = task.IssueID
		log.Printf("[WARN] Task %d has no PRID, falling back to IssueID=%d for PR API calls", task.ID, prID)
	}

	// Hub execution branch (task 2.2.2, D7 second cut): when the agent's
	// backend is a hub-opencode instance, route the review through OpenCode with
	// a prepared workspace (shallow clone of the PR head branch). OpenCode
	// operates the files itself via shared-path directory binding; the sandbox
	// is cleaned up after. Workspace prep failure falls back to single-shot LLM.
	if hb, ok := r.factory.ResolveHubOpenCode(agent); ok {
		wwc, err := prepareReviewWorkspace(ctx, task, agent, r.factory)
		if err != nil {
			// Workspace prep failed — fall back to single-shot LLM. This needs
			// the provider, so fetch it only here (opencode itself never does).
			log.Printf("[WARN] Task %d: hub-opencode workspace prep failed (%v); falling back to single-shot", task.ID, err)
			provider, getErr := r.factory.llmRegistry.Get(agent.Provider)
			if getErr != nil {
				return nil, fmt.Errorf("get provider: %w", getErr)
			}
			return r.runSingleShotReview(ctx, task, agent, provider)
		}
		defer wwc.Sandbox.Cleanup()

		userPrompt := task.Context
		if strings.TrimSpace(userPrompt) == "" {
			userPrompt = "Please review this pull request using the code in the working directory."
		}

		res, err := r.factory.runViaHub(ctx, task, agent, hb, &TaskContext{
			TaskType:     task.TaskType,
			Role:         "review",
			Backend:      hb.Name(),
			Repo:         task.Repo,
			IssueID:      task.IssueID,
			PRID:         prID,
			IssueTitle:   task.Event,
			IssueBody:    task.Context,
			SystemPrompt: agent.SystemPrompt,
			UserPrompt:   userPrompt,
			SandboxPath:  wwc.Sandbox.WorkDir,
			MemoryKeys:   r.factory.loadMemoryKeys(task),
		})
		if err != nil {
			return nil, err
		}
		r.factory.saveReviewMemory(task, res.Content)
		return res, nil
	}

	client := r.factory.giteaFactory.GetGiteaClient(agent.GiteaToken)

	diff, err := client.PRDiff(owner, repo, prID)
	if err != nil {
		return nil, fmt.Errorf("get PR diff: %w", err)
	}

	pr, err := client.PRGet(owner, repo, prID)
	if err != nil {
		return nil, fmt.Errorf("get PR: %w", err)
	}

	files, err := client.PRFiles(owner, repo, prID)
	if err != nil {
		log.Printf("[WARN] Failed to get PR files: %v", err)
	}

	var fileList strings.Builder
	for _, f := range files {
		fileList.WriteString(fmt.Sprintf("- %s (+%d/-%d)\n", f.Filename, f.Additions, f.Deletions))
	}

	prTitle, _ := pr["title"].(string)
	prBody, _ := pr["body"].(string)

	basePrompt := agentpkg.BuildReviewPrompt(agentpkg.ReviewPromptInput{
		Repo:         task.Repo,
		PRNumber:     prID,
		PRTitle:      prTitle,
		PRBody:       prBody,
		ChangedFiles: fileList.String(),
		Diff:         diff,
	})
	systemPrompt := agentpkg.MergeAgentSystemPrompt(basePrompt, agent.SystemPrompt)

	userContent := task.Context
	if strings.TrimSpace(userContent) == "" {
		userContent = "Please review this pull request using the criteria in the system prompt."
	}

	// Hub execution branch (task 2.1.3): when the agent's backend is a
	// hub-hermes instance, route the review through Hermes. Matea still
	// pre-fetches the PR diff/metadata above (the hub must not call Gitea
	// directly) and passes it via the TaskContext.
	if hb, ok := r.factory.ResolveHubExecution(agent); ok {
		return r.factory.runViaHub(ctx, task, agent, hb, &TaskContext{
			TaskType:     task.TaskType,
			Role:         "review",
			Backend:      hb.Name(),
			Repo:         task.Repo,
			IssueID:      task.IssueID,
			PRID:         prID,
			IssueTitle:   prTitle,
			IssueBody:    prBody,
			Diff:         diff,
			SystemPrompt: systemPrompt,
			UserPrompt:   userContent,
			MemoryKeys:   r.factory.loadMemoryKeys(task),
		})
	}

	provider, err := r.factory.llmRegistry.Get(agent.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	messages, err = agentpkg.TruncateMessages(messages, nil, r.factory.resolveMaxInputTokens(agent.MaxInputTokens, agent.Provider, agent.Model), r.factory.getModelMeta(agent.Provider, agent.Model))
	if err != nil {
		return nil, fmt.Errorf("truncate messages: %w", err)
	}

	req := &llm.ChatRequest{
		Model:     agent.Model,
		Messages:  messages,
		MaxTokens: r.factory.resolveMaxOutputTokens(agent.MaxOutputTokens, agent.Provider, agent.Model),
	}
	r.factory.resolveSamplingParams(agent.Temperature, agent.Provider, agent.Model).ApplyTo(req)
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}

	log.Printf("[INFO] Task %d LLM response: %d tokens used", task.ID, resp.Usage.TotalTokens)
	r.factory.recordTaskUsage(task.ID, agent.Provider, agent.Model, resp.Usage)

	return &Result{
		Content: resp.Content,
		Action:  "comment",
	}, nil
}

// runSingleShotReview is the fallback single-shot LLM review used when the
// OpenCode workspace cannot be prepared (e.g. Gitea unreachable). It reviews
// from the task text only — no diff is available — so the conclusion is
// necessarily shallow. Mirrors AnalyzeRunner.runSingleShot.
func (r *ReviewRunner) runSingleShotReview(ctx context.Context, task *store.Task, agent *store.Agent, provider llm.Provider) (*Result, error) {
	systemPrompt := agent.SystemPrompt
	if systemPrompt != "" {
		systemPrompt += "\n\n"
	}
	systemPrompt += "## Workspace\n\nNo local repository workspace or PR diff is available for this review (clone unavailable). Base your review on the task text only.\n"

	userPrompt := task.Context
	if strings.TrimSpace(userPrompt) == "" {
		userPrompt = "Please review this pull request."
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	messages, err := agentpkg.TruncateMessages(messages, nil, r.factory.resolveMaxInputTokens(agent.MaxInputTokens, agent.Provider, agent.Model), r.factory.getModelMeta(agent.Provider, agent.Model))
	if err != nil {
		return nil, fmt.Errorf("truncate messages: %w", err)
	}

	req := &llm.ChatRequest{
		Model:     agent.Model,
		Messages:  messages,
		MaxTokens: r.factory.resolveMaxOutputTokens(agent.MaxOutputTokens, agent.Provider, agent.Model),
	}
	r.factory.resolveSamplingParams(agent.Temperature, agent.Provider, agent.Model).ApplyTo(req)
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("LLM call: %w", err)
	}

	log.Printf("[INFO] Task %d LLM response: %d tokens used", task.ID, resp.Usage.TotalTokens)
	r.factory.recordTaskUsage(task.ID, agent.Provider, agent.Model, resp.Usage)

	return &Result{
		Content: resp.Content,
		Action:  "comment",
	}, nil
}
