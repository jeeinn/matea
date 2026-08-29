package workflow

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRenderStatusCardRunning(t *testing.T) {
	start := time.Date(2026, 8, 28, 12, 25, 16, 0, time.UTC)
	body := RenderStatusCard(StatusCard{
		TaskID: 13, AgentName: "code-review", Role: "审查",
		State: StatusCardRunning, StartedAt: start, Trigger: "@code-review",
	})

	assert.True(t, IsAgentComment(body), "must keep the agent marker to avoid webhook re-trigger")
	// Marker sits on line two, right after the agent marker.
	lines := strings.SplitN(body, "\n", 3)
	require2(t, len(lines) >= 2)
	assert.Equal(t, AgentCommentMarker, lines[0])
	assert.Equal(t, StatusCardMarker(13), lines[1])

	assert.Contains(t, body, "🔄 处理中")
	assert.Contains(t, body, "### 🤖 code-review · 审查")
	assert.Contains(t, body, "| **开始于** | 2026-08-28 12:25:16 |")
	// Plain id, never "#13": Gitea auto-links #N to this repo's issue/PR of
	// the same number, so a card for task 13 would render as a link to a
	// completely unrelated object.
	assert.Contains(t, body, "| **任务** | 13 |")
	assert.Contains(t, body, "| **触发** | @code-review |")
	assert.NotContains(t, body, "耗时", "a running task has no duration to show")
}

func TestRenderStatusCardTaskIDIsNotAnIssueReference(t *testing.T) {
	// Regression: the card used to render the task id as "#13", which Gitea
	// turns into a link to issue/PR 13 of the repo the card is posted in.
	for _, id := range []int64{1, 13, 99} {
		body := RenderStatusCard(StatusCard{TaskID: id, AgentName: "a", State: StatusCardRunning})
		assert.NotContains(t, body, "| **任务** | #", "task id must not be rendered as a #N reference (task %d)", id)
	}
}

func TestRenderStatusCardSuccess(t *testing.T) {
	body := RenderStatusCard(StatusCard{
		TaskID: 12, AgentName: "code-opencode", Role: "编码",
		State: StatusCardSuccess, Duration: 4*time.Minute + 20*time.Second,
		Detail: "✅ PR 已创建：#6",
	})

	assert.Contains(t, body, "✅ 完成 · 耗时 4m20s")
	assert.Contains(t, body, "✅ PR 已创建：#6", "guidance renders as plain text, not a fenced block")
	assert.NotContains(t, body, "```")
}

func TestRenderStatusCardFailed(t *testing.T) {
	body := RenderStatusCard(StatusCard{
		TaskID: 13, AgentName: "coder007",
		State:  StatusCardFailed,
		Detail: "context deadline exceeded\n\tat internal/agents/x.go:42",
	})

	assert.Contains(t, body, "❌ 失败 · context deadline exceeded", "state line carries the cause")
	assert.Contains(t, body, "**错误原因：**")
	assert.Contains(t, body, "```\ncontext deadline exceeded", "full cause stays verbatim in a code block")
}

func TestRenderStatusCardTruncatesLongFailureCause(t *testing.T) {
	long := strings.Repeat("x", 400)
	body := RenderStatusCard(StatusCard{TaskID: 1, State: StatusCardFailed, Detail: long})

	assert.Contains(t, body, "...", "over-long causes must be truncated on the state line")
	assert.True(t, len(body) < 400+len(long), "card must not explode in size")
}

func TestRenderStatusCardOmitsOptionalRows(t *testing.T) {
	body := RenderStatusCard(StatusCard{TaskID: 7, AgentName: "analyze001"})
	assert.NotContains(t, body, "**开始于**")
	assert.NotContains(t, body, "**触发**")
	assert.Contains(t, body, "🔄 处理中", "missing timestamps must not break the state row")
}

func TestStatusCardMarkerIsTaskScoped(t *testing.T) {
	assert.NotEqual(t, StatusCardMarker(1), StatusCardMarker(2))
	assert.Contains(t, StatusCardMarker(13), "task-13")
}

func TestRoleLabel(t *testing.T) {
	assert.Equal(t, "分析", RoleLabel("analyze"))
	assert.Equal(t, "编码", RoleLabel("coder"))
	assert.Equal(t, "审查", RoleLabel("review"))
	assert.Equal(t, "", RoleLabel("unknown"))
	assert.Equal(t, "", RoleLabel(""))
}

// require2 is a tiny helper so this file needs no extra test dependency.
func require2(t *testing.T, cond bool) {
	t.Helper()
	if !cond {
		t.Fatal("precondition failed")
	}
}
