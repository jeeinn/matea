package dispatcher

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/workflow"
)

// Dispatcher orchestrates the event processing pipeline:
// WebhookEvent → EventResolver → Gate checks → WorkflowContext → TaskQueue.Enqueue → Executor.execute
type Dispatcher struct {
	queue     *TaskQueue
	executor  *Executor
	db        *store.DB
	giteaCfg  atomic.Pointer[config.GiteaConfig]
	agentsCfg *config.AgentsConfig

	// v2 components
	registry   *agents.Registry
	resolver   *workflow.Resolver
	wfMgr      *workflow.WorkflowManager
	l1Gate     *workflow.L1Gate
	sessionSvc *workflow.SessionService
	wfPolicy   *workflow.WorkflowPolicy
	lifecycle  *workflow.SessionLifecycle

	// In-flight lock: prevents concurrent tasks on the same (repo, issue)
	inFlight sync.Map // map[string]bool — key is "repo#issueID"
}

// NewDispatcher creates a new Dispatcher with all components wired together.
func NewDispatcher(
	db *store.DB,
	giteaCfg *config.GiteaConfig,
	dispatcherCfg *config.DispatcherConfig,
	llmRegistry *llm.Registry,
	agentsCfg *config.AgentsConfig,
	sandboxCfg sandbox.SandboxConfig,
	mcpCfg config.MCPConfig,
) *Dispatcher {
	queue := NewTaskQueue(db, dispatcherCfg.QueueSize)
	agentDefaults := config.DefaultAgentDefaults()
	if agentsCfg != nil {
		agentDefaults = agentsCfg.Defaults
		if agentDefaults.MaxOutputTokens <= 0 {
			agentDefaults.MaxOutputTokens = config.DefaultAgentDefaults().MaxOutputTokens
		}
		if agentDefaults.MaxInputTokens <= 0 {
			agentDefaults.MaxInputTokens = config.DefaultAgentDefaults().MaxInputTokens
		}
		if agentDefaults.Temperature <= 0 {
			agentDefaults.Temperature = config.DefaultAgentDefaults().Temperature
		}
		if agentDefaults.Timeout == "" {
			agentDefaults.Timeout = config.DefaultAgentDefaults().Timeout
		}
	}
	agentConcurrency := config.AgentConcurrencyParallel
	if dispatcherCfg != nil && dispatcherCfg.AgentConcurrency != "" {
		agentConcurrency = dispatcherCfg.AgentConcurrency
	}
	executor := NewExecutor(
		dispatcherCfg.MaxConcurrent,
		dispatcherCfg.TaskRetryCount,
		agentConcurrency,
		llmRegistry,
		db,
		agentDefaults,
		resolveDefaultLoop(agentsCfg),
		sandboxCfg,
		mcpCfg,
	)

	d := &Dispatcher{
		queue:     queue,
		executor:  executor,
		db:        db,
		agentsCfg: agentsCfg,
	}
	d.giteaCfg.Store(giteaCfg)

	// Wire up Gitea client factory for result writeback
	var backends *config.AgentBackendsConfig
	if agentsCfg != nil {
		backends = &agentsCfg.Backends
	}
	executor.SetGiteaClientFactory(d, nil, backends)

	return d
}

// SetDebugConfigGetter supplies live debug settings for conversation logging.
func (d *Dispatcher) SetDebugConfigGetter(getter func() config.DebugConfig) {
	if d.executor != nil && d.executor.giteaFactory != nil {
		var backends *config.AgentBackendsConfig
		if d.agentsCfg != nil {
			backends = &d.agentsCfg.Backends
		}
		d.executor.SetGiteaClientFactory(d.executor.giteaFactory, getter, backends)
	}
}

// SetModelMetaProvider sets the model metadata provider for adaptive token limits.
func (d *Dispatcher) SetModelMetaProvider(m agents.ModelMetaProvider) {
	if d.executor != nil {
		d.executor.SetModelMetaProvider(m)
	}
}

// SetGiteaConfig updates Gitea settings used for admin clients / writeback (hot reload).
func (d *Dispatcher) SetGiteaConfig(cfg *config.GiteaConfig) {
	if cfg == nil {
		return
	}
	d.giteaCfg.Store(cfg)
}

func resolveDefaultLoop(agentsCfg *config.AgentsConfig) config.AgentLoopConfig {
	if agentsCfg == nil {
		return config.DefaultAgentLoopConfig()
	}
	loop := agentsCfg.Loop
	if loop.MaxIterations <= 0 && loop.TotalTimeout == "" && loop.IterationInterval <= 0 {
		return config.DefaultAgentLoopConfig()
	}
	defaults := config.DefaultAgentLoopConfig()
	if loop.MaxIterations <= 0 {
		loop.MaxIterations = defaults.MaxIterations
	}
	if loop.TotalTimeout == "" {
		loop.TotalTimeout = defaults.TotalTimeout
	}
	return loop
}

// SetWorkflowComponents sets the v2 workflow components.
// When set, the dispatcher uses the new Assign-based pipeline instead of Router.Match.
func (d *Dispatcher) SetWorkflowComponents(registry *agents.Registry, resolver *workflow.Resolver, wfMgr *workflow.WorkflowManager, l1Gate *workflow.L1Gate, sessionSvc *workflow.SessionService, wfPolicy *workflow.WorkflowPolicy, lifecycle *workflow.SessionLifecycle) {
	d.registry = registry
	d.resolver = resolver
	d.wfMgr = wfMgr
	d.l1Gate = l1Gate
	d.sessionSvc = sessionSvc
	d.wfPolicy = wfPolicy
	d.lifecycle = lifecycle

	// Wire task completion callback for WorkflowContext + Session updates
	d.executor.SetOnComplete(func(task *store.Task) {
		if wfMgr == nil || task == nil {
			return
		}
		ctx, err := d.db.GetWorkflowContext(task.Repo, task.IssueID)
		if err != nil {
			log.Printf("[WARN] Failed to get workflow context for task %d completion: %v", task.ID, err)
			return
		}
		if err := wfMgr.OnTaskComplete(ctx, task.TaskType, task.PRID, task.SessionID); err != nil {
			log.Printf("[WARN] Failed to update workflow context after task %d: %v", task.ID, err)
		}
		// Update session state
		if sessionSvc != nil && task.SessionID != "" {
			if session, err := d.db.GetSession(task.SessionID); err == nil {
				// Extract branch from task result (for DevRunner PR creation)
				branch := session.Branch
				if err := sessionSvc.CompleteTask(session, task.ID, branch, ""); err != nil {
					log.Printf("[WARN] Failed to update session after task %d: %v", task.ID, err)
				}
			}
		}
		// L3 notification (if policy configured)
		d.postL3Notification(task)
		d.releaseIssueLock(task.Repo, task.IssueID)
	})

	// Wire task failure callback: rollback workflow stage + release in-flight lock
	d.executor.SetOnFailed(func(task *store.Task) {
		if task == nil {
			return
		}
		if wfMgr != nil {
			ctx, err := d.db.GetWorkflowContext(task.Repo, task.IssueID)
			if err != nil {
				log.Printf("[WARN] Failed to get workflow context for task %d failure: %v", task.ID, err)
			} else if err := wfMgr.OnTaskFailed(ctx, task.TaskType); err != nil {
				log.Printf("[WARN] Failed to rollback workflow context after task %d: %v", task.ID, err)
			}
		}
		d.releaseIssueLock(task.Repo, task.IssueID)
	})
}

// SetWorkflowPolicy replaces the live L2 policy (e.g. after WebUI config hot-reload).
func (d *Dispatcher) SetWorkflowPolicy(wfPolicy *workflow.WorkflowPolicy) {
	d.wfPolicy = wfPolicy
}

// Shutdown cancels in-flight executor work so agent loops can exit on process stop.
func (d *Dispatcher) Shutdown() {
	if d.executor != nil {
		d.executor.Shutdown()
	}
	if d.queue != nil {
		d.queue.StopScanner()
	}
}

// Start initializes the executor workers, loads pending tasks, and starts the queue scanner.
func (d *Dispatcher) Start() error {
	// Mark orphaned running tasks as failed (e.g. previous process killed with Ctrl+C)
	if n, err := d.db.FailOrphanedRunningTasks("matea restarted; interrupted running task"); err != nil {
		log.Printf("[WARN] Failed to clear orphaned running tasks: %v", err)
	} else if n > 0 {
		log.Printf("[INFO] Marked %d orphaned running task(s) as failed after restart", n)
	}

	// Load pending tasks from DB before starting workers
	if err := d.queue.LoadPending(); err != nil {
		return fmt.Errorf("load pending tasks: %w", err)
	}

	// Start executor workers
	d.executor.Start(d.queue)

	// Start queue scanner (scan every 60s, reset stale tasks after 10min)
	d.queue.StartScanner(60*time.Second, 10*time.Minute)

	log.Printf("[INFO] Dispatcher started")
	return nil
}

// HandleEvent processes an ingress Intent through the v2 pipeline.
// This is the callback function passed to giteaingress.Handler; the Gitea
// payload is decomposed from the Intent (1.3.1).
// Returns true for terminal outcomes (enqueued or intentionally skipped);
// false for transient failures that should remain accepted for ReplayAccepted.
func (d *Dispatcher) HandleEvent(intent *giteaingress.Intent) bool {
	evt := intent.Event
	log.Printf("[INFO] Processing event: %s/%s repo=%s sender=%s",
		evt.Event, evt.Action, evt.Repo.FullName, evt.Sender.Login)

	if d.resolver == nil {
		log.Printf("[WARN] No resolver configured, ignoring event")
		return true
	}

	return d.handleEventV2(evt)
}

func (d *Dispatcher) releaseIssueLock(repo string, issueID int) {
	lockKey := fmt.Sprintf("%s#%d", repo, issueID)
	d.inFlight.Delete(lockKey)
}

// ResetIssue cancels in-flight work for the issue, fails pending/running/partial
// tasks, archives sessions, resets workflow context to idle, and releases the
// in-flight lock so a new @mention can be accepted immediately.
func (d *Dispatcher) ResetIssue(repo string, issueID int) (tasksReset int, err error) {
	cancelled := 0
	if d.executor != nil {
		cancelled = d.executor.CancelByIssue(repo, issueID)
	}

	n, err := d.db.ResetTasksByIssue(repo, issueID, "workflow reset from web UI")
	if err != nil {
		return 0, err
	}

	d.resetIssue(repo, issueID)
	log.Printf("[INFO] ResetIssue %s#%d: cancelled_running=%d tasks_reset=%d", repo, issueID, cancelled, n)
	return n, nil
}

// CancelAndResetTask cancels a running task (if any), marks it failed in DB,
// and releases the issue in-flight lock.
func (d *Dispatcher) CancelAndResetTask(taskID int64, reason string) (*store.Task, error) {
	task, err := d.db.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	if d.executor != nil {
		d.executor.CancelTask(taskID)
	}
	updated, err := d.db.ResetTask(taskID, reason)
	if err != nil {
		return nil, err
	}
	d.releaseIssueLock(task.Repo, task.IssueID)
	log.Printf("[INFO] CancelAndResetTask %d on %s#%d", taskID, task.Repo, task.IssueID)
	return updated, nil
}

// GetGiteaClient creates a Gitea client using agent's token for writeback.
func (d *Dispatcher) GetGiteaClient(agentToken string) *gitea.Client {
	cfg := d.giteaCfg.Load()
	if cfg == nil {
		return gitea.NewClient("", agentToken)
	}
	return gitea.NewClient(cfg.URL, agentToken)
}

// GetAdminGiteaClient creates a Gitea client using admin token.
func (d *Dispatcher) GetAdminGiteaClient() *gitea.Client {
	cfg := d.giteaCfg.Load()
	if cfg == nil {
		return gitea.NewClient("", "")
	}
	return gitea.NewClient(cfg.URL, cfg.AdminToken)
}
