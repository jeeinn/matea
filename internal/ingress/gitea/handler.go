package gitea

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/jeeinn/matea/internal/config"
)

// EventCallback is called when a webhook event is parsed and matched.
// It receives the parsed event for further processing (e.g., enqueue to dispatcher).
//
// Return semantics (must stay aligned with dispatcher.HandleEvent):
//   - true  — terminal outcome (enqueued, intentionally skipped, gate reject, etc.);
//     the delivery is marked processed and will not be replayed.
//   - false — transient failure (DB/enqueue errors); leave status=accepted so
//     ReplayAccepted can retry after restart.
type EventCallback func(evt *WebhookEvent) bool

// Handler processes incoming Gitea webhook requests.
type Handler struct {
	mu       sync.RWMutex
	cfg      *config.GiteaConfig
	dedup    *Deduplicator
	callback EventCallback
}

// NewHandler creates a new webhook Handler.
func NewHandler(cfg *config.GiteaConfig, db *sql.DB, callback EventCallback) *Handler {
	return &Handler{
		cfg:      cfg,
		dedup:    NewDeduplicator(db),
		callback: callback,
	}
}

// SetGiteaConfig updates Gitea settings used for signature verification (hot reload).
func (h *Handler) SetGiteaConfig(cfg *config.GiteaConfig) {
	if cfg == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg = cfg
}

func (h *Handler) giteaConfig() *config.GiteaConfig {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

// ReplayAccepted re-dispatches deliveries left in accepted state after a crash.
func (h *Handler) ReplayAccepted() {
	items, err := h.dedup.ListAccepted()
	if err != nil {
		log.Printf("[WARN] Failed to list accepted webhook deliveries: %v", err)
		return
	}
	if len(items) == 0 {
		return
	}
	log.Printf("[INFO] Replaying %d accepted webhook delivery(ies)", len(items))
	for _, item := range items {
		item := item
		go h.processAccepted(item.DeliveryID, item.EventType, item.Payload)
	}
}

// ServeHTTP handles the webhook POST request from Gitea.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := ReadBody(r)
	if err != nil {
		log.Printf("[ERROR] Failed to read request body: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	cfg := h.giteaConfig()
	if cfg != nil && cfg.WebhookSecret != "" {
		if !VerifySignature(r, cfg.WebhookSecret, body) {
			log.Printf("[WARN] Invalid webhook signature from %s", r.RemoteAddr)
			http.Error(w, "Invalid signature", http.StatusUnauthorized)
			return
		}
	}

	eventType := r.Header.Get("X-Gitea-Event")
	deliveryID := r.Header.Get("X-Gitea-Delivery")

	if eventType == "" {
		http.Error(w, "Missing X-Gitea-Event header", http.StatusBadRequest)
		return
	}

	if h.dedup.IsProcessed(deliveryID) {
		log.Printf("[INFO] Duplicate delivery %s, skipping", deliveryID)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"duplicate"}`)
		return
	}

	// Validate payload before accepting into the inbox.
	if _, err := ParseEvent(eventType, deliveryID, body); err != nil {
		log.Printf("[ERROR] Failed to parse event %s: %v", eventType, err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Inbox: persist accepted delivery BEFORE returning 200 so a crash after ACK
	// can be recovered via ReplayAccepted on startup.
	accepted, err := h.dedup.TryAccept(deliveryID, eventType, body)
	if err != nil {
		log.Printf("[ERROR] Failed to accept delivery %s: %v", deliveryID, err)
		http.Error(w, "Failed to accept delivery", http.StatusInternalServerError)
		return
	}
	if !accepted {
		log.Printf("[INFO] Duplicate delivery %s on accept, skipping", deliveryID)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"duplicate"}`)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})

	go h.processAccepted(deliveryID, eventType, body)
}

func (h *Handler) processAccepted(deliveryID, eventType string, body []byte) {
	evt, err := ParseEvent(eventType, deliveryID, body)
	if err != nil {
		// Payload was validated before accept; re-parse failure is unrecoverable.
		log.Printf("[ERROR] Failed to re-parse accepted delivery %s: %v", deliveryID, err)
		h.dedup.MarkProcessed(deliveryID)
		return
	}
	if h.callback == nil {
		h.dedup.MarkProcessed(deliveryID)
		return
	}
	if h.callback(evt) {
		h.dedup.MarkProcessed(deliveryID)
		return
	}
	// Keep status=accepted so ReplayAccepted can retry after restart.
	log.Printf("[WARN] Event processing returned false for delivery %s; leaving accepted for replay", deliveryID)
}
