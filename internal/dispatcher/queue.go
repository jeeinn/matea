package dispatcher

import (
	"log"
	"sync"
	"time"

	"github.com/jeeinn/matea/internal/store"
)

// TaskQueue is an in-memory task queue backed by SQLite for persistence.
type TaskQueue struct {
	mu     sync.Mutex
	ch     chan *store.Task
	db     *store.DB
	size   int
	stopCh chan struct{}
}

// NewTaskQueue creates a new task queue.
func NewTaskQueue(db *store.DB, size int) *TaskQueue {
	return &TaskQueue{
		ch:     make(chan *store.Task, size),
		db:     db,
		size:   size,
		stopCh: make(chan struct{}),
	}
}

// Enqueue adds a task to the queue and persists it to SQLite.
func (q *TaskQueue) Enqueue(task *store.Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Persist to DB
	if err := q.db.CreateTask(task); err != nil {
		return err
	}

	// Add to in-memory channel (non-blocking)
	select {
	case q.ch <- task:
		log.Printf("[INFO] Task enqueued: id=%d agent=%d type=%s", task.ID, task.AgentID, task.TaskType)
	default:
		log.Printf("[WARN] Task queue full, task %d will be picked up on restart", task.ID)
	}

	return nil
}

// Dequeue returns a channel that yields tasks.
func (q *TaskQueue) Dequeue() <-chan *store.Task {
	return q.ch
}

// push enqueues an already-persisted task onto the in-memory channel without
// touching the DB. Used by restart re-attach (ReattachHubHandles), which has
// already set the task's DB status to pending. If the channel is full, the
// pending task is still recoverable on the next scanner scan.
func (q *TaskQueue) push(t *store.Task) {
	select {
	case q.ch <- t:
		log.Printf("[INFO] Task re-enqueued for re-attach: id=%d agent=%d type=%s", t.ID, t.AgentID, t.TaskType)
	default:
		log.Printf("[WARN] Task queue full, re-attach task %d will be picked up by the next scanner scan", t.ID)
	}
}

// LoadPending loads pending tasks from DB into the queue on startup.
func (q *TaskQueue) LoadPending() error {
	tasks, err := q.db.ListPendingTasks()
	if err != nil {
		return err
	}

	for _, t := range tasks {
		select {
		case q.ch <- t:
		default:
			log.Printf("[WARN] Queue full, skipping pending task %d", t.ID)
		}
	}

	log.Printf("[INFO] Loaded %d pending tasks from DB", len(tasks))
	return nil
}

// StartScanner starts the background scanner for pending and stale tasks.
// - scanInterval: how often to scan for pending tasks (e.g., 60s)
// - staleThreshold: how long a task can be in "running" before being reset (e.g., 10min)
func (q *TaskQueue) StartScanner(scanInterval, staleThreshold time.Duration) {
	go func() {
		ticker := time.NewTicker(scanInterval)
		defer ticker.Stop()

		log.Printf("[INFO] Queue scanner started (interval=%v, staleThreshold=%v)", scanInterval, staleThreshold)

		for {
			select {
			case <-ticker.C:
				q.scanPendingTasks()
				q.resetStaleRunningTasks(staleThreshold)
			case <-q.stopCh:
				log.Printf("[INFO] Queue scanner stopped")
				return
			}
		}
	}()
}

// StopScanner stops the background scanner.
func (q *TaskQueue) StopScanner() {
	close(q.stopCh)
}

// scanPendingTasks loads pending tasks from DB into the queue.
func (q *TaskQueue) scanPendingTasks() {
	tasks, err := q.db.ListPendingTasks()
	if err != nil {
		log.Printf("[ERROR] Failed to scan pending tasks: %v", err)
		return
	}

	loaded := 0
	for _, t := range tasks {
		select {
		case q.ch <- t:
			loaded++
		default:
			// Queue full, skip
		}
	}

	if loaded > 0 {
		log.Printf("[INFO] Scanner loaded %d pending tasks", loaded)
	}
}

// resetStaleRunningTasks resets tasks that have been in "running" state too long,
// leaving hub tasks (those with a non-terminal handle) to their own poll loop so
// the Executor never spins up a duplicate in-process poll for the same Handle.
func (q *TaskQueue) resetStaleRunningTasks(threshold time.Duration) {
	count, err := q.db.ResetStaleRunningTasksExceptHub(threshold)
	if err != nil {
		log.Printf("[ERROR] Failed to reset stale tasks: %v", err)
		return
	}

	if count > 0 {
		log.Printf("[INFO] Reset %d stale running tasks", count)
	}
}
