package gitea

// Intent is the unified trigger structure emitted by the Gitea ingress
// (task 1.3.1). It is the single output type of the ingress layer: the HTTP
// handler produces it from webhook deliveries (both the live path and
// ReplayAccepted crash recovery), and the dispatcher consumes it.
//
// Phase 1 carries the parsed Gitea payload directly; downstream layers
// (dispatcher → workflow resolver) decompose via Intent.Event. Trigger
// sources beyond Gitea (MCP / API / CLI) are a Phase 2 concern (1.3.3) —
// the routing fields they need are reserved on this struct (1.3.2).
type Intent struct {
	// Event is the parsed Gitea webhook payload.
	Event *WebhookEvent
}

// ParseIntent parses a webhook delivery into the unified Intent. It shares
// ParseEvent's normalization (assignee / reviewer / action aliases), so live
// and replayed deliveries produce identical Intents.
func ParseIntent(eventType, deliveryID string, payload []byte) (*Intent, error) {
	evt, err := ParseEvent(eventType, deliveryID, payload)
	if err != nil {
		return nil, err
	}
	return &Intent{Event: evt}, nil
}

// WrapEvent wraps an already-parsed event into an Intent (tests and internal
// producers that hold a WebhookEvent rather than a raw payload).
func WrapEvent(evt *WebhookEvent) *Intent {
	return &Intent{Event: evt}
}
