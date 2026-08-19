package agents

import (
	"fmt"
	"path"
	"strings"
)

// B3: git_sync diff whitelist (20260815-git-sync-3phase-plan.md v3.1).
//
// The three-element validation proves WHERE the hub's draft branch came from;
// the diff whitelist constrains WHAT it may touch. Under the hub-push trust
// model the highest-severity leak is secret material committed into the draft
// branch (the task-scoped deploy key is sitting in the hub's workdir one
// directory above the clone), so a small built-in deny list is ALWAYS on —
// "默认开基础校验". Operators extend it per backend via
// agents.backends.<name>.allowed_paths / denied_paths.
//
// Semantics:
//   - Deny wins. The built-in defaults and configured denied_paths are both
//     absolute (allowed_paths cannot re-allow a denied path).
//   - allowed_paths, when non-empty, is a further restriction: every changed
//     path must match at least one allow glob.
//   - Matching is glob (path.Match) against the repo-relative path; a pattern
//     without a slash also matches the basename, so ".env" catches
//     "config/.env" without needing ** support. A trailing "/*" is recursive:
//     "vendor/*" matches everything under vendor/ at any depth (path.Match's
//     * does not cross "/", which would silently narrow an operator's deny
//     rule — unacceptable for a security feature).

// defaultDeniedDiffPatterns is the always-on built-in deny list. It targets
// the secret-leak class: dotenv files, key/cert material, SSH private keys,
// and the contract's own deploy-key file (BuildGitSyncInstructions step 2
// restores it as `key` in the hub workdir).
var defaultDeniedDiffPatterns = []string{
	".env", ".env.*",
	"*.pem", "*.key", "*.p12", "*.pfx",
	"id_rsa*", "id_ed25519*",
	"key",
}

// DiffPolicy is the per-backend path whitelist checked at Approve (B3). The
// zero value is valid and means "built-in defaults only".
type DiffPolicy struct {
	Allowed []string // globs; non-empty = every changed path must match one
	Denied  []string // globs; extends (never replaces) the built-in defaults
}

// DiffPolicyViolationError reports changed paths that violate the whitelist.
// Typed so runViaHub can audit violations to operation_logs with errors.As.
type DiffPolicyViolationError struct {
	Paths []string
}

func (e *DiffPolicyViolationError) Error() string {
	return fmt.Sprintf("git_sync approve: draft diff touches %d denied/disallowed path(s): %s",
		len(e.Paths), strings.Join(e.Paths, ", "))
}

// diffPatternMatches reports whether a changed repo-relative path matches a
// policy glob. Full-path match first; a trailing "/*" is recursive (any depth
// under that directory); slash-less patterns also match the basename
// (documented above). An invalid glob never matches — config load rejects
// invalid patterns at startup (ValidateBackendDiffPaths), so a typo cannot
// silently disable a deny rule.
func diffPatternMatches(pattern, p string) bool {
	if ok, err := path.Match(pattern, p); err == nil && ok {
		return true
	}
	if strings.HasSuffix(pattern, "/*") && strings.HasPrefix(p, strings.TrimSuffix(pattern, "*")) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		if ok, err := path.Match(pattern, path.Base(p)); err == nil && ok {
			return true
		}
	}
	return false
}

// violations returns the subset of changed paths the policy rejects.
func (p DiffPolicy) violations(changed []string) []string {
	var out []string
	for _, fp := range changed {
		if fp == "" {
			continue
		}
		denied := false
		for _, pat := range defaultDeniedDiffPatterns {
			if diffPatternMatches(pat, fp) {
				denied = true
				break
			}
		}
		if !denied {
			for _, pat := range p.Denied {
				if diffPatternMatches(pat, fp) {
					denied = true
					break
				}
			}
		}
		if denied {
			out = append(out, fp)
			continue
		}
		if len(p.Allowed) > 0 {
			ok := false
			for _, pat := range p.Allowed {
				if diffPatternMatches(pat, fp) {
					ok = true
					break
				}
			}
			if !ok {
				out = append(out, fp)
			}
		}
	}
	return out
}
