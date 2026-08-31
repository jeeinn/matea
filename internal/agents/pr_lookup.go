package agents

import (
	"fmt"

	"github.com/jeeinn/matea/internal/gitea"
)

// prLookup memoizes the Gitea reads a single git_sync Prepare performs.
//
// Prepare asks about the same objects more than once by design, because the
// questions are independent: resolveGitSyncBaseBranch wants the PR's base ref
// ("where should this work land"), resolveGitSyncDraftBranch wants its head ref
// and tip ("which branch may the hub push"). Both answers come from one
// GET /pulls/{index}. Likewise the repository is read for its ssh_url and then
// possibly again for its default branch.
//
// The lookup keeps those call sites independent without paying for the extra
// round trips. It is deliberately per-Prepare and not a long-lived cache: a PR
// head moves whenever anything pushes to it, so a value cached across tasks
// would eventually anchor a hub at a stale tip — the exact failure the
// continuation anchor exists to prevent.
//
// Errors are memoized too. A task on a plain issue makes /pulls 404, and
// repeating that lookup per resolver only doubles the noise in the log.
type prLookup struct {
	client *gitea.Client
	owner  string
	repo   string

	prs  map[int]prLookupResult
	info *gitea.RepoInfo
	err  error
	done bool
}

type prLookupResult struct {
	pr  map[string]interface{}
	err error
}

func newPRLookup(client *gitea.Client, owner, repo string) *prLookup {
	return &prLookup{
		client: client,
		owner:  owner,
		repo:   repo,
		prs:    make(map[int]prLookupResult, 2),
	}
}

// PR returns GET /pulls/{index}, reading it at most once per index.
func (l *prLookup) PR(index int) (map[string]interface{}, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("gitea client unavailable")
	}
	if got, ok := l.prs[index]; ok {
		return got.pr, got.err
	}
	pr, err := l.client.PRGet(l.owner, l.repo, index)
	l.prs[index] = prLookupResult{pr: pr, err: err}
	return pr, err
}

// Repo returns the repository metadata, reading it at most once.
func (l *prLookup) Repo() (*gitea.RepoInfo, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("gitea client unavailable")
	}
	if l.done {
		return l.info, l.err
	}
	l.info, l.err = l.client.GetRepo(l.owner, l.repo)
	l.done = true
	return l.info, l.err
}

// Available reports whether the lookup can reach Gitea at all. Callers that
// degrade gracefully (a missing PR is not a fatal error) use it instead of
// treating "no client" as "no PR".
func (l *prLookup) Available() bool {
	return l != nil && l.client != nil
}
