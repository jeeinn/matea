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
	// Resolve the hub-opencode decision (task 2.2.3, D7 third cut). Like the
	// hermes branch, a hub-opencode reply tolerates a missing gitea block: the
	// minimal reply workspace (decision B) is cloned-free, so gitea is not a
	// hard dependency for this path.
	ocHB, viaOpenCode := r.factory.ResolveHubOpenCode(agent)

	// Get comment history for context. Required for the builtin path (the
	// prompt is built from it), best-effort for the hub paths (hermes and
	// opencode).
	var comments []gitea.IssueComment
	switch {
	case r.factory.giteaFactory == nil:
		if !viaHub && !viaOpenCode {
			return nil, fmt.Errorf("gitea client factory not configured")
		}
		log.Printf("[WARN] Task %d: no gitea client factory configured; hub reply proceeds without comment history", task.ID)
	default:
		client := r.factory.giteaFactory.GetGiteaClient(agent.GiteaToken)
		if client == nil {
			if !viaHub && !viaOpenCode {
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

	// Hub-OpenCode branch (task 2.2.3, D7 third cut, decision B): when the
	// agent's backend is a hub-opencode instance, prepare a minimal (empty)
	// workspace solely to satisfy the OpenCode Submit SandboxPath contract,
	// then route the reply through OpenCode. Workspace prep failure degrades
	// to a single-shot reply rather than failing the task. Comment history is
	// delivered best-effort (OpenCode currently ignores Comments; it carries
	// the reply target via IssueBody/UserPrompt).
	if viaOpenCode {
		wwc, err := prepareReplyWorkspace(ctx, task, agent, r.factory)
		if err != nil {
			log.Printf("[WARN] Task %d: hub-opencode reply workspace prep failed (%v); falling back to single-shot", task.ID, err)
			return r.runSingleShotReply(ctx, task, agent, comments)
		}
		defer wwc.Sandbox.Cleanup()
		res, err := r.factory.runViaHub(ctx, task, agent, ocHB, &TaskContext{
			TaskType:     task.TaskType,
			Role:         "interaction",
			Backend:      ocHB.Name(),
			Repo:         task.Repo,
			IssueID:      task.IssueID,
			PRID:         task.PRID,
			IssueTitle:   task.Event,
			IssueBody:    task.Context,
			Comments:     toCommentSnapshots(comments),
			SystemPrompt: agent.SystemPrompt,
			UserPrompt:   fmt.Sprintf("Repository: %s\nIssue/PR #%d\n\n%s", task.Repo, task.IssueID, task.Context),
			SandboxPath:  wwc.Sandbox.WorkDir,
			MemoryKeys:   r.factory.loadMemoryKeys(task),
		})
		if err != nil {
			return nil, err
		}
		return res, nil
	}

	// Builtin path: single-shot reply through the in-process LLM.
	return r.runSingleShotReply(ctx, task, agent, comments)
}

// runSingleShotReply is the builtin reply implementation (in-process LLM, no
// hub). It builds the prompt from the comment history and calls the provider.
// It is also the degradation target for the hub-opencode path when workspace
// preparation fails (task 2.2.3).
func (r *InteractionRunner) runSingleShotReply(ctx context.Context, task *store.Task, agent *store.Agent, comments []gitea.IssueComment) (*Result, error) {
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

	res := &Result{
		Content: resp.Content,
		Action:  "comment",
	}
	r.factory.emitBuiltinDeliver(task, res)
	return res, nil
}
