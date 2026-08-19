package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/store"
)

func TestSessionServiceGetOrCreate(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	// Create agent
	agent := &store.Agent{Name: "coder", GiteaUsername: "coder", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// First call creates
	s1, err := svc.GetOrCreate("owner/repo", 10, agent.ID, store.RoleCoder)
	require.NoError(t, err)
	assert.NotEmpty(t, s1.ID)
	assert.Equal(t, store.SessionActive, s1.Status)
	assert.Equal(t, store.RoleCoder, s1.Role)
	// B2.2: no on-disk session workspace is assigned — continuation anchors on
	// LastHead (git-native), not WorkspacePath.
	assert.Empty(t, s1.WorkspacePath)

	// Second call reuses
	s2, err := svc.GetOrCreate("owner/repo", 10, agent.ID, store.RoleCoder)
	require.NoError(t, err)
	assert.Equal(t, s1.ID, s2.ID)
}

func TestSessionServiceGetOrCreateDifferentRoles(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	agent := &store.Agent{Name: "multi", GiteaUsername: "multi", GiteaToken: "t", Role: store.RoleAnalyze, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// Analyze session
	s1, err := svc.GetOrCreate("owner/repo", 1, agent.ID, store.RoleAnalyze)
	require.NoError(t, err)
	assert.Empty(t, s1.WorkspacePath) // No workspace for analyze

	// Coder session for same issue — different session; no workspace either
	// since B2.2 (git-native continuation).
	s2, err := svc.GetOrCreate("owner/repo", 1, agent.ID, store.RoleCoder)
	require.NoError(t, err)
	assert.NotEqual(t, s1.ID, s2.ID)
	assert.Empty(t, s2.WorkspacePath)
}

func TestSessionServiceCompleteTask(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	agent := &store.Agent{Name: "coder", GiteaUsername: "coder", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	s, err := svc.GetOrCreate("owner/repo", 5, agent.ID, store.RoleCoder)
	require.NoError(t, err)

	// Complete task
	err = svc.CompleteTask(s, 99, "ai/fix-5", "")
	require.NoError(t, err)

	// Verify
	got, err := db.GetSession(s.ID)
	require.NoError(t, err)
	assert.Equal(t, store.SessionIdle, got.Status)
	assert.Equal(t, int64(99), got.LastTaskID)
	assert.Equal(t, "ai/fix-5", got.Branch)
}

func TestSessionServiceCompleteTaskWithPRID(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	agent := &store.Agent{Name: "coder", GiteaUsername: "coder", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	s, err := svc.GetOrCreate("owner/repo", 5, agent.ID, store.RoleCoder)
	require.NoError(t, err)

	// Complete task with PR ID
	err = svc.CompleteTask(s, 100, "ai/fix-5", "42")
	require.NoError(t, err)

	got, err := db.GetSession(s.ID)
	require.NoError(t, err)
	assert.Equal(t, 42, got.PRID)
}

func TestSessionServiceArchive(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	agent := &store.Agent{Name: "coder", GiteaUsername: "coder", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	s, err := svc.GetOrCreate("owner/repo", 1, agent.ID, store.RoleCoder)
	require.NoError(t, err)

	err = svc.Archive(s.ID)
	require.NoError(t, err)

	got, err := db.GetSession(s.ID)
	require.NoError(t, err)
	assert.Equal(t, store.SessionArchived, got.Status)
}

func TestSessionServiceArchiveByIssue(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "/data/work")

	agent := &store.Agent{Name: "coder", GiteaUsername: "coder", GiteaToken: "t", Role: store.RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// Create sessions for same issue
	_, err := svc.GetOrCreate("owner/repo", 1, agent.ID, store.RoleCoder)
	require.NoError(t, err)

	// Archive all
	err = svc.ArchiveByIssue("owner/repo", 1)
	require.NoError(t, err)

	// Verify
	sessions, err := db.ListSessionsByIssue("owner/repo", 1)
	require.NoError(t, err)
	assert.Len(t, sessions, 0) // All archived
}

func TestSessionServiceGetByIssue(t *testing.T) {
	db := newTestDB(t)
	svc := NewSessionService(db, "")

	agent := &store.Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Role: store.RoleAnalyze, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	_, err := svc.GetOrCreate("owner/repo", 1, agent.ID, store.RoleAnalyze)
	require.NoError(t, err)

	sessions, err := svc.GetByIssue("owner/repo", 1)
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
}
