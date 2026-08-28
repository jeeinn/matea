package agents

import (
	"context"
	"fmt"
	"log"
	"strings"

	agentpkg "github.com/jeeinn/matea/internal/agent"
	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/llm"
	"github.com/jeeinn/matea/internal/sandbox"
	"github.com/jeeinn/matea/internal/store"
)

// WriteWorkspaceContext holds the prepared workspace state for a write task.
// It is produced by prepareWriteWorkspace and consumed by finalizeWriteChanges,
// so the coding phase (AgentLoop / CodingBackend.Run) sits between the two.
type WriteWorkspaceContext struct {
	Sandbox       *sandbox.Sandbox
	Git           *sandbox.Git
	Audit         *sandbox.AuditLogger
	BranchName    string
	Owner         string
	Repo          string
	RepoInfo      *gitea.RepoInfo
	UseSession    bool // true if the task belongs to a session (records branch+head on finalize)
	SessionBranch string
}

// prepareWriteWorkspace sets up the sandbox, clones the repository, and
// prepares the working branch for a write task (dev / bugfix).
//
// Session continuation (B2.2) is git-native: every write task gets a fresh
// task-level sandbox and clone; a session task resumes by anchoring its
// working branch on the session's recorded LastHead (the head SHA of the
// previous task's pushed draft branch) instead of reusing an on-disk session
// workspace. The deprecated WorkspacePath column is no longer consulted here.
//
// On error the sandbox is cleaned up here (mirroring the original defer). On
// success the caller owns the sandbox lifecycle and must
// `defer wwc.Sandbox.Cleanup()` — all write workspaces are task-level now.
func prepareWriteWorkspace(ctx context.Context, task *store.Task, agent *store.Agent, factory *RunnerFactory) (*WriteWorkspaceContext, error) {
	_ = ctx // reserved for future use (e.g. cancellable clone)

	// Parse repo owner/name
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	// Get Gitea client — guard against a nil factory (e.g. dispatch-only
	// factories in tests or deployments without Gitea) so workspace prep
	// returns an error the runner can react to instead of panicking.
	if factory.giteaFactory == nil {
		return nil, fmt.Errorf("gitea client factory not configured")
	}
	client := factory.giteaFactory.GetGiteaClient(agent.GiteaToken)

	// Get repo info for clone URL
	repoInfo, err := client.GetRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get repo info: %w", err)
	}
	cloneURL, err := gitea.AuthenticatedCloneURL(repoInfo.CloneURL, agent.GiteaUsername, agent.GiteaToken)
	if err != nil {
		return nil, fmt.Errorf("authenticated clone url: %w", err)
	}
	redactedCloneURL := gitea.RedactCloneURL(cloneURL)

	// Session continuation state (B2.2): the git-native anchor, not an on-disk
	// workspace. LastHead (recorded at the previous task's successful push) is
	// the authoritative start point; a session that only has Branch (pre-B2.2
	// rows) falls back to the remote branch head via prepareExistingBranch.
	useSession := false
	var sessionBranch, sessionLastHead string
	if task.SessionID != "" && factory.db != nil {
		if session, err := factory.db.GetSession(task.SessionID); err == nil {
			useSession = true
			sessionBranch = session.Branch
			sessionLastHead = session.LastHead
		}
	}
	isContinuation := sessionLastHead != "" || sessionBranch != ""

	sb := sandbox.New(factory.sandboxCfg, task.ID)
	if err := sb.Setup(); err != nil {
		return nil, fmt.Errorf("setup sandbox: %w", err)
	}

	wwc := &WriteWorkspaceContext{
		Sandbox:       sb,
		Owner:         owner,
		Repo:          repo,
		RepoInfo:      repoInfo,
		UseSession:    useSession,
		SessionBranch: sessionBranch,
	}
	cleanupOnErr := func() {
		if wwc.Sandbox != nil {
			wwc.Sandbox.Cleanup()
		}
	}

	// Create audit logger
	audit := sandbox.NewAuditLogger(factory.db, task.ID, agent.ID)
	wwc.Audit = audit

	git := sandbox.NewGit(sb)
	wwc.Git = git

	// Clone. Continuation tasks need full history: the LastHead anchor lives
	// on the session's draft branch, which a shallow clone of the default
	// branch cannot reach.
	var cloneResult *sandbox.Result
	if isContinuation {
		cloneResult = git.CloneFull(cloneURL)
	} else {
		cloneResult = git.Clone(cloneURL)
	}
	audit.LogCommand("git", []string{"clone", redactedCloneURL}, cloneResult)
	if cloneResult.Error != nil {
		errMsg := cloneResult.Stderr
		if errMsg == "" {
			errMsg = cloneResult.Error.Error()
		}
		cleanupOnErr()
		return nil, fmt.Errorf("clone repo: %s", errMsg)
	}

	// Preset git identity so the gateway's commit (during finalize) succeeds on
	// the first try. Convention: name = Gitea username, email = {user}@matea.local.
	setupAgentGitIdentity(git, agent.GiteaUsername)

	branchName, isExistingBranch := resolveBranchPlan(task, sessionBranch, git)
	wwc.BranchName = branchName

	// A session branch recorded by a previous task that failed before its first
	// push exists neither locally (fresh clone) nor on the remote — there is no
	// work to preserve, so start the branch fresh instead of failing the
	// checkout.
	//
	// Note the dispatcher copies the session branch into task.BaseBranch for
	// coder continuation when the webhook omits pull_request (pipeline.go), so
	// "BaseBranch set" does NOT imply a genuine PR head here. Treat BaseBranch
	// as session-derived when it is empty or equals the session branch; only a
	// BaseBranch differing from the session branch (a real PR head from the
	// webhook, solve_comment) must exist and keeps failing loud — as does the
	// LastHead anchor case below.
	sessionDerived := strings.TrimSpace(task.BaseBranch) == "" || task.BaseBranch == sessionBranch
	if isExistingBranch && sessionLastHead == "" && sessionDerived &&
		!git.LocalBranchExists(branchName) && !git.RemoteBranchExists("origin", branchName) {
		log.Printf("[WARN] Task %d session branch %s not found locally or on remote (previous task failed before first push?); starting it fresh", task.ID, branchName)
		isExistingBranch = false
	}
	// Surface local-only session branches early (before coding), so operators
	// see stranded work in logs even when the task later fails to push.
	if isExistingBranch {
		warnLocalOnlyBranch(git, branchName, task.ID)
	}

	switch {
	case isExistingBranch && sessionLastHead != "" && task.BaseBranch == "":
		// Continuation with a recorded head (B2.2): branch exactly from the
		// session's LastHead. It is reachable because the previous task pushed
		// the draft branch and we cloned full history. A missing anchor means
		// the remote branch was deleted/rewound meanwhile — fail loud so the
		// operator can archive the session instead of silently restarting work.
		anchorResult := sb.Execute("git", "checkout", "-b", branchName, sessionLastHead)
		audit.LogCommand("git", []string{"checkout", "-b", branchName, sessionLastHead}, anchorResult)
		if anchorResult.Error != nil {
			errMsg := anchorResult.Stderr
			if errMsg == "" {
				errMsg = anchorResult.Error.Error()
			}
			cleanupOnErr()
			return nil, fmt.Errorf("session continuation anchor %s not found (draft branch deleted or rewound on remote?): %s",
				sessionLastHead, errMsg)
		}
		log.Printf("[INFO] Task %d continues session at %s (branch %s)", task.ID, sessionLastHead[:8], branchName)
	case isExistingBranch:
		// solve_comment (PR head) or a legacy session branch without LastHead:
		// anchor on the remote branch head.
		if err := prepareExistingBranch(sb, git, audit, branchName); err != nil {
			cleanupOnErr()
			return nil, err
		}
		log.Printf("[INFO] Checked out existing branch: %s", branchName)
	default:
		// Create new branch
		branchResult := git.CreateBranch(branchName)
		audit.LogCommand("git", []string{"checkout", "-b", branchName}, branchResult)
		if branchResult.Error != nil {
			errMsg := branchResult.Stderr
			if errMsg == "" {
				errMsg = branchResult.Error.Error()
			}
			cleanupOnErr()
			return nil, fmt.Errorf("create branch: %s", errMsg)
		}
		saveSessionBranch(factory, task, branchName)
	}

	return wwc, nil
}

// warnLocalOnlyBranch logs a warning when the working branch exists locally but
// has no counterpart on the remote, so an operator can see unpushed work piling
// up on local-only session branches (P2 observability). It never fails the task.
func warnLocalOnlyBranch(git *sandbox.Git, branchName string, taskID int64) {
	if branchName == "" {
		return
	}
	if git.RemoteBranchExists("origin", branchName) {
		return
	}
	log.Printf("[WARN] Task %d branch %s is local-only (not pushed to origin); work may be stranded on a local branch", taskID, branchName)
}

// setupAgentGitIdentity configures the git identity used for gateway commits.
// name = Gitea username (fallback matea-agent), email = {name}@matea.local,
// matching the TASKS.md convention for attributing agent commits to the agent.
func setupAgentGitIdentity(git *sandbox.Git, giteaUsername string) {
	name := giteaUsername
	if name == "" {
		name = "matea-agent"
	}
	email := fmt.Sprintf("%s@matea.local", name)
	if res := git.ConfigUser(name, email); res.Error != nil {
		log.Printf("[WARN] git identity preset failed (%s <%s>): %v", name, email, res.Error)
	}
}

// finalizeWriteChanges checks for uncommitted changes, stages, commits, pushes,
// and creates or updates the PR. Behavior is identical to the finalize phase
// previously inlined in runWriteTask.
//
// The provider is resolved once in the coding phase (runWriteTask) and passed in
// so the same instance is reused for the commit-message LLM call, matching the
// pre-refactor behavior. If the workspace has no changes, a comment-style Result
// is returned without touching git/PR. The agentResult string is the coder's
// summary (used as PR body / comment content and as input to the commit-message
// generator).
func finalizeWriteChanges(ctx context.Context, wwc *WriteWorkspaceContext, task *store.Task, agent *store.Agent, factory *RunnerFactory, provider llm.Provider, taskSubType, agentResult string) (*Result, error) {
	git := wwc.Git
	branchName := wwc.BranchName
	audit := wwc.Audit

	// Check if there are changes to commit
	if !git.HasChanges() {
		// Fail closed when the coder dumped unexecuted tool markup and left the
		// tree clean — posting that as a success comment hides the real failure.
		if agentpkg.LooksLikePseudoToolCall(agentResult) {
			return nil, fmt.Errorf("coding produced no workspace changes and summary looks like an unexecuted tool call; check model supports_tools and tool_call API compatibility")
		}
		// OpenCode (or a prior turn) may already have committed on the working
		// branch, but NEVER report success until the branch is pushed and a PR
		// exists. If the agent committed locally and never pushed, the remote
		// head is missing and CreatePR would 404 — silently degrading to a
		// "success" comment. Push first, then open/update the PR. A push failure
		// is fatal; a PR failure after a successful push is also fatal (the
		// delivery is incomplete, not a success).
		if wwc.BranchName != "" && wwc.RepoInfo != nil &&
			wwc.BranchName != wwc.RepoInfo.DefaultBranch &&
			factory != nil && factory.giteaFactory != nil {
			// Attribute the delivery to the agent that ran the task (see
			// resolveTaskGiteaClient); falls back to admin only if the agent
			// carries no token.
			client := resolveTaskGiteaClient(factory.giteaFactory, agent)
			if client == nil {
				return nil, fmt.Errorf("cannot finalize delivery for branch %s: gitea client unavailable", wwc.BranchName)
			}
			// Push the local branch before opening the PR so the remote head
			// exists. This is a no-op when already in sync, and creates the
			// remote branch when the agent committed locally but never pushed.
			pushResult := git.Push("origin", wwc.BranchName)
			audit.LogCommand("git", []string{"push", "origin", wwc.BranchName}, pushResult)
			if pushResult.Error != nil {
				errMsg := pushResult.Stderr
				if errMsg == "" {
					errMsg = pushResult.Error.Error()
				}
				return nil, fmt.Errorf("push %s before opening PR failed: %s", wwc.BranchName, errMsg)
			}
			if wwc.UseSession {
				saveSessionProgress(factory, task, wwc.BranchName, git.HeadSHA())
			}
			res, err := FinalizeWriteTaskPR(client, wwc.Owner, wwc.Repo, wwc.BranchName, wwc.RepoInfo.DefaultBranch, task, taskSubType, agentResult)
			if err != nil {
				// Push may have succeeded, but without a PR the delivery is
				// incomplete — do NOT report success.
				return nil, fmt.Errorf("changes pushed to %s but PR creation failed: %w", wwc.BranchName, err)
			}
			return res, nil
		}
		return &Result{
			Content: agentResult,
			Action:  "comment",
		}, nil
	}

	// Stage and commit
	git.Add()
	commitMsg := GenerateCommitMessage(ctx, CommitMessageInput{
		Git:          git,
		Provider:     provider,
		Model:        agent.Model,
		Temperature:  factory.resolveTemperature(agent.Temperature, agent.Provider, agent.Model),
		TaskSubType:  taskSubType,
		Task:         task,
		AgentSummary: agentResult,
	})
	log.Printf("[INFO] Task %d commit message: %s", task.ID, commitMsg)
	commitResult := git.Commit(commitMsg)
	audit.LogCommand("git", []string{"commit"}, commitResult)
	if commitResult.Error != nil {
		return nil, fmt.Errorf("commit: %w", commitResult.Error)
	}

	// Push to remote
	pushResult := git.Push("origin", branchName)
	audit.LogCommand("git", []string{"push", "origin", branchName}, pushResult)
	if pushResult.Error != nil {
		errMsg := pushResult.Stderr
		if errMsg == "" {
			errMsg = pushResult.Error.Error()
		}
		return nil, fmt.Errorf("push: %s", errMsg)
	}

	// Update session continuation state after a successful push (B2.2): the
	// branch name plus its head SHA become the next task's anchor.
	if wwc.UseSession {
		saveSessionProgress(factory, task, branchName, git.HeadSHA())
	}

	client := resolveTaskGiteaClient(factory.giteaFactory, agent)
	if client == nil {
		return nil, fmt.Errorf("changes pushed to %s but cannot open PR: gitea client unavailable", branchName)
	}
	return FinalizeWriteTaskPR(client, wwc.Owner, wwc.Repo, branchName, wwc.RepoInfo.DefaultBranch, task, taskSubType, agentResult)
}

// prepareAnalyzeWorkspace sets up a temporary sandbox with a shallow clone of
// the repository's default branch for read-only analysis tasks.
//
// No branch is created, no changes are pushed, and the workspace is always
// cleaned up by the caller. On clone failure an error is returned so the
// caller can fall back to single-shot analysis.
func prepareAnalyzeWorkspace(ctx context.Context, task *store.Task, agent *store.Agent, factory *RunnerFactory) (*WriteWorkspaceContext, error) {
	_ = ctx

	// Parse repo owner/name
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	// Get Gitea client — guard against a nil factory (e.g. dispatch-only
	// factories in tests or deployments without Gitea) so workspace prep
	// returns an error the runner can react to instead of panicking.
	if factory.giteaFactory == nil {
		return nil, fmt.Errorf("gitea client factory not configured")
	}
	client := factory.giteaFactory.GetGiteaClient(agent.GiteaToken)

	// Get repo info for clone URL and default branch
	repoInfo, err := client.GetRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get repo info: %w", err)
	}
	cloneURL, err := gitea.AuthenticatedCloneURL(repoInfo.CloneURL, agent.GiteaUsername, agent.GiteaToken)
	if err != nil {
		return nil, fmt.Errorf("authenticated clone url: %w", err)
	}
	redactedCloneURL := gitea.RedactCloneURL(cloneURL)

	// Create temporary sandbox (always cleaned up by caller)
	sb := sandbox.New(factory.sandboxCfg, task.ID)
	if err := sb.Setup(); err != nil {
		return nil, fmt.Errorf("setup sandbox: %w", err)
	}

	wwc := &WriteWorkspaceContext{
		Sandbox:    sb,
		Owner:      owner,
		Repo:       repo,
		RepoInfo:   repoInfo,
		UseSession: false,
	}

	git := sandbox.NewGit(sb)
	wwc.Git = git

	// Shallow clone default branch
	cloneResult := git.CloneBranch(cloneURL, repoInfo.DefaultBranch)
	wwc.Audit = sandbox.NewAuditLogger(factory.db, task.ID, agent.ID)
	wwc.Audit.LogCommand("git", []string{"clone", "--depth", "1", "--branch", repoInfo.DefaultBranch, redactedCloneURL}, cloneResult)

	if cloneResult.Error != nil {
		errMsg := cloneResult.Stderr
		if errMsg == "" {
			errMsg = cloneResult.Error.Error()
		}
		sb.Cleanup()
		return nil, fmt.Errorf("clone repo: %s", errMsg)
	}

	return wwc, nil
}

// prepareReviewWorkspace sets up a temporary sandbox with a shallow clone of
// the pull request's head branch for code-review tasks (task 2.2.2, D7 second
// cut). OpenCode reads the files itself via shared-path directory binding, so
// the workspace is always cleaned up by the caller. On clone failure an error
// is returned so the runner can fall back to a single-shot review.
//
// Unlike prepareAnalyzeWorkspace (which clones the repo's default branch), this
// clones the exact PR head ref so OpenCode reviews the code as proposed, not
// the base branch. The head ref is resolved from the PR detail returned by
// gitea.PRHeadRef.
func prepareReviewWorkspace(ctx context.Context, task *store.Task, agent *store.Agent, factory *RunnerFactory) (*WriteWorkspaceContext, error) {
	_ = ctx

	// Parse repo owner/name
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}
	owner, repo := parts[0], parts[1]

	// Get Gitea client — guard against a nil factory (e.g. dispatch-only
	// factories in tests or deployments without Gitea) so workspace prep
	// returns an error the runner can react to instead of panicking.
	if factory.giteaFactory == nil {
		return nil, fmt.Errorf("gitea client factory not configured")
	}
	client := factory.giteaFactory.GetGiteaClient(agent.GiteaToken)

	prID := task.PRID
	if prID == 0 {
		prID = task.IssueID
	}

	// Get repo info for clone URL and PR head ref.
	repoInfo, err := client.GetRepo(owner, repo)
	if err != nil {
		return nil, fmt.Errorf("get repo info: %w", err)
	}
	cloneURL, err := gitea.AuthenticatedCloneURL(repoInfo.CloneURL, agent.GiteaUsername, agent.GiteaToken)
	if err != nil {
		return nil, fmt.Errorf("authenticated clone url: %w", err)
	}
	redactedCloneURL := gitea.RedactCloneURL(cloneURL)

	// Resolve the PR head ref so we clone the branch under review.
	pr, err := client.PRGet(owner, repo, prID)
	if err != nil {
		return nil, fmt.Errorf("get PR: %w", err)
	}
	headRef, err := gitea.PRHeadRef(pr)
	if err != nil {
		return nil, fmt.Errorf("get PR head ref: %w", err)
	}

	// Create temporary sandbox (always cleaned up by caller).
	sb := sandbox.New(factory.sandboxCfg, task.ID)
	if err := sb.Setup(); err != nil {
		return nil, fmt.Errorf("setup sandbox: %w", err)
	}

	wwc := &WriteWorkspaceContext{
		Sandbox:    sb,
		Owner:      owner,
		Repo:       repo,
		RepoInfo:   repoInfo,
		UseSession: false,
	}

	git := sandbox.NewGit(sb)
	wwc.Git = git

	// Shallow clone of the PR head branch.
	cloneResult := git.CloneBranch(cloneURL, headRef)
	wwc.Audit = sandbox.NewAuditLogger(factory.db, task.ID, agent.ID)
	wwc.Audit.LogCommand("git", []string{"clone", "--depth", "1", "--branch", headRef, redactedCloneURL}, cloneResult)

	if cloneResult.Error != nil {
		errMsg := cloneResult.Stderr
		if errMsg == "" {
			errMsg = cloneResult.Error.Error()
		}
		sb.Cleanup()
		return nil, fmt.Errorf("clone repo: %s", errMsg)
	}

	return wwc, nil
}

// prepareReplyWorkspace sets up a temporary sandbox directory with NO repository
// clone (task 2.2.3, D7 third cut, decision B). A reply is a pure conversation
// and OpenCode does not need the repository contents, so we create an empty
// temporary workspace solely to satisfy the OpenCode Submit contract's
// SandboxPath requirement (opencode_http.go Submit rejects an empty
// SandboxPath). The caller always cleans up the workspace.
//
// Unlike prepareAnalyzeWorkspace / prepareReviewWorkspace, this function never
// touches Gitea: the workspace is empty by design, so a nil giteaFactory is
// fine and can never panic here. On sandbox setup failure an error is returned
// so the runner can fall back to a single-shot reply.
func prepareReplyWorkspace(ctx context.Context, task *store.Task, agent *store.Agent, factory *RunnerFactory) (*WriteWorkspaceContext, error) {
	_ = ctx

	// Parse repo owner/name (kept for consistency with the sibling prepare*
	// helpers; not used for a network clone here).
	parts := strings.SplitN(task.Repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", task.Repo)
	}

	// No Gitea dependency: the workspace is empty by design, so a nil
	// giteaFactory is fine and never panics here (unlike analyze/review which
	// clone the repository).
	sb := sandbox.New(factory.sandboxCfg, task.ID)
	if err := sb.Setup(); err != nil {
		return nil, fmt.Errorf("setup sandbox: %w", err)
	}

	wwc := &WriteWorkspaceContext{
		Sandbox:    sb,
		Owner:      parts[0],
		Repo:       parts[1],
		UseSession: false,
	}
	wwc.Audit = sandbox.NewAuditLogger(factory.db, task.ID, agent.ID)
	return wwc, nil
}
