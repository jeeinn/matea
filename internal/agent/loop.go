package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/logging"
)

// AgentLoop manages the multi-turn conversation between LLM and tools.
// Task-level deadline is owned by the Executor context; this loop only
// enforces maxIterations, optional iterationInterval, and optional no-progress exit.
type AgentLoop struct {
	provider          llm.Provider
	registry          *ToolRegistry
	model             string
	modelMeta         *config.ModelDefinition
	providerName      string
	maxTokens         int // output tokens per completion
	maxInputTokens    int // input budget for messages+tools
	temperature       float64
	topP              float64
	frequencyPenalty  float64
	presencePenalty   float64
	maxIterations     int
	iterationInterval time.Duration
	recorder          ConversationRecorder
	usageRecorder     func(provider, model string, usage llm.Usage)
	taskID            int64

	// Harness: no-progress exit (only when progressSnap is set and noProgressLimit > 0)
	noProgressLimit  int
	progressSnap     func() string
	lastProgressSnap string
	stallCount       int
}

// NewAgentLoop creates a new AgentLoop with default iteration settings.
func NewAgentLoop(provider llm.Provider, registry *ToolRegistry, model string, maxTokens int, temperature float64) *AgentLoop {
	return &AgentLoop{
		provider:       provider,
		registry:       registry,
		model:          model,
		maxTokens:      maxTokens,
		maxInputTokens: config.DefaultMaxInputTokens,
		temperature:    temperature,
		maxIterations:  20,
	}
}

// NewAgentLoopWithConfig creates a new AgentLoop with loop configuration.
// maxInputTokens controls TruncateMessages before each ChatCompletion.
func NewAgentLoopWithConfig(provider llm.Provider, registry *ToolRegistry, model string, maxTokens, maxInputTokens int, temperature float64, loopCfg config.AgentLoopConfig) *AgentLoop {
	maxIter := loopCfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	if maxInputTokens <= 0 {
		maxInputTokens = config.DefaultMaxInputTokens
	}

	iterationInterval := time.Duration(0)
	if loopCfg.IterationInterval > 0 {
		iterationInterval = time.Duration(loopCfg.IterationInterval) * time.Second
	}

	return &AgentLoop{
		provider:          provider,
		registry:          registry,
		model:             model,
		maxTokens:         maxTokens,
		maxInputTokens:    maxInputTokens,
		temperature:       temperature,
		maxIterations:     maxIter,
		iterationInterval: iterationInterval,
	}
}

// SetMaxIterations sets the maximum number of iterations.
func (a *AgentLoop) SetMaxIterations(n int) {
	a.maxIterations = n
}

// SetModelMeta sets the model metadata for tool support awareness.
func (a *AgentLoop) SetModelMeta(meta *config.ModelDefinition) {
	a.modelMeta = meta
}

// SetUsageRecorder sets the usage recorder callback.
func (a *AgentLoop) SetUsageRecorder(recorder func(provider, model string, usage llm.Usage)) {
	a.usageRecorder = recorder
}

// SetProviderName sets the provider name for usage recording.
func (a *AgentLoop) SetProviderName(name string) {
	a.providerName = name
}

// SetSamplingParams sets optional top_p / penalty values (0 = omit / provider default).
// Temperature continues to come from the constructor.
func (a *AgentLoop) SetSamplingParams(topP, frequencyPenalty, presencePenalty float64) {
	a.topP = topP
	a.frequencyPenalty = frequencyPenalty
	a.presencePenalty = presencePenalty
}

// SetConversationRecorder enables per-iteration conversation persistence for a task.
func (a *AgentLoop) SetConversationRecorder(recorder ConversationRecorder, taskID int64) {
	a.recorder = recorder
	a.taskID = taskID
}

// SetNoProgressGuard enables stall detection: after each tool-call round, progressSnap
// is compared to the previous snapshot. If unchanged for limit consecutive rounds,
// Run returns an error. limit <= 0 or nil snap disables the guard.
func (a *AgentLoop) SetNoProgressGuard(limit int, progressSnap func() string) {
	a.noProgressLimit = limit
	a.progressSnap = progressSnap
	a.lastProgressSnap = ""
	a.stallCount = 0
}

// Run executes the agent loop with the given messages.
// Returns the final assistant message content.
func (a *AgentLoop) Run(ctx context.Context, messages []llm.Message) (string, error) {
	tools := a.registry.ToLLMTools()
	if a.modelMeta != nil && !a.modelMeta.SupportsTools {
		tools = nil
	}

	for i := 0; i < a.maxIterations; i++ {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("agent loop cancelled: %w", err)
		}
		if i > 0 && a.iterationInterval > 0 {
			logging.Debugf("Agent loop waiting %s before iteration %d/%d",
				a.iterationInterval, i+1, a.maxIterations)
			timer := time.NewTimer(a.iterationInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", fmt.Errorf("agent loop cancelled during iteration delay: %w", ctx.Err())
			case <-timer.C:
			}
		}

		logging.Debugf("Agent loop iteration %d/%d", i+1, a.maxIterations)

		trimmed, err := TruncateMessages(messages, tools, a.maxInputTokens, a.modelMeta)
		if err != nil {
			return "", fmt.Errorf("truncate messages: %w", err)
		}
		messages = trimmed
		// Record delta after truncate: msgStart captured before truncate can exceed len(messages) and panic.
		msgStart := len(messages)
		if i == 0 {
			// Persist the initial input (system prompt + user context) as iteration 0
			// so the conversation log shows what the model was actually given.
			a.persistInitial(messages)
		}

		resp, err := a.provider.ChatCompletion(ctx, &llm.ChatRequest{
			Model:            a.model,
			Messages:         messages,
			Tools:            tools,
			MaxTokens:        a.maxTokens,
			Temperature:      a.temperature,
			TopP:             a.topP,
			FrequencyPenalty: a.frequencyPenalty,
			PresencePenalty:  a.presencePenalty,
		})
		if err != nil {
			return "", fmt.Errorf("LLM call: %w", err)
		}

		// Record usage if recorder is set
		if a.usageRecorder != nil {
			a.usageRecorder(a.providerName, a.model, resp.Usage)
		}

		logging.Debugf("LLM response: content_len=%d, tool_calls=%d, finish=%s",
			len(resp.Content), len(resp.ToolCalls), resp.FinishReason)
		if resp.Content != "" {
			logging.Debugf("LLM content preview: %s", truncateForLog(resp.Content, 800))
		}
		for _, call := range resp.ToolCalls {
			logging.Debugf("LLM tool_call: %s(%s)", call.Function.Name, truncateForLog(call.Function.Arguments, 500))
		}

		if len(resp.ToolCalls) == 0 {
			if LooksLikePseudoToolCall(resp.Content) {
				a.persistIteration(i+1, messages[msgStart:], resp)
				return "", fmt.Errorf("model returned textual tool-call markup instead of structured tool_calls; use a model with supports_tools=true and a provider that emits OpenAI-compatible tool_calls")
			}
			a.persistIteration(i+1, messages[msgStart:], resp)
			return resp.Content, nil
		}

		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, call := range resp.ToolCalls {
			logging.Debugf("Executing tool: %s(%s)", call.Function.Name, truncateForLog(call.Function.Arguments, 500))

			result, err := a.registry.ExecuteTool(call)
			if err != nil {
				result = fmt.Sprintf("Error: %v", err)
				logging.Warnf("Tool execution failed: %v", err)
			}
			logging.Debugf("Tool result %s: %s", call.Function.Name, truncateForLog(result, 1200))

			messages = append(messages, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: call.ID,
			})
		}

		a.persistIteration(i+1, messages[msgStart:], nil)

		if err := a.checkNoProgress(); err != nil {
			return "", err
		}
	}

	return "", fmt.Errorf("max iterations (%d) reached", a.maxIterations)
}

func (a *AgentLoop) checkNoProgress() error {
	if a.progressSnap == nil || a.noProgressLimit <= 0 {
		return nil
	}
	snap := a.progressSnap()
	if a.lastProgressSnap != "" && snap == a.lastProgressSnap {
		a.stallCount++
		logging.Debugf("Agent loop no progress (%d/%d): workspace fingerprint unchanged",
			a.stallCount, a.noProgressLimit)
		if a.stallCount >= a.noProgressLimit {
			return fmt.Errorf("no progress for %d consecutive iterations (workspace unchanged); stopping to avoid burning tokens", a.noProgressLimit)
		}
	} else {
		a.stallCount = 0
	}
	a.lastProgressSnap = snap
	return nil
}

func (a *AgentLoop) persistIteration(iteration int, delta []llm.Message, finalAssistant *llm.ChatResponse) {
	if a.recorder == nil || a.taskID <= 0 {
		return
	}
	if len(delta) == 0 && finalAssistant == nil {
		return
	}
	if err := a.recorder.RecordIteration(a.taskID, iteration, delta, finalAssistant); err != nil {
		logging.Warnf("Failed to persist conversation log (task=%d iter=%d): %v", a.taskID, iteration, err)
	}
}

// persistInitial records the initial messages (system prompt + user context) as
// iteration 0. Called once on the first loop iteration, after truncation, so the
// log reflects exactly what the first LLM call saw.
func (a *AgentLoop) persistInitial(messages []llm.Message) {
	if a.recorder == nil || a.taskID <= 0 || len(messages) == 0 {
		return
	}
	if err := a.recorder.RecordIteration(a.taskID, 0, messages, nil); err != nil {
		logging.Warnf("Failed to persist initial conversation messages (task=%d): %v", a.taskID, err)
	}
}

func truncateForLog(s string, maxRunes int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "…(truncated)"
}
