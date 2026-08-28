package agents

import (
	"fmt"
	"log"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// FinalizeWriteTaskPR comments on an existing open PR or creates one if the
// branch has no open PR yet.
//
// Extracted as an exported helper in git_sync task A1: the builtin write path
// (finalizeWriteChanges) and the git_sync transport's Approve both need the
// exact same "PR exists → report update, else create" semantics. Under
// git_sync the hub has already committed and pushed the draft branch, so
// Approve skips straight here after fetch + three-element validation — no
// re-commit, no re-push.
//
// client carries the identity the delivery is attributed to — normally the
// agent that ran the task (see resolveTaskGiteaClient). It used to be named
// adminClient because every call site passed the platform admin; PRs therefore
// showed up as authored by the admin account, and Gitea's cross-reference
// timeline events ("X referenced this issue") inherited that attribution.
func FinalizeWriteTaskPR(client *gitea.Client, owner, repo, branchName, baseBranch string, task *store.Task, taskSubType, agentResult string) (*Result, error) {
	existingPR, err := client.FindOpenPRByHead(owner, repo, branchName)
	if err != nil {
		return nil, fmt.Errorf("find open PR: %w", err)
	}
	if existingPR != nil {
		log.Printf("[INFO] Task %d updated existing branch: %s (PR #%d)", task.ID, branchName, existingPR.Number)
		return &Result{
			Content: fmt.Sprintf("🔄 Updated PR branch `%s` with new changes.\n\n%s", branchName, agentResult),
			Action:  "comment",
			PRID:    existingPR.Number,
		}, nil
	}

	prTitle := fmt.Sprintf("AI Solution: %s", task.Event)
	if taskSubType == "bugfix" {
		prTitle = fmt.Sprintf("Bugfix: %s", task.Event)
	}
	contentPreview := agentResult
	if len(contentPreview) > 500 {
		contentPreview = contentPreview[:500] + "..."
	}
	// "Refs #N", not "Fixes #N": Gitea's CLOSE_KEYWORDS (close/closes/closed/
	// fix/fixes/fixed/resolve/resolves/resolved) make a merged PR auto-close the
	// referenced issue. "refs" is deliberately absent from that list, so the PR
	// still records the cross-reference in the issue timeline but closing the
	// issue stays a human decision.
	issueLink := ""
	if task.IssueID > 0 {
		issueLink = fmt.Sprintf("\n\nRefs #%d", task.IssueID)
	}
	prBody := fmt.Sprintf("## AI Generated Solution\n\n%s\n\n---\n*Task ID: %d*%s", contentPreview, task.ID, issueLink)

	pr, err := client.CreatePR(owner, repo, gitea.CreatePRRequest{
		Title: prTitle,
		Body:  prBody,
		Head:  branchName,
		Base:  baseBranch,
	})
	if err != nil {
		return nil, fmt.Errorf("create PR: %w", err)
	}

	log.Printf("[INFO] Task %d PR created: %s (PR #%d)", task.ID, pr.HTMLURL, pr.Number)
	return &Result{
		// Use Gitea's native #N reference syntax so the link renders correctly
		// regardless of Gitea's ROOT_URL configuration (e.g. localhost setups).
		Content: fmt.Sprintf("✅ PR created: #%d\n\n%s", pr.Number, agentResult),
		Action:  "pr",
		PRID:    pr.Number,
	}, nil
}

// resolveTaskGiteaClient returns the Gitea client a task's delivery should be
// attributed to: the agent that actually ran the task, not the platform admin.
//
// Rationale: agents are ordinary Gitea users with repo write access, so opening
// a PR needs no admin rights. Using the agent's own token makes the PR author —
// and every timeline event Gitea derives from it (e.g. "X referenced this
// issue") — name the worker that produced the change, which is what an operator
// expects to see. The admin client is reserved for platform-level actions that
// belong to no agent (deploy key issuance, orphaned key sweeps).
//
// Fallback: if the agent or its token is missing, this degrades to the admin
// client and logs a warning. An unattributed-but-delivered PR beats a hard
// failure, and the warning keeps the misconfiguration visible in logs.
func resolveTaskGiteaClient(gf GiteaClientFactory, agent *store.Agent) *gitea.Client {
	if gf == nil {
		return nil
	}
	if agent != nil && agent.GiteaToken != "" {
		return gf.GetGiteaClient(agent.GiteaToken)
	}
	name := "<nil>"
	if agent != nil {
		name = agent.Name
	}
	log.Printf("[WARN] agent %q has no Gitea token; falling back to admin client for PR delivery", name)
	return gf.GetAdminGiteaClient()
}
