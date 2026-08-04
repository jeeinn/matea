package workflow

import (
	"log"
	"regexp"
	"strconv"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/webhook"
)

// ResolveResult holds the resolution of a webhook event.
type ResolveResult struct {
	Agent    *store.Agent
	TaskType string
	Role     string
	IssueID  int
	PRID     int
	Force    bool // /force detected — skip soft gate warnings

	// Lifecycle events (not task-creating)
	Lifecycle string // "archive" — archive sessions and update context to done
	Merged    bool   // true if PR was merged (not just closed)
}

// Resolver resolves webhook events to agent + task type.
// Unified intent: all triggers (Assign / Request Review / @mention) pull an agent into the conversation.
type Resolver struct {
	registry *agents.Registry
}

// NewResolver creates a new Event Resolver.
func NewResolver(registry *agents.Registry) *Resolver {
	return &Resolver{registry: registry}
}

// linkedIssuePattern matches "Fixes #N", "Closes #N", "Resolves #N" in PR bodies.
var linkedIssuePattern = regexp.MustCompile(`(?i)(?:fix(?:es|ed)?|close[sd]?|resolve[sd]?)\s+#(\d+)`)

// mentionPattern matches @username in comment bodies.
var mentionPattern = regexp.MustCompile(`@(\w[\w-]*)`)

// slashCommandPattern matches /command at line start (ignoring code blocks).
// Matches: /dev, /reply, /force at the beginning of a line.
var slashCommandPattern = regexp.MustCompile(`(?m)^/(\w+)`)

// hasSlashCommand checks if a slash command appears at line start, excluding code blocks.
func hasSlashCommand(body, command string) bool {
	// Strip code blocks first to avoid false positives
	body = stripCodeBlocks(body)

	matches := slashCommandPattern.FindAllStringSubmatch(body, -1)
	for _, match := range matches {
		if len(match) >= 2 && match[1] == command {
			return true
		}
	}
	return false
}

// stripCodeBlocks removes markdown code blocks (``` and `) from text.
func stripCodeBlocks(text string) string {
	// Remove triple-backtick blocks (greedy match anything between ```)
	tripleBacktick := regexp.MustCompile("(?s)```.*?```")
	text = tripleBacktick.ReplaceAllString(text, "")

	// Remove inline code
	inlineCode := regexp.MustCompile("`[^`]*`")
	text = inlineCode.ReplaceAllString(text, "")

	return text
}


// Resolve determines what to do with a webhook event.
// Returns nil if the event should be ignored.
func (r *Resolver) Resolve(evt *webhook.WebhookEvent) *ResolveResult {
	switch evt.Event {
	case "issues":
		return r.resolveIssue(evt)
	case "pull_request":
		return r.resolvePullRequest(evt)
	case "issue_comment", "pull_request_comment":
		return r.resolveComment(evt)
	default:
		return nil
	}
}

// resolveIssue handles issue events.
func (r *Resolver) resolveIssue(evt *webhook.WebhookEvent) *ResolveResult {
	switch evt.Action {
	case "assigned":
		return r.resolveAssigned(evt)
	case "closed":
		return r.resolveIssueClosed(evt)
	case "unassigned":
		// v2: unassigned does not trigger tasks or revert stage
		return nil
	case "labeled", "label_updated":
		// v2: label events do not trigger tasks
		return nil
	default:
		return nil
	}
}

// resolveIssueClosed handles issues.closed events — archive sessions, set context to done.
func (r *Resolver) resolveIssueClosed(evt *webhook.WebhookEvent) *ResolveResult {
	issueID := 0
	if evt.Issue != nil {
		issueID = evt.Issue.Number
	}
	return &ResolveResult{
		IssueID:   issueID,
		Lifecycle: "archive",
	}
}

// resolveAssigned handles issues.assigned events — pulls the assigned agent into the issue conversation.
// Uses ONLY the single assignee from the webhook payload (not the full assignees list).
func (r *Resolver) resolveAssigned(evt *webhook.WebhookEvent) *ResolveResult {
	if evt.Assignee == nil {
		log.Printf("[DEBUG] issues.assigned event with no assignee field, ignoring")
		return nil
	}

	username := evt.Assignee.Login
	agent := r.registry.GetByGiteaUsername(username)
	if agent == nil {
		log.Printf("[DEBUG] Assigned agent %q not in registry, ignoring", username)
		return nil
	}

	if agent.Role == "" {
		log.Printf("[WARN] Agent %q has no role configured, defaulting to analyze", agent.Name)
	}

	role := agent.Role
	if role == "" {
		role = store.RoleAnalyze
	}

	// Resolve task type from the (role, surface, intent) table.
	// review+issue+assign has no mapping → task not created (previously it
	// produced review_pr and was rejected later by the L1 gate; resolving
	// to nil here is the same externally-visible outcome, one step earlier).
	surface := DetermineSurface(evt)
	taskType := ResolveTaskType(role, surface, IntentAssign, evt)
	if taskType == "" {
		log.Printf("[DEBUG] No task type mapping for role=%q surface=%q intent=%q, ignoring", role, surface, IntentAssign)
		return nil
	}

	// Use ResolveLogicIssueAndPR to support PR context (same as comment path)
	issueID, prID := r.ResolveLogicIssueAndPR(evt)

	return &ResolveResult{
		Agent:    agent,
		TaskType: taskType,
		Role:     role,
		IssueID:  issueID,
		PRID:     prID,
	}
}

// resolvePullRequest handles pull_request events.
func (r *Resolver) resolvePullRequest(evt *webhook.WebhookEvent) *ResolveResult {
	if evt.PR == nil {
		return nil
	}

	switch evt.Action {
	case "review_requested", "opened":
		return r.resolveReviewRequested(evt)
	case "closed":
		return r.resolvePRClosed(evt)
	default:
		return nil
	}
}

// resolveReviewRequested handles pull_request review_requested events — pulls the review agent into the PR conversation.
func (r *Resolver) resolveReviewRequested(evt *webhook.WebhookEvent) *ResolveResult {
	// Find a review agent among requested reviewers
	if evt.PR.RequestedReviewers == nil || len(evt.PR.RequestedReviewers) == 0 {
		return nil
	}

	for _, reviewer := range evt.PR.RequestedReviewers {
		agent := r.registry.GetByGiteaUsername(reviewer.Login)
		if agent != nil && agent.Role == store.RoleReview {
			// Resolve task type from the (role, surface, intent) table
			taskType := ResolveTaskType(store.RoleReview, SurfacePR, IntentReviewRequested, evt)
			if taskType == "" {
				return nil
			}

			// Try to resolve linked issue from PR body
			issueID := r.resolveLinkedIssue(evt)

			return &ResolveResult{
				Agent:    agent,
				TaskType: taskType,
				Role:     store.RoleReview,
				IssueID:  issueID,
				PRID:     evt.PR.Number,
			}
		}
	}

	return nil
}

// resolvePRClosed handles pull_request.closed events.
// Merged PR → archive sessions; closed without merge → retain for pr_closed_retention.
func (r *Resolver) resolvePRClosed(evt *webhook.WebhookEvent) *ResolveResult {
	// Gitea sends state="closed" with merged=true when a PR is merged.
	// The state field is NEVER "merged" — that was the old bug.
	merged := evt.PR != nil && evt.PR.Merged

	issueID := r.resolveLinkedIssue(evt)

	return &ResolveResult{
		IssueID:   issueID,
		PRID:      evt.PR.Number,
		Lifecycle: "archive",
		Merged:    merged,
	}
}

// resolveLinkedIssue extracts the linked issue number from PR / PR-as-issue body
// (Fixes/Closes/Resolves #N). Returns 0 if none found.
func (r *Resolver) resolveLinkedIssue(evt *webhook.WebhookEvent) int {
	for _, body := range linkedIssueBodies(evt) {
		if n := extractLinkedIssue(body); n > 0 {
			return n
		}
	}
	return 0
}

func linkedIssueBodies(evt *webhook.WebhookEvent) []string {
	if evt == nil {
		return nil
	}
	var bodies []string
	if evt.PR != nil && evt.PR.Body != "" {
		bodies = append(bodies, evt.PR.Body)
	}
	// issue_comment on a PR: Gitea puts the PR body on evt.Issue (number = PR#).
	if evt.Issue != nil && evt.Issue.Body != "" && (evt.PR != nil || evt.Issue.IsPullRequest()) {
		bodies = append(bodies, evt.Issue.Body)
	}
	return bodies
}

func extractLinkedIssue(body string) int {
	matches := linkedIssuePattern.FindStringSubmatch(body)
	if len(matches) < 2 {
		return 0
	}
	n, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0
	}
	return n
}

// isPRConversation reports whether the event is about a pull request thread.
func isPRConversation(evt *webhook.WebhookEvent) bool {
	if evt == nil {
		return false
	}
	if evt.PR != nil {
		return true
	}
	return evt.Issue != nil && evt.Issue.IsPullRequest()
}

// ResolveLogicIssueAndPR returns logical identifiers for an event:
//   - logic issue id from Fixes/Closes #N when on a PR; else the Issue number
//   - pr id when the conversation is a PR (0 for plain issues)
//
// Pure PR with no linked issue: issueID=0, prID=P. Downstream must map this
// via effectiveIssueKey (prID) for session/lock/workflow/writeback — do not
// key storage on raw 0.
func (r *Resolver) ResolveLogicIssueAndPR(evt *webhook.WebhookEvent) (issueID, prID int) {
	if isPRConversation(evt) {
		if evt.PR != nil {
			prID = evt.PR.Number
		} else if evt.Issue != nil {
			prID = evt.Issue.Number
		}
		issueID = r.resolveLinkedIssue(evt) // 0 if none
		return issueID, prID
	}
	if evt.Issue != nil {
		issueID = evt.Issue.Number
	}
	return issueID, prID
}

// IsAgentSender checks if the event sender is any active agent (to prevent self-trigger loops).
func (r *Resolver) IsAgentSender(evt *webhook.WebhookEvent) bool {
	return r.registry.GetByGiteaUsername(evt.Sender.Login) != nil
}

// resolveComment handles issue_comment / pull_request_comment events — pulls @mentioned agent into the conversation.
func (r *Resolver) resolveComment(evt *webhook.WebhookEvent) *ResolveResult {
	if evt.Comment == nil {
		return nil
	}

	body := evt.Comment.Body

	// Skip agent comments (loop prevention)
	if IsAgentComment(body) {
		return nil
	}

	// Check for force mode commands (line-start anchored, excluding code blocks)
	forceDev := hasSlashCommand(body, "dev")
	forceReply := hasSlashCommand(body, "reply")

	// Pull agent into conversation via @mention
	agent := r.findMentionedAgent(body)

	// If no explicit @mention, we can't resolve
	if agent == nil {
		return nil
	}

	// Session / Workflow keys use logic issue; PR APIs use prID.
	issueID, prID := r.ResolveLogicIssueAndPR(evt)

	// Derive intent: slash commands override plain mention (dev wins over reply,
	// matching the previous commentTaskType precedence).
	intent := IntentMention
	if forceDev {
		intent = IntentSlashDev
	} else if forceReply {
		intent = IntentSlashReply
	}

	// Resolve task type from the (role, surface, intent) table.
	// Empty role falls back to analyze (previously the switch default returned
	// reply_comment; analyze+mention maps to the same task type).
	role := agent.Role
	if role == "" {
		role = store.RoleAnalyze
	}
	surface := DetermineSurface(evt)
	taskType := ResolveTaskType(role, surface, intent, evt)
	if taskType == "" {
		log.Printf("[DEBUG] No task type mapping for role=%q surface=%q intent=%q, ignoring", role, surface, intent)
		return nil
	}

	// Detect /force for soft gate bypass (line-start anchored, excluding code blocks)
	force := hasSlashCommand(body, "force") && !forceDev && !forceReply

	return &ResolveResult{
		Agent:    agent,
		TaskType: taskType,
		Role:     agent.Role,
		IssueID:  issueID,
		PRID:     prID,
		Force:    force,
	}
}

// findMentionedAgent finds the first @mentioned agent in the comment body.
func (r *Resolver) findMentionedAgent(body string) *store.Agent {
	matches := mentionPattern.FindAllStringSubmatch(body, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			username := match[1]
			agent := r.registry.GetByGiteaUsername(username)
			if agent != nil {
				return agent
			}
		}
	}
	return nil
}
