package store

import (
	"encoding/json"
	"fmt"

	"github.com/jeeinn/matea/internal/llm"
)

// ConversationLogEntry is one persisted message from an agent loop iteration.
type ConversationLogEntry struct {
	ID         int64  `json:"id"`
	TaskID     int64  `json:"task_id"`
	Iteration  int    `json:"iteration"`
	Seq        int    `json:"seq"`
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCalls  string `json:"tool_calls,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// AppendConversationMessages stores messages for one agent loop iteration.
func (db *DB) AppendConversationMessages(taskID int64, iteration int, messages []llm.Message, maxContentChars int) error {
	if taskID <= 0 || len(messages) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for seq, msg := range messages {
		content := truncateStoredContent(msg.Content, maxContentChars)
		toolCalls := ""
		if len(msg.ToolCalls) > 0 {
			data, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("marshal tool_calls: %w", err)
			}
			toolCalls = truncateStoredContent(string(data), maxContentChars)
		}
		_, err := tx.Exec(`INSERT INTO task_conversation_logs
			(task_id, iteration, seq, role, content, tool_calls, tool_call_id)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			taskID, iteration, seq, msg.Role, content, toolCalls, msg.ToolCallID)
		if err != nil {
			return fmt.Errorf("insert conversation log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit conversation log: %w", err)
	}
	return nil
}

// ListConversationLogs returns conversation messages for a task ordered by iteration and seq.
func (db *DB) ListConversationLogs(taskID int64) ([]ConversationLogEntry, error) {
	rows, err := db.Query(`SELECT id, task_id, iteration, seq, role, content, tool_calls, tool_call_id
		FROM task_conversation_logs WHERE task_id = ? ORDER BY iteration, seq`, taskID)
	if err != nil {
		return nil, fmt.Errorf("query conversation logs: %w", err)
	}
	defer rows.Close()

	var entries []ConversationLogEntry
	for rows.Next() {
		var e ConversationLogEntry
		if err := rows.Scan(&e.ID, &e.TaskID, &e.Iteration, &e.Seq, &e.Role, &e.Content, &e.ToolCalls, &e.ToolCallID); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// CountConversationLogs returns how many conversation log rows exist for a task.
func (db *DB) CountConversationLogs(taskID int64) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM task_conversation_logs WHERE task_id = ?`, taskID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count conversation logs: %w", err)
	}
	return n, nil
}

func truncateStoredContent(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + "…(truncated)"
}
