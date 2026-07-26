package webhook

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEventIssues(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {
			"id": 1,
			"name": "test-repo",
			"full_name": "admin/test-repo",
			"owner": {"id": 1, "login": "admin"}
		},
		"issue": {
			"id": 100,
			"number": 1,
			"title": "Test Issue",
			"body": "Test body",
			"state": "open",
			"user": {"id": 1, "login": "admin"},
			"assignees": [{"id": 2, "login": "ai-agent"}],
			"labels": [{"id": 1, "name": "bug"}]
		},
		"sender": {"id": 1, "login": "admin"}
	}`)

	evt, err := ParseEvent("issues", "delivery-001", payload)
	if err != nil {
		t.Fatalf("ParseEvent failed: %v", err)
	}

	if evt.Event != "issues" {
		t.Errorf("Expected event=issues, got %s", evt.Event)
	}
	if evt.Action != "assigned" {
		t.Errorf("Expected action=assigned, got %s", evt.Action)
	}
	if evt.Repo.FullName != "admin/test-repo" {
		t.Errorf("Expected repo=admin/test-repo, got %s", evt.Repo.FullName)
	}
	if evt.Issue == nil {
		t.Fatal("Expected issue to be non-nil")
	}
	if evt.Issue.Number != 1 {
		t.Errorf("Expected issue number=1, got %d", evt.Issue.Number)
	}
	if evt.DeliveryID != "delivery-001" {
		t.Errorf("Expected deliveryID=delivery-001, got %s", evt.DeliveryID)
	}

	t.Logf("Parsed event: %s/%s repo=%s", evt.Event, evt.Action, evt.Repo.FullName)
}

func TestParseEventPullRequest(t *testing.T) {
	payload := []byte(`{
		"action": "opened",
		"repository": {
			"id": 1,
			"name": "test-repo",
			"full_name": "admin/test-repo",
			"owner": {"id": 1, "login": "admin"}
		},
		"pull_request": {
			"id": 200,
			"number": 10,
			"title": "New Feature",
			"body": "Feature description",
			"state": "open",
			"user": {"id": 1, "login": "admin"},
			"head": {"ref": "feature", "repo": {"full_name": "admin/test-repo"}},
			"base": {"ref": "main", "repo": {"full_name": "admin/test-repo"}}
		},
		"sender": {"id": 1, "login": "admin"}
	}`)

	evt, err := ParseEvent("pull_request", "delivery-002", payload)
	if err != nil {
		t.Fatalf("ParseEvent failed: %v", err)
	}

	if evt.PR == nil {
		t.Fatal("Expected PR to be non-nil")
	}
	if evt.PR.Number != 10 {
		t.Errorf("Expected PR number=10, got %d", evt.PR.Number)
	}

	t.Logf("Parsed PR event: %s/%s", evt.Event, evt.Action)
}

func TestHasLabel(t *testing.T) {
	evt := &WebhookEvent{
		Issue: &Issue{
			Labels: []Label{
				{ID: 1, Name: "bug"},
				{ID: 2, Name: "enhancement"},
			},
		},
	}

	if !evt.HasLabel("bug") {
		t.Error("Expected to find label 'bug'")
	}
	if !evt.HasLabel("enhancement") {
		t.Error("Expected to find label 'enhancement'")
	}
	if evt.HasLabel("nonexistent") {
		t.Error("Should not find label 'nonexistent'")
	}

	// Test with nil issue
	evt2 := &WebhookEvent{}
	if evt2.HasLabel("bug") {
		t.Error("Should return false for nil issue")
	}
}

func TestHasAssignee(t *testing.T) {
	evt := &WebhookEvent{
		Issue: &Issue{
			Assignees: []User{
				{ID: 1, Login: "user1"},
				{ID: 2, Login: "ai-agent"},
			},
		},
	}

	if !evt.HasAssignee("ai-agent") {
		t.Error("Expected to find assignee 'ai-agent'")
	}
	if evt.HasAssignee("nonexistent") {
		t.Error("Should not find assignee 'nonexistent'")
	}
}

func TestHasMention(t *testing.T) {
	evt := &WebhookEvent{
		Comment: &Comment{
			Body: "Hey @ai-agent, please review this",
		},
	}

	if !evt.HasMention("ai-agent") {
		t.Error("Expected to find mention '@ai-agent'")
	}
	if evt.HasMention("nonexistent") {
		t.Error("Should not find mention '@nonexistent'")
	}

	// Test with nil comment
	evt2 := &WebhookEvent{}
	if evt2.HasMention("ai-agent") {
		t.Error("Should return false for nil comment")
	}
}

func TestParseEventAssignee(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {
			"id": 1,
			"name": "repo",
			"full_name": "owner/repo"
		},
		"issue": {
			"id": 1,
			"number": 5,
			"title": "Test",
			"state": "open",
			"user": {"id": 1, "login": "admin"},
			"assignees": [
				{"id": 1, "login": "admin"},
				{"id": 2, "login": "coder-ds"}
			]
		},
		"assignee": {"id": 2, "login": "coder-ds"},
		"sender": {"id": 1, "login": "admin"}
	}`)

	evt, err := ParseEvent("issues", "del-assignee", payload)
	require.NoError(t, err)

	// Assignee is the single user this event is about
	require.NotNil(t, evt.Assignee)
	assert.Equal(t, "coder-ds", evt.Assignee.Login)
	assert.Equal(t, 2, evt.Assignee.ID)

	// Issue.Assignees is the full list
	assert.Len(t, evt.Issue.Assignees, 2)
}

func TestParseEventPullRequestReviewRequested(t *testing.T) {
	payload := []byte(`{
		"action": "review_requested",
		"repository": {
			"id": 1,
			"name": "repo",
			"full_name": "owner/repo"
		},
		"pull_request": {
			"id": 10,
			"number": 3,
			"title": "Fix bug",
			"state": "open",
			"user": {"id": 1, "login": "coder-ds"},
			"head": {"ref": "fix-3", "repo": {"full_name": "owner/repo"}},
			"base": {"ref": "main", "repo": {"full_name": "owner/repo"}},
			"requested_reviewers": [
				{"id": 5, "login": "reviewer-gpt"}
			]
		},
		"sender": {"id": 1, "login": "coder-ds"}
	}`)

	evt, err := ParseEvent("pull_request", "del-review", payload)
	require.NoError(t, err)

	require.NotNil(t, evt.PR)
	require.Len(t, evt.PR.RequestedReviewers, 1)
	assert.Equal(t, "reviewer-gpt", evt.PR.RequestedReviewers[0].Login)
}

func TestParseEventPullRequestReviewRequestedGiteaFormat(t *testing.T) {
	payload := []byte(`{
		"action": "review_requested",
		"repository": {
			"id": 1,
			"name": "repo",
			"full_name": "owner/repo"
		},
		"pull_request": {
			"id": 10,
			"number": 3,
			"title": "Fix bug",
			"state": "open",
			"user": {"id": 1, "login": "coder-ds"},
			"head": {"ref": "fix-3", "repo": {"full_name": "owner/repo"}},
			"base": {"ref": "main", "repo": {"full_name": "owner/repo"}}
		},
		"requested_reviewer": {"id": 5, "login": "reviewer-gpt"},
		"sender": {"id": 1, "login": "coder-ds"}
	}`)

	evt, err := ParseEvent("pull_request", "del-review-gitea", payload)
	require.NoError(t, err)

	require.NotNil(t, evt.PR)
	require.Len(t, evt.PR.RequestedReviewers, 1)
	assert.Equal(t, "reviewer-gpt", evt.PR.RequestedReviewers[0].Login)
	assert.Equal(t, 5, evt.PR.RequestedReviewers[0].ID)
}

func TestParseEventPullRequestOpenedWithGiteaReviewer(t *testing.T) {
	payload := []byte(`{
		"action": "opened",
		"repository": {
			"id": 1,
			"name": "repo",
			"full_name": "owner/repo"
		},
		"pull_request": {
			"id": 11,
			"number": 4,
			"title": "New feature",
			"state": "open",
			"user": {"id": 1, "login": "coder-ds"},
			"head": {"ref": "feat-4", "repo": {"full_name": "owner/repo"}},
			"base": {"ref": "main", "repo": {"full_name": "owner/repo"}}
		},
		"requested_reviewer": {"id": 5, "login": "reviewer-gpt"},
		"sender": {"id": 1, "login": "coder-ds"}
	}`)

	evt, err := ParseEvent("pull_request", "del-opened-review", payload)
	require.NoError(t, err)

	require.NotNil(t, evt.PR)
	require.Len(t, evt.PR.RequestedReviewers, 1)
	assert.Equal(t, "reviewer-gpt", evt.PR.RequestedReviewers[0].Login)
}

func TestParseEventIssueAssigneeInsideIssue(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {
			"id": 1,
			"name": "repo",
			"full_name": "jeeinn/matea"
		},
		"issue": {
			"id": 1,
			"number": 5,
			"title": "Test",
			"state": "open",
			"user": {"id": 1, "login": "jeeinn"},
			"assignee": {"id": 2, "login": "issue-analyze"},
			"assignees": [
				{"id": 2, "login": "issue-analyze"}
			]
		},
		"sender": {"id": 1, "login": "jeeinn"}
	}`)

	evt, err := ParseEvent("issues", "del-issue-assignee", payload)
	require.NoError(t, err)
	require.NotNil(t, evt.Assignee)
	assert.Equal(t, "issue-analyze", evt.Assignee.Login)
}

func TestParseEventIssueAssignOnlyAssigneesList(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "repo", "full_name": "jeeinn/matea"},
		"issue": {
			"id": 1, "number": 5, "title": "Test", "state": "open",
			"user": {"id": 1, "login": "jeeinn"},
			"assignees": [{"id": 2, "login": "issue-analyze"}]
		},
		"sender": {"id": 1, "login": "jeeinn"}
	}`)

	evt, err := ParseEvent("issues", "del-assignees-only", payload)
	require.NoError(t, err)
	require.NotNil(t, evt.Assignee)
	assert.Equal(t, "issue-analyze", evt.Assignee.Login)
}

func TestParseEventIssueAssignEventType(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "repo", "full_name": "jeeinn/matea"},
		"issue": {
			"id": 1, "number": 5, "title": "Test", "state": "open",
			"user": {"id": 1, "login": "jeeinn"},
			"assignee": {"id": 2, "login": "issue-analyze"}
		},
		"sender": {"id": 1, "login": "jeeinn"}
	}`)

	evt, err := ParseEvent("issue_assign", "del-issue-assign", payload)
	require.NoError(t, err)
	assert.Equal(t, "issues", evt.Event)
	require.NotNil(t, evt.Assignee)
	assert.Equal(t, "issue-analyze", evt.Assignee.Login)
}

func TestParseEventNoAssignee(t *testing.T) {
	payload := []byte(`{
		"action": "opened",
		"repository": {"id": 1, "name": "repo", "full_name": "owner/repo"},
		"issue": {
			"id": 1, "number": 1, "title": "Test", "state": "open",
			"user": {"id": 1, "login": "admin"}
		},
		"sender": {"id": 1, "login": "admin"}
	}`)

	evt, err := ParseEvent("issues", "del-no-assignee", payload)
	require.NoError(t, err)
	assert.Nil(t, evt.Assignee)
}

func TestIssueIsPullRequest(t *testing.T) {
	payload := []byte(`{
		"action": "created",
		"repository": {"id": 1, "name": "repo", "full_name": "owner/repo"},
		"issue": {
			"id": 2, "number": 2, "title": "PR", "body": "Fixes #1", "state": "open",
			"user": {"id": 1, "login": "admin"},
			"html_url": "http://gitea/owner/repo/pulls/2",
			"pull_request": {}
		},
		"comment": {"id": 1, "body": "@coder hi", "user": {"id": 1, "login": "human"}},
		"sender": {"id": 1, "login": "human"}
	}`)
	evt, err := ParseEvent("issue_comment", "del-pr-comment", payload)
	require.NoError(t, err)
	require.NotNil(t, evt.Issue)
	assert.True(t, evt.Issue.IsPullRequest())

	plain := &Issue{Number: 3, HTMLURL: "http://gitea/owner/repo/issues/3"}
	assert.False(t, plain.IsPullRequest())
}
