package store

import (
	"encoding/json"
	"fmt"
	"time"
)

// AgentLoopConfig contains agent-specific loop configuration (multi-turn only).
type AgentLoopConfig struct {
	MaxIterations      int      `json:"max_iterations,omitempty"`
	TotalTimeout       string   `json:"total_timeout,omitempty"`
	IterationInterval  int      `json:"iteration_interval,omitempty"`  // seconds between loop rounds; 0 = no delay
	NoProgressLimit    *int     `json:"no_progress_limit,omitempty"`   // nil = inherit; 0 = disable; >0 = limit
	VerifyCommands     []string `json:"verify_commands,omitempty"`     // nil = inherit; empty slice = disable verify
	IndependentChecker *bool    `json:"independent_checker,omitempty"` // nil = inherit; true/false override
}

// Agent represents an AI agent registered in the system.
type Agent struct {
	ID              int64            `json:"id"`
	Name            string           `json:"name"`
	GiteaUsername   string           `json:"gitea_username"`
	GiteaToken      string           `json:"gitea_token"`
	AvatarURL       string           `json:"avatar_url"`
	Provider        string           `json:"provider"`
	Model           string           `json:"model"`
	MaxOutputTokens int              `json:"max_output_tokens"`
	MaxInputTokens  int              `json:"max_input_tokens"`
	Temperature     float64          `json:"temperature"`
	Timeout         string           `json:"timeout"` // single-shot task timeout, e.g. "5m"
	SystemPrompt    string           `json:"system_prompt"`
	UserTemplate    string           `json:"user_template"`
	LoopConfig      *AgentLoopConfig `json:"loop_config,omitempty"`
	Repos           []string         `json:"repos,omitempty"`
	Role            string           `json:"role"` // analyze | coder | review
	Status          string           `json:"status"`
	Backend         string           `json:"backend"`                   // coding backend name; default "builtin" (OpenCode Path A)
	BackendOptions  map[string]any   `json:"backend_options,omitempty"` // backend-specific options (JSON)
	ToolPack        string           `json:"tool_pack"`                 // ToolPack name; empty = use role-based default
	McpServers      []string         `json:"mcp_servers,omitempty"`     // Enabled MCP server names; empty = none
	ManagedByMatea  bool             `json:"managed_by_matea"`          // true = Matea created and manages this Gitea account
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

const agentSelectCols = `id, name, gitea_username, gitea_token, avatar_url, provider, model,
	max_output_tokens, max_input_tokens, temperature, timeout, system_prompt, user_template,
	loop_config, repos, role, status, backend, backend_options, tool_pack, mcp_servers, managed_by_matea, created_at, updated_at`

func scanAgent(scanner interface {
	Scan(dest ...any) error
}) (*Agent, error) {
	var a Agent
	var loopConfigJSON, reposJSON, backendOptionsJSON, mcpServersJSON string
	var managedByMateaInt int
	err := scanner.Scan(
		&a.ID, &a.Name, &a.GiteaUsername, &a.GiteaToken, &a.AvatarURL, &a.Provider, &a.Model,
		&a.MaxOutputTokens, &a.MaxInputTokens, &a.Temperature, &a.Timeout, &a.SystemPrompt, &a.UserTemplate,
		&loopConfigJSON, &reposJSON, &a.Role, &a.Status, &a.Backend, &backendOptionsJSON, &a.ToolPack, &mcpServersJSON, &managedByMateaInt, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	a.ManagedByMatea = managedByMateaInt != 0
	if loopConfigJSON != "" {
		json.Unmarshal([]byte(loopConfigJSON), &a.LoopConfig)
	}
	if reposJSON != "" && reposJSON != "[]" {
		json.Unmarshal([]byte(reposJSON), &a.Repos)
	}
	if backendOptionsJSON != "" && backendOptionsJSON != "{}" {
		json.Unmarshal([]byte(backendOptionsJSON), &a.BackendOptions)
	}
	if mcpServersJSON != "" && mcpServersJSON != "[]" {
		json.Unmarshal([]byte(mcpServersJSON), &a.McpServers)
	}
	if a.Backend == "" {
		a.Backend = "builtin"
	}
	return &a, nil
}

// CreateAgent inserts a new agent into the database.
func (db *DB) CreateAgent(a *Agent) error {
	loopConfigJSON := "{}"
	if a.LoopConfig != nil {
		data, _ := json.Marshal(a.LoopConfig)
		loopConfigJSON = string(data)
	}
	reposJSON := "[]"
	if a.Repos != nil {
		data, _ := json.Marshal(a.Repos)
		reposJSON = string(data)
	}
	backendOptionsJSON := "{}"
	if a.BackendOptions != nil {
		data, _ := json.Marshal(a.BackendOptions)
		backendOptionsJSON = string(data)
	}
	mcpServersJSON := "[]"
	if a.McpServers != nil {
		data, _ := json.Marshal(a.McpServers)
		mcpServersJSON = string(data)
	}
	if a.Backend == "" {
		a.Backend = "builtin"
	}

	result, err := db.Exec(`INSERT INTO agents
		(name, gitea_username, gitea_token, avatar_url, provider, model,
		 max_output_tokens, max_input_tokens, temperature, timeout, system_prompt, user_template,
		 loop_config, repos, role, status, backend, backend_options, tool_pack, mcp_servers, managed_by_matea)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.GiteaUsername, a.GiteaToken, a.AvatarURL, a.Provider, a.Model,
		a.MaxOutputTokens, a.MaxInputTokens, a.Temperature, a.Timeout, a.SystemPrompt, a.UserTemplate,
		loopConfigJSON, reposJSON, a.Role, a.Status, a.Backend, backendOptionsJSON, a.ToolPack, mcpServersJSON, boolToInt(a.ManagedByMatea))
	if err != nil {
		return fmt.Errorf("insert agent: %w", err)
	}
	id, _ := result.LastInsertId()
	a.ID = id
	return nil
}

// GetAgent returns an agent by ID.
func (db *DB) GetAgent(id int64) (*Agent, error) {
	a, err := scanAgent(db.QueryRow(`SELECT `+agentSelectCols+` FROM agents WHERE id = ?`, id))
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	return a, nil
}

// GetAgentByGiteaUsername returns an agent by their Gitea username.
func (db *DB) GetAgentByGiteaUsername(username string) (*Agent, error) {
	a, err := scanAgent(db.QueryRow(`SELECT `+agentSelectCols+` FROM agents WHERE gitea_username = ?`, username))
	if err != nil {
		return nil, fmt.Errorf("get agent by username: %w", err)
	}
	return a, nil
}

// ListAgents returns all agents.
func (db *DB) ListAgents() ([]*Agent, error) {
	rows, err := db.Query(`SELECT ` + agentSelectCols + ` FROM agents ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, nil
}

// UpdateAgent updates an existing agent.
func (db *DB) UpdateAgent(a *Agent) error {
	loopConfigJSON := "{}"
	if a.LoopConfig != nil {
		data, _ := json.Marshal(a.LoopConfig)
		loopConfigJSON = string(data)
	}
	reposJSON := "[]"
	if a.Repos != nil {
		data, _ := json.Marshal(a.Repos)
		reposJSON = string(data)
	}
	backendOptionsJSON := "{}"
	if a.BackendOptions != nil {
		data, _ := json.Marshal(a.BackendOptions)
		backendOptionsJSON = string(data)
	}
	mcpServersJSON := "[]"
	if a.McpServers != nil {
		data, _ := json.Marshal(a.McpServers)
		mcpServersJSON = string(data)
	}
	if a.Backend == "" {
		a.Backend = "builtin"
	}

	_, err := db.Exec(`UPDATE agents SET name=?, provider=?, model=?,
		max_output_tokens=?, max_input_tokens=?, temperature=?, timeout=?,
		system_prompt=?, user_template=?, loop_config=?, repos=?, role=?, status=?,
		avatar_url=?, gitea_token=?, backend=?, backend_options=?, tool_pack=?, mcp_servers=?, managed_by_matea=?, updated_at=CURRENT_TIMESTAMP
		WHERE id=?`,
		a.Name, a.Provider, a.Model,
		a.MaxOutputTokens, a.MaxInputTokens, a.Temperature, a.Timeout,
		a.SystemPrompt, a.UserTemplate, loopConfigJSON, reposJSON, a.Role, a.Status,
		a.AvatarURL, a.GiteaToken, a.Backend, backendOptionsJSON, a.ToolPack, mcpServersJSON, boolToInt(a.ManagedByMatea), a.ID)
	if err != nil {
		return fmt.Errorf("update agent: %w", err)
	}
	return nil
}

// DeleteAgent deletes an agent by ID.
func (db *DB) DeleteAgent(id int64) error {
	_, err := db.Exec("DELETE FROM agents WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	return nil
}
