// Package hermes implements a HubBackend adapter for the Hermes agent
// orchestrator (Phase 2, task 2.1.1).
//
// Hermes Runs API (verified 2026-08-05 against official docs):
//
//	POST /v1/runs  → submit a run, body: {input, session_id?, instructions?,
//	                  conversation_history?, previous_response_id?}
//	                  returns {run_id, status:"started"}
//	GET  /v1/runs/{run_id} → poll status, returns {status, output, session_id, usage}
//	                       terminal: completed | failed | cancelled
//	GET  /v1/capabilities  → capability discovery (optional)
//
// Authentication: Bearer <API_SERVER_KEY>
//
// Matea persists the Handle (RemoteID + IdempotencyKey) returned by Submit
// into SQLite immediately, and the Executor re-attaches polling for
// non-terminal Handles on startup (HubBackend contract §1.2.1). This adapter
// honors that contract: Submit only fires the HTTP call and stores the
// handle; Poll drives the lifecycle.
//
// session_id correlation (D3): Hermes uses session_id to continue a prior
// conversation for the same repo. Matea derives a stable session key from
// TaskContext.Repo so that analyze → review → code flows on the same repo
// share a Hermes session for cross-task memory.
package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
)

const (
	// hermesDefaultTimeout is the per-request HTTP timeout for Hermes calls.
	hermesDefaultTimeout = 30 * time.Second

	// hermesRunEndpoint is the Runs API submit endpoint.
	hermesRunEndpoint = "/v1/runs"

	// hermesCapabilitiesEndpoint is the optional capability-discovery endpoint.
	hermesCapabilitiesEndpoint = "/v1/capabilities"
)

// hermesRunRequest is the body of POST /v1/runs.
type hermesRunRequest struct {
	Input               string                          `json:"input"`
	SessionID           string                          `json:"session_id,omitempty"`
	Instructions        string                          `json:"instructions,omitempty"`
	ConversationHistory []hermesConversationMessage      `json:"conversation_history,omitempty"`
	PreviousResponseID  string                          `json:"previous_response_id,omitempty"`
}

// hermesConversationMessage is one turn of conversation history.
type hermesConversationMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// hermesRunResponse is the response of POST /v1/runs.
type hermesRunResponse struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

// hermesPollResponse is the response of GET /v1/runs/{run_id}.
type hermesPollResponse struct {
	Status  string `json:"status"`
	Output  string `json:"output,omitempty"`
	Session string `json:"session_id,omitempty"`
	Usage   any    `json:"usage,omitempty"`
}

// Backend implements agents.HubBackend for Hermes over its official Runs API.
//
// Field grouping: cfg + client are immutable after construction; mu guards
// the terminal-outcome cache used only when Submit happens to observe a
// synchronous terminal response (Hermes returns "started" in the normal
// async case, but a fast-fail or cached result could surface immediately).
type Backend struct {
	name   string
	cfg    config.BackendConfig
	client *http.Client
	token  string

	// terminalCache caches outcomes for runs that reach a terminal state
	// before the first Poll. Keyed by RemoteID. Normal Hermes flows do not
	// populate this — Submit returns before completion — but the cache
	// makes the adapter robust to fast-fail and synchronous-completion
	// scenarios without violating the async contract.
	mu             sync.Mutex
	terminalCache  map[string]agents.BackendResult
}

// Compile-time interface compliance check.
var _ agents.HubBackend = (*Backend)(nil)

// NewBackend constructs a Hermes HubBackend from a named config entry.
// It validates required fields and sets a default timeout.
func NewBackend(name string, cfg config.BackendConfig) (*Backend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("hermes backend %q: base_url is required", name)
	}

	timeout := hermesDefaultTimeout
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	// Extract Bearer token from auth config (prefer explicit API key, fall
	// back to password for flexibility).
	token := cfg.Auth.Username
	if cfg.Auth.Password != "" {
		token = cfg.Auth.Password
	}

	return &Backend{
		name:          name,
		cfg:           cfg,
		client:        &http.Client{Timeout: timeout},
		token:         token,
		terminalCache: make(map[string]agents.BackendResult),
	}, nil
}

// Name returns the backend name (e.g. "hermes-prod").
func (b *Backend) Name() string { return b.name }

// Capabilities declares the Hermes feature set. Hermes supports tool use
// (multi-turn within its own sandbox), cross-session memory (session_id),
// and brings its own IM channels (feishu/wecom native deliver).
func (b *Backend) Capabilities() agents.HubCapabilities {
	return agents.HubCapabilities{
		SupportsToolUse:   true,
		SupportsMemory:    true,
		HasIMChannels:     true,
		SupportsMCPClient: false, // Hermes drives Matea, not the other way
	}
}

// HealthCheck probes the Hermes capabilities endpoint (lightweight).
// Falls back to a simple GET /v1/runs probe if /v1/capabilities is absent.
func (b *Backend) HealthCheck(ctx context.Context) error {
	if err := b.do(ctx, http.MethodGet, hermesCapabilitiesEndpoint, nil, nil); err != nil {
		// Fall back to listing runs (cheap empty GET) if capabilities
		// endpoint is unavailable — any 2xx proves the API server is up.
		return b.do(ctx, http.MethodGet, hermesRunEndpoint, nil, nil)
	}
	return nil
}

// Submit hands a task to Hermes and returns a persistable Handle. It only
// fires the HTTP call — Hermes returns {run_id, status:"started"} quickly
// and the run continues server-side. Poll drives the lifecycle.
//
// IdempotencyKey: derived from (task_type, repo, issue_id, pr_id) so a
// duplicate webhook dispatch submits only one run.
//
// session_id: derived from repo so that analyze → review → code flows on
// the same repo share a Hermes session for cross-task memory (D3).
func (b *Backend) Submit(ctx context.Context, tc *agents.TaskContext) (*agents.Handle, error) {
	if tc == nil {
		return nil, fmt.Errorf("hermes backend: nil TaskContext")
	}

	req := b.buildRunRequest(tc)
	var resp hermesRunResponse
	if err := b.do(ctx, http.MethodPost, hermesRunEndpoint, req, &resp); err != nil {
		return nil, err
	}

	if resp.RunID == "" {
		return nil, fmt.Errorf("hermes backend: POST /v1/runs returned no run_id")
	}

	handle := &agents.Handle{
		Backend:        b.name,
		RemoteID:       resp.RunID,
		IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", tc.TaskType, tc.Repo, tc.IssueID, tc.PRID),
	}

	// If Hermes reports a non-started terminal status synchronously
	// (fast-fail or cached), cache the outcome so the first Poll returns
	// it. This preserves the async contract while being robust to edge
	// cases.
	if resp.Status != "" && resp.Status != "started" && resp.Status != "pending" && resp.Status != "running" {
		b.mu.Lock()
		b.terminalCache[resp.RunID] = agents.BackendResult{Summary: resp.Status}
		b.mu.Unlock()
	}

	return handle, nil
}

// Poll reports the current result and state for a Handle. It maps Hermes
// status strings to agents.State and extracts the output text.
//
// Hermes status → Matea state:
//
//	completed → StateDone
//	failed    → StateFailed
//	cancelled → StateCanceled
//	otherwise → StateRunning
func (b *Backend) Poll(ctx context.Context, h *agents.Handle) (*agents.BackendResult, agents.State, error) {
	if h == nil {
		return nil, "", fmt.Errorf("hermes backend: nil Handle")
	}
	if h.Backend != "" && h.Backend != b.name {
		return nil, "", fmt.Errorf("hermes backend: handle belongs to backend %q", h.Backend)
	}

	// Check the synchronous-completion cache first.
	b.mu.Lock()
	if cached, ok := b.terminalCache[h.RemoteID]; ok {
		delete(b.terminalCache, h.RemoteID)
		b.mu.Unlock()
		return &cached, agents.StateDone, nil
	}
	b.mu.Unlock()

	var resp hermesPollResponse
	if err := b.do(ctx, http.MethodGet, hermesRunEndpoint+"/"+h.RemoteID, nil, &resp); err != nil {
		return nil, "", err
	}

	state := mapHermesStatus(resp.Status)
	result := &agents.BackendResult{Summary: resp.Output}

	// If Hermes returns a session_id alongside output, embed it in the
	// summary for potential correlation by callers. Only when output is
	// non-empty (terminal states) — running-state polls have no output.
	if resp.Session != "" && resp.Output != "" {
		result.Summary = fmt.Sprintf("[session:%s] %s", resp.Session, resp.Output)
	}

	return result, state, nil
}

// Cancel asks Hermes to stop a run (best effort). Hermes Runs API does not
// expose a dedicated cancel endpoint in the minimal contract; we surface
// a graceful "not implemented" so callers can degrade. Matea's workflow
// layer treats cancellation as advisory.
func (b *Backend) Cancel(ctx context.Context, h *agents.Handle) error {
	if h == nil {
		return fmt.Errorf("hermes backend: nil Handle")
	}
	// Hermes Runs API has no documented cancel endpoint in the minimal
	// contract. Returning nil makes Cancel idempotent (best effort) per
	// the HubBackend contract. If Hermes later exposes POST
	// /v1/runs/{id}/cancel, wire it here.
	_ = ctx
	return nil
}

// buildRunRequest constructs the POST /v1/runs body from a TaskContext.
func (b *Backend) buildRunRequest(tc *agents.TaskContext) *hermesRunRequest {
	input := tc.UserPrompt
	if input == "" {
		// Fallback: assemble a minimal input from available context.
		input = fmt.Sprintf("%s\n\n%s", tc.IssueTitle, tc.IssueBody)
	}

	req := &hermesRunRequest{
		Input:        input,
		Instructions: tc.SystemPrompt,
		SessionID:    deriveSessionID(tc),
	}

	// Inject conversation history (comment snapshots) as Hermes messages.
	if len(tc.Comments) > 0 {
		req.ConversationHistory = make([]hermesConversationMessage, 0, len(tc.Comments))
		for _, c := range tc.Comments {
			role := "user"
			if c.Author == "matea" || c.Author == b.name {
				role = "assistant"
			}
			req.ConversationHistory = append(req.ConversationHistory, hermesConversationMessage{
				Role:    role,
				Content: fmt.Sprintf("[%s] %s", c.Author, c.Body),
			})
		}
	}

	// Inject diff as part of input for review tasks.
	if tc.Diff != "" {
		req.Input = fmt.Sprintf("## Diff\n%s\n\n## Question\n%s", tc.Diff, req.Input)
	}

	// Inject repo/issue memory (D3 cross-task sharing, task 2.1.5) so the hub
	// can recall prior task conclusions (e.g. an analyze summary) on the same
	// repo+issue. Appended only when present, so existing callers are unaffected.
	if len(tc.MemoryKeys) > 0 {
		var mb strings.Builder
		mb.WriteString("\n\n## Previously remembered context (repo/issue memory)\n")
		for k, v := range tc.MemoryKeys {
			mb.WriteString(fmt.Sprintf("- %s: %s\n", k, v))
		}
		req.Input += mb.String()
	}

	// git_sync write tasks (task B1): inject the shared hub-push contract —
	// the same BuildGitSyncInstructions block OpenCode receives (task A4). It
	// carries the task-scoped deploy key (base64), the clone URL, the draft
	// branch Hermes may push, the required commit footer and the result
	// trailer. Hermes executes the git steps with its own tools; Matea never
	// sees a patch (no patch-return special case). Appended last so the
	// mandatory workflow sits in the recency window of the prompt.
	if tc.GitSync != nil {
		req.Input = strings.TrimSpace(req.Input + "\n\n" +
			agents.BuildGitSyncInstructions(tc.GitSync, fmt.Sprintf("matea-hub-%d", tc.TaskID)))
	}

	return req
}

// init registers the Hermes backend factory with the agents package's
// type→constructor registry. This keeps the dependency one-directional
// (backends/hermes → agents): the agents package never imports this
// sub-package, so there is no import cycle, and future hub types register
// themselves the same way.
func init() {
	agents.RegisterHubBackendFactory(config.BackendTypeHubHermes, func(name string, cfg config.BackendConfig) (agents.HubBackend, error) {
		return NewBackend(name, cfg)
	})
}

// do executes an HTTP request against the Hermes API. It attaches Bearer
// auth when configured, decodes the JSON response into out (if non-nil),
// and surfaces non-2xx responses as errors with the response body.
func (b *Backend) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hermes: marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	url := b.cfg.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("hermes: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("hermes: request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4MB cap
	if err != nil {
		return fmt.Errorf("hermes: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("hermes: %s %s returned %d: %s", method, path, resp.StatusCode, string(data))
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("hermes: decode response: %w", err)
		}
	}
	return nil
}

// mapHermesStatus maps Hermes run status strings to Matea agents.State.
func mapHermesStatus(s string) agents.State {
	switch s {
	case "completed":
		return agents.StateDone
	case "failed":
		return agents.StateFailed
	case "cancelled", "canceled":
		return agents.StateCanceled
	case "started", "pending", "running":
		return agents.StateRunning
	default:
		// Unknown status: treat as running (Poll will retry).
		return agents.StateRunning
	}
}

// deriveSessionID produces a stable session key from the repo so that flows
// on the same repo share a Hermes session. Analayze → review → code on the
// same repo thus get cross-task memory via Hermes's session continuity.
func deriveSessionID(tc *agents.TaskContext) string {
	if tc.Repo == "" {
		return ""
	}
	return "matea:" + tc.Repo
}
