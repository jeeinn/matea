package agents

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock Gitea accessor ---

type mockGiteaReadOnly struct {
	issueTitle  string
	issueBody   string
	diff        string
	issueErr    error
	diffErr     error
}

func (m *mockGiteaReadOnly) GetIssue(ctx context.Context, repo string, issueID int) (title, body string, err error) {
	return m.issueTitle, m.issueBody, m.issueErr
}

func (m *mockGiteaReadOnly) GetPRDiff(ctx context.Context, repo string, prID int) (diff string, err error) {
	return m.diff, m.diffErr
}

// --- Tests ---

func TestToolBoxRegisterAndCount(t *testing.T) {
	tb := NewToolBox(nil, nil)

	assert.Equal(t, 0, tb.ToolCount())
	assert.False(t, tb.HasTool("anything"))

	tb.Register(ToolDecl{
		Name:     "test_tool",
		Category: ToolCatSandbox,
		Exposure: ExposureBuiltinOnly,
	})

	assert.Equal(t, 1, tb.ToolCount())
	assert.True(t, tb.HasTool("test_tool"))
	assert.False(t, tb.HasTool("other"))
}

func TestToolBoxToolsForBuiltin(t *testing.T) {
	// Builtin harness (ToolDirect) should see ALL tools
	tb := NewToolBox(nil, nil)

	tb.Register(ToolDecl{Name: "sandbox_tool", Category: ToolCatSandbox, Exposure: ExposureBuiltinOnly})
	tb.Register(ToolDecl{Name: "gitea_tool", Category: ToolCatGitea, Exposure: ExposureAll})
	tb.Register(ToolDecl{Name: "skill_tool", Category: ToolCatSkill, Exposure: ExposureAll})

	builtin := &mockHarness{
		profile: HarnessProfile{ID: "builtin", ToolTransport: ToolDirect},
	}

	tools := tb.ToolsFor(builtin)
	assert.Len(t, tools, 3) // builtin sees all
}

func TestToolBoxToolsForRemote(t *testing.T) {
	// Remote harness (ToolViaSubmit) should NOT see sandbox tools
	tb := NewToolBox(nil, nil)

	tb.Register(ToolDecl{Name: "sandbox_tool", Category: ToolCatSandbox, Exposure: ExposureBuiltinOnly})
	tb.Register(ToolDecl{Name: "gitea_tool", Category: ToolCatGitea, Exposure: ExposureAll})
	tb.Register(ToolDecl{Name: "skill_tool", Category: ToolCatSkill, Exposure: ExposureAll})

	remote := &mockHarness{
		profile: HarnessProfile{ID: "remote", ToolTransport: ToolViaSubmit},
	}

	tools := tb.ToolsFor(remote)
	assert.Len(t, tools, 2) // remote sees gitea + skill only

	// Verify sandbox tool is excluded
	for _, td := range tools {
		assert.NotEqual(t, "sandbox_tool", td.Name)
		assert.NotEqual(t, ToolCatSandbox, td.Category)
	}
}

func TestToolBoxExecuteUnknownTool(t *testing.T) {
	tb := NewToolBox(nil, nil)

	_, err := tb.Execute(context.Background(), "nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestToolBoxExecuteGiteaReadIssue(t *testing.T) {
	gitea := &mockGiteaReadOnly{
		issueTitle: "Test Issue",
		issueBody:  "Issue body here",
	}
	tb := NewToolBox(nil, gitea)

	td := NewGiteaReadIssueTool(gitea)
	tb.Register(td)

	result, err := tb.Execute(context.Background(), "gitea_read_issue", map[string]interface{}{
		"repo":     "owner/repo",
		"issue_id": float64(123),
	})
	require.NoError(t, err)
	assert.Contains(t, result, "Test Issue")
	assert.Contains(t, result, "Issue body here")
}

func TestToolBoxExecuteGiteaReadIssueMissingParams(t *testing.T) {
	gitea := &mockGiteaReadOnly{}
	tb := NewToolBox(nil, gitea)

	td := NewGiteaReadIssueTool(gitea)
	tb.Register(td)

	_, err := tb.Execute(context.Background(), "gitea_read_issue", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestToolBoxExecuteGiteaReadPRDiff(t *testing.T) {
	gitea := &mockGiteaReadOnly{
		diff: "diff --git a/file.go b/file.go\n+added line",
	}
	tb := NewToolBox(nil, gitea)

	td := NewGiteaReadPRDiffTool(gitea)
	tb.Register(td)

	result, err := tb.Execute(context.Background(), "gitea_read_pr_diff", map[string]interface{}{
		"repo":  "owner/repo",
		"pr_id": float64(456),
	})
	require.NoError(t, err)
	assert.Contains(t, result, "diff --git")
}

func TestToolBoxExecuteGiteaReadPRDiffMissingParams(t *testing.T) {
	gitea := &mockGiteaReadOnly{}
	tb := NewToolBox(nil, gitea)

	td := NewGiteaReadPRDiffTool(gitea)
	tb.Register(td)

	_, err := tb.Execute(context.Background(), "gitea_read_pr_diff", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestToolBoxExecuteGiteaError(t *testing.T) {
	gitea := &mockGiteaReadOnly{
		issueErr: ErrNotFound,
	}
	tb := NewToolBox(nil, gitea)

	td := NewGiteaReadIssueTool(gitea)
	tb.Register(td)

	_, err := tb.Execute(context.Background(), "gitea_read_issue", map[string]interface{}{
		"repo":     "owner/repo",
		"issue_id": float64(999),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get issue")
}

func TestToolCategoryConstants(t *testing.T) {
	assert.Equal(t, ToolCategory("sandbox"), ToolCatSandbox)
	assert.Equal(t, ToolCategory("gitea"), ToolCatGitea)
	assert.Equal(t, ToolCategory("skill"), ToolCatSkill)
}

func TestToolExposureConstants(t *testing.T) {
	assert.Equal(t, ToolExposure("builtin_only"), ExposureBuiltinOnly)
	assert.Equal(t, ToolExposure("all"), ExposureAll)
}

func TestToolBoxReregisterOverwrites(t *testing.T) {
	tb := NewToolBox(nil, nil)

	tb.Register(ToolDecl{Name: "tool1", Category: ToolCatSandbox})
	assert.Equal(t, 1, tb.ToolCount())

	tb.Register(ToolDecl{Name: "tool1", Category: ToolCatGitea})
	assert.Equal(t, 1, tb.ToolCount()) // still 1, overwritten

	tools := tb.ToolsFor(&mockHarness{profile: HarnessProfile{ToolTransport: ToolDirect}})
	assert.Equal(t, ToolCatGitea, tools[0].Category)
}

// ErrNotFound is a sentinel for mock errors.
var ErrNotFound = fmt.Errorf("not found")
