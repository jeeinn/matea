package agents

import (
	"fmt"
	"sort"
	"sync"

	"github.com/jeeinn/matea/internal/config"
)

// HubBackendFactory constructs a HubBackend instance from a named config entry.
//
// Sub-packages (e.g. internal/agents/backends/hermes) register themselves via
// RegisterHubBackendFactory so the agents package can construct them on demand
// without importing the sub-package directly. A direct import would create a
// cycle: agents ↔ backends/hermes (the sub-package imports agents to satisfy
// the HubBackend interface). The init()-based registry keeps the dependency
// one-directional (sub-package → agents) and scales to future hub types
// (openclaw / api) without touching the agents package.
type HubBackendFactory func(name string, cfg config.BackendConfig) (HubBackend, error)

var (
	hubBackendFactoryMu sync.RWMutex
	hubBackendFactories = map[string]HubBackendFactory{}
)

// RegisterHubBackendFactory registers a constructor for a hub backend type
// (e.g. config.BackendTypeHubHermes). Intended to be called from a sub-package
// init() function.
func RegisterHubBackendFactory(backendType string, f HubBackendFactory) {
	hubBackendFactoryMu.Lock()
	defer hubBackendFactoryMu.Unlock()
	hubBackendFactories[config.NormalizeBackend(backendType)] = f
}

// UnregisterHubBackendFactory removes a registered constructor. The registry
// is process-global mutable state, so tests that install a stub factory must
// be able to undo it — otherwise a later test (or a parallel one) inherits the
// stub. Production code never calls this.
func UnregisterHubBackendFactory(backendType string) {
	hubBackendFactoryMu.Lock()
	defer hubBackendFactoryMu.Unlock()
	delete(hubBackendFactories, config.NormalizeBackend(backendType))
}

// SnapshotHubBackendFactories returns a copy of the current registry, and
// RestoreHubBackendFactories replaces the registry with such a copy. Together
// they give tests a save/restore pair around registry mutation:
//
//	defer agents.RestoreHubBackendFactories(agents.SnapshotHubBackendFactories())
func SnapshotHubBackendFactories() map[string]HubBackendFactory {
	hubBackendFactoryMu.RLock()
	defer hubBackendFactoryMu.RUnlock()
	out := make(map[string]HubBackendFactory, len(hubBackendFactories))
	for k, v := range hubBackendFactories {
		out[k] = v
	}
	return out
}

// RestoreHubBackendFactories replaces the registry contents with the snapshot.
func RestoreHubBackendFactories(snapshot map[string]HubBackendFactory) {
	hubBackendFactoryMu.Lock()
	defer hubBackendFactoryMu.Unlock()
	hubBackendFactories = make(map[string]HubBackendFactory, len(snapshot))
	for k, v := range snapshot {
		hubBackendFactories[k] = v
	}
}

// registeredHubBackendTypes returns the sorted list of hub backend types that
// have a registered constructor. Used to build accurate "supported types"
// error messages instead of a hardcoded, quickly-stale list.
func registeredHubBackendTypes() []string {
	hubBackendFactoryMu.RLock()
	defer hubBackendFactoryMu.RUnlock()
	out := make([]string, 0, len(hubBackendFactories))
	for k := range hubBackendFactories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildHubBackend constructs a HubBackend instance for a configured backend
// entry by dispatching on its normalized type. Returns an error if no factory
// is registered for that type (e.g. an unimplemented reserved hub-* type).
func buildHubBackend(name string, cfg config.BackendConfig) (HubBackend, error) {
	hubBackendFactoryMu.RLock()
	f, ok := hubBackendFactories[config.NormalizeBackend(cfg.Type)]
	hubBackendFactoryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("no factory registered for backend type %q", cfg.Type)
	}
	return f(name, cfg)
}
