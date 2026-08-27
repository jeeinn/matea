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
func FinalizeWriteTaskPR(adminClient *gitea.Client, owner, repo, branchName, baseBranch string, task *store.Task, taskSubType, agentResult string) (*Result, error) {
	existingPR, err := adminClient.FindOpenPRByHead(owner, repo, branchName)
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
	issueLink := ""
	if task.IssueID > 0 {
		issueLink = fmt.Sprintf("\n\nFixes #%d", task.IssueID)
	}
	prBody := fmt.Sprintf("## AI Generated Solution\n\n%s\n\n---\n*Task ID: %d*%s", contentPreview, task.ID, issueLink)

	pr, err := adminClient.CreatePR(owner, repo, gitea.CreatePRRequest{
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

