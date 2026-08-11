package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jeeinn/matea/internal/deliver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapHubResultEmitsDeliver verifies that a hub backend returning a
// DeliverRequest actually fans the event out to the configured webhook
// (task 2.3.3) — not merely logs and drops it.
func TestMapHubResultEmitsDeliver(t *testing.T) {
	var mu sync.Mutex
	var got deliver.Event
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		hits++
		_ = json.Unmarshal(body, &got)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := &RunnerFactory{}
	f.SetDeliverClient(deliver.New(deliver.Config{WebhookURL: srv.URL}))

	backend := &fakeHubBackend{name: "hub-opencode"}
	res := &BackendResult{
		Summary: "review done",
		Deliver: &DeliverRequest{
			Event:   "task_completed",
			Channel: "feishu",
			Repo:    "o/r",
			PRID:    42,
			Action:  "comment",
			Content: "3 issues found",
		},
	}
	out := f.mapHubResult(backend, res)
	require.NotNil(t, out)
	assert.Equal(t, "review done", out.Content)
	assert.Equal(t, "comment", out.Action)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, hits, "webhook must be POSTed exactly once")
	assert.Equal(t, "task_completed", got.Event)
	assert.Equal(t, "feishu", got.Channel)
	assert.Equal(t, "o/r", got.Repo)
	assert.Equal(t, 42, got.PRID)
	assert.Equal(t, "3 issues found", got.Content)
}

// TestMapHubResultNoDeliverClient must not panic and must drop the request
// (logged) when no deliver client is configured.
func TestMapHubResultNoDeliverClient(t *testing.T) {
	f := &RunnerFactory{} // deliverClient nil
	backend := &fakeHubBackend{name: "hub-opencode"}
	res := &BackendResult{
		Summary: "ok",
		Deliver: &DeliverRequest{Event: "task_completed", Channel: "feishu", Content: "x"},
	}
	out := f.mapHubResult(backend, res)
	require.NotNil(t, out)
	assert.Equal(t, "ok", out.Content)
}
