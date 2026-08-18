package agents

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// This file defines the WorkspaceTransport abstraction and the git_sync
// transport (task A1 of 20260815-git-sync-3phase-plan.md v3.1).
//
// Trust model (v3): credentials are HELD AND USED BY THE HUB. Matea issues a
// task-scoped read-write deploy key at Prepare, hands it to the hub inside
// GitSyncInfo, and the hub clones / edits / commits / pushes the draft branch
// matea/hub-{taskID} itself. Matea never pushes on the hub's behalf: Approve
// only fetches, runs the three-element validation and opens the PR (approval).
// Matea's admin token is NEVER given to a hub.
//
// Gitea deploy keys are repo-scoped (read-only / read-write) with no
// per-branch granularity, so "draft-branch-prefix-only writes" are enforced at
// the application layer by the three-element validation (branch name +
// hub_handles ownership + required footer), not by the credential.

// DraftBranchPrefix namespaces all hub-pushed draft branches.
const DraftBranchPrefix = "matea/hub-"

// DraftBranchName returns the draft branch a hub must push for a task.
func DraftBranchName(taskID int64) string {
	return fmt.Sprintf("%s%d", DraftBranchPrefix, taskID)
}

// RequiredFooter returns the commit-message footer every hub commit must carry.
func RequiredFooter(taskID int64) string {
	return fmt.Sprintf("matea-task-id: %d", taskID)
}

// GitSyncInfo is the package Matea hands to a hub at Submit time (embedded in
// TaskContext, wired in task A2). The hub uses it to clone, commit and push.
type GitSyncInfo struct {
	CloneURL       string `json:"clone_url"`             // SSH clone URL (hub authenticates with PrivateKey)
	PrivateKey     string `json:"private_key"`           // task-scoped deploy key (OpenSSH PEM)
	DraftBranch    string `json:"draft_branch"`          // matea/hub-{taskID} — the only branch the hub may push
	BaseBranch     string `json:"base_branch"`           // e.g. main
	BaseHEAD       string `json:"base_head"`             // anchor: base branch head at Prepare time
	AnchorHEAD     string `json:"anchor_head,omitempty"` // B2.3 continuation anchor: session LastHead; empty = BaseHEAD
	CommitAuthor   string `json:"commit_author"`         // e.g. "Matea Hub <hub@matea.local>"
	RequiredFooter string `json:"required_footer"`       // "matea-task-id: {taskID}"
	HubPush        bool   `json:"hub_push"`              // always true: the hub pushes, Matea never does
}

// anchor returns the commit the hub must branch from and the draft must
// descend from: the session continuation anchor when set (B2.3), otherwise
// the base branch head captured at Prepare.
func (i *GitSyncInfo) anchor() string {
	if i.AnchorHEAD != "" {
		return i.AnchorHEAD
	}
	return i.BaseHEAD
}

// GitSyncResult is what the hub reports back after pushing (embedded in
// BackendResult, wired in task A2).
type GitSyncResult struct {
	DraftBranch string `json:"draft_branch"`
	DraftHEAD   string `json:"draft_head"`
}

// IssuedDeployKey is a freshly created task-scoped deploy key.
type IssuedDeployKey struct {
	KeyID      int64  // Gitea-side key id (for Revoke)
	PrivateKey string // OpenSSH private key PEM, handed to the hub once
	PublicKey  string // OpenSSH public key (registered with Gitea)
}

// DeployKeyIssuer creates and revokes task-scoped deploy keys. Implemented
// against the Gitea keys API in task A6; tests substitute fakes. The A0.2
// spike confirmed: create → 201 with id, delete → 204 idempotent (safe retry),
// revocation effective immediately, and a write:repository-scoped token
// suffices (no admin needed).
type DeployKeyIssuer interface {
	Issue(ctx context.Context, owner, repo, title string) (*IssuedDeployKey, error)
	Revoke(ctx context.Context, owner, repo string, keyID int64) error
}

// WorkspaceTransport owns how a hub write task's workspace/credentials are
// prepared, approved and cleaned up. git_sync is the target transport for all
// hub write tasks; builtin write tasks never touch this interface.
type WorkspaceTransport interface {
	// Name returns the transport id ("git_sync").
	Name() string

	// Prepare issues the task-scoped credential, computes the draft branch and
	// base anchor, and builds the GitSyncInfo for the hub. The returned
	// IssuedDeployKey is retained by the caller (persisted in A2) so Cleanup
	// can revoke it.
	Prepare(ctx context.Context, task *store.Task, owner, repo, baseBranch string) (*GitSyncInfo, *IssuedDeployKey, error)

	// Approve fetches the hub-pushed draft branch, runs the three-element
	// validation (branch exclusivity / base anchor / required footer), then
	// opens or updates the PR via FinalizeWriteTaskPR. It never re-commits and
	// never pushes.
	Approve(ctx context.Context, task *store.Task, agent *store.Agent, owner, repo string, info *GitSyncInfo, result *GitSyncResult, agentResult string) (*Result, error)

	// Cleanup revokes the task-scoped deploy key (best effort; the Gitea
	// delete is idempotent so retries are safe).
	Cleanup(ctx context.Context, owner, repo string, key *IssuedDeployKey) error
}

// gitSyncTransport is the git_sync WorkspaceTransport.
type gitSyncTransport struct {
	giteaFactory GiteaClientFactory
	issuer       DeployKeyIssuer // nil until task A6 wires the Gitea implementation
	workBaseDir  string          // temp fetch workspaces are created under here
	policy       DiffPolicy      // B3 diff whitelist (per-backend allowed/denied_paths)

	// runGit is replaceable in tests. It runs git with args in dir and returns
	// combined stdout output.
	runGit func(ctx context.Context, dir string, args ...string) (string, error)
}

// NewGitSyncTransport builds the git_sync transport. issuer may be nil until
// task A6 lands; Prepare fails loudly in that case. policy is the B3 diff
// whitelist; the zero value applies the built-in deny defaults only.
func NewGitSyncTransport(giteaFactory GiteaClientFactory, issuer DeployKeyIssuer, workBaseDir string, policy DiffPolicy) WorkspaceTransport {
	return &gitSyncTransport{
		giteaFactory: giteaFactory,
		issuer:       issuer,
		workBaseDir:  workBaseDir,
		policy:       policy,
		runGit:       defaultRunGit,
	}
}

func (t *gitSyncTransport) Name() string { return "git_sync" }

func (t *gitSyncTransport) Prepare(ctx context.Context, task *store.Task, owner, repo, baseBranch string) (*GitSyncInfo, *IssuedDeployKey, error) {
	if t.issuer == nil {
		return nil, nil, fmt.Errorf("git_sync transport: deploy key issuer not wired (task A6)")
	}
	adminClient := t.giteaFactory.GetAdminGiteaClient()
	if adminClient == nil {
		return nil, nil, fmt.Errorf("git_sync transport: admin gitea client unavailable")
	}

	repoInfo, err := adminClient.GetRepo(owner, repo)
	if err != nil {
		return nil, nil, fmt.Errorf("git_sync prepare: get repo: %w", err)
	}
	if repoInfo.SSHURL == "" {
		return nil, nil, fmt.Errorf("git_sync prepare: repo %s/%s has no ssh_url (SSH must be enabled on the Gitea server)", owner, repo)
	}
	base := gitea.ResolveDefaultBranch(baseBranch)
	if baseBranch == "" {
		base = gitea.ResolveDefaultBranch(repoInfo.DefaultBranch)
	}
	branch, err := adminClient.GetBranch(owner, repo, base)
	if err != nil {
		return nil, nil, fmt.Errorf("git_sync prepare: anchor base branch %q: %w", base, err)
	}

	key, err := t.issuer.Issue(ctx, owner, repo, fmt.Sprintf("matea-hub-task-%d", task.ID))
	if err != nil {
		return nil, nil, fmt.Errorf("git_sync prepare: issue deploy key: %w", err)
	}

	info := &GitSyncInfo{
		CloneURL:       repoInfo.SSHURL,
		PrivateKey:     key.PrivateKey,
		DraftBranch:    DraftBranchName(task.ID),
		BaseBranch:     base,
		BaseHEAD:       branch.Commit.ID,
		CommitAuthor:   "Matea Hub <hub@matea.local>",
		RequiredFooter: RequiredFooter(task.ID),
		HubPush:        true,
	}
	return info, key, nil
}

// fetchedDraft is the git-side evidence Approve validates against. Separated
// from validateGitSyncDraft so the three-element rules are unit-testable
// without a git binary (task A7 adversarial cases).
type fetchedDraft struct {
	DraftHEAD     string   // actual head of the fetched draft branch
	BaseHEAD      string   // current head of the base branch (for drift check)
	IsAncestor    bool     // whether the anchor (info.anchor()) is an ancestor of DraftHEAD
	NewCommitMsgs []string // commit messages of anchor..DraftHEAD
	ChangedPaths  []string // B3: repo-relative paths touched on anchor..DraftHEAD
}

// validateGitSyncDraft enforces the three elements:
//  1. branch exclusivity — the hub reported and pushed exactly DraftBranch;
//  2. base anchor — the anchor (session LastHead for continuations, else the
//     Prepare-time base head) is an ancestor of the draft head, and the base
//     branch has not drifted since Prepare (v3.1: drift = fail + warn, no
//     automatic rebase; the drift check always uses BaseHEAD — its window is
//     this task's Prepare→Approve, independent of continuation);
//  3. required footer — every new commit carries matea-task-id: {taskID}
//     (checked on the range anchor..DraftHEAD so a continuation only signs
//     its own commits, not the previous task's);
//  4. diff whitelist (B3) — changed paths are checked against the built-in
//     deny defaults plus the backend's allowed/denied_paths; violations
//     surface as *DiffPolicyViolationError so the caller can audit them.
func validateGitSyncDraft(info *GitSyncInfo, result *GitSyncResult, fetched *fetchedDraft, policy DiffPolicy) error {
	// Element 1: branch exclusivity (name check; hub_handles ownership is
	// keyed by task id at the runViaHub layer).
	if result == nil || result.DraftBranch == "" {
		return fmt.Errorf("git_sync approve: hub returned no git_sync result (draft branch missing)")
	}
	if result.DraftBranch != info.DraftBranch {
		return fmt.Errorf("git_sync approve: hub pushed branch %q, expected %q (branch exclusivity)",
			result.DraftBranch, info.DraftBranch)
	}
	if fetched.DraftHEAD == "" {
		return fmt.Errorf("git_sync approve: draft branch %q not found on remote (hub did not push?)", info.DraftBranch)
	}
	if result.DraftHEAD != "" && result.DraftHEAD != fetched.DraftHEAD {
		return fmt.Errorf("git_sync approve: hub reported draft head %q but remote has %q",
			result.DraftHEAD, fetched.DraftHEAD)
	}
	anchor := info.anchor()
	if fetched.DraftHEAD == anchor {
		return fmt.Errorf("git_sync approve: draft branch %q has no new commits", info.DraftBranch)
	}

	// Element 2: start-point anchor + drift (fail + warn, no auto rebase).
	if !fetched.IsAncestor {
		return fmt.Errorf("git_sync approve: draft head %s is not descended from anchor %s (start-point anchoring)",
			fetched.DraftHEAD, anchor)
	}
	if fetched.BaseHEAD != "" && fetched.BaseHEAD != info.BaseHEAD {
		return fmt.Errorf("git_sync approve: base branch %q drifted from %s to %s since Prepare (fail+warn policy; rebase intentionally not automatic)",
			info.BaseBranch, info.BaseHEAD, fetched.BaseHEAD)
	}

	// Element 3: required footer on every new commit.
	if len(fetched.NewCommitMsgs) == 0 {
		return fmt.Errorf("git_sync approve: no new commits on %q", info.DraftBranch)
	}
	for i, msg := range fetched.NewCommitMsgs {
		if !strings.Contains(msg, info.RequiredFooter) {
			return fmt.Errorf("git_sync approve: commit %d of %d on %q lacks required footer %q",
				i+1, len(fetched.NewCommitMsgs), info.DraftBranch, info.RequiredFooter)
		}
	}

	// Element 4 (B3): diff whitelist — default-on basic check.
	if bad := policy.violations(fetched.ChangedPaths); len(bad) > 0 {
		return &DiffPolicyViolationError{Paths: bad}
	}
	return nil
}

func (t *gitSyncTransport) Approve(ctx context.Context, task *store.Task, agent *store.Agent, owner, repo string, info *GitSyncInfo, result *GitSyncResult, agentResult string) (*Result, error) {
	adminClient := t.giteaFactory.GetAdminGiteaClient()
	if adminClient == nil {
		return nil, fmt.Errorf("git_sync approve: admin gitea client unavailable")
	}

	fetched, err := t.fetchDraft(ctx, adminClient, owner, repo, info)
	if err != nil {
		return nil, err
	}
	if err := validateGitSyncDraft(info, result, fetched, t.policy); err != nil {
		return nil, err
	}
	// Normalize the hub-reported head to the fetched (authoritative) one so the
	// caller can persist an accurate session LastHead even when the hub's
	// result/trailer carried no head (B2.3 continuation quality depends on it).
	result.DraftHEAD = fetched.DraftHEAD

	// Validation passed: the hub's draft branch is the deliverable. Open or
	// update the PR — no re-commit, no re-push (unlike finalizeWriteChanges).
	taskSubType := "dev"
	if task.TaskType == "fix_bug" {
		taskSubType = "bugfix"
	}
	return FinalizeWriteTaskPR(adminClient, owner, repo, info.DraftBranch, info.BaseBranch, task, taskSubType, agentResult)
}

// fetchDraft clones nothing whole: it inits a bare-ish temp repo, fetches the
// draft and base branches with Matea's own credentials (never the hub's deploy
// key), and extracts the validation evidence.
func (t *gitSyncTransport) fetchDraft(ctx context.Context, adminClient *gitea.Client, owner, repo string, info *GitSyncInfo) (*fetchedDraft, error) {
	repoInfo, err := adminClient.GetRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("git_sync approve: get repo: %w", err)
	}
	fetchURL, err := gitea.AuthenticatedCloneURL(repoInfo.CloneURL, "", adminClient.Token)
	if err != nil {
		return nil, fmt.Errorf("git_sync approve: build fetch url: %w", err)
	}

	dir, err := os.MkdirTemp(t.workBaseDir, fmt.Sprintf("gitsync-approve-%d-*", 0))
	if err != nil {
		return nil, fmt.Errorf("git_sync approve: temp workspace: %w", err)
	}
	defer os.RemoveAll(dir)

	out := &fetchedDraft{}
	if _, err := t.runGit(ctx, dir, "init", "-q"); err != nil {
		return nil, fmt.Errorf("git_sync approve: git init: %w", err)
	}
	if _, err := t.runGit(ctx, dir, "remote", "add", "origin", fetchURL); err != nil {
		return nil, fmt.Errorf("git_sync approve: git remote add: %w", err)
	}
	// Fetch the draft branch; absence means the hub never pushed.
	draftOut, draftErr := t.runGit(ctx, dir, "fetch", "-q", "origin", info.DraftBranch)
	if draftErr != nil {
		log.Printf("[INFO] git_sync approve: fetch draft %q failed (hub did not push?): %v (%s)", info.DraftBranch, draftErr, draftOut)
		return out, nil // DraftHEAD stays "" → validation reports "not found"
	}
	head, err := t.runGit(ctx, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return nil, fmt.Errorf("git_sync approve: resolve draft head: %w", err)
	}
	out.DraftHEAD = strings.TrimSpace(head)

	// Base branch head for the drift check (best effort: a missing base ref is
	// caught here; drift policy only applies when we could resolve it).
	if _, err := t.runGit(ctx, dir, "fetch", "-q", "origin", info.BaseBranch); err == nil {
		if baseHead, berr := t.runGit(ctx, dir, "rev-parse", "FETCH_HEAD"); berr == nil {
			out.BaseHEAD = strings.TrimSpace(baseHead)
		}
	}

	// Ancestor check: the anchor (session LastHead for continuations, else the
	// Prepare-time base head) must be reachable from the draft head.
	anchor := info.anchor()
	if _, err := t.runGit(ctx, dir, "merge-base", "--is-ancestor", anchor, out.DraftHEAD); err == nil {
		out.IsAncestor = true
	}

	// Commit messages on the new range for the footer check. The range starts
	// at the anchor so a continuation task signs only its own commits (the
	// previous task's carry its footer, not this one's).
	if out.IsAncestor {
		if logs, lerr := t.runGit(ctx, dir, "log", "--format=%B%x00", fmt.Sprintf("%s..%s", anchor, out.DraftHEAD)); lerr == nil {
			for _, m := range strings.Split(logs, "\x00") {
				if strings.TrimSpace(m) != "" {
					out.NewCommitMsgs = append(out.NewCommitMsgs, m)
				}
			}
		}
		// Changed paths for the B3 diff whitelist, same anchor-bounded range.
		if names, derr := t.runGit(ctx, dir, "diff", "--name-only", fmt.Sprintf("%s..%s", anchor, out.DraftHEAD)); derr == nil {
			for _, n := range strings.Split(names, "\n") {
				if n = strings.TrimSpace(n); n != "" {
					out.ChangedPaths = append(out.ChangedPaths, n)
				}
			}
		}
	}
	return out, nil
}

func (t *gitSyncTransport) Cleanup(ctx context.Context, owner, repo string, key *IssuedDeployKey) error {
	if key == nil || t.issuer == nil {
		return nil
	}
	if err := t.issuer.Revoke(ctx, owner, repo, key.KeyID); err != nil {
		// Delete is idempotent on the Gitea side (204 even for a missing key),
		// so a failure here is worth a loud warning and a caller-side retry,
		// not a silent drop.
		log.Printf("[WARN] git_sync cleanup: revoke deploy key %d for %s/%s failed: %v", key.KeyID, owner, repo, err)
		return fmt.Errorf("git_sync cleanup: revoke deploy key: %w", err)
	}
	return nil
}

// defaultRunGit executes git with a bounded context. Output is combined
// stdout+stderr (trimmed by callers); credentials never appear in logs because
// callers pass the authenticated URL only to remote-add/fetch.
func defaultRunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never prompt for credentials interactively inside an approval run.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}

// DraftHeadTrailer is the exact line prefix a hub must end its final response
// with so Matea can cross-check the pushed draft head (task A4). Approve
// treats the fetched remote state as authoritative; the trailer is the
// hub's honesty cross-check.
const DraftHeadTrailer = "matea-draft-head: "

// ParseDraftHeadTrailer extracts the reported draft head from a hub's final
// message. Returns "" when absent — Approve's fetch is authoritative, the
// trailer only cross-checks when present.
func ParseDraftHeadTrailer(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, DraftHeadTrailer) {
			return strings.TrimSpace(strings.TrimPrefix(line, DraftHeadTrailer))
		}
	}
	return ""
}

// BuildGitSyncInstructions renders the mandatory git workflow a hub must
// follow under git_sync (validated end-to-end by the A0.1 spike against a real
// OpenCode + Gitea). Shared by the OpenCode (A4) and Hermes (B1) integrations
// so both hubs get byte-identical contract instructions.
//
// The private key travels base64-encoded on one line: PEM's multi-line shape
// is too easy for an LLM to mangle when re-typing; base64 -d restores it
// byte-exactly (spike finding).
//
// Continuation (B2.3): when info.AnchorHEAD is set, the draft branch starts
// from that commit (the session's previous task head) instead of the default
// branch tip. The clone is a full clone, so the anchor is reachable as long
// as the previous draft branch (or a descendant ref) still exists on the
// remote; an unreachable anchor fails the checkout loudly.
func BuildGitSyncInstructions(info *GitSyncInfo, workSubdir string) string {
	keyB64 := base64.StdEncoding.EncodeToString([]byte(info.PrivateKey))
	sshCmd := fmt.Sprintf("GIT_SSH_COMMAND='ssh -i %s/key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'", workSubdir)
	checkoutStep := fmt.Sprintf("git checkout -b %[1]s", info.DraftBranch)
	if info.AnchorHEAD != "" {
		checkoutStep = fmt.Sprintf("git checkout -b %[1]s %[2]s", info.DraftBranch, info.AnchorHEAD)
	}
	continuationNote := ""
	if info.AnchorHEAD != "" {
		continuationNote = fmt.Sprintf(`
This task CONTINUES prior session work: the draft branch starts from commit %[1]s (the previous task's pushed head), NOT from the default branch tip. That commit is reachable in your fresh clone; if the checkout fails because it is missing, STOP and report the failure — do not restart the work from the default branch.`, info.AnchorHEAD)
	}
	return fmt.Sprintf(`## Git workflow (MANDATORY — follow exactly)

You are given a task-scoped deploy key. Do ALL git work yourself; the orchestrator never commits or pushes for you.

1. Create and enter the work directory: mkdir -p %[1]s && cd %[1]s
2. Restore the deploy key (it is base64-encoded, single line):
   printf '%%s' '%[2]s' | base64 -d > key && chmod 600 key
   Verify: ssh-keygen -y -f key must print a public key without error.
3. Clone the repository: %[3]s git clone %[4]s repo
4. cd repo && git config user.email "hub@matea.local" && git config user.name "matea-hub" && %[5]s
5. Do the task work inside repo/ (read, edit, write files as needed).
6. Commit ALL changes: git add -A && git commit -m "<summary>" -m "%[6]s"
   Every commit message MUST contain the footer line: %[6]s
7. Push the draft branch: cd repo && GIT_SSH_COMMAND='ssh -i ../key -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null' git push -u origin %[7]s
8. End your final response with a line exactly of this form (full 40-char sha):
   matea-draft-head: <output of: cd repo && git rev-parse HEAD>
%[8]s
Rules: push ONLY the branch %[7]s — never any other branch. Do not open pull requests yourself.`,
		workSubdir, keyB64, sshCmd, info.CloneURL, checkoutStep, info.RequiredFooter, info.DraftBranch, continuationNote)
}
