package dispatcher

import (
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

// buildTaskContext constructs the context string for the task from the event.
// If the agent has a user_template, it renders it with the event data.
// Otherwise, it falls back to the default context builder.
// For write-path continuations (solve_comment), recent PR/issue comments —
// especially review-agent feedback — are appended so the coder sees review
// guidance even when the triggering comment only says "fix per review".
func (d *Dispatcher) buildTaskContext(evt *giteaingress.WebhookEvent, agent *store.Agent, taskType string) string {
	base := d.renderTaskContextBase(evt, agent, taskType)
	if needsCommentHistory(taskType) {
		base = d.appendCommentHistory(base, evt)
	}
	return base
}

func (d *Dispatcher) renderTaskContextBase(evt *giteaingress.WebhookEvent, agent *store.Agent, taskType string) string {
	// Try to use agent's user_template first
	if agent.UserTemplate != "" {
		rendered, err := RenderTemplate(agent.UserTemplate, BuildTemplateData(evt))
		if err != nil {
			log.Printf("[WARN] Failed to render user_template: %v, using default", err)
		} else if rendered != "" {
			return rendered
		}
	}

	// Try to use template from config based on task type
	if d.agentsCfg != nil {
		if tmpl, ok := d.agentsCfg.Templates[taskType]; ok && tmpl.UserTemplate != "" {
			data := BuildTemplateData(evt)
			data.Task = &TaskData{TaskType: taskType}
			rendered, err := RenderTemplate(tmpl.UserTemplate, data)
			if err != nil {
				log.Printf("[WARN] Failed to render config template: %v, using default", err)
			} else if rendered != "" {
				return rendered
			}
		}
	}

	return d.buildDefaultContext(evt)
}

func needsCommentHistory(taskType string) bool {
	switch taskType {
	case "solve_comment":
		return true
	default:
		return false
	}
}

// appendCommentHistory fetches recent issue/PR comments and appends them to context.
// Failures are best-effort: the base context is still returned.
func (d *Dispatcher) appendCommentHistory(base string, evt *giteaingress.WebhookEvent) string {
	owner, repo, commentIssueID := commentFetchTarget(evt)
	if owner == "" || repo == "" || commentIssueID <= 0 {
		return base
	}

	client := d.GetAdminGiteaClient()
	if client == nil {
		return base
	}

	comments, err := client.IssueComments(owner, repo, commentIssueID)
	if err != nil {
		log.Printf("[WARN] Failed to fetch comments for %s/%s#%d: %v", owner, repo, commentIssueID, err)
		return base
	}
	if len(comments) == 0 {
		return base
	}

	prefer := d.reviewAgentUsernames()
	selected := selectCommentsForContext(comments, prefer, commentHistoryLimit)
	history := formatCommentHistory(selected, prefer)
	if history == "" {
		return base
	}

	log.Printf("[INFO] Injected %d recent comments into solve_comment context for %s/%s#%d",
		len(selected), owner, repo, commentIssueID)
	return base + history
}

func commentFetchTarget(evt *giteaingress.WebhookEvent) (owner, repo string, issueOrPR int) {
	if evt == nil {
		return "", "", 0
	}
	full := evt.Repo.FullName
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 {
		return "", "", 0
	}
	owner, repo = parts[0], parts[1]

	// Gitea stores PR conversation comments under the PR index via the issues API.
	// Always fetch by PR number (not logic issue id) so review history stays on the PR thread.
	if evt.PR != nil && evt.PR.Number > 0 {
		return owner, repo, evt.PR.Number
	}
	if evt.Issue != nil && evt.Issue.Number > 0 {
		return owner, repo, evt.Issue.Number
	}
	return owner, repo, 0
}

func (d *Dispatcher) reviewAgentUsernames() map[string]bool {
	out := make(map[string]bool)
	if d == nil || d.registry == nil {
		return out
	}
	for _, u := range d.registry.GiteaUsernamesByRole(store.RoleReview) {
		if u != "" {
			out[u] = true
		}
	}
	return out
}

// buildDefaultContext builds the default context string without templates.
func (d *Dispatcher) buildDefaultContext(evt *giteaingress.WebhookEvent) string {
	var sb strings.Builder

	// Add repository info
	sb.WriteString(fmt.Sprintf("Repository: %s\n", evt.Repo.FullName))

	// Add issue/PR info
	if evt.Issue != nil {
		sb.WriteString(fmt.Sprintf("Issue #%d: %s\n", evt.Issue.Number, evt.Issue.Title))
		sb.WriteString(fmt.Sprintf("State: %s\n", evt.Issue.State))
		sb.WriteString(fmt.Sprintf("Author: %s\n", evt.Issue.User.Login))
		if evt.Issue.Body != "" {
			sb.WriteString(fmt.Sprintf("\nBody:\n%s\n", evt.Issue.Body))
		}
		if len(evt.Issue.Labels) > 0 {
			labels := make([]string, len(evt.Issue.Labels))
			for i, l := range evt.Issue.Labels {
				labels[i] = l.Name
			}
			sb.WriteString(fmt.Sprintf("Labels: %s\n", strings.Join(labels, ", ")))
		}
	}

	if evt.PR != nil {
		sb.WriteString(fmt.Sprintf("PR #%d: %s\n", evt.PR.Number, evt.PR.Title))
		sb.WriteString(fmt.Sprintf("State: %s\n", evt.PR.State))
		sb.WriteString(fmt.Sprintf("Author: %s\n", evt.PR.User.Login))
		sb.WriteString(fmt.Sprintf("Head: %s → Base: %s\n", evt.PR.Head.Ref, evt.PR.Base.Ref))
		if evt.PR.Body != "" {
			sb.WriteString(fmt.Sprintf("\nBody:\n%s\n", evt.PR.Body))
		}
	}

	if evt.Comment != nil {
		sb.WriteString(fmt.Sprintf("\nComment by %s:\n%s\n", evt.Comment.User.Login, evt.Comment.Body))
	}

	sb.WriteString(fmt.Sprintf("\nEvent: %s/%s\n", evt.Event, evt.Action))
	sb.WriteString(fmt.Sprintf("Sender: %s\n", evt.Sender.Login))

	return sb.String()
}
