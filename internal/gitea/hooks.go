package gitea

import (
	"encoding/json"
	"fmt"
)

// Webhook represents a Gitea system (admin) webhook.
type Webhook struct {
	ID     int64         `json:"id"`
	Type   string        `json:"type"`
	Config WebhookConfig `json:"config"`
	Events []string      `json:"events"`
	Active bool          `json:"active"`
}

// WebhookConfig holds the transport configuration of a webhook.
type WebhookConfig struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret"`
}

// CreateWebhookRequest is the payload for creating an admin webhook.
type CreateWebhookRequest struct {
	Type   string        `json:"type"`
	Config WebhookConfig `json:"config"`
	Events []string      `json:"events"`
	Active bool          `json:"active"`
}

// defaultWebhookEvents is the event set Matea reacts to on the Issue/PR
// lifecycle. Site-level webhooks deliver these to /webhook/gitea.
var defaultWebhookEvents = []string{
	"issues",
	"issue_comment",
	"pull_request",
	"pull_request_comment",
}

// ListAdminWebhooks returns all system-level webhooks.
func (c *Client) ListAdminWebhooks() ([]Webhook, error) {
	body, err := c.do("GET", "/admin/hooks", nil)
	if err != nil {
		return nil, fmt.Errorf("list admin webhooks: %w", err)
	}
	var hooks []Webhook
	if err := json.Unmarshal(body, &hooks); err != nil {
		return nil, fmt.Errorf("unmarshal webhooks: %w", err)
	}
	return hooks, nil
}

// CreateAdminWebhook creates a system-level webhook.
func (c *Client) CreateAdminWebhook(req CreateWebhookRequest) (*Webhook, error) {
	body, err := c.do("POST", "/admin/hooks", req)
	if err != nil {
		return nil, fmt.Errorf("create admin webhook: %w", err)
	}
	var wh Webhook
	if err := json.Unmarshal(body, &wh); err != nil {
		return nil, fmt.Errorf("unmarshal webhook: %w", err)
	}
	return &wh, nil
}

// EnsureWebhook ensures a system webhook pointing at callbackURL exists.
// It lists existing admin webhooks and returns early (registered=true,
// created=false) if one already targets the same URL; otherwise it creates a
// new active webhook. The default event set covers the Issue/PR lifecycle
// Matea reacts to. A nil/empty events slice selects the default set.
//
// Returns (registered, created, hookID, err).
func (c *Client) EnsureWebhook(callbackURL, secret string, events []string) (registered bool, created bool, hookID int64, err error) {
	if callbackURL == "" {
		return false, false, 0, fmt.Errorf("callbackURL is required")
	}
	if len(events) == 0 {
		events = defaultWebhookEvents
	}
	hooks, listErr := c.ListAdminWebhooks()
	if listErr != nil {
		return false, false, 0, listErr
	}
	for _, h := range hooks {
		if h.Config.URL == callbackURL {
			return true, false, h.ID, nil
		}
	}
	wh, createErr := c.CreateAdminWebhook(CreateWebhookRequest{
		Type:   "gitea",
		Config: WebhookConfig{URL: callbackURL, ContentType: "json", Secret: secret},
		Events: events,
		Active: true,
	})
	if createErr != nil {
		return false, false, 0, createErr
	}
	return true, true, wh.ID, nil
}
