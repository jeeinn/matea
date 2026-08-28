package dispatcher

import (
	"fmt"
	"log"
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

	targetID := effectiveIssueKey(task.IssueID, task.PRID)

	switch task.TaskType {
	case "analyze_issue":
		detail := ""
		if d.wfPolicy.Notify.OnAnalyzeDone {
			detail = workflow.FormatL3Comment(workflow.L3AnalyzeDone, map[string]string{
				"task_id":    fmt.Sprintf("%d", task.ID),
				"agent_name": agent.GiteaUsername,
			})
		}
		d.completeStatusCard(agent, task, targetID, detail)

	case "solve_issue", "fix_bug", "solve_comment":
		detail := ""
		// Guidance only when a PR was actually created and the notice is on.
		if d.wfPolicy.Notify.OnCoderPROpened && task.PRID > 0 {
			giteaCfg := d.giteaCfg.Load()
			prURL := fmt.Sprintf("%s/%s/pulls/%d", strings.TrimRight(giteaCfg.URL, "/"), task.Repo, task.PRID)
			detail = workflow.FormatL3Comment(workflow.L3CoderPROpened, map[string]string{
				"pr_url": prURL,
			})
		}
		d.completeStatusCard(agent, task, targetID, detail)
	}
}

// completeStatusCard flips the task's card to "完成", folding the L3 guidance
// into it. If the card cannot be written at all, the guidance is posted as a
// plain comment instead — the user should learn the next step even when the
// card machinery is broken.
func (d *Dispatcher) completeStatusCard(agent *store.Agent, task *store.Task, targetID int, detail string) {
	if err := d.finishStatusCard(agent, task, targetID, detail); err != nil {
		log.Printf("[WARN] Task %d status card not updated (%v); falling back to a plain comment", task.ID, err)
		if detail != "" {
			d.postGateComment(agent, task.Repo, targetID, detail)
		}
	}
}
