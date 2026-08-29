package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Adversarial coverage for the three-element validation (task A1/A7):
// wrong branch / wrong start point / missing footer / no changes / fake
// completion must all be rejected; only the fully consistent draft passes.

func testGitSyncInfo() *GitSyncInfo {
	return &GitSyncInfo{
		CloneURL:       "ssh://git@example.com/owner/repo.git",
		DraftBranch:    DraftBranchName(42),
		BaseBranch:     "main",
		BaseHEAD:       "aaaa0000",
		RequiredFooter: RequiredFooter(42),
		HubPush:        true,
	}
}

func TestDraftBranchNameAndFooter(t *testing.T) {
	assert.Equal(t, "matea/hub-42", DraftBranchName(42))
	assert.Equal(t, "matea-task-id: 42", RequiredFooter(42))
	assert.True(t, true)
}

func TestValidateGitSyncDraft_HappyPath(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:     "bbbb1111",
		BaseHEAD:      "aaaa0000",
		IsAncestor:    true,
		NewCommitMsgs: []string{"feat: change\n\nmatea-task-id: 42\n"},
	}
	require.NoError(t, validateGitSyncDraft(info, result, fetched, DiffPolicy{}))
}

func TestValidateGitSyncDraft_NilResult(t *testing.T) {
	err := validateGitSyncDraft(testGitSyncInfo(), nil, &fetchedDraft{}, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no git_sync result")
}

func TestValidateGitSyncDraft_WrongBranch(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "main", DraftHEAD: "bbbb1111"} // hub pushed a non-draft branch
	fetched := &fetchedDraft{DraftHEAD: "bbbb1111"}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch exclusivity")
}

func TestValidateGitSyncDraft_BranchNotPushed(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{DraftHEAD: ""} // fetch found nothing — fake completion
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found on remote")
}

func TestValidateGitSyncDraft_HeadMismatch(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "cccc2222"} // hub claims cccc
	fetched := &fetchedDraft{DraftHEAD: "bbbb1111", BaseHEAD: "aaaa0000"}        // remote has bbbb
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reported draft head")
}

func TestValidateGitSyncDraft_NoNewCommits(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "aaaa0000"}
	fetched := &fetchedDraft{DraftHEAD: "aaaa0000", BaseHEAD: "aaaa0000", IsAncestor: true}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no new commits")
}

func TestValidateGitSyncDraft_WrongStartPoint(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:  "bbbb1111",
		BaseHEAD:   "aaaa0000",
		IsAncestor: false, // branched off somewhere else entirely
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
}

// F5: the two real-world shapes of a wrong start point must be told apart — a
// draft that branched from the base tip (previous round's work silently lost)
// versus one with unrelated history.
func TestValidateGitSyncDraft_StartFromBaseNamesLostWork(t *testing.T) {
	info := testGitSyncInfo()
	info.AnchorHEAD = "eeee4444"
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:       "bbbb1111",
		BaseHEAD:        "aaaa0000",
		IsAncestor:      false,
		StartedFromBase: true,
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Contains(t, err.Error(), "branched from base")
	assert.Contains(t, err.Error(), "previous session's work is NOT in this branch")
	assert.Contains(t, err.Error(), "git checkout -b matea/hub-42 eeee4444", "the error tells the operator how to redo it")
}

func TestValidateGitSyncDraft_UnrelatedHistoryReportsNoMergeBase(t *testing.T) {
	info := testGitSyncInfo()
	info.AnchorHEAD = "eeee4444"
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:  "bbbb1111",
		BaseHEAD:   "aaaa0000",
		IsAncestor: false,
		MergeBase:  "", // unrelated histories: git merge-base found nothing
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start-point anchoring")
	assert.Contains(t, err.Error(), "shares no history")
}

func TestValidateGitSyncDraft_BaseDriftFails(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:     "bbbb1111",
		BaseHEAD:      "dddd3333", // base moved after Prepare (another PR merged)
		IsAncestor:    true,
		NewCommitMsgs: []string{"feat: change\n\nmatea-task-id: 42\n"},
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drifted")
}

func TestValidateGitSyncDraft_MissingFooter(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:     "bbbb1111",
		BaseHEAD:      "aaaa0000",
		IsAncestor:    true,
		NewCommitMsgs: []string{"feat: change without the footer\n"},
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required footer")
}

func TestValidateGitSyncDraft_OneCommitOfManyMissingFooter(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:  "bbbb1111",
		BaseHEAD:   "aaaa0000",
		IsAncestor: true,
		NewCommitMsgs: []string{
			"feat: part 1\n\nmatea-task-id: 42\n",
			"fix: sneaky unsigned commit\n",
		},
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "commit 2 of 2")
}

func TestValidateGitSyncDraft_EmptyCommitRange(t *testing.T) {
	info := testGitSyncInfo()
	result := &GitSyncResult{DraftBranch: "matea/hub-42", DraftHEAD: "bbbb1111"}
	fetched := &fetchedDraft{
		DraftHEAD:     "bbbb1111",
		BaseHEAD:      "aaaa0000",
		IsAncestor:    true,
		NewCommitMsgs: nil, // log produced nothing though heads differ — inconsistent
	}
	err := validateGitSyncDraft(info, result, fetched, DiffPolicy{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no new commits")
}
