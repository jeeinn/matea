package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
)

// workspaceProgressSnapshot returns a fingerprint of uncommitted workspace state.
// Used by AgentLoop no-progress detection (git status --porcelain).
func workspaceProgressSnapshot(sb *sandbox.Sandbox) string {
	if sb == nil {
		return ""
	}
	res := sb.Execute("git", "status", "--porcelain")
	if res == nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// runHarnessVerify runs configured shell commands in the workspace after coding
// and before commit/PR. Empty cmds is a no-op. First failing command aborts.
func runHarnessVerify(sb *sandbox.Sandbox, cmds []string) error {
	if sb == nil || len(cmds) == 0 {
		return nil
	}
	for _, raw := range cmds {
		cmd := strings.TrimSpace(raw)
		if cmd == "" {
			continue
		}
		log.Printf("[INFO] Harness verify: %s", cmd)
		res := sb.ExecuteShell(cmd)
		if res.Error != nil || res.ExitCode != 0 {
			out := strings.TrimSpace(res.Stdout)
			errOut := strings.TrimSpace(res.Stderr)
			detail := errOut
			if detail == "" {
				detail = out
			}
			if detail == "" && res.Error != nil {
				detail = res.Error.Error()
			}
			if len(detail) > 4000 {
				detail = detail[:4000] + "…(truncated)"
			}
			return fmt.Errorf("verify gate failed (exit=%d): %s\n%s", res.ExitCode, cmd, detail)
		}
		log.Printf("[INFO] Harness verify OK: %s", cmd)
	}
	return nil
}

// runIndependentChecker asks a fresh LLM (no agent-loop history) to PASS/FAIL the
// current git diff against the issue. Opt-in via agents.loop.independent_checker.
func runIndependentChecker(
	ctx context.Context,
	sb *sandbox.Sandbox,
	provider llm.Provider,
	agentModel string,
	sampling SamplingParams,
	maxTokens int,
	issueTitle, issueBody, coderSummary string,
) error {
	if sb == nil || provider == nil {
		return nil
	}

	diffRes := sb.Execute("git", "diff", "HEAD")
	diff := ""
	if diffRes != nil {
		diff = strings.TrimSpace(diffRes.Stdout)
		if diff == "" {
			st := sb.Execute("git", "status", "--porcelain")
			if st != nil {
				diff = strings.TrimSpace(st.Stdout)
			}
		}
	}

	prompt := agentpkg.BuildIndependentCheckerPrompt(agentpkg.CheckerPromptInput{
		IssueTitle: issueTitle,
		IssueBody:  issueBody,
		Diff:       diff,
		Summary:    coderSummary,
	})

	req := &llm.ChatRequest{
		Model: agentModel,
		Messages: []llm.Message{
			{Role: "system", Content: "You are an independent Checker. Reply with a short rationale and end with VERDICT: PASS or VERDICT: FAIL."},
			{Role: "user", Content: prompt},
		},
		MaxTokens: maxTokens,
	}
	sampling.ApplyTo(req)

	log.Printf("[INFO] Harness independent checker: invoking LLM on git diff (%d chars)", len(diff))
	resp, err := provider.ChatCompletion(ctx, req)
	if err != nil {
		return fmt.Errorf("independent checker LLM: %w", err)
	}

	pass, ok := agentpkg.ParseCheckerVerdict(resp.Content)
	if !ok {
		preview := strings.TrimSpace(resp.Content)
		if len(preview) > 500 {
			preview = preview[:500] + "…"
		}
		return fmt.Errorf("independent checker: missing VERDICT line\n%s", preview)
	}
	if !pass {
		preview := strings.TrimSpace(resp.Content)
		if len(preview) > 2000 {
			preview = preview[:2000] + "…"
		}
		return fmt.Errorf("independent checker: VERDICT: FAIL\n%s", preview)
	}
	log.Printf("[INFO] Harness independent checker: VERDICT: PASS")
	return nil
}
