package dispatcher

import "testing"

func TestEffectiveIssueKey(t *testing.T) {
	tests := []struct {
		name       string
		logic, pr  int
		want       int
	}{
		{name: "linked issue", logic: 1, pr: 2, want: 1},
		{name: "plain issue", logic: 7, pr: 0, want: 7},
		{name: "pure PR", logic: 0, pr: 20, want: 20},
		{name: "both zero", logic: 0, pr: 0, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveIssueKey(tt.logic, tt.pr); got != tt.want {
				t.Fatalf("effectiveIssueKey(%d,%d)=%d, want %d", tt.logic, tt.pr, got, tt.want)
			}
		})
	}
}
