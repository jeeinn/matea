package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Stage constants for WorkflowContext.
const (
	StageIdle       = "idle"
	StageAnalyzing  = "analyzing"
	StageAnalyzed   = "analyzed"
	StageDeveloping = "developing"
	StageReviewing  = "reviewing"
	StageDone       = "done"
)

// Role constants for agents.
const (
	RoleAnalyze = "analyze"
	RoleCoder   = "coder"
	RoleReview  = "review"
)

// WorkflowContext tracks the workflow stage for a repo + issue.
type WorkflowContext struct {
	ID            int64     `json:"id"`
	Repo          string    `json:"repo"`
	IssueID       int       `json:"issue_id"`
	PRID          int       `json:"pr_id"`
	Stage         string    `json:"stage"`
	PreviousStage string    `json:"previous_stage"` // stage before the last real stage change (for failure/reply rollback)
	ActiveAgentID int64     `json:"active_agent_id"`
	ActiveRole    string    `json:"active_role"`
	SessionID     string    `json:"session_id"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// GetOrCreateWorkflowContext returns the existing context or creates a new one in idle stage.
func (db *DB) GetOrCreateWorkflowContext(repo string, issueID int) (*WorkflowContext, error) {
	ctx, err := db.GetWorkflowContext(repo, issueID)
	if err == nil {
		return ctx, nil
	}

	// Create new context in idle stage
	ctx = &WorkflowContext{
		Repo:    repo,
		IssueID: issueID,
		Stage:   StageIdle,
	}
	result, err := db.Exec(`INSERT INTO workflow_contexts (repo, issue_id, stage, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, repo, issueID, StageIdle)
	if err != nil {
		return nil, fmt.Errorf("insert workflow context: %w", err)
	}
	id, _ := result.LastInsertId()
	ctx.ID = id
	ctx.UpdatedAt = time.Now()
	return ctx, nil
}

// GetWorkflowContext returns the context for the given repo and issue.
func (db *DB) GetWorkflowContext(repo string, issueID int) (*WorkflowContext, error) {
	var ctx WorkflowContext
	err := db.QueryRow(`SELECT id, repo, issue_id, pr_id, stage, previous_stage, active_agent_id, active_role, session_id, updated_at
		FROM workflow_contexts WHERE repo = ? AND issue_id = ?`, repo, issueID).Scan(
		&ctx.ID, &ctx.Repo, &ctx.IssueID, &ctx.PRID, &ctx.Stage, &ctx.PreviousStage, &ctx.ActiveAgentID, &ctx.ActiveRole, &ctx.SessionID, &ctx.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get workflow context: %w", err)
	}
	return &ctx, nil
}

// UpdateWorkflowContext updates an existing workflow context.
func (db *DB) UpdateWorkflowContext(ctx *WorkflowContext) error {
	_, err := db.Exec(`UPDATE workflow_contexts SET pr_id=?, stage=?, previous_stage=?, active_agent_id=?, active_role=?, session_id=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`, ctx.PRID, ctx.Stage, ctx.PreviousStage, ctx.ActiveAgentID, ctx.ActiveRole, ctx.SessionID, ctx.ID)
	if err != nil {
		return fmt.Errorf("update workflow context: %w", err)
	}
	return nil
}

// ListWorkflowContextsByRepo returns all workflow contexts for a given repo.
func (db *DB) ListWorkflowContextsByRepo(repo string) ([]*WorkflowContext, error) {
	rows, err := db.Query(`SELECT id, repo, issue_id, pr_id, stage, previous_stage, active_agent_id, active_role, session_id, updated_at
		FROM workflow_contexts WHERE repo = ? ORDER BY issue_id`, repo)
	if err != nil {
		return nil, fmt.Errorf("list workflow contexts: %w", err)
	}
	defer rows.Close()

	var contexts []*WorkflowContext
	for rows.Next() {
		var ctx WorkflowContext
		if err := rows.Scan(&ctx.ID, &ctx.Repo, &ctx.IssueID, &ctx.PRID, &ctx.Stage, &ctx.PreviousStage, &ctx.ActiveAgentID, &ctx.ActiveRole, &ctx.SessionID, &ctx.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workflow context: %w", err)
		}
		contexts = append(contexts, &ctx)
	}
	return contexts, nil
}

// TransitionStage updates the stage and active agent for a workflow context.
//
// PreviousStage 记录「本次转换前的阶段」，供两类回落使用：
//   - analyze 任务失败回滚（WorkflowManager.OnTaskFailed）
//   - 回复类任务完成后回落（WorkflowManager.OnTaskComplete，见 1.5.2A）
//
// 同阶段重入（如 developing → developing、reviewing → reviewing）不产生真实的
// 阶段变化，此时清空 PreviousStage，表示「没有可回落的前序阶段」，避免回落逻辑
// 误用上一次转换遗留的陈旧值把工作流倒退。
func (db *DB) TransitionStage(ctx *WorkflowContext, stage string, agentID int64, role, sessionID string) error {
	if stage != ctx.Stage {
		ctx.PreviousStage = ctx.Stage
	} else {
		ctx.PreviousStage = ""
	}
	ctx.Stage = stage
	ctx.ActiveAgentID = agentID
	ctx.ActiveRole = role
	ctx.SessionID = sessionID
	return db.UpdateWorkflowContext(ctx)
}

// WorkflowPolicyDB represents a per-repo workflow policy stored in DB.
type WorkflowPolicyDB struct {
	ID        int64     `json:"id"`
	Repo      string    `json:"repo"`
	Preset    string    `json:"preset"`
	GatesJSON string    `json:"gates"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// GetWorkflowPolicy returns the per-repo workflow policy, or (nil, nil) if not configured.
func (db *DB) GetWorkflowPolicy(repo string) (*WorkflowPolicyDB, error) {
	var wp WorkflowPolicyDB
	err := db.QueryRow(`SELECT id, repo, preset, gates, created_at, updated_at
		FROM workflow_policies WHERE repo = ?`, repo).Scan(
		&wp.ID, &wp.Repo, &wp.Preset, &wp.GatesJSON, &wp.CreatedAt, &wp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow policy: %w", err)
	}
	return &wp, nil
}

// UpsertWorkflowPolicy inserts or updates a per-repo workflow policy.
func (db *DB) UpsertWorkflowPolicy(repo, preset string, gatesJSON string) error {
	_, err := db.Exec(`INSERT OR REPLACE INTO workflow_policies (repo, preset, gates, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP)`, repo, preset, gatesJSON)
	if err != nil {
		return fmt.Errorf("upsert workflow policy: %w", err)
	}
	return nil
}

// DeleteWorkflowPolicy removes a per-repo workflow policy (falls back to system default).
func (db *DB) DeleteWorkflowPolicy(repo string) error {
	_, err := db.Exec(`DELETE FROM workflow_policies WHERE repo = ?`, repo)
	if err != nil {
		return fmt.Errorf("delete workflow policy: %w", err)
	}
	return nil
}

// ListWorkflowPolicies returns all per-repo workflow policies.
func (db *DB) ListWorkflowPolicies() ([]*WorkflowPolicyDB, error) {
	rows, err := db.Query(`SELECT id, repo, preset, gates, created_at, updated_at
		FROM workflow_policies ORDER BY repo`)
	if err != nil {
		return nil, fmt.Errorf("list workflow policies: %w", err)
	}
	defer rows.Close()

	var policies []*WorkflowPolicyDB
	for rows.Next() {
		var wp WorkflowPolicyDB
		if err := rows.Scan(&wp.ID, &wp.Repo, &wp.Preset, &wp.GatesJSON, &wp.CreatedAt, &wp.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workflow policy: %w", err)
		}
		policies = append(policies, &wp)
	}
	return policies, nil
}
