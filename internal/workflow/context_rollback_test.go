package workflow

import (
	"testing"

	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 1.5.2A: 回复类任务完成后回落 stage，避免滞留。

// TestOnTaskCompleteReplyCommentRollsBackFromAnalyzing 覆盖核心缺陷：
// @提及 analyze Agent 触发 reply_comment，stage 被推到 analyzing，
// 任务完成后必须回落，否则后续 analyze/coder 触发会被「分析进行中」吞掉。
func TestOnTaskCompleteReplyCommentRollsBackFromAnalyzing(t *testing.T) {
	db := newTestDB(t)
	mgr := NewWorkflowManager(db)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 40)
	require.NoError(t, err)

	agent := &store.Agent{Name: "analyzer40", GiteaUsername: "analyzer40", GiteaToken: "t", Role: store.RoleAnalyze, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	result := mgr.Transition(ctx, store.RoleAnalyze)
	require.True(t, result.Allowed)
	require.NoError(t, mgr.ApplyTransition(ctx, result, agent.ID, store.RoleAnalyze, "sess-reply"))
	require.Equal(t, store.StageAnalyzing, ctx.Stage)
	require.Equal(t, store.StageIdle, ctx.PreviousStage)

	require.NoError(t, mgr.OnTaskComplete(ctx, "reply_comment", 0, "sess-reply"))

	got, err := db.GetWorkflowContext("owner/repo", 40)
	require.NoError(t, err)
	assert.Equal(t, store.StageIdle, got.Stage, "reply_comment 完成后 stage 必须回落，不得滞留 analyzing")
	assert.Equal(t, "", got.PreviousStage)
	assert.Equal(t, int64(0), got.ActiveAgentID)
	assert.Equal(t, "", got.ActiveRole)
	assert.Equal(t, "", got.SessionID)

	// 回归验证：回落后能再次触发分析，不再被 SkipTask 吞掉
	next := mgr.Transition(got, store.RoleAnalyze)
	assert.True(t, next.Allowed)
	assert.False(t, next.SkipTask)
}

// TestOnTaskCompleteReplyCommentRollsBackToAnalyzed 验证回落目标是「推进前的阶段」而非固定 idle。
func TestOnTaskCompleteReplyCommentRollsBackToAnalyzed(t *testing.T) {
	db := newTestDB(t)
	mgr := NewWorkflowManager(db)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 41)
	require.NoError(t, err)
	ctx.Stage = store.StageAnalyzed
	require.NoError(t, db.UpdateWorkflowContext(ctx))

	agent := &store.Agent{Name: "coder41", GiteaUsername: "coder41", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// coder 被 @提及后用 /reply 强制回复模式 → reply_comment
	result := mgr.Transition(ctx, store.RoleCoder)
	require.True(t, result.Allowed)
	require.NoError(t, mgr.ApplyTransition(ctx, result, agent.ID, store.RoleCoder, "sess-r2"))
	require.Equal(t, store.StageDeveloping, ctx.Stage)
	require.Equal(t, store.StageAnalyzed, ctx.PreviousStage)

	require.NoError(t, mgr.OnTaskComplete(ctx, "reply_comment", 0, "sess-r2"))

	got, err := db.GetWorkflowContext("owner/repo", 41)
	require.NoError(t, err)
	assert.Equal(t, store.StageAnalyzed, got.Stage)
	assert.Equal(t, "", got.PreviousStage)
}

// TestOnTaskCompleteReplyCommentSameStageNoRollback 验证同阶段重入不回落：
// reviewing → reviewing 没有真实阶段变化，不应把工作流倒退回 developing。
func TestOnTaskCompleteReplyCommentSameStageNoRollback(t *testing.T) {
	db := newTestDB(t)
	mgr := NewWorkflowManager(db)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 42)
	require.NoError(t, err)
	ctx.Stage = store.StageDeveloping
	require.NoError(t, db.UpdateWorkflowContext(ctx))

	agent := &store.Agent{Name: "reviewer42", GiteaUsername: "reviewer42", GiteaToken: "t", Role: store.RoleReview, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// developing → reviewing：真实变化，记录 PreviousStage
	r1 := mgr.Transition(ctx, store.RoleReview)
	require.NoError(t, mgr.ApplyTransition(ctx, r1, agent.ID, store.RoleReview, "sess-rv1"))
	require.Equal(t, store.StageReviewing, ctx.Stage)
	require.Equal(t, store.StageDeveloping, ctx.PreviousStage)

	// reviewing → reviewing：同阶段重入，PreviousStage 应被清空
	r2 := mgr.Transition(ctx, store.RoleReview)
	require.NoError(t, mgr.ApplyTransition(ctx, r2, agent.ID, store.RoleReview, "sess-rv2"))
	require.Equal(t, store.StageReviewing, ctx.Stage)
	require.Equal(t, "", ctx.PreviousStage)

	require.NoError(t, mgr.OnTaskComplete(ctx, "reply_comment", 0, "sess-rv2"))

	got, err := db.GetWorkflowContext("owner/repo", 42)
	require.NoError(t, err)
	assert.Equal(t, store.StageReviewing, got.Stage, "同阶段重入不应回落到 developing")
	assert.Equal(t, agent.ID, got.ActiveAgentID, "未回落时不应清空活跃 Agent")
}

// TestOnTaskCompleteSolveCommentWithPRStaysDeveloping 验证 1.5.2A 决策：
// solve_comment 产出 PR 时与 solve_issue 一致，保持 developing。
func TestOnTaskCompleteSolveCommentWithPRStaysDeveloping(t *testing.T) {
	db := newTestDB(t)
	mgr := NewWorkflowManager(db)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 43)
	require.NoError(t, err)
	ctx.Stage = store.StageAnalyzed
	require.NoError(t, db.UpdateWorkflowContext(ctx))

	agent := &store.Agent{Name: "coder43", GiteaUsername: "coder43", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	result := mgr.Transition(ctx, store.RoleCoder)
	require.NoError(t, mgr.ApplyTransition(ctx, result, agent.ID, store.RoleCoder, "sess-sc1"))
	require.Equal(t, store.StageDeveloping, ctx.Stage)

	require.NoError(t, mgr.OnTaskComplete(ctx, "solve_comment", 77, "sess-sc1"))

	got, err := db.GetWorkflowContext("owner/repo", 43)
	require.NoError(t, err)
	assert.Equal(t, store.StageDeveloping, got.Stage, "已产出 PR 应保持 developing")
	assert.Equal(t, 77, got.PRID)
	assert.Equal(t, agent.ID, got.ActiveAgentID)
}

// TestOnTaskCompleteSolveCommentWithoutPRRollsBack 验证 1.5.2A 决策的另一半：
// solve_comment 未产出 PR 时回落到推进前的阶段。
func TestOnTaskCompleteSolveCommentWithoutPRRollsBack(t *testing.T) {
	db := newTestDB(t)
	mgr := NewWorkflowManager(db)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 44)
	require.NoError(t, err)
	ctx.Stage = store.StageAnalyzed
	require.NoError(t, db.UpdateWorkflowContext(ctx))

	agent := &store.Agent{Name: "coder44", GiteaUsername: "coder44", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	result := mgr.Transition(ctx, store.RoleCoder)
	require.NoError(t, mgr.ApplyTransition(ctx, result, agent.ID, store.RoleCoder, "sess-sc2"))
	require.Equal(t, store.StageDeveloping, ctx.Stage)
	require.Equal(t, store.StageAnalyzed, ctx.PreviousStage)

	require.NoError(t, mgr.OnTaskComplete(ctx, "solve_comment", 0, "sess-sc2"))

	got, err := db.GetWorkflowContext("owner/repo", 44)
	require.NoError(t, err)
	assert.Equal(t, store.StageAnalyzed, got.Stage, "未产出 PR 应回落到 analyzed")
	assert.Equal(t, 0, got.PRID)
	assert.Equal(t, int64(0), got.ActiveAgentID)
}

// TestTransitionStageClearsPreviousOnSameStage 直接覆盖 store 层的 PreviousStage 记录规则。
func TestTransitionStageClearsPreviousOnSameStage(t *testing.T) {
	db := newTestDB(t)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 45)
	require.NoError(t, err)

	// idle → developing：记录 PreviousStage
	require.NoError(t, db.TransitionStage(ctx, store.StageDeveloping, 0, store.RoleCoder, "s1"))
	assert.Equal(t, store.StageIdle, ctx.PreviousStage)

	// developing → developing：同阶段重入，清空 PreviousStage
	require.NoError(t, db.TransitionStage(ctx, store.StageDeveloping, 0, store.RoleCoder, "s2"))
	assert.Equal(t, "", ctx.PreviousStage)
}
