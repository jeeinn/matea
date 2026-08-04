package gitea

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseIntentProducesUnifiedOutput verifies the ingress output contract
// (task 1.3.1): raw deliveries become Intent, sharing ParseEvent's
// normalizations so live and replayed paths are identical.
func TestParseIntentProducesUnifiedOutput(t *testing.T) {
	payload := []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "r", "full_name": "o/r", "owner": {"id": 1, "login": "o"}},
		"issue": {"id": 1, "number": 7, "title": "t", "body": "b", "state": "open",
			"user": {"id": 1, "login": "o"}, "assignees": [{"id": 2, "login": "matea-coder"}], "labels": []},
		"sender": {"id": 1, "login": "o"}
	}`)

	intent, err := ParseIntent("issue_assign", "delivery-1", payload)
	require.NoError(t, err)
	require.NotNil(t, intent)
	require.NotNil(t, intent.Event)

	// ParseEvent normalizations flow through: event alias + assignee fill.
	assert.Equal(t, "issues", intent.Event.Event)
	assert.Equal(t, "delivery-1", intent.Event.DeliveryID)
	require.NotNil(t, intent.Event.Assignee)
	assert.Equal(t, "matea-coder", intent.Event.Assignee.Login)

	// Invalid payloads are rejected at the boundary.
	_, err = ParseIntent("issues", "delivery-2", []byte(`{not-json`))
	require.Error(t, err)
}

// TestWrapEvent verifies the internal-producer helper keeps the event by
// reference (no copy), so dispatcher-side mutations stay visible.
func TestWrapEvent(t *testing.T) {
	evt := &WebhookEvent{Event: "issues", Action: "assigned", DeliveryID: "d-9"}
	intent := WrapEvent(evt)
	require.NotNil(t, intent)
	assert.Same(t, evt, intent.Event)
}

// TestIntentSourceAndReservedFields verifies the 1.3.2 contract: every
// ingress-produced Intent is stamped SourceGitea, and the Phase 2 routing
// fields serialize only when populated.
func TestIntentSourceAndReservedFields(t *testing.T) {
	payload := []byte(`{
		"action": "created",
		"repository": {"id": 1, "name": "r", "full_name": "o/r", "owner": {"id": 1, "login": "o"}},
		"issue": {"id": 1, "number": 1, "title": "t", "body": "b", "state": "open", "user": {"id": 1, "login": "o"}, "assignees": [], "labels": []},
		"comment": {"id": 5, "body": "@matea-coder /dev", "user": {"id": 1, "login": "o"}},
		"sender": {"id": 1, "login": "o"}
	}`)

	intent, err := ParseIntent("issue_comment", "delivery-src", payload)
	require.NoError(t, err)
	assert.Equal(t, SourceGitea, intent.Source)
	assert.Empty(t, intent.Channel)
	assert.Empty(t, intent.ThreadID)

	// WrapEvent stamps the source too.
	assert.Equal(t, SourceGitea, WrapEvent(&WebhookEvent{}).Source)

	// JSON: source always present; empty routing fields omitted; the parsed
	// payload itself never serializes (Gitea-specific).
	data, err := json.Marshal(intent)
	require.NoError(t, err)
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "gitea", raw["source"])
	_, hasChannel := raw["channel"]
	_, hasThread := raw["thread_id"]
	_, hasEvent := raw["Event"]
	assert.False(t, hasChannel, "empty channel should be omitted")
	assert.False(t, hasThread, "empty thread_id should be omitted")
	assert.False(t, hasEvent, "webhook payload must not serialize into Intent JSON")

	// Populated routing fields round-trip (Phase 2 producers).
	data, err = json.Marshal(&Intent{Source: SourceMCP, Channel: "webhook", ThreadID: "th-1"})
	require.NoError(t, err)
	var back Intent
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, SourceMCP, back.Source)
	assert.Equal(t, "webhook", back.Channel)
	assert.Equal(t, "th-1", back.ThreadID)
}
