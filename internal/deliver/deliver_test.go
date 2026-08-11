package deliver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmitPostsJSON verifies the happy path: the event is POSTed as JSON with
// the correct content type and body.
func TestEmitPostsJSON(t *testing.T) {
	var mu sync.Mutex
	var got Event
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		ct = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		_ = json.Unmarshal(body, &got)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{WebhookURL: srv.URL})
	e := Event{Event: "task_completed", Channel: "feishu", Repo: "o/r", IssueID: 12, Action: "comment", Content: "done"}
	require.NoError(t, c.Emit(context.Background(), e))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "application/json", ct)
	assert.Equal(t, e, got)
}

// TestEmitDisabledNoRequest verifies a disabled client (empty URL) and a nil
// client both no-op without contacting any server.
func TestEmitDisabledNoRequest(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	// empty webhook_url → disabled
	c := New(Config{})
	require.NoError(t, c.Emit(context.Background(), Event{Event: "x"}))
	assert.False(t, hit, "disabled client must not POST")

	// nil client → safe no-op
	var nilClient *Client
	require.NoError(t, nilClient.Emit(context.Background(), Event{Event: "x"}))
	assert.False(t, hit, "nil client must not POST")
}

// TestEmitRetriesOn5xx verifies a transient 5xx is retried and then succeeds.
func TestEmitRetriesOn5xx(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		if count < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(Config{WebhookURL: srv.URL, MaxRetries: 2, Timeout: time.Second})
	require.NoError(t, c.Emit(context.Background(), Event{Event: "task_completed"}))
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, count, "should retry once then succeed")
}

// TestEmitGivesUpAfterMaxRetries verifies the client stops after the initial
// attempt plus MaxRetries and surfaces the error.
func TestEmitGivesUpAfterMaxRetries(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(Config{WebhookURL: srv.URL, MaxRetries: 1, Timeout: time.Second})
	err := c.Emit(context.Background(), Event{Event: "task_completed"})
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, count, "initial attempt + 1 retry")
}

// TestEmitNoRetryOn4xx verifies a client (4xx) error is not retried — a
// malformed payload will not fix itself.
func TestEmitNoRetryOn4xx(t *testing.T) {
	var mu sync.Mutex
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := New(Config{WebhookURL: srv.URL, MaxRetries: 3, Timeout: time.Second})
	err := c.Emit(context.Background(), Event{Event: "task_completed"})
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, count, "4xx must not be retried")
}
