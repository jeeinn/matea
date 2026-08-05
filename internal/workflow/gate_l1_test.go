package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
)

func TestL1ReviewRequiresPR(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	// Create a workflow context with no PR
	_, err := db.GetOrCreateWorkflowContext("owner/repo", 1)
	require.NoError(t, err)

	reviewAgent := &store.Agent{ID: 1, Name: "reviewer", Role: store.RoleReview, Status: "active"}

	// Issue event with no PR → should fail
	evt := &giteaingress.WebhookEvent{
		Event: "issues",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{Number: 1},
	}
	result := gate.CheckL1(evt, store.RoleReview, reviewAgent)
	assert.False(t, result.Allowed)
	assert.Equal(t, "hard", result.Level)
	assert.Equal(t, "l1.review_requires_pr", result.Code)
}

func TestL1ReviewWithPRID(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	// Create workflow context with a PR
	ctx, err := db.GetOrCreateWorkflowContext("owner/repo", 2)
	require.NoError(t, err)
	ctx.PRID = 10
	require.NoError(t, db.UpdateWorkflowContext(ctx))

	reviewAgent := &store.Agent{ID: 1, Name: "reviewer", Role: store.RoleReview, Status: "active"}

	evt := &giteaingress.WebhookEvent{
		Event: "issues",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{Number: 2},
	}
	result := gate.CheckL1(evt, store.RoleReview, reviewAgent)
	assert.True(t, result.Allowed)
}

func TestL1ReviewOnPROpen(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	reviewAgent := &store.Agent{ID: 1, Name: "reviewer", Role: store.RoleReview, Status: "active"}

	// PR event with open state → allowed
	evt := &giteaingress.WebhookEvent{
		Event: "pull_request",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		PR:    &giteaingress.PullRequest{Number: 10, State: "open"},
	}
	result := gate.CheckL1(evt, store.RoleReview, reviewAgent)
	assert.True(t, result.Allowed)
}

func TestL1ReviewOnClosedPR(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	reviewAgent := &store.Agent{ID: 1, Name: "reviewer", Role: store.RoleReview, Status: "active"}

	// PR event with closed state → should fail
	evt := &giteaingress.WebhookEvent{
		Event: "pull_request",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		PR:    &giteaingress.PullRequest{Number: 10, State: "closed"},
	}
	result := gate.CheckL1(evt, store.RoleReview, reviewAgent)
	assert.False(t, result.Allowed)
	assert.Equal(t, "l1.review_on_closed_pr", result.Code)
}

func TestL1AnalyzePassesThrough(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	analyzeAgent := &store.Agent{ID: 1, Name: "analyzer", Role: store.RoleAnalyze, Status: "active"}

	evt := &giteaingress.WebhookEvent{
		Event: "issues",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{Number: 1},
	}
	result := gate.CheckL1(evt, store.RoleAnalyze, analyzeAgent)
	assert.True(t, result.Allowed)
	assert.Equal(t, "pass", result.Level)
}

func TestL1CoderPassesThrough(t *testing.T) {
	db := newTestDB(t)
	gate := NewL1Gate(db)

	coderAgent := &store.Agent{ID: 1, Name: "coder", Role: store.RoleCoder, Status: "active"}

	evt := &giteaingress.WebhookEvent{
		Event: "issues",
		Repo:  giteaingress.Repository{FullName: "owner/repo"},
		Issue: &giteaingress.Issue{Number: 1},
	}
	result := gate.CheckL1(evt, store.RoleCoder, coderAgent)
	assert.True(t, result.Allowed)
}

func TestFormatAgentComment(t *testing.T) {
	body := "✅ 分析完成"
	formatted := FormatAgentComment(body)
	assert.Contains(t, formatted, "<!-- matea-agent -->")
	assert.Contains(t, formatted, body)
}

func TestIsAgentComment(t *testing.T) {
	assert.True(t, IsAgentComment("<!-- matea-agent -->\nHello"))
	assert.False(t, IsAgentComment("Regular comment"))
	assert.False(t, IsAgentComment(""))
}
