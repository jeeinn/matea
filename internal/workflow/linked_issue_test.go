package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

// The resolver decides which issue a PR conversation is ABOUT, and that number
// is the session key: (repo, issueID, agent, role). Before "Refs" was
// understood, every Matea PR — whose body ends with "Refs #N", the footer
// prBody writes — looked like it had no linked issue at all, so a task on the
// PR was keyed to the PR's own number and landed in a brand new session. The
// session that opened the PR, keyed to the issue, held the coder's LastHead
// continuation anchor, and the new one could not see it.
//
// jeeinn/rust-study: a continuation task on PR #8 ("Refs #7") was keyed 8
// while the task that opened the PR was keyed 7.

func TestExtractLinkedIssue(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "Gitea close keyword", body: "Fixes #7", want: 7},
		{name: "close keyword, past tense", body: "Closed #12", want: 12},
		{name: "resolves keyword", body: "Resolves #3", want: 3},
		// The case that was missing: prBody's own footer.
		{name: "Refs footer written by prBody", body: "body\n\n---\n*Task ID: 17*\n\nRefs #7", want: 7},
		{name: "ref, singular", body: "ref #4", want: 4},
		{name: "references, spelled out", body: "References #42", want: 42},
		{name: "case insensitive", body: "REFS #7", want: 7},
		// A body carrying both a reference and a close keyword: the close
		// wins regardless of position (see resolver.go extractLinkedIssue).
		{name: "close beats reference regardless of position", body: "Refs #7\n\nFixes #9", want: 9},
		// Must NOT match: an inline cross-reference inside prose is not the
		// "what is this about" link, and neither is a bare # with no keyword.
		{name: "no keyword", body: "see #7", want: 0},
		{name: "empty body", body: "", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractLinkedIssue(tt.body))
		})
	}
}

// prEvent makes a pull_request-flavoured webhook: Gitea sends both the PR
// object and, for issue_comment on a PR, the PR body on evt.Issue.
func prEvent(number int, body string) *giteaingress.WebhookEvent {
	return &giteaingress.WebhookEvent{
		Event: "issue_comment",
		PR:    &giteaingress.PullRequest{Number: number, Body: body},
		Issue: &giteaingress.Issue{Number: number, Body: body, HTMLURL: "http://gitea/o/r/pulls/8"},
	}
}

func TestResolveLogicIssueAndPRReadsRefs(t *testing.T) {
	r := NewResolver(setupRegistry())

	// The live case: PR #8 answering issue #7, footer written by prBody.
	issueID, prID := r.ResolveLogicIssueAndPR(prEvent(8, "## AI 生成的解决方案\n\nblah\n\n---\n*Task ID: 14*\n\nRefs #7"))
	assert.Equal(t, 8, prID, "the conversation id is the PR")
	assert.Equal(t, 7, issueID, "a PR carrying 'Refs #7' is ABOUT issue #7 — without this the task is keyed 8 and gets a brand new session")

	// A PR with no cross-reference at all stays keyed to itself.
	issueID, prID = r.ResolveLogicIssueAndPR(prEvent(20, "no references here"))
	assert.Equal(t, 20, prID)
	assert.Equal(t, 0, issueID, "no linked issue: the caller falls back to prID as the effective key")

	// A plain issue is never mistaken for a PR conversation.
	evt := &giteaingress.WebhookEvent{Event: "issues", Issue: &giteaingress.Issue{Number: 7, Body: "Refs #3"}}
	issueID, prID = r.ResolveLogicIssueAndPR(evt)
	require.Equal(t, 0, prID)
	assert.Equal(t, 7, issueID, "an issue conversation is keyed to itself; its own body is not a PR cross-reference")
}
