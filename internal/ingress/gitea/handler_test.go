package gitea

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDedupTryAcceptAndReplay(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	d := NewDeduplicator(db.DB)
	payload := []byte(`{"action":"assigned"}`)

	ok, err := d.TryAccept("d1", "issues", payload)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, d.IsProcessed("d1"))

	ok, err = d.TryAccept("d1", "issues", payload)
	require.NoError(t, err)
	assert.False(t, ok, "duplicate accept must fail")

	pending, err := d.ListAccepted()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "d1", pending[0].DeliveryID)
	assert.Equal(t, "issues", pending[0].EventType)

	d.MarkProcessed("d1")
	pending, err = d.ListAccepted()
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.True(t, d.IsProcessed("d1"))
}

func TestHandlerAcceptsBeforeAsyncProcess(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	var called atomic.Int32
	h := NewHandler(&config.GiteaConfig{}, db.DB, func(evt *WebhookEvent) bool {
		called.Add(1)
		assert.Equal(t, "issues", evt.Event)
		return true
	})

	payload := []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "r", "full_name": "o/r", "owner": {"id": 1, "login": "o"}},
		"issue": {"id": 1, "number": 1, "title": "t", "body": "b", "state": "open", "user": {"id": 1, "login": "o"}, "assignees": [], "labels": []},
		"sender": {"id": 1, "login": "o"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "issues")
	req.Header.Set("X-Gitea-Delivery", "delivery-inbox-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), "accepted")

	// Inbox row must exist immediately (before callback finishes).
	dedup := NewDeduplicator(db.DB)
	assert.True(t, dedup.IsProcessed("delivery-inbox-1"))

	require.Eventually(t, func() bool {
		return called.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)

	pending, err := dedup.ListAccepted()
	require.NoError(t, err)
	assert.Empty(t, pending, "should be marked processed after callback")
}

func TestHandlerLeavesAcceptedOnCallbackFalse(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()

	var called atomic.Int32
	h := NewHandler(&config.GiteaConfig{}, db.DB, func(evt *WebhookEvent) bool {
		called.Add(1)
		return false // transient failure — must stay accepted for ReplayAccepted
	})

	payload := []byte(`{
		"action": "assigned",
		"repository": {"id": 1, "name": "r", "full_name": "o/r", "owner": {"id": 1, "login": "o"}},
		"issue": {"id": 1, "number": 1, "title": "t", "body": "b", "state": "open", "user": {"id": 1, "login": "o"}, "assignees": [], "labels": []},
		"sender": {"id": 1, "login": "o"}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook/gitea", bytes.NewReader(payload))
	req.Header.Set("X-Gitea-Event", "issues")
	req.Header.Set("X-Gitea-Delivery", "delivery-fail-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	require.Eventually(t, func() bool {
		return called.Load() == 1
	}, 2*time.Second, 10*time.Millisecond)

	dedup := NewDeduplicator(db.DB)
	pending, err := dedup.ListAccepted()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "delivery-fail-1", pending[0].DeliveryID)

	// Replay should invoke callback again.
	h.ReplayAccepted()
	require.Eventually(t, func() bool {
		return called.Load() >= 2
	}, 2*time.Second, 10*time.Millisecond)
}
