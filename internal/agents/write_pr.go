package agents

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
)

// maxPRTitleRunes caps how much of the issue title goes into the PR title.
// Gitea renders the title in list views and in the merge commit subject, so an
// untruncated issue title can push out the part that identifies the branch.
const maxPRTitleRunes = 60

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
			Content: fmt.Sprintf("🔄 已更新 PR 分支 `%s`\n\n%s", branchName, agentResult),
			Action:  "comment",
			PRID:    existingPR.Number,
		}, nil
	}

	// task.Event is the webhook event NAME ("issues", "issue_comment"), not the
	// issue title, so every PR used to be titled "AI Solution: issues" —
	// jeeinn/rust-study PR #8 is a live example, and it tells a reviewer
	// nothing about what the PR changes. Resolve the real subject instead.
	subject := prTitleSubject(client, owner, repo, task)
	prefix := "AI Solution"
	if taskSubType == "bugfix" {
		prefix = "Bugfix"
	}
	prTitle := buildPRTitle(prefix, subject)
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
	prBody := fmt.Sprintf("## AI 生成的解决方案\n\n%s\n\n---\n*Task ID: %d*%s", contentPreview, task.ID, issueLink)

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
		Content: fmt.Sprintf("✅ PR 已创建：#%d\n\n%s", pr.Number, agentResult),
		Action:  "pr",
		PRID:    pr.Number,
	}, nil
}

// prTitlePrefixes are the subjects FinalizeWriteTaskPR puts in front of a PR
// title. A subject that already carries one came from another Matea PR and
// must not be prefixed twice: jeeinn/rust-study PR #9 was titled
// "AI Solution: AI Solution: issues" because PR #8's own title was used as the
// subject verbatim.
var prTitlePrefixes = []string{"AI Solution:", "Bugfix:"}

// webhookEventNames are the values store.Task.Event can hold. A subject left
// over from one of these is the fingerprint of the pre-2026-08-29 bug that
// titled every PR after the webhook event name ("AI Solution: issues"); it
// says nothing about the change and must not be propagated to the next PR.
var webhookEventNames = map[string]bool{
	"issues": true, "issue_comment": true, "issue_assign": true,
	"pull_request": true, "pull_request_comment": true, "pull_request_assign": true,
	"pull_request_review": true, "pull_request_review_request": true,
	"push": true, "create": true, "delete": true, "fork": true, "release": true,
}

// refsIssuePattern matches the "Refs #N" line FinalizeWriteTaskPR appends to a
// PR body. Deliberately line-anchored: an inline "#N" inside a quoted diff or a
// code block is not a cross-reference.
//
// workflow.linkedIssuePattern does NOT match "refs" (it only knows Gitea's
// close keywords), which is why a continuation task on a PR loses the link to
// the original issue and inherits the PR's title instead. The title only needs
// the reference, so it is read here rather than by relaxing that pattern —
// task.IssueID drives session keys and comment writeback, and changing what it
// resolves to would move where replies land.
var refsIssuePattern = regexp.MustCompile(`(?im)^refs\s+#(\d+)\s*$`)

// buildPRTitle joins a prefix and a subject without stacking prefixes.
func buildPRTitle(prefix, subject string) string {
	for _, p := range prTitlePrefixes {
		if strings.HasPrefix(subject, p) {
			subject = strings.TrimSpace(strings.TrimPrefix(subject, p))
			break
		}
	}
	if subject == "" {
		return prefix
	}
	return prefix + ": " + subject
}

// prTitleSubject resolves what a PR is actually about, for use in its title.
//
// task.Event holds the webhook event NAME ("issues", "issue_comment") — not
// the subject of the work — so titles built from it came out as
// "AI Solution: issues" (jeeinn/rust-study PR #8). Resolution order:
//
//  1. the title of the linked issue or PR;
//  2. if that title is itself an event-name leftover, the title of the issue
//     referenced by the "Refs #N" line of its body — the original request the
//     PR was answering (PR #8's body carries "Refs #7", whose title is
//     "更新项目的readme，符合当前项目最新描述");
//  3. the task id, which at least identifies the task.
//
// An event name is never used as a title subject.
func prTitleSubject(client *gitea.Client, owner, repo string, task *store.Task) string {
	if task == nil {
		return "AI 任务"
	}
	if client == nil {
		return fmt.Sprintf("Task %d", task.ID)
	}
	seen := make(map[int]bool, 2)
	for _, id := range []int{task.IssueID, task.PRID} {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true

		obj, err := client.IssueGet(owner, repo, id)
		if err != nil {
			log.Printf("[WARN] Task %d: cannot read title of %s/%s#%d for PR title: %v", task.ID, owner, repo, id, err)
			continue
		}
		title, _ := obj["title"].(string)
		if subject, ok := cleanPRTitleSubject(title); ok {
			return truncatePRTitle(subject)
		}
		// Hop 2: the object's own title is unusable, follow its "Refs #N".
		body, _ := obj["body"].(string)
		if ref := extractRefsIssue(body); ref > 0 && !seen[ref] {
			seen[ref] = true
			linked, err := client.IssueGet(owner, repo, ref)
			if err != nil {
				log.Printf("[WARN] Task %d: cannot read title of linked %s/%s#%d for PR title: %v", task.ID, owner, repo, ref, err)
				continue
			}
			linkedTitle, _ := linked["title"].(string)
			if subject, ok := cleanPRTitleSubject(linkedTitle); ok {
				log.Printf("[INFO] Task %d: PR title falls back to linked #%d (%s/%s#%d is an event-name leftover)", task.ID, ref, owner, repo, id)
				return truncatePRTitle(subject)
			}
		}
	}
	return fmt.Sprintf("Task %d", task.ID)
}

// cleanPRTitleSubject reports whether title is usable as a PR title subject,
// stripping a Matea prefix when present. Empty titles and webhook event names
// (the fingerprint of the old event-name bug) are rejected.
func cleanPRTitleSubject(title string) (string, bool) {
	subject := strings.TrimSpace(title)
	for _, p := range prTitlePrefixes {
		if strings.HasPrefix(subject, p) {
			subject = strings.TrimSpace(strings.TrimPrefix(subject, p))
			break
		}
	}
	if subject == "" {
		return "", false
	}
	if webhookEventNames[strings.ToLower(subject)] {
		return "", false
	}
	return subject, true
}

// extractRefsIssue returns the issue number of a body's "Refs #N" line, or 0.
func extractRefsIssue(body string) int {
	m := refsIssuePattern.FindStringSubmatch(body)
	if len(m) < 2 {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// truncatePRTitle shortens s to maxPRTitleRunes, appending an ellipsis.
func truncatePRTitle(s string) string {
	runes := []rune(s)
	if len(runes) <= maxPRTitleRunes {
		return s
	}
	return string(runes[:maxPRTitleRunes]) + "..."
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
