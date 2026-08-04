package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAgentBackendDefaultsToBuiltin(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{
		Name:          "coder-1",
		GiteaUsername: "coder-1",
		GiteaToken:    "tok",
		Provider:      "deepseek",
		Model:         "deepseek-chat",
		Role:          RoleCoder,
		Status:        "active",
		// Backend intentionally left empty
	}
	require.NoError(t, db.CreateAgent(agent))

	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "builtin", got.Backend)
	assert.Nil(t, got.BackendOptions)
}

func TestCreateAgentWithBackendAndOptions(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{
		Name:           "coder-oc",
		GiteaUsername:  "coder-oc",
		GiteaToken:     "tok",
		Provider:       "deepseek",
		Model:          "deepseek-chat",
		Role:           RoleCoder,
		Status:         "active",
		Backend:        "opencode-local",
		BackendOptions: map[string]any{"opencode_model": "claude-sonnet", "inject_system_prompt": true},
	}
	require.NoError(t, db.CreateAgent(agent))

	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "opencode-local", got.Backend)
	assert.Equal(t, "claude-sonnet", got.BackendOptions["opencode_model"])
	assert.Equal(t, true, got.BackendOptions["inject_system_prompt"])
}

func TestUpdateAgentBackend(t *testing.T) {
	db := newTestDB(t)

	agent := &Agent{
		Name:          "coder-2",
		GiteaUsername: "coder-2",
		GiteaToken:    "tok",
		Role:          RoleCoder,
		Status:        "active",
	}
	require.NoError(t, db.CreateAgent(agent))

	// Switch to opencode backend
	agent.Backend = "opencode-local"
	agent.BackendOptions = map[string]any{"opencode_session_id": "abc-123"}
	require.NoError(t, db.UpdateAgent(agent))

	got, err := db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "opencode-local", got.Backend)
	assert.Equal(t, "abc-123", got.BackendOptions["opencode_session_id"])

	// Switch back to builtin (clear options)
	agent.Backend = "builtin"
	agent.BackendOptions = nil
	require.NoError(t, db.UpdateAgent(agent))

	got, err = db.GetAgent(agent.ID)
	require.NoError(t, err)
	assert.Equal(t, "builtin", got.Backend)
}

func TestListAgentsIncludesBackend(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.CreateAgent(&Agent{
		Name: "a", GiteaUsername: "u-a", GiteaToken: "t", Role: RoleAnalyze, Status: "active",
	}))
	require.NoError(t, db.CreateAgent(&Agent{
		Name: "c", GiteaUsername: "u-c", GiteaToken: "t", Role: RoleCoder, Status: "active",
		Backend: "opencode-local",
	}))

	agents, err := db.ListAgents()
	require.NoError(t, err)
	require.Len(t, agents, 2)

	byName := map[string]*Agent{}
	for _, a := range agents {
		byName[a.Name] = a
	}
	assert.Equal(t, "builtin", byName["a"].Backend)
	assert.Equal(t, "opencode-local", byName["c"].Backend)
}

// TestMigrateBackendIdentifiers verifies the 1.2.6(d) idempotent data
// migration: legacy identifiers converge to canonical names; named backends
// and already-canonical values are untouched; running twice is a no-op.
func TestMigrateBackendIdentifiers(t *testing.T) {
	db := newTestDB(t)

	require.NoError(t, db.CreateAgent(&Agent{
		Name: "legacy-internal", GiteaUsername: "u-li", GiteaToken: "t", Role: RoleCoder, Status: "active",
	}))
	require.NoError(t, db.CreateAgent(&Agent{
		Name: "legacy-type", GiteaUsername: "u-lt", GiteaToken: "t", Role: RoleCoder, Status: "active",
	}))
	require.NoError(t, db.CreateAgent(&Agent{
		Name: "named", GiteaUsername: "u-n", GiteaToken: "t", Role: RoleCoder, Status: "active",
		Backend: "opencode-local",
	}))
	require.NoError(t, db.CreateAgent(&Agent{
		Name: "already-canonical", GiteaUsername: "u-ac", GiteaToken: "t", Role: RoleCoder, Status: "active",
		Backend: "builtin",
	}))

	// Simulate pre-1.2.6 rows: legacy identifiers written directly.
	_, err := db.Exec(`UPDATE agents SET backend='internal' WHERE name='legacy-internal'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE agents SET backend='opencode_http' WHERE name='legacy-type'`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE agents SET backend='' WHERE name='already-canonical'`)
	require.NoError(t, err)

	// Run twice to prove idempotency.
	require.NoError(t, db.migrateBackendIdentifiers())
	require.NoError(t, db.migrateBackendIdentifiers())

	byName := map[string]*Agent{}
	agents, err := db.ListAgents()
	require.NoError(t, err)
	for _, a := range agents {
		byName[a.Name] = a
	}
	assert.Equal(t, "builtin", byName["legacy-internal"].Backend)
	assert.Equal(t, "hub-opencode", byName["legacy-type"].Backend)
	assert.Equal(t, "opencode-local", byName["named"].Backend, "named backends must not be touched")
	assert.Equal(t, "builtin", byName["already-canonical"].Backend, "empty backend converges to builtin")
}
