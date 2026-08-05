package workflow

import (
	"testing"

	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/stretchr/testify/assert"
)

// TestTaskTypeTable_Completeness verifies all expected combinations are mapped.
func TestTaskTypeTable_Completeness(t *testing.T) {
	roles := []string{store.RoleAnalyze, store.RoleCoder, store.RoleReview}
	surfaces := []Surface{SurfaceIssue, SurfacePR}
	intents := []Intent{IntentAssign, IntentReviewRequested, IntentMention, IntentSlashDev, IntentSlashReply}

	// Count expected mappings (not all combinations are valid)
	expectedMappings := map[ResolveKey]bool{
		// Analyze
		{store.RoleAnalyze, SurfaceIssue, IntentAssign}:      true,
		{store.RoleAnalyze, SurfaceIssue, IntentMention}:     true,
		{store.RoleAnalyze, SurfaceIssue, IntentSlashDev}:    true,
		{store.RoleAnalyze, SurfaceIssue, IntentSlashReply}:  true,
		{store.RoleAnalyze, SurfacePR, IntentMention}:        true,
		{store.RoleAnalyze, SurfacePR, IntentSlashDev}:       true,
		{store.RoleAnalyze, SurfacePR, IntentSlashReply}:     true,

		// Coder
		{store.RoleCoder, SurfaceIssue, IntentAssign}:      true,
		{store.RoleCoder, SurfaceIssue, IntentMention}:     true,
		{store.RoleCoder, SurfaceIssue, IntentSlashDev}:    true,
		{store.RoleCoder, SurfaceIssue, IntentSlashReply}:  true,
		{store.RoleCoder, SurfacePR, IntentMention}:        true,
		{store.RoleCoder, SurfacePR, IntentSlashDev}:       true,
		{store.RoleCoder, SurfacePR, IntentSlashReply}:     true,

		// Review
		{store.RoleReview, SurfacePR, IntentReviewRequested}: true,
		{store.RoleReview, SurfacePR, IntentMention}:         true,
		{store.RoleReview, SurfacePR, IntentSlashDev}:        true,
		{store.RoleReview, SurfacePR, IntentSlashReply}:      true,
		{store.RoleReview, SurfaceIssue, IntentMention}:      true,
		{store.RoleReview, SurfaceIssue, IntentSlashDev}:     true,
		{store.RoleReview, SurfaceIssue, IntentSlashReply}:   true,
	}

	// Verify all expected mappings exist
	for key := range expectedMappings {
		taskType, exists := TaskTypeTable[key]
		assert.True(t, exists, "Missing mapping for %+v", key)
		assert.NotEmpty(t, taskType, "Empty task type for %+v", key)
	}

	// Verify no unexpected mappings
	assert.Equal(t, len(expectedMappings), len(TaskTypeTable), "Table has unexpected entries")

	// Verify intentionally missing combinations
	missingCombinations := []ResolveKey{
		// Review on issue assign - not supported
		{store.RoleReview, SurfaceIssue, IntentAssign},
		// Analyze doesn't have review_requested
		{store.RoleAnalyze, SurfacePR, IntentReviewRequested},
		// Coder doesn't have review_requested
		{store.RoleCoder, SurfacePR, IntentReviewRequested},
		// Review doesn't support assign on issue
		{store.RoleReview, SurfaceIssue, IntentReviewRequested},
	}

	for _, key := range missingCombinations {
		_, exists := TaskTypeTable[key]
		assert.False(t, exists, "Unexpected mapping for invalid combination %+v", key)
	}

	// Sanity check: iterate all combinations to ensure table coverage
	for _, role := range roles {
		for _, surface := range surfaces {
			for _, intent := range intents {
				key := ResolveKey{Role: role, Surface: surface, Intent: intent}
				if expectedMappings[key] {
					taskType := ResolveTaskType(role, surface, intent, nil)
					assert.NotEmpty(t, taskType, "ResolveTaskType returned empty for valid key %+v", key)
				}
			}
		}
	}
}

// TestResolveTaskType_AnalyzeRole verifies analyze role mappings.
func TestResolveTaskType_AnalyzeRole(t *testing.T) {
	tests := []struct {
		surface  Surface
		intent   Intent
		expected string
	}{
		{SurfaceIssue, IntentAssign, "analyze_issue"},
		{SurfaceIssue, IntentMention, "reply_comment"},
		{SurfaceIssue, IntentSlashDev, "solve_comment"},
		{SurfaceIssue, IntentSlashReply, "reply_comment"},
		{SurfacePR, IntentMention, "reply_comment"},
		{SurfacePR, IntentSlashDev, "solve_comment"},
		{SurfacePR, IntentSlashReply, "reply_comment"},
	}

	for _, tt := range tests {
		t.Run(string(tt.surface)+"_"+string(tt.intent), func(t *testing.T) {
			taskType := ResolveTaskType(store.RoleAnalyze, tt.surface, tt.intent, nil)
			assert.Equal(t, tt.expected, taskType)
		})
	}
}

// TestResolveTaskType_CoderRole verifies coder role mappings.
func TestResolveTaskType_CoderRole(t *testing.T) {
	tests := []struct {
		surface  Surface
		intent   Intent
		expected string
	}{
		{SurfaceIssue, IntentAssign, "solve_issue"},
		{SurfaceIssue, IntentMention, "solve_comment"},
		{SurfaceIssue, IntentSlashDev, "solve_comment"},
		{SurfaceIssue, IntentSlashReply, "reply_comment"},
		{SurfacePR, IntentMention, "solve_comment"},
		{SurfacePR, IntentSlashDev, "solve_comment"},
		{SurfacePR, IntentSlashReply, "reply_comment"},
	}

	for _, tt := range tests {
		t.Run(string(tt.surface)+"_"+string(tt.intent), func(t *testing.T) {
			taskType := ResolveTaskType(store.RoleCoder, tt.surface, tt.intent, nil)
			assert.Equal(t, tt.expected, taskType)
		})
	}
}

// TestResolveTaskType_CoderRole_BugLabel verifies bug label special case.
func TestResolveTaskType_CoderRole_BugLabel(t *testing.T) {
	evt := &giteaingress.WebhookEvent{
		Issue: &giteaingress.Issue{
			Number: 1,
			Labels: []giteaingress.Label{
				{Name: "bug"},
			},
		},
	}

	taskType := ResolveTaskType(store.RoleCoder, SurfaceIssue, IntentAssign, evt)
	assert.Equal(t, "fix_bug", taskType, "Coder + assign + bug label should return fix_bug")

	// Without bug label
	evtNoBug := &giteaingress.WebhookEvent{
		Issue: &giteaingress.Issue{
			Number: 1,
			Labels: []giteaingress.Label{
				{Name: "enhancement"},
			},
		},
	}

	taskType = ResolveTaskType(store.RoleCoder, SurfaceIssue, IntentAssign, evtNoBug)
	assert.Equal(t, "solve_issue", taskType, "Coder + assign without bug label should return solve_issue")
}

// TestResolveTaskType_ReviewRole verifies review role mappings.
func TestResolveTaskType_ReviewRole(t *testing.T) {
	tests := []struct {
		surface  Surface
		intent   Intent
		expected string
	}{
		{SurfacePR, IntentReviewRequested, "review_pr"},
		{SurfacePR, IntentMention, "reply_comment"},       // Critical: NOT review_pr
		{SurfacePR, IntentSlashDev, "solve_comment"},
		{SurfacePR, IntentSlashReply, "reply_comment"},
		{SurfaceIssue, IntentMention, "reply_comment"},    // Critical: NOT review_pr
		{SurfaceIssue, IntentSlashDev, "solve_comment"},
		{SurfaceIssue, IntentSlashReply, "reply_comment"},
	}

	for _, tt := range tests {
		t.Run(string(tt.surface)+"_"+string(tt.intent), func(t *testing.T) {
			taskType := ResolveTaskType(store.RoleReview, tt.surface, tt.intent, nil)
			assert.Equal(t, tt.expected, taskType)
		})
	}
}

// TestResolveTaskType_CriticalCases verifies the critical cases from the planning doc.
func TestResolveTaskType_CriticalCases(t *testing.T) {
	t.Run("ReviewOnIssueMention_NotReviewPR", func(t *testing.T) {
		// @matea-review in Issue comment should NOT trigger review_pr
		taskType := ResolveTaskType(store.RoleReview, SurfaceIssue, IntentMention, nil)
		assert.Equal(t, "reply_comment", taskType, "@review in Issue should be reply_comment, not review_pr")
	})

	t.Run("ReviewOnPRMention_NotReviewPR", func(t *testing.T) {
		// @matea-review in PR comment should NOT trigger review_pr (unless review_requested)
		taskType := ResolveTaskType(store.RoleReview, SurfacePR, IntentMention, nil)
		assert.Equal(t, "reply_comment", taskType, "@review in PR comment should be reply_comment, not review_pr")
	})

	t.Run("AnalyzeMention_NotFullAnalysis", func(t *testing.T) {
		// @matea-analyst in comment should be lightweight reply, not full analyze_issue
		taskType := ResolveTaskType(store.RoleAnalyze, SurfaceIssue, IntentMention, nil)
		assert.Equal(t, "reply_comment", taskType, "@analyze in comment should be reply_comment, not analyze_issue")
	})
}

// TestDetermineSurface verifies surface detection from events.
func TestDetermineSurface(t *testing.T) {
	t.Run("NilEvent", func(t *testing.T) {
		surface := DetermineSurface(nil)
		assert.Equal(t, SurfaceIssue, surface)
	})

	t.Run("IssueEvent", func(t *testing.T) {
		evt := &giteaingress.WebhookEvent{
			Issue: &giteaingress.Issue{Number: 1},
		}
		surface := DetermineSurface(evt)
		assert.Equal(t, SurfaceIssue, surface)
	})

	t.Run("PREvent", func(t *testing.T) {
		evt := &giteaingress.WebhookEvent{
			PR: &giteaingress.PullRequest{Number: 1},
		}
		surface := DetermineSurface(evt)
		assert.Equal(t, SurfacePR, surface)
	})

	t.Run("IssueAsPR", func(t *testing.T) {
		evt := &giteaingress.WebhookEvent{
			Issue: &giteaingress.Issue{
				Number:      1,
				PullRequest: []byte(`{}`), // IsPullRequest() will return true
			},
		}
		surface := DetermineSurface(evt)
		assert.Equal(t, SurfacePR, surface)
	})
}
