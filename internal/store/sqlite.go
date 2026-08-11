package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// DB wraps the SQLite database connection.
type DB struct {
	*sql.DB
}

// Open creates or opens the SQLite database at the given path.
// It also creates all required tables if they don't exist.
func Open(dbPath string) (*DB, error) {
	// Ensure parent directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	sqlDB, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=10000&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Set connection pool settings for SQLite
	sqlDB.SetMaxOpenConns(1) // SQLite only supports one writer at a time
	sqlDB.SetMaxIdleConns(1)

	// Enable foreign keys
	if _, err := sqlDB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	db := &DB{sqlDB}
	if err := db.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// migrate creates all required tables.
func (db *DB) migrate() error {
	// Step 1: Create tables (IF NOT EXISTS is safe to repeat)
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS agents (
			id                 INTEGER PRIMARY KEY AUTOINCREMENT,
			name               TEXT NOT NULL,
			gitea_username     TEXT NOT NULL UNIQUE,
			gitea_token        TEXT NOT NULL,
			avatar_url         TEXT DEFAULT '',
			provider           TEXT NOT NULL DEFAULT 'deepseek',
			model              TEXT NOT NULL DEFAULT 'deepseek-v4-flash',
			max_output_tokens  INTEGER DEFAULT 8192,
			max_input_tokens   INTEGER DEFAULT 115200,
			temperature        REAL DEFAULT 0.3,
			timeout            TEXT DEFAULT '5m',
			system_prompt      TEXT NOT NULL DEFAULT '',
			user_template      TEXT NOT NULL DEFAULT '',
			status             TEXT DEFAULT 'active',
			backend            TEXT DEFAULT 'internal',
			backend_options    TEXT DEFAULT '{}',
			created_at         DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at         DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS routes (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			event       TEXT NOT NULL,
			action      TEXT DEFAULT '',
			label       TEXT DEFAULT '',
			assignee    TEXT DEFAULT '',
			mention     TEXT DEFAULT '',
			agent_id    INTEGER NOT NULL,
			priority    INTEGER DEFAULT 0,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			event       TEXT NOT NULL,
			repo        TEXT NOT NULL,
			issue_id    INTEGER NOT NULL,
			agent_id    INTEGER NOT NULL,
			task_type   TEXT NOT NULL DEFAULT 'trigger',
			context     TEXT DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'pending',
			priority    INTEGER DEFAULT 0,
			delivery_id TEXT DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			started_at  DATETIME,
			finished_at DATETIME,
			result      TEXT DEFAULT '',
			error       TEXT DEFAULT '',
			FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS prompt_history (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id      INTEGER NOT NULL,
			system_prompt TEXT NOT NULL,
			user_template TEXT NOT NULL,
			version       INTEGER NOT NULL,
			is_active     INTEGER DEFAULT 1,
			note          TEXT DEFAULT '',
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_by    TEXT DEFAULT 'admin',
			FOREIGN KEY (agent_id) REFERENCES agents(id) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS processed_deliveries (
			delivery_id TEXT PRIMARY KEY,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS operation_logs (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_id    INTEGER,
			task_id     INTEGER,
			action      TEXT NOT NULL,
			detail      TEXT DEFAULT '',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role          TEXT NOT NULL DEFAULT 'user',
			display_name  TEXT DEFAULT '',
			email         TEXT DEFAULT '',
			is_active     INTEGER DEFAULT 1,
			must_change_password INTEGER NOT NULL DEFAULT 0,
			last_login    DATETIME,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_agent_id ON tasks(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_delivery_id ON tasks(delivery_id)`,
		`CREATE TABLE IF NOT EXISTS task_usage (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id           INTEGER NOT NULL,
			provider          TEXT NOT NULL,
			model             TEXT NOT NULL,
			prompt_tokens     INTEGER DEFAULT 0,
			completion_tokens INTEGER DEFAULT 0,
			total_tokens      INTEGER DEFAULT 0,
			cost              REAL DEFAULT 0.0,
			created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_usage_task_id ON task_usage(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		`CREATE TABLE IF NOT EXISTS system_config (
			key         TEXT PRIMARY KEY,
			value       TEXT NOT NULL,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS workflow_contexts (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			repo            TEXT NOT NULL,
			issue_id        INTEGER NOT NULL DEFAULT 0,
			pr_id           INTEGER NOT NULL DEFAULT 0,
			stage           TEXT NOT NULL DEFAULT 'idle',
			active_agent_id INTEGER NOT NULL DEFAULT 0,
			active_role     TEXT NOT NULL DEFAULT '',
			session_id      TEXT NOT NULL DEFAULT '',
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(repo, issue_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_repo_issue ON workflow_contexts(repo, issue_id)`,
		`CREATE TABLE IF NOT EXISTS agent_sessions (
			id              TEXT PRIMARY KEY,
			repo            TEXT NOT NULL,
			issue_id        INTEGER NOT NULL DEFAULT 0,
			pr_id           INTEGER NOT NULL DEFAULT 0,
			agent_id        INTEGER NOT NULL,
			role            TEXT NOT NULL,
			status          TEXT NOT NULL DEFAULT 'active',
			branch          TEXT NOT NULL DEFAULT '',
			workspace_path  TEXT NOT NULL DEFAULT '',
			last_task_id    INTEGER NOT NULL DEFAULT 0,
			message_count   INTEGER NOT NULL DEFAULT 0,
			last_active_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_repo_issue ON agent_sessions(repo, issue_id)`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_status ON agent_sessions(status)`,
		`CREATE TABLE IF NOT EXISTS task_conversation_logs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id      INTEGER NOT NULL,
			iteration    INTEGER NOT NULL,
			seq          INTEGER NOT NULL,
			role         TEXT NOT NULL,
			content      TEXT DEFAULT '',
			tool_calls   TEXT DEFAULT '',
			tool_call_id TEXT DEFAULT '',
			created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (task_id) REFERENCES tasks(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_task_conv_logs_task_id ON task_conversation_logs(task_id)`,
		`CREATE INDEX IF NOT EXISTS idx_task_conv_logs_task_iter ON task_conversation_logs(task_id, iteration)`,
		`CREATE TABLE IF NOT EXISTS workflow_policies (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			repo        TEXT NOT NULL UNIQUE,
			preset      TEXT DEFAULT 'standard',
			gates       TEXT DEFAULT '{}',
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workflow_policies_repo ON workflow_policies(repo)`,
		`CREATE TABLE IF NOT EXISTS memories (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			repo        TEXT NOT NULL,
			issue_id    INTEGER NOT NULL DEFAULT 0,
			key         TEXT NOT NULL,
			value       TEXT NOT NULL,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(repo, issue_id, key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_memories_repo_issue ON memories(repo, issue_id)`,
		// Hub handle persistence (HubBackend contract §1.2.1): the persistable
		// reference to a task submitted to a hub backend, keyed by task_id so
		// the Executor can re-attach polling after a restart and idempotent
		// re-submission can reuse the same remote run instead of double-starting.
		`CREATE TABLE IF NOT EXISTS hub_handles (
			task_id         INTEGER PRIMARY KEY,
			backend         TEXT NOT NULL,
			remote_id       TEXT NOT NULL,
			idempotency_key TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'running',
			created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_hub_handles_status ON hub_handles(status)`,
	}

	for _, m := range migrations {
		if _, err := db.Exec(m); err != nil {
			return fmt.Errorf("execute migration: %w\nSQL: %s", err, m)
		}
	}

	// Step 2: Schema evolution (ALTER TABLE) — runs after tables exist
	additionalMigrations := []string{
		`ALTER TABLE prompt_history ADD COLUMN is_active INTEGER DEFAULT 1`,
		`ALTER TABLE prompt_history ADD COLUMN note TEXT DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_history_agent_id ON prompt_history(agent_id)`,
		`CREATE INDEX IF NOT EXISTS idx_prompt_history_is_active ON prompt_history(is_active)`,
		`ALTER TABLE agents ADD COLUMN loop_config TEXT DEFAULT '{}'`,
		`ALTER TABLE agents ADD COLUMN repos TEXT DEFAULT '[]'`,
		`ALTER TABLE tasks ADD COLUMN base_branch TEXT DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN role TEXT NOT NULL DEFAULT 'analyze'`,
		`ALTER TABLE tasks ADD COLUMN session_id TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN role TEXT DEFAULT ''`,
		`ALTER TABLE tasks ADD COLUMN pr_id INTEGER NOT NULL DEFAULT 0`, // P0: PR number for review_pr tasks
		`ALTER TABLE workflow_contexts ADD COLUMN previous_stage TEXT DEFAULT ''`,
		`ALTER TABLE agents ADD COLUMN max_output_tokens INTEGER DEFAULT 2048`,
		`ALTER TABLE agents ADD COLUMN max_input_tokens INTEGER DEFAULT 65536`,
		`ALTER TABLE agents ADD COLUMN timeout TEXT DEFAULT '5m'`,
		`ALTER TABLE task_usage ADD COLUMN cost REAL DEFAULT 0.0`,
		`ALTER TABLE agents ADD COLUMN backend TEXT DEFAULT 'builtin'`,    // OpenCode Path A; canonical name since 1.2.6
		`ALTER TABLE agents ADD COLUMN backend_options TEXT DEFAULT '{}'`, // OpenCode Path A
		`ALTER TABLE agents ADD COLUMN tool_pack TEXT DEFAULT ''`,         // P1.4: ToolPack per agent
		`ALTER TABLE agents ADD COLUMN mcp_servers TEXT DEFAULT '[]'`,     // P2.8: MCP servers per agent
		`ALTER TABLE agents ADD COLUMN managed_by_matea INTEGER DEFAULT 0`, // Phase 1.1.3: track Matea-managed Gitea accounts
		`DROP TABLE IF EXISTS routes`,                                     // v2: routes table removed (Assign model replaces Label trigger)
		// Webhook inbox: accept-before-200, replay accepted on startup
		`ALTER TABLE processed_deliveries ADD COLUMN status TEXT NOT NULL DEFAULT 'processed'`,
		`ALTER TABLE processed_deliveries ADD COLUMN event_type TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE processed_deliveries ADD COLUMN payload BLOB`,
		`ALTER TABLE users ADD COLUMN must_change_password INTEGER NOT NULL DEFAULT 0`,
	}

	for _, m := range additionalMigrations {
		if _, err := db.Exec(m); err != nil {
			// Ignore "duplicate column" and "no such table" errors
			if !isDuplicateColumnError(err) && !isNoSuchTableError(err) {
				return fmt.Errorf("execute additional migration: %w\nSQL: %s", err, m)
			}
		}
	}

	if err := db.migrateAgentTokenBudget(); err != nil {
		return err
	}

	if err := db.migrateBackendIdentifiers(); err != nil {
		return err
	}

	return nil
}

// migrateBackendIdentifiers rewrites legacy coding-backend identifiers to
// canonical names (task 1.2.6d): 'internal'/'' → 'builtin', 'opencode_http' →
// 'hub-opencode'. Idempotent; safe to run on every startup. Read-side
// normalization (config.NormalizeBackend) also accepts legacy values, so this
// migration is a convergence step, not a requirement for correctness.
func (db *DB) migrateBackendIdentifiers() error {
	if _, err := db.Exec(`UPDATE agents SET backend='builtin' WHERE backend IN ('internal','')`); err != nil {
		return fmt.Errorf("migrate backend identifiers (builtin): %w", err)
	}
	if _, err := db.Exec(`UPDATE agents SET backend='hub-opencode' WHERE backend='opencode_http'`); err != nil {
		return fmt.Errorf("migrate backend identifiers (hub-opencode): %w", err)
	}
	return nil
}

// migrateAgentTokenBudget backfills max_output_tokens from legacy columns (idempotent).
// Must not nest Query/QueryRow while another rows cursor is open — SQLite MaxOpenConns=1 would deadlock.
func (db *DB) migrateAgentTokenBudget() error {
	hasLegacy, err := db.hasColumn("agents", "max_tokens")
	if err != nil {
		return err
	}

	query := `SELECT id, max_output_tokens, max_input_tokens, timeout, COALESCE(loop_config, '{}') FROM agents`
	if hasLegacy {
		query = `SELECT id, max_output_tokens, max_input_tokens, timeout, COALESCE(loop_config, '{}'), COALESCE(max_tokens, 0) FROM agents`
	}

	rows, err := db.Query(query)
	if err != nil {
		return fmt.Errorf("migrate agent budget query: %w", err)
	}

	type row struct {
		id       int64
		out      int
		in       int
		timeout  string
		loopJSON string
		legacy   int
	}
	var updates []row
	for rows.Next() {
		var r row
		if hasLegacy {
			err = rows.Scan(&r.id, &r.out, &r.in, &r.timeout, &r.loopJSON, &r.legacy)
		} else {
			err = rows.Scan(&r.id, &r.out, &r.in, &r.timeout, &r.loopJSON)
		}
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}

	for _, r := range updates {
		out := r.out
		if r.legacy > out {
			out = r.legacy
		}
		var loopCfg struct {
			MaxTokens int `json:"max_tokens"`
		}
		_ = json.Unmarshal([]byte(r.loopJSON), &loopCfg)
		if loopCfg.MaxTokens > out {
			out = loopCfg.MaxTokens
		}
		if out <= 0 {
			out = 8192
		}
		in := r.in
		if in <= 0 {
			in = 115200
		}
		timeout := r.timeout
		if timeout == "" {
			timeout = "5m"
		}

		// Strip deprecated loop_config keys
		cleaned := map[string]interface{}{}
		_ = json.Unmarshal([]byte(r.loopJSON), &cleaned)
		delete(cleaned, "max_tokens")
		delete(cleaned, "timeout")
		cleanedJSON, _ := json.Marshal(cleaned)
		if string(cleanedJSON) == "null" {
			cleanedJSON = []byte("{}")
		}

		_, err := db.Exec(`UPDATE agents SET max_output_tokens=?, max_input_tokens=?, timeout=?, loop_config=? WHERE id=?`,
			out, in, timeout, string(cleanedJSON), r.id)
		if err != nil {
			return fmt.Errorf("migrate agent %d: %w", r.id, err)
		}
	}
	return nil
}

func (db *DB) hasColumn(table, column string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, nil
}

// isDuplicateColumnError checks if the error is a "duplicate column" error.
func isDuplicateColumnError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "duplicate column") || contains(err.Error(), "already exists")
}

// isNoSuchTableError checks if the error is a "no such table" error.
func isNoSuchTableError(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "no such table")
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
