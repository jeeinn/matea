package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/jeeinn/matea/internal/agents"
	"github.com/jeeinn/matea/internal/auth"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"
)

// Handler serves the management API.
type Handler struct {
	db             *store.DB
	manager        *agents.Manager
	prompt         *agents.PromptManager
	auth           *AuthMiddleware
	jwtManager     *auth.JWTManager
	cfg            *config.Config
	cfgManager     *config.ConfigManager
	onConfigChange func(cfg *config.Config)
	issueCtrl      IssueController
	setupTokens    *SetupTokenManager
}

// IssueController cancels in-flight work and resets issue/task state.
// Implemented by dispatcher.Dispatcher; optional for unit tests.
type IssueController interface {
	ResetIssue(repo string, issueID int) (tasksReset int, err error)
	CancelAndResetTask(taskID int64, reason string) (*store.Task, error)
}

// NewHandler creates a new API handler.
func NewHandler(db *store.DB, manager *agents.Manager, cfg *config.Config, jwtManager *auth.JWTManager, cfgManager *config.ConfigManager, onConfigChange func(cfg *config.Config)) *Handler {
	return &Handler{
		db:             db,
		manager:        manager,
		prompt:         agents.NewPromptManager(db, &cfg.Agents),
		auth:           NewAuthMiddleware(cfg.API.AuthToken),
		jwtManager:     jwtManager,
		cfg:            cfg,
		cfgManager:     cfgManager,
		onConfigChange: onConfigChange,
	}
}

// SetIssueController wires dispatcher cancel/reset into task/session APIs.
func (h *Handler) SetIssueController(c IssueController) {
	h.issueCtrl = c
}

// SetSetupTokenManager wires the first-run setup token (C-2). Called by main
// only while setup is incomplete; a nil manager disables the setup endpoints.
func (h *Handler) SetSetupTokenManager(m *SetupTokenManager) {
	h.setupTokens = m
}

// RegisterRoutes registers all API routes on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// User management endpoints
	mux.HandleFunc("GET /api/users", h.jwtWrap(h.listUsers))
	mux.HandleFunc("POST /api/users", h.jwtWrap(h.createUser))
	mux.HandleFunc("PUT /api/users/{id}", h.jwtWrap(h.updateUser))
	mux.HandleFunc("DELETE /api/users/{id}", h.jwtWrap(h.deleteUser))

	// Repo endpoints
	mux.HandleFunc("GET /api/repos", h.jwtWrap(h.listRepos))

	// System config endpoints
	mux.HandleFunc("GET /api/config", h.jwtWrap(h.getConfig))
	mux.HandleFunc("PUT /api/config", h.jwtWrap(h.updateConfig))
	mux.HandleFunc("DELETE /api/config/{key}", h.jwtWrap(h.deleteConfigEntry))
	mux.HandleFunc("GET /api/config/providers/{name}/models", h.jwtWrap(h.getProviderModels))
	mux.HandleFunc("GET /api/config/provider-presets", h.jwtWrap(h.listProviderPresets))
	mux.HandleFunc("POST /api/config/discover-models", h.jwtWrap(h.discoverModelsHandler))
	mux.HandleFunc("GET /api/config/export", h.jwtWrap(h.handleConfigExport))
	mux.HandleFunc("POST /api/config/import", h.jwtWrap(h.handleConfigImport))
	mux.HandleFunc("POST /api/config/gitea-webhook", h.jwtWrap(h.giteaWebhookHandler))
	mux.HandleFunc("POST /api/config/test/gitea", h.jwtWrap(h.testGiteaConfig))
	mux.HandleFunc("POST /api/config/test/llm", h.jwtWrap(h.testLLMConfig))

	// Health summary (Phase 2.5 C-18/C-19/C-22). Per-component readiness of
	// every external dependency; JWT required.
	mux.HandleFunc("GET /api/health/summary", h.jwtWrap(h.handleHealthSummary))

	// First-run setup (Phase 2.5). Status is PUBLIC: the unauthenticated Web
	// UI needs it to choose between the wizard and the login page (it exposes
	// only missing-key names, never values). Mutation endpoints are gated by
	// the console-printed Setup Token (C-2) and self-disable once setup is
	// complete.
	mux.HandleFunc("GET /api/setup/status", h.getSetupStatus)
	mux.HandleFunc("POST /api/setup/verify", h.requireSetupToken(h.verifySetupToken))
	mux.HandleFunc("GET /api/setup/detect", h.requireSetupToken(h.detectLocalServices))
	mux.HandleFunc("POST /api/setup/test/gitea", h.requireSetupToken(h.testSetupGitea))
	mux.HandleFunc("POST /api/setup/test/llm", h.requireSetupToken(h.testSetupLLM))
	mux.HandleFunc("POST /api/setup/complete", h.requireSetupToken(h.completeSetup))
	mux.HandleFunc("GET /api/setup/provider-presets", h.requireSetupToken(h.listProviderPresets))
	mux.HandleFunc("POST /api/setup/discover-models", h.requireSetupToken(h.discoverModelsHandler))
	mux.HandleFunc("GET /api/setup/env-detection", h.requireSetupToken(h.handleEnvDetection))
	mux.HandleFunc("POST /api/setup/apply-env", h.requireSetupToken(h.handleApplyEnv))

	// Prompt template endpoints
	mux.HandleFunc("GET /api/prompt-templates", h.authorizeWrap(h.listPromptTemplates))
	mux.HandleFunc("PUT /api/prompt-templates", h.jwtWrap(h.updatePromptTemplates))
	mux.HandleFunc("DELETE /api/prompt-templates/{name}", h.jwtWrap(h.deletePromptTemplate))

	// Agent endpoints
	mux.HandleFunc("GET /api/agents", h.authorizeWrap(h.listAgents))
	mux.HandleFunc("POST /api/agents", h.authorizeWrap(h.createAgent))
	mux.HandleFunc("GET /api/agents/{id}", h.authorizeWrap(h.getAgent))
	mux.HandleFunc("PUT /api/agents/{id}", h.authorizeWrap(h.updateAgent))
	mux.HandleFunc("DELETE /api/agents/{id}", h.authorizeWrap(h.deleteAgent))
	mux.HandleFunc("GET /api/agents/{id}/tasks", h.authorizeWrap(h.listAgentTasks))
	mux.HandleFunc("GET /api/tasks", h.authorizeWrap(h.listTasks))
	mux.HandleFunc("GET /api/tasks/{id}", h.authorizeWrap(h.getTask))
	mux.HandleFunc("GET /api/tasks/{id}/conversation", h.authorizeWrap(h.getTaskConversation))
	mux.HandleFunc("POST /api/tasks/{id}/reset", h.authorizeWrap(h.resetTask))
	mux.HandleFunc("GET /api/logs", h.authorizeWrap(h.listLogs))
	mux.HandleFunc("GET /api/stats", h.authorizeWrap(h.getStats))
	mux.HandleFunc("GET /api/templates", h.authorizeWrap(h.listTemplates))

	// Prompt management endpoints
	mux.HandleFunc("GET /api/agents/{id}/prompts", h.authorizeWrap(h.listPrompts))
	mux.HandleFunc("POST /api/agents/{id}/prompts", h.authorizeWrap(h.createPrompt))
	mux.HandleFunc("GET /api/agents/{id}/prompts/active", h.authorizeWrap(h.getActivePrompt))
	mux.HandleFunc("POST /api/prompts/{id}/activate", h.authorizeWrap(h.activatePrompt))
	mux.HandleFunc("DELETE /api/prompts/{id}", h.authorizeWrap(h.deletePrompt))

	// Session reset endpoint
	mux.HandleFunc("POST /api/sessions/reset", h.authorizeWrap(h.resetSession))

	// Workflow context endpoints
	mux.HandleFunc("GET /api/workflow-context", h.authorizeWrap(h.listWorkflowContexts))

	// Workflow policy endpoints (per-repo override).
	// {repo...} matches owner/name (slashes); authorizeWrap accepts JWT or API token.
	mux.HandleFunc("GET /api/workflow-policies", h.authorizeWrap(h.listWorkflowPolicies))
	mux.HandleFunc("GET /api/workflow-policies/{repo...}", h.authorizeWrap(h.getWorkflowPolicy))
	mux.HandleFunc("PUT /api/workflow-policies/{repo...}", h.authorizeWrap(h.upsertWorkflowPolicy))
	mux.HandleFunc("DELETE /api/workflow-policies/{repo...}", h.authorizeWrap(h.deleteWorkflowPolicy))
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func parseID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}
