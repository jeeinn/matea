package dispatcher

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
)

const (
	// commentHistoryLimit matches InteractionRunner's recent-comment window.
	commentHistoryLimit = 10
	// maxCommentBodyChars caps a single comment so one long review cannot dominate context.
	maxCommentBodyChars = 8000
)

// selectCommentsForContext picks up to limit comments for coder continuation.
// Review-agent comments (preferUsernames) are prioritized; remaining slots are
// filled with other recent comments. Result is chronological (oldest → newest).
func selectCommentsForContext(comments []gitea.IssueComment, preferUsernames map[string]bool, limit int) []gitea.IssueComment {
	if limit <= 0 || len(comments) == 0 {
		return nil
	}
	if preferUsernames == nil {
		preferUsernames = map[string]bool{}
	}

	type indexed struct {
		idx int
		c   gitea.IssueComment
	}
	var preferred, others []indexed
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if strings.TrimSpace(c.Body) == "" || isProgressOnlyComment(c.Body) {
			continue
		}
		item := indexed{idx: i, c: c}
		if preferUsernames[c.User.Login] {
			preferred = append(preferred, item)
		} else {
			others = append(others, item)
		}
	}

	picked := make([]indexed, 0, limit)
	seen := make(map[int]bool, limit)
	for _, item := range preferred {
		if len(picked) >= limit {
			break
		}
		picked = append(picked, item)
		seen[item.idx] = true
	}
	for _, item := range others {
		if len(picked) >= limit {
			break
		}
		if seen[item.idx] {
			continue
		}
		picked = append(picked, item)
		seen[item.idx] = true
	}

	sort.Slice(picked, func(i, j int) bool { return picked[i].idx < picked[j].idx })

	out := make([]gitea.IssueComment, len(picked))
	for i, item := range picked {
		out[i] = item.c
	}
	return out
}

// formatCommentHistory builds a prompt section from selected comments.
func formatCommentHistory(comments []gitea.IssueComment, preferUsernames map[string]bool) string {
	if len(comments) == 0 {
		return ""
	}
	if preferUsernames == nil {
		preferUsernames = map[string]bool{}
	}

	var sb strings.Builder
	sb.WriteString("\n## Recent PR / Issue comments\n")
	sb.WriteString("Use this thread (especially review feedback) when applying the requested changes.\n\n")

	hasReview := false
	for _, c := range comments {
		if preferUsernames[c.User.Login] {
			hasReview = true
			break
		}
	}
	if hasReview {
		sb.WriteString("### Review feedback (priority)\n")
		for _, c := range comments {
			if !preferUsernames[c.User.Login] {
				continue
			}
			sb.WriteString(formatOneComment(c, true))
		}
		sb.WriteString("### Other recent comments\n")
		for _, c := range comments {
			if preferUsernames[c.User.Login] {
				continue
			}
			sb.WriteString(formatOneComment(c, false))
		}
	} else {
		for _, c := range comments {
			sb.WriteString(formatOneComment(c, false))
		}
	}
	return sb.String()
}

func formatOneComment(c gitea.IssueComment, review bool) string {
	label := c.User.Login
	if review {
		label = fmt.Sprintf("%s [review]", c.User.Login)
	}
	return fmt.Sprintf("[%s]:\n%s\n\n", label, truncateCommentBody(c.Body, maxCommentBodyChars))
}

func truncateCommentBody(body string, maxChars int) string {
	body = strings.TrimSpace(body)
	if maxChars <= 0 || len(body) <= maxChars {
		return body
	}
	return body[:maxChars] + "\n…(truncated)"
}

// isProgressOnlyComment skips short gateway progress pings that add little for coding.
func isProgressOnlyComment(body string) bool {
	trimmed := strings.TrimSpace(body)
	if len(trimmed) > 240 {
		return false
	}
	return strings.Contains(trimmed, "已开始处理")
}
