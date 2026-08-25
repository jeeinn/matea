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
