package store

import (
	"fmt"
	"time"
)

// Session status constants.
const (
	SessionActive   = "active"
	SessionIdle     = "idle"
	SessionArchived = "archived"
)

// AgentSession represents a persistent session between an agent and an issue.
type AgentSession struct {
	ID      string `json:"id"`
	Repo    string `json:"repo"`
	IssueID int    `json:"issue_id"`
	PRID    int    `json:"pr_id"`
	AgentID int64  `json:"agent_id"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	Branch  string `json:"branch"`
	// WorkspacePath is deprecated (B2.1): git_sync session continuation is
	// anchored on LastHead, not a Matea-side on-disk workspace. The column and
	// field stay until B2.2 swaps the remaining consumers.
	WorkspacePath string `json:"workspace_path"`
	// LastHead is the head SHA of the session's latest draft branch — the
	// anchor the next continuation task branches from (B2.2/B2.3).
	LastHead string `json:"last_head"`
	// Memory is the rolling per-session summary injected into continuation
	// prompts (B2.3). Distinct from the repo/issue-scoped memories table.
	Memory       string    `json:"memory"`
	LastTaskID   int64     `json:"last_task_id"`
	MessageCount int       `json:"message_count"`
	LastActiveAt time.Time `json:"last_active_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// CreateSession inserts a new agent session.
func (db *DB) CreateSession(s *AgentSession) error {
	_, err := db.Exec(`INSERT INTO agent_sessions (id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.Repo, s.IssueID, s.PRID, s.AgentID, s.Role, s.Status, s.Branch, s.WorkspacePath, s.LastHead, s.Memory, s.LastTaskID, s.MessageCount, s.LastActiveAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

// GetSession returns a session by ID.
func (db *DB) GetSession(id string) (*AgentSession, error) {
	var s AgentSession
	err := db.QueryRow(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE id = ?`, id).Scan(
		&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}
	return &s, nil
}

// GetSessionByRepoIssueAgentRole returns the session for the given repo, issue, agent, and role.
func (db *DB) GetSessionByRepoIssueAgentRole(repo string, issueID int, agentID int64, role string) (*AgentSession, error) {
	var s AgentSession
	err := db.QueryRow(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE repo = ? AND issue_id = ? AND agent_id = ? AND role = ? AND status != ?
		ORDER BY last_active_at DESC LIMIT 1`, repo, issueID, agentID, role, SessionArchived).Scan(
		&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get session by repo/issue/agent/role: %w", err)
	}
	return &s, nil
}

// GetActiveSessionForIssue returns the most recently active non-archived session for a repo+issue.
func (db *DB) GetActiveSessionForIssue(repo string, issueID int) (*AgentSession, error) {
	var s AgentSession
	err := db.QueryRow(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE repo = ? AND issue_id = ? AND status != ?
		ORDER BY last_active_at DESC LIMIT 1`, repo, issueID, SessionArchived).Scan(
		&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get active session for issue: %w", err)
	}
	return &s, nil
}

// ListSessionsByIssue returns all non-archived sessions for a repo+issue.
func (db *DB) ListSessionsByIssue(repo string, issueID int) ([]*AgentSession, error) {
	rows, err := db.Query(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE repo = ? AND issue_id = ? AND status != ?
		ORDER BY last_active_at DESC`, repo, issueID, SessionArchived)
	if err != nil {
		return nil, fmt.Errorf("list sessions by issue: %w", err)
	}
	defer rows.Close()

	var sessions []*AgentSession
	for rows.Next() {
		var s AgentSession
		if err := rows.Scan(&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, &s)
	}
	return sessions, nil
}

// UpdateSession updates an existing session.
func (db *DB) UpdateSession(s *AgentSession) error {
	_, err := db.Exec(`UPDATE agent_sessions SET pr_id=?, role=?, status=?, branch=?, workspace_path=?, last_head=?, memory=?, last_task_id=?, message_count=?, last_active_at=CURRENT_TIMESTAMP
		WHERE id=?`, s.PRID, s.Role, s.Status, s.Branch, s.WorkspacePath, s.LastHead, s.Memory, s.LastTaskID, s.MessageCount, s.ID)
	if err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

// ArchiveSession marks a session as archived.
func (db *DB) ArchiveSession(id string) error {
	_, err := db.Exec(`UPDATE agent_sessions SET status=?, last_active_at=CURRENT_TIMESTAMP WHERE id=?`, SessionArchived, id)
	if err != nil {
		return fmt.Errorf("archive session: %w", err)
	}
	return nil
}

// ListIdleSessionsOlderThan returns idle sessions whose last_active_at is older than the given time.
func (db *DB) ListIdleSessionsOlderThan(cutoff time.Time) ([]*AgentSession, error) {
	rows, err := db.Query(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE status = ? AND last_active_at < ?`, SessionIdle, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list idle sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*AgentSession
	for rows.Next() {
		var s AgentSession
		if err := rows.Scan(&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, &s)
	}
	return sessions, nil
}

// ListArchivedSessionsWithWorkspace returns archived sessions that still have a workspace path.
func (db *DB) ListArchivedSessionsWithWorkspace() ([]*AgentSession, error) {
	rows, err := db.Query(`SELECT id, repo, issue_id, pr_id, agent_id, role, status, branch, workspace_path, last_head, memory, last_task_id, message_count, last_active_at, created_at
		FROM agent_sessions WHERE status = ? AND workspace_path != ''`, SessionArchived)
	if err != nil {
		return nil, fmt.Errorf("list archived sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*AgentSession
	for rows.Next() {
		var s AgentSession
		if err := rows.Scan(&s.ID, &s.Repo, &s.IssueID, &s.PRID, &s.AgentID, &s.Role, &s.Status, &s.Branch, &s.WorkspacePath, &s.LastHead, &s.Memory, &s.LastTaskID, &s.MessageCount, &s.LastActiveAt, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, &s)
	}
	return sessions, nil
}
