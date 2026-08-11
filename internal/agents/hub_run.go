package agents

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// Hub execution tuning.
const (
	// hubPollInterval is the delay between Poll calls while a hub-side run is
	// in progress. Hub runs (especially code tasks) can take minutes, so a few
	// seconds of spacing is appropriate.
	hubPollInterval = 2 * time.Second

	// hubPollTimeout caps the total time a single hub task may run before the
	// executor gives up and marks it failed. Long-running code tasks may need
	// this raised; it is a safety net against stuck runs, not a typical bound.
	hubPollTimeout = 30 * time.Minute

	// hubCancelTimeout bounds the best-effort Cancel call issued when a run is
	// aborted. It runs on a fresh context because the caller's context is by
	// definition already done at that point.
	hubCancelTimeout = 10 * time.Second

	// AnalyzeMemoryKey is the repo/issue-scoped memory key under which an
	// analyze task records its conclusion so later tasks (review/code) on the
	// same repo+issue can recall it (D3 cross-task memory sharing, task 2.1.5).
	//
	// Exported so out-of-package consumers (the hermes e2e tests today, a
	// memory inspection/cleanup API later) reference the single definition
	// instead of re-spelling the literal.
	AnalyzeMemoryKey = "analysis_summary"

	// ReviewMemoryKey records a review conclusion (D3 cross-task memory
	// sharing, task 2.2.2). Best-effort, like AnalyzeMemoryKey.
	ReviewMemoryKey = "review_summary"
)

// ResolveHubExecution decides whether an agent's backend is a Hermes-type hub
// that should execute the task through Submit/Poll instead of Matea's
// in-process LLM.
//
// This is the *execution path* decision, not an authorization check — see
// validateHubDispatch, which is the gate runners call first. Returning false
// here is routine (builtin, and hub-opencode which is write-only) and does not
// imply the agent's backend is invalid.
//
// Only hub-hermes routes task execution through the hub in Phase 2; builtin
// and hub-opencode (write-only) continue through their existing paths. The
// decision mirrors ResolveHubBackend: normalize the name, fall back to the
// default, then check the configured type. Returns the resolved HubBackend and
// true when the task should run via the hub.
func (f *RunnerFactory) ResolveHubExecution(agent *store.Agent) (HubBackend, bool) {
	name := config.NormalizeBackend(agent.Backend)
	if name == "" {
		name = config.NormalizeBackend(f.backends.Default)
	}
	if name == "" || name == config.BackendNameBuiltin {
		return nil, false
	}
	cfg, ok := f.backends.Backends[name]
	if !ok {
		return nil, false
	}
	if config.NormalizeBackend(cfg.Type) != config.BackendTypeHubHermes {
		return nil, false
	}
	hb, err := f.hubRegistry.Lookup(name)
	if err != nil {
		return nil, false
	}
	return hb, true
}

// ResolveHubOpenCode decides whether an agent's backend is an OpenCode-type hub
// that should execute the task through Submit/Poll. Mirrors ResolveHubExecution
// but matches on BackendTypeHubOpenCode. Returns the resolved HubBackend and
// true when the task should run through OpenCode.
//
// Phase 2 (D7) wires analyze (2.2.1), review (2.2.2) and reply (2.2.3) through
// OpenCode. Review clones the PR head; reply uses a minimal empty workspace
// (decision B) since it needs no repository contents.
func (f *RunnerFactory) ResolveHubOpenCode(agent *store.Agent) (HubBackend, bool) {
	name := config.NormalizeBackend(agent.Backend)
	if name == "" {
		name = config.NormalizeBackend(f.backends.Default)
	}
	if name == "" || name == config.BackendNameBuiltin {
		return nil, false
	}
	cfg, ok := f.backends.Backends[name]
	if !ok {
		return nil, false
	}
	if config.NormalizeBackend(cfg.Type) != config.BackendTypeHubOpenCode {
		return nil, false
	}
	hb, err := f.hubRegistry.Lookup(name)
	if err != nil {
		return nil, false
	}
	return hb, true
}

// runViaHub executes a task through a hub backend's async Submit/Poll contract
// and maps the resulting BackendResult back into a Runner Result. It polls
// until the handle reaches a terminal state or the context is cancelled.
//
// Persistence of the Handle for restart-recovery (HubBackend contract §1.2.1)
// is intentionally out of scope for this Phase 2 wiring pass; the in-process
// poll loop satisfies the async contract for live executions. Executor
// re-attach on restart is a follow-up hardening task.
func (f *RunnerFactory) runViaHub(ctx context.Context, task *store.Task, agent *store.Agent, backend HubBackend, tc *TaskContext) (*Result, error) {
	if tc == nil {
		return nil, fmt.Errorf("hub execution: nil TaskContext")
	}

	handle, err := backend.Submit(ctx, tc)
	if err != nil {
		return nil, fmt.Errorf("hub submit: %w", err)
	}

	// pollCtx is derived from ctx, so an executor-side cancel propagates here
	// immediately; the timeout is only an upper safety bound on top of it.
	pollCtx, cancel := context.WithTimeout(ctx, hubPollTimeout)
	defer cancel()

	for {
		res, state, err := backend.Poll(pollCtx, handle)
		if err != nil {
			// A cancelled/expired context surfaces as a transport error from
			// Poll. Attribute it correctly and tell the hub to stop, instead
			// of reporting it as a backend failure.
			if pollCtx.Err() != nil {
				return nil, abortHubRun(ctx, pollCtx, backend, handle)
			}
			return nil, fmt.Errorf("hub poll: %w", err)
		}
		if state.IsTerminal() {
			switch state {
			case StateFailed:
				return nil, fmt.Errorf("hub backend %q reported task failure", backend.Name())
			case StateCanceled:
				return nil, fmt.Errorf("hub backend %q cancelled the task", backend.Name())
			default: // StateDone
				return mapHubResult(backend, res), nil
			}
		}
		select {
		case <-pollCtx.Done():
			return nil, abortHubRun(ctx, pollCtx, backend, handle)
		case <-time.After(hubPollInterval):
		}
	}
}

// abortHubRun handles a hub run that must stop before reaching a terminal
// state. It attributes the cause (executor cancel vs. task deadline vs. the
// local poll safety timeout), asks the backend to stop the remote run on a
// best-effort basis, and returns the resulting error.
//
// The Cancel call uses a fresh context: both ctx and pollCtx are already done
// by the time we get here, so reusing them would make Cancel a guaranteed
// no-op and leave the hub-side run orphaned.
func abortHubRun(ctx, pollCtx context.Context, backend HubBackend, handle *Handle) error {
	cause := ctx.Err()
	reason := "task cancelled by executor"
	switch {
	case cause == nil:
		cause = pollCtx.Err()
		reason = fmt.Sprintf("hub poll safety timeout of %s exceeded", hubPollTimeout)
	case errors.Is(cause, context.DeadlineExceeded):
		reason = "task deadline exceeded"
	}

	cancelCtx, cancel := context.WithTimeout(context.Background(), hubCancelTimeout)
	defer cancel()
	if err := backend.Cancel(cancelCtx, handle); err != nil {
		log.Printf("[WARN] hub backend %q: cancel run %q failed after abort: %v",
			backend.Name(), handle.RemoteID, err)
	}

	return fmt.Errorf("hub run aborted (%s): %w", reason, cause)
}

// mapHubResult converts a completed BackendResult into a Runner Result.
//
// Phase 2 scope: only the free-text Summary is consumed and the action is
// always a Gitea comment, because the wired task types are read/reply
// (analyze / review / reply_comment). The richer BackendResult fields —
// GiteaActions (e.g. a hub-requested create_pr) and Deliver (IM fan-out) —
// are deliberately ignored here and warned about, so a hub asking for them
// fails visibly in the log rather than silently doing nothing. Honoring them
// belongs to the write-task wiring pass.
func mapHubResult(backend HubBackend, res *BackendResult) *Result {
	if res == nil {
		return &Result{Action: "comment"}
	}
	if len(res.GiteaActions) > 0 {
		log.Printf("[WARN] hub backend %q returned %d gitea_actions; ignored in the Phase 2 read/reply path (only summary → comment is honored)",
			backend.Name(), len(res.GiteaActions))
	}
	if res.Deliver != nil {
		log.Printf("[WARN] hub backend %q returned a deliver request (event=%q channel=%q); ignored in the Phase 2 read/reply path",
			backend.Name(), res.Deliver.Event, res.Deliver.Channel)
	}
	if res.ExternallyHandled {
		log.Printf("[WARN] hub backend %q set externally_handled on a read/reply task; ignored (no git work to skip)",
			backend.Name())
	}
	return &Result{Content: res.Summary, Action: "comment"}
}

// toCommentSnapshots converts Gitea issue/PR comments into the serializable
// CommentSnapshot form the hub backend consumes (it must not call Gitea
// directly). Missing/garbled timestamps are left zero-valued — no hub uses
// CreatedAt for ordering today, but an unexplained 0001-01-01 in a request
// dump is confusing, so an unparsable (non-empty) value is logged.
func toCommentSnapshots(comments []gitea.IssueComment) []CommentSnapshot {
	out := make([]CommentSnapshot, 0, len(comments))
	for _, c := range comments {
		var created time.Time
		if t, err := time.Parse(time.RFC3339, c.Created); err == nil {
			created = t
		} else if c.Created != "" {
			log.Printf("[DEBUG] comment by %q has unparsable timestamp %q; sending zero CreatedAt: %v",
				c.User.Login, c.Created, err)
		}
		out = append(out, CommentSnapshot{
			Author:    c.User.Login,
			Body:      c.Body,
			CreatedAt: created,
		})
	}
	return out
}

// loadMemoryKeys returns the repo/issue-scoped memory map for a task, or nil
// when no store is configured or no memory exists. Used to carry D3
// cross-task memory into the hub request (task 2.1.5).
func (f *RunnerFactory) loadMemoryKeys(task *store.Task) map[string]string {
	if f.db == nil {
		return nil
	}
	m, err := f.db.GetAllMemory(task.Repo, task.IssueID)
	if err != nil || len(m) == 0 {
		return nil
	}
	return m
}

// saveAnalyzeMemory records an analyze task's conclusion under the
// repo/issue-scoped AnalyzeMemoryKey so later tasks on the same
// repo+issue can recall it (task 2.1.5). Failures are logged, not fatal —
// memory is a best-effort enhancement, never a hard dependency.
func (f *RunnerFactory) saveAnalyzeMemory(task *store.Task, content string) {
	if f.db == nil || content == "" {
		return
	}
	if err := f.db.SetMemory(task.Repo, task.IssueID, AnalyzeMemoryKey, content); err != nil {
		log.Printf("[WARN] save analyze memory (repo=%s issue=%d): %v", task.Repo, task.IssueID, err)
	}
}

// saveReviewMemory records a review task's conclusion under the
// repo/issue-scoped ReviewMemoryKey (task 2.2.2, D7 second cut). Failures are
// logged, not fatal — memory is a best-effort enhancement, never a hard
// dependency.
func (f *RunnerFactory) saveReviewMemory(task *store.Task, content string) {
	if f.db == nil || content == "" {
		return
	}
	if err := f.db.SetMemory(task.Repo, task.IssueID, ReviewMemoryKey, content); err != nil {
		log.Printf("[WARN] save review memory (repo=%s issue=%d): %v", task.Repo, task.IssueID, err)
	}
}
