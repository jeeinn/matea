package api

import (
	"encoding/json"
	"net/http"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/store"
)

// AgentDTO is the API response for agents, hiding sensitive fields.
type AgentDTO struct {
	ID              int64                  `json:"id"`
	Name            string                 `json:"name"`
	GiteaUsername   string                 `json:"gitea_username"`
	AvatarURL       string                 `json:"avatar_url"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	MaxInputTokens  int                    `json:"max_input_tokens"`
	Temperature     float64                `json:"temperature"`
	Timeout         string                 `json:"timeout"`
	SystemPrompt    string                 `json:"system_prompt"`
	UserTemplate    string                 `json:"user_template"`
	LoopConfig      *store.AgentLoopConfig `json:"loop_config,omitempty"`
	Repos           []string               `json:"repos,omitempty"`
	Role            string                 `json:"role"`
	Status          string                 `json:"status"`
	Backend         string                 `json:"backend"`
	BackendOptions  map[string]any         `json:"backend_options,omitempty"`
	ToolPack        string                 `json:"tool_pack"`
	McpServers      []string               `json:"mcp_servers,omitempty"`
}

func toAgentDTO(a *store.Agent) AgentDTO {
	return AgentDTO{
		ID:              a.ID,
		Name:            a.Name,
		GiteaUsername:   a.GiteaUsername,
		AvatarURL:       a.AvatarURL,
		Repos:           a.Repos,
		Provider:        a.Provider,
		Model:           a.Model,
		MaxOutputTokens: a.MaxOutputTokens,
		MaxInputTokens:  a.MaxInputTokens,
		Temperature:     a.Temperature,
		Timeout:         a.Timeout,
		SystemPrompt:    a.SystemPrompt,
		UserTemplate:    a.UserTemplate,
		LoopConfig:      a.LoopConfig,
		Role:            a.Role,
		Status:          a.Status,
		Backend:         a.Backend,
		BackendOptions:  a.BackendOptions,
		ToolPack:        a.ToolPack,
		McpServers:      a.McpServers,
	}
}

// --- Agent endpoints ---

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents, err := h.db.ListAgents()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	dtos := make([]AgentDTO, len(agents))
	for i, a := range agents {
		dtos[i] = toAgentDTO(a)
	}
	writeJSON(w, 200, dtos)
}

func (h *Handler) createAgent(w http.ResponseWriter, r *http.Request) {
	var req agents.CreateAgentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	agent, err := h.manager.CreateAgent(req)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// Add agent as collaborator to selected repos
	var repoWarnings []string
	if len(req.Repos) > 0 {
		repoWarnings = h.manager.AddCollaboratorToRepos(req.GiteaUsername, req.Repos)
	}

	resp := map[string]interface{}{
		"agent": toAgentDTO(agent),
	}
	if len(repoWarnings) > 0 {
		resp["repo_warnings"] = repoWarnings
	}
	writeJSON(w, 201, resp)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	agent, err := h.db.GetAgent(id)
	if err != nil {
		writeError(w, 404, "agent not found")
		return
	}
	writeJSON(w, 200, toAgentDTO(agent))
}

func (h *Handler) updateAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	agent, err := h.db.GetAgent(id)
	if err != nil {
		writeError(w, 404, "agent not found")
		return
	}

	// Save old prompt for comparison
	oldSysPrompt := agent.SystemPrompt
	oldUsrTemplate := agent.UserTemplate

	// Decode into a temp struct to capture extra fields (repos)
	var req struct {
		store.Agent
		Repos []string `json:"repos"`
		// TakeOverGiteaUser explicitly hands an existing non-Matea-managed
		// Gitea account to Matea (password reset via Admin API).
		TakeOverGiteaUser bool `json:"take_over_gitea_user"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, 400, "invalid request body")
		return
	}
	// Copy decoded fields to agent
	agent.Name = req.Name
	agent.Provider = req.Provider
	agent.Model = req.Model
	agent.MaxOutputTokens = req.MaxOutputTokens
	agent.MaxInputTokens = req.MaxInputTokens
	agent.Temperature = req.Temperature
	agent.Timeout = req.Timeout
	agent.SystemPrompt = req.SystemPrompt
	agent.UserTemplate = req.UserTemplate
	agent.Status = req.Status
	agent.Repos = req.Repos
	if req.Role != "" {
		agent.Role = req.Role
	}
	if req.LoopConfig != nil {
		agent.LoopConfig = req.LoopConfig
	}
	if req.Backend != "" {
		agent.Backend = req.Backend
	}
	// backend_options: replace if provided in request (nil → keep existing)
	if req.BackendOptions != nil {
		agent.BackendOptions = req.BackendOptions
	}
	if req.ToolPack != "" {
		agent.ToolPack = req.ToolPack
	}
	// mcp_servers: replace if provided in request (nil → keep existing)
	if req.McpServers != nil {
		agent.McpServers = req.McpServers
	}
	agent.ID = id
	if err := h.manager.UpdateAgent(agent, req.TakeOverGiteaUser); err != nil {
		writeError(w, 500, err.Error())
		return
	}

	// Add agent as collaborator to newly selected repos
	var repoWarnings []string
	if len(req.Repos) > 0 {
		repoWarnings = h.manager.AddCollaboratorToRepos(agent.GiteaUsername, req.Repos)
	}

	// Auto-create prompt history if prompt changed
	if agent.SystemPrompt != oldSysPrompt || agent.UserTemplate != oldUsrTemplate {
		if h.prompt != nil && agent.SystemPrompt != "" {
			if _, err := h.prompt.SavePrompt(id, agent.SystemPrompt, agent.UserTemplate, "Agent 编辑更新", "ui"); err != nil {
				// Log but don't fail the request
				_ = err
			}
		}
	}

	resp := map[string]interface{}{
		"agent": agent,
	}
	if len(repoWarnings) > 0 {
		resp["repo_warnings"] = repoWarnings
	}
	writeJSON(w, 200, resp)
}

func (h *Handler) deleteAgent(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, 400, "invalid id")
		return
	}
	// Delete agent and Gitea user
	if err := h.manager.DeleteAgent(id, true); err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}
