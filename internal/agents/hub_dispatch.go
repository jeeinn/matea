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
//   - a configured hub-hermes instance → the shared Hermes backend singleton
//     (constructed via the init()-registered factory, Phase 2);
//   - reserved hub-* names without an implementation (hub-openclaw / hub-api /
//     any unconfigured hub-*) → explicit error;
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
		case config.BackendTypeHubHermes:
			b, err := f.hubRegistry.Lookup(name)
			if err != nil {
				// Registration may have been skipped in NewRunnerFactory
				// because the instance failed construction — surface that
				// precise error instead of the registry's generic message.
				if _, cerr := buildHubBackend(name, cfg); cerr != nil {
					return nil, fmt.Errorf("hub-hermes backend %q is misconfigured: %w", name, cerr)
				}
				return nil, err
			}
			return b, nil
		default:
			return nil, fmt.Errorf("backend %q has unsupported type %q (supported: %s)",
				name, cfg.Type, supportedBackendTypes())
		}
	}

	if strings.HasPrefix(name, "hub-") {
		return nil, fmt.Errorf("hub backend %q is not configured in agents.backends (supported types: %s)",
			name, supportedBackendTypes())
	}
	return nil, fmt.Errorf("unknown backend %q: not builtin, not a configured hub-*, not in agents.backends config", name)
}

// supportedBackendTypes lists the backend types this binary can actually
// construct: the two implemented in-package, plus every type whose factory a
// linked sub-package registered through init(). Built dynamically so the
// message cannot drift out of date as hub types are added (it previously
// hardcoded "Phase 1 supports builtin and hub-opencode" and went stale the
// moment hub-hermes landed).
func supportedBackendTypes() string {
	types := []string{config.BackendTypeBuiltin, config.BackendTypeHubOpenCode}
	for _, t := range registeredHubBackendTypes() {
		if t != config.BackendTypeBuiltin && t != config.BackendTypeHubOpenCode {
			types = append(types, t)
		}
	}
	return strings.Join(types, ", ")
}

// validateHubDispatch is the runner entry *gate*: it rejects the task when the
// agent names a backend this binary cannot serve (unknown name, reserved-but-
// unimplemented hub-*, misconfigured instance). It answers "may this task run
// at all?" — nothing more.
//
// It is deliberately distinct from ResolveHubExecution, which answers the
// separate question "should this task run through the hub's Submit/Poll
// instead of the in-process LLM?". A task can pass validateHubDispatch and
// still get false from ResolveHubExecution — that is the normal case for
// builtin, and for hub-opencode (validated here, but write-only and driven
// through the CodingBackend path). Runners therefore call both: the gate
// first, then the execution-path decision.
//
// The two share ResolveHubBackend's normalization, so the duplicated lookup is
// intentional (correctness over saving a map read).
//
// Handle persistence and executor re-attach on restart are implemented in
// runViaHub (HubBackend contract §1.2.1): the Handle is persisted to SQLite
// immediately after Submit and re-attached on re-entry, so a Matea restart
// resumes the hub run instead of losing or double-submitting it.
func (f *RunnerFactory) validateHubDispatch(agent *store.Agent) error {
	_, err := f.ResolveHubBackend(agent)
	return err
}
