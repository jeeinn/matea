package agents

import (
	"context"
	"testing"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ResolveHubBackend (task 1.2.4) -----------------------------------------

func newHubDispatchFactory(t *testing.T, backends *config.AgentBackendsConfig) *RunnerFactory {
	t.Helper()
	return NewRunnerFactory(llm.NewRegistry(nil), nil, nil, config.DefaultAgentDefaults(), config.DefaultAgentLoopConfig(), nil, backends, nil, sandbox.DefaultConfig(), nil, "")
}

func TestResolveHubBackendBuiltin(t *testing.T) {
	factory := newHubDispatchFactory(t, nil)

	for _, backendName := range []string{"", "builtin", "internal" /* legacy */} {
		b, err := factory.ResolveHubBackend(&store.Agent{Backend: backendName})
		require.NoError(t, err, "backend %q should resolve to builtin", backendName)
		assert.Equal(t, "builtin", b.Name())
	}
}

func TestResolveHubBackendNamedOpenCodeInstance(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {Type: config.BackendTypeHubOpenCode, BaseURL: srv.URL},
		},
	}
	factory := newHubDispatchFactory(t, backends)

	b, err := factory.ResolveHubBackend(&store.Agent{Backend: "opencode-local"})
	require.NoError(t, err)
	assert.Equal(t, "opencode-local", b.Name())

	// The registry returns the shared singleton so Submit→Poll affinity holds.
	again, err := factory.ResolveHubBackend(&store.Agent{Backend: "opencode-local"})
	require.NoError(t, err)
	assert.Same(t, b, again, "hub-opencode instances must be shared singletons")
}

func TestResolveHubBackendNamedBuiltinType(t *testing.T) {
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"my-builtin": {Type: config.BackendTypeBuiltin},
		},
	}
	factory := newHubDispatchFactory(t, backends)

	b, err := factory.ResolveHubBackend(&store.Agent{Backend: "my-builtin"})
	require.NoError(t, err)
	assert.Equal(t, "builtin", b.Name())
}

// TestResolveHubBackendReservedHubNamesError pins the Phase 1 contract:
// hub-hermes / hub-openclaw / hub-api (and any unconfigured hub-*) fail with
// an explicit not-implemented error — never a silent fallback to builtin.
func TestResolveHubBackendReservedHubNamesError(t *testing.T) {
	factory := newHubDispatchFactory(t, nil)

	for _, name := range []string{"hub-hermes", "hub-openclaw", "hub-api", "hub-future"} {
		_, err := factory.ResolveHubBackend(&store.Agent{Backend: name})
		require.Error(t, err, "reserved hub backend %q must error", name)
		assert.Contains(t, err.Error(), "not implemented in Phase 1")
	}
}

// TestResolveHubBackendUnknownMustError pins the no-silent-fallback rule for
// names that are neither builtin, nor hub-*, nor configured.
func TestResolveHubBackendUnknownMustError(t *testing.T) {
	factory := newHubDispatchFactory(t, nil)

	for _, name := range []string{"hub_opencode" /* typo: underscore */, "opencode", "Hub-hermes" /* case */} {
		_, err := factory.ResolveHubBackend(&store.Agent{Backend: name})
		require.Error(t, err, "unknown backend %q must error", name)
	}
}

func TestResolveHubBackendMisconfiguredOpenCode(t *testing.T) {
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			// Missing BaseURL: registration is skipped at factory build;
			// resolution must surface the precise construction error.
			"broken-oc": {Type: config.BackendTypeHubOpenCode},
		},
	}
	factory := newHubDispatchFactory(t, backends)

	_, err := factory.ResolveHubBackend(&store.Agent{Backend: "broken-oc"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "misconfigured")
	assert.Contains(t, err.Error(), "base_url")
}

// --- Runner-level dispatch branch --------------------------------------------

// TestRunnersRejectReservedHubBackend verifies the 1.2.4 branch is wired into
// all runners: a reserved hub-* backend fails the task before any LLM, Gitea,
// or workspace work happens.
func TestRunnersRejectReservedHubBackend(t *testing.T) {
	factory := newHubDispatchFactory(t, nil)
	agent := &store.Agent{Backend: "hub-hermes", Provider: "mock", Model: "m"}
	task := &store.Task{ID: 1, Repo: "owner/repo", IssueID: 2}

	runners := map[string]Runner{
		"analyze": NewAnalyzeRunner(factory),
		"review":  NewReviewRunner(factory),
		"reply":   NewInteractionRunner(factory),
		"dev":     NewDevRunner(factory),
		"bugfix":  NewBugfixRunner(factory),
	}
	for name, r := range runners {
		_, err := r.Run(context.Background(), task, agent)
		require.Error(t, err, "runner %s must reject hub-hermes", name)
		assert.Contains(t, err.Error(), "not implemented in Phase 1", "runner %s", name)
	}
}

// TestRunnersRejectUnknownBackend verifies unknown (non hub-*) backend names
// also fail loudly in every runner.
func TestRunnersRejectUnknownBackend(t *testing.T) {
	factory := newHubDispatchFactory(t, nil)
	agent := &store.Agent{Backend: "not-a-backend", Provider: "mock", Model: "m"}
	task := &store.Task{ID: 1, Repo: "owner/repo", IssueID: 2}

	runners := map[string]Runner{
		"analyze": NewAnalyzeRunner(factory),
		"review":  NewReviewRunner(factory),
		"reply":   NewInteractionRunner(factory),
		"dev":     NewDevRunner(factory),
		"bugfix":  NewBugfixRunner(factory),
	}
	for name, r := range runners {
		_, err := r.Run(context.Background(), task, agent)
		require.Error(t, err, "runner %s must reject unknown backend", name)
		assert.Contains(t, err.Error(), "unknown backend", "runner %s", name)
	}
}

// TestRunnerDispatchPassesForValidSelections verifies the branch does not
// block valid selections. Write runners proceed into the CodingBackend path
// (which needs Gitea/workspace and is covered by existing tests); here the
// analyze runner proves dispatch passed by failing *later* on the
// unregistered provider — not at the hub check.
func TestRunnerDispatchPassesForValidSelections(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {Type: config.BackendTypeHubOpenCode, BaseURL: srv.URL},
		},
	}
	factory := newHubDispatchFactory(t, backends)
	task := &store.Task{ID: 1, Repo: "owner/repo", IssueID: 2}

	// Direct validation: builtin and hub-opencode selections pass.
	require.NoError(t, factory.validateHubDispatch(&store.Agent{Backend: ""}))
	require.NoError(t, factory.validateHubDispatch(&store.Agent{Backend: "opencode-local"}))

	// Runner level: analyze with a hub-opencode agent passes dispatch (write-
	// only is enforced at Submit, Phase 2) and fails later on the provider.
	_, err := NewAnalyzeRunner(factory).Run(context.Background(), task, &store.Agent{
		Backend: "opencode-local", Provider: "missing", Model: "m",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "provider")
	assert.NotContains(t, err.Error(), "not implemented")
	assert.NotContains(t, err.Error(), "unknown backend")
}

// TestHubRegistryContainsBuiltinAndInstances pins NewRunnerFactory registry
// construction: builtin plus each configured hub-opencode instance.
func TestHubRegistryContainsBuiltinAndInstances(t *testing.T) {
	srv := newTestOpenCodeServer(t, nil)
	backends := &config.AgentBackendsConfig{
		Backends: map[string]config.BackendConfig{
			"opencode-local": {Type: config.BackendTypeHubOpenCode, BaseURL: srv.URL},
		},
	}
	factory := newHubDispatchFactory(t, backends)

	names := factory.hubRegistry.Names()
	assert.Contains(t, names, "builtin")
	assert.Contains(t, names, "opencode-local")
	assert.Len(t, names, 2)

	// Registered opencode instance is healthy against the mock sidecar.
	oc, err := factory.hubRegistry.Lookup("opencode-local")
	require.NoError(t, err)
	require.NoError(t, oc.HealthCheck(context.Background()))
}
