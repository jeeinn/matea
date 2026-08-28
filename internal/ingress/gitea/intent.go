package gitea

// Trigger sources (task 1.3.2). Phase 1 implements only SourceGitea; the
// remaining values are reserved for Phase 2 ingress implementations (1.3.3 —
// no other trigger is built in Phase 1, but the Intent contract already
// carries what they will need).
const (
	// SourceGitea identifies triggers arriving via Gitea webhooks.
	SourceGitea = "gitea"
	// SourceMCP / SourceAPI / SourceCLI are reserved for Phase 2 triggers
	// (Matea as MCP server, management REST API, local CLI).
	SourceMCP = "mcp"
	SourceAPI = "api"
	SourceCLI = "cli"
)

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
	// Source identifies the trigger origin; always SourceGitea in Phase 1.
	Source string `json:"source"`

	// Channel routes deliver/IM events back to the originating channel
	// (reserved; populated by Phase 2 IM ingresses).
	Channel string `json:"channel,omitempty"`

	// ThreadID correlates multi-turn conversations across triggers
	// (reserved; populated by Phase 2 IM/session ingresses).
	ThreadID string `json:"thread_id,omitempty"`

	// Event is the parsed Gitea webhook payload (nil for non-Gitea sources).
	// It is intentionally excluded from JSON serialization: the payload is
	// consumed in-process by the dispatcher and should not be persisted or
	// forwarded as-is. If a future ingress (MCP/API/CLI) needs to round-trip
	// an Intent through logs/queues, the source-specific payload should be
	// re-attached from the delivery layer rather than serialized here.
	Event *WebhookEvent `json:"-"`
}

// ParseIntent parses a webhook delivery into the unified Intent. It shares
// ParseEvent's normalization (assignee / reviewer / action aliases), so live
// and replayed deliveries produce identical Intents.
func ParseIntent(eventType, deliveryID string, payload []byte) (*Intent, error) {
	evt, err := ParseEvent(eventType, deliveryID, payload)
	if err != nil {
		return nil, err
	}
	return &Intent{Source: SourceGitea, Event: evt}, nil
}

// WrapEvent wraps an already-parsed event into an Intent (tests and internal
// producers that hold a WebhookEvent rather than a raw payload).
func WrapEvent(evt *WebhookEvent) *Intent {
	return &Intent{Source: SourceGitea, Event: evt}
}
