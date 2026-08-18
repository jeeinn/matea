package agents

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/deliver"
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
// Reliability (HubBackend contract §1.2.1): the Handle is persisted to SQLite
// immediately after Submit, and on re-entry the function reuses any persisted
// non-terminal Handle for the task instead of re-submitting. That single branch
// delivers two guarantees:
//   - Idempotency: executor whole-task retries (and the stale-scanner's re-enqueue,
//     which is suppressed for hub tasks) never double-submit the same task.
//   - Restart re-attach: if Matea crashes mid-run, the Executor re-enqueues the
//     orphaned task on startup; this function rebuilds the in-process poll loop
//     from the persisted Handle and the backend re-attaches to the still-living
//     remote run (see OpenCodeHTTPBackend.Poll, which re-reads the sidecar
//     session when its instance-local cache was lost on restart).
func (f *RunnerFactory) runViaHub(ctx context.Context, task *store.Task, agent *store.Agent, backend HubBackend, tc *TaskContext) (*Result, error) {
	if tc == nil {
		return nil, fmt.Errorf("hub execution: nil TaskContext")
	}

	// git_sync write path (task A3): when the backend's workspace_transport is
	// git_sync and this is a write task, Matea Prepares credentials before
	// Submit and Approves the hub-pushed draft branch after completion.
	// Read/reply tasks take the original comment path.
	transport := f.gitSyncTransportFor(backend)
	writeViaGitSync := transport != nil && isWriteTaskType(task.TaskType)
	var gitSyncInfo *GitSyncInfo
	var issuedKey *IssuedDeployKey

	// Idempotency + restart re-attach: reuse a persisted non-terminal Handle
	// for this task instead of re-submitting. Skipping Submit here is what
	// prevents a duplicate remote run when the task is retried or re-attached.
	var handle *Handle
	var persisted *store.HubHandle
	if f.db != nil {
		if existing, gerr := f.db.GetHubHandle(task.ID); gerr == nil && existing != nil && !store.IsTerminalHubStatus(existing.Status) {
			handle = &Handle{
				Backend:        existing.Backend,
				RemoteID:       existing.RemoteID,
				IdempotencyKey: existing.IdempotencyKey,
			}
			persisted = existing
			log.Printf("[INFO] hub execution task %d: re-attaching to persisted handle (backend=%q remote=%q)",
				task.ID, existing.Backend, existing.RemoteID)
		}
	}

	if writeViaGitSync && handle == nil {
		owner, repo := splitOwnerRepo(task.Repo)
		info, key, err := transport.Prepare(ctx, task, owner, repo, task.BaseBranch)
		if err != nil {
			return nil, fmt.Errorf("git_sync prepare: %w", err)
		}
		gitSyncInfo = info
		issuedKey = key
		// Session continuation (B2.3): when the task's session recorded a
		// LastHead, the hub branches the NEW per-task draft branch from that
		// commit instead of the base tip. Validation anchors on it too.
		if anchor := f.sessionLastHead(task); anchor != "" {
			gitSyncInfo.AnchorHEAD = anchor
			log.Printf("[INFO] hub execution task %d: session continuation anchored at %s", task.ID, anchor[:min(8, len(anchor))])
		}
		tc.GitSync = info
	}
	if writeViaGitSync && persisted != nil && persisted.DraftBranch != "" {
		// Re-attach: rebuild the approval-side contract from the handle row
		// (Prepare is NOT re-run — the same draft branch/key stay in force).
		// The anchor comes from the persisted row (B2.3), NOT a fresh session
		// read: a concurrent same-session task may have advanced LastHead
		// since this task's Prepare, and validation must use the original one.
		gitSyncInfo = &GitSyncInfo{
			DraftBranch:    persisted.DraftBranch,
			BaseBranch:     task.BaseBranch,
			BaseHEAD:       persisted.BaseHEAD,
			AnchorHEAD:     persisted.AnchorHEAD,
			RequiredFooter: RequiredFooter(task.ID),
			HubPush:        true,
		}
		issuedKey = &IssuedDeployKey{KeyID: persisted.DeployKeyID}
	}

	if handle == nil {
		var err error
		handle, err = backend.Submit(ctx, tc)
		if err != nil {
			if issuedKey != nil {
				f.cleanupGitSyncKey(transport, task, issuedKey)
			}
			return nil, fmt.Errorf("hub submit: %w", err)
		}
		// Persist the Handle immediately after Submit so a crash before the
		// poll loop finishes is recoverable on restart (re-attach, not re-run).
		if f.db != nil {
			row := &store.HubHandle{
				TaskID:         task.ID,
				Backend:        handle.Backend,
				RemoteID:       handle.RemoteID,
				IdempotencyKey: handle.IdempotencyKey,
				Status:         store.HubHandleStatusRunning,
			}
			if gitSyncInfo != nil {
				row.DraftBranch = gitSyncInfo.DraftBranch
				row.BaseHEAD = gitSyncInfo.BaseHEAD
				row.AnchorHEAD = gitSyncInfo.AnchorHEAD
			}
			if issuedKey != nil {
				row.DeployKeyID = issuedKey.KeyID
			}
			if serr := f.db.SaveHubHandle(row); serr != nil {
				log.Printf("[WARN] hub execution task %d: failed to persist handle: %v", task.ID, serr)
			}
		}
	}

	// pollCtx is derived from ctx, so an executor-side cancel propagates here
	// immediately; the timeout is only an upper safety bound on top of it.
	pollCtx, cancel := context.WithTimeout(ctx, hubPollTimeout)
	defer cancel()

	for {
		res, state, err := backend.Poll(pollCtx, handle)
		// A terminal state takes precedence over any accompanying error:
		// backends report failure as (nil, StateFailed, err) — the error
		// describes why, but the run is over and the Handle must be marked
		// terminal so it is never re-attached or re-submitted.
		if state.IsTerminal() {
			switch state {
			case StateFailed:
				f.markHubHandleTerminal(task.ID, store.HubHandleStatusFailed)
				f.cleanupGitSyncKey(transport, task, issuedKey)
				if err != nil {
					return nil, fmt.Errorf("hub backend %q reported task failure: %w", backend.Name(), err)
				}
				return nil, fmt.Errorf("hub backend %q reported task failure", backend.Name())
			case StateCanceled:
				f.markHubHandleTerminal(task.ID, store.HubHandleStatusCanceled)
				f.cleanupGitSyncKey(transport, task, issuedKey)
				if err != nil {
					return nil, fmt.Errorf("hub backend %q cancelled the task: %w", backend.Name(), err)
				}
				return nil, fmt.Errorf("hub backend %q cancelled the task", backend.Name())
			default: // StateDone
				f.markHubHandleTerminal(task.ID, store.HubHandleStatusDone)
				if writeViaGitSync {
					// Write path (task A3/A4): the hub already committed and
					// pushed the draft branch; Approve fetches, validates the
					// three elements and opens the PR. finalizeWriteChanges'
					// commit/push stage is intentionally bypassed.
					if gitSyncInfo == nil {
						f.cleanupGitSyncKey(transport, task, issuedKey)
						return nil, fmt.Errorf("hub backend %q completed write task %d but no git_sync prepare state is available", backend.Name(), task.ID)
					}
					summary := ""
					var gitSyncRes *GitSyncResult
					if res != nil {
						summary = res.Summary
						gitSyncRes = res.GitSync
					}
					if gitSyncRes == nil {
						// The draft branch name is deterministic and Approve's
						// fetch is authoritative, so a hub that reports no
						// explicit git_sync result (e.g. re-attached via Poll
						// after a restart, or Hermes whose Poll output carries
						// only the trailer) still completes — a hub that pushed
						// nothing fails at the fetch ("not found on remote").
						gitSyncRes = &GitSyncResult{
							DraftBranch: gitSyncInfo.DraftBranch,
							DraftHEAD:   ParseDraftHeadTrailer(summary),
						}
					} else {
						// Partial result hardening (B1): a hub that reports a
						// git_sync result with individual fields empty gets them
						// filled from the authoritative Prepare-side contract
						// and the trailer — the fetch validation downstream is
						// the final arbiter either way.
						if gitSyncRes.DraftBranch == "" {
							gitSyncRes.DraftBranch = gitSyncInfo.DraftBranch
						}
						if gitSyncRes.DraftHEAD == "" {
							gitSyncRes.DraftHEAD = ParseDraftHeadTrailer(summary)
						}
					}
					owner, repo := splitOwnerRepo(task.Repo)
					out, aerr := transport.Approve(ctx, task, agent, owner, repo, gitSyncInfo, gitSyncRes, summary)
					f.cleanupGitSyncKey(transport, task, issuedKey)
					if aerr != nil {
						// B3: diff-whitelist violations are security-relevant —
						// audit them to operation_logs before surfacing the error.
						var viol *DiffPolicyViolationError
						if errors.As(aerr, &viol) && f.db != nil {
							f.db.LogOperation(agent.ID, task.ID, "git_sync_diff_violation",
								fmt.Sprintf("backend=%q branch=%q paths=%s",
									backend.Name(), gitSyncInfo.DraftBranch, strings.Join(viol.Paths, ",")))
						}
						return nil, fmt.Errorf("git_sync approve: %w", aerr)
					}
					// Session continuation state (B2.3): record the pushed draft
					// branch + its authoritative head (Approve normalized
					// gitSyncRes.DraftHEAD to the fetched value) as the next
					// task's anchor, plus the rolling session summary that the
					// next continuation prompt injects.
					saveSessionProgress(f, task, gitSyncInfo.DraftBranch, gitSyncRes.DraftHEAD)
					saveSessionMemory(f, task, summary)
					f.emitDeliverEvent(backend.Name(), deliver.Event{
						Event:   deliver.EventTaskCompleted,
						Repo:    task.Repo,
						IssueID: task.IssueID,
						PRID:    out.PRID,
						Action:  out.Action,
						Content: summary,
					}, false)
					return out, nil
				}
				return f.mapHubResult(backend, res, task), nil
			}
		}
		if err != nil {
			// A cancelled/expired context surfaces as a transport error from
			// Poll. Attribute it correctly and tell the hub to stop, instead
			// of reporting it as a backend failure.
			if pollCtx.Err() != nil {
				f.cleanupGitSyncKey(transport, task, issuedKey)
				return nil, abortHubRun(f, task, ctx, pollCtx, backend, handle)
			}
			return nil, fmt.Errorf("hub poll: %w", err)
		}
		select {
		case <-pollCtx.Done():
			f.cleanupGitSyncKey(transport, task, issuedKey)
			return nil, abortHubRun(f, task, ctx, pollCtx, backend, handle)
		case <-time.After(hubPollInterval):
		}
	}
}

// markHubHandleTerminal records the terminal status of a persisted hub handle.
// Best-effort: when db is nil or no handle was persisted, it is a no-op so a
// missing row never fails an otherwise-successful run.
func (f *RunnerFactory) markHubHandleTerminal(taskID int64, status string) {
	if f.db == nil {
		return
	}
	if err := f.db.UpdateHubHandleStatus(taskID, status); err != nil {
		log.Printf("[WARN] hub handle task %d: failed to mark %q: %v", taskID, status, err)
	}
}

// splitOwnerRepo splits "owner/repo" task identifiers into their parts.
func splitOwnerRepo(full string) (string, string) {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) != 2 {
		return full, ""
	}
	return parts[0], parts[1]
}

// cleanupGitSyncKey revokes the task-scoped deploy key after a git_sync run
// reaches any terminal state. Best-effort: failures are logged (the Gitea
// delete is idempotent, so an operator-side retry/sweep stays safe); a nil
// key/transport is a no-op so read/reply tasks are unaffected.
func (f *RunnerFactory) cleanupGitSyncKey(transport WorkspaceTransport, task *store.Task, key *IssuedDeployKey) {
	if transport == nil || key == nil || key.KeyID == 0 {
		return
	}
	owner, repo := splitOwnerRepo(task.Repo)
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := transport.Cleanup(cleanupCtx, owner, repo, key); err != nil {
		log.Printf("[WARN] task %d: git_sync deploy key %d revoke failed (safe to retry): %v", task.ID, key.KeyID, err)
	}
}

// abortHubRun handles a hub run that must stop before reaching a terminal
// state. It attributes the cause (executor cancel vs. task deadline vs. the
// local poll safety timeout), asks the backend to stop the remote run on a
// best-effort basis, marks the persisted Handle terminal locally (so a
// restart never re-attaches or re-submits this run), and returns the resulting
// error.
//
// The Cancel call uses a fresh context: both ctx and pollCtx are already done
// by the time we get here, so reusing them would make Cancel a guaranteed
// no-op and leave the hub-side run orphaned. Even when Cancel is a no-op on
// the backend (e.g. Hermes' minimal contract has no cancel endpoint), marking
// the Handle canceled here is what actually prevents a restart re-pickup of
// the orphaned run (Phase 2 review Problem E-1).
func abortHubRun(f *RunnerFactory, task *store.Task, ctx, pollCtx context.Context, backend HubBackend, handle *Handle) error {
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

	// Mark the Handle terminal so a Matea restart never re-attaches or
	// re-submits this aborted run (the hub-side run may yet finish on its own,
	// but Matea must not treat it as in-flight).
	f.markHubHandleTerminal(task.ID, store.HubHandleStatusCanceled)

	return fmt.Errorf("hub run aborted (%s): %w", reason, cause)
}

// mapHubResult converts a completed BackendResult into a Runner Result.
//
// Phase 2 scope: only the free-text Summary is consumed and the action is
// always a Gitea comment, because the wired task types are read/reply
// (analyze / review / reply_comment). The richer BackendResult fields —
// GiteaActions (e.g. a hub-requested create_pr) — are still ignored here and
// warned about; Deliver (IM fan-out, task 2.3.3) is honored by emitting an
// outbound event. When the hub returns no explicit DeliverRequest (channel-less
// hubs like OpenCode never do), a task_completed event is synthesized so the
// deliver.webhook_url promise of task 2.2.4 holds for hub results too; like the
// builtin path, the synthesized event is a silent no-op when unconfigured.
func (f *RunnerFactory) mapHubResult(backend HubBackend, res *BackendResult, task *store.Task) *Result {
	if res == nil {
		return &Result{Action: "comment"}
	}
	if len(res.GiteaActions) > 0 {
		log.Printf("[WARN] hub backend %q returned %d gitea_actions; ignored in the Phase 2 read/reply path (only summary → comment is honored)",
			backend.Name(), len(res.GiteaActions))
	}
	if res.Deliver != nil {
		f.emitDeliver(backend, res.Deliver)
	} else if task != nil {
		f.emitDeliverEvent(backend.Name(), deliver.Event{
			Event:   deliver.EventTaskCompleted,
			Repo:    task.Repo,
			IssueID: task.IssueID,
			PRID:    task.PRID,
			Action:  "comment",
			Content: res.Summary,
		}, false)
	}
	if res.ExternallyHandled {
		log.Printf("[WARN] hub backend %q set externally_handled on a read/reply task; ignored (no git work to skip)",
			backend.Name())
	}
	return &Result{Content: res.Summary, Action: "comment"}
}

// emitDeliver fans out a hub backend's DeliverRequest to the configured
// webhook (task 2.3.3). It is best-effort: failure is logged, never fatal.
// When no deliver client is configured (webhook_url empty), the request is
// logged and dropped so a misconfiguration fails visibly rather than silently.
func (f *RunnerFactory) emitDeliver(backend HubBackend, d *DeliverRequest) {
	e := deliver.Event{
		Event:    d.Event,
		Channel:  d.Channel,
		ThreadID: d.ThreadID,
		Repo:     d.Repo,
		IssueID:  d.IssueID,
		PRID:     d.PRID,
		Action:   d.Action,
		Content:  d.Content,
	}
	// Hub backends explicitly request delivery, so a missing subscriber is a
	// misconfiguration worth surfacing (warnIfMissing=true).
	f.emitDeliverEvent(backend.Name(), e, true)
}

// emitBuiltinDeliver fans out a builtin task's completion as a deliver event
// (task 2.3.3). Unlike the hub path, the builtin runner produces no
// DeliverRequest, so we synthesize one from the task + Result. Delivery is
// optional: when no webhook subscriber is configured (deliver.webhook_url
// empty) this is a silent no-op, so builtin tasks never warn on every run.
func (f *RunnerFactory) emitBuiltinDeliver(task *store.Task, res *Result) {
	if f.deliverClient == nil {
		return
	}
	e := deliver.Event{
		Event:   deliver.EventTaskCompleted,
		Repo:    task.Repo,
		IssueID: task.IssueID,
		PRID:    task.PRID,
		Action:  res.Action,
		Content: res.Content,
	}
	f.emitDeliverEvent("builtin", e, false)
}

// emitDeliverEvent fans out a deliver.Event to the configured webhook (task
// 2.3.3). Best-effort: failure is logged, never fatal. When no deliver client
// is configured, warnIfMissing controls whether the drop is surfaced — hub
// backends explicitly request delivery (a missing subscriber is a
// misconfiguration worth surfacing), while the builtin path treats delivery
// as optional and stays silent.
func (f *RunnerFactory) emitDeliverEvent(source string, e deliver.Event, warnIfMissing bool) {
	if f.deliverClient == nil || !f.deliverClient.Enabled() {
		if warnIfMissing {
			log.Printf("[WARN] source %q produced a deliver event (event=%q channel=%q) but no deliver client is configured (deliver.webhook_url empty); dropping",
				source, e.Event, e.Channel)
		}
		return
	}
	if err := f.deliverClient.Emit(context.Background(), e); err != nil {
		log.Printf("[WARN] deliver emit failed (source=%q event=%q channel=%q): %v",
			source, e.Event, e.Channel, err)
		return
	}
	log.Printf("[INFO] deliver event %q fanned out to channel %q via webhook (source=%q)",
		e.Event, e.Channel, source)
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

// sessionLastHead returns the continuation anchor recorded on the task's
// session (B2.3): the previous task's pushed draft head. "" when the task has
// no session, the session is gone, or nothing has been recorded yet (fresh
// session → the hub branches from the base tip per the default contract).
func (f *RunnerFactory) sessionLastHead(task *store.Task) string {
	if f.db == nil || task.SessionID == "" {
		return ""
	}
	session, err := f.db.GetSession(task.SessionID)
	if err != nil {
		return ""
	}
	return session.LastHead
}

// loadSessionMemory returns the rolling per-session summary (B2.1 memory
// column) for injection into continuation prompts (B2.3). "" when absent.
func (f *RunnerFactory) loadSessionMemory(task *store.Task) string {
	if f.db == nil || task.SessionID == "" {
		return ""
	}
	session, err := f.db.GetSession(task.SessionID)
	if err != nil {
		return ""
	}
	return session.Memory
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
