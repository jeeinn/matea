package store

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB creates a temporary SQLite database for testing.
func newTestDB(t *testing.T) *DB {
	t.Helper()
	tmpDB, err := os.CreateTemp("", "store-test-*.db")
	require.NoError(t, err)
	tmpDB.Close()

	db, err := Open(tmpDB.Name())
	require.NoError(t, err)

	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpDB.Name())
	})

	return db
}

// --- Agent Tests ---

func TestCreateAgentWithRole(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{
		Name:            "analyze-007",
		GiteaUsername:   "analyze-007",
		GiteaToken:      "token-abc",
		Provider:        "deepseek",
		Model:           "deepseek-chat",
		MaxOutputTokens: 4096,
		MaxInputTokens:  8192,
		Temperature:     0.3,
		SystemPrompt:    "You are an analyzer.",
		Role:            RoleAnalyze,
		Status:          "active",
	}

	err := db.CreateAgent(agent)
	require.NoError(t, err)
	assert.Greater(t, agent.ID, int64(0))

	// Verify by ID
	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "analyze-007", got.Name)
	assert.Equal(t, RoleAnalyze, got.Role)

	// Verify by username
	got2, err := db.GetAgentByGiteaUsername("analyze-007")
	require.NoError(t, err)
	assert.Equal(t, agent.ID, got2.ID)
	assert.Equal(t, RoleAnalyze, got2.Role)
}

func TestAgentRoleDefault(t *testing.T) {
	db := newTestDB(t)

	// Agent created via old migration path (role column added later) should default to 'analyze'
	agent := &Agent{
		Name:          "legacy-agent",
		GiteaUsername: "legacy",
		GiteaToken:    "token",
		Role:          "", // zero value
		Status:        "active",
	}
	err := db.CreateAgent(agent)
	require.NoError(t, err)

	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got.Role) // stored as empty string since we didn't set it
}

func TestUpdateAgentRole(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{
		Name:          "coder-ds",
		GiteaUsername: "coder-ds",
		GiteaToken:    "token",
		Role:          RoleCoder,
		Status:        "active",
	}
	err := db.CreateAgent(agent)
	require.NoError(t, err)

	// Update role
	agent.Role = RoleReview
	err = db.UpdateAgent(agent)
	require.NoError(t, err)

	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, RoleReview, got.Role)
}

func TestListAgentsIncludesRole(t *testing.T) {
	db := newTestDB(t)

	agents := []*Agent{
		{Name: "a1", GiteaUsername: "u1", GiteaToken: "t1", Role: RoleAnalyze, Status: "active"},
		{Name: "c1", GiteaUsername: "u2", GiteaToken: "t2", Role: RoleCoder, Status: "active"},
		{Name: "r1", GiteaUsername: "u3", GiteaToken: "t3", Role: RoleReview, Status: "active"},
	}
	for _, a := range agents {
		require.NoError(t, db.CreateAgent(a))
	}

	list, err := db.ListAgents()
	require.NoError(t, err)
	assert.Len(t, list, 3)

	roles := map[string]bool{}
	for _, a := range list {
		roles[a.Role] = true
	}
	assert.True(t, roles[RoleAnalyze])
	assert.True(t, roles[RoleCoder])
	assert.True(t, roles[RoleReview])
}

// --- WorkflowContext Tests ---

func TestGetOrCreateWorkflowContext(t *testing.T) {
	db := newTestDB(t)

	// First call creates
	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 42)
	require.NoError(t, err)
	assert.Equal(t, "owner/repo", ctx.Repo)
	assert.Equal(t, 42, ctx.IssueID)
	assert.Equal(t, StageIdle, ctx.Stage)
	assert.Greater(t, ctx.ID, int64(0))

	// Second call returns same
	ctx2, err := db.GetOrCreateWorkflowContext("owner/repo", 42)
	require.NoError(t, err)
	assert.Equal(t, ctx.ID, ctx2.ID)
}

func TestWorkflowContextTransition(t *testing.T) {
	db := newTestDB(t)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 1)
	require.NoError(t, err)

	// Create an agent for the transition
	agent := &Agent{Name: "analyzer", GiteaUsername: "analyzer", GiteaToken: "t", Role: RoleAnalyze, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// idle → analyzing
	err = db.TransitionStage(ctx, StageAnalyzing, agent.ID, RoleAnalyze, "sess-1")
	require.NoError(t, err)

	got, err := db.GetWorkflowContext("owner/repo", 1)
	require.NoError(t, err)
	assert.Equal(t, StageAnalyzing, got.Stage)
	assert.Equal(t, agent.ID, got.ActiveAgentID)
	assert.Equal(t, RoleAnalyze, got.ActiveRole)
	assert.Equal(t, "sess-1", got.SessionID)

	// analyzing → analyzed
	err = db.TransitionStage(ctx, StageAnalyzed, agent.ID, RoleAnalyze, "sess-1")
	require.NoError(t, err)

	got, err = db.GetWorkflowContext("owner/repo", 1)
	require.NoError(t, err)
	assert.Equal(t, StageAnalyzed, got.Stage)
}

func TestWorkflowContextPRID(t *testing.T) {
	db := newTestDB(t)

	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 5)
	require.NoError(t, err)

	ctx.PRID = 88
	err = db.UpdateWorkflowContext(ctx)
	require.NoError(t, err)

	got, err := db.GetWorkflowContext("owner/repo", 5)
	require.NoError(t, err)
	assert.Equal(t, 88, got.PRID)
}

func TestListWorkflowContextsByRepo(t *testing.T) {
	db := newTestDB(t)

	_, err := db.GetOrCreateWorkflowContext("owner/repo", 1)
	require.NoError(t, err)
	_, err = db.GetOrCreateWorkflowContext("owner/repo", 2)
	require.NoError(t, err)
	_, err = db.GetOrCreateWorkflowContext("other/repo", 3)
	require.NoError(t, err)

	list, err := db.ListWorkflowContextsByRepo("owner/repo")
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

// --- AgentSession Tests ---

func TestCreateAndGetSession(t *testing.T) {
	db := newTestDB(t)

	now := time.Now().Truncate(time.Second)
	s := &AgentSession{
		ID:            "sess-abc-123",
		Repo:          "owner/repo",
		IssueID:       10,
		AgentID:       1,
		Role:          RoleCoder,
		Status:        SessionActive,
		Branch:        "ai/fix-10",
		WorkspacePath: "/data/sessions/sess-abc-123/repo",
		LastHead:      "0123456789abcdef0123456789abcdef01234567",
		Memory:        "task 7: pushed matea/hub-7",
		LastActiveAt:  now,
		CreatedAt:     now,
	}

	err := db.CreateSession(s)
	require.NoError(t, err)

	got, err := db.GetSession("sess-abc-123")
	require.NoError(t, err)
	assert.Equal(t, "owner/repo", got.Repo)
	assert.Equal(t, 10, got.IssueID)
	assert.Equal(t, RoleCoder, got.Role)
	assert.Equal(t, SessionActive, got.Status)
	assert.Equal(t, "ai/fix-10", got.Branch)
	// B2.1: git-native continuation state round-trips.
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", got.LastHead)
	assert.Equal(t, "task 7: pushed matea/hub-7", got.Memory)
}

func TestGetSessionByRepoIssueAgentRole(t *testing.T) {
	db := newTestDB(t)

	s := &AgentSession{
		ID:           "sess-1",
		Repo:         "owner/repo",
		IssueID:      5,
		AgentID:      100,
		Role:         RoleCoder,
		Status:       SessionActive,
		LastActiveAt: time.Now(),
		CreatedAt:    time.Now(),
	}
	require.NoError(t, db.CreateSession(s))

	got, err := db.GetSessionByRepoIssueAgentRole("owner/repo", 5, 100, RoleCoder)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)

	// Different role should not match
	_, err = db.GetSessionByRepoIssueAgentRole("owner/repo", 5, 100, RoleAnalyze)
	assert.Error(t, err)
}

func TestGetActiveSessionForIssue(t *testing.T) {
	db := newTestDB(t)

	// Create two sessions for same issue, different roles
	s1 := &AgentSession{
		ID: "sess-analyze", Repo: "owner/repo", IssueID: 1, AgentID: 1, Role: RoleAnalyze,
		Status: SessionIdle, LastActiveAt: time.Now().Add(-time.Hour), CreatedAt: time.Now(),
	}
	s2 := &AgentSession{
		ID: "sess-coder", Repo: "owner/repo", IssueID: 1, AgentID: 2, Role: RoleCoder,
		Status: SessionActive, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(s1))
	require.NoError(t, db.CreateSession(s2))

	// Most recently active
	got, err := db.GetActiveSessionForIssue("owner/repo", 1)
	require.NoError(t, err)
	assert.Equal(t, "sess-coder", got.ID)
}

func TestArchiveSession(t *testing.T) {
	db := newTestDB(t)

	s := &AgentSession{
		ID: "sess-archive", Repo: "owner/repo", IssueID: 1, AgentID: 1, Role: RoleCoder,
		Status: SessionActive, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(s))

	err := db.ArchiveSession("sess-archive")
	require.NoError(t, err)

	got, err := db.GetSession("sess-archive")
	require.NoError(t, err)
	assert.Equal(t, SessionArchived, got.Status)
}

func TestUpdateSession(t *testing.T) {
	db := newTestDB(t)

	s := &AgentSession{
		ID: "sess-update", Repo: "owner/repo", IssueID: 1, AgentID: 1, Role: RoleCoder,
		Status: SessionActive, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(s))

	// Update fields
	s.PRID = 42
	s.Branch = "ai/fix-1"
	s.LastTaskID = 99
	s.Status = SessionIdle
	s.LastHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	s.Memory = "rolling summary"
	err := db.UpdateSession(s)
	require.NoError(t, err)

	got, err := db.GetSession("sess-update")
	require.NoError(t, err)
	assert.Equal(t, 42, got.PRID)
	assert.Equal(t, "ai/fix-1", got.Branch)
	assert.Equal(t, int64(99), got.LastTaskID)
	assert.Equal(t, SessionIdle, got.Status)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", got.LastHead)
	assert.Equal(t, "rolling summary", got.Memory)
}

// TestSessionLastHeadMemoryDefaults covers the B2.1 migration defaults: a DB
// created before last_head/memory existed (or rows written without them)
// surfaces empty strings, never NULL scan errors.
func TestSessionLastHeadMemoryDefaults(t *testing.T) {
	db := newTestDB(t)

	// Simulate a pre-B2.1 row by writing through the legacy column set.
	_, err := db.Exec(`INSERT INTO agent_sessions (id, repo, issue_id, agent_id, role, status, branch, workspace_path)
		VALUES ('legacy-sess', 'o/r', 3, 1, ?, ?, 'ai/fix-3', '/tmp/x')`, RoleCoder, SessionIdle)
	require.NoError(t, err)

	got, err := db.GetSession("legacy-sess")
	require.NoError(t, err)
	assert.Equal(t, "", got.LastHead)
	assert.Equal(t, "", got.Memory)
}

func TestListSessionsByIssue(t *testing.T) {
	db := newTestDB(t)

	s1 := &AgentSession{
		ID: "s1", Repo: "owner/repo", IssueID: 1, AgentID: 1, Role: RoleAnalyze,
		Status: SessionIdle, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	s2 := &AgentSession{
		ID: "s2", Repo: "owner/repo", IssueID: 1, AgentID: 2, Role: RoleCoder,
		Status: SessionActive, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	s3 := &AgentSession{
		ID: "s3", Repo: "owner/repo", IssueID: 1, AgentID: 3, Role: RoleReview,
		Status: SessionArchived, LastActiveAt: time.Now(), CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(s1))
	require.NoError(t, db.CreateSession(s2))
	require.NoError(t, db.CreateSession(s3))

	// Should not include archived
	list, err := db.ListSessionsByIssue("owner/repo", 1)
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestListIdleSessionsOlderThan(t *testing.T) {
	db := newTestDB(t)

	s := &AgentSession{
		ID: "old-sess", Repo: "owner/repo", IssueID: 1, AgentID: 1, Role: RoleCoder,
		Status: SessionIdle, LastActiveAt: time.Now().Add(-48 * time.Hour), CreatedAt: time.Now(),
	}
	require.NoError(t, db.CreateSession(s))

	// Cutoff 24h ago — should find it
	sessions, err := db.ListIdleSessionsOlderThan(time.Now().Add(-24 * time.Hour))
	require.NoError(t, err)
	assert.Len(t, sessions, 1)
	assert.Equal(t, "old-sess", sessions[0].ID)

	// Cutoff 72h ago — should not find it
	sessions, err = db.ListIdleSessionsOlderThan(time.Now().Add(-72 * time.Hour))
	require.NoError(t, err)
	assert.Len(t, sessions, 0)
}

// --- Task Tests ---

func TestTaskWithSessionAndRole(t *testing.T) {
	db := newTestDB(t)

	// Create agent first (FK)
	agent := &Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Role: RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	task := &Task{
		Event:      "issues",
		Repo:       "owner/repo",
		IssueID:    1,
		AgentID:    agent.ID,
		TaskType:   "solve_issue",
		Status:     "pending",
		DeliveryID: "del-001",
		SessionID:  "sess-abc",
		Role:       RoleCoder,
	}
	err := db.CreateTask(task)
	require.NoError(t, err)
	assert.Greater(t, task.ID, int64(0))

	got, err := db.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "sess-abc", got.SessionID)
	assert.Equal(t, RoleCoder, got.Role)
}

func TestHasRunningTaskForAgentAndNextPending(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	busy, err := db.HasRunningTaskForAgent(agent.ID, 0)
	require.NoError(t, err)
	assert.False(t, busy)

	running := &Task{
		Event: "issues", Repo: "owner/repo", IssueID: 1, AgentID: agent.ID,
		TaskType: "solve_issue", Status: StatusRunning, DeliveryID: "run-1",
	}
	require.NoError(t, db.CreateTask(running))
	require.NoError(t, db.UpdateTaskStatus(running.ID, StatusRunning, "", ""))

	busy, err = db.HasRunningTaskForAgent(agent.ID, 0)
	require.NoError(t, err)
	assert.True(t, busy)

	busy, err = db.HasRunningTaskForAgent(agent.ID, running.ID)
	require.NoError(t, err)
	assert.False(t, busy, "exclude self")

	pending := &Task{
		Event: "issues", Repo: "owner/repo", IssueID: 2, AgentID: agent.ID,
		TaskType: "solve_issue", Status: StatusPending, DeliveryID: "pend-1", Priority: 10,
	}
	require.NoError(t, db.CreateTask(pending))

	next, err := db.NextPendingTaskForAgent(agent.ID)
	require.NoError(t, err)
	require.NotNil(t, next)
	assert.Equal(t, pending.ID, next.ID)
}

func TestHasPendingOrRunningTask(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	// No tasks yet
	has, err := db.HasPendingOrRunningTask("owner/repo", 1)
	require.NoError(t, err)
	assert.False(t, has)

	// Create a pending task
	task := &Task{
		Event: "issues", Repo: "owner/repo", IssueID: 1, AgentID: agent.ID,
		TaskType: "analyze_issue", Status: "pending", DeliveryID: "d1",
	}
	require.NoError(t, db.CreateTask(task))

	has, err = db.HasPendingOrRunningTask("owner/repo", 1)
	require.NoError(t, err)
	assert.True(t, has)

	// Different issue should not match
	has, err = db.HasPendingOrRunningTask("owner/repo", 2)
	require.NoError(t, err)
	assert.False(t, has)

	// Different repo should not match
	has, err = db.HasPendingOrRunningTask("other/repo", 1)
	require.NoError(t, err)
	assert.False(t, has)
}

func TestTaskColumnsAndScanConsistency(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Role: RoleCoder, Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	task := &Task{
		Event:      "issues",
		Repo:       "owner/repo",
		IssueID:    5,
		AgentID:    agent.ID,
		TaskType:   "solve_issue",
		Context:    "some context",
		Status:     "pending",
		Priority:   10,
		DeliveryID: "del-consistency",
		BaseBranch: "ai/fix-5",
		SessionID:  "sess-xyz",
		Role:       RoleCoder,
	}
	require.NoError(t, db.CreateTask(task))

	// Verify all fields round-trip via ListTasks
	tasks, err := db.ListTasks(10, 0)
	require.NoError(t, err)
	require.Len(t, tasks, 1)

	got := tasks[0]
	assert.Equal(t, task.Repo, got.Repo)
	assert.Equal(t, task.IssueID, got.IssueID)
	assert.Equal(t, task.TaskType, got.TaskType)
	assert.Equal(t, task.Context, got.Context)
	assert.Equal(t, task.Priority, got.Priority)
	assert.Equal(t, task.BaseBranch, got.BaseBranch)
	assert.Equal(t, task.SessionID, got.SessionID)
	assert.Equal(t, task.Role, got.Role)
}
