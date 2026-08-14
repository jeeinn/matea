package agents

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/jeeinn/matea/internal/deliver"
	"github.com/jeeinn/matea/internal/store"
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
	out := f.mapHubResult(backend, res, &store.Task{Repo: "o/r", PRID: 42})
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
	out := f.mapHubResult(backend, res, nil)
	require.NotNil(t, out)
	assert.Equal(t, "ok", out.Content)
}

// TestMapHubResultSynthesizesDeliverWithoutRequest covers the 2.2.4 promise:
// channel-less hubs (OpenCode) never return a DeliverRequest, so mapHubResult
// synthesizes a task_completed event from the task + summary — otherwise a
// configured deliver.webhook_url would never fire for hub read/reply tasks
// (found by the Phase 2 E2E: opencode/hermes completions reached no sink).
func TestMapHubResultSynthesizesDeliverWithoutRequest(t *testing.T) {
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

	backend := &fakeHubBackend{name: "opencode-local"}
	res := &BackendResult{Summary: "analysis via opencode"} // no DeliverRequest
	out := f.mapHubResult(backend, res, &store.Task{Repo: "o/r", IssueID: 35})
	require.NotNil(t, out)
	assert.Equal(t, "analysis via opencode", out.Content)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, hits, "synthesized task_completed must be POSTed exactly once")
	assert.Equal(t, deliver.EventTaskCompleted, got.Event)
	assert.Equal(t, "o/r", got.Repo)
	assert.Equal(t, 35, got.IssueID)
	assert.Equal(t, "comment", got.Action)
	assert.Equal(t, "analysis via opencode", got.Content)
}

// TestEmitDeliverEventDisabledClientSilent: a configured-but-empty webhook_url
// (disabled client) must neither POST nor log the misleading "fanned out" line
// — and, with warnIfMissing=false, stay fully silent.
func TestEmitDeliverEventDisabledClientSilent(t *testing.T) {
	f := &RunnerFactory{}
	f.SetDeliverClient(deliver.New(deliver.Config{})) // empty URL = disabled
	require.NotPanics(t, func() {
		f.emitDeliverEvent("builtin", deliver.Event{Event: deliver.EventTaskCompleted}, false)
		f.emitDeliverEvent("hub", deliver.Event{Event: deliver.EventTaskCompleted}, true)
	})
}

// TestEmitBuiltinDeliver verifies the builtin (non-hub) runner path also fans
// a completion event out to the configured webhook (Phase 2 review S1). Before
// this, builtin tasks never triggered deliver, so a configured webhook_url
// looked broken for analyze/review/reply/write tasks.
func TestEmitBuiltinDeliver(t *testing.T) {
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

	f.emitBuiltinDeliver(&store.Task{Repo: "o/r", IssueID: 7, PRID: 12}, &Result{Content: "analysis summary", Action: "comment"})

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, hits, "webhook must be POSTed exactly once")
	assert.Equal(t, "task_completed", got.Event)
	assert.Equal(t, "o/r", got.Repo)
	assert.Equal(t, 7, got.IssueID)
	assert.Equal(t, 12, got.PRID)
	assert.Equal(t, "comment", got.Action)
	assert.Equal(t, "analysis summary", got.Content)
}

// TestEmitBuiltinDeliverNoClient must not panic and must stay silent (no
// webhook POST) when no deliver client is configured — builtin delivery is
// optional, unlike the hub path which warns on a missing subscriber.
func TestEmitBuiltinDeliverNoClient(t *testing.T) {
	f := &RunnerFactory{} // deliverClient nil
	require.NotPanics(t, func() {
		f.emitBuiltinDeliver(&store.Task{Repo: "o/r", IssueID: 1}, &Result{Content: "x", Action: "comment"})
	})
}
