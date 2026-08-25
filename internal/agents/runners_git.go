package agents

import (
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// resolveWorkBranch picks the branch to work on for session sync and checkout.
// Priority: task.BaseBranch (PR head) > session.Branch > empty (defer to branch plan).
func resolveWorkBranch(task *store.Task, sessionBranch string) string {
	if task != nil && strings.TrimSpace(task.BaseBranch) != "" {
		return strings.TrimSpace(task.BaseBranch)
	}
	if strings.TrimSpace(sessionBranch) != "" {
		return strings.TrimSpace(sessionBranch)
	}
	return ""
}

// resolveBranchPlan decides the working branch and whether it already exists.
func resolveBranchPlan(task *store.Task, sessionBranch string, git *sandbox.Git) (branchName string, isExisting bool) {
	if branch := resolveWorkBranch(task, sessionBranch); branch != "" {
		return branch, true
	}
	branchName = sandbox.GenerateBranchName(task.TaskType, task.IssueID)
	if git.LocalBranchExists(branchName) {
		return branchName, true
	}
	return branchName, false
}

// checkoutWorkBranch switches to branch, stashing local changes when switching away from dirty state.
func checkoutWorkBranch(sb *sandbox.Sandbox, git *sandbox.Git, audit *sandbox.AuditLogger, branch string) error {
	current, err := git.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}
	if current == branch {
		log.Printf("[INFO] Already on branch %s, skipping checkout", branch)
		return nil
	}

	stashed := false
	if git.HasChanges() {
		stashResult := git.Stash("gateway auto-stash before checkout")
		audit.LogCommand("git", []string{"stash", "push", "-u"}, stashResult)
		if stashResult.Error != nil {
			errMsg := stashResult.Stderr
			if errMsg == "" {
				errMsg = stashResult.Error.Error()
			}
			return fmt.Errorf("git stash: %s", errMsg)
		}
		if !strings.Contains(stashResult.Stdout, "No local changes to save") {
			stashed = true
		}
	}

	var checkoutResult *sandbox.Result
	if git.LocalBranchExists(branch) {
		checkoutResult = git.Checkout(branch)
		audit.LogCommand("git", []string{"checkout", branch}, checkoutResult)
	} else if git.RemoteBranchExists("origin", branch) {
		checkoutResult = sb.Execute("git", "checkout", "-b", branch, "origin/"+branch)
		audit.LogCommand("git", []string{"checkout", "-b", branch, "origin/" + branch}, checkoutResult)
	} else {
		return fmt.Errorf("branch %s not found locally or on remote", branch)
	}
	if checkoutResult.Error != nil {
		if stashed {
			_ = git.StashPop()
		}
		errMsg := checkoutResult.Stderr
		if errMsg == "" {
			errMsg = checkoutResult.Error.Error()
		}
		return fmt.Errorf("git checkout %s: %s", branch, errMsg)
	}

	if stashed {
		popResult := git.StashPop()
		audit.LogCommand("git", []string{"stash", "pop"}, popResult)
		if popResult.Error != nil {
			errMsg := popResult.Stderr
			if errMsg == "" {
				errMsg = popResult.Error.Error()
			}
			return fmt.Errorf("git stash pop: %s", errMsg)
		}
	}
	return nil
}

// prepareExistingBranch ensures the workspace is on an existing branch with latest remote when available.
func prepareExistingBranch(sb *sandbox.Sandbox, git *sandbox.Git, audit *sandbox.AuditLogger, branchName string) error {
	current, err := git.GetCurrentBranch()
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}
	if current == branchName {
		log.Printf("[INFO] Already on branch %s", branchName)
		if git.RemoteBranchExists("origin", branchName) {
			fetchResult := git.FetchBranch("origin", branchName)
			audit.LogCommand("git", []string{"fetch", "origin", branchName}, fetchResult)
			if fetchResult.Error != nil {
				errMsg := fetchResult.Stderr
				if errMsg == "" {
					errMsg = fetchResult.Error.Error()
				}
				return fmt.Errorf("git fetch %s: %s", branchName, errMsg)
			}
		}
		return nil
	}

	remoteExists := git.RemoteBranchExists("origin", branchName)
	if remoteExists {
		fetchResult := git.FetchBranch("origin", branchName)
		audit.LogCommand("git", []string{"fetch", "origin", branchName}, fetchResult)
		if fetchResult.Error != nil {
			errMsg := fetchResult.Stderr
			if errMsg == "" {
				errMsg = fetchResult.Error.Error()
			}
			return fmt.Errorf("git fetch %s: %s", branchName, errMsg)
		}
	}

	return checkoutWorkBranch(sb, git, audit, branchName)
}
