package agents

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"strings"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

const agentTokenName = "matea-agent"

// Manager handles agent lifecycle (create, update, delete) and Gitea account registration.
type Manager struct {
	db       *store.DB
	gitea    *gitea.Client
	cfg      *config.GiteaConfig
	registry *Registry
}

// NewManager creates a new agent Manager.
func NewManager(db *store.DB, cfg *config.GiteaConfig) *Manager {
	return &Manager{
		db:    db,
		gitea: gitea.NewClient(cfg.URL, cfg.AdminToken),
		cfg:   cfg,
	}
}

// SetRegistry wires the in-memory agent registry for hot refresh on CRUD.
func (m *Manager) SetRegistry(registry *Registry) {
	m.registry = registry
}

// CreateAgentRequest is the payload for creating a new agent.
type CreateAgentRequest struct {
	Name            string                 `json:"name"`
	GiteaUsername   string                 `json:"gitea_username"`
	Provider        string                 `json:"provider"`
	Model           string                 `json:"model"`
	MaxOutputTokens int                    `json:"max_output_tokens"`
	MaxInputTokens  int                    `json:"max_input_tokens"`
	Temperature     float64                `json:"temperature"`
	Timeout         string                 `json:"timeout"`
	SystemPrompt    string                 `json:"system_prompt"`
	UserTemplate    string                 `json:"user_template"`
	LoopConfig      *store.AgentLoopConfig `json:"loop_config,omitempty"`
	Repos           []string               `json:"repos,omitempty"`           // Repos to add as collaborator (e.g. ["owner/repo"])
	Role            string                 `json:"role"`                      // analyze | coder | review
	Backend         string                 `json:"backend"`                   // coding backend; default "builtin"
	BackendOptions  map[string]any         `json:"backend_options,omitempty"` // backend-specific options
	ToolPack        string                 `json:"tool_pack"`                 // ToolPack name; empty = use role-based default
	McpServers      []string               `json:"mcp_servers,omitempty"`     // Enabled MCP server names
}

// ReloadGitea updates the Gitea client after config changes.
func (m *Manager) ReloadGitea(cfg *config.GiteaConfig) {
	m.gitea = gitea.NewClient(cfg.URL, cfg.AdminToken)
	m.cfg = cfg
}

// ListRepos returns all repositories from Gitea.
func (m *Manager) ListRepos() ([]gitea.RepoItem, error) {
	return m.gitea.ListRepos()
}

// AddCollaboratorToRepos adds the agent user as a collaborator to the specified repos.
func (m *Manager) AddCollaboratorToRepos(username string, repos []string) []string {
	var errors []string
	for _, repo := range repos {
		parts := splitRepo(repo)
		if len(parts) != 2 {
			errors = append(errors, fmt.Sprintf("invalid repo format: %s", repo))
			continue
		}
		if err := m.gitea.AdminAddCollaborator(parts[0], parts[1], username); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", repo, err))
		} else {
			log.Printf("[INFO] Added %s as collaborator to %s", username, repo)
		}
	}
	return errors
}

func splitRepo(fullName string) []string {
	parts := strings.SplitN(fullName, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	return parts
}

// EnsureGiteaAccount ensures the agent user exists on the current Gitea instance
// and returns a valid API token. It creates the user when missing, or refreshes
// the token when the stored token is invalid (e.g. after switching Gitea URL).
func (m *Manager) EnsureGiteaAccount(username, currentToken string) (token string, userCreated bool, err error) {
	if strings.TrimSpace(username) == "" {
		return "", false, fmt.Errorf("gitea username is empty")
	}

	user, err := m.gitea.GetUser(username)
	if err != nil {
		return "", false, fmt.Errorf("lookup gitea user: %w", err)
	}

	if user != nil && m.gitea.ValidateUserToken(username, currentToken) {
		return currentToken, false, nil
	}

	password := generatePassword()
	if user == nil {
		// User doesn't exist - create new Matea-managed account
		if _, err := m.gitea.AdminCreateUser(gitea.CreateUserRequest{
			LoginName:          username,
			Username:           username,
			Email:              username + "@gateway.local",
			Password:           password,
			SendNotify:         false,
			MustChangePassword: false,
		}); err != nil {
			return "", false, fmt.Errorf("create gitea user: %w", err)
		}
		userCreated = true
		log.Printf("[INFO] Created Gitea user: %s", username)
	} else {
		// User exists but token invalid - check if managed by Matea
		// Query the agent by gitea_username to check managed_by_matea flag
		existingAgent, err := m.db.GetAgentByGiteaUsername(username)
		if err == nil && existingAgent != nil && existingAgent.ManagedByMatea {
			// This is a Matea-managed account, safe to reset password
			if err := m.gitea.AdminUpdateUserPassword(username, password); err != nil {
				return "", false, fmt.Errorf("reset gitea user password: %w", err)
			}
			log.Printf("[INFO] Refreshed Gitea credentials for Matea-managed user: %s", username)
		} else {
			// Either no agent found, or agent exists but not managed by Matea
			// This is a real person's account - refuse to take over
			return "", false, fmt.Errorf("gitea user %q already exists and is not managed by Matea; please choose a different username or manually take over the account", username)
		}
	}

	tokenResp, err := m.gitea.CreateTokenWithCredentials(username, password, agentTokenName)
	if err != nil {
		return "", userCreated, fmt.Errorf("create gitea token: %w", err)
	}
	log.Printf("[INFO] Created Gitea token for: %s", username)
	return tokenResp.SHA1, userCreated, nil
}

// CreateAgent registers a new agent with Gitea account and stores it in DB.
func (m *Manager) CreateAgent(req CreateAgentRequest) (*store.Agent, error) {
	var token string
	var userCreated bool
	if m.cfg != nil && m.cfg.AutoProvision {
		t, uc, err := m.EnsureGiteaAccount(req.GiteaUsername, "")
		if err != nil {
			return nil, err
		}
		token = t
		userCreated = uc
	} else {
		log.Printf("[INFO] Gitea account auto-provision disabled (gitea.auto_provision=false); skipping EnsureGiteaAccount for %s", req.GiteaUsername)
	}

	role := req.Role
	if role == "" {
		role = store.RoleAnalyze
	}
	agent := &store.Agent{
		Name:            req.Name,
		GiteaUsername:   req.GiteaUsername,
		GiteaToken:      token,
		Provider:        req.Provider,
		Model:           req.Model,
		MaxOutputTokens: req.MaxOutputTokens,
		MaxInputTokens:  req.MaxInputTokens,
		Temperature:     req.Temperature,
		Timeout:         req.Timeout,
		SystemPrompt:    req.SystemPrompt,
		UserTemplate:    req.UserTemplate,
		LoopConfig:      req.LoopConfig,
		Repos:           req.Repos,
		Role:            role,
		Status:          "active",
		Backend:         req.Backend,
		BackendOptions:  req.BackendOptions,
		ToolPack:        req.ToolPack,
		McpServers:      req.McpServers,
		ManagedByMatea:  userCreated, // Mark as managed if we created the Gitea user
	}
	if err := m.db.CreateAgent(agent); err != nil {
		return nil, fmt.Errorf("store agent: %w", err)
	}

	log.Printf("[INFO] Agent created: id=%d name=%s gitea=%s", agent.ID, agent.Name, agent.GiteaUsername)
	if m.registry != nil {
		m.registry.Refresh(agent)
	}
	return agent, nil
}

// UpdateAgent updates an agent's configuration and ensures its Gitea account exists.
func (m *Manager) UpdateAgent(agent *store.Agent) error {
	if m.cfg != nil && m.cfg.AutoProvision {
		token, userCreated, err := m.EnsureGiteaAccount(agent.GiteaUsername, agent.GiteaToken)
		if err != nil {
			return err
		}
		if token != agent.GiteaToken {
			agent.GiteaToken = token
		}
		if userCreated {
			log.Printf("[INFO] Provisioned Gitea user for agent id=%d username=%s", agent.ID, agent.GiteaUsername)
		}
	} else {
		log.Printf("[INFO] Gitea account auto-provision disabled (gitea.auto_provision=false); skipping EnsureGiteaAccount for %s", agent.GiteaUsername)
	}
	if err := m.db.UpdateAgent(agent); err != nil {
		return err
	}
	if m.registry != nil {
		m.registry.Refresh(agent)
	}
	return nil
}

// DeleteAgent deletes an agent and optionally the Gitea user.
func (m *Manager) DeleteAgent(id int64, deleteGiteaUser bool) error {
	agent, err := m.db.GetAgent(id)
	if err != nil {
		return fmt.Errorf("get agent: %w", err)
	}

	// Delete Gitea user if requested
	if deleteGiteaUser && agent.GiteaUsername != "" {
		if err := m.gitea.AdminDeleteUser(agent.GiteaUsername); err != nil {
			log.Printf("[WARN] Failed to delete Gitea user %s: %v", agent.GiteaUsername, err)
			// Continue with agent deletion even if Gitea user deletion fails
		} else {
			log.Printf("[INFO] Deleted Gitea user: %s", agent.GiteaUsername)
		}
	}

	if err := m.db.DeleteAgent(id); err != nil {
		return err
	}
	if m.registry != nil {
		m.registry.Remove(id)
	}
	return nil
}

func generatePassword() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
