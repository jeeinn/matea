package dispatcher

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
)

// postStatusCard opens the task's status card in the running state. Called
// right after the task is enqueued, in place of the old "🔄 已开始处理" comment
// — that comment used to sit there forever whenever a task never reached a
// terminal state.
func (d *Dispatcher) postStatusCard(agent *store.Agent, task *store.Task, targetID int) {
	if err := updateStatusCard(d, d.db, agent, task, targetID, workflow.StatusCard{
		TaskID:    task.ID,
		AgentName: agent.Name,
		Role:      workflow.RoleLabel(task.Role),
		State:     workflow.StatusCardRunning,
		StartedAt: time.Now(),
		Trigger:   statusCardTrigger(agent),
	}); err != nil {
		// No fallback comment: this replaced "已开始处理", and re-adding that
		// noise on failure would defeat the change.
		log.Printf("[WARN] Task %d: %v", task.ID, err)
	}
}

// finishStatusCard marks the task's card as successfully completed. detail
// carries the caller's guidance text (e.g. the L3 "next step" notice); pass ""
// when there is none.
func (d *Dispatcher) finishStatusCard(agent *store.Agent, task *store.Task, targetID int, detail string) error {
	startedAt, elapsed := statusCardTiming(task)
	return updateStatusCard(d, d.db, agent, task, targetID, workflow.StatusCard{
		TaskID:    task.ID,
		AgentName: agent.Name,
		Role:      workflow.RoleLabel(task.Role),
		State:     workflow.StatusCardSuccess,
		StartedAt: startedAt,
		Duration:  elapsed,
		Trigger:   statusCardTrigger(agent),
		Detail:    detail,
	})
}

// failStatusCard marks the task's card as failed, carrying the cause.
func failStatusCard(f statusCardClientFactory, db *store.DB, agent *store.Agent, task *store.Task, targetID int, cause string) error {
	startedAt, elapsed := statusCardTiming(task)
	return updateStatusCard(f, db, agent, task, targetID, workflow.StatusCard{
		TaskID:    task.ID,
		AgentName: agent.Name,
		Role:      workflow.RoleLabel(task.Role),
		State:     workflow.StatusCardFailed,
		StartedAt: startedAt,
		Duration:  elapsed,
		Trigger:   statusCardTrigger(agent),
		Detail:    cause,
	})
}

// statusCardClientFactory is the minimum a caller needs to post a card: build a
// Gitea client bound to the agent's own token. Both Dispatcher and Executor
// satisfy it (Dispatcher via its GetGiteaClient method).
type statusCardClientFactory interface {
	GetGiteaClient(token string) *gitea.Client
}

// updateStatusCard renders card and applies it to the task's single progress
// comment: PATCH when one already exists, otherwise create it.
//
// Lookup is two-layer on purpose:
//  1. task.StatusCommentID — the fast path, set when the card was created.
//  2. marker scan over the issue's comments — the recovery path, for a restart
//     that lost the ID, a card created before the ID was persisted, or a card
//     that was deleted underneath us.
//
// Only when both fail does it create a new card, which is what keeps a task to
// one progress comment instead of one per state change.
//
// It returns an error when the card could neither be updated nor created, so
// callers holding information the user must not miss (a failure cause, an L3
// next-step notice) can fall back to a plain comment. Progress chatter has no
// such fallback — losing "已开始处理" is the point, not a regression.
func updateStatusCard(f statusCardClientFactory, db *store.DB, agent *store.Agent, task *store.Task, targetID int, card workflow.StatusCard) error {
	if f == nil || db == nil || agent == nil || task == nil || targetID == 0 {
		return fmt.Errorf("status card: missing prerequisites (task=%v target=%d)", task, targetID)
	}
	if agent.GiteaToken == "" {
		return fmt.Errorf("status card: agent %q has no Gitea token", agent.Name)
	}
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("status card: invalid repo format %q", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	body := workflow.RenderStatusCard(card)
	client := f.GetGiteaClient(agent.GiteaToken)

	// Fast path: we already know the comment ID.
	if task.StatusCommentID > 0 {
		if err := client.EditIssueComment(owner, repo, int(task.StatusCommentID), body); err == nil {
			return nil
		}
		// Card gone or no longer editable — fall through and re-locate it.
		log.Printf("[WARN] Task %d status card %d not updatable; re-locating by marker", task.ID, task.StatusCommentID)
	}

	// Recovery path: find the card by its marker.
	if id, ok := findStatusCard(client, owner, repo, targetID, task.ID); ok {
		if err := client.EditIssueComment(owner, repo, id, body); err == nil {
			saveStatusCardID(db, task, int64(id))
			return nil
		}
		log.Printf("[WARN] Task %d status card %d found by marker but not updatable", task.ID, id)
	}

	// Last resort: no usable card, create one.
	created, err := client.CreateIssueComment(owner, repo, targetID, body)
	if err != nil {
		return fmt.Errorf("create status card on %s#%d: %w", task.Repo, targetID, err)
	}
	saveStatusCardID(db, task, int64(created.ID))
	log.Printf("[INFO] Task %d status card created: comment %d on %s#%d", task.ID, created.ID, task.Repo, targetID)
	return nil
}

// findStatusCard locates a task's card among an issue's comments by marker.
// When several match (e.g. a duplicate slipped through) the newest wins so all
// later updates converge on one card.
func findStatusCard(client *gitea.Client, owner, repo string, targetID int, taskID int64) (int, bool) {
	comments, err := client.IssueComments(owner, repo, targetID)
	if err != nil {
		return 0, false
	}
	marker := workflow.StatusCardMarker(taskID)
	for i := len(comments) - 1; i >= 0; i-- {
		if strings.Contains(comments[i].Body, marker) {
			return comments[i].ID, true
		}
	}
	return 0, false
}

// saveStatusCardID persists the card ID so the next update skips the marker
// scan. A persistence failure is logged, not fatal: the marker scan still
// recovers the card on the next pass.
func saveStatusCardID(db *store.DB, task *store.Task, id int64) {
	if err := db.UpdateTaskStatusCommentID(task.ID, id); err != nil {
		log.Printf("[WARN] Persist status card id for task %d: %v", task.ID, err)
		return
	}
	task.StatusCommentID = id
}

// statusCardTiming derives the card's start stamp and elapsed time from the
// task's own timestamps. StartedAt is set when the task leaves pending;
// FinishedAt only on a terminal state, so a running task reports no duration.
func statusCardTiming(task *store.Task) (startedAt time.Time, elapsed time.Duration) {
	if task.StartedAt == nil {
		return time.Time{}, 0
	}
	startedAt = *task.StartedAt
	if task.FinishedAt == nil {
		return startedAt, 0
	}
	return startedAt, task.FinishedAt.Sub(startedAt)
}

// statusCardTrigger renders how the task was invoked. Agents are triggered by
// @mention or assignment, so the Gitea username is the meaningful label; the
// internal name is only a fallback for agents without a linked account.
func statusCardTrigger(agent *store.Agent) string {
	if agent.GiteaUsername != "" {
		return "@" + agent.GiteaUsername
	}
	if agent.Name != "" {
		return "@" + agent.Name
	}
	return ""
}
