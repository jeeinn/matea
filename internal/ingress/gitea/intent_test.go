package gitea

import (
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
