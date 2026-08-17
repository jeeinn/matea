package store

import (
	"fmt"
	"time"
)

// Hub handle status values. These mirror the agents.State terminal vocabulary
// but live in the store package to avoid an import cycle (agents imports store).
// Persistence of the Handle (HubBackend contract §1.2.1) is the reliability
// foundation that lets the Executor resume a hub run whose in-process poll loop
// was lost on restart, and that lets idempotent re-submission reuse an existing
// handle instead of double-submitting the same task.
const (
	HubHandleStatusPending  = "pending"
	HubHandleStatusRunning  = "running"
	HubHandleStatusDone     = "done"
	HubHandleStatusFailed   = "failed"
	HubHandleStatusCanceled = "canceled"
)

// IsTerminalHubStatus reports whether a hub handle status is final.
func IsTerminalHubStatus(status string) bool {
	switch status {
	case HubHandleStatusDone, HubHandleStatusFailed, HubHandleStatusCanceled:
		return true
	default:
		return false
	}
}

// HubHandle is the persistable reference to a task submitted to a hub backend
// (OpenCode / Hermes / custom). It is keyed by task_id so the Executor can
// re-attach polling after a restart and so idempotent re-submission reuses the
// same remote run instead of starting a duplicate.
type HubHandle struct {
	TaskID         int64     `json:"task_id"`
	Backend        string    `json:"backend"`         // backend name, e.g. "hub-opencode"
	RemoteID       string    `json:"remote_id"`       // hub-side session/job id
	IdempotencyKey string    `json:"idempotency_key"` // dedup key for safe resubmission
	Status         string    `json:"status"`          // HubHandleStatus*

	// git_sync fields (task A2): the draft-branch contract state for hub write
	// tasks. Empty/zero for read/reply tasks and shared_path-era handles.
	DraftBranch string `json:"draft_branch,omitempty"` // matea/hub-{taskID} registered at Prepare
	BaseHEAD    string `json:"base_head,omitempty"`    // base branch head anchor at Prepare
	DeployKeyID int64  `json:"deploy_key_id,omitempty"` // Gitea deploy key id for Cleanup revoke

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// SaveHubHandle persists (INSERT OR REPLACE) the hub handle for a task. The
// task_id primary key means a re-submission overwrites the previous handle,
// keeping at most one live handle per task.
func (db *DB) SaveHubHandle(h *HubHandle) error {
	if h == nil {
		return fmt.Errorf("save hub handle: nil handle")
	}
	if h.Status == "" {
		h.Status = HubHandleStatusRunning
	}
	_, err := db.Exec(
		`INSERT OR REPLACE INTO hub_handles (task_id, backend, remote_id, idempotency_key, status, draft_branch, base_head, deploy_key_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		h.TaskID, h.Backend, h.RemoteID, h.IdempotencyKey, h.Status, h.DraftBranch, h.BaseHEAD, h.DeployKeyID,
	)
	if err != nil {
		return fmt.Errorf("save hub handle: %w", err)
	}
	return nil
}

// GetHubHandle returns the persisted handle for a task, or (nil, nil) when no
// handle has been recorded. A real error is returned only on query failure.
func (db *DB) GetHubHandle(taskID int64) (*HubHandle, error) {
	var h HubHandle
	err := db.QueryRow(
		`SELECT task_id, backend, remote_id, idempotency_key, status, draft_branch, base_head, deploy_key_id, created_at, updated_at
		 FROM hub_handles WHERE task_id = ?`, taskID,
	).Scan(&h.TaskID, &h.Backend, &h.RemoteID, &h.IdempotencyKey, &h.Status, &h.DraftBranch, &h.BaseHEAD, &h.DeployKeyID, &h.CreatedAt, &h.UpdatedAt)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return nil, nil
		}
		return nil, fmt.Errorf("get hub handle: %w", err)
	}
	return &h, nil
}

// UpdateHubHandleStatus records the terminal (or transition) status of a hub
// handle. Best-effort: a missing row is not an error (the handle may never have
// been persisted, e.g. when db was nil at Submit time).
func (db *DB) UpdateHubHandleStatus(taskID int64, status string) error {
	res, err := db.Exec(
		`UPDATE hub_handles SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE task_id = ?`,
		status, taskID,
	)
	if err != nil {
		return fmt.Errorf("update hub handle status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// No handle row to update — acceptable (e.g. db was nil at Submit).
		return nil
	}
	return nil
}

// ListNonTerminalHubHandles returns every persisted handle that is not yet in a
// terminal state. Used at startup to find hub runs that must be re-attached.
func (db *DB) ListNonTerminalHubHandles() ([]*HubHandle, error) {
	rows, err := db.Query(
		`SELECT task_id, backend, remote_id, idempotency_key, status, draft_branch, base_head, deploy_key_id, created_at, updated_at
		 FROM hub_handles
		 WHERE status NOT IN (?, ?, ?)
		 ORDER BY created_at ASC`,
		HubHandleStatusDone, HubHandleStatusFailed, HubHandleStatusCanceled,
	)
	if err != nil {
		return nil, fmt.Errorf("list non-terminal hub handles: %w", err)
	}
	defer rows.Close()

	var out []*HubHandle
	for rows.Next() {
		var h HubHandle
		if err := rows.Scan(&h.TaskID, &h.Backend, &h.RemoteID, &h.IdempotencyKey, &h.Status, &h.DraftBranch, &h.BaseHEAD, &h.DeployKeyID, &h.CreatedAt, &h.UpdatedAt); err != nil {
			return out, fmt.Errorf("scan hub handle: %w", err)
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}

// HasNonTerminalHubHandle reports whether a task currently owns a non-terminal
// hub handle. Used by the queue scanner to skip hub tasks when reclaiming stale
// running tasks — the hub poll loop (bounded by hubPollTimeout) owns that
// lifecycle, and a generic stale-reset would create a duplicate in-process poll.
func (db *DB) HasNonTerminalHubHandle(taskID int64) (bool, error) {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM hub_handles WHERE task_id = ? AND status NOT IN (?, ?, ?)`,
		taskID, HubHandleStatusDone, HubHandleStatusFailed, HubHandleStatusCanceled,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("has non-terminal hub handle: %w", err)
	}
	return n > 0, nil
}

// DeleteHubHandle removes a persisted handle (cleanup after terminal state).
func (db *DB) DeleteHubHandle(taskID int64) error {
	if _, err := db.Exec(`DELETE FROM hub_handles WHERE task_id = ?`, taskID); err != nil {
		return fmt.Errorf("delete hub handle: %w", err)
	}
	return nil
}

// FailOrphanedRunningTasksExceptHub marks running tasks as failed on startup
// (previous process killed) but preserves tasks that own a non-terminal hub
// handle: those are re-attached by the Executor instead of failed, so a hub run
// in progress at crash time is resumed rather than lost. Returns the number of
// tasks marked failed.
func (db *DB) FailOrphanedRunningTasksExceptHub(reason string) (int, error) {
	if reason == "" {
		reason = "matea restarted; interrupted running task"
	}
	res, err := db.Exec(
		`UPDATE tasks SET status='failed', error=?, finished_at=CURRENT_TIMESTAMP
		 WHERE status='running'
		   AND id NOT IN (SELECT task_id FROM hub_handles WHERE status NOT IN (?, ?, ?))`,
		reason, HubHandleStatusDone, HubHandleStatusFailed, HubHandleStatusCanceled,
	)
	if err != nil {
		return 0, fmt.Errorf("fail orphaned running tasks except hub: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
