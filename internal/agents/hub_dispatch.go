package agents

import (
	"fmt"
	"strings"

	"github.com/jeeinn/matea/internal/config"
	"github.com/jeeinn/matea/internal/store"
)

// hub_dispatch.go implements the Phase 1 hub-* dispatch branch (task 1.2.4).
//
// Every runner resolves the agent's backend selection through
// ResolveHubBackend before doing any work:
//   - "builtin" (or empty/legacy "internal") → the in-process BuiltinHubBackend;
//   - a configured hub-opencode instance (e.g. "opencode-local") → the shared
//     OpenCodeHTTPBackend singleton from the registry — stays fully usable;
//   - reserved hub-* names without an implementation (hub-hermes /
//     hub-openclaw / hub-api / any unconfigured hub-*) → explicit error;
//   - anything else unknown → explicit error.
//
// Unknown backends must never silently fall back to builtin: a typo like
// "hub_opencode" would otherwise burn the user's own LLM quota while they
// believe a hub is in use.
//
// Phase 1 scope: runners only *validate* through this seam — write tasks
// continue through the CodingBackend path, non-write tasks keep their direct
// LLM calls (Analyze/Review are forced builtin by design). Actual
// Submit/Poll dispatch through HubBackend, with Handle persistence and
// executor re-attach, is Phase 2.

// ResolveHubBackend maps an agent's backend selection to a HubBackend.
// Resolution mirrors ResolveCodingBackend (normalization + default), then
// applies the hub dispatch rules described above.
func (f *RunnerFactory) ResolveHubBackend(agent *store.Agent) (HubBackend, error) {
	name := config.NormalizeBackend(agent.Backend)
	if name == "" {
		name = config.NormalizeBackend(f.backends.Default)
	}
	if name == "" {
		name = config.BackendNameBuiltin
	}
	if name == config.BackendNameBuiltin {
		return f.hubRegistry.Lookup(config.BackendNameBuiltin)
	}

	if cfg, ok := f.backends.Backends[name]; ok {
		switch config.NormalizeBackend(cfg.Type) {
		case config.BackendTypeBuiltin:
			return f.hubRegistry.Lookup(config.BackendNameBuiltin)
		case config.BackendTypeHubOpenCode:
			b, err := f.hubRegistry.Lookup(name)
			if err != nil {
				// Registration was skipped in NewRunnerFactory because the
				// instance failed construction — surface that precise error
				// instead of the registry's generic "unknown backend".
				if _, cerr := NewOpenCodeHTTPBackend(name, cfg); cerr != nil {
					return nil, fmt.Errorf("hub-opencode backend %q is misconfigured: %w", name, cerr)
				}
				return nil, err
			}
			return b, nil
		default:
			return nil, fmt.Errorf("backend %q has unsupported type %q (Phase 1 supports %q and %q)",
				name, cfg.Type, config.BackendTypeBuiltin, config.BackendTypeHubOpenCode)
		}
	}

	if strings.HasPrefix(name, "hub-") {
		return nil, fmt.Errorf("hub backend %q is not implemented in Phase 1 (available: builtin, hub-opencode)", name)
	}
	return nil, fmt.Errorf("unknown backend %q: not builtin, not a configured hub-*, not in agents.backends config", name)
}

// validateHubDispatch is the Phase 1 runner entry check: it reserves the
// hub-* dispatch branch in every runner. Reserved-but-unimplemented hub
// backends and unknown names fail the task loudly; builtin and configured
// hub-opencode instances pass through to the existing execution paths.
func (f *RunnerFactory) validateHubDispatch(agent *store.Agent) error {
	_, err := f.ResolveHubBackend(agent)
	return err
}
