package agents

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// B4: deploy-key lifecycle hook (20260815-git-sync-3phase-plan.md v3.1).
//
// runViaHub revokes the task-scoped deploy key at every terminal state, but
// three leak windows remain:
//  1. Revoke itself can fail (network/Gitea error after 3 retries) — the key
//     stays on the repo while the handle row goes terminal.
//  2. A crash between Prepare (key issued) and SaveHubHandle (key id
//     persisted) leaves a key no row points at.
//  3. Any future handle-row deletion must not strand its key either —
//     "deploy key 随 hub_handle 删除/失效回收".
//
// SweepOrphanedDeployKeys is the backstop: per repo that ever ran a git_sync
// task, list the deploy keys and revoke every matea-issued one whose task has
// no non-terminal handle row, subject to a grace period that covers the
// Prepare→persist race (a freshly issued key whose row is not yet written
// must never be swept from under an in-flight Submit).

// DeployKeyTitlePrefix namespaces every deploy key Matea issues (Prepare
// titles keys "<prefix><taskID>"); the sweep only touches keys with this
// prefix — operator-managed keys on the same repo are never revoked.
const DeployKeyTitlePrefix = "matea-hub-task-"

// DeployKeySweepGrace is how young a matea-issued key must be to be exempt
// from the sweep. Prepare→Submit→persist completes in milliseconds; 30
// minutes makes the race essentially impossible while still bounding any
// leak's lifetime to one grace period plus one sweep interval.
const DeployKeySweepGrace = 30 * time.Minute

// DeployKeyTaskID parses a matea-issued deploy key title back into its task
// ID. ok=false for foreign (non-Matea) titles.
func DeployKeyTaskID(title string) (int64, bool) {
	if !strings.HasPrefix(title, DeployKeyTitlePrefix) {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(title, DeployKeyTitlePrefix), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// SweepOrphanedDeployKeys revokes orphaned matea-issued deploy keys across
// all repos that have hub handle rows, returning the number revoked. Per-repo
// failures are logged and skipped so one broken repo never blocks the rest of
// the sweep; a DB failure aborts (the protected set is load-bearing).
//
// A key is revoked only when ALL hold:
//   - its title parses as matea-hub-task-<id> (Matea-issued);
//   - that task has NO non-terminal hub handle row on the repo (not in
//     flight, not re-attaching after a restart);
//   - the key is older than grace (Prepare→persist race window).
//
// Every revocation is audited to operation_logs (action
// "git_sync_key_swept"). Revoke is idempotent server-side, so re-sweeping an
// already-revoked id is safe.
func SweepOrphanedDeployKeys(ctx context.Context, db *store.DB, client *gitea.Client, issuer DeployKeyIssuer, grace time.Duration, now time.Time) (int, error) {
	if db == nil || client == nil || issuer == nil {
		return 0, fmt.Errorf("git_sync key sweep: db, gitea client and issuer are all required")
	}
	repos, err := db.ListHubHandleRepos()
	if err != nil {
		return 0, fmt.Errorf("git_sync key sweep: %w", err)
	}
	revoked := 0
	for _, full := range repos {
		owner, repo := splitOwnerRepo(full)
		protectedIDs, err := db.ListNonTerminalHubTaskIDsByRepo(full)
		if err != nil {
			return revoked, fmt.Errorf("git_sync key sweep: protected set for %s: %w", full, err)
		}
		protected := make(map[int64]bool, len(protectedIDs))
		for _, id := range protectedIDs {
			protected[id] = true
		}

		keys, err := client.ListDeployKeys(owner, repo)
		if err != nil {
			log.Printf("[WARN] git_sync key sweep: list deploy keys for %s failed (skipping repo): %v", full, err)
			continue
		}
		for _, k := range keys {
			taskID, ok := DeployKeyTaskID(k.Title)
			if !ok || protected[taskID] {
				continue
			}
			if !k.CreatedAt.IsZero() && now.Sub(k.CreatedAt) < grace {
				continue // inside the Prepare→persist race window
			}
			if err := issuer.Revoke(ctx, owner, repo, k.ID); err != nil {
				log.Printf("[WARN] git_sync key sweep: revoke key %d (%q) on %s failed: %v", k.ID, k.Title, full, err)
				continue
			}
			revoked++
			db.LogOperation(0, taskID, "git_sync_key_swept",
				fmt.Sprintf("repo=%s key_id=%d title=%q", full, k.ID, k.Title))
			log.Printf("[INFO] git_sync key sweep: revoked orphaned deploy key %d (%q) on %s", k.ID, k.Title, full)
		}
	}
	return revoked, nil
}
