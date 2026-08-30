package agents

import (
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
)

// PR title construction (2026-08-30). Two failures observed live on
// jeeinn/rust-study PR #9, which was titled "AI Solution: AI Solution: issues":
//
//  1. the subject came from PR #8's own title, which already carried the
//     "AI Solution:" prefix, and it was prefixed again;
//  2. what remained was "issues" — the webhook event name left over from the
//     previous title bug — so even a single prefix would have said nothing
//     about the change.
//
// The shared fake Gitea (fakeObject / newPRContinuationServer) lives in
// gitsync_pr_continuation_test.go: Gitea numbers issues and PRs in one space,
// so the same fixture serves both files.

func TestPRTitleDoesNotStackPrefixes(t *testing.T) {
	// jeeinn/rust-study PR #9 was titled "AI Solution: AI Solution: issues".
	assert.Equal(t, "AI Solution: 修复登录", buildPRTitle("AI Solution", "修复登录"))
	assert.Equal(t, "AI Solution: issues", buildPRTitle("AI Solution", "AI Solution: issues"))
	assert.Equal(t, "Bugfix: 修复登录", buildPRTitle("Bugfix", "AI Solution: 修复登录"))
	assert.Equal(t, "AI Solution", buildPRTitle("AI Solution", ""))
}

func TestPRTitleSubjectPrefersRealIssueTitle(t *testing.T) {
	objects := []fakeObject{
		{number: 7, title: "更新项目的readme，符合当前项目最新描述"},
		{number: 8, title: "AI Solution: issues", body: "## AI Generated Solution\n\nblah\n\n---\n*Task ID: 14*\n\nRefs #7", state: "open"},
	}
	srv := newPRContinuationServer(t, objects, nil)
	defer srv.Close()
	client := gitea.NewClient(srv.URL, "tok")

	// PR #8's own title is an event-name leftover; the "Refs #7" line in its
	// body points at the request it was answering.
	got := prTitleSubject(client, "o", "r", &store.Task{ID: 17, IssueID: 8})
	assert.Equal(t, "更新项目的readme，符合当前项目最新描述", got,
		"a continuation must inherit the original request's title, not the previous PR's broken one")
}

func TestPRTitleSubjectUsesUsableTitleDirectly(t *testing.T) {
	objects := []fakeObject{
		{number: 7, title: "AI Solution: 修复登录超时"},
	}
	srv := newPRContinuationServer(t, objects, nil)
	defer srv.Close()
	client := gitea.NewClient(srv.URL, "tok")

	// A prefixed title from another Matea PR is reused with its prefix
	// stripped — buildPRTitle puts the current task's prefix back on.
	got := prTitleSubject(client, "o", "r", &store.Task{ID: 17, IssueID: 7})
	assert.Equal(t, "修复登录超时", got)
}

func TestPRTitleSubjectFallsBackToTaskID(t *testing.T) {
	objects := []fakeObject{
		// Event-name leftover with no "Refs #N" to follow.
		{number: 8, title: "AI Solution: issues", body: "no reference here", state: "open"},
	}
	srv := newPRContinuationServer(t, objects, nil)
	defer srv.Close()
	client := gitea.NewClient(srv.URL, "tok")

	got := prTitleSubject(client, "o", "r", &store.Task{ID: 17, IssueID: 8})
	assert.Equal(t, "Task 17", got, "an event name must never become a title subject")

	// No client at all (unit / degraded path).
	assert.Equal(t, "AI 任务", prTitleSubject(nil, "o", "r", nil))
}

func TestPRTitleSubjectTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("描", maxPRTitleRunes+20)
	objects := []fakeObject{{number: 7, title: long}}
	srv := newPRContinuationServer(t, objects, nil)
	defer srv.Close()

	got := prTitleSubject(gitea.NewClient(srv.URL, "tok"), "o", "r", &store.Task{ID: 17, IssueID: 7})
	assert.LessOrEqual(t, len([]rune(got)), maxPRTitleRunes+3, "long titles are truncated so the prefix stays visible")
	assert.True(t, strings.HasSuffix(got, "..."))
}

func TestExtractRefsIssue(t *testing.T) {
	assert.Equal(t, 7, extractRefsIssue("body\n\nRefs #7"))
	assert.Equal(t, 7, extractRefsIssue("body\n\nrefs #7\n"))
	// An inline reference inside prose or a quoted diff is not a cross-reference.
	assert.Equal(t, 0, extractRefsIssue("代码中提到了 refs #7 不算引用"))
	assert.Equal(t, 0, extractRefsIssue(""))
	assert.Equal(t, 0, extractRefsIssue("Refs #abc"))
}

func TestCleanPRTitleSubject(t *testing.T) {
	subject, ok := cleanPRTitleSubject("  修复登录  ")
	assert.True(t, ok)
	assert.Equal(t, "修复登录", subject)

	subject, ok = cleanPRTitleSubject("Bugfix: 修复登录")
	assert.True(t, ok)
	assert.Equal(t, "修复登录", subject)

	_, ok = cleanPRTitleSubject("issues")
	assert.False(t, ok, "webhook event names are the fingerprint of the old title bug")

	_, ok = cleanPRTitleSubject("   ")
	assert.False(t, ok)

	// Case-insensitive event-name detection.
	_, ok = cleanPRTitleSubject("Pull_Request")
	assert.False(t, ok)
}
