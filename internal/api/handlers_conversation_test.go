package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTaskConversation(t *testing.T) {
	mux, db := setupWorkflowPolicyAPI(t)

	agent := &store.Agent{Name: "coder", GiteaUsername: "c", GiteaToken: "t", Status: "active", Role: store.RoleCoder}
	require.NoError(t, db.CreateAgent(agent))
	task := &store.Task{
		Event: "issues", Repo: "owner/repo", IssueID: 1, AgentID: agent.ID,
		TaskType: "solve_issue", Status: store.StatusSuccess, DeliveryID: "conv-1",
	}
	require.NoError(t, db.CreateTask(task))
	require.NoError(t, db.AppendConversationMessages(task.ID, 1, []llm.Message{
		{Role: "user", Content: "fix it"},
		{Role: "assistant", Content: "ok", ToolCalls: []llm.ToolCall{{
			ID: "t1", Type: "function", Function: llm.FuncCall{Name: "read_file", Arguments: `{"path":"a.go"}`},
		}}},
	}, 0))

	w := httptest.NewRecorder()
	mux.ServeHTTP(w, apiAuthReq(http.MethodGet, "/api/tasks/99999/conversation", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	idPath := strconv.FormatInt(task.ID, 10)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, apiAuthReq(http.MethodGet, "/api/tasks/"+idPath+"/conversation", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp struct {
		TaskID   int64                        `json:"task_id"`
		Count    int                          `json:"count"`
		Messages []store.ConversationLogEntry `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, task.ID, resp.TaskID)
	assert.Equal(t, 2, resp.Count)
	require.Len(t, resp.Messages, 2)
	assert.Equal(t, "user", resp.Messages[0].Role)
	assert.Equal(t, "assistant", resp.Messages[1].Role)
	assert.Contains(t, resp.Messages[1].ToolCalls, "read_file")

	// getTask includes conversation_count
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, apiAuthReq(http.MethodGet, "/api/tasks/"+idPath, nil))
	require.Equal(t, http.StatusOK, w.Code)
	var detail map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, float64(2), detail["conversation_count"])
}
