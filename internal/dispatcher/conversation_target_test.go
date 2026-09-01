package dispatcher

import (
	"testing"

	"github.com/jeeinn/matea/internal/store"
)

// The 2026-08-31 root-cause fix split two things that used to be the same
// number: where a task is KEYED (the linked issue, so the session and the
// coder's LastHead anchor stay continuous across the PR) and where its REPLY
// goes (the PR, because that is where the user asked).
//
// jeeinn/rust-study is the live case: PR #8 carries "Refs #7", so a task on
// PR #8 is keyed 7 but must answer on 8.

func TestConversationTargetPrefersThePR(t *testing.T) {
	tests := []struct {
		name          string
		issueID, prID int
		want          int
	}{
		{name: "PR conversation: reply on the PR, not the linked issue", issueID: 7, prID: 8, want: 8},
		{name: "pure issue: PRID unset", issueID: 7, prID: 0, want: 7},
		{name: "pure PR: IssueID is the PR number", issueID: 20, prID: 20, want: 20},
		{name: "PR with no linked issue: IssueID fell back to PRID", issueID: 8, prID: 8, want: 8},
		{name: "nothing resolvable", issueID: 0, prID: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := conversationTarget(tt.issueID, tt.prID); got != tt.want {
				t.Errorf("conversationTarget(%d, %d) = %d, want %d", tt.issueID, tt.prID, got, tt.want)
			}
		})
	}
}

// The point of the split: if conversationTarget ever degenerates back into
// "prefer IssueID", a write task on a PR posts its result on the linked issue
// and the user never sees it on the PR they were commenting on.
func TestWritebackTargetIDAnswersOnThePRNotTheLinkedIssue(t *testing.T) {
	tests := []struct {
		name   string
		task   *store.Task
		want   int
		wantOK bool
	}{
		{
			name:   "write task on PR #8 keyed to issue #7",
			task:   &store.Task{IssueID: 7, PRID: 8, TaskType: "solve_issue"},
			want:   8,
			wantOK: true,
		},
		{
			name:   "review_pr on PR #8 keyed to issue #7",
			task:   &store.Task{IssueID: 7, PRID: 8, TaskType: "review_pr"},
			want:   8,
			wantOK: true,
		},
		{
			name:   "reply_comment on PR #8 keyed to issue #7",
			task:   &store.Task{IssueID: 7, PRID: 8, TaskType: "reply_comment"},
			want:   8,
			wantOK: true,
		},
		{
			name:   "analyze_issue on a plain issue",
			task:   &store.Task{IssueID: 7, PRID: 0, TaskType: "analyze_issue"},
			want:   7,
			wantOK: true,
		},
		{
			name:   "no conversation at all",
			task:   &store.Task{IssueID: 0, PRID: 0},
			want:   0,
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := writebackTargetID(tt.task)
			if ok != tt.wantOK {
				t.Fatalf("writebackTargetID ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("writebackTargetID = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestWritebackTargetIDHandlesNilTask(t *testing.T) {
	if id, ok := writebackTargetID(nil); ok || id != 0 {
		t.Errorf("writebackTargetID(nil) = (%d, %v), want (0, false)", id, ok)
	}
}
