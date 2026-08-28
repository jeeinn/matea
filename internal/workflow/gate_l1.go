package workflow

import (
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// GateResult is the outcome of a gate check.
type GateResult struct {
	Allowed bool   // Whether the action is allowed
	Level   string // "pass", "hard"
	Code    string // e.g. "l1.review_requires_pr"
	Message string // Human-readable message for comments
}

// L1Gate checks structural (hard) gate rules that cannot be overridden.
type L1Gate struct {
	db *store.DB
}

// NewL1Gate creates a new L1 gate checker.
func NewL1Gate(db *store.DB) *L1Gate {
	return &L1Gate{db: db}
}

// CheckL1 runs all L1 structural checks.
// Returns a GateResult. If Allowed is false, the task should not be enqueued.
func (g *L1Gate) CheckL1(evt *giteaingress.WebhookEvent, role string, agent *store.Agent) GateResult {
	switch role {
	case store.RoleReview:
		return g.checkReviewRequiresPR(evt)
	default:
		// L1 has no structural checks for analyze/coder in Phase 16
		return GateResult{Allowed: true, Level: "pass"}
	}
}

// checkReviewRequiresPR verifies that a review agent has an open PR to review.
func (g *L1Gate) checkReviewRequiresPR(evt *giteaingress.WebhookEvent) GateResult {
	// For PR events, the PR itself is available
	if evt.PR != nil {
		// PR exists — check if it's open
		if evt.PR.State == "closed" {
			return GateResult{
				Allowed: false,
				Level:   "hard",
				Code:    "l1.review_on_closed_pr",
				Message: "❌ 无法审查已关闭的 PR。",
			}
		}
		return GateResult{Allowed: true, Level: "pass"}
	}

	// For issue events, we need to check if there's an open PR for this issue
	if evt.Issue != nil {
		// Check workflow context for a linked PR
		repo := evt.Repo.FullName
		issueID := evt.Issue.Number

		ctx, err := g.db.GetWorkflowContext(repo, issueID)
		if err != nil || ctx.PRID == 0 {
			return GateResult{
				Allowed: false,
				Level:   "hard",
				Code:    "l1.review_requires_pr",
				Message: "❌ 无法审查：此 Issue 尚无关联的 PR。请先由 coder 创建 PR 后再请求审查。",
			}
		}

		// PR ID exists — could verify it's still open via Gitea API
		// For now, trust the workflow context
		return GateResult{Allowed: true, Level: "pass"}
	}

	return GateResult{
		Allowed: false,
		Level:   "hard",
		Code:    "l1.review_requires_pr",
		Message: "❌ 无法审查：缺少 PR 或 Issue 信息。",
	}
}

// FormatAgentComment wraps a message with the agent comment marker for loop prevention.
func FormatAgentComment(body string) string {
	return "<!-- matea-agent -->\n" + body
}

// IsAgentComment checks if a comment was posted by the gateway agent.
func IsAgentComment(body string) bool {
	return strings.HasPrefix(body, "<!-- matea-agent -->")
}

// CommentOnIssue posts a comment on the issue/PR using the agent's token.
// This is a helper for gate results that need to post feedback.
func CommentOnIssue(giteaURL, agentToken, repo string, issueID int, body string) error {
	if agentToken == "" || repo == "" || issueID == 0 {
		return fmt.Errorf("missing parameters for comment")
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s", repo)
	}

	client := gitea.NewClient(giteaURL, agentToken)
	commentBody := FormatAgentComment(body)
	if err := client.IssueComment(parts[0], parts[1], issueID, commentBody); err != nil {
		return fmt.Errorf("post comment on %s#%d: %w", repo, issueID, err)
	}
	log.Printf("[INFO] Posted gate comment on %s#%d: %s", repo, issueID, truncate(body, 100))
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
