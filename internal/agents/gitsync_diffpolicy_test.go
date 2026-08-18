package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// B3 coverage: the git_sync diff whitelist. The three-element validation
// proves where the draft came from; this proves what it may touch. The
// built-in deny defaults are always on (default-on basic check), config
// extends them per backend, and violations are audited to operation_logs.

func TestDiffPatternMatches(t *testing.T) {
	// Full-path globs.
	assert.True(t, diffPatternMatches("docs/*", "docs/a.md"))
	assert.False(t, diffPatternMatches("docs/*", "src/a.go"))
	// A trailing "/*" is recursive (operator intent for directory rules).
	assert.True(t, diffPatternMatches("vendor/*", "vendor/lib/x.go"))
	assert.True(t, diffPatternMatches("vendor/*", "vendor/x.go"))
	assert.False(t, diffPatternMatches("vendor/*", "vendorx/y.go"))
	// Slash-less patterns also match the basename (nested .env etc.).
	assert.True(t, diffPatternMatches(".env", ".env"))
	assert.True(t, diffPatternMatches(".env", "config/.env"))
	assert.True(t, diffPatternMatches(".env.*", "deploy/.env.production"))
	assert.True(t, diffPatternMatches("*.pem", "certs/server.pem"))
	assert.False(t, diffPatternMatches("*.pem", "docs/pem.md"))
	// Invalid globs never match (config load rejects them first).
	assert.False(t, diffPatternMatches("[unclosed", "anything"))
}

func TestDiffPolicyBuiltinDefaultsAlwaysOn(t *testing.T) {
	p := DiffPolicy{} // zero value: built-in defaults only
	viol := p.violations([]string{
		"src/main.go",       // fine
		"README.md",         // fine
		".env",              // dotenv secret
		"config/.env.local", // nested dotenv variant
		"certs/server.pem",  // key material
		"key",               // the contract's deploy-key file
		"ssh/id_ed25519",    // ssh private key
		"docs/keys.md",      // fine — not a key file
		"src/keystore.go",   // fine — *.key matches basename exactly, not substrings
	})
	assert.ElementsMatch(t, []string{".env", "config/.env.local", "certs/server.pem", "key", "ssh/id_ed25519"}, viol)
}

func TestDiffPolicyConfigDenyExtendsDefaults(t *testing.T) {
	p := DiffPolicy{Denied: []string{"vendor/*", "*.snap"}}
	viol := p.violations([]string{"vendor/lib/x.go", "pkg/a.go", "snapshots/b.snap"})
	assert.ElementsMatch(t, []string{"vendor/lib/x.go", "snapshots/b.snap"}, viol)
}

func TestDiffPolicyAllowedRestrictsButNeverReallowsDeny(t *testing.T) {
	p := DiffPolicy{Allowed: []string{"src/*", "docs/*"}}
	// A path outside the allow list is rejected...
	assert.Equal(t, []string{"Makefile"}, p.violations([]string{"Makefile", "src/a.go"}))
	// ...but an allow glob cannot re-allow a built-in deny (deny wins).
	p2 := DiffPolicy{Allowed: []string{".env", "src/*"}}
	assert.Equal(t, []string{".env"}, p2.violations([]string{".env", "src/a.go"}))
}

func TestValidateGitSyncDraftDiffViolationTyped(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:     "bbbb1111",
		BaseHEAD:      "aaaa0000",
		IsAncestor:    true,
		NewCommitMsgs: []string{"feat: change\n\nmatea-task-id: 42\n"},
		ChangedPaths:  []string{"src/a.go", ".env"},
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	var viol *DiffPolicyViolationError
	require.True(t, errors.As(err, &viol), "violation must surface as the typed error for auditing")
	assert.Equal(t, []string{".env"}, viol.Paths)
	assert.Contains(t, err.Error(), "denied/disallowed")
}

// Real-git Approve: a hub that commits the deploy key / dotenv into the draft
// is rejected even when all three git elements pass.
func TestGitSyncApproveRejectsSecretInDiff(t *testing.T) {
	taskID := int64(9701)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	// The hub (compromised or confused) committed the deploy key file.
	require.NoError(t, os.WriteFile(filepath.Join(work, "key"), []byte("PRIVATE KEY MATERIAL"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: signed but toxic", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	transport, fake, _ := newApproveTransport(t, remote, mainHEAD)
	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}

	_, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.Error(t, err)
	var viol *DiffPolicyViolationError
	require.True(t, errors.As(err, &viol))
	assert.Equal(t, []string{"key"}, viol.Paths)
	assert.Nil(t, fake.prCreated, "no PR for a draft leaking the deploy key")
}

// runViaHub integration: the violation error surfaces AND an audit row lands
// in operation_logs; the deploy key is still revoked.
func TestRunViaHubGitSyncDiffViolationAudited(t *testing.T) {
	taskID := int64(9702)
	remote, work, mainHEAD, run := initGitSyncBase(t)

	run(work, "checkout", "-q", "-b", DraftBranchName(taskID))
	require.NoError(t, os.WriteFile(filepath.Join(work, "fix.go"), []byte("package fix\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(work, ".env"), []byte("TOKEN=secret\n"), 0o644))
	run(work, "add", "-A")
	run(work, "commit", "-q", "-m", "feat: signed but toxic", "-m", RequiredFooter(taskID))
	run(work, "push", "-q", "origin", DraftBranchName(taskID))

	fake := newGitSyncFakeGitea(t, remote, mainHEAD)
	issuer := &fakeDeployKeyIssuer{}
	db := newHubRunTestDB(t)
	f := newGitSyncFactory(db, fake.server.URL, remote, issuer)

	hub := &gitSyncTestHub{name: "gs-opencode", pollState: StateDone, pollRes: &BackendResult{Summary: "pushed"}}
	task := gitSyncApproveTask(taskID)
	tc := &TaskContext{TaskType: "solve_issue", Repo: "o/r", IssueID: 12, TaskID: taskID}

	_, err := f.runViaHub(context.Background(), task, &store.Agent{ID: 7}, hub, tc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "denied/disallowed")
	assert.Contains(t, err.Error(), ".env")
	assert.Nil(t, fake.prCreated)
	assert.Equal(t, []int64{1}, issuer.revoked, "key revoked even on policy rejection")

	logs, lerr := db.ListOperationLogs(10, 0)
	require.NoError(t, lerr)
	require.NotEmpty(t, logs, "violation must be audited")
	found := false
	for _, l := range logs {
		if l.Action == "git_sync_diff_violation" {
			found = true
			assert.Equal(t, int64(7), l.AgentID)
			assert.Equal(t, taskID, l.TaskID)
			assert.Contains(t, l.Detail, ".env")
			assert.Contains(t, l.Detail, DraftBranchName(taskID))
		}
	}
	assert.True(t, found, "operation_logs must carry a git_sync_diff_violation row, got: %v", logs)
}

// A clean draft passes the default policy untouched (happy-path regression:
// normal source files must not trip the built-in denies).
func TestGitSyncApproveCleanDiffPassesDefaultPolicy(t *testing.T) {
	taskID := int64(9703)
	cloneURL, mainHEAD, _ := setupGitSyncRemote(t, taskID) // draft touches fix.go only
	transport, _, _ := newApproveTransport(t, cloneURL, mainHEAD)
	info := &GitSyncInfo{DraftBranch: DraftBranchName(taskID), BaseBranch: "main", BaseHEAD: mainHEAD, RequiredFooter: RequiredFooter(taskID), HubPush: true}

	res, err := transport.Approve(context.Background(), gitSyncApproveTask(taskID), &store.Agent{}, "o", "r",
		info, &GitSyncResult{DraftBranch: info.DraftBranch}, "done")
	require.NoError(t, err)
	assert.Equal(t, 77, res.PRID)
}
