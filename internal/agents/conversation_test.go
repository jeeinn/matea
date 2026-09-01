package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/jeeinn/matea/internal/store"
)

// Gitea numbers issues and PRs in one space, so "which object is this
// conversation?" is answered by probing both ids. Two things must hold: never
// probe the same object twice (that is a wasted API call per task), and never
// probe 0.

func TestConversationIDs(t *testing.T) {
	tests := []struct {
		name    string
		task    *store.Task
		wantPR  []int
		wantIss []int
	}{
		{
			name:    "PR conversation with a linked issue",
			task:    &store.Task{PRID: 8, IssueID: 7},
			wantPR:  []int{8, 7},
			wantIss: []int{7, 8},
		},
		{
			name:    "PR with no linked issue: both fields hold the PR number",
			task:    &store.Task{PRID: 8, IssueID: 8},
			wantPR:  []int{8},
			wantIss: []int{8},
		},
		{
			name:    "plain issue",
			task:    &store.Task{PRID: 0, IssueID: 7},
			wantPR:  []int{7},
			wantIss: []int{7},
		},
		{
			name:    "PRID only",
			task:    &store.Task{PRID: 8},
			wantPR:  []int{8},
			wantIss: []int{8},
		},
		{
			name:    "neither",
			task:    &store.Task{},
			wantPR:  nil,
			wantIss: nil,
		},
		{
			name:    "nil task",
			task:    nil,
			wantPR:  nil,
			wantIss: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantPR, conversationIDsPRFirst(tt.task))
			assert.Equal(t, tt.wantIss, conversationIDsIssueFirst(tt.task))
		})
	}
}

// The two orders exist because the two callers want different objects. The PR
// first order must hit the PR on the first probe (otherwise every PR task pays
// for a 404 on the linked issue), and the issue-first order must hit the
// requirement on the first probe (otherwise the title costs an extra hop).
func TestConversationIDsOrderMatchesWhatEachCallerWants(t *testing.T) {
	task := &store.Task{PRID: 8, IssueID: 7}

	prIDs := conversationIDsPRFirst(task)
	assert.Equal(t, 8, prIDs[0], "draft-branch reuse wants the PR first: IssueID=7 is an issue and would 404 on /pulls/")

	issueIDs := conversationIDsIssueFirst(task)
	assert.Equal(t, 7, issueIDs[0], "the title wants the requirement first: PR #8's own title is an event-name leftover")
}
