package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/store"
)

const (
	opencodeWorkspaceModeGatewayPath = "matea_path"
	opencodeDefaultTimeout           = 45 * time.Minute
)

// OpenCodeHTTPBackend implements CodingBackend by calling a local
// `opencode serve` sidecar over HTTP (Path A).
//
// API reference (from opencode-sdk-js):
//
//	POST /session                           → create session, returns {id,...}
//	POST /session/{id}/message              → send a message (modelID, providerID, parts, system, tools)
//	POST /session/{id}/abort                → abort an in-progress message
//	GET  /session/{id}/message              → list messages (info + parts[])
//
// The workspace directory is the same one prepared by prepareWriteWorkspace;
// the OpenCode sidecar must have access to the same filesystem (same machine).
// This matches the first-release constraint: only local sidecar, no remote.
type OpenCodeHTTPBackend struct {
	cfg    config.BackendConfig
	client *http.Client
	name   string // backend name from config, e.g. "opencode-local"

	// hubResults caches terminal outcomes of HubBackend.Submit, keyed by the
	// opencode session id. Instance-local: shared registry instances (1.2.4)
	// keep Submit→Poll affinity; the fresh-per-call CodingBackend path never
	// touches this cache.
	hubMu      sync.Mutex
	hubResults map[string]hubOutcome
}

// hubOutcome is the cached terminal result of a submitted hub task.
type hubOutcome struct {
	result *BackendResult
	err    error
}

// NewOpenCodeHTTPBackend builds an OpenCode HTTP backend from a named config entry.
// It validates required fields and sets a default timeout.
func NewOpenCodeHTTPBackend(name string, cfg config.BackendConfig) (*OpenCodeHTTPBackend, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("opencode backend %q: base_url is required", name)
	}
	if cfg.WorkspaceMode != "" && cfg.WorkspaceMode != opencodeWorkspaceModeGatewayPath {
		return nil, fmt.Errorf("opencode backend %q: unsupported workspace_mode %q (first release only supports %q)",
			name, cfg.WorkspaceMode, opencodeWorkspaceModeGatewayPath)
	}

	timeout := opencodeDefaultTimeout
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}

	client := &http.Client{
		Timeout: timeout,
	}

	return &OpenCodeHTTPBackend{
		cfg:        cfg,
		client:     client,
		name:       name,
		hubResults: make(map[string]hubOutcome),
	}, nil
}

// Name returns the backend name (e.g. "opencode-local").
func (b *OpenCodeHTTPBackend) Name() string { return b.name }

// Run creates a session, sends the user prompt as a text part, and waits for
// the response. The returned CodingResult carries the assistant's text summary
// and the remote session ID (for future continue support).
//
// Provider mapping:
//   - Model/provider are NOT derived from Agent.Provider/Agent.Model — those
//     belong to matea's builtin-LLM namespace and are meaningless (or worse,
//     billable) to the OpenCode sidecar. They are sent only when the agent
//     explicitly overrides them via backend_options (opencode_model +
//     opencode_provider).
//   - Without an override the sidecar runs its own configured default model
//     (matching v4 §4.3).
//
// The actual file modifications happen on disk via the sidecar; Run does not
// touch the workspace. finalizeWriteChanges uses git.HasChanges() to decide
// whether to commit, regardless of what the summary says.
func (b *OpenCodeHTTPBackend) Run(ctx context.Context, req CodingRequest) (*CodingResult, error) {
	task := req.Task

	// OpenCode serve resolves relative directory against its own process cwd,
	// not Gateway's. Always pass an absolute path (A0 / opencode-a0-notes).
	if req.WorkDir != "" {
		if abs, err := filepath.Abs(req.WorkDir); err == nil {
			req.WorkDir = abs
		}
	}

	// 1. Create session
	sessionID, err := b.createSession(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("create opencode session: %w", err)
	}
	log.Printf("[INFO] Task %d opencode session created: %s", task.ID, sessionID)

	// 2. Send message
	summary, messages, err := b.sendMessage(ctx, sessionID, req)
	if err != nil {
		// Try to abort on failure so the sidecar doesn't keep working
		if abortErr := b.Abort(ctx, sessionID); abortErr != nil {
			log.Printf("[WARN] Failed to abort opencode session %s after message error: %v", sessionID, abortErr)
		}
		// Return a failed-but-informative result so that Submit can still
		// surface the transcript (including any assistant error messages) to
		// the conversation log.
		if summary == "" {
			summary = err.Error()
		}
		return &CodingResult{
			Summary:         summary,
			Success:         false,
			RemoteSessionID: sessionID,
			Provider:        nil,
			Messages:        b.opencodeMessagesToLLM(messages),
		}, nil
	}

	log.Printf("[INFO] Task %d opencode coding completed, summary len=%d", task.ID, len(summary))

	return &CodingResult{
		Summary:         summary,
		Success:         true,
		RemoteSessionID: sessionID,
		// Provider is nil for opencode backend — the LLM call is handled
		// server-side. finalizeWriteChanges will still generate a commit
		// message using the gateway's own provider (see note in finalize).
		Provider: nil,
		Messages: b.opencodeMessagesToLLM(messages),
	}, nil
}

// Abort issues POST /session/{id}/abort to the sidecar. It is best-effort —
// a network error does not fail the caller, only logs.
func (b *OpenCodeHTTPBackend) Abort(ctx context.Context, handle string) error {
	if handle == "" {
		return nil
	}
	url := fmt.Sprintf("%s/session/%s/abort", b.cfg.BaseURL, handle)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return err
	}
	b.setAuth(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("abort request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("abort returned status %d", resp.StatusCode)
	}
	return nil
}

// --- API helpers -----------------------------------------------------------

// opencodeCreateSessionResponse is the minimal response shape we need from
// POST /session. Full Session object has many more fields; we only need id.
type opencodeCreateSessionResponse struct {
	ID string `json:"id"`
}

// createSession calls POST /session and returns the new session id.
//
// Working directory binding (OpenCode serve — see docs/archived/20260715-opencode-a0-notes.md):
// The server scopes project context via query `?directory=` and/or header
// `X-Opencode-Directory`. Message-body `directory` alone is NOT sufficient on
// current serve builds to pin the session workspace; we always send both query
// and header when WorkDir is set.
func (b *OpenCodeHTTPBackend) createSession(ctx context.Context, req CodingRequest) (string, error) {
	url := b.cfg.BaseURL + "/session"
	if req.WorkDir != "" {
		url = url + "?directory=" + urlQueryEscape(req.WorkDir)
	}

	body := map[string]interface{}{
		"title": fmt.Sprintf("matea-task-%d", req.Task.ID),
	}

	httpReq, err := b.newJSONRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return "", err
	}
	if req.WorkDir != "" {
		httpReq.Header.Set("X-Opencode-Directory", req.WorkDir)
	}

	var resp opencodeCreateSessionResponse
	if err := b.doJSON(httpReq, &resp); err != nil {
		return "", err
	}
	if resp.ID == "" {
		return "", fmt.Errorf("session create returned empty id")
	}
	return resp.ID, nil
}

// opencodeMessageRequest maps to the /session/{id}/message request body.
type opencodeMessageRequest struct {
	// ModelID/ProviderID are omitted unless explicitly overridden via
	// backend_options — the opencode server then runs its own default model.
	ModelID    string                `json:"modelID,omitempty"`
	ProviderID string                `json:"providerID,omitempty"`
	Parts      []opencodeMessagePart `json:"parts"`
	System     string                `json:"system,omitempty"`
	Tools      *opencodeToolsConfig  `json:"tools,omitempty"`
	Directory  string                `json:"directory,omitempty"`
}

type opencodeMessagePart struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type opencodeToolsConfig struct {
	Search bool `json:"search"`
	Read   bool `json:"read"`
	Write  bool `json:"write"`
	Edit   bool `json:"edit"`
	// Command disabled by default for safety; sidecar manages its own perms.
}

// opencodeMessagesListItem is one entry from GET /session/{id}/message.
// Each item has info (role, id, etc.) and parts (text / tool / etc.).
type opencodeMessagesListItem struct {
	Info  opencodeMessageInfo   `json:"info"`
	Parts []opencodeMessagePart `json:"parts"`
}

type opencodeMessageInfo struct {
	ID    string                `json:"id"`
	Role  string                `json:"role"` // "user" | "assistant"
	Error *opencodeMessageError `json:"error"`
}

// opencodeMessageError mirrors the run failure recorded on an assistant
// message's info.error (e.g. provider 401 / quota / unknown-model errors).
// Without decoding it, a failed run surfaces only as the opaque
// "no assistant text message found".
type opencodeMessageError struct {
	Name string `json:"name"`
	Data struct {
		Message     string `json:"message"`
		StatusCode  int    `json:"statusCode"`
		IsRetryable bool   `json:"isRetryable"`
	} `json:"data"`
}

// formatOpencodeError renders the provider-side run failure for task errors.
func formatOpencodeError(e *opencodeMessageError) string {
	msg := strings.TrimSpace(e.Data.Message)
	name := e.Name
	if name == "" {
		name = "error"
	}
	if e.Data.StatusCode != 0 {
		return fmt.Sprintf("%s (status %d): %s", name, e.Data.StatusCode, msg)
	}
	if msg == "" {
		return name
	}
	return fmt.Sprintf("%s: %s", name, msg)
}

// sendMessage calls POST /session/{id}/message and then polls
// GET /session/{id}/message to extract the assistant's text response.
// It returns the assistant summary plus the full message list so that
// conversation logs can persist the transcript even on failure.
//
// Note: the SDK uses streaming tokens (SSE) for real-time UI. For the gateway's
// headless use case, a synchronous POST + subsequent list-messages lookup is
// sufficient and simpler. The HTTP client timeout guards against hangs.
func (b *OpenCodeHTTPBackend) sendMessage(ctx context.Context, sessionID string, req CodingRequest) (string, []opencodeMessagesListItem, error) {
	msgReq := opencodeMessageRequest{
		Parts: []opencodeMessagePart{
			{Type: "text", Text: req.Prompt},
		},
		System:    req.SystemPrompt,
		Directory: req.WorkDir,
		Tools: &opencodeToolsConfig{
			Search: true,
			Read:   true,
			Write:  true,
			Edit:   true,
		},
	}

	// Model/provider are sent ONLY when explicitly overridden via
	// backend_options (opencode_model + opencode_provider, both required).
	// The agent's own Provider/Model belong to matea's builtin-LLM namespace
	// and must not leak into OpenCode: an unknown or paid pair makes the
	// sidecar run fail (e.g. OpenCode Zen 401 on a paid model). When omitted,
	// the opencode server runs its own configured default model.
	if bo := req.BackendOptions; bo != nil {
		model, _ := bo["opencode_model"].(string)
		provider, _ := bo["opencode_provider"].(string)
		switch {
		case model != "" && provider != "":
			msgReq.ModelID = model
			msgReq.ProviderID = provider
		case model != "" || provider != "":
			log.Printf("[WARN] Task %d: opencode_model and opencode_provider must be set together; ignoring override, opencode server default model applies", req.Task.ID)
		}
		if v, ok := bo["opencode_agent"].(string); ok && v != "" {
			// opencode_agent is noted in v4 §4.3 but the server-side agent
			// selection mechanism differs between releases. Pass through as
			// a non-fatal hint; ignore if the endpoint doesn't consume it.
			log.Printf("[INFO] Task %d: opencode_agent=%q (informational)", req.Task.ID, v)
		}
	}

	url := fmt.Sprintf("%s/session/%s/message", b.cfg.BaseURL, sessionID)
	if req.WorkDir != "" {
		url = url + "?directory=" + urlQueryEscape(req.WorkDir)
	}
	httpReq, err := b.newJSONRequest(ctx, http.MethodPost, url, msgReq)
	if err != nil {
		return "", nil, err
	}
	if req.WorkDir != "" {
		httpReq.Header.Set("X-Opencode-Directory", req.WorkDir)
	}

	// The POST /message endpoint is synchronous and returns when the run completes.
	// We ignore the response body (token stream shape varies) and instead fetch
	// the final assistant message from the messages list, which is more stable.
	if err := b.doJSON(httpReq, nil); err != nil {
		return "", nil, err
	}

	// Fetch message list and extract the last assistant text part
	return b.getLastAssistantMessage(ctx, sessionID)
}

// getLastAssistantMessage fetches the message list and returns both the
// concatenated text of the most recent assistant message and the full message
// list for downstream conversation logging.
func (b *OpenCodeHTTPBackend) getLastAssistantMessage(ctx context.Context, sessionID string) (string, []opencodeMessagesListItem, error) {
	url := fmt.Sprintf("%s/session/%s/message", b.cfg.BaseURL, sessionID)
	httpReq, err := b.newJSONRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}

	var messages []opencodeMessagesListItem
	if err := b.doJSON(httpReq, &messages); err != nil {
		return "", nil, fmt.Errorf("list messages: %w", err)
	}

	// Walk backwards to find the last assistant message
	var lastErr *opencodeMessageError
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role != "assistant" {
			continue
		}
		// Concatenate all text parts
		var texts []string
		for _, p := range messages[i].Parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		if len(texts) > 0 {
			// Join with double-newline to match multi-part natural flow
			return joinParts(texts), messages, nil
		}
		// Assistant message without text: remember its run error so a
		// provider-side failure (401/quota/unknown model) is surfaced
		// instead of the opaque fallback below.
		if lastErr == nil && messages[i].Info.Error != nil {
			lastErr = messages[i].Info.Error
		}
	}

	if lastErr != nil {
		return "", messages, fmt.Errorf("opencode run failed in session %s: %s", sessionID, formatOpencodeError(lastErr))
	}
	return "", messages, fmt.Errorf("no assistant text message found in session %s", sessionID)
}

func joinParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += "\n\n" + p
	}
	return result
}

// opencodeMessagesToLLM converts the raw OpenCode message list into the
// llm.Message shape used by task_conversation_logs. Matea supplies its own
// system/user messages from TaskContext, so OpenCode-echoed user/system roles
// are dropped to avoid duplication.
func (b *OpenCodeHTTPBackend) opencodeMessagesToLLM(messages []opencodeMessagesListItem) []llm.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]llm.Message, 0, len(messages))
	for _, m := range messages {
		role := m.Info.Role
		if role == "user" || role == "system" {
			continue
		}
		if role == "" {
			role = "assistant"
		}
		var texts []string
		for _, p := range m.Parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		content := joinParts(texts)
		if m.Info.Error != nil {
			errText := formatOpencodeError(m.Info.Error)
			if content != "" {
				content = content + "\n\n[error] " + errText
			} else {
				content = "[error] " + errText
			}
		}
		if content == "" {
			continue
		}
		out = append(out, llm.Message{
			Role:    role,
			Content: content,
		})
	}
	return out
}

func urlQueryEscape(s string) string {
	return url.QueryEscape(s)
}

// --- HTTP plumbing ---------------------------------------------------------

func (b *OpenCodeHTTPBackend) newJSONRequest(ctx context.Context, method, url string, body interface{}) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	b.setAuth(req)
	return req, nil
}

func (b *OpenCodeHTTPBackend) setAuth(req *http.Request) {
	if b.cfg.Auth.Username != "" || b.cfg.Auth.Password != "" {
		req.SetBasicAuth(b.cfg.Auth.Username, b.cfg.Auth.Password)
	}
}

// doJSON executes httpReq and decodes the JSON response into out (if out != nil).
// Non-2xx responses return an error with the status and a truncated body.
func (b *OpenCodeHTTPBackend) doJSON(req *http.Request, out interface{}) error {
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		// Truncate long error bodies for readability
		snippet := string(body)
		if len(snippet) > 500 {
			snippet = snippet[:500] + "..."
		}
		return fmt.Errorf("opencode API %s %s returned %d: %s",
			req.Method, req.URL.Path, resp.StatusCode, snippet)
	}

	if out != nil && len(body) > 0 {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// --- Health check ----------------------------------------------------------

// HealthCheck calls the configured health endpoint and reports whether the
// sidecar is reachable and healthy. Returns nil on success, error otherwise.
//
// The health endpoint path is configurable (default "/health" if HealthCheck.Path
// is empty). The check is a simple GET; any 2xx status counts as healthy.
func (b *OpenCodeHTTPBackend) HealthCheck(ctx context.Context) error {
	path := b.cfg.HealthCheck.Path
	if path == "" {
		path = "/health"
	}
	url := b.cfg.BaseURL + path

	// Short timeout for health checks — we don't want to block the task queue
	client := &http.Client{Timeout: 5 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	b.setAuth(req)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

// --- HubBackend implementation (task 1.2.5) ----------------------------------
//
// OpenCodeHTTPBackend implements HubBackend alongside CodingBackend, so the
// hub seam has two real implementations (builtin + hub-opencode) to validate
// against. Phase 1 execution stays synchronous: Submit runs the opencode
// session to completion via the existing CodingBackend.Run path and caches
// the outcome keyed by session id; the first Poll reports the terminal state.
// True async polling of the hub session (with Handle persistence and
// executor re-attach per 1.2.1) is Phase 2 — the current CodingBackend runner
// path is unchanged and OpenCode remains fully usable.
//
// Write-only constraint (ARCHITECTURE: Analyze/Review never走 OpenCode) is
// enforced here too: non-write task types are rejected at Submit.

// Compile-time interface compliance check.
var _ HubBackend = (*OpenCodeHTTPBackend)(nil)

// Capabilities declares the opencode feature set: the sidecar runs its own
// tool-use loop server-side; Matea keeps git/PR finalization.
func (b *OpenCodeHTTPBackend) Capabilities() HubCapabilities {
	return HubCapabilities{
		SupportsToolUse: true,
	}
}

// Submit runs a write task on the opencode sidecar synchronously and returns
// a Handle whose RemoteID is the opencode session id. Non-write task types
// are rejected as submission errors.
//
// Workspace requirement: read/reply tasks need TaskContext.SandboxPath (a
// Matea-prepared minimal scratch directory the sidecar can work in).
// git_sync write tasks (tc.GitSync != nil, task A4) need NO Matea workspace —
// the sidecar clones the repo itself using the deploy key carried in
// GitSyncInfo, into a per-task subdirectory of the opencode server's own
// project directory (the X-Opencode-Directory header is omitted so the
// server default applies). Since A5, git_sync is the ONLY hub write
// transport; a write task without GitSyncInfo is rejected here rather than
// silently given a local workspace.
func (b *OpenCodeHTTPBackend) Submit(ctx context.Context, tc *TaskContext) (*Handle, error) {
	if tc == nil {
		return nil, fmt.Errorf("hub-opencode backend %q: nil TaskContext", b.name)
	}
	subType, err := opencodeSubType(tc.TaskType)
	if err != nil {
		return nil, err
	}
	gitSync := tc.GitSync != nil
	if !gitSync && tc.SandboxPath == "" {
		return nil, fmt.Errorf("hub-opencode backend %q: TaskContext.SandboxPath is required for %s tasks", b.name, tc.TaskType)
	}

	userPrompt := tc.UserPrompt
	// Session + repo/issue memory (B2.3): the shared renderer both hubs use.
	// OpenCode previously dropped MemoryKeys entirely; it now gets the same
	// block Hermes receives. Appended BEFORE the git_sync contract so the
	// mandatory workflow stays last (recency window).
	if mc := BuildMemoryContext(tc); mc != "" {
		userPrompt = strings.TrimSpace(userPrompt + "\n\n" + mc)
	}
	if gitSync {
		// The hub clones/commits/pushes per the spike-validated contract; the
		// instructions carry the task-scoped deploy key (base64) and the draft
		// branch it may push. Work happens in a per-task subdirectory so
		// concurrent sessions never share a checkout.
		userPrompt = strings.TrimSpace(tc.UserPrompt + "\n\n" +
			BuildGitSyncInstructions(tc.GitSync, fmt.Sprintf("matea-hub-%d", tc.TaskID)))
	}

	res, err := b.Run(ctx, CodingRequest{
		WorkDir:      tc.SandboxPath, // empty under git_sync → default opencode project
		Task:         &store.Task{ID: tc.TaskID, Repo: tc.Repo, Event: tc.IssueTitle, Context: tc.IssueBody},
		Agent:        &store.Agent{Provider: tc.Provider, Model: tc.Model},
		TaskSubType:  subType,
		Prompt:       userPrompt,
		SystemPrompt: tc.SystemPrompt,
	})
	if err != nil {
		return nil, err
	}
	if !res.Success {
		// Cache the failed result and return a Handle so that runViaHub's Poll
		// path reports StateFailed with the transcript preserved for conversation
		// logging.
		remoteID := res.RemoteSessionID
		if remoteID == "" {
			remoteID = fmt.Sprintf("opencode-%d", time.Now().UnixNano())
		}
		h := &Handle{
			Backend:        b.name,
			RemoteID:       remoteID,
			IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", tc.TaskType, tc.Repo, tc.IssueID, tc.PRID),
		}
		result := &BackendResult{Summary: res.Summary, Messages: res.Messages}
		// Do not report GitSync on failure: the draft branch was not validated
		// and runViaHub's StateFailed branch does not consume it.
		b.hubMu.Lock()
		b.hubResults[remoteID] = hubOutcome{
			result: result,
			err:    fmt.Errorf("hub-opencode backend %q reported failure: %s", b.name, res.Summary),
		}
		b.hubMu.Unlock()
		return h, nil
	}

	remoteID := res.RemoteSessionID
	if remoteID == "" {
		remoteID = fmt.Sprintf("opencode-%d", time.Now().UnixNano())
	}
	h := &Handle{
		Backend:        b.name,
		RemoteID:       remoteID,
		IdempotencyKey: fmt.Sprintf("%s:%s:%d:%d", tc.TaskType, tc.Repo, tc.IssueID, tc.PRID),
	}
	result := &BackendResult{Summary: res.Summary, Messages: res.Messages}
	if gitSync {
		// Report the draft branch back so runViaHub's Approve can fetch and
		// validate it. The trailer cross-checks hub honesty; the fetched remote
		// state is authoritative.
		result.GitSync = &GitSyncResult{
			DraftBranch: tc.GitSync.DraftBranch,
			DraftHEAD:   ParseDraftHeadTrailer(res.Summary),
		}
	}
	b.hubMu.Lock()
	b.hubResults[remoteID] = hubOutcome{result: result}
	b.hubMu.Unlock()
	return h, nil
}

// Poll returns the cached terminal outcome for a Handle produced by Submit.
// The cache is instance-local (see the struct doc); an unknown RemoteID is an
// error, never a silent pending.
//
// Restart re-attach: when the cache miss is caused by a Matea restart (the
// instance-local cache is gone but the opencode sidecar is a separate process
// that may still hold the session), Poll attempts to recover the result
// directly from the sidecar via GET /session/{id}/message. This is the recovery
// half of the Handle persistence contract — without it, a persisted Handle
// after restart would always report "unknown handle" and the task would fail
// despite the sidecar having finished. If the sidecar no longer has the session
// (GC'd or also restarted), Poll falls back to the unknown-handle error, which
// lets the executor retry / re-submit as appropriate.
func (b *OpenCodeHTTPBackend) Poll(ctx context.Context, h *Handle) (*BackendResult, State, error) {
	if h == nil {
		return nil, "", fmt.Errorf("hub-opencode backend %q: nil Handle", b.name)
	}
	if h.Backend != "" && h.Backend != b.name {
		return nil, "", fmt.Errorf("hub-opencode backend %q: handle belongs to backend %q", b.name, h.Backend)
	}
	b.hubMu.Lock()
	out, ok := b.hubResults[h.RemoteID]
	b.hubMu.Unlock()
	if ok {
		if out.err != nil {
			// Return the result alongside the error so that runViaHub can still
			// log any assistant-side transcript captured before the failure.
			return out.result, StateFailed, out.err
		}
		return out.result, StateDone, nil
	}

	// Cache miss — try to re-attach to the still-living sidecar session.
	summary, messages, rerr := b.getLastAssistantMessage(ctx, h.RemoteID)
	if rerr == nil && summary != "" {
		res := &BackendResult{Summary: summary, Messages: b.opencodeMessagesToLLM(messages)}
		// Re-populate the cache so subsequent Polls are cheap and consistent.
		b.hubMu.Lock()
		b.hubResults[h.RemoteID] = hubOutcome{result: res}
		b.hubMu.Unlock()
		log.Printf("[INFO] hub-opencode backend %q: re-attached to sidecar session %q after cache miss", b.name, h.RemoteID)
		return res, StateDone, nil
	}
	if len(messages) > 0 {
		// The sidecar still has the session but the run failed. Preserve the
		// transcript so a Matea restart does not lose the failure context.
		if summary == "" {
			summary = rerr.Error()
		}
		res := &BackendResult{Summary: summary, Messages: b.opencodeMessagesToLLM(messages)}
		b.hubMu.Lock()
		b.hubResults[h.RemoteID] = hubOutcome{result: res, err: rerr}
		b.hubMu.Unlock()
		log.Printf("[INFO] hub-opencode backend %q: re-attached to failed sidecar session %q after cache miss", b.name, h.RemoteID)
		return res, StateFailed, rerr
	}

	return nil, "", fmt.Errorf("hub-opencode backend %q: unknown handle %q", b.name, h.RemoteID)
}

// Cancel aborts the opencode session referenced by the Handle (best effort,
// same semantics as CodingBackend.Abort). Nil handles and empty RemoteIDs are
// tolerated — cancellation is idempotent by nature.
func (b *OpenCodeHTTPBackend) Cancel(ctx context.Context, h *Handle) error {
	if h == nil || h.RemoteID == "" {
		return nil
	}
	return b.Abort(ctx, h.RemoteID)
}

// opencodeSubType maps a hub task type to the coding sub-type OpenCode
// understands. Write tasks (solve/fix) map to their natural sub-type; read
// tasks (analyze/review/reply) map to "dev" as a best-effort carrier — the
// workspace is prepared by Matea and cleaned up after, so the read-only
// contract is preserved regardless of what OpenCode does inside the sandbox.
//
// Wired in 2.2.1 (analyze), 2.2.2 (review) and 2.2.3 (reply); unknown task
// types are rejected.
func opencodeSubType(taskType string) (string, error) {
	switch taskType {
	case "solve_issue", "solve_comment":
		return "dev", nil
	case "fix_bug":
		return "bugfix", nil
	case "analyze_issue":
		return "dev", nil
	case "review_pr":
		return "dev", nil
	case "reply_comment":
		return "dev", nil
	default:
		return "", fmt.Errorf("hub-opencode backend: unsupported task type %q (supported: solve_issue, solve_comment, fix_bug, analyze_issue, review_pr, reply_comment)", taskType)
	}
}
