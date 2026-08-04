package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

// setupRegistry creates a registry with test agents for all roles.
func setupRegistry() *agents.Registry {
	reg := agents.NewRegistry()
	reg.Refresh(&store.Agent{ID: 1, Name: "analyze-007", GiteaUsername: "analyze-007", Role: store.RoleAnalyze, Status: "active"})
	reg.Refresh(&store.Agent{ID: 2, Name: "coder-ds", GiteaUsername: "coder-ds", Role: store.RoleCoder, Status: "active"})
	reg.Refresh(&store.Agent{ID: 3, Name: "coder-claude", GiteaUsername: "coder-claude", Role: store.RoleCoder, Status: "active"})
	reg.Refresh(&store.Agent{ID: 4, Name: "reviewer-gpt", GiteaUsername: "reviewer-gpt", Role: store.RoleReview, Status: "active"})
	return reg
}

func buildIssueAssignedEvent(assignee string, labels []string) *giteaingress.WebhookEvent {
	var lbls []giteaingress.Label
	for i, l := range labels {
		lbls = append(lbls, giteaingress.Label{ID: i + 1, Name: l})
	}
	assigneeUser := &giteaingress.User{ID: 100, Login: assignee}
	return &giteaingress.WebhookEvent{
		Event:    "issues",
		Action:   "assigned",
		Assignee: assigneeUser,
		Repo:     giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{
			Number: 42,
			Labels: lbls,
		},
		Sender: giteaingress.User{ID: 1, Login: "human"},
	}
}

func buildPRReviewRequestedEvent(reviewers []string) *giteaingress.WebhookEvent {
	var revs []giteaingress.User
	for i, r := range reviewers {
		revs = append(revs, giteaingress.User{ID: 200 + i, Login: r})
	}
	return &giteaingress.WebhookEvent{
		Event:  "pull_request",
		Action: "review_requested",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		PR: &giteaingress.PullRequest{
			Number:             10,
			Body:               "Fixes #5",
			RequestedReviewers: revs,
		},
		Sender: giteaingress.User{ID: 1, Login: "coder-ds"},
	}
}

func TestResolveAssignedAnalyze(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildIssueAssignedEvent("analyze-007", nil)
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, int64(1), result.Agent.ID)
	assert.Equal(t, "analyze_issue", result.TaskType)
	assert.Equal(t, store.RoleAnalyze, result.Role)
	assert.Equal(t, 42, result.IssueID)
}

func TestResolveAssignedCoderNoBug(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildIssueAssignedEvent("coder-ds", []string{"feature"})
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, int64(2), result.Agent.ID)
	assert.Equal(t, "solve_issue", result.TaskType)
	assert.Equal(t, store.RoleCoder, result.Role)
}

func TestResolveAssignedCoderWithBug(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildIssueAssignedEvent("coder-ds", []string{"bug", "backend"})
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, "fix_bug", result.TaskType)
}

func TestResolveAssignedUnknownUser(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildIssueAssignedEvent("random-user", nil)
	result := resolver.Resolve(evt)

	assert.Nil(t, result)
}

func TestResolveAssignedReviewAgentToIssueIgnored(t *testing.T) {
	// Explicit acceptance point from the 1.1.4a code review:
	// assigning a review agent to a plain Issue must NOT produce review_pr
	// (previously taskTypeForRole returned review_pr unconditionally and the
	// task was rejected later by the L1 gate — same external outcome, but one
	// step later). With the resolve table wired in, the event resolves to nil.
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildIssueAssignedEvent("reviewer-gpt", nil)
	result := resolver.Resolve(evt)

	assert.Nil(t, result, "review agent assigned to a plain Issue must not trigger a task")
}

func TestResolveAssignedFromIssueAssigneeField(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt, err := giteaingress.ParseEvent("issues", "del-1", []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "repo", "full_name": "owner/repo"},
		"issue": {
			"id": 1, "number": 1, "title": "Test", "state": "open",
			"user": {"id": 1, "login": "human"},
			"assignee": {"id": 100, "login": "analyze-007"}
		},
		"sender": {"id": 1, "login": "human"}
	}`))
	require.NoError(t, err)

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "analyze_issue", result.TaskType)
}

func TestResolveAssignedNoAssigneeField(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issues",
		Action: "assigned",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 1},
		Sender: giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	assert.Nil(t, result)
}

func TestResolveReviewRequested(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRReviewRequestedEvent([]string{"reviewer-gpt"})
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, int64(4), result.Agent.ID)
	assert.Equal(t, "review_pr", result.TaskType)
	assert.Equal(t, store.RoleReview, result.Role)
	assert.Equal(t, 10, result.PRID)
	assert.Equal(t, 5, result.IssueID) // From "Fixes #5" in PR body
}

func TestResolveReviewRequestedUnknownReviewer(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRReviewRequestedEvent([]string{"random-person"})
	result := resolver.Resolve(evt)

	assert.Nil(t, result)
}

func TestResolveReviewRequestedNonReviewAgent(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	// coder-ds is not a review agent
	evt := buildPRReviewRequestedEvent([]string{"coder-ds"})
	result := resolver.Resolve(evt)

	assert.Nil(t, result)
}

func TestResolvePROpenedWithReviewer(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRReviewRequestedEvent([]string{"reviewer-gpt"})
	evt.Action = "opened"
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, "review_pr", result.TaskType)
}

func TestResolvePRSynchronizeIgnored(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRReviewRequestedEvent([]string{"reviewer-gpt"})
	evt.Action = "synchronize"
	result := resolver.Resolve(evt)

	assert.Nil(t, result)
}

func TestResolveUnassignedIgnored(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:    "issues",
		Action:   "unassigned",
		Assignee: &giteaingress.User{Login: "analyze-007"},
		Repo:     giteaingress.Repository{FullName: "owner/repo"},
		Issue:    &giteaingress.Issue{Number: 1},
		Sender:   giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	assert.Nil(t, result)
}

func TestResolveLabeledIgnored(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issues",
		Action: "labeled",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 1},
		Sender: giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	assert.Nil(t, result)
}

func TestResolveCommentWithMention(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:   "issue_comment",
		Action:  "created",
		Repo:    giteaingress.Repository{FullName: "owner/repo"},
		Issue:   &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{Body: "@coder-ds please fix this"},
		Sender:  giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "coder-ds", result.Agent.GiteaUsername)
	assert.Equal(t, "solve_comment", result.TaskType)
}

func TestResolveCommentNoMention(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:   "issue_comment",
		Action:  "created",
		Repo:    giteaingress.Repository{FullName: "owner/repo"},
		Issue:   &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{Body: "just a regular comment"},
		Sender:  giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	assert.Nil(t, result) // No @mention → ignore
}

func TestResolveLinkedIssueFromPRBody(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	tests := []struct {
		body     string
		expected int
	}{
		{"Fixes #42", 42},
		{"Closes #100", 100},
		{"Resolves #7", 7},
		{"fixes #99", 99}, // case insensitive
		{"Fixed #55", 55}, // past tense
		{"No linked issue", 0},
		{"", 0},
	}

	for _, tt := range tests {
		evt := buildPRReviewRequestedEvent([]string{"reviewer-gpt"})
		evt.PR.Body = tt.body
		result := resolver.Resolve(evt)
		require.NotNil(t, result, "body: %q", tt.body)
		assert.Equal(t, tt.expected, result.IssueID, "body: %q", tt.body)
	}
}

func TestIsAgentSender(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Sender: giteaingress.User{Login: "coder-ds"},
	}
	assert.True(t, resolver.IsAgentSender(evt))

	evt.Sender.Login = "human-user"
	assert.False(t, resolver.IsAgentSender(evt))
}

func TestResolveMultipleReviewersFirstMatch(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	// Multiple reviewers, first non-agent is skipped, review agent is found
	evt := buildPRReviewRequestedEvent([]string{"random-person", "reviewer-gpt"})
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, "reviewer-gpt", result.Agent.GiteaUsername)
}

// --- P0: PR close / merge detection tests ---

func buildPRClosedEvent(merged bool, prNumber int, body string) *giteaingress.WebhookEvent {
	return &giteaingress.WebhookEvent{
		Event:  "pull_request",
		Action: "closed",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		PR: &giteaingress.PullRequest{
			Number: prNumber,
			State:  "closed",
			Merged: merged,
			Body:   body,
		},
		Sender: giteaingress.User{ID: 1, Login: "coder-ds"},
	}
}

func TestResolvePRClosedMerged(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRClosedEvent(true, 10, "Fixes #5")
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, "archive", result.Lifecycle)
	assert.Equal(t, 10, result.PRID)
	assert.Equal(t, 5, result.IssueID) // From "Fixes #5"
	assert.True(t, result.Merged, "merged PR should set Merged=true")
}

func TestResolvePRClosedNotMerged(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := buildPRClosedEvent(false, 10, "Fixes #5")
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.Equal(t, "archive", result.Lifecycle)
	assert.Equal(t, 10, result.PRID)
	assert.False(t, result.Merged, "closed-without-merge should set Merged=false")
}

func TestResolvePRClosedStateClosedMergedTrue(t *testing.T) {
	// P0 fix: Gitea sends state="closed" + merged=true, NOT state="merged"
	// The old code checked state=="merged" which was always false
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "pull_request",
		Action: "closed",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		PR: &giteaingress.PullRequest{
			Number: 15,
			State:  "closed", // Gitea sends "closed", never "merged"
			Merged: true,     // This is the correct field
			Body:   "Fixes #8",
		},
		Sender: giteaingress.User{ID: 1, Login: "coder-ds"},
	}
	result := resolver.Resolve(evt)

	require.NotNil(t, result)
	assert.True(t, result.Merged, "state=closed + merged=true must be detected as merged")
	assert.Equal(t, 15, result.PRID)
	assert.Equal(t, 8, result.IssueID)
}

func TestPRCommentUsesLinkedLogicIssue(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	// Real-world shape: issue_comment on a PR — issue.number is PR#, body has Fixes #1.
	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{
			Number:      2,
			Body:        "Fixes #1\n\nImplement feature",
			HTMLURL:     "http://gitea/owner/repo/pulls/2",
			PullRequest: []byte("{}"),
		},
		Comment: &giteaingress.Comment{Body: "@coder-ds please address review"},
		Sender:  giteaingress.User{Login: "human"},
	}
	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, 1, result.IssueID, "logic issue from Fixes #1")
	assert.Equal(t, 2, result.PRID, "PR number preserved for PR APIs")
	assert.Equal(t, "solve_comment", result.TaskType)
}

func TestPRCommentAndReviewShareLogicIssue(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	reviewEvt := buildPRReviewRequestedEvent([]string{"reviewer-gpt"})
	reviewEvt.PR.Number = 2
	reviewEvt.PR.Body = "Fixes #1"
	review := resolver.Resolve(reviewEvt)
	require.NotNil(t, review)

	commentEvt := &giteaingress.WebhookEvent{
		Event:   "pull_request_comment",
		Action:  "created",
		Repo:    giteaingress.Repository{FullName: "owner/repo"},
		PR:      &giteaingress.PullRequest{Number: 2, Body: "Fixes #1"},
		Comment: &giteaingress.Comment{Body: "@coder-ds continue"},
		Sender:  giteaingress.User{Login: "human"},
	}
	comment := resolver.Resolve(commentEvt)
	require.NotNil(t, comment)

	assert.Equal(t, review.IssueID, comment.IssueID)
	assert.Equal(t, 1, comment.IssueID)
	assert.Equal(t, 2, comment.PRID)
}

func TestPurePRCommentIssueIDZero(t *testing.T) {
	reg := setupRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:   "pull_request_comment",
		Action:  "created",
		Repo:    giteaingress.Repository{FullName: "owner/repo"},
		PR:      &giteaingress.PullRequest{Number: 20, Body: "no linked issue"},
		Comment: &giteaingress.Comment{Body: "@coder-ds rename please"},
		Sender:  giteaingress.User{Login: "reviewer"},
	}
	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, 0, result.IssueID)
	assert.Equal(t, 20, result.PRID)
}
