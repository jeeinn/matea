package gitea

import (
	"encoding/json"
	"fmt"
)

// IssueComment posts a comment on the given issue.
func (c *Client) IssueComment(owner, repo string, issueID int, body string) error {
	_, err := c.do("POST", fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, issueID),
		map[string]string{"body": body})
	if err != nil {
		return fmt.Errorf("issue comment: %w", err)
	}
	return nil
}

// CreateIssueComment posts a comment on the given issue and returns the created
// comment, including the ID Gitea assigned to it.
//
// Prefer this over IssueComment whenever the caller may need to update the
// comment later: the task status card is created once at task start and then
// PATCHed in place, instead of stacking one comment per state change.
func (c *Client) CreateIssueComment(owner, repo string, issueID int, body string) (*IssueComment, error) {
	raw, err := c.do("POST", fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, issueID),
		map[string]string{"body": body})
	if err != nil {
		return nil, fmt.Errorf("create issue comment: %w", err)
	}
	var comment IssueComment
	if err := json.Unmarshal(raw, &comment); err != nil {
		return nil, fmt.Errorf("unmarshal created comment: %w", err)
	}
	return &comment, nil
}

// EditIssueComment replaces the body of an existing comment in place
// (PATCH /repos/{owner}/{repo}/issues/comments/{id}).
//
// Gitea only allows editing comments the authenticated identity may modify, so
// callers must edit with the same token that created the comment — the status
// card is created with the task's agent token and must be updated with it too.
func (c *Client) EditIssueComment(owner, repo string, commentID int, body string) error {
	if _, err := c.do("PATCH", fmt.Sprintf("/repos/%s/%s/issues/comments/%d", owner, repo, commentID),
		map[string]string{"body": body}); err != nil {
		return fmt.Errorf("edit issue comment: %w", err)
	}
	return nil
}

// IssueAddLabels adds labels to the given issue.
func (c *Client) IssueAddLabels(owner, repo string, issueID int, labels []string) error {
	_, err := c.do("POST", fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, issueID),
		map[string][]string{"labels": labels})
	if err != nil {
		return fmt.Errorf("issue add labels: %w", err)
	}
	return nil
}

// IssueRemoveLabel removes a label from the given issue.
func (c *Client) IssueRemoveLabel(owner, repo string, issueID int, label string) error {
	// Need to get label ID first, then delete
	// For simplicity, use the label name as ID (Gitea API accepts name)
	_, err := c.do("DELETE", fmt.Sprintf("/repos/%s/%s/issues/%d/labels/%s", owner, repo, issueID, label), nil)
	if err != nil {
		return fmt.Errorf("issue remove label: %w", err)
	}
	return nil
}

// CheckIssueRead performs a side-effect-free read probe of the repo issue
// API. TestConnection uses it to verify the token carries the issue scope
// (runtime needs write:issue to post comments; a read probe is the only
// check that does not mutate anything).
func (c *Client) CheckIssueRead(owner, repo string) error {
	_, err := c.do("GET", fmt.Sprintf("/repos/%s/%s/issues?limit=1", owner, repo), nil)
	if err != nil {
		return fmt.Errorf("issue read probe: %w", err)
	}
	return nil
}

// IssueGet returns the issue details.
func (c *Client) IssueGet(owner, repo string, issueID int) (map[string]interface{}, error) {
	body, err := c.do("GET", fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, issueID), nil)
	if err != nil {
		return nil, fmt.Errorf("issue get: %w", err)
	}

	var issue map[string]interface{}
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("unmarshal issue: %w", err)
	}
	return issue, nil
}

// IssueUnassign removes the given usernames from an issue's assignees.
// Uses DELETE /repos/{owner}/{repo}/issues/{index}/assignees.
func (c *Client) IssueUnassign(owner, repo string, issueID int, usernames ...string) error {
	if len(usernames) == 0 {
		return fmt.Errorf("issue unassign: at least one username required")
	}
	_, err := c.do("DELETE", fmt.Sprintf("/repos/%s/%s/issues/%d/assignees", owner, repo, issueID),
		map[string][]string{"assignees": usernames})
	if err != nil {
		return fmt.Errorf("issue unassign: %w", err)
	}
	return nil
}
