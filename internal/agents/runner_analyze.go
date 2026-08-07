package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// appendAnalysisLandingNote adds a soft-gate reminder to analyze results so the
// user is prompted to land suggested code changes via a coder task (P2-2).
func appendAnalysisLandingNote(content string) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	return content + "\n\n💡 **落地提示**：本任务为只读分析。若上述建议涉及代码改动，请由 coder Agent 执行对应任务——分析结论不会自动落地。"
}

// --- AnalyzeRunner ---

// AnalyzeRunner handles issue analysis tasks.
type AnalyzeRunner struct {
	factory *RunnerFactory
}

// NewAnalyzeRunner creates a new AnalyzeRunner.
func NewAnalyzeRunner(factory *RunnerFactory) *AnalyzeRunner {
	return &AnalyzeRunner{factory: factory}
}

// Run executes the analyze task.
//
// It first attempts a shallow clone + short read-only AgentLoop for richer
// analysis. If clone fails it falls back to single-shot LLM (legacy behavior).
func (r *AnalyzeRunner) Run(ctx context.Context, task *store.Task, agent *store.Agent) (*Result, error) {
	// Hub dispatch branch (1.2.4): reserved hub-* / unknown backends fail
	// loudly. Analyze stays on the builtin LLM by design (forced builtin);
	// configured hub-opencode instances pass validation but are write-only.
	if err := r.factory.validateHubDispatch(agent); err != nil {
		return nil, err
	}

	// Hub execution branch (task 2.1.2): when the agent's backend is a
	// hub-hermes instance, route the analyze task through Hermes via
	// Submit/Poll instead of the in-process LLM loop. Hermes owns its own
	// execution environment, so no workspace clone is prepared here.
	if hb, ok := r.factory.ResolveHubExecution(agent); ok {
		tc := &TaskContext{
			TaskType:     task.TaskType,
			Role:         "analyze",
			Backend:      hb.Name(),
			Repo:         task.Repo,
			IssueID:      task.IssueID,
			PRID:         task.PRID,
			IssueTitle:   task.Event,
			IssueBody:    task.Context,
			SystemPrompt: agent.SystemPrompt,
			UserPrompt:   task.Context,
			MemoryKeys:   r.factory.loadMemoryKeys(task),
		}
		res, err := r.factory.runViaHub(ctx, task, agent, hb, tc)
		if err != nil {
			return nil, err
		}
		// Record the conclusion for later tasks on the same repo+issue
		// (D3 cross-task memory sharing, task 2.1.5).
		r.factory.saveAnalyzeMemory(task, res.Content)
		return res, nil
	}

	provider, err := r.factory.llmRegistry.Get(agent.Provider)
	if err != nil {
		return nil, fmt.Errorf("get provider: %w", err)
	}

	// Try read-only workspace + short loop for richer analysis
	wwc, err := prepareAnalyzeWorkspace(ctx, task, agent, r.factory)
	if err != nil {
		log.Printf("[WARN] Task %d analyze workspace failed (%v); falling back to single-shot", task.ID, err)
		return r.runSingleShot(ctx, task, agent, provider)
	}
	defer wwc.Sandbox.Cleanup()

	return r.runAnalyzeLoop(ctx, task, agent, provider, wwc.Sandbox)
}

// runSingleShot is the legacy single-shot LLM analysis (no workspace).
func (r *AnalyzeRunner) runSingleShot(ctx context.Context, task *store.Task, agent *store.Agent, provider llm.Provider) (*Result, error) {
	systemPrompt := agent.SystemPrompt
	if systemPrompt != "" {
		systemPrompt += "\n\n"
	}
	systemPrompt += "## Workspace\n\nNo local repository workspace is available for this analysis (clone unavailable or provider lacks tools). Do not invent paths like `/workspace`; base conclusions on the issue text only.\n"

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task.Context},
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
		Content: appendAnalysisLandingNote(resp.Content),
		Action:  "comment",
	}, nil
}

// runAnalyzeLoop runs a short read-only AgentLoop on a prepared workspace.
func (r *AnalyzeRunner) runAnalyzeLoop(ctx context.Context, task *store.Task, agent *store.Agent, provider llm.Provider, sb *sandbox.Sandbox) (*Result, error) {
	maxInput := r.factory.resolveMaxInputTokens(agent.MaxInputTokens, agent.Provider, agent.Model)
	maxOutput := r.factory.resolveMaxOutputTokens(agent.MaxOutputTokens, agent.Provider, agent.Model)
	sampling := r.factory.resolveSamplingParams(agent.Temperature, agent.Provider, agent.Model)

	if !llm.SupportsTools(provider) {
		log.Printf("[WARN] Task %d provider %q lacks tool support; falling back to single-shot analyze", task.ID, agent.Provider)
		return r.runSingleShot(ctx, task, agent, provider)
	}

	// Load code context (best-effort)
	codeCtx, err := agentpkg.LoadCodeContext(sb, maxInput)
	if err != nil {
		log.Printf("[WARN] Failed to load code context: %v", err)
	}

	// Build analyze prompt
	taskCtx := agentpkg.TaskContext{
		IssueTitle: task.Event,
		IssueBody:  task.Context,
		RepoName:   task.Repo,
		TaskType:   "analyze",
	}
	basePrompt := agentpkg.BuildAnalyzePrompt(taskCtx, codeCtx)
	systemPrompt := agentpkg.MergeAgentSystemPrompt(basePrompt, agent.SystemPrompt)
	systemPrompt += fmt.Sprintf("\n\n## Workspace\n\nYour working directory is `%s`. All file paths are relative to this directory; do not guess or use absolute paths like /workspace.\n", sb.WorkDir)

	// Assemble analyze-readonly tool pack
	packID := r.factory.resolveToolPack(task.TaskType)
	packCfg, ok := r.factory.toolPacks.Packs[packID]
	if !ok {
		return nil, fmt.Errorf("tool pack %q not found", packID)
	}
	toolRegistry, err := agentpkg.AssembleToolRegistry(packCfg.Tools, sb)
	if err != nil {
		return nil, fmt.Errorf("assemble tool registry for pack %q: %w", packID, err)
	}

	// Register MCP tools if enabled for this agent
	if len(agent.McpServers) > 0 && r.factory.mcpRegistry != nil {
		if err := toolRegistry.RegisterMCPTools(ctx, r.factory.mcpRegistry, agent.McpServers); err != nil {
			return nil, fmt.Errorf("register mcp tools: %w", err)
		}
	}

	// Register skill discovery tools (Analyze: no arbitrary skill scripts)
	skillReg := agentpkg.NewSkillRegistry(sb, r.factory.gatewayDir)
	toolRegistry.Register(agentpkg.NewListSkillsTool(skillReg))
	toolRegistry.Register(agentpkg.NewLoadSkillTool(skillReg, toolRegistry, false))

	// Short loop: max 5 iterations for read-only analysis
	loop := agentpkg.NewAgentLoopWithConfig(
		provider,
		toolRegistry,
		agent.Model,
		maxOutput,
		maxInput,
		sampling.Temperature,
		r.factory.defaultLoop,
	)
	loop.SetMaxIterations(5)
	loop.SetSamplingParams(sampling.TopP, sampling.FrequencyPenalty, sampling.PresencePenalty)
	loop.SetModelMeta(r.factory.getModelMeta(agent.Provider, agent.Model))
	loop.SetProviderName(agent.Provider)
	loop.SetUsageRecorder(func(p, m string, usage llm.Usage) {
		r.factory.recordTaskUsage(task.ID, p, m, usage)
	})

	if r.factory.getDebugConfig != nil {
		debugCfg := r.factory.getDebugConfig()
		if debugCfg.ConversationLog.Enabled && r.factory.db != nil {
			loop.SetConversationRecorder(
				newConversationRecorder(r.factory.db, debugCfg.ConversationLog.MaxContentChars),
				task.ID,
			)
		}
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: task.Context},
	}

	messages, err = agentpkg.TruncateMessages(messages, toolRegistry.ToLLMTools(), maxInput, r.factory.getModelMeta(agent.Provider, agent.Model))
	if err != nil {
		return nil, fmt.Errorf("truncate messages: %w", err)
	}

	result, err := loop.Run(ctx, messages)
	if err != nil {
		return nil, fmt.Errorf("analyze agent loop: %w", err)
	}

	log.Printf("[INFO] Task %d analyze loop completed", task.ID)

	return &Result{
		Content: appendAnalysisLandingNote(result),
		Action:  "comment",
	}, nil
}
