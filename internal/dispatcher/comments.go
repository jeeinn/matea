package dispatcher

import (
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/agents"
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

	// Same target the executor used for the result comment and for failure
	// writeback. This used to be effectiveIssueKey, which prefers the linked
	// issue while writebackTargetID prefers the PR for review_pr — so a
	// review's card was created on one thread and completed on another,
	// leaving the copy the user actually reads stuck on "处理中".
	targetID, ok := writebackTargetID(task)
	if !ok {
		return
	}

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
			detail = workflow.FormatL3Comment(workflow.L3CoderPROpened, map[string]string{
				"pr_ref":        fmt.Sprintf("#%d", task.PRID),
				"reviewer_hint": reviewerHint(d.registry),
			})
		}
		d.completeStatusCard(agent, task, targetID, detail)

	// review_pr and reply_comment used to fall through this switch untouched,
	// so their cards were posted as "处理中" and never flipped: on
	// jeeinn/rust-study PR #8 a review that finished in 56s still shows a card
	// frozen at "🔄 处理中" to this day.
	//
	// detail stays empty — no L3 template exists for these types yet, and
	// flipping the card to 完成 is the card machinery's job rather than a
	// notification's, so it must not depend on a Notify switch.
	case "review_pr", "reply_comment":
		d.completeStatusCard(agent, task, targetID, "")
	}
}

// reviewerHint renders the "what next" suggestion for a card whose task opened
// a PR. It names a real review-role account when one exists, because the old
// wording ("Request reviewer Agent 进行代码审查") told the user to do
// something without saying who to ask; with a name the suggestion is
// copy-pasteable into a PR comment.
func reviewerHint(registry *agents.Registry) string {
	const noReviewer = "在 PR 中指派一个代码审查 Agent 请求复审"
	if registry == nil {
		return noReviewer
	}
	names := registry.GiteaUsernamesByRole("review")
	if len(names) == 0 {
		return noReviewer
	}
	mentions := make([]string, 0, len(names))
	for _, n := range names {
		mentions = append(mentions, "@"+n)
	}
	return "在 PR 中 " + strings.Join(mentions, " 或 ") + " 请求代码审查"
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
