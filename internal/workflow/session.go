package workflow

import (
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/jeeinn/matea/internal/store"
)

// SessionService manages AgentSession lifecycle.
type SessionService struct {
	db      *store.DB
	baseDir string // workspace.base_dir for constructing workspace paths
}

// NewSessionService creates a new SessionService.
func NewSessionService(db *store.DB, baseDir string) *SessionService {
	return &SessionService{db: db, baseDir: baseDir}
}

// GetOrCreate returns an existing session for the repo/issue/agent/role, or creates a new one.
func (s *SessionService) GetOrCreate(repo string, issueID int, agentID int64, role string) (*store.AgentSession, error) {
	// Try to find existing non-archived session
	existing, err := s.db.GetSessionByRepoIssueAgentRole(repo, issueID, agentID, role)
	if err == nil {
		log.Printf("[INFO] Reusing existing session %s for %s#%d agent=%d role=%s",
			existing.ID, repo, issueID, agentID, role)
		return existing, nil
	}

	// Create new session
	sessionID := uuid.New().String()[:8] // Short UUID for readability
	now := time.Now()
	session := &store.AgentSession{
		ID:           sessionID,
		Repo:         repo,
		IssueID:      issueID,
		AgentID:      agentID,
		Role:         role,
		Status:       store.SessionActive,
		LastActiveAt: now,
		CreatedAt:    now,
	}

	// B2.2: no on-disk session workspace is assigned anymore — write-task
	// continuation anchors on the session's LastHead (git-native). The
	// deprecated workspace_path column stays empty for new sessions; the
	// lifecycle GC still reclaims legacy pre-B2.2 workspace directories.

	if err := s.db.CreateSession(session); err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	log.Printf("[INFO] Created new session %s for %s#%d agent=%d role=%s",
		sessionID, repo, issueID, agentID, role)
	return session, nil
}

// GetByIssue returns all active sessions for a repo+issue.
func (s *SessionService) GetByIssue(repo string, issueID int) ([]*store.AgentSession, error) {
	return s.db.ListSessionsByIssue(repo, issueID)
}

// GetActiveForIssue returns the most recently active session for a repo+issue.
func (s *SessionService) GetActiveForIssue(repo string, issueID int) (*store.AgentSession, error) {
	return s.db.GetActiveSessionForIssue(repo, issueID)
}

// CompleteTask updates a session after a task completes successfully.
func (s *SessionService) CompleteTask(session *store.AgentSession, taskID int64, branch, prID string) error {
	session.Status = store.SessionIdle
	session.LastTaskID = taskID
	session.LastActiveAt = time.Now()

	if branch != "" {
		session.Branch = branch
	}
	if prID != "" {
		// Parse PR ID if it's a number string
		var id int
		if _, err := fmt.Sscanf(prID, "%d", &id); err == nil {
			session.PRID = id
		}
	}

	if err := s.db.UpdateSession(session); err != nil {
		return fmt.Errorf("update session after task: %w", err)
	}

	log.Printf("[INFO] Session %s completed task %d, status=idle, branch=%s",
		session.ID, taskID, session.Branch)
	return nil
}

// Archive marks a session as archived.
func (s *SessionService) Archive(sessionID string) error {
	return s.db.ArchiveSession(sessionID)
}

// ArchiveByIssue archives all active sessions for a repo+issue.
func (s *SessionService) ArchiveByIssue(repo string, issueID int) error {
	sessions, err := s.db.ListSessionsByIssue(repo, issueID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if err := s.db.ArchiveSession(sess.ID); err != nil {
			log.Printf("[WARN] Failed to archive session %s: %v", sess.ID, err)
		}
	}
	return nil
}
