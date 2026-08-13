package dispatcher

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/deliver"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/workflow"
)

// handleEventV2 processes events through the new Assign-based pipeline.
func (d *Dispatcher) handleEventV2(evt *giteaingress.WebhookEvent) bool {
	// Step 1: Sender filter — skip if sender is any active agent (self-trigger prevention)
	if d.resolver.IsAgentSender(evt) {
		log.Printf("[INFO] Sender %q is an active agent, ignoring event to prevent self-trigger", evt.Sender.Login)
		return true
	}

	// Step 1b: Check for /matea reset command in comments
	if evt.Comment != nil && strings.Contains(evt.Comment.Body, "/matea reset") {
		logicIssue, prID := d.resolver.ResolveLogicIssueAndPR(evt)
		repo := evt.Repo.FullName
		if logicIssue > 0 {
			log.Printf("[INFO] /matea reset for %s#%d (logic_issue=%d pr=%d)", repo, logicIssue, logicIssue, prID)
			d.resetIssue(repo, logicIssue)
			return true
		}
		if prID > 0 {
			// Pure PR: effective key is prID (also clear legacy issue_id=0 if any).
			key := effectiveIssueKey(0, prID)
			log.Printf("[INFO] /matea reset for pure PR %s effective_key=%d (logic_issue=0 pr=%d)", repo, key, prID)
			d.resetIssue(repo, key)
			if key != 0 {
				d.resetIssue(repo, 0) // legacy pure-PR rows before effectiveKey fix
			}
			return true
		}
		if evt.Issue != nil && evt.Issue.Number > 0 {
			log.Printf("[INFO] /matea reset command detected for %s#%d", repo, evt.Issue.Number)
			d.resetIssue(repo, evt.Issue.Number)
			return true
		}
	}

	// Step 2: Resolve event via EventResolver
	result := d.resolver.Resolve(evt)
	if result == nil {
		if evt.Event == "pull_request" && evt.Action == "review_requested" {
			var reviewers []string
			prNum := 0
			if evt.PR != nil {
				prNum = evt.PR.Number
				for _, r := range evt.PR.RequestedReviewers {
					reviewers = append(reviewers, r.Login)
				}
			}
			log.Printf("[INFO] review_requested not handled: repo=%s pr=%d reviewers=%v sender=%q",
				evt.Repo.FullName, prNum, reviewers, evt.Sender.Login)
		} else {
			log.Printf("[DEBUG] Event %s/%s not handled by resolver, ignoring", evt.Event, evt.Action)
		}
		return true // Not an error, just not handled
	}

	repo := evt.Repo.FullName
	logicIssueID := result.IssueID
	prID := result.PRID
	// Session / lock / workflow / task.IssueID / gate comments use effective key.
	// Resolver may return logic_issue_id=0 for pure PRs; fall back to pr_id.
	issueID := effectiveIssueKey(logicIssueID, prID)

	// Handle lifecycle events (archive, done) — no agent required
	if result.Lifecycle != "" {
		log.Printf("[INFO] Lifecycle event: %s logic_issue_id=%d pr_id=%d merged=%v",
			result.Lifecycle, logicIssueID, prID, result.Merged)
		return d.handleLifecycleEvent(result, repo)
	}

	// Task-creating events require an agent
	if result.Agent == nil {
		log.Printf("[DEBUG] No agent resolved for %s/%s, ignoring", evt.Event, evt.Action)
		return true
	}

	log.Printf("[INFO] Resolved: agent=%q agent_id=%d role=%s taskType=%s logic_issue_id=%d pr_id=%d effective_key=%d",
		result.Agent.Name, result.Agent.ID, result.Role, result.TaskType, logicIssueID, prID, issueID)

	// Step 3: L1 gate check
	if d.l1Gate != nil {
		l1Result := d.l1Gate.CheckL1(evt, result.Role, result.Agent)
		if !l1Result.Allowed {
			log.Printf("[INFO] L1 gate rejected: %s — %s", l1Result.Code, l1Result.Message)
			// Post rejection comment via agent's token
			d.postGateComment(result.Agent, repo, issueID, l1Result.Message)
			return true
		}
	}

	// Step 4: WorkflowContext transition
	var sessionID string
	var sessionBranch string
	if d.wfMgr != nil {
		ctx, err := d.db.GetOrCreateWorkflowContext(repo, issueID)
		if err != nil {
			log.Printf("[ERROR] Failed to get workflow context: %v", err)
			return false
		}

		transition := d.wfMgr.Transition(ctx, result.Role)
		if !transition.Allowed {
			log.Printf("[INFO] Transition blocked: %s", transition.Reason)
			d.postGateComment(result.Agent, repo, issueID, "⚠️ "+transition.Reason)
			return true
		}

		// Step 4b: L2 gate evaluation (if policy is configured)
		if d.wfPolicy != nil {
			policy := d.getEffectivePolicy(repo)
			if policy == nil {
				policy = d.wfPolicy
			}
			// Check relevant gates based on the transition
			gatesToCheck := d.gatesForTransition(ctx, result.Role)
			// Determine if PR is a draft (for review_warn_if_draft gate)
			isDraftPR := evt.PR != nil && evt.PR.Draft
			for _, gateID := range gatesToCheck {
				gateResult := workflow.EvaluateGate(policy, gateID, ctx, result.Role, result.Agent.ID, isDraftPR)
				if !gateResult.Allowed {
					if result.Force && gateResult.Level == "soft" {
						// /force bypasses soft gates
						log.Printf("[INFO] /force bypassing soft gate: %s", gateID)
						d.postGateComment(result.Agent, repo, issueID, "⚡ /force 已跳过软门禁: "+gateResult.Message)
						continue
					}
					// Hard gate or no /force — block
					log.Printf("[INFO] L2 gate rejected: %s — %s", gateID, gateResult.Message)
					d.postGateComment(result.Agent, repo, issueID, gateResult.Message)
					return true
				}
				if gateResult.Level == "soft" && policy.Notify.OnGateSoft {
					// Soft gate passed — post warning
					d.postGateComment(result.Agent, repo, issueID, gateResult.Message)
				}
			}
		}

		// Step 5: In-flight lock
		lockKey := fmt.Sprintf("%s#%d", repo, issueID)
		if _, loaded := d.inFlight.LoadOrStore(lockKey, true); loaded {
			log.Printf("[INFO] In-flight lock held for %s, skipping", lockKey)
			d.postGateComment(result.Agent, repo, issueID, "⏳ 此 Issue 正在处理中，请稍候。")
			return true
		}

		// Step 6: Check for existing pending/running tasks
		hasPending, err := d.db.HasPendingOrRunningTask(repo, issueID)
		if err != nil {
			d.inFlight.Delete(lockKey)
			log.Printf("[ERROR] Failed to check pending tasks: %v", err)
			return false
		}
		if hasPending {
			d.inFlight.Delete(lockKey)
			log.Printf("[INFO] Pending/running task exists for %s#%d, skipping", repo, issueID)
			d.postGateComment(result.Agent, repo, issueID, "⏳ 已有任务正在处理中。")
			return true
		}

		// Step 6b: Get or create session
		if d.sessionSvc != nil {
			session, err := d.sessionSvc.GetOrCreate(repo, issueID, result.Agent.ID, result.Role)
			if err != nil {
				d.inFlight.Delete(lockKey)
				log.Printf("[ERROR] Failed to get/create session: %v", err)
				return false
			}
			sessionID = session.ID
			if result.Role == store.RoleCoder && session.Branch != "" {
				sessionBranch = session.Branch
			}
		}

		// Save previous agent ID before ApplyTransition overwrites it
		prevAgentID := ctx.ActiveAgentID

		// Apply transition to DB
		if err := d.wfMgr.ApplyTransition(ctx, transition, result.Agent.ID, result.Role, sessionID); err != nil {
			d.inFlight.Delete(lockKey)
			log.Printf("[ERROR] Failed to apply transition: %v", err)
			return false
		}

		// Step 6c: Unassign previous agent on stage transition (if configured)
		d.unassignPreviousAgentOnTransition(repo, issueID, prevAgentID, result.Agent.ID)
	}

	// Step 7: Build task context and enqueue
	taskContext := d.buildTaskContext(evt, result.Agent, result.TaskType)

	task := &store.Task{
		Event:      evt.Event,
		Repo:       repo,
		IssueID:    issueID, // effective key (logic issue, or prID for pure PR)
		PRID:       prID,
		AgentID:    result.Agent.ID,
		TaskType:   result.TaskType,
		Context:    taskContext,
		Status:     "pending",
		Priority:   10, // Default priority for v2
		Role:       result.Role,
		SessionID:  sessionID,
		DeliveryID: evt.DeliveryID,
	}

	// PR head branch, or session branch for coder continuation when webhook omits pull_request.
	if evt.PR != nil && evt.PR.Head.Ref != "" {
		task.BaseBranch = strings.TrimSpace(evt.PR.Head.Ref)
	}
	if task.BaseBranch == "" && sessionBranch != "" {
		task.BaseBranch = sessionBranch
	}

	if err := d.queue.Enqueue(task); err != nil {
		// Release in-flight lock on error
		lockKey := fmt.Sprintf("%s#%d", repo, issueID)
		d.inFlight.Delete(lockKey)
		log.Printf("[ERROR] Failed to enqueue task: %v", err)
		return false
	}

	log.Printf("[INFO] Task %d enqueued: agent_id=%d agent=%s role=%s type=%s logic_issue_id=%d pr_id=%d effective_key=%d session_id=%s",
		task.ID, result.Agent.ID, result.Agent.Name, result.Role, result.TaskType, logicIssueID, prID, issueID, sessionID)

	// Post progress comment
	d.postGateComment(result.Agent, repo, issueID,
		fmt.Sprintf("🔄 %s 已开始处理（task #%d）", result.Agent.Name, task.ID))

	return true
}

// resetIssue archives all sessions and resets the workflow context to idle.
func (d *Dispatcher) resetIssue(repo string, issueID int) {
	// Archive sessions
	if d.sessionSvc != nil {
		d.sessionSvc.ArchiveByIssue(repo, issueID)
	}

	// Reset context
	ctx, err := d.db.GetWorkflowContext(repo, issueID)
	if err == nil {
		ctx.Stage = store.StageIdle
		ctx.PreviousStage = ""
		ctx.ActiveAgentID = 0
		ctx.ActiveRole = ""
		ctx.SessionID = ""
		d.db.UpdateWorkflowContext(ctx)
	}

	// Release in-flight lock
	d.releaseIssueLock(repo, issueID)

	log.Printf("[INFO] Reset %s#%d: sessions archived, context=idle", repo, issueID)
}

// handleLifecycleEvent handles lifecycle events (archive, done) from the Resolver.
func (d *Dispatcher) handleLifecycleEvent(result *workflow.ResolveResult, repo string) bool {
	if d.lifecycle == nil {
		return true
	}

	switch result.Lifecycle {
	case "archive":
		if result.PRID > 0 {
			// PR closed
			if err := d.lifecycle.OnPRClosed(repo, result.PRID, result.IssueID, result.Merged); err != nil {
				log.Printf("[WARN] PR lifecycle error: %v", err)
			}
			// PR merged → fan out a deliver event (task 2.4.3). The merge
			// webhook never reaches a runner, so the completion-style
			// task_completed event cannot cover it; emit it here instead.
			// A nil/disabled client is a silent no-op.
			if result.Merged {
				d.emitPrMerged(repo, result.PRID, result.IssueID)
			}
		} else if result.IssueID > 0 {
			// Issue closed
			if err := d.lifecycle.OnIssueClosed(repo, result.IssueID); err != nil {
				log.Printf("[WARN] Issue lifecycle error: %v", err)
			}
		}
	}
	return true
}

// emitPrMerged fans out a deliver event when a PR is merged. task_completed
// (task finish) is emitted by the runners; pr_merged is the lifecycle-side
// counterpart emitted here because the merge event never reaches a runner.
// Safe to call with a nil/disabled deliver client (no-op).
func (d *Dispatcher) emitPrMerged(repo string, prID, issueID int) {
	if d.deliverClient == nil {
		return
	}
	e := deliver.Event{
		Event:   deliver.EventPrMerged,
		Repo:    repo,
		PRID:    prID,
		IssueID: issueID,
		Action:  "notify",
		Content: fmt.Sprintf("PR %s#%d merged", repo, prID),
	}
	if err := d.deliverClient.Emit(context.Background(), e); err != nil {
		log.Printf("[WARN] deliver emit (pr_merged) failed for %s#%d: %v", repo, prID, err)
		return
	}
	log.Printf("[INFO] deliver event pr_merged fanned out for %s#%d", repo, prID)
}
