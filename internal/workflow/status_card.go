package workflow

import (
	"fmt"
	"strings"
	"time"
)

// Status cards (20260828-issue-status-card, plan §2/§6).
//
// Each task owns exactly ONE progress comment — the status card — which is
// created when the task starts and PATCHed in place on every later state
// change. The previous behaviour posted a fresh "已开始处理" comment per task
// and left it there forever: when a task hangs (observed: task #13 running
// >2h with no terminal state) that comment is permanent noise with no signal.
// A single card is self-contained whether or not the task ever reaches a
// terminal state.
//
// The card is a plain markdown comment, not a Gitea timeline event: Gitea does
// not expose custom timeline event types via API, and it sanitizes raw HTML, so
// the card sticks to emoji + tables + fenced blocks.

// AgentCommentMarker is the invisible prefix every Matea-authored comment
// carries. IsAgentComment checks it to avoid re-triggering on our own output.
const AgentCommentMarker = "<!-- matea-agent -->"

// statusCardMarkerFmt builds the marker that identifies a task's card among an
// issue's comments. It is how the card is found again after a restart dropped
// the cached comment ID, or after the ID was never persisted.
const statusCardMarkerFmt = "<!-- matea-status-card:task-%d -->"

// StatusCardMarker returns the marker embedded in a task's status card.
func StatusCardMarker(taskID int64) string {
	return fmt.Sprintf(statusCardMarkerFmt, taskID)
}

// StatusCardState is the lifecycle state rendered on a card.
type StatusCardState string

const (
	StatusCardRunning StatusCardState = "running"
	StatusCardSuccess StatusCardState = "success"
	StatusCardFailed  StatusCardState = "failed"
)

// StatusCard carries everything needed to render a task's progress card.
type StatusCard struct {
	TaskID    int64
	AgentName string
	Role      string // display role (e.g. 代码审查); empty omits it
	State     StatusCardState
	StartedAt time.Time     // absolute start stamp; Gitea already shows "x 前"
	Duration  time.Duration // elapsed for terminal states; 0 while running
	Trigger   string        // e.g. "@code-review"
	Detail    string        // terminal supplement: guidance when done, cause when failed
}

// RenderStatusCard renders the card as pure markdown.
//
// The output ALWAYS starts with AgentCommentMarker so IsAgentComment still
// recognises it as our own comment (otherwise the card would feed the webhook
// back into a new task). The task marker follows on line two.
func RenderStatusCard(c StatusCard) string {
	title := "🤖 " + c.AgentName
	if c.Role != "" {
		title += " · " + c.Role
	}

	var b strings.Builder
	b.WriteString(AgentCommentMarker + "\n")
	b.WriteString(StatusCardMarker(c.TaskID) + "\n\n")
	b.WriteString("### " + title + "\n\n")
	b.WriteString("| 项目 | 内容 |\n")
	b.WriteString("|------|------|\n")
	b.WriteString(fmt.Sprintf("| **状态** | %s |\n", c.stateText()))
	if !c.StartedAt.IsZero() {
		b.WriteString(fmt.Sprintf("| **开始于** | %s |\n", c.StartedAt.Format("2006-01-02 15:04:05")))
	}
	b.WriteString(fmt.Sprintf("| **任务** | #%d |\n", c.TaskID))
	if c.Trigger != "" {
		b.WriteString(fmt.Sprintf("| **触发** | %s |\n", c.Trigger))
	}
	if detail := c.renderDetail(); detail != "" {
		b.WriteString("\n" + detail + "\n")
	}
	b.WriteString("\n> Matea 状态卡 · 随任务进展自动更新\n")
	return b.String()
}

// stateText renders the status cell: running shows no duration (nothing to
// measure yet), terminal states show elapsed time or the failure cause.
func (c StatusCard) stateText() string {
	switch c.State {
	case StatusCardSuccess:
		text := "✅ 完成"
		if c.Duration > 0 {
			text += fmt.Sprintf(" · 耗时 %s", formatCardDuration(c.Duration))
		}
		return text
	case StatusCardFailed:
		text := "❌ 失败"
		if cause := firstLine(c.Detail); cause != "" {
			text += fmt.Sprintf(" · %s", truncateCardText(cause, 120))
		}
		return text
	default:
		return "🔄 处理中"
	}
}

// renderDetail renders the block below the status table. Failures get a fenced
// block because stack/error text must survive markdown rendering verbatim.
func (c StatusCard) renderDetail() string {
	detail := strings.TrimSpace(c.Detail)
	if detail == "" {
		return ""
	}
	if c.State == StatusCardFailed {
		return "**错误原因：**\n\n```\n" + detail + "\n```"
	}
	return detail
}

// formatCardDuration renders a duration at second granularity (4m20s, not
// 4m20.137s) — the extra precision is noise on a progress card.
func formatCardDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.Round(time.Second).String()
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// truncateCardText shortens s to at most limit runes, appending an ellipsis.
func truncateCardText(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "..."
}

// RoleLabel maps an internal role key to its Chinese display name for cards.
func RoleLabel(role string) string {
	switch role {
	case "analyze":
		return "分析"
	case "coder":
		return "编码"
	case "review":
		return "审查"
	default:
		return ""
	}
}
