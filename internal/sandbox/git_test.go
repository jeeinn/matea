package sandbox

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitOperations(t *testing.T) {
	cfg := SandboxConfig{
		Mode:           ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      1024,
		MaxFileSize:    1024,
	}

	s := New(cfg, 1001)
	err := s.Setup()
	require.NoError(t, err)
	defer s.Cleanup()

	git := NewGit(s)

	// Initialize a git repo
	result := s.Execute("git", "init")
	require.NoError(t, result.Error)

	// Configure git user
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")

	// Create a file
	err = s.WriteFile("test.txt", []byte("initial content"))
	require.NoError(t, err)

	// Stage
	result = git.Add()
	require.NoError(t, result.Error)

	// Commit
	result = git.Commit("initial commit")
	if result.Error != nil {
		t.Logf("Commit stderr: %s", result.Stderr)
		t.Logf("Commit stdout: %s", result.Stdout)
		require.NoError(t, result.Error)
	}

	// Check status - should be clean
	result = git.Status()
	require.NoError(t, result.Error)
	assert.Empty(t, result.Stdout)
}

func TestGitCreateBranch(t *testing.T) {
	cfg := SandboxConfig{
		Mode:           ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      1024,
		MaxFileSize:    1024,
	}

	s := New(cfg, 1002)
	err := s.Setup()
	require.NoError(t, err)
	defer s.Cleanup()

	git := NewGit(s)

	// Initialize a git repo
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	err = s.WriteFile("test.txt", []byte("content"))
	require.NoError(t, err)
	git.Add()
	git.Commit("initial")

	// Create branch
	result := git.CreateBranch("matea/test/task-1002")
	require.NoError(t, result.Error)

	// Get current branch
	branch, err := git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "matea/test/task-1002", branch)
}

func TestGitHasChanges(t *testing.T) {
	cfg := SandboxConfig{
		Mode:           ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      1024,
		MaxFileSize:    1024,
	}

	s := New(cfg, 1003)
	err := s.Setup()
	require.NoError(t, err)
	defer s.Cleanup()

	git := NewGit(s)

	// Initialize a git repo
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	err = s.WriteFile("test.txt", []byte("content"))
	require.NoError(t, err)
	git.Add()
	git.Commit("initial")

	// Should not have changes
	assert.False(t, git.HasChanges(), "Should not have changes after commit")

	// Add a new file
	err = s.WriteFile("new.txt", []byte("new content"))
	require.NoError(t, err)

	// Should have changes now
	assert.True(t, git.HasChanges(), "Should have changes after adding new file")
}

func TestValidateBranchName(t *testing.T) {
	tests := []struct {
		name    string
		branch  string
		wantErr bool
	}{
		{"valid solve-issue", "matea/solve-issue-1", false},
		{"valid fix-bug", "matea/fix-bug-123", false},
		{"valid review", "matea/review-pr-456", false},
		{"invalid no prefix", "main", true},
		{"invalid legacy ai prefix", "ai/dev/issue-1", true},
		{"invalid feature", "feature/test", true},
		{"invalid with semicolon", "matea/test;rm -rf /", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBranchName(tt.branch)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateBranchName(t *testing.T) {
	tests := []struct {
		taskType string
		issueID  int
		expected string
	}{
		{"solve_issue", 14, "matea/solve-issue-14"},
		{"fix_bug", 5, "matea/fix-bug-5"},
		{"solve_comment", 123, "matea/solve-comment-123"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := GenerateBranchName(tt.taskType, tt.issueID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func setupGitRepoWithRemote(t *testing.T) (*Sandbox, *Git, string) {
	t.Helper()
	cfg := SandboxConfig{
		Mode:           ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      4096,
		MaxFileSize:    4096,
	}

	s := New(cfg, 2001)
	require.NoError(t, s.Setup())
	t.Cleanup(func() { s.Cleanup() })

	git := NewGit(s)
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	require.NoError(t, s.WriteFile("README.md", []byte("hello")))
	git.Add()
	require.NoError(t, git.Commit("initial").Error)
	require.NoError(t, s.Execute("git", "checkout", "-B", "main").Error)

	bareDir := filepath.Join(t.TempDir(), "remote.git")
	require.NoError(t, s.Execute("git", "init", "--bare", bareDir).Error)
	require.NoError(t, s.Execute("git", "remote", "add", "origin", bareDir).Error)
	require.NoError(t, s.Execute("git", "push", "-u", "origin", "HEAD:refs/heads/main").Error)

	return s, git, bareDir
}

func TestLocalBranchExists(t *testing.T) {
	_, git, _ := setupGitRepoWithRemote(t)

	assert.True(t, git.LocalBranchExists("main"))
	assert.False(t, git.LocalBranchExists("matea/dev/issue-1"))

	require.NoError(t, git.CreateBranch("matea/dev/issue-1").Error)
	assert.True(t, git.LocalBranchExists("matea/dev/issue-1"))
}

func TestRemoteBranchExists(t *testing.T) {
	s, git, _ := setupGitRepoWithRemote(t)

	assert.True(t, git.RemoteBranchExists("origin", "main"))
	assert.False(t, git.RemoteBranchExists("origin", "matea/dev/issue-2"))

	require.NoError(t, git.CreateBranch("matea/dev/issue-2").Error)
	require.NoError(t, s.WriteFile("feature.txt", []byte("x")))
	git.Add()
	require.NoError(t, git.Commit("feature").Error)
	require.NoError(t, git.Push("origin", "matea/dev/issue-2").Error)

	assert.True(t, git.RemoteBranchExists("origin", "matea/dev/issue-2"))
}

func TestFetchBranchDoesNotRequireSetBranches(t *testing.T) {
	s, git, _ := setupGitRepoWithRemote(t)

	require.NoError(t, git.CreateBranch("matea/dev/issue-3").Error)
	require.NoError(t, s.WriteFile("b.txt", []byte("b")))
	git.Add()
	require.NoError(t, git.Commit("on feature").Error)
	require.NoError(t, git.Push("origin", "matea/dev/issue-3").Error)

	require.NoError(t, git.Checkout("main").Error)

	fetchResult := git.FetchBranch("origin", "matea/dev/issue-3")
	require.NoError(t, fetchResult.Error, fetchResult.Stderr)

	checkoutResult := git.Checkout("matea/dev/issue-3")
	if checkoutResult.Error != nil {
		checkoutResult = s.Execute("git", "checkout", "-b", "matea/dev/issue-3", "origin/matea/dev/issue-3")
	}
	require.NoError(t, checkoutResult.Error, checkoutResult.Stderr)

	branch, err := git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "matea/dev/issue-3", branch)
}

func TestResetFetchRefspecsRemovesBranchSpecificRefs(t *testing.T) {
	s, git, _ := setupGitRepoWithRemote(t)

	require.NoError(t, s.Execute("git", "remote", "set-branches", "--add", "origin", "matea/dev/issue-4").Error)

	before := s.Execute("git", "config", "--get-all", "remote.origin.fetch")
	require.NoError(t, before.Error)
	assert.Contains(t, before.Stdout, "matea/dev/issue-4")

	resetResult := git.ResetFetchRefspecs("origin")
	require.NoError(t, resetResult.Error, resetResult.Stderr)

	after := s.Execute("git", "config", "--get-all", "remote.origin.fetch")
	require.NoError(t, after.Error)
	assert.NotContains(t, after.Stdout, "matea/dev/issue-4")
	assert.Contains(t, after.Stdout, "refs/heads/*")
}

func TestLocalOnlyBranchSkipsRemoteFetch(t *testing.T) {
	s, git, _ := setupGitRepoWithRemote(t)

	require.NoError(t, git.CreateBranch("matea/dev/issue-5").Error)
	assert.True(t, git.LocalBranchExists("matea/dev/issue-5"))
	assert.False(t, git.RemoteBranchExists("origin", "matea/dev/issue-5"))

	// Poison config like the old set-branches path did.
	require.NoError(t, s.Execute("git", "remote", "set-branches", "--add", "origin", "matea/dev/issue-5").Error)
	git.ResetFetchRefspecs("origin")

	require.NoError(t, git.Checkout("matea/dev/issue-5").Error)
	branch, err := git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "matea/dev/issue-5", branch)
}
