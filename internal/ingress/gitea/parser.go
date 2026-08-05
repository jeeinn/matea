package gitea

import (
	"encoding/json"
	"log"
	"strings"
)

// WebhookEvent represents the parsed Gitea webhook event.
type WebhookEvent struct {
	DeliveryID        string       `json:"-"`
	Event             string       `json:"-"`
	Action            string       `json:"action"`
	Repo              Repository   `json:"repository"`
	Issue             *Issue       `json:"issue,omitempty"`
	PR                *PullRequest `json:"pull_request,omitempty"`
	Comment           *Comment     `json:"comment,omitempty"`
	Assignee          *User        `json:"assignee,omitempty"`           // Single assignee from issues.assigned event
	RequestedReviewer *User        `json:"requested_reviewer,omitempty"` // Single reviewer from pull_request.review_requested (Gitea format)
	Sender            User         `json:"sender"`
}

type Repository struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Owner         User   `json:"owner"`
	CloneURL      string `json:"clone_url"`
	SSHURL        string `json:"ssh_url"`
	DefaultBranch string `json:"default_branch"`
}

type Issue struct {
	ID        int     `json:"id"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	State     string  `json:"state"`
	User      User    `json:"user"`
	Assignee  *User   `json:"assignee,omitempty"` // Primary assignee (Gitea often puts this inside issue)
	Assignees []User  `json:"assignees"`
	Labels    []Label `json:"labels"`
	HTMLURL   string  `json:"html_url"`
	// PullRequest is present (often `{}`) when this issue object is a PR conversation
	// in issue_comment webhooks. Used to distinguish PR# from Issue#.
	PullRequest json.RawMessage `json:"pull_request,omitempty"`
}

// IsPullRequest reports whether this issue payload represents a pull request
// (Gitea issue_comment on a PR, or HTML URL under /pulls/).
func (i *Issue) IsPullRequest() bool {
	if i == nil {
		return false
	}
	if len(i.PullRequest) > 0 && string(i.PullRequest) != "null" {
		return true
	}
	return strings.Contains(i.HTMLURL, "/pulls/")
}

type PullRequest struct {
	ID                 int    `json:"id"`
	Number             int    `json:"number"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	State              string `json:"state"`
	Merged             bool   `json:"merged"` // Gitea sets this to true when PR is merged (even with state="closed")
	Draft              bool   `json:"draft"`  // Gitea sets this for draft PRs
	User               User   `json:"user"`
	Head               Branch `json:"head"`
	Base               Branch `json:"base"`
	HTMLURL            string `json:"html_url"`
	RequestedReviewers []User `json:"requested_reviewers,omitempty"`
}

type Branch struct {
	Ref  string     `json:"ref"`
	Repo Repository `json:"repo"`
}

type Comment struct {
	ID      int    `json:"id"`
	Body    string `json:"body"`
	User    User   `json:"user"`
	HTMLURL string `json:"html_url"`
}

type User struct {
	ID    int    `json:"id"`
	Login string `json:"login"`
}

type Label struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ParseEvent parses the webhook payload based on the event type.
func ParseEvent(eventType, deliveryID string, payload []byte) (*WebhookEvent, error) {
	var evt WebhookEvent
	if err := json.Unmarshal(payload, &evt); err != nil {
		return nil, err
	}
	evt.DeliveryID = deliveryID
	evt.Event = eventType
	// Gitea may send assignment as issue_assign; normalize to issues for resolver.
	if evt.Event == "issue_assign" {
		evt.Event = "issues"
	}

	normalizeAssignee(&evt)
	normalizeRequestedReviewers(&evt)

	// Normalize action names across Gitea versions
	// Gitea 1.26+ sends "label_updated" instead of "labeled"
	if evt.Action == "label_updated" {
		evt.Action = "labeled"
	}

	log.Printf("[DEBUG] Parsed event: type=%s action=%s repo=%s sender=%s",
		eventType, evt.Action, evt.Repo.FullName, evt.Sender.Login)

	return &evt, nil
}

// normalizeAssignee fills evt.Assignee when Gitea only sends issue.assignee or issue.assignees.
func normalizeAssignee(evt *WebhookEvent) {
	if evt.Assignee != nil {
		return
	}
	if evt.Issue == nil {
		return
	}
	if evt.Issue.Assignee != nil {
		evt.Assignee = evt.Issue.Assignee
		return
	}
	if evt.Action != "assigned" || len(evt.Issue.Assignees) == 0 {
		return
	}
	// Gitea versions differ: some only populate issue.assignees on assign events.
	last := evt.Issue.Assignees[len(evt.Issue.Assignees)-1]
	evt.Assignee = &last
	log.Printf("[DEBUG] Normalized assignee from issue.assignees: %s", last.Login)
}

// normalizeRequestedReviewers fills PR.RequestedReviewers when Gitea sends top-level requested_reviewer.
func normalizeRequestedReviewers(evt *WebhookEvent) {
	if evt.PR == nil || len(evt.PR.RequestedReviewers) > 0 {
		return
	}
	if evt.RequestedReviewer == nil || evt.RequestedReviewer.Login == "" {
		return
	}
	evt.PR.RequestedReviewers = []User{*evt.RequestedReviewer}
	log.Printf("[DEBUG] Normalized requested reviewer from requested_reviewer: %s", evt.RequestedReviewer.Login)
}

// HasLabel checks if the issue has the given label.
func (evt *WebhookEvent) HasLabel(label string) bool {
	if evt.Issue == nil {
		return false
	}
	for _, l := range evt.Issue.Labels {
		if l.Name == label {
			return true
		}
	}
	return false
}

// HasAssignee checks if the issue is assigned to the given user.
func (evt *WebhookEvent) HasAssignee(username string) bool {
	if evt.Issue == nil {
		return false
	}
	for _, a := range evt.Issue.Assignees {
		if a.Login == username {
			return true
		}
	}
	return false
}

// HasMention checks if the comment body mentions the given username.
func (evt *WebhookEvent) HasMention(username string) bool {
	if evt.Comment == nil {
		return false
	}
	mention := "@" + username
	return len(evt.Comment.Body) > 0 && contains(evt.Comment.Body, mention)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
