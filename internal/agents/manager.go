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
	// TakeOverGiteaUser explicitly hands an existing, non-Matea-managed Gitea
	// account to Matea (password reset via Admin API). Anti-hijack safeguard
	// stays on when false.
	TakeOverGiteaUser bool `json:"take_over_gitea_user,omitempty"`
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
//
// Account protection (task 1.1.3): when the Gitea user already exists but is not
// managed by Matea (no managed agent record for that username), the call refuses
// to touch the account — anti-hijack. takeOver is the explicit escape hatch: the
// operator asserts the account may be handed to Matea, so its password is reset
// via the Admin API and a fresh token is minted.
//
// The returned managed flag reports whether the account is under Matea
// management after the call (created, taken over, refreshed, or reused with a
// valid token).
func (m *Manager) EnsureGiteaAccount(username, currentToken string, takeOver bool) (token string, managed bool, err error) {
	if strings.TrimSpace(username) == "" {
		return "", false, fmt.Errorf("gitea username is empty")
	}

	user, err := m.gitea.GetUser(username)
	if err != nil {
		return "", false, fmt.Errorf("lookup gitea user: %w", err)
	}

	if user != nil && m.gitea.ValidateUserToken(username, currentToken) {
		return currentToken, true, nil
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
		log.Printf("[INFO] Created Gitea user: %s", username)
	} else {
		// User exists but token invalid - check if managed by Matea
		// Query the agent by gitea_username to check managed_by_matea flag
		existingAgent, lookupErr := m.db.GetAgentByGiteaUsername(username)
		accountManaged := lookupErr == nil && existingAgent != nil && existingAgent.ManagedByMatea
		switch {
		case accountManaged:
			// This is a Matea-managed account, safe to reset password
		case takeOver:
			// Explicit operator takeover: hand the existing account to Matea.
			log.Printf("[WARN] Taking over existing Gitea user %q on explicit request; resetting password", username)
		default:
			// Either no agent found, or agent exists but not managed by Matea
			// This is a real person's account - refuse to take over
			return "", false, fmt.Errorf("gitea user %q already exists and is not managed by Matea; please choose a different username, or enable take_over_gitea_user to hand the account to Matea (its password will be reset)", username)
		}
		if err := m.gitea.AdminUpdateUserPassword(username, password); err != nil {
			return "", false, fmt.Errorf("reset gitea user password: %w", err)
		}
		log.Printf("[INFO] Refreshed Gitea credentials for user: %s", username)
	}

	tokenResp, err := m.gitea.CreateTokenWithCredentials(username, password, agentTokenName)
	if err != nil {
		return "", true, fmt.Errorf("create gitea token: %w", err)
	}
	log.Printf("[INFO] Created Gitea token for: %s", username)
	return tokenResp.SHA1, true, nil
}

// CreateAgent registers a new agent with Gitea account and stores it in DB.
func (m *Manager) CreateAgent(req CreateAgentRequest) (*store.Agent, error) {
	var token string
	managed := false
	if m.cfg != nil && m.cfg.AutoProvision {
		t, md, err := m.EnsureGiteaAccount(req.GiteaUsername, "", req.TakeOverGiteaUser)
		if err != nil {
			return nil, err
		}
		token = t
		managed = md
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
		ManagedByMatea:  managed, // True when the Gitea account was created, refreshed, or taken over by Matea
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
// takeOver is passed through to EnsureGiteaAccount for the case where the
// configured Gitea user exists but is not yet Matea-managed.
func (m *Manager) UpdateAgent(agent *store.Agent, takeOver bool) error {
	if m.cfg != nil && m.cfg.AutoProvision {
		token, _, err := m.EnsureGiteaAccount(agent.GiteaUsername, agent.GiteaToken, takeOver)
		if err != nil {
			return err
		}
		if token != agent.GiteaToken {
			agent.GiteaToken = token
		}
		// EnsureGiteaAccount only succeeds for Matea-managed accounts; when
		// auto-provision is on, the account is under Matea's management.
		agent.ManagedByMatea = true
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
