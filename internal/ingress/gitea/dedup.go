package gitea

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
)

// Delivery statuses for the webhook inbox (processed_deliveries).
const (
	DeliveryStatusAccepted  = "accepted"
	DeliveryStatusProcessed = "processed"
)

// AcceptedDelivery is a webhook payload persisted before async processing.
type AcceptedDelivery struct {
	DeliveryID string
	EventType  string
	Payload    []byte
}

// Deduplicator checks and records webhook delivery IDs to prevent duplicate processing.
type Deduplicator struct {
	db *sql.DB
}

// NewDeduplicator creates a new Deduplicator backed by SQLite.
func NewDeduplicator(db *sql.DB) *Deduplicator {
	return &Deduplicator{db: db}
}

// IsProcessed returns true if the delivery ID has already been accepted or processed.
func (d *Deduplicator) IsProcessed(deliveryID string) bool {
	if deliveryID == "" {
		return false
	}
	var count int
	err := d.db.QueryRow("SELECT COUNT(*) FROM processed_deliveries WHERE delivery_id = ?", deliveryID).Scan(&count)
	if err != nil {
		log.Printf("[WARN] Dedup check failed: %v", err)
		return false
	}
	return count > 0
}

// TryAccept records the delivery as accepted with its payload (inbox).
// Returns accepted=false when the delivery_id already exists (duplicate).
// Empty deliveryID always succeeds without persisting (no inbox / no dedup).
func (d *Deduplicator) TryAccept(deliveryID, eventType string, payload []byte) (accepted bool, err error) {
	if deliveryID == "" {
		return true, nil
	}
	res, err := d.db.Exec(
		`INSERT OR IGNORE INTO processed_deliveries (delivery_id, status, event_type, payload) VALUES (?, ?, ?, ?)`,
		deliveryID, DeliveryStatusAccepted, eventType, payload,
	)
	if err != nil {
		return false, fmt.Errorf("accept delivery: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("accept delivery rows: %w", err)
	}
	return n > 0, nil
}

// MarkProcessed records the delivery ID as fully processed.
func (d *Deduplicator) MarkProcessed(deliveryID string) {
	if deliveryID == "" {
		return
	}
	_, err := d.db.Exec(
		`UPDATE processed_deliveries SET status = ? WHERE delivery_id = ?`,
		DeliveryStatusProcessed, deliveryID,
	)
	if err != nil {
		_, err2 := d.db.Exec(`INSERT OR IGNORE INTO processed_deliveries (delivery_id) VALUES (?)`, deliveryID)
		if err2 != nil {
			log.Printf("[WARN] Failed to mark delivery as processed: %v (fallback: %v)", err, err2)
		}
		return
	}
}

// ListAccepted returns deliveries that were accepted but not yet marked processed
// (e.g. process crash after HTTP 200).
func (d *Deduplicator) ListAccepted() ([]AcceptedDelivery, error) {
	rows, err := d.db.Query(
		`SELECT delivery_id, event_type, payload FROM processed_deliveries WHERE status = ? ORDER BY created_at ASC`,
		DeliveryStatusAccepted,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AcceptedDelivery
	for rows.Next() {
		var item AcceptedDelivery
		var payload []byte
		if err := rows.Scan(&item.DeliveryID, &item.EventType, &payload); err != nil {
			return out, err
		}
		item.Payload = payload
		out = append(out, item)
	}
	return out, rows.Err()
}

// Cleanup removes delivery records older than the given number of days.
func (d *Deduplicator) Cleanup(days int) {
	_, err := d.db.Exec(
		`DELETE FROM processed_deliveries WHERE created_at < datetime('now', ?)`,
		"-"+strconv.Itoa(days)+" days",
	)
	if err != nil {
		log.Printf("[WARN] Dedup cleanup failed: %v", err)
	}
}
