package dispatcher

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
)

// postGateComment posts a comment on the issue/PR using the agent's Gitea token.
func (d *Dispatcher) postGateComment(agent *store.Agent, repo string, issueID int, body string) {
	giteaCfg := d.giteaCfg.Load()
	if giteaCfg == nil || agent.GiteaToken == "" || issueID == 0 {
		return
	}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return
	}
	client := gitea.NewClient(giteaCfg.URL, agent.GiteaToken)
	commentBody := workflow.FormatAgentComment(body)
	if err := client.IssueComment(parts[0], parts[1], issueID, commentBody); err != nil {
		log.Printf("[WARN] Failed to post gate comment on %s#%d: %v", repo, issueID, err)
	}
}

// prURLPattern matches PR URLs in task results (e.g. "PR created: http://...").
var prURLPattern = regexp.MustCompile(`PR created: (https?://\S+)`)

// postL3Notification posts an L3 comment notification after task completion,
// if the workflow policy has the corresponding notification enabled.
func (d *Dispatcher) postL3Notification(task *store.Task) {
	if d.wfPolicy == nil || d.giteaCfg.Load() == nil {
		return
	}

	// Load agent for token and name
	agent, err := d.db.GetAgent(task.AgentID)
	if err != nil || agent.GiteaToken == "" {
		return
	}

	switch task.TaskType {
		case "analyze_issue":
			if !d.wfPolicy.Notify.OnAnalyzeDone {
				return
			}
			body := workflow.FormatL3Comment(workflow.L3AnalyzeDone, map[string]string{
				"task_id":    fmt.Sprintf("%d", task.ID),
				"agent_name": agent.GiteaUsername,
			})
			d.postGateComment(agent, task.Repo, effectiveIssueKey(task.IssueID, task.PRID), body)

		case "solve_issue", "fix_bug", "solve_comment":
			if !d.wfPolicy.Notify.OnCoderPROpened {
				return
			}
			// Only notify when a PR was actually created
			if task.PRID == 0 {
				return
			}
			// Extract PR URL from result
			prURL := ""
			if matches := prURLPattern.FindStringSubmatch(task.Result); len(matches) >= 2 {
				prURL = matches[1]
			}
			if prURL == "" {
				return
			}
			body := workflow.FormatL3Comment(workflow.L3CoderPROpened, map[string]string{
				"pr_url": prURL,
			})
			d.postGateComment(agent, task.Repo, effectiveIssueKey(task.IssueID, task.PRID), body)
		}
}
