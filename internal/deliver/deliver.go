// Package deliver implements Matea's outbound event fan-out (task 2.3.3).
//
// It is a one-way, outbound-only channel: Matea POSTs a standardized Event to
// a single configured webhook_url. There is deliberately no inbound receiving
// module — Hermes Poll / OpenCode sync never push completion events back;
// Matea is the one that initiates the delivery. The "fan-out" intent lives in
// the Event shape (channel / thread_id routing): one internal event can be
// broadcast to multiple external surfaces by pointing webhook_url at a bridge
// that re-distributes.
//
// Deliver is best-effort. A failed POST is logged and surfaced as an error but
// never blocks or fails the task that produced the event — a notification that
// cannot be sent must not take down the analysis/review result it accompanies.
package deliver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// Config holds the outbound deliver configuration.
type Config struct {
	// WebhookURL is the single destination for outbound events. Empty means
	// deliver is disabled (no-op) — an OpenCode result then only lands on
	// Gitea, with no IM notification (the 2.2.4 / 2.3.4 gap).
	WebhookURL string `yaml:"webhook_url"`
	// Timeout bounds a single POST attempt.
	Timeout time.Duration `yaml:"timeout"`
	// MaxRetries is the number of additional attempts after the first failure
	// (network error or 5xx). Best-effort only; 0 means a single attempt.
	MaxRetries int `yaml:"max_retries"`
}

// Event is the standardized outbound payload Matea fans out to external
// channels (IM bridges / hub receivers). The shape mirrors the hub backend's
// DeliverRequest so the two definitions never drift.
type Event struct {
	Event    string `json:"event"`
	Channel  string `json:"channel"`
	ThreadID string `json:"thread_id,omitempty"`
	Repo     string `json:"repo,omitempty"`
	IssueID  int    `json:"issue_id,omitempty"`
	PRID     int    `json:"pr_id,omitempty"`
	Action   string `json:"action"`
	Content  string `json:"content"`
}

// Event types fanned out by Matea. task_completed is emitted by runners when a
// task finishes; pr_merged is emitted by the dispatcher when a PR is merged —
// the merge webhook never reaches a runner, so it is handled at the
// lifecycle/dispatcher layer (task 2.4.3).
const (
	EventTaskCompleted = "task_completed"
	EventPrMerged      = "pr_merged"
)

// Client emits Events to the configured webhook. A Client built from a Config
// with an empty WebhookURL is disabled and its Emit is a no-op.
type Client struct {
	cfg  Config
	http *http.Client
}

// DefaultTimeout is used when Config.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// New builds a Client. A zero Config yields a disabled client (no-op Emit).
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
	}
}

// Enabled reports whether the client will actually POST events (non-empty
// webhook_url). A nil or zero-config Client is disabled; Emit on it is a no-op.
func (c *Client) Enabled() bool {
	return c != nil && c.cfg.WebhookURL != ""
}

// Emit POSTs the event as JSON to the configured webhook. It is safe to call
// on a disabled client (returns nil without sending). On failure it retries up
// to cfg.MaxRetries times with a short backoff, then returns the last error.
// Only transport errors and 5xx are retried; a client (4xx) error stops
// immediately because a malformed payload or auth failure will not fix itself.
func (c *Client) Emit(ctx context.Context, e Event) error {
	if c == nil || c.cfg.WebhookURL == "" {
		return nil // deliver disabled
	}
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal deliver event: %w", err)
	}
	attempts := 1 + c.cfg.MaxRetries
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
		retryable, err := c.post(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		log.Printf("[WARN] deliver emit attempt %d/%d to %s failed: %v",
			attempt+1, attempts, c.cfg.WebhookURL, err)
		if !retryable {
			// Client (4xx) error: a malformed payload or auth failure will
			// not fix itself on retry — stop immediately.
			return err
		}
	}
	return fmt.Errorf("deliver webhook %s failed after %d attempt(s): %w",
		c.cfg.WebhookURL, attempts, lastErr)
}

// post performs a single POST. It returns (retryable, err): transport errors
// and 5xx are retryable; 4xx is not.
func (c *Client) post(ctx context.Context, body []byte) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("build deliver request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return true, err // transport error — transient, retryable
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// Client error: bad payload or auth. Retrying will not help.
		return false, fmt.Errorf("deliver webhook rejected event (status %d)", resp.StatusCode)
	}
	// 5xx (or other): treat as transient.
	return true, fmt.Errorf("deliver webhook returned status %d", resp.StatusCode)
}
