package store

import (
	"database/sql"
	"fmt"
	"time"
)

// Task status constants.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
	// StatusPartial indicates the runner succeeded but Gitea writeback failed.
	// The task is not pure success: the result exists but was not delivered to the issue/PR.
	StatusPartial = "partial"
)

// Task represents an agent execution task.
type Task struct {
	ID         int64      `json:"id"`
	Event      string     `json:"event"`
	Repo       string     `json:"repo"`
	IssueID    int        `json:"issue_id"`
	PRID       int        `json:"pr_id"` // PR number for review_pr / solve_issue tasks (0 = no PR)
	AgentID    int64      `json:"agent_id"`
	TaskType   string     `json:"task_type"`
	Context    string     `json:"context"`
	Status     string     `json:"status"`
	Priority   int        `json:"priority"`
	DeliveryID string     `json:"delivery_id"`
	BaseBranch string     `json:"base_branch"` // PR head branch for solve_comment (empty = create new branch)
	SessionID  string     `json:"session_id"`  // Link to AgentSession
	Role       string     `json:"role"`        // Role that produced this task
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Result     string     `json:"result"`
	Error      string     `json:"error"`
	// StatusCommentID is the Gitea comment ID of the task's status card (0 =
	// none yet). Persisted so Matea can PATCH the same card after a restart
	// instead of posting a second one.
	StatusCommentID int64 `json:"status_comment_id"`
}

// TaskUsage represents token usage for a task.
type TaskUsage struct {
	ID               int64     `json:"id"`
	TaskID           int64     `json:"task_id"`
	Provider         string    `json:"provider"`
	Model            string    `json:"model"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	TotalTokens      int       `json:"total_tokens"`
	Cost             float64   `json:"cost"`
	CreatedAt        time.Time `json:"created_at"`
}

// TaskUsageSummary represents aggregated usage for a task.
type TaskUsageSummary struct {
	Provider              string  `json:"provider"`
	Model                 string  `json:"model"`
	TotalPromptTokens     int     `json:"total_prompt_tokens"`
	TotalCompletionTokens int     `json:"total_completion_tokens"`
	TotalTokens           int     `json:"total_tokens"`
	CallCount             int     `json:"call_count"`
	TotalCost             float64 `json:"total_cost"`
}

// CreateTaskUsage records token usage for a task.
func (db *DB) CreateTaskUsage(u *TaskUsage) error {
	_, err := db.Exec(`INSERT INTO task_usage (task_id, provider, model, prompt_tokens, completion_tokens, total_tokens, cost)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.TaskID, u.Provider, u.Model, u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.Cost)
	if err != nil {
		return fmt.Errorf("insert task usage: %w", err)
	}
	return nil
}

// GetTaskUsage returns all usage records for a task.
func (db *DB) GetTaskUsage(taskID int64) ([]TaskUsage, error) {
	rows, err := db.Query(`SELECT id, task_id, provider, model, prompt_tokens, completion_tokens, total_tokens, cost, created_at
		FROM task_usage WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query task usage: %w", err)
	}
	defer rows.Close()

	var usages []TaskUsage
	for rows.Next() {
		var u TaskUsage
		if err := rows.Scan(&u.ID, &u.TaskID, &u.Provider, &u.Model, &u.PromptTokens, &u.CompletionTokens, &u.TotalTokens, &u.Cost, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan task usage: %w", err)
		}
		usages = append(usages, u)
	}
	return usages, nil
}

// GetTaskUsageSummary returns aggregated usage for a task.
func (db *DB) GetTaskUsageSummary(taskID int64) (*TaskUsageSummary, error) {
	var summary TaskUsageSummary
	err := db.QueryRow(`SELECT provider, model, SUM(prompt_tokens), SUM(completion_tokens), SUM(total_tokens), COUNT(*), SUM(cost)
		FROM task_usage WHERE task_id = ? GROUP BY provider, model`, taskID).Scan(
		&summary.Provider, &summary.Model, &summary.TotalPromptTokens, &summary.TotalCompletionTokens, &summary.TotalTokens, &summary.CallCount, &summary.TotalCost)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("query task usage summary: %w", err)
	}
	return &summary, nil
}

// CreateTask inserts a new task.
func (db *DB) CreateTask(t *Task) error {
	// Check for duplicate delivery_id to prevent duplicate tasks
	if t.DeliveryID != "" {
		exists, err := db.deliveryExists(t.DeliveryID)
		if err != nil {
			return fmt.Errorf("check delivery: %w", err)
		}
		if exists {
			return fmt.Errorf("task with delivery_id %s already exists", t.DeliveryID)
		}
	}

	result, err := db.Exec(`INSERT INTO tasks (event, repo, issue_id, pr_id, agent_id, task_type, context, status, priority, delivery_id, base_branch, session_id, role)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Event, t.Repo, t.IssueID, t.PRID, t.AgentID, t.TaskType, t.Context, t.Status, t.Priority, t.DeliveryID, t.BaseBranch, t.SessionID, t.Role)
	if err != nil {
		return fmt.Errorf("insert task: %w", err)
	}
	id, _ := result.LastInsertId()
	t.ID = id
	return nil
}

// deliveryExists checks if a task with the given delivery_id already exists.
func (db *DB) deliveryExists(deliveryID string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE delivery_id = ?`, deliveryID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateTaskStatus updates a task's status, result, and error.
func (db *DB) UpdateTaskStatus(id int64, status, result, errMsg string) error {
	_, err := db.Exec(`UPDATE tasks SET status=?, result=?, error=?, 
		CASE WHEN status='running' AND started_at IS NULL THEN started_at=CURRENT_TIMESTAMP END,
		CASE WHEN status IN ('success','failed') THEN finished_at=CURRENT_TIMESTAMP END
		WHERE id=?`, status, result, errMsg, id)
	if err != nil {
		// Simplified update without CASE
		_, err2 := db.Exec(`UPDATE tasks SET status=?, result=?, error=? WHERE id=?`, status, result, errMsg, id)
		if err2 != nil {
			return fmt.Errorf("update task status: %w", err2)
		}
	}
	return nil
}

// UpdateTaskStatusCommentID records the Gitea comment ID of the task's status
// card. Called immediately after the card is created so later state changes
// PATCH that same comment (and so a restart can find it again without scanning
// comments for the marker).
func (db *DB) UpdateTaskStatusCommentID(id, commentID int64) error {
	if _, err := db.Exec(`UPDATE tasks SET status_comment_id=? WHERE id=?`, commentID, id); err != nil {
		return fmt.Errorf("update task status comment id: %w", err)
	}
	return nil
}

// taskColumns is the common SELECT column list for tasks.
const taskColumns = `id, event, repo, issue_id, pr_id, agent_id, task_type, context, status, priority, delivery_id, base_branch, session_id, role, created_at, started_at, finished_at, result, error, status_comment_id`

// taskScanFields returns scan targets for a Task row.
func taskScanFields(t *Task) []interface{} {
	return []interface{}{&t.ID, &t.Event, &t.Repo, &t.IssueID, &t.PRID, &t.AgentID, &t.TaskType, &t.Context, &t.Status, &t.Priority, &t.DeliveryID, &t.BaseBranch, &t.SessionID, &t.Role, &t.CreatedAt, &t.StartedAt, &t.FinishedAt, &t.Result, &t.Error, &t.StatusCommentID}
}

// ListPendingTasks returns all pending tasks ordered by priority and creation time.
func (db *DB) ListPendingTasks() ([]*Task, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM tasks WHERE status='pending' ORDER BY priority DESC, created_at ASC`, taskColumns))
	if err != nil {
		return nil, fmt.Errorf("list pending tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(taskScanFields(&t)...); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

// HasRunningTaskForAgent reports whether another task for agentID is currently running.
// excludeTaskID (when > 0) is ignored — useful when checking before promoting a candidate.
func (db *DB) HasRunningTaskForAgent(agentID, excludeTaskID int64) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM tasks WHERE agent_id=? AND status=? AND id!=?`,
		agentID, StatusRunning, excludeTaskID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("has running task for agent: %w", err)
	}
	return n > 0, nil
}

// NextPendingTaskForAgent returns the highest-priority pending task for an agent, or nil.
func (db *DB) NextPendingTaskForAgent(agentID int64) (*Task, error) {
	var t Task
	err := db.QueryRow(
		fmt.Sprintf(`SELECT %s FROM tasks WHERE agent_id=? AND status=? ORDER BY priority DESC, created_at ASC LIMIT 1`, taskColumns),
		agentID, StatusPending,
	).Scan(taskScanFields(&t)...)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("next pending task for agent: %w", err)
	}
	return &t, nil
}

// GetTask returns a task by ID.
func (db *DB) GetTask(id int64) (*Task, error) {
	var t Task
	err := db.QueryRow(fmt.Sprintf(`SELECT %s FROM tasks WHERE id=?`, taskColumns), id).Scan(taskScanFields(&t)...)
	if err != nil {
		return nil, fmt.Errorf("get task: %w", err)
	}
	return &t, nil
}

// CountTasks returns the total number of tasks.
func (db *DB) CountTasks() (int, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count tasks: %w", err)
	}
	return count, nil
}

// ListTasksFiltered returns tasks with filtering and pagination, plus total count.
func (db *DB) ListTasksFiltered(limit, offset int, status, taskType string, agentID int64) ([]*Task, int, error) {
	// Build WHERE clause
	where := "1=1"
	args := []interface{}{}
	if status != "" {
		where += " AND status = ?"
		args = append(args, status)
	}
	if taskType != "" {
		where += " AND task_type = ?"
		args = append(args, taskType)
	}
	if agentID > 0 {
		where += " AND agent_id = ?"
		args = append(args, agentID)
	}

	// Count total
	var total int
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM tasks WHERE %s`, where), countArgs...).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count tasks: %w", err)
	}

	// Fetch page
	args = append(args, limit, offset)
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM tasks WHERE %s ORDER BY created_at DESC LIMIT ? OFFSET ?`, taskColumns, where), args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(taskScanFields(&t)...); err != nil {
			return nil, 0, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, total, nil
}

// ListTasks returns tasks with pagination.
func (db *DB) ListTasks(limit, offset int) ([]*Task, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM tasks ORDER BY created_at DESC LIMIT ? OFFSET ?`, taskColumns), limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(taskScanFields(&t)...); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

// ListTasksByAgentID returns tasks for a specific agent.
func (db *DB) ListTasksByAgentID(agentID int64, limit int) ([]*Task, error) {
	rows, err := db.Query(fmt.Sprintf(`SELECT %s FROM tasks WHERE agent_id = ? ORDER BY created_at DESC LIMIT ?`, taskColumns), agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks by agent: %w", err)
	}
	defer rows.Close()

	var tasks []*Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(taskScanFields(&t)...); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		tasks = append(tasks, &t)
	}
	return tasks, nil
}

// HasPendingOrRunningTask checks if there is a pending or running task for the given repo and issue.
func (db *DB) HasPendingOrRunningTask(repo string, issueID int) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE repo = ? AND issue_id = ? AND status IN ('pending', 'running')`, repo, issueID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check pending task: %w", err)
	}
	return count > 0, nil
}

// ResetTask marks a pending/running/partial task as failed so the issue can accept new work.
// partial (runner succeeded but writeback failed) is also resettable to allow manual retry.
// Returns the updated task. No-op error if task is already terminal.
func (db *DB) ResetTask(id int64, reason string) (*Task, error) {
	task, err := db.GetTask(id)
	if err != nil {
		return nil, err
	}
	if task.Status != StatusPending && task.Status != StatusRunning && task.Status != StatusPartial {
		return nil, fmt.Errorf("task %d status is %q; only pending/running/partial can be reset", id, task.Status)
	}
	if reason == "" {
		reason = "manually reset"
	}
	_, err = db.Exec(`UPDATE tasks SET status='failed', error=?, finished_at=CURRENT_TIMESTAMP WHERE id=? AND status IN ('pending','running','partial')`,
		reason, id)
	if err != nil {
		return nil, fmt.Errorf("reset task: %w", err)
	}
	return db.GetTask(id)
}

// ResetTasksByIssue marks all pending/running/partial tasks for a repo+issue as failed.
// Returns the number of rows updated.
func (db *DB) ResetTasksByIssue(repo string, issueID int, reason string) (int, error) {
	if reason == "" {
		reason = "manually reset"
	}
	result, err := db.Exec(`UPDATE tasks SET status='failed', error=?, finished_at=CURRENT_TIMESTAMP
		WHERE repo=? AND issue_id=? AND status IN ('pending','running','partial')`,
		reason, repo, issueID)
	if err != nil {
		return 0, fmt.Errorf("reset tasks by issue: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// FailOrphanedRunningTasks marks all running tasks as failed (e.g. after process crash/restart).
func (db *DB) FailOrphanedRunningTasks(reason string) (int, error) {
	if reason == "" {
		reason = "matea restarted; interrupted running task"
	}
	result, err := db.Exec(`UPDATE tasks SET status='failed', error=?, finished_at=CURRENT_TIMESTAMP
		WHERE status='running'`, reason)
	if err != nil {
		return 0, fmt.Errorf("fail orphaned running tasks: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}

// ResetStaleRunningTasks resets tasks that have been in "running" state too long.
// Returns the number of tasks reset.
func (db *DB) ResetStaleRunningTasks(threshold time.Duration) (int, error) {
	cutoff := time.Now().Add(-threshold)
	result, err := db.Exec(`UPDATE tasks SET status='pending', started_at=NULL
		WHERE status='running' AND started_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("reset stale tasks: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(count), nil
}

// ResetStaleRunningTasksExceptHub resets stale running tasks the same way as
// ResetStaleRunningTasks, but never touches a task that owns a non-terminal hub
// handle. Hub runs are managed by their own poll loop (bounded by
// hubPollTimeout); a generic stale-reset would reclaim a legitimately in-flight
// hub task and cause the Executor to spin up a duplicate in-process poll loop
// for the same Handle. Hub tasks that are truly orphaned (Matea crashed) are
// recovered by ReattachHubHandles on startup, not by the scanner.
func (db *DB) ResetStaleRunningTasksExceptHub(threshold time.Duration) (int, error) {
	cutoff := time.Now().Add(-threshold)
	result, err := db.Exec(`UPDATE tasks SET status='pending', started_at=NULL
		WHERE status='running' AND started_at < ?
		  AND id NOT IN (SELECT task_id FROM hub_handles WHERE status NOT IN (?, ?, ?))`,
		cutoff, HubHandleStatusDone, HubHandleStatusFailed, HubHandleStatusCanceled)
	if err != nil {
		return 0, fmt.Errorf("reset stale tasks except hub: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return int(count), nil
}
