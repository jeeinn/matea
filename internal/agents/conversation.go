package agents

import "github.com/jeeinn/matea/internal/store"

// Gitea numbers issues and pull requests in ONE space, and a task stores both
// ids, so "which object is this conversation?" has to be answered by probing:
//
//   - PRID is set when the resolver recognised a pull request conversation.
//   - IssueID is the effective key: the linked issue when the resolver found a
//     cross-reference in the PR body ("Refs #7"), otherwise the PR's own
//     number.
//
// Either field can therefore hold the PR, and they are often equal. Probing
// both without de-duplicating means two API calls for the same object on every
// task, and — worse — silently different results depending on which one
// answered first.
//
// The order is NOT shared, because the two callers want different things:
// conversationIDsPRFirst is for "find me a pull request" (PRID is a PR by
// definition, so it hits on the first try), conversationIDsIssueFirst is for
// "find me what this work is about" (IssueID is the requirement, so it hits on
// the first try). Both are at most 2 ids, never 0, never repeated.
//
// This is not a duplicate of dispatcher.effectiveIssueKey, which answers a
// different question — "which single id keys this task's session and row" — and
// lives in a package that imports this one, so it could not be called from here
// anyway.

func conversationIDs(task *store.Task, prFirst bool) []int {
	if task == nil {
		return nil
	}
	prID, issueID := task.PRID, task.IssueID
	switch {
	case prID <= 0 && issueID <= 0:
		return nil
	case prID <= 0:
		return []int{issueID}
	case issueID <= 0:
		return []int{prID}
	case prID == issueID:
		return []int{prID}
	}
	if prFirst {
		return []int{prID, issueID}
	}
	return []int{issueID, prID}
}

// conversationIDsPRFirst orders the conversation ids for a lookup that wants a
// pull request: git_sync draft-branch reuse.
func conversationIDsPRFirst(task *store.Task) []int {
	return conversationIDs(task, true)
}

// conversationIDsIssueFirst orders the conversation ids for a lookup that
// wants the requirement: the title a PR should be given.
func conversationIDsIssueFirst(task *store.Task) []int {
	return conversationIDs(task, false)
}
