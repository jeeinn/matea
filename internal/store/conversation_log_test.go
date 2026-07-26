package store

import (
	"testing"

	"github.com/jeeinn/matea/internal/llm"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAppendAndListConversationLogs(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{Name: "a", GiteaUsername: "u", GiteaToken: "t", Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	task := &Task{
		Event:    "issues.assigned",
		Repo:     "owner/repo",
		IssueID:  1,
		AgentID:  agent.ID,
		TaskType: "solve_issue",
		Status:   "pending",
	}
	require.NoError(t, db.CreateTask(task))

	messages := []llm.Message{
		{Role: "assistant", Content: "thinking", ToolCalls: []llm.ToolCall{{
			ID: "c1", Type: "function", Function: llm.FuncCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
		}}},
		{Role: "tool", Content: "file contents", ToolCallID: "c1"},
	}
	require.NoError(t, db.AppendConversationMessages(task.ID, 1, messages, 0))

	entries, err := db.ListConversationLogs(task.ID)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, 1, entries[0].Iteration)
	assert.Equal(t, "assistant", entries[0].Role)
	assert.Contains(t, entries[0].ToolCalls, "read_file")
	assert.Equal(t, "tool", entries[1].Role)
	assert.Equal(t, "c1", entries[1].ToolCallID)

	n, err := db.CountConversationLogs(task.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = db.CountConversationLogs(task.ID + 99)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestAppendConversationMessagesTruncatesContent(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{Name: "a", GiteaUsername: "u2", GiteaToken: "t", Status: "active"}
	require.NoError(t, db.CreateAgent(agent))

	task := &Task{Event: "e", Repo: "r", IssueID: 1, AgentID: agent.ID, TaskType: "solve_issue", Status: "pending"}
	require.NoError(t, db.CreateTask(task))

	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	require.NoError(t, db.AppendConversationMessages(task.ID, 1, []llm.Message{{Role: "tool", Content: string(long)}}, 50))

	entries, err := db.ListConversationLogs(task.ID)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Contains(t, entries[0].Content, "…(truncated)")
	assert.LessOrEqual(t, len(entries[0].Content), 70)
}
