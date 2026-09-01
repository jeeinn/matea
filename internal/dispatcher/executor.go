package dispatcher

import (
	"context"
	"fmt"
	"log"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/deliver"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/mcp"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	"github.com/jeeinn/matea/internal/workflow"
)

// GiteaClientFactory creates Gitea clients for result writeback.
type GiteaClientFactory interface {
	GetGiteaClient(token string) *gitea.Client
	GetAdminGiteaClient() *gitea.Client
}

// TaskCompleteCallback is called after a task completes successfully.
type TaskCompleteCallback func(task *store.Task)

// TaskFailedCallback is called after a task fails (all retries exhausted).
type TaskFailedCallback func(task *store.Task)

// Executor runs agent tasks from the queue with concurrency control.
type Executor struct {
	maxConcurrent    int
	agentConcurrency string // parallel | serial_queue
	llmRegistry      *llm.Registry
	db               *store.DB
	sem              chan struct{}
	retryCount       int // task_retry_count: whole-task retries after runner failure
	giteaFactory     GiteaClientFactory

	// cfgMu guards the hot-reloadable agent configuration below (ReloadAgentsConfig
	// swaps them from the API goroutine while workers read them).
	cfgMu             sync.RWMutex
	runnerFactory     *agents.RunnerFactory
	agentDefaults     config.AgentDefaultsConfig
	defaultLoop       config.AgentLoopConfig
	getDebugConfig    func() config.DebugConfig
	modelMetaProvider agents.ModelMetaProvider

	sandboxCfg sandbox.SandboxConfig
	mcpCfg     config.MCPConfig
	deliverCfg config.DeliverConfig
	onComplete TaskCompleteCallback
	onFailed   TaskFailedCallback

	// rootCtx is cancelled on Shutdown so in-flight agent loops abort promptly.
	rootCtx    context.Context
	rootCancel context.CancelFunc

	// Per-task cancel registry for WebUI reset / abort.
	runMu   sync.Mutex
	running map[int64]*runningTask // taskID → cancel handle

	// sweepOnce guards the B4 deploy-key sweep loop so repeated
	// SetGiteaClientFactory calls (config reload) never start duplicates.
	sweepOnce sync.Once

	// serial_queue: in-process claim so two workers cannot start the same agent.
	agentMu   sync.Mutex
	agentBusy map[int64]bool
}

type runningTask struct {
	repo     string
	issueID  int
	cancel   context.CancelFunc
	external bool // true when cancelled via CancelTask / CancelByIssue (skip finalize)
}

// NewExecutor creates a new Executor.
func NewExecutor(maxConcurrent, retryCount int, agentConcurrency string, llmRegistry *llm.Registry, db *store.DB, agentDefaults config.AgentDefaultsConfig, defaultLoop config.AgentLoopConfig, sandboxCfg sandbox.SandboxConfig, mcpCfg config.MCPConfig) *Executor {
	if defaultLoop.MaxIterations <= 0 {
		defaultLoop = config.DefaultAgentLoopConfig()
	}
	if agentConcurrency != config.AgentConcurrencySerialQueue {
		agentConcurrency = config.AgentConcurrencyParallel
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	return &Executor{
		maxConcurrent:    maxConcurrent,
		agentConcurrency: agentConcurrency,
		llmRegistry:      llmRegistry,
		sandboxCfg:       sandboxCfg,
		db:               db,
		sem:              make(chan struct{}, maxConcurrent),
		retryCount:       retryCount,
		agentDefaults:    agentDefaults,
		defaultLoop:      defaultLoop,
		mcpCfg:           mcpCfg,
		rootCtx:          rootCtx,
		rootCancel:       rootCancel,
		running:          make(map[int64]*runningTask),
		agentBusy:        make(map[int64]bool),
	}
}

// Shutdown cancels in-flight task contexts (agent loops / LLM calls observe ctx.Done()).
func (e *Executor) Shutdown() {
	if e.rootCancel != nil {
		e.rootCancel()
	}
}

// ReattachHubHandles recovers hub tasks whose in-process poll loop was lost on
// restart. For each persisted non-terminal hub handle it loads the task and:
//   - task still running (orphaned by the crash) → reset to pending and enqueue,
//     so the Executor rebuilds the poll loop from the persisted Handle; runViaHub
//     reuses the Handle instead of re-submitting, and the backend re-attaches to
//     the still-living remote run (HubBackend contract §1.2.1).
//   - task pending → already loaded by LoadPending; skipped to avoid double-enqueue.
//   - task terminal → the run concluded without updating the handle; the handle
//     is reconciled (marked terminal) and cleaned up.
//
// Call this after LoadPending: orphaned-running hub tasks are the only set it
// enqueues, so a task is enqueued at most once (no duplicate poll). The stale
// scanner additionally excludes hub tasks, so no parallel reclaim occurs within
// a live process.
func (e *Executor) ReattachHubHandles(queue *TaskQueue) {
	if e.db == nil || queue == nil {
		return
	}
	handles, err := e.db.ListNonTerminalHubHandles()
	if err != nil {
		log.Printf("[WARN] ReattachHubHandles: list handles: %v", err)
		return
	}
	if len(handles) == 0 {
		return
	}

	var reattached, cleaned int
	for _, h := range handles {
		task, err := e.db.GetTask(h.TaskID)
		if err != nil {
			log.Printf("[WARN] ReattachHubHandles: load task %d: %v; marking handle terminal", h.TaskID, err)
			e.db.UpdateHubHandleStatus(h.TaskID, store.HubHandleStatusFailed)
			cleaned++
			continue
		}
		switch {
		case task.Status == store.StatusPending:
			// Already enqueued by LoadPending; re-attaching would double-run.
			continue
		case task.Status == store.StatusRunning:
			if err := e.db.UpdateTaskStatus(h.TaskID, store.StatusPending, "", ""); err != nil {
				log.Printf("[WARN] ReattachHubHandles: reset task %d: %v", h.TaskID, err)
				continue
			}
			queue.push(task)
			reattached++
		default:
			// Terminal task with a stale non-terminal handle: reconcile.
			status := store.HubHandleStatusFailed
			if task.Status == store.StatusSuccess || task.Status == store.StatusPartial {
				status = store.HubHandleStatusDone
			}
			e.db.UpdateHubHandleStatus(h.TaskID, status)
			cleaned++
		}
	}
	if reattached > 0 {
		log.Printf("[INFO] Reattached %d hub task(s) after restart (rebuilt poll loop from persisted Handle)", reattached)
	}
	if cleaned > 0 {
		log.Printf("[INFO] Reconciled %d stale hub handle(s) after restart", cleaned)
	}
}

// CancelTask cancels a single in-flight task context (if running).
// Returns true if a running cancel handle was found and invoked.
func (e *Executor) CancelTask(taskID int64) bool {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	rt, ok := e.running[taskID]
	if !ok || rt == nil {
		return false
	}
	rt.external = true
	if rt.cancel != nil {
		rt.cancel()
	}
	return true
}

// CancelByIssue cancels all in-flight tasks for repo#issueID.
// Returns the number of running tasks cancelled.
func (e *Executor) CancelByIssue(repo string, issueID int) int {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	n := 0
	for _, rt := range e.running {
		if rt == nil || rt.repo != repo || rt.issueID != issueID {
			continue
		}
		rt.external = true
		if rt.cancel != nil {
			rt.cancel()
		}
		n++
	}
	return n
}

func (e *Executor) registerRunning(task *store.Task, cancel context.CancelFunc) {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	e.running[task.ID] = &runningTask{
		repo:    task.Repo,
		issueID: task.IssueID,
		cancel:  cancel,
	}
}

// unregisterRunning removes the cancel handle and reports whether the task was
// cancelled externally (WebUI reset). Safe to call multiple times.
func (e *Executor) unregisterRunning(taskID int64) (external bool) {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	rt, ok := e.running[taskID]
	if !ok {
		return false
	}
	external = rt.external
	delete(e.running, taskID)
	return external
}

// SetOnComplete sets the callback for successful task completion.
func (e *Executor) SetOnComplete(cb TaskCompleteCallback) {
	e.onComplete = cb
}

// SetOnFailed sets the callback for failed task completion.
func (e *Executor) SetOnFailed(cb TaskFailedCallback) {
	e.onFailed = cb
}

// SetGiteaClientFactory sets the factory for creating Gitea clients.
func (e *Executor) SetGiteaClientFactory(factory GiteaClientFactory, getDebugConfig func() config.DebugConfig, backends *config.AgentBackendsConfig) {
	e.cfgMu.Lock()
	e.giteaFactory = factory
	e.getDebugConfig = getDebugConfig
	e.rebuildRunnerFactoryLocked(backends)
	e.cfgMu.Unlock()
	e.startDeployKeySweepLoop(10 * time.Minute)
}

// rebuildRunnerFactoryLocked constructs a fresh RunnerFactory from the current
// executor state and swaps it in. Callers must hold cfgMu. In-flight tasks keep
// the factory they started with; new tasks pick up the new one. Hub backend
// singletons are (re)registered inside NewRunnerFactory, so a backends config
// change fully takes effect here without a restart.
func (e *Executor) rebuildRunnerFactoryLocked(backends *config.AgentBackendsConfig) {
	mcpReg := mcp.NewRegistry(e.mcpCfg)
	gatewayDir, _ := os.Getwd()
	rf := agents.NewRunnerFactory(e.llmRegistry, e.giteaFactory, e.db, e.agentDefaults, e.defaultLoop, e.getDebugConfig, backends, nil, e.sandboxCfg, mcpReg, gatewayDir)
	// (Re)inject the outbound deliver client whenever the runner factory is
	// rebuilt (task 2.3.3). A disabled config (empty webhook_url) yields a
	// no-op client.
	rf.SetDeliverClient(buildDeliverClient(e.deliverCfg))
	if e.modelMetaProvider != nil {
		rf.SetModelMetaProvider(e.modelMetaProvider)
	}
	// git_sync (task A6): the deploy key issuer rides on the admin client's
	// token (write:repository scope suffices per the A0.2 spike — no extra
	// credential is introduced, and the hub never sees this token).
	if e.giteaFactory != nil {
		if admin := e.giteaFactory.GetAdminGiteaClient(); admin != nil {
			rf.SetDeployKeyIssuer(agents.NewGiteaDeployKeyIssuer(admin))
		}
	}
	e.runnerFactory = rf
}

// ReloadAgentsConfig hot-swaps agent defaults / loop config / coding backends
// without a restart (config hot-reload path). The runner factory is rebuilt so
// newly added hub backends resolve immediately.
func (e *Executor) ReloadAgentsConfig(defaults config.AgentDefaultsConfig, loop config.AgentLoopConfig, backends *config.AgentBackendsConfig) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.agentDefaults = defaults
	if loop.MaxIterations <= 0 {
		loop = config.DefaultAgentLoopConfig()
	}
	e.defaultLoop = loop
	e.rebuildRunnerFactoryLocked(backends)
}

// getRunnerFactory returns the current runner factory (nil before the first
// SetGiteaClientFactory).
func (e *Executor) getRunnerFactory() *agents.RunnerFactory {
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	return e.runnerFactory
}

// getAgentConfig returns the current agent defaults / loop config for workers.
func (e *Executor) getAgentConfig() (config.AgentDefaultsConfig, config.AgentLoopConfig) {
	e.cfgMu.RLock()
	defer e.cfgMu.RUnlock()
	return e.agentDefaults, e.defaultLoop
}

// startDeployKeySweepLoop runs the B4 deploy-key lifecycle hook: an immediate
// sweep at startup (catches crash-window leaks promptly), then periodically.
// Each tick re-resolves the admin client from the live factory so config
// reloads take effect without restarting the loop. Guarded by sweepOnce —
// SetGiteaClientFactory is re-invoked on config reload and must not stack
// loops. The cadence mirrors the session cleanup loop (10 minutes).
func (e *Executor) startDeployKeySweepLoop(interval time.Duration) {
	e.sweepOnce.Do(func() {
		go func() {
			sweep := func() {
				if e.giteaFactory == nil || e.db == nil {
					return
				}
				admin := e.giteaFactory.GetAdminGiteaClient()
				if admin == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				n, err := agents.SweepOrphanedDeployKeys(ctx, e.db, admin,
					agents.NewGiteaDeployKeyIssuer(admin), agents.DeployKeySweepGrace, time.Now())
				if err != nil {
					log.Printf("[WARN] deploy key sweep error: %v", err)
				} else if n > 0 {
					log.Printf("[INFO] deploy key sweep: revoked %d orphaned key(s)", n)
				}
			}
			sweep() // startup pass
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				sweep()
			}
		}()
		log.Printf("[INFO] Deploy key sweep loop started (interval: %s)", interval)
	})
}

// SetDeliverConfig updates the outbound deliver configuration. The new client
// is applied immediately to the live runner factory.
func (e *Executor) SetDeliverConfig(cfg config.DeliverConfig) {
	e.cfgMu.Lock()
	e.deliverCfg = cfg
	rf := e.runnerFactory
	e.cfgMu.Unlock()
	if rf != nil {
		rf.SetDeliverClient(buildDeliverClient(cfg))
	}
}

// buildDeliverClient converts the parsed deliver config into a deliver.Client,
// resolving the timeout string with a sane default.
func buildDeliverClient(cfg config.DeliverConfig) *deliver.Client {
	timeout := deliver.DefaultTimeout
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		} else {
			log.Printf("[WARN] deliver.timeout %q invalid; using default %s", cfg.Timeout, deliver.DefaultTimeout)
		}
	}
	return deliver.New(deliver.Config{
		WebhookURL: cfg.WebhookURL,
		Timeout:    timeout,
		MaxRetries: cfg.MaxRetries,
	})
}

// SetModelMetaProvider sets the model metadata provider for adaptive token limits.
func (e *Executor) SetModelMetaProvider(m agents.ModelMetaProvider) {
	e.cfgMu.Lock()
	e.modelMetaProvider = m
	rf := e.runnerFactory
	e.cfgMu.Unlock()
	if rf != nil {
		rf.SetModelMetaProvider(m)
	}
}

// Start begins the executor workers.
func (e *Executor) Start(queue *TaskQueue) {
	for i := 0; i < e.maxConcurrent; i++ {
		go e.worker(queue)
	}
	log.Printf("[INFO] Executor started with %d workers (agent_concurrency=%s)", e.maxConcurrent, e.agentConcurrency)
}

func (e *Executor) worker(queue *TaskQueue) {
	for task := range queue.Dequeue() {
		if !e.tryAcquireAgentSlot(task) {
			log.Printf("[INFO] Task %d deferred (agent_id=%d serial_queue busy); stays pending", task.ID, task.AgentID)
			continue
		}
		e.sem <- struct{}{} // acquire
		e.executeSafely(task)
		<-e.sem // release
		e.releaseAgentSlot(task.AgentID)
		e.kickNextForAgent(queue, task.AgentID)
	}
}

// tryAcquireAgentSlot claims the agent for serial_queue mode.
// In parallel mode always returns true.
func (e *Executor) tryAcquireAgentSlot(task *store.Task) bool {
	if e.agentConcurrency != config.AgentConcurrencySerialQueue || task == nil {
		return true
	}
	e.agentMu.Lock()
	defer e.agentMu.Unlock()
	if e.agentBusy[task.AgentID] {
		return false
	}
	if e.db != nil {
		busy, err := e.db.HasRunningTaskForAgent(task.AgentID, task.ID)
		if err != nil {
			log.Printf("[WARN] HasRunningTaskForAgent agent=%d: %v; deferring task %d", task.AgentID, err, task.ID)
			return false
		}
		if busy {
			return false
		}
	}
	e.agentBusy[task.AgentID] = true
	return true
}

func (e *Executor) releaseAgentSlot(agentID int64) {
	if e.agentConcurrency != config.AgentConcurrencySerialQueue {
		return
	}
	e.agentMu.Lock()
	delete(e.agentBusy, agentID)
	e.agentMu.Unlock()
}

// kickNextForAgent pushes the next pending task for this agent onto the queue channel.
func (e *Executor) kickNextForAgent(queue *TaskQueue, agentID int64) {
	if e.agentConcurrency != config.AgentConcurrencySerialQueue || e.db == nil || queue == nil {
		return
	}
	next, err := e.db.NextPendingTaskForAgent(agentID)
	if err != nil {
		log.Printf("[WARN] NextPendingTaskForAgent agent=%d: %v", agentID, err)
		return
	}
	if next == nil {
		return
	}
	select {
	case queue.ch <- next:
		log.Printf("[INFO] serial_queue woke pending task %d for agent_id=%d", next.ID, agentID)
	default:
		// Scanner will pick it up; channel full is non-fatal.
	}
}

// executeSafely runs execute and converts panics into a failed task without crashing the worker.
func (e *Executor) executeSafely(task *store.Task) {
	defer func() {
		if r := recover(); r != nil {
			e.handleTaskPanic(task, r)
		}
	}()
	e.execute(task)
}

func (e *Executor) handleTaskPanic(task *store.Task, recovered any) {
	e.unregisterRunning(task.ID)
	log.Printf("[ERROR] Task %d panicked: %v\n%s", task.ID, recovered, debug.Stack())

	err := fmt.Errorf("task panicked: %v", recovered)
	finished := time.Now()
	task.FinishedAt = &finished
	task.Status = "failed"
	task.Error = err.Error()
	e.db.UpdateTaskStatus(task.ID, "failed", "", task.Error)

	if writeErr := e.writeFailureToGitea(task, err); writeErr != nil {
		log.Printf("[ERROR] Task %d failure writeback failed: %v", task.ID, writeErr)
	}
	if e.onFailed != nil {
		e.onFailed(task)
	}
}

func (e *Executor) execute(task *store.Task) {
	log.Printf("[INFO] Executing task: id=%d agent=%d type=%s", task.ID, task.AgentID, task.TaskType)

	// Skip tasks already terminal (e.g. reset while still queued).
	if fresh, err := e.db.GetTask(task.ID); err == nil {
		if fresh.Status != store.StatusPending && fresh.Status != store.StatusRunning {
			log.Printf("[INFO] Task %d skipped (status=%s)", task.ID, fresh.Status)
			return
		}
	}

	// Mark as running
	now := time.Now()
	task.Status = "running"
	task.StartedAt = &now
	e.db.UpdateTaskStatus(task.ID, "running", "", "")

	// Load agent first so timeout can be resolved per agent/task type
	agent, err := e.db.GetAgent(task.AgentID)
	if err != nil {
		e.finalizeTaskResult(task, fmt.Errorf("load agent: %w", err))
		return
	}

	timeout := e.resolveTaskTimeout(task.TaskType, agent)
	// Shared across task retries; cancelled on Executor.Shutdown (Ctrl+C / SIGTERM)
	// or per-task CancelTask / CancelByIssue (WebUI reset).
	parent := e.rootCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	e.registerRunning(task, cancel)
	defer cancel()

	for attempt := 0; attempt <= e.retryCount; attempt++ {
		if attempt > 0 {
			if ctx.Err() != nil {
				err = fmt.Errorf("task cancelled before retry: %w", ctx.Err())
				break
			}
			log.Printf("[INFO] Retrying whole task %d (task_retry %d/%d)", task.ID, attempt, e.retryCount)
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = fmt.Errorf("task cancelled during retry backoff: %w", ctx.Err())
			case <-timer.C:
			}
			if ctx.Err() != nil {
				break
			}
		}

		err = e.runTask(ctx, task, agent)
		if err == nil {
			break
		}
		// Do not burn task retries on intentional cancellation / deadline.
		if ctx.Err() != nil {
			break
		}
	}

	if e.unregisterRunning(task.ID) {
		log.Printf("[INFO] Task %d aborted by reset; skipping finalize", task.ID)
		return
	}
	e.finalizeTaskResult(task, err)
}

// finalizeTaskResult records the terminal task status, attempts Gitea writeback,
// and fires the appropriate callback. Extracted from execute() so the
// success/partial/failed branching is unit-testable without spinning up a runner.
//
// Status rules:
//   - runErr != nil                       -> failed  (existing behavior)
//   - runErr == nil, writeback succeeds   -> success (existing behavior)
//   - runErr == nil, writeback fails      -> partial (NEW: previously silent success)
//   - runErr == nil, writeback skipped    -> partial (NEW: e.g. no factory, empty result, no target)
//
// For partial, task.Result is still persisted so a human can inspect what the
// runner produced; task.Error carries the writeback error; onFailed (not
// onComplete) fires so the workflow does not advance past a failed delivery.
//
// Ordering: callbacks (onComplete/onFailed) and writeback run BEFORE the
// terminal status is persisted. Observers that poll for status==success/failed
// can therefore rely on workflow / session state already being consistent.
func (e *Executor) finalizeTaskResult(task *store.Task, runErr error) {
	finished := time.Now()
	task.FinishedAt = &finished

	if runErr != nil {
		// Failure path: write failure comment + fire onFailed, then persist
		// status=failed so the workflow rollback is observable by the time
		// external watchers see the status flip.
		task.Status = store.StatusFailed
		task.Error = runErr.Error()
		log.Printf("[ERROR] Task %d failed: %v", task.ID, runErr)
		if writeErr := e.writeFailureToGitea(task, runErr); writeErr != nil {
			log.Printf("[ERROR] Task %d failure writeback failed: %v", task.ID, writeErr)
		}
		if e.onFailed != nil {
			e.onFailed(task)
		}
		e.db.UpdateTaskStatus(task.ID, store.StatusFailed, "", task.Error)
		return
	}

	// Runner succeeded — attempt writeback BEFORE committing success so that
	// a writeback failure is observable in task.Status / task.Error instead of
	// being silently swallowed (P0.1: 写回可靠性).
	if writeErr := e.writeBackToGitea(task); writeErr != nil {
		wbErr := fmt.Errorf("writeback failed: %w", writeErr)
		task.Status = store.StatusPartial
		task.Error = wbErr.Error()
		log.Printf("[ERROR] Task %d writeback failed (marked partial): %v", task.ID, writeErr)
		// Best-effort notice via admin token — the agent token may be the culprit.
		if commentErr := e.writePartialFailureComment(task, wbErr); commentErr != nil {
			log.Printf("[ERROR] Task %d partial-failure comment also failed: %v", task.ID, commentErr)
		}
		// Treat as failure from workflow's perspective: do not advance (no onComplete),
		// release locks / rollback stage so the issue can accept a manual retry.
		if e.onFailed != nil {
			e.onFailed(task)
		}
		// Keep task.Result in DB so a human can inspect what the runner produced.
		e.db.UpdateTaskStatus(task.ID, store.StatusPartial, task.Result, task.Error)
		return
	}

	// Success path: fire onComplete before persisting so workflow/session state
	// is consistent by the time observers see status=success (WaitForTask polls
	// task.Status; without this ordering the test could observe success before
	// OnTaskComplete / session completion had run).
	task.Status = store.StatusSuccess
	log.Printf("[INFO] Task %d completed successfully", task.ID)
	if e.onComplete != nil {
		e.onComplete(task)
	}
	e.db.UpdateTaskStatus(task.ID, store.StatusSuccess, task.Result, "")
}

func (e *Executor) resolveTaskTimeout(taskType string, agent *store.Agent) time.Duration {
	agentDefaults, defaultLoop := e.getAgentConfig()
	if isLoopTask(taskType) {
		merged := agents.MergeLoopConfig(agent.LoopConfig, defaultLoop)
		if d, err := time.ParseDuration(merged.TotalTimeout); err == nil && d > 0 {
			return d
		}
		if d, err := time.ParseDuration(defaultLoop.TotalTimeout); err == nil && d > 0 {
			return d
		}
		return 30 * time.Minute
	}

	timeoutStr := agent.Timeout
	if timeoutStr == "" {
		timeoutStr = agentDefaults.Timeout
	}
	if d, err := time.ParseDuration(timeoutStr); err == nil && d > 0 {
		return d
	}
	return 20 * time.Minute
}

func isLoopTask(taskType string) bool {
	switch taskType {
	case "solve_issue", "solve_comment", "fix_bug":
		return true
	default:
		return false
	}
}

func (e *Executor) runTask(ctx context.Context, task *store.Task, agent *store.Agent) error {
	runner := e.getRunnerFactory().GetRunner(task.TaskType)

	result, err := runner.Run(ctx, task, agent)
	if err != nil {
		return fmt.Errorf("runner execution: %w", err)
	}

	task.Result = result.Content
	if result.PRID > 0 {
		task.PRID = result.PRID
		log.Printf("[INFO] Task %d created PR #%d", task.ID, result.PRID)
	}
	log.Printf("[INFO] Task %d completed, action=%s", task.ID, result.Action)
	return nil
}

// writebackTargetID returns the Gitea issue/PR index to post comments on: the
// PR whenever the task belongs to a PR conversation, because that is where the
// user asked.
//
// It used to prefer IssueID for everything except review_pr. That stopped
// being right when the resolver learned to read "Refs #N": a task on PR #8 is
// now keyed to issue #7 (keeps the session and the coder's continuation anchor
// continuous), so a write task answering on the PR would have posted its
// result on the issue instead. conversationTarget keeps the two concerns apart.
func writebackTargetID(task *store.Task) (targetID int, ok bool) {
	if task == nil {
		return 0, false
	}
	id := conversationTarget(task.IssueID, task.PRID)
	if id <= 0 {
		return 0, false
	}
	return id, true
}

// writeBackToGitea posts the LLM result as a comment on the Gitea issue/PR.
func (e *Executor) writeBackToGitea(task *store.Task) error {
	if e.giteaFactory == nil {
		return fmt.Errorf("no Gitea factory configured, writeback skipped")
	}

	if task.Result == "" {
		return fmt.Errorf("no result to write back")
	}

	targetID, ok := writebackTargetID(task)
	if !ok {
		return fmt.Errorf("no issue/PR target for writeback")
	}

	agent, err := e.db.GetAgent(task.AgentID)
	if err != nil {
		return fmt.Errorf("load agent for writeback: %w", err)
	}

	client := e.giteaFactory.GetGiteaClient(agent.GiteaToken)

	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	commentBody := formatComment(task, agentLabel(agent))

	if err := client.IssueComment(owner, repo, targetID, commentBody); err != nil {
		return fmt.Errorf("post comment: %w", err)
	}

	log.Printf("[INFO] Task %d result written back to %s (target #%d)", task.ID, task.Repo, targetID)
	return nil
}

// writeFailureToGitea posts task failure details to the linked Gitea issue/PR.
func (e *Executor) writeFailureToGitea(task *store.Task, taskErr error) error {
	if e.giteaFactory == nil {
		log.Printf("[DEBUG] No Gitea factory configured, skipping failure writeback for task %d", task.ID)
		return nil
	}
	if taskErr == nil {
		return nil
	}

	targetID, ok := writebackTargetID(task)
	if !ok {
		log.Printf("[DEBUG] No issue/PR target for task %d, skipping failure writeback", task.ID)
		return nil
	}

	agent, err := e.db.GetAgent(task.AgentID)
	if err != nil {
		return fmt.Errorf("load agent for failure writeback: %w", err)
	}

	// Fold the failure into the task's status card: the card is the single
	// place a task's outcome lives, so a failed task leaves one trace instead
	// of a stale "已开始处理" comment plus a separate failure comment.
	//
	// A failure cause is exactly the information a user must not miss, so if
	// the card cannot be written the classic failure comment is still posted.
	if err := failStatusCard(e.giteaFactory, e.db, agent, task, targetID, taskErr.Error()); err != nil {
		log.Printf("[WARN] Task %d status card not updated (%v); falling back to a plain failure comment", task.ID, err)
		client := e.giteaFactory.GetGiteaClient(agent.GiteaToken)
		parts := strings.SplitN(task.Repo, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid repo format: %s", task.Repo)
		}
		commentBody := workflow.FormatAgentComment(formatFailureComment(task, taskErr, agentLabel(agent)))
		if err := client.IssueComment(parts[0], parts[1], targetID, commentBody); err != nil {
			return fmt.Errorf("post failure comment: %w", err)
		}
	}

	log.Printf("[INFO] Task %d failure written back to %s#%d", task.ID, task.Repo, targetID)
	return nil
}

// writePartialFailureComment posts a "writeback failed" notice to the Gitea
// issue/PR using the admin client. This is best-effort: if the agent token was
// the cause of the writeback failure, the admin token may still be able to
// deliver a minimal notice so the user is not left without any signal.
func (e *Executor) writePartialFailureComment(task *store.Task, wbErr error) error {
	if e.giteaFactory == nil {
		return nil
	}
	targetID, ok := writebackTargetID(task)
	if !ok {
		return nil
	}
	client := e.giteaFactory.GetAdminGiteaClient()
	if client == nil {
		return nil
	}
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]
	agentName := "unknown"
	if a, aerr := e.db.GetAgent(task.AgentID); aerr == nil {
		agentName = agentLabel(a)
	}
	body := workflow.FormatAgentComment(formatPartialFailureComment(task, wbErr, agentName))
	if err := client.IssueComment(owner, repo, targetID, body); err != nil {
		return fmt.Errorf("post partial failure comment: %w", err)
	}
	log.Printf("[INFO] Task %d partial-failure notice posted to %s#%d", task.ID, task.Repo, targetID)
	return nil
}

// agentLabel renders an agent for a comment footer. The Gitea username wins
// because it is the identity the reader actually sees on the issue; the
// internal name is the fallback for agents with no linked account. The raw
// AgentID was printed here before — a database row id that means nothing to
// anyone reading the comment.
func agentLabel(agent *store.Agent) string {
	switch {
	case agent == nil:
		return "unknown"
	case agent.GiteaUsername != "":
		return "@" + agent.GiteaUsername
	case agent.Name != "":
		return agent.Name
	default:
		return fmt.Sprintf("id=%d", agent.ID)
	}
}

// commentFooter renders the trailing attribution line of an agent comment.
func commentFooter(task *store.Task, agentName string) string {
	return fmt.Sprintf("*Task ID: %d | Agent: %s | Type: %s*", task.ID, agentName, task.TaskType)
}

func formatPartialFailureComment(task *store.Task, wbErr error, agentName string) string {
	var sb strings.Builder
	sb.WriteString("⚠️ **任务已执行但写回失败**\n\n")
	sb.WriteString("Agent 已完成计算，但结果未能成功评论到此 Issue/PR。可在任务列表查看完整结果（状态：部分完成）。\n\n")
	sb.WriteString("**写回错误：**\n")
	sb.WriteString("```\n")
	sb.WriteString(strings.TrimSpace(wbErr.Error()))
	sb.WriteString("\n```\n\n")
	sb.WriteString("---\n")
	sb.WriteString(commentFooter(task, agentName))
	return sb.String()
}

func formatFailureComment(task *store.Task, taskErr error, agentName string) string {
	var sb strings.Builder
	sb.WriteString("❌ **任务执行失败**\n\n")
	sb.WriteString("**错误原因：**\n")
	sb.WriteString("```\n")
	sb.WriteString(strings.TrimSpace(taskErr.Error()))
	sb.WriteString("\n```\n\n")
	sb.WriteString("---\n")
	sb.WriteString(commentFooter(task, agentName))
	return sb.String()
}

// formatComment formats the LLM result as a Gitea comment.
func formatComment(task *store.Task, agentName string) string {
	var sb strings.Builder

	sb.WriteString("🤖 **AI Agent Response**\n\n")
	sb.WriteString(task.Result)
	sb.WriteString("\n\n---\n")
	sb.WriteString(commentFooter(task, agentName))

	return sb.String()
}
