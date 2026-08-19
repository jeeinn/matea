package agents

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveWorkBranch(t *testing.T) {
	task := &store.Task{BaseBranch: "ai/dev/issue-2"}
	assert.Equal(t, "ai/dev/issue-2", resolveWorkBranch(task, ""))
	assert.Equal(t, "ai/dev/issue-2", resolveWorkBranch(task, "ai/dev/issue-3"))
	assert.Equal(t, "ai/dev/issue-3", resolveWorkBranch(&store.Task{}, "ai/dev/issue-3"))
	assert.Equal(t, "", resolveWorkBranch(&store.Task{}, ""))
	assert.Equal(t, "ai/dev/issue-2", resolveWorkBranch(&store.Task{BaseBranch: " ai/dev/issue-2 "}, ""))
}

func testAuditLogger(t *testing.T) *sandbox.AuditLogger {
	t.Helper()
	tmp, err := os.CreateTemp("", "runners-git-*.db")
	require.NoError(t, err)
	tmp.Close()
	db, err := store.Open(tmp.Name())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmp.Name())
	})
	return sandbox.NewAuditLogger(db, 1, 1)
}

func setupSessionGitRepo(t *testing.T) (*sandbox.Sandbox, *sandbox.Git, string) {
	t.Helper()
	cfg := sandbox.SandboxConfig{
		Mode:           sandbox.ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      4096,
		MaxFileSize:    4096,
	}
	s := sandbox.New(cfg, 3001)
	require.NoError(t, s.Setup())
	t.Cleanup(func() { s.Cleanup() })

	git := sandbox.NewGit(s)
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	require.NoError(t, s.WriteFile("README.md", []byte("hello")))
	git.Add()
	require.NoError(t, git.Commit("initial").Error)
	require.NoError(t, s.Execute("git", "checkout", "-B", "master").Error)

	bareDir := filepath.Join(t.TempDir(), "remote.git")
	require.NoError(t, s.Execute("git", "init", "--bare", bareDir).Error)
	require.NoError(t, s.Execute("git", "remote", "add", "origin", bareDir).Error)
	require.NoError(t, s.Execute("git", "push", "-u", "origin", "HEAD:refs/heads/master").Error)

	require.NoError(t, git.CreateBranch("ai/dev/issue-2").Error)
	require.NoError(t, s.WriteFile("internal/config/schema.go", []byte("package config\n")))
	git.Add()
	require.NoError(t, git.Commit("feature").Error)
	require.NoError(t, git.Push("origin", "ai/dev/issue-2").Error)

	return s, git, bareDir
}

func TestCheckoutWorkBranchSkipsWhenAlreadyOnBranch(t *testing.T) {
	s, git, _ := setupSessionGitRepo(t)

	require.NoError(t, git.Checkout("ai/dev/issue-2").Error)
	require.NoError(t, s.WriteFile("internal/config/schema.go", []byte("dirty")))

	audit := testAuditLogger(t)
	err := checkoutWorkBranch(s, git, audit, "ai/dev/issue-2")
	require.NoError(t, err)

	branch, err := git.GetCurrentBranch()
	require.NoError(t, err)
	assert.Equal(t, "ai/dev/issue-2", branch)
	assert.True(t, git.HasChanges())
}
