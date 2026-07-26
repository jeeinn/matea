package dispatcher

// effectiveIssueKey returns the repo-scoped key for session / workflow / in-flight / comments.
// Linked Issue (Fixes #N) uses logicIssueID; pure PR (no linked issue) falls back to prID
// so distinct PRs do not collide on issue_id=0.
func effectiveIssueKey(logicIssueID, prID int) int {
	if logicIssueID > 0 {
		return logicIssueID
	}
	if prID > 0 {
		return prID
	}
	return 0
}
