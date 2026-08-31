package dispatcher

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
)

// getEffectivePolicy returns the effective workflow policy for a repo.
// If a per-repo policy exists in DB, it overrides the global policy.
func (d *Dispatcher) getEffectivePolicy(repo string) *workflow.WorkflowPolicy {
	if d.db == nil {
		return d.wfPolicy
	}

	dbPolicy, err := d.db.GetWorkflowPolicy(repo)
	if err != nil || dbPolicy == nil {
		return d.wfPolicy
	}

	var gateOverrides map[string]string
	if dbPolicy.GatesJSON != "" {
		_ = json.Unmarshal([]byte(dbPolicy.GatesJSON), &gateOverrides)
	}

	return workflow.BuildPolicy(dbPolicy.Preset, gateOverrides)
}

// gatesForTransition returns the L2 gate IDs to check for the given transition.
func (d *Dispatcher) gatesForTransition(ctx *store.WorkflowContext, role string) []string {
	var gates []string

	switch role {
	case store.RoleCoder:
		if ctx.Stage == store.StageIdle || ctx.Stage == store.StageDone {
			gates = append(gates, workflow.GateCoderRequiresAnalyzed)
		}
		if ctx.Stage == store.StageDeveloping {
			gates = append(gates, workflow.GateRerunSameStage)
		}
		// Check if switching to a different coder agent
		if ctx.ActiveAgentID != 0 {
			gates = append(gates, workflow.GateCoderSwitchAgent)
		}
	case store.RoleAnalyze:
		if ctx.Stage == store.StageDeveloping {
			gates = append(gates, workflow.GateReanalyzeWhileDev)
		}
		if ctx.Stage == store.StageAnalyzing {
			gates = append(gates, workflow.GateRerunSameStage)
		}
	case store.RoleReview:
		// Draft PR warning for review tasks
		gates = append(gates, workflow.GateReviewWarnIfDraft)
		// Maker ≠ Checker: same agent as active coder
		if ctx.ActiveAgentID != 0 {
			gates = append(gates, workflow.GateReviewNotSameCoder)
		}
	}

	return gates
}

// unassignPreviousAgentOnTransition removes the previous agent from the issue's assignee
// when the stage transitions to a different role. This is controlled by the
// stage_transition_unassign gate policy.
func (d *Dispatcher) unassignPreviousAgentOnTransition(repo string, issueID, prID int, prevAgentID int64, newAgentID int64) {
	giteaCfg := d.giteaCfg.Load()
	if d.wfPolicy == nil || giteaCfg == nil {
		return
	}

	policy := d.getEffectivePolicy(repo)
	if policy == nil {
		policy = d.wfPolicy
	}

	level := policy.GetGateLevel(workflow.GateStageTransitionUnassign)
	if level == workflow.GateOff {
		return
	}

	if prevAgentID == 0 || prevAgentID == newAgentID {
		return
	}

	prevAgent, err := d.db.GetAgent(prevAgentID)
	if err != nil {
		log.Printf("[WARN] Failed to get previous agent %d: %v", prevAgentID, err)
		return
	}

	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return
	}
	owner, repoName := parts[0], parts[1]

	if prevAgent.GiteaUsername == "" {
		log.Printf("[WARN] Previous agent %d has empty gitea_username, skip unassign", prevAgentID)
		return
	}

	// The agent was assigned on the conversation the user acted in, which for
	// a PR task is the PR even though the task is keyed to the linked issue.
	target := conversationTarget(issueID, prID)

	giteaClient := gitea.NewClient(giteaCfg.URL, giteaCfg.AdminToken)
	if err := giteaClient.IssueUnassign(owner, repoName, target, prevAgent.GiteaUsername); err != nil {
		log.Printf("[WARN] Failed to unassign agent %s from %s#%d: %v",
			prevAgent.GiteaUsername, repo, target, err)
		if level == workflow.GateHard {
			d.postGateComment(prevAgent, repo, target,
				fmt.Sprintf("⚠️ 未能从 Issue 移除前一 Agent [%s] 的分配，已按配置继续执行", prevAgent.GiteaUsername))
		}
	} else {
		log.Printf("[INFO] Unassigned agent %s from %s#%d on stage transition",
			prevAgent.GiteaUsername, repo, target)
	}
}
