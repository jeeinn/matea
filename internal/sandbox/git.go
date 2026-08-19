package sandbox

import (
	"fmt"
	"log"
	"strings"
)

// Git provides Git operations within a sandbox workspace.
type Git struct {
	sandbox *Sandbox
}

// NewGit creates a new Git helper for the sandbox.
func NewGit(sandbox *Sandbox) *Git {
	return &Git{sandbox: sandbox}
}

// Clone clones a repository into the workspace.
func (g *Git) Clone(repoURL string) *Result {
	// Use shallow clone for efficiency
	result := g.sandbox.Execute("git", "clone", "--depth", "1", repoURL, ".")
	if result.Error != nil {
		return result
	}
	log.Printf("[INFO] Cloned repository into workspace")
	return result
}

// CloneBranch clones a specific branch of a repository.
func (g *Git) CloneBranch(repoURL, branch string) *Result {
	result := g.sandbox.Execute("git", "clone", "--depth", "1", "--branch", branch, repoURL, ".")
	if result.Error != nil {
		return result
	}
	log.Printf("[INFO] Cloned repository branch %s into workspace", branch)
	return result
}

// CloneFull clones the repository with full history into the workspace.
// Session continuation (B2.2) needs this: the anchor SHA recorded on the
// session (LastHead) lives on the session's draft branch, which a shallow
// clone of the default branch cannot reach.
func (g *Git) CloneFull(repoURL string) *Result {
	result := g.sandbox.Execute("git", "clone", repoURL, ".")
	if result.Error != nil {
		return result
	}
	log.Printf("[INFO] Cloned repository (full history) into workspace")
	return result
}

// HeadSHA returns the full SHA of the current HEAD, or "" on error.
func (g *Git) HeadSHA() string {
	res := g.sandbox.Execute("git", "rev-parse", "HEAD")
	if res.Error != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

// CreateBranch creates and switches to a new branch.
func (g *Git) CreateBranch(branch string) *Result {
	result := g.sandbox.Execute("git", "checkout", "-b", branch)
	if result.Error != nil {
		return result
	}
	log.Printf("[INFO] Created branch: %s", branch)
	return result
}

// Checkout switches to an existing branch.
func (g *Git) Checkout(branch string) *Result {
	return g.sandbox.Execute("git", "checkout", branch)
}

// Stash saves uncommitted changes (including untracked files).
func (g *Git) Stash(message string) *Result {
	return g.sandbox.Execute("git", "stash", "push", "-u", "-m", message)
}

// StashPop restores the most recent stash.
func (g *Git) StashPop() *Result {
	return g.sandbox.Execute("git", "stash", "pop")
}

// Add stages all changes.
func (g *Git) Add() *Result {
	return g.sandbox.Execute("git", "add", ".")
}

// AddFile stages a specific file.
func (g *Git) AddFile(path string) *Result {
	return g.sandbox.Execute("git", "add", path)
}

// Commit creates a commit with the given message.
func (g *Git) Commit(message string) *Result {
	return g.sandbox.Execute("git", "commit", "-m", message)
}

// ConfigUser sets the git identity (user.name / user.email) used for commits
// the gateway makes on the agent's behalf. Setting it up front avoids the
// first commit failing on a missing identity.
func (g *Git) ConfigUser(name, email string) *Result {
	if r := g.sandbox.Execute("git", "config", "user.name", name); r.Error != nil {
		return r
	}
	return g.sandbox.Execute("git", "config", "user.email", email)
}

// LocalBranchExists reports whether a local branch exists.
func (g *Git) LocalBranchExists(branch string) bool {
	result := g.sandbox.Execute("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return result.Error == nil
}

// RemoteBranchExists reports whether a branch exists on the remote.
func (g *Git) RemoteBranchExists(remote, branch string) bool {
	result := g.sandbox.Execute("git", "ls-remote", "--heads", remote, branch)
	if result.Error != nil {
		return false
	}
	return strings.TrimSpace(result.Stdout) != ""
}

// FetchRef fetches using an explicit refspec, avoiding configured remote fetch refspecs.
func (g *Git) FetchRef(remote, refspec string) *Result {
	return g.sandbox.Execute("git", "fetch", "--depth", "1", remote, refspec)
}

// FetchBranch fetches a single branch from remote without modifying remote config.
func (g *Git) FetchBranch(remote, branch string) *Result {
	refspec := fmt.Sprintf("refs/heads/%s:refs/remotes/%s/%s", branch, remote, branch)
	return g.FetchRef(remote, refspec)
}

// ResetFetchRefspecs restores the remote fetch config to the default wildcard refspec.
// Removes branch-specific refspecs left by `git remote set-branches --add`.
func (g *Git) ResetFetchRefspecs(remote string) *Result {
	key := fmt.Sprintf("remote.%s.fetch", remote)
	g.sandbox.Execute("git", "config", "--unset-all", key)
	return g.sandbox.Execute("git", "config", "--add", key,
		fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", remote))
}

// Push pushes the current branch to remote.
// Uses --force to handle retries (branch may already exist from a previous attempt).
func (g *Git) Push(remote, branch string) *Result {
	if g.RemoteBranchExists(remote, branch) {
		g.FetchBranch(remote, branch)
	}
	result := g.sandbox.Execute("git", "push", "--force-with-lease", remote, branch)
	if result.Error != nil {
		// Fallback to plain --force if --force-with-lease still fails
		result = g.sandbox.Execute("git", "push", "--force", remote, branch)
	}
	if result.Error != nil {
		return result
	}
	log.Printf("[INFO] Pushed to %s/%s", remote, branch)
	return result
}

// Status returns the git status.
func (g *Git) Status() *Result {
	return g.sandbox.Execute("git", "status", "--short")
}

// Diff returns the diff of unstaged changes.
func (g *Git) Diff() *Result {
	return g.sandbox.Execute("git", "diff")
}

// DiffStaged returns the diff of staged changes.
func (g *Git) DiffStaged() *Result {
	return g.sandbox.Execute("git", "diff", "--cached")
}

// DiffCachedStat returns staged diff statistics.
func (g *Git) DiffCachedStat() *Result {
	return g.sandbox.Execute("git", "diff", "--cached", "--stat")
}

// DiffCachedNameStatus returns staged file change list (M/A/D).
func (g *Git) DiffCachedNameStatus() *Result {
	return g.sandbox.Execute("git", "diff", "--cached", "--name-status")
}

// Log returns the git log.
func (g *Git) Log(count int) *Result {
	return g.sandbox.Execute("git", "log", fmt.Sprintf("-%d", count), "--oneline")
}

// GetCurrentBranch returns the current branch name.
func (g *Git) GetCurrentBranch() (string, error) {
	result := g.sandbox.Execute("git", "rev-parse", "--abbrev-ref", "HEAD")
	if result.Error != nil {
		return "", fmt.Errorf("get current branch: %w", result.Error)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// HasChanges checks if there are any uncommitted changes.
func (g *Git) HasChanges() bool {
	status := g.Status()
	return strings.TrimSpace(status.Stdout) != ""
}

// ValidateBranchName checks if a branch name is safe (must start with "ai/").
func ValidateBranchName(branch string) error {
	if !strings.HasPrefix(branch, "ai/") {
		return fmt.Errorf("branch must start with 'ai/', got: %s", branch)
	}
	// Check for dangerous characters
	if strings.ContainsAny(branch, " ;&|`$") {
		return fmt.Errorf("branch name contains dangerous characters: %s", branch)
	}
	return nil
}

// GenerateBranchName generates a safe branch name using issue number.
func GenerateBranchName(taskType string, issueID int) string {
	cleanType := strings.ReplaceAll(taskType, "_", "-")
	return fmt.Sprintf("ai/%s/issue-%d", cleanType, issueID)
}
