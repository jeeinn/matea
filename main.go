package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jeeinn/matea/internal/agents"
	// Blank import registers the Hermes HubBackend factory (hub-hermes) via its
	// init(). Must stay imported so agents.buildHubBackend can construct it.
	_ "github.com/jeeinn/matea/internal/agents/backends/hermes"
	"github.com/jeeinn/matea/internal/api"
	"github.com/jeeinn/matea/internal/auth"
	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/dispatcher"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/logging"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
	giteaingress "github.com/jeeinn/matea/internal/ingress/gitea"
	"github.com/jeeinn/matea/internal/workflow"
)

//go:embed web/dist/*
var webDist embed.FS

// setContentType sets the correct Content-Type header based on file extension.
func setContentType(w http.ResponseWriter, path string) {
	switch {
	case strings.HasSuffix(path, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(path, ".mjs"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(path, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(path, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(path, ".json"):
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	case strings.HasSuffix(path, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	case strings.HasSuffix(path, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		w.Header().Set("Content-Type", "image/jpeg")
	case strings.HasSuffix(path, ".ico"):
		w.Header().Set("Content-Type", "image/x-icon")
	case strings.HasSuffix(path, ".woff"):
		w.Header().Set("Content-Type", "font/woff")
	case strings.HasSuffix(path, ".woff2"):
		w.Header().Set("Content-Type", "font/woff2")
	}
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to configuration file")
	flag.Parse()

	// Load configuration (auto-generates minimal bootstrap YAML if missing)
	loadRes, err := config.LoadWithBootstrap(*configPath)
	if err != nil {
		log.Fatalf("[FATAL] Failed to load config: %v", err)
	}
	cfg := loadRes.Config
	if loadRes.BootstrapCreated {
		log.Printf("[INFO] Bootstrap config written: %s (random jwt_secret generated)", loadRes.BootstrapPath)
		log.Printf("[INFO] Default admin: admin / admin123 — change password on first login")
		log.Printf("[INFO] Configure Gitea and LLM in Web UI → System Config")
	}
	logging.SetLevel(cfg.Logging.Level)
	closeLog, err := logging.SetupOutput(cfg.Logging.Path)
	if err != nil {
		log.Fatalf("[FATAL] Failed to setup logging: %v", err)
	}
	defer closeLog()

	// Open database
	db, err := store.Open(cfg.Database.Path)
	if err != nil {
		log.Fatalf("[FATAL] Failed to open database: %v", err)
	}
	defer db.Close()
	log.Printf("[INFO] Database opened: %s", cfg.Database.Path)

	// Initialize config manager (DB overrides on top of file config)
	cfgManager := config.NewConfigManager(cfg)
	cfgManager.SetStore(db)
	if err := cfgManager.MigrateLegacyConfigKeys(); err != nil {
		log.Printf("[WARN] Failed to migrate legacy config keys: %v", err)
	}
	if err := cfgManager.ApplyDBOverrides(); err != nil {
		log.Printf("[WARN] Failed to apply DB config overrides: %v", err)
	}

	// Register model discovery function for dynamic model listing
	config.SetModelDiscoveryFunc(llm.DiscoverModels)

	// Ensure workspace directory exists
	if err := os.MkdirAll(cfg.Workspace.BaseDir, 0755); err != nil {
		log.Fatalf("[FATAL] Failed to create workspace dir: %v", err)
	}

	// Get active config (may have DB overrides)
	activeCfg := cfgManager.Get()

	// First-run setup (Phase 2.5): while Gitea/LLM config is incomplete, arm
	// the Setup Token that gates the public /api/setup/* wizard endpoints.
	// The token is printed to the console (the root of trust); it is decoupled
	// from the default admin password and self-regenerates on expiry.
	var setupTokens *api.SetupTokenManager
	if setup := config.CheckSetup(activeCfg); setup.SetupRequired {
		setupTokens = api.NewSetupTokenManager()
		log.Printf("[INFO] Setup incomplete (missing: %s) — Web UI will open the setup wizard", strings.Join(setup.Missing, ", "))
	}

	// Initialize LLM registry
	llmRegistry := llm.NewRegistry(&activeCfg.LLM)
	llmRegistry.SetRateLimitBackoff(activeCfg.Dispatcher.RateLimitBackoff, activeCfg.LLM.RateLimitRetries)

	sandboxCfg := parseSandboxConfig(&activeCfg.Sandbox)

	// Initialize dispatcher (Router + TaskQueue + Executor)
	d := dispatcher.NewDispatcher(db, &activeCfg.Gitea, &activeCfg.Dispatcher, llmRegistry, &activeCfg.Agents, sandboxCfg, activeCfg.MCP)
	d.SetDebugConfigGetter(func() config.DebugConfig {
		return cfgManager.Get().Debug
	})
	d.SetModelMetaProvider(cfgManager)
	d.SetDeliverConfig(activeCfg.Deliver)

	// Initialize v2 workflow components
	registry := agents.NewRegistry()
	if err := registry.LoadFromDB(db); err != nil {
		log.Printf("[WARN] Failed to load agent registry: %v", err)
	}
	resolver := workflow.NewResolver(registry)
	wfMgr := workflow.NewWorkflowManager(db)
	l1Gate := workflow.NewL1Gate(db)
	sessionSvc := workflow.NewSessionService(db, activeCfg.Workspace.BaseDir)
	wfPolicy := workflow.BuildPolicy(activeCfg.Workflow.Preset, activeCfg.Workflow.Gates)
	sessionCfg := &activeCfg.Session
	if sessionCfg.IdleTTL == "" {
		defaultSessionCfg := config.DefaultSessionConfig()
		sessionCfg = &defaultSessionCfg
	}
	lifecycle := workflow.NewSessionLifecycle(db, wfMgr, sessionSvc, sessionCfg, activeCfg.Workspace.BaseDir)
	d.SetWorkflowComponents(registry, resolver, wfMgr, l1Gate, sessionSvc, wfPolicy, lifecycle)

	// Start session cleanup loop (every 10 minutes)
	lifecycle.StartCleanupLoop(10 * time.Minute)

	log.Printf("[INFO] Workflow v2 components initialized (with SessionService + Lifecycle)")

	// Start dispatcher (loads pending tasks and starts workers)
	if err := d.Start(); err != nil {
		log.Fatalf("[FATAL] Failed to start dispatcher: %v", err)
	}

	// Initialize authentication
	jwtExpiration, err := time.ParseDuration(cfg.Auth.JWTExpiration)
	if err != nil {
		jwtExpiration = 24 * time.Hour
	}
	jwtManager := auth.NewJWTManager(cfg.Auth.JWTSecret, jwtExpiration)

	// Create default admin user if needed
	if err := api.EnsureDefaultAdmin(db, cfg.Auth.DefaultAdminPassword); err != nil {
		log.Printf("[WARN] Failed to create default admin: %v", err)
	}

	// Build HTTP server
	mux := http.NewServeMux()

	// Health check (includes setup_required for ops / early UI probes)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		active := cfgManager.Get()
		setup := config.CheckSetup(active)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","version":"0.11.4","setup_required":%t}`, setup.SetupRequired)
	})

	// Webhook handler - wired to dispatcher
	webhookHandler := giteaingress.NewHandler(&activeCfg.Gitea, db.DB, d.HandleEvent)
	mux.Handle("POST /webhook/gitea", webhookHandler)
	// Recover deliveries accepted before crash (after HTTP 200, before processing finished).
	webhookHandler.ReplayAccepted()

	// Serve static files (Web UI)
	webFS, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		log.Printf("[WARN] Failed to load embedded web files: %v", err)
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			filePath := strings.TrimPrefix(path, "/")

			// Try to serve static file
			data, err := fs.ReadFile(webFS, filePath)
			if err == nil {
				setContentType(w, path)
				w.Write(data)
				return
			}

			// Only fallback to index.html for non-file requests
			if !strings.Contains(path, ".") {
				indexData, _ := fs.ReadFile(webFS, "index.html")
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write(indexData)
				return
			}

			// File not found
			http.NotFound(w, r)
		})
	}

	// Management API
	manager := agents.NewManager(db, &activeCfg.Gitea)
	manager.SetRegistry(registry)
	apiHandler := api.NewHandler(db, manager, activeCfg, jwtManager, cfgManager, func(newCfg *config.Config) {
		// Hot-reload LLM providers when config changes
		llmRegistry.Reload(&newCfg.LLM)
		llmRegistry.SetRateLimitBackoff(newCfg.Dispatcher.RateLimitBackoff, newCfg.LLM.RateLimitRetries)
		manager.ReloadGitea(&newCfg.Gitea)
		d.SetGiteaConfig(&newCfg.Gitea)
		d.SetWorkflowPolicy(workflow.BuildPolicy(newCfg.Workflow.Preset, newCfg.Workflow.Gates))
		d.SetDeliverConfig(newCfg.Deliver)
		webhookHandler.SetGiteaConfig(&newCfg.Gitea)
		log.Printf("[INFO] LLM registry, Gitea client, workflow policy, and deliver config reloaded")
	})
	apiHandler.SetIssueController(d)
	if setupTokens != nil {
		apiHandler.SetSetupTokenManager(setupTokens)
	}
	apiHandler.RegisterRoutes(mux)

	// Auth API
	authHandler := api.NewAuthHandler(db, jwtManager, cfg.Auth.DefaultAdminPassword)
	authHandler.RegisterAuthRoutes(mux)

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)

	go func() {
		listenURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
		log.Printf("[INFO] Server starting on %s (Web UI: %s)", addr, listenURL)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server failed: %v", err)
		}
	}()

	<-done
	log.Println("[INFO] Server shutting down...")

	// Cancel in-flight agent loops / LLM calls before closing HTTP.
	d.Shutdown()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("[FATAL] Server forced to shutdown: %v", err)
	}

	log.Println("[INFO] Server exited cleanly")
}

func parseSandboxConfig(cfg *config.SandboxConfig) sandbox.SandboxConfig {
	cmdTimeout, _ := time.ParseDuration(cfg.CommandTimeout)
	taskTimeout, _ := time.ParseDuration(cfg.TaskTimeout)
	cleanupAfter, _ := time.ParseDuration(cfg.CleanupAfter)

	return sandbox.SandboxConfig{
		Mode:           sandbox.SandboxMode(cfg.Mode),
		BaseDir:        cfg.BaseDir,
		CommandTimeout: cmdTimeout,
		TaskTimeout:    taskTimeout,
		MaxOutput:      cfg.MaxOutput,
		MaxFileSize:    cfg.MaxFileSize,
		CleanupAfter:   cleanupAfter,
	}
}
