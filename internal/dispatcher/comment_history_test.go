package dispatcher

import (
	"strings"
	"testing"

	"github.com/jeeinn/matea/internal/gitea"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelectCommentsForContextPrefersReviewAndKeepsChronology(t *testing.T) {
	comments := []gitea.IssueComment{
		{ID: 1, User: gitea.User{Login: "human"}, Body: "please fix"},
		{ID: 2, User: gitea.User{Login: "ai-reviewer"}, Body: "LICENSE is broken"},
		{ID: 3, User: gitea.User{Login: "ai-coder"}, Body: "🔄 code-developer 已开始处理（task #11）\n<!-- matea-agent -->"},
		{ID: 4, User: gitea.User{Login: "jeeinn"}, Body: "@ai-coder fix per review"},
		{ID: 5, User: gitea.User{Login: "ai-reviewer"}, Body: "also add trailing newline"},
	}
	prefer := map[string]bool{"ai-reviewer": true}

	selected := selectCommentsForContext(comments, prefer, 3)
	require.Len(t, selected, 3)

	// Both review comments kept; fill remaining with newest non-progress other.
	// Selected set is returned in chronological order.
	assert.Equal(t, "ai-reviewer", selected[0].User.Login)
	assert.Equal(t, "LICENSE is broken", selected[0].Body)
	assert.Equal(t, "jeeinn", selected[1].User.Login)
	assert.Equal(t, "@ai-coder fix per review", selected[1].Body)
	assert.Equal(t, "ai-reviewer", selected[2].User.Login)
	assert.Equal(t, "also add trailing newline", selected[2].Body)

	for _, c := range selected {
		assert.False(t, isProgressOnlyComment(c.Body))
	}
}

func TestFormatCommentHistoryMarksReview(t *testing.T) {
	comments := []gitea.IssueComment{
		{User: gitea.User{Login: "ai-reviewer"}, Body: "fix LICENSE"},
		{User: gitea.User{Login: "jeeinn"}, Body: "@ai-coder please apply"},
	}
	prefer := map[string]bool{"ai-reviewer": true}
	out := formatCommentHistory(comments, prefer)
	assert.Contains(t, out, "## Recent PR / Issue comments")
	assert.Contains(t, out, "### Review feedback (priority)")
	assert.Contains(t, out, "[ai-reviewer [review]]:")
	assert.Contains(t, out, "fix LICENSE")
	assert.Contains(t, out, "### Other recent comments")
	assert.Contains(t, out, "[jeeinn]:")
}

func TestTruncateCommentBody(t *testing.T) {
	assert.Equal(t, "short", truncateCommentBody(" short ", 10))
	long := strings.Repeat("a", 20)
	got := truncateCommentBody(long, 10)
	assert.True(t, strings.HasPrefix(got, "aaaaaaaaaa"))
	assert.Contains(t, got, "truncated")
}

func TestCommentFetchTargetPrefersPR(t *testing.T) {
	evt := &giteaingress.WebhookEvent{
		Repo:  giteaingress.Repository{FullName: "jeeinn/rust-study"},
		Issue: &giteaingress.Issue{Number: 2},
		PR:    &giteaingress.PullRequest{Number: 2},
	}
	owner, repo, id := commentFetchTarget(evt)
	assert.Equal(t, "jeeinn", owner)
	assert.Equal(t, "rust-study", repo)
	assert.Equal(t, 2, id)
}

func TestNeedsCommentHistory(t *testing.T) {
	assert.True(t, needsCommentHistory("solve_comment"))
	assert.False(t, needsCommentHistory("solve_issue"))
	assert.False(t, needsCommentHistory("review_pr"))
}
