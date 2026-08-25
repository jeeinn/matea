package agents

import (
	"context"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupLocalGitRepo builds a minimal local git workspace (no network) suitable
// for exercising finalizeWriteChanges without a Gitea server.
func setupLocalGitRepo(t *testing.T) (*sandbox.Sandbox, *sandbox.Git, *sandbox.AuditLogger) {
	t.Helper()
	cfg := sandbox.SandboxConfig{
		Mode:           sandbox.ModeFixed,
		BaseDir:        t.TempDir(),
		CommandTimeout: 30 * time.Second,
		MaxOutput:      4096,
		MaxFileSize:    4096,
	}
	s := sandbox.New(cfg, 9001)
	require.NoError(t, s.Setup())
	t.Cleanup(func() { s.Cleanup() })

	git := sandbox.NewGit(s)
	s.Execute("git", "init")
	s.Execute("git", "config", "user.email", "test@test.com")
	s.Execute("git", "config", "user.name", "Test")
	require.NoError(t, s.WriteFile("README.md", []byte("hello")))
	git.Add()
	require.NoError(t, git.Commit("initial").Error)

	audit := testAuditLogger(t)
	return s, git, audit
}

// TestFinalizeWriteChangesNoChangesReturnsComment verifies the no-changes early
// return: when the workspace has no uncommitted changes, finalizeWriteChanges
// returns a comment-style Result without touching git or PR.
func TestFinalizeWriteChangesNoChangesReturnsComment(t *testing.T) {
	s, git, audit := setupLocalGitRepo(t)

	wwc := &WriteWorkspaceContext{
		Sandbox:    s,
		Git:        git,
		Audit:      audit,
		BranchName: "main",
	}
	task := &store.Task{ID: 9001, Repo: "owner/repo"}
	agent := &store.Agent{Provider: "mock", Model: "m"}

	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")

	result, err := finalizeWriteChanges(context.Background(), wwc, task, agent, factory, nil, "dev", "nothing changed")
	require.NoError(t, err)
	assert.Equal(t, "comment", result.Action)
	assert.Equal(t, "nothing changed", result.Content)
	assert.Equal(t, 0, result.PRID)
}

func TestFinalizeWriteChangesRejectsPseudoToolCallOnCleanTree(t *testing.T) {
	s, git, audit := setupLocalGitRepo(t)

	wwc := &WriteWorkspaceContext{
		Sandbox:    s,
		Git:        git,
		Audit:      audit,
		BranchName: "main",
	}
	task := &store.Task{ID: 9002, Repo: "owner/repo"}
	agent := &store.Agent{Provider: "mock", Model: "m"}
	factory := NewRunnerFactory(nil, nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")

	dsml := `<|DSML|tool_calls>
<|DSML|invoke name="read_file">
<|DSML|parameter name="path" string="true">docs/RENAME-TO-MATEA.md</|DSML|parameter>
</|DSML|invoke>`
	_, err := finalizeWriteChanges(context.Background(), wwc, task, agent, factory, nil, "dev", dsml)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexecuted tool call")
}

// TestFinalizeWriteChangesCleanTreePushesBeforePRFailClosed is the P0 regression
// test. When the tree is clean but the agent committed locally without pushing
// (no remote branch), finalizeWriteChanges must push first and return an error
// on push failure — never a silent "success" comment. The old code called
// CreatePR directly, got a 404, logged a WARN, and returned a success comment,
// so the issue looked done while the remote had nothing.
func TestFinalizeWriteChangesCleanTreePushesBeforePRFailClosed(t *testing.T) {
	s, git, audit := setupLocalGitRepo(t)

	wwc := &WriteWorkspaceContext{
		Sandbox:    s,
		Git:        git,
		Audit:      audit,
		BranchName: "matea/solve-issue-9",
		RepoInfo:   &gitea.RepoInfo{DefaultBranch: "main"},
	}
	task := &store.Task{ID: 9009, Repo: "owner/repo"}
	agent := &store.Agent{Provider: "mock", Model: "m"}

	// mockGiteaFactory returns a non-nil admin client, so the clean-tree branch
	// enters the push-before-PR path. The local repo has no "origin" remote, so
	// the push fails and finalizeWriteChanges must return an error.
	factory := NewRunnerFactory(nil, &mockGiteaFactory{}, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, nil, nil, sandbox.DefaultConfig(), nil, "")

	_, err := finalizeWriteChanges(context.Background(), wwc, task, agent, factory, nil, "dev", "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "push")
	assert.Contains(t, err.Error(), "before opening PR failed")
}
