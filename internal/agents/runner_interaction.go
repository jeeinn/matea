package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/gitea"
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

	// Resolve the hub execution decision *before* touching Gitea. The hub
	// path only needs the comment history on a best-effort basis, so a
	// deployment without a configured gitea block must not panic on the
	// nil factory before we even reach the branch.
	hb, viaHub := r.factory.ResolveHubExecution(agent)

	// Get comment history for context. Required for the builtin path (the
	// prompt is built from it), best-effort for the hub path.
	var comments []gitea.IssueComment
	switch {
	case r.factory.giteaFactory == nil:
		if !viaHub {
			return nil, fmt.Errorf("gitea client factory not configured")
		}
		log.Printf("[WARN] Task %d: no gitea client factory configured; hub reply proceeds without comment history", task.ID)
	default:
		client := r.factory.giteaFactory.GetGiteaClient(agent.GiteaToken)
		if client == nil {
			if !viaHub {
				return nil, fmt.Errorf("gitea client unavailable (task %d)", task.ID)
			}
			log.Printf("[WARN] Task %d: gitea client unavailable; hub reply proceeds without comment history", task.ID)
			break
		}
		var err error
		comments, err = client.IssueComments(owner, repo, task.IssueID)
		if err != nil {
			log.Printf("[WARN] Failed to get comments: %v", err)
		}
	}

	// Hub execution branch (task 2.1.4): when the agent's backend is a
	// hub-hermes instance, route the reply through Hermes. The comment
	// history is delivered as conversation_history so Hermes continues the
	// multi-turn session (session_id correlation, D3). A failed or skipped
	// comment fetch yields an empty history rather than failing the task.
	if viaHub {
		return r.factory.runViaHub(ctx, task, agent, hb, &TaskContext{
			TaskType:     task.TaskType,
			Role:         "interaction",
			Backend:      hb.Name(),
			Repo:         task.Repo,
			IssueID:      task.IssueID,
			PRID:         task.PRID,
			IssueTitle:   task.Event,
			IssueBody:    task.Context,
			Comments:     toCommentSnapshots(comments),
			SystemPrompt: agent.SystemPrompt,
			UserPrompt:   fmt.Sprintf("Repository: %s\nIssue/PR #%d\n\n%s", task.Repo, task.IssueID, task.Context),
			MemoryKeys:   r.factory.loadMemoryKeys(task),
		})
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
