package workflow

import (
	"fmt"
	"log"

	"github.com/jeeinn/matea/internal/store"
)

// WorkflowManager manages WorkflowContext state transitions.
type WorkflowManager struct {
	db *store.DB
}

// NewWorkflowManager creates a new WorkflowManager.
func NewWorkflowManager(db *store.DB) *WorkflowManager {
	return &WorkflowManager{db: db}
}

// TransitionResult holds the result of a stage transition attempt.
type TransitionResult struct {
	Allowed  bool   // Whether the transition is allowed
	NewStage string // The stage to transition to (if Allowed)
	Reason   string // Human-readable reason (for comments)
	SkipTask bool   // If true, the task should not be enqueued
}

// Transition evaluates whether a stage transition is allowed and returns the result.
// This does NOT modify the database — the caller is responsible for applying the transition
// after successful L2 gate check and task enqueue.
func (m *WorkflowManager) Transition(ctx *store.WorkflowContext, role string) TransitionResult {
	currentStage := ctx.Stage

	switch role {
	case store.RoleAnalyze:
		return m.transitionAnalyze(currentStage)
	case store.RoleCoder:
		return m.transitionCoder(currentStage)
	case store.RoleReview:
		return m.transitionReview(currentStage)
	default:
		return TransitionResult{Allowed: false, Reason: fmt.Sprintf("unknown role: %s", role)}
	}
}

// transitionAnalyze handles analyze role transitions.
func (m *WorkflowManager) transitionAnalyze(currentStage string) TransitionResult {
	switch currentStage {
	case store.StageIdle, store.StageAnalyzed, store.StageDone:
		return TransitionResult{Allowed: true, NewStage: store.StageAnalyzing}
	case store.StageAnalyzing:
		// Already analyzing — in-flight check should catch this
		return TransitionResult{Allowed: false, SkipTask: true, Reason: "分析任务正在进行中"}
	case store.StageDeveloping:
		// Re-analyze while developing — allowed with soft warning (L2 handles the warning)
		return TransitionResult{Allowed: true, NewStage: store.StageAnalyzing, Reason: "开发阶段中重新分析，可能中断当前开发"}
	case store.StageReviewing:
		// Re-analyze while reviewing — allowed with soft warning
		return TransitionResult{Allowed: true, NewStage: store.StageAnalyzing, Reason: "审查阶段中重新分析"}
	default:
		return TransitionResult{Allowed: false, Reason: fmt.Sprintf("无法从阶段 %s 转换到 analyzing", currentStage)}
	}
}

// transitionCoder handles coder role transitions.
func (m *WorkflowManager) transitionCoder(currentStage string) TransitionResult {
	switch currentStage {
	case store.StageIdle:
		// Idle → developing (allowed if allow_skip_analyze is true, which is the default)
		return TransitionResult{Allowed: true, NewStage: store.StageDeveloping}
	case store.StageAnalyzed:
		return TransitionResult{Allowed: true, NewStage: store.StageDeveloping}
	case store.StageDeveloping:
		// Already developing — in-flight check should catch same-task; re-run same stage handled by L2
		return TransitionResult{Allowed: true, NewStage: store.StageDeveloping, Reason: "开发阶段重新执行"}
	case store.StageReviewing:
		// @coder continuation from review — allowed
		return TransitionResult{Allowed: true, NewStage: store.StageDeveloping, Reason: "从审查阶段回到开发"}
	case store.StageDone:
		return TransitionResult{Allowed: true, NewStage: store.StageDeveloping, Reason: "从完成状态重新开发"}
	case store.StageAnalyzing:
		return TransitionResult{Allowed: false, Reason: "分析进行中，请等待分析完成后再开始开发"}
	default:
		return TransitionResult{Allowed: false, Reason: fmt.Sprintf("无法从阶段 %s 转换到 developing", currentStage)}
	}
}

// transitionReview handles review role transitions.
func (m *WorkflowManager) transitionReview(currentStage string) TransitionResult {
	// Review is always allowed (structural gate L1 checks for open PR)
	return TransitionResult{Allowed: true, NewStage: store.StageReviewing}
}

// ApplyTransition updates the WorkflowContext in the database after a successful transition.
func (m *WorkflowManager) ApplyTransition(ctx *store.WorkflowContext, result TransitionResult, agentID int64, role, sessionID string) error {
	if !result.Allowed {
		return fmt.Errorf("transition not allowed: %s", result.Reason)
	}
	return m.db.TransitionStage(ctx, result.NewStage, agentID, role, sessionID)
}

// OnTaskComplete updates the WorkflowContext stage after a task completes successfully.
func (m *WorkflowManager) OnTaskComplete(ctx *store.WorkflowContext, taskType string, prID int, sessionID string) error {
	switch taskType {
	case "analyze_issue":
		ctx.Stage = store.StageAnalyzed
	case "solve_issue", "fix_bug":
		// Stay in developing; write PR ID if available
		if prID > 0 {
			ctx.PRID = prID
		}
	case "review_pr":
		// Stay in reviewing; write PR ID if available (for L1 gate check)
		if prID > 0 {
			ctx.PRID = prID
		}
	case "reply_comment":
		// 纯回复类任务不推进工作流：mention/斜杠命令入口在 pipeline 中统一调用了
		// Transition() 把 stage 推到 analyzing/developing/reviewing，若完成时不回落，
		// stage 会滞留在中间态（analyzing 尤其致命，会让后续 analyze/coder 触发被
		// 「分析进行中」直接吞掉）。这里回落到本次推进前的阶段。
		if prID > 0 {
			ctx.PRID = prID
		}
		m.rollbackTransientStage(ctx, taskType)
	case "solve_comment":
		if prID > 0 {
			// 已产出 PR：与 solve_issue 保持一致，停留在 developing 等待后续审查
			ctx.PRID = prID
		} else {
			// 未产出 PR：本次触发没有实际推进工作流，回落到推进前的阶段
			m.rollbackTransientStage(ctx, taskType)
		}
	default:
		log.Printf("[WARN] Unknown task type %s for stage update", taskType)
	}
	return m.db.UpdateWorkflowContext(ctx)
}

// rollbackTransientStage 把「由回复类任务临时推进的 stage」回落到推进前的阶段。
//
// 规则：
//   - PreviousStage 为空 → 本次触发是同阶段重入（TransitionStage 已清空），
//     没有可回落的前序阶段，保持现状。
//   - PreviousStage 为 analyzing → analyzing 是「分析进行中」的瞬时标记，
//     不能作为回落目标（否则会重新制造滞留），按 idle 处理，与 OnTaskFailed 一致。
//   - 回落目标与当前 stage 相同 → 仅清理 PreviousStage。
//
// 回落时一并清空 ActiveAgentID/ActiveRole/SessionID，语义与 OnTaskFailed 的回滚一致：
// 该 Agent 已完成本次回复，不再持有工作流所有权。
func (m *WorkflowManager) rollbackTransientStage(ctx *store.WorkflowContext, taskType string) {
	rollback := ctx.PreviousStage
	if rollback == "" {
		return
	}
	if rollback == store.StageAnalyzing {
		rollback = store.StageIdle
	}
	if rollback == ctx.Stage {
		ctx.PreviousStage = ""
		return
	}

	from := ctx.Stage
	ctx.Stage = rollback
	ctx.PreviousStage = ""
	ctx.ActiveAgentID = 0
	ctx.ActiveRole = ""
	ctx.SessionID = ""
	log.Printf("[INFO] Rolled back workflow %s#%d stage: %s → %s (%s completed, workflow not advanced)",
		ctx.Repo, ctx.IssueID, from, rollback, taskType)
}

// OnTaskFailed rolls back workflow stage after a task fails.
// analyze_issue failures revert analyzing → previous stage (or idle).
func (m *WorkflowManager) OnTaskFailed(ctx *store.WorkflowContext, taskType string) error {
	switch taskType {
	case "analyze_issue":
		if ctx.Stage != store.StageAnalyzing {
			return nil
		}
		rollback := ctx.PreviousStage
		if rollback == "" || rollback == store.StageAnalyzing {
			rollback = store.StageIdle
		}
		ctx.Stage = rollback
		ctx.PreviousStage = ""
		ctx.ActiveAgentID = 0
		ctx.ActiveRole = ""
		ctx.SessionID = ""
		log.Printf("[INFO] Rolled back workflow %s#%d stage: analyzing → %s (analyze task failed)",
			ctx.Repo, ctx.IssueID, rollback)
		return m.db.UpdateWorkflowContext(ctx)
	default:
		// solve_issue, review_pr, etc. keep their current stage
		return nil
	}
}
