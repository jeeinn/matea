package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

func setupMentionRegistry() *agents.Registry {
	reg := agents.NewRegistry()
	reg.Refresh(&store.Agent{ID: 1, Name: "analyze-007", GiteaUsername: "analyze-007", Role: store.RoleAnalyze, Status: "active"})
	reg.Refresh(&store.Agent{ID: 2, Name: "coder-ds", GiteaUsername: "coder-ds", Role: store.RoleCoder, Status: "active"})
	reg.Refresh(&store.Agent{ID: 3, Name: "reviewer-gpt", GiteaUsername: "reviewer-gpt", Role: store.RoleReview, Status: "active"})
	return reg
}

func TestMentionResolveAnalyzeAgent(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "@analyze-007 请分析一下这个需求",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, int64(1), result.Agent.ID)
	assert.Equal(t, "reply_comment", result.TaskType)
	assert.Equal(t, store.RoleAnalyze, result.Role)
	assert.Equal(t, 5, result.IssueID)
}

func TestMentionResolveCoderAgent(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 10},
		Comment: &giteaingress.Comment{
			Body: "@coder-ds 请修复这个问题",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, int64(2), result.Agent.ID)
	assert.Equal(t, "solve_comment", result.TaskType)
	assert.Equal(t, store.RoleCoder, result.Role)
}

func TestMentionResolveOnPRComment(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "pull_request_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		PR:     &giteaingress.PullRequest{Number: 20},
		Comment: &giteaingress.Comment{
			Body: "@coder-ds 命名改为 CamelCase",
			User: giteaingress.User{Login: "reviewer"},
		},
		Sender: giteaingress.User{Login: "reviewer"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "solve_comment", result.TaskType)
	assert.Equal(t, 20, result.PRID)
	assert.Equal(t, 0, result.IssueID, "pure PR without Fixes #N uses logic_issue_id=0")
}

func TestMentionForceDevMode(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "/dev\n@analyze-007 请直接实现",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "solve_comment", result.TaskType) // /dev forces solve_comment
}

func TestMentionForceReplyMode(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "/reply\n@coder-ds 只回答问题",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "reply_comment", result.TaskType) // /reply forces reply_comment
}

func TestMentionNoAgent(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "这是一条普通评论，没有 @mention",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	assert.Nil(t, result) // No mention → ignore
}

func TestMentionUnknownUser(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "@random-person 请帮忙看看",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	assert.Nil(t, result) // Unknown user → ignore
}

func TestMentionAgentCommentIgnored(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "<!-- matea-agent -->\n✅ 分析完成\n@coder-ds 建议开始实现",
			User: giteaingress.User{Login: "analyze-007"},
		},
		Sender: giteaingress.User{Login: "analyze-007"},
	}

	result := resolver.Resolve(evt)
	assert.Nil(t, result) // Agent comment → ignored
}

func TestMentionReviewerOnPR(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "pull_request_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		PR:     &giteaingress.PullRequest{Number: 15},
		Comment: &giteaingress.Comment{
			Body: "@reviewer-gpt 请审查最新变更",
			User: giteaingress.User{Login: "coder-ds"},
		},
		Sender: giteaingress.User{Login: "coder-ds"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "reply_comment", result.TaskType) // Review role → reply
	assert.Equal(t, store.RoleReview, result.Role)
}

func TestMentionMultipleMentionsFirstAgent(t *testing.T) {
	reg := setupMentionRegistry()
	resolver := NewResolver(reg)

	evt := &giteaingress.WebhookEvent{
		Event:  "issue_comment",
		Action: "created",
		Repo:   giteaingress.Repository{FullName: "owner/repo"},
		Issue:  &giteaingress.Issue{Number: 5},
		Comment: &giteaingress.Comment{
			Body: "@human-user @coder-ds 请实现这个功能",
			User: giteaingress.User{Login: "human"},
		},
		Sender: giteaingress.User{Login: "human"},
	}

	result := resolver.Resolve(evt)
	require.NotNil(t, result)
	assert.Equal(t, "coder-ds", result.Agent.GiteaUsername) // First agent found
}
