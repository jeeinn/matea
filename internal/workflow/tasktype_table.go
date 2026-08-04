package workflow

import (
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/webhook"
)

// Surface represents the event surface (issue or PR).
type Surface string

const (
	SurfaceIssue Surface = "issue"
	SurfacePR    Surface = "pr"
)

// Intent represents the user's intent when triggering an agent.
type Intent string

const (
	IntentAssign          Intent = "assign"           // Assign agent to issue
	IntentReviewRequested Intent = "review_requested" // Request review on PR
	IntentMention         Intent = "mention"          // @mention in comment
	IntentSlashDev        Intent = "slash_dev"        // /dev command in comment
	IntentSlashReply      Intent = "slash_reply"      // /reply command in comment
)

// ResolveKey uniquely identifies a trigger scenario.
type ResolveKey struct {
	Role    string  // analyze | coder | review
	Surface Surface // issue | pr
	Intent  Intent  // assign | review_requested | mention | slash_dev | slash_reply
}

// TaskTypeTable maps (role, surface, intent) to task_type.
// This table preserves all current behavior while making the mapping explicit.
var TaskTypeTable = map[ResolveKey]string{
	// Analyze role
	{store.RoleAnalyze, SurfaceIssue, IntentAssign}:    "analyze_issue",
	{store.RoleAnalyze, SurfaceIssue, IntentMention}:   "reply_comment",
	{store.RoleAnalyze, SurfaceIssue, IntentSlashDev}:  "solve_comment", // Force dev mode
	{store.RoleAnalyze, SurfaceIssue, IntentSlashReply}: "reply_comment",
	{store.RoleAnalyze, SurfacePR, IntentMention}:      "reply_comment",
	{store.RoleAnalyze, SurfacePR, IntentSlashDev}:     "solve_comment", // Force dev mode
	{store.RoleAnalyze, SurfacePR, IntentSlashReply}:   "reply_comment",

	// Coder role
	{store.RoleCoder, SurfaceIssue, IntentAssign}:    "solve_issue", // Note: special case for bug label → fix_bug
	{store.RoleCoder, SurfaceIssue, IntentMention}:   "solve_comment",
	{store.RoleCoder, SurfaceIssue, IntentSlashDev}:  "solve_comment",
	{store.RoleCoder, SurfaceIssue, IntentSlashReply}: "reply_comment", // Force reply mode
	{store.RoleCoder, SurfacePR, IntentMention}:      "solve_comment",
	{store.RoleCoder, SurfacePR, IntentSlashDev}:     "solve_comment",
	{store.RoleCoder, SurfacePR, IntentSlashReply}:    "reply_comment", // Force reply mode

	// Review role
	{store.RoleReview, SurfacePR, IntentReviewRequested}: "review_pr",
	{store.RoleReview, SurfacePR, IntentMention}:         "reply_comment", // Critical: NOT review_pr
	{store.RoleReview, SurfacePR, IntentSlashDev}:        "solve_comment", // Force dev mode
	{store.RoleReview, SurfacePR, IntentSlashReply}:      "reply_comment",
	{store.RoleReview, SurfaceIssue, IntentMention}:      "reply_comment", // Critical: NOT review_pr
	{store.RoleReview, SurfaceIssue, IntentSlashDev}:     "solve_comment", // Force dev mode
	{store.RoleReview, SurfaceIssue, IntentSlashReply}:   "reply_comment",
}

// ResolveTaskType looks up the task type from the resolve table.
// Returns the task type, or empty string if no mapping exists.
func ResolveTaskType(role string, surface Surface, intent Intent, evt *webhook.WebhookEvent) string {
	key := ResolveKey{Role: role, Surface: surface, Intent: intent}
	taskType, ok := TaskTypeTable[key]
	if !ok {
		return "" // No mapping found
	}

	// Special case: coder + assign + bug label → fix_bug
	if role == store.RoleCoder && intent == IntentAssign && surface == SurfaceIssue {
		if evt != nil && evt.Issue != nil {
			for _, label := range evt.Issue.Labels {
				if label.Name == "bug" {
					return "fix_bug"
				}
			}
		}
	}

	return taskType
}

// DetermineSurface determines the surface (issue or PR) from the event.
func DetermineSurface(evt *webhook.WebhookEvent) Surface {
	if evt == nil {
		return SurfaceIssue
	}
	if evt.PR != nil {
		return SurfacePR
	}
	if evt.Issue != nil && evt.Issue.IsPullRequest() {
		return SurfacePR
	}
	return SurfaceIssue
}
