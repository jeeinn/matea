package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
)

type countingProvider struct {
	calls int
}

func (p *countingProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls++
	if p.calls < 3 {
		return &llm.ChatResponse{
			ToolCalls: []llm.ToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: llm.FuncCall{
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				},
			}},
			FinishReason: "tool_calls",
		}, nil
	}
	return &llm.ChatResponse{Content: "done", FinishReason: "stop"}, nil
}

func TestAgentLoopIterationInterval(t *testing.T) {
	provider := &countingProvider{}
	registry := NewToolRegistry()
	loop := NewAgentLoopWithConfig(provider, registry, "test-model", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations:     3,
		IterationInterval: 1,
	})

	start := time.Now()
	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if provider.calls != 3 {
		t.Fatalf("expected 3 provider calls, got %d", provider.calls)
	}
	if elapsed < 2*time.Second {
		t.Fatalf("expected at least 2s delay between iterations, got %v", elapsed)
	}
}

type recordingRecorder struct {
	calls []recordCall
}

type recordCall struct {
	iteration int
	messages  int
	roles     []string
	hasFinal  bool
}

func (r *recordingRecorder) RecordIteration(taskID int64, iteration int, messages []llm.Message, finalAssistant *llm.ChatResponse) error {
	roles := make([]string, len(messages))
	for i, m := range messages {
		roles[i] = m.Role
	}
	r.calls = append(r.calls, recordCall{
		iteration: iteration,
		messages:  len(messages),
		roles:     roles,
		hasFinal:  finalAssistant != nil,
	})
	return nil
}

func TestAgentLoopPersistAfterTruncate(t *testing.T) {
	provider := &countingProvider{}
	registry := NewToolRegistry()
	recorder := &recordingRecorder{}
	loop := NewAgentLoopWithConfig(provider, registry, "test-model", 1024, 400, 0.3, config.AgentLoopConfig{
		MaxIterations: 3,
	})
	loop.SetConversationRecorder(recorder, 42)

	messages := []llm.Message{
		{Role: "system", Content: "You are a coder."},
		{Role: "user", Content: "Fix the bug."},
	}
	for i := 0; i < 20; i++ {
		messages = append(messages,
			llm.Message{
				Role:    "assistant",
				Content: "",
				ToolCalls: []llm.ToolCall{{
					ID:   fmt.Sprintf("call-%d", i),
					Type: "function",
					Function: llm.FuncCall{
						Name:      "read_file",
						Arguments: `{"path":"README.md"}`,
					},
				}},
			},
			llm.Message{
				Role:       "tool",
				Content:    strings.Repeat("x", 200),
				ToolCallID: fmt.Sprintf("call-%d", i),
			},
		)
	}

	_, err := loop.Run(context.Background(), messages)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(recorder.calls) == 0 {
		t.Fatal("expected conversation recorder to be called")
	}
}

func TestAgentLoopPersistsIterations(t *testing.T) {
	provider := &countingProvider{}
	registry := NewToolRegistry()
	recorder := &recordingRecorder{}
	loop := NewAgentLoopWithConfig(provider, registry, "test-model", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 3,
	})
	loop.SetConversationRecorder(recorder, 42)

	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// 3 iterations + 1 initial-input record (iteration 0)
	if len(recorder.calls) != 4 {
		t.Fatalf("expected 4 recorded calls (initial + 3 iterations), got %d", len(recorder.calls))
	}
	if recorder.calls[0].iteration != 0 {
		t.Fatalf("expected first recorded call to be iteration 0 (initial input), got %d", recorder.calls[0].iteration)
	}
	if !recorder.calls[3].hasFinal {
		t.Fatalf("expected final iteration to include assistant response")
	}
}

func TestAgentLoopPersistsInitialMessages(t *testing.T) {
	provider := &countingProvider{}
	registry := NewToolRegistry()
	recorder := &recordingRecorder{}
	loop := NewAgentLoopWithConfig(provider, registry, "test-model", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 3,
	})
	loop.SetConversationRecorder(recorder, 42)

	messages := []llm.Message{
		{Role: "system", Content: "You are a coder."},
		{Role: "user", Content: "Fix the bug."},
	}
	_, err := loop.Run(context.Background(), messages)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(recorder.calls) == 0 {
		t.Fatal("expected conversation recorder to be called")
	}
	initial := recorder.calls[0]
	if initial.iteration != 0 {
		t.Fatalf("expected initial input recorded as iteration 0, got %d", initial.iteration)
	}
	if initial.messages != 2 {
		t.Fatalf("expected initial record to contain system+user messages, got %d", initial.messages)
	}
	if len(initial.roles) != 2 || initial.roles[0] != "system" || initial.roles[1] != "user" {
		t.Fatalf("expected initial roles [system user], got %v", initial.roles)
	}
	if initial.hasFinal {
		t.Fatal("initial record must not carry a final assistant response")
	}
	// Initial input is recorded exactly once even across multiple iterations.
	for i, c := range recorder.calls[1:] {
		if c.iteration == 0 {
			t.Fatalf("call %d: iteration 0 recorded more than once", i+1)
		}
	}
}

func TestAgentLoopNoRecorderNoPanic(t *testing.T) {
	provider := &countingProvider{}
	registry := NewToolRegistry()
	loop := NewAgentLoopWithConfig(provider, registry, "test-model", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 3,
	})
	// No SetConversationRecorder: persistInitial/persistIteration must be no-ops.
	if _, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

// toolsCaptureProvider records whether Tools were sent in ChatRequest.
type toolsCaptureProvider struct {
	sawTools bool
	called   bool
}

func (p *toolsCaptureProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	p.called = true
	p.sawTools = len(req.Tools) > 0
	return &llm.ChatResponse{Content: "ok", FinishReason: "stop"}, nil
}

func TestAgentLoopOmitsToolsWhenModelDoesNotSupport(t *testing.T) {
	provider := &toolsCaptureProvider{}
	registry := NewToolRegistry()
	registry.Register(&ToolDef{
		Name:        "noop",
		Description: "noop",
		Parameters:  llm.Parameters{Type: "object", Properties: map[string]llm.Property{}},
		Fn:          func(map[string]interface{}) (string, error) { return "ok", nil },
	})
	loop := NewAgentLoopWithConfig(provider, registry, "reasoner", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 1,
	})
	loop.SetModelMeta(&config.ModelDefinition{SupportsTools: false})

	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !provider.called {
		t.Fatal("expected provider to be called")
	}
	if provider.sawTools {
		t.Fatal("expected Tools to be omitted when SupportsTools=false")
	}
}

func TestAgentLoopSendsToolsWhenModelSupports(t *testing.T) {
	provider := &toolsCaptureProvider{}
	registry := NewToolRegistry()
	registry.Register(&ToolDef{
		Name:        "noop",
		Description: "noop",
		Parameters:  llm.Parameters{Type: "object", Properties: map[string]llm.Property{}},
		Fn:          func(map[string]interface{}) (string, error) { return "ok", nil },
	})
	loop := NewAgentLoopWithConfig(provider, registry, "flash", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 1,
	})
	loop.SetModelMeta(&config.ModelDefinition{SupportsTools: true})

	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !provider.sawTools {
		t.Fatal("expected Tools to be sent when SupportsTools=true")
	}
}

type pseudoToolCallProvider struct {
	content string
}

func (p *pseudoToolCallProvider) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return &llm.ChatResponse{Content: p.content, FinishReason: "stop"}, nil
}

func TestAgentLoopRejectsPseudoToolCallContent(t *testing.T) {
	provider := &pseudoToolCallProvider{content: `<|DSML|tool_calls>
<|DSML|invoke name="read_file">
<|DSML|parameter name="path" string="true">docs/RENAME-TO-MATEA.md</|DSML|parameter>
</|DSML|invoke>`}
	registry := NewToolRegistry()
	loop := NewAgentLoopWithConfig(provider, registry, "flash", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 1,
	})
	loop.SetModelMeta(&config.ModelDefinition{SupportsTools: true})

	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("expected error for textual tool-call markup")
	}
	if !strings.Contains(err.Error(), "textual tool-call markup") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// When SupportsTools=false, tools are omitted from the request; the model may
// still dump DSML/text tool markup into content. Detection must still fail closed.
func TestAgentLoopRejectsPseudoToolCallWhenToolsOmitted(t *testing.T) {
	provider := &pseudoToolCallProvider{content: `<|DSML|tool_calls>
<|DSML|invoke name="read_file">
<|DSML|parameter name="path" string="true">docs/RENAME-TO-MATEA.md</|DSML|parameter>
</|DSML|invoke>`}
	registry := NewToolRegistry()
	registry.Register(&ToolDef{
		Name:        "read_file",
		Description: "read a file",
		Parameters:  llm.Parameters{Type: "object", Properties: map[string]llm.Property{}},
		Fn:          func(map[string]interface{}) (string, error) { return "ok", nil },
	})
	loop := NewAgentLoopWithConfig(provider, registry, "reasoner", 1024, 8192, 0.3, config.AgentLoopConfig{
		MaxIterations: 1,
	})
	loop.SetModelMeta(&config.ModelDefinition{SupportsTools: false})

	_, err := loop.Run(context.Background(), []llm.Message{{Role: "user", Content: "go"}})
	if err == nil {
		t.Fatal("expected error for textual tool-call markup when tools omitted")
	}
	if !strings.Contains(err.Error(), "textual tool-call markup") {
		t.Fatalf("unexpected error: %v", err)
	}
}
