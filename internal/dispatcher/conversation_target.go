package dispatcher

// conversationTarget picks where a reply about a conversation belongs.
//
// It is NOT always the effective issue key. Since the resolver learned to read
// "Refs #N" out of a PR body (2026-08-31), a task on a pull request is keyed
// to the ORIGINAL issue: that is what keeps the session — and therefore the
// coder's LastHead continuation anchor — continuous across the PR. But the
// user asked on the PR, and a reply posted on the issue lands somewhere they
// are not looking. jeeinn/rust-study: "@code-opencode" on PR #8 must answer on
// #8 even though that task's session key is #7.
//
// Keys that address STORAGE stay on the effective key (session, workflow
// context, in-flight lock). Keys that address a HUMAN go through this.
func conversationTarget(issueID, prID int) int {
	if prID > 0 {
		return prID
	}
	return issueID
}
