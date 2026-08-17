package gitea

import (
	"encoding/json"
	"fmt"
)

// RepoInfo represents basic repository information.
type RepoInfo struct {
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	CloneURL      string `json:"clone_url"`
	// SSHURL is the ssh clone URL (git@host:owner/repo.git). git_sync hands
	// this to hubs together with a task-scoped deploy key (task A1).
	SSHURL string `json:"ssh_url"`
}

// GetRepo returns basic repository information.
func (c *Client) GetRepo(owner, repo string) (*RepoInfo, error) {
	body, err := c.do("GET", fmt.Sprintf("/repos/%s/%s", owner, repo), nil)
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}

	var info RepoInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("unmarshal repo: %w", err)
	}
	return &info, nil
}

// BranchInfo represents a branch head. Used by git_sync Prepare to anchor the
// draft branch's base HEAD (three-element validation, task A1).
type BranchInfo struct {
	Name   string `json:"name"`
	Commit struct {
		ID string `json:"id"`
	} `json:"commit"`
}

// GetBranch returns the branch with its head commit id.
func (c *Client) GetBranch(owner, repo, branch string) (*BranchInfo, error) {
	body, err := c.do("GET", fmt.Sprintf("/repos/%s/%s/branches/%s", owner, repo, branch), nil)
	if err != nil {
		return nil, fmt.Errorf("get branch: %w", err)
	}

	var info BranchInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("unmarshal branch: %w", err)
	}
	return &info, nil
}

// RepoItem represents a repository in a list response.
type RepoItem struct {
	FullName      string `json:"full_name"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

// ListRepos returns all repositories visible to the authenticated user.
func (c *Client) ListRepos() ([]RepoItem, error) {
	body, err := c.do("GET", "/repos/search?limit=1000", nil)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}

	var resp struct {
		Data []RepoItem `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal repos: %w", err)
	}
	return resp.Data, nil
}

// GetFileContent returns the content of a file in the repository.
func (c *Client) GetFileContent(owner, repo, ref, filepath string) (string, error) {
	body, err := c.do("GET", fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s", owner, repo, filepath, ref), nil)
	if err != nil {
		return "", fmt.Errorf("get file: %w", err)
	}

	var file struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.Unmarshal(body, &file); err != nil {
		return "", fmt.Errorf("unmarshal file: %w", err)
	}
	return file.Content, nil
}
