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

	provider, err := r.factory.llmRegistry.Get(agent.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	userContent := task.Context
	if strings.TrimSpace(userContent) == "" {
		userContent = "Please review this pull request using the criteria in the system prompt."
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
