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

// --- InteractionRunner ---

// InteractionRunner handles @Mention reply tasks.
type InteractionRunner struct {
	factory *RunnerFactory
}

// NewInteractionRunner creates a new InteractionRunner.
func NewInteractionRunner(factory *RunnerFactory) *InteractionRunner {
	return &InteractionRunner{factory: factory}
}

// Run executes the interaction task.
func (r *InteractionRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
	// Hub dispatch branch (1.2.4): reserved hub-* / unknown backends fail
	// loudly. Reply stays on the builtin LLM in Phase 1.
	if err := r.factory.validateHubDispatch(agent); err != nil {
		return nil, err
	}

	// Parse repo owner/name
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	// Get Gitea client
	client := r.factory.giteaFactory.GetGiteaClient(agent.GiteaToken)

	// Get comment history for context
	comments, err := client.IssueComments(owner, repo, task.IssueID)
	if err != nil {
		log.Printf("[WARN] Failed to get comments: %v", err)
	}

	// Build context with comment history
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repository: %s\n", task.Repo))
	sb.WriteString(fmt.Sprintf("Issue/PR #%d\n\n", task.IssueID))
	sb.WriteString("## Comment History\n")
	for i, c := range comments {
		if i >= 10 { // Limit to last 10 comments
			sb.WriteString("... (truncated)\n")
			break
		}
		sb.WriteString(fmt.Sprintf("[%s]: %s\n\n", c.User.Login, c.Body))
	}

	// Get LLM provider
	provider, err := r.factory.llmRegistry.Get(agent.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	// Build messages
	messages := []llm.Message{
		{Role: "system", Content: agent.SystemPrompt},
		{Role: "user", Content: sb.String()},
	}

	messages, err = agentpkg.TruncateMessages(messages, nil, r.factory.resolveMaxInputTokens(agent.MaxInputTokens, agent.Provider, agent.Model), r.factory.getModelMeta(agent.Provider, agent.Model))
	if err != nil {
		return nil, fmt.Errorf("truncate messages: %w", err)
	}

	// Call LLM
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
