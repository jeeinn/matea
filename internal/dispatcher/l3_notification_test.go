package dispatcher

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newL3Dispatcher wires a Dispatcher to the card mock server. The Gitea URL
// points at the test server, so every card write lands in the recorder.
func newL3Dispatcher(t *testing.T, db *store.DB, url string, reg *agents.Registry) *Dispatcher {
	t.Helper()
	d := &Dispatcher{db: db, wfPolicy: workflow.PresetStandard(), registry: reg}
	d.giteaCfg.Store(&config.GiteaConfig{URL: url})
	return d
}

// TestPostL3NotificationCompletesReviewCard is the regression for
// jeeinn/rust-study PR #8: a code review that finished in 56 seconds is still
// showing a card frozen at "🔄 处理中" today, because postL3Notification's
// switch had no review_pr branch — the completion PATCH was never sent.
func TestPostL3NotificationCompletesReviewCard(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	_, task := newCardTestTask(t, db) // review_pr, IssueID=5
	task.PRID = 8

	newL3Dispatcher(t, db, server.URL, nil).postL3Notification(task)

	require.Len(t, srv.posts, 1, "the card must be created exactly once")
	require.Len(t, srv.postPaths, 1)
	// The card belongs on the PR thread: writebackTargetID prefers PRID for
	// review_pr, while the old effectiveIssueKey preferred the linked issue —
	// which put the card on issue 5 and completed it there, leaving the copy
	// on PR 8 untouched.
	assert.Contains(t, srv.postPaths[0], "issues/8/",
		"review card must be posted to the PR thread, got %s", srv.postPaths[0])

	body := srv.posts[0]
	assert.Contains(t, body, "✅ 完成", "a finished review must flip its card to 完成")
	assert.NotContains(t, body, "🔄 处理中", "the card must not stay on 处理中")
}

// TestPostL3NotificationCompletesReplyComment covers the other task type that
// used to fall through the switch untouched, leaving the same frozen card.
func TestPostL3NotificationCompletesReplyComment(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	_, task := newCardTestTask(t, db)
	task.TaskType = "reply_comment"

	newL3Dispatcher(t, db, server.URL, nil).postL3Notification(task)

	require.Len(t, srv.posts, 1)
	assert.Contains(t, srv.posts[0], "✅ 完成")
}

// TestPostL3NotificationCoderPRUsesNativeReference asserts the PR guidance no
// longer inlines an absolute URL. gitea.url is an internal address under
// docker-compose, so jeeinn/rust-study issue #7 carries a link to
// http://localhost:3000/... that no reader outside that host can open.
func TestPostL3NotificationCoderPRUsesNativeReference(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	_, task := newCardTestTask(t, db)
	task.TaskType = "solve_issue"
	task.PRID = 8

	// newCardTestTask already seeded a review-role agent named "code-review",
	// so the registry has someone to name in the guidance.
	reg := agents.NewRegistry()
	require.NoError(t, reg.LoadFromDB(db))
	require.Contains(t, reg.GiteaUsernamesByRole("review"), "code-review", "precondition: a review agent must be registered")

	newL3Dispatcher(t, db, server.URL, reg).postL3Notification(task)

	require.Len(t, srv.posts, 1)
	body := srv.posts[0]
	assert.NotContains(t, body, "http://", "guidance must use a native #N reference, not an absolute URL")
	assert.Contains(t, body, "PR 已创建：#8")
	// The old wording ("Request reviewer Agent 进行代码审查") told the user to
	// ask for a review without saying who to ask.
	assert.Contains(t, body, "@code-review", "guidance must name a real account the user can mention")
	assert.Contains(t, body, "✅ 完成")
}

// TestPostL3NotificationAnalyzeDoneTaskIDIsNotAReference guards the other
// place a task id used to be rendered as "#N", which Gitea turns into a link
// to an unrelated issue.
func TestPostL3NotificationAnalyzeDoneTaskIDIsNotAReference(t *testing.T) {
	srv := &cardServer{}
	server := httptest.NewServer(srv.handler(t))
	defer server.Close()

	db := newCardTestDB(t)
	_, task := newCardTestTask(t, db)
	task.TaskType = "analyze_issue"

	newL3Dispatcher(t, db, server.URL, nil).postL3Notification(task)

	require.Len(t, srv.posts, 1)
	body := srv.posts[0]
	assert.Contains(t, body, "分析完成")
	assert.Contains(t, body, "任务 "+strconv.FormatInt(task.ID, 10))
	assert.NotContains(t, body, "task #", "task id must not render as a #N reference")
}

func TestReviewerHint(t *testing.T) {
	// No registry at all (workflow components not wired) must still yield an
	// actionable sentence rather than a dangling "@".
	assert.Contains(t, reviewerHint(nil), "指派")

	db := newCardTestDB(t)
	reg := agents.NewRegistry()
	require.NoError(t, reg.LoadFromDB(db))
	assert.Contains(t, reviewerHint(reg), "指派", "with no review agent the hint must stay actionable")

	require.NoError(t, db.CreateAgent(&store.Agent{
		Name: "code-review", GiteaUsername: "code-review", Role: "review", Status: "active",
	}))
	require.NoError(t, reg.LoadFromDB(db))
	hint := reviewerHint(reg)
	assert.Contains(t, hint, "@code-review")
	assert.Contains(t, hint, "请求代码审查")
}
