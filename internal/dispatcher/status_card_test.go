package dispatcher

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cardServer is a stand-in Gitea that records what the status-card machinery
// does: POSTs (creates), PATCHes (in-place updates) and comment listings
// (marker scans).
type cardServer struct {
	posts     []string             // raw bodies of POST /comments
	postPaths []string             // request path of each POST, e.g. .../issues/8/comments
	patches   map[int]string       // commentID -> latest body
	comments  []gitea.IssueComment // served to GET /comments
	failAll   bool                 // force every request to 500
	unknownID bool                 // PATCH any id -> 404 (card deleted upstream)
}

func (s *cardServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if s.failAll {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		switch r.Method {
		case http.MethodPost:
			s.posts = append(s.posts, string(raw))
			s.postPaths = append(s.postPaths, r.URL.Path)
			id := 900 + len(s.posts)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(gitea.IssueComment{ID: id, Body: "created"}))
		case http.MethodPatch:
			id, err := strconv.Atoi(path.Base(r.URL.Path))
			require.NoError(t, err)
			if s.unknownID {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if s.patches == nil {
				s.patches = map[int]string{}
			}
			s.patches[id] = string(raw)
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(gitea.IssueComment{ID: id, Body: "updated"}))
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(s.comments))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}
}

type cardTestFactory struct{ url string }

func (f *cardTestFactory) GetGiteaClient(token string) *gitea.Client {
	return gitea.NewClient(f.url, token)
}

func newCardTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/card.db")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}

// newCardTestTask creates an agent + task pair on "owner/repo" (tasks have a
// foreign key on agents, so the agent row must exist first).
func newCardTestTask(t *testing.T, db *store.DB) (*store.Agent, *store.Task) {
	t.Helper()
	agent := &store.Agent{Name: "code-review", GiteaUsername: "code-review", GiteaToken: "agent-token", Role: "review", Status: "active"}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{Event: "Review PR", Repo: "owner/repo", IssueID: 5, AgentID: agent.ID, TaskType: "review_pr", Status: store.StatusPending, Role: "review"}
	require.NoError(t, db.CreateTask(task))
	return agent, task
}

func runningCard(task *store.Task) workflow.StatusCard {
	return workflow.StatusCard{TaskID: task.ID, AgentName: "code-review", Role: "审查", State: workflow.StatusCardRunning, StartedAt: time.Now(), Trigger: "@code-review"}
}

func successCard(task *store.Task) workflow.StatusCard {
	return workflow.StatusCard{TaskID: task.ID, AgentName: "code-review", Role: "审查", State: workflow.StatusCardSuccess, StartedAt: time.Now(), Duration: 4 * time.Minute, Trigger: "@code-review", Detail: "✅ 分析完成（task #1）。"}
}

// TestUpdateStatusCardCreatesThenPatchesSameCard is the core guarantee of the
// design: a task creates exactly one comment and updates it in place. This is
// what stops the old pattern of one "已开始处理" comment per task piling up.
func TestUpdateStatusCardCreatesThenPatchesSameCard(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	agent, task := newCardTestTask(t, db)
	f := &cardTestFactory{url: server.URL}

	require.NoError(t, updateStatusCard(f, db, agent, task, 5, runningCard(task)))
	require.Len(t, srv.posts, 1, "first update must create the card")
	assert.Equal(t, int64(901), task.StatusCommentID, "created comment id must be persisted")

	require.NoError(t, updateStatusCard(f, db, agent, task, 5, successCard(task)))
	assert.Len(t, srv.posts, 1, "second update must NOT create another comment")
	require.Len(t, srv.patches, 1, "second update must PATCH the existing card")

	patched, ok := srv.patches[901]
	require.True(t, ok, "the PATCH must target the created card")
	assert.Contains(t, patched, "✅ 完成")
	assert.Contains(t, patched, "耗时 4m0s")
}

// TestUpdateStatusCardRecoversByMarkerAfterIDLoss simulates a restart: the
// persisted ID is gone from memory, but the card still exists on Gitea. The
// marker scan must find it so the task does not get a second card.
func TestUpdateStatusCardRecoversByMarkerAfterIDLoss(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	agent, task := newCardTestTask(t, db)
	f := &cardTestFactory{url: server.URL}

	require.NoError(t, updateStatusCard(f, db, agent, task, 5, runningCard(task)))

	// Restart: in-memory ID lost; the card survives on Gitea.
	task.StatusCommentID = 0
	srv.comments = []gitea.IssueComment{{ID: 901, Body: workflow.RenderStatusCard(runningCard(task))}}

	require.NoError(t, updateStatusCard(f, db, agent, task, 5, successCard(task)))
	assert.Len(t, srv.posts, 1, "must reuse the existing card instead of creating a new one")
	require.Contains(t, srv.patches, 901, "must PATCH the card found by marker")
	assert.Equal(t, int64(901), task.StatusCommentID, "recovered id must be re-persisted")
}

// TestUpdateStatusCardCreatesWhenCardDeleted covers the case where the card was
// deleted on Gitea: PATCH 404s and the marker scan finds nothing, so a fresh
// card is created rather than the state change being silently dropped.
func TestUpdateStatusCardCreatesWhenCardDeleted(t *testing.T) {
	srv := &cardServer{unknownID: true}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	agent, task := newCardTestTask(t, db)
	f := &cardTestFactory{url: server.URL}

	task.StatusCommentID = 777 // stale id: the comment is gone
	require.NoError(t, updateStatusCard(f, db, agent, task, 5, successCard(task)))

	assert.Len(t, srv.posts, 1, "a deleted card must be recreated")
	assert.Equal(t, int64(901), task.StatusCommentID, "task must point at the new card")
}

// TestUpdateStatusCardReturnsErrorWhenGiteaFails ensures callers that must not
// lose information (failure causes, L3 guidance) can detect the failure and fall
// back to a plain comment.
func TestUpdateStatusCardReturnsErrorWhenGiteaFails(t *testing.T) {
	srv := &cardServer{failAll: true}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	agent, task := newCardTestTask(t, db)
	f := &cardTestFactory{url: server.URL}

	err := updateStatusCard(f, db, agent, task, 5, runningCard(task))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create status card")
	assert.Zero(t, task.StatusCommentID)
}

// TestUpdateStatusCardSkipsWithoutAgentToken guards the no-identity case: an
// agent without a Gitea token cannot post or edit anything, and must fail
// loudly rather than posting as an anonymous/incorrect identity.
func TestUpdateStatusCardSkipsWithoutAgentToken(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	agent, task := newCardTestTask(t, db)
	agent.GiteaToken = ""

	err := updateStatusCard(&cardTestFactory{url: server.URL}, db, agent, task, 5, runningCard(task))
	require.Error(t, err)
	assert.Empty(t, srv.posts)
}

// TestStatusCardTiming converts the task's timestamps into the card's start
// stamp and elapsed time; a task still running must report no duration.
func TestStatusCardTiming(t *testing.T) {
	start := time.Date(2026, 8, 28, 12, 25, 16, 0, time.UTC)

	t.Run("running", func(t *testing.T) {
		task := &store.Task{StartedAt: &start}
		got, elapsed := statusCardTiming(task)
		assert.Equal(t, start, got)
		assert.Zero(t, elapsed)
	})

	t.Run("finished", func(t *testing.T) {
		finished := start.Add(42 * time.Minute)
		task := &store.Task{StartedAt: &start, FinishedAt: &finished}
		got, elapsed := statusCardTiming(task)
		assert.Equal(t, start, got)
		assert.Equal(t, 42*time.Minute, elapsed)
	})

	t.Run("not started", func(t *testing.T) {
		got, elapsed := statusCardTiming(&store.Task{})
		assert.True(t, got.IsZero())
		assert.Zero(t, elapsed)
	})
}

// TestStatusCardTrigger prefers the Gitea username — that is the identity a
// reader sees and can @mention back.
func TestStatusCardTrigger(t *testing.T) {
	assert.Equal(t, "@code-review", statusCardTrigger(&store.Agent{GiteaUsername: "code-review", Name: "review-bot"}))
	assert.Equal(t, "@review-bot", statusCardTrigger(&store.Agent{Name: "review-bot"}))
	assert.Empty(t, statusCardTrigger(&store.Agent{}))
}

// TestRenderedCardIsRecognisedAsAgentComment protects the webhook loop guard:
// a card that is not recognised as our own comment would re-trigger a task.
func TestRenderedCardIsRecognisedAsAgentComment(t *testing.T) {
	body := workflow.RenderStatusCard(workflow.StatusCard{TaskID: 13, AgentName: "code-review", State: workflow.StatusCardRunning})
	assert.True(t, workflow.IsAgentComment(body), "card must carry the agent marker prefix")
	assert.True(t, strings.HasPrefix(body, workflow.AgentCommentMarker+"\n"))
	assert.Contains(t, body, workflow.StatusCardMarker(13))
}
