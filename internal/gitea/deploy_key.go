package gitea

import (
	"encoding/json"
	"fmt"
	"time"
)

// Deploy key API (git_sync task A6). Contract verified against Gitea 1.22.6 in
// the A0.2 spike (docs/20260817-a0-spike-results.md):
//   - create → 201 with {id, fingerprint, read_only}
//   - delete → 204, idempotent (deleting a missing id also returns 204) and
//     effective immediately
//   - a token with write:repository scope suffices — no admin required
//   - identical public key material on the same repo → 422 (each task must use
//     a freshly generated keypair)

// DeployKey is a repo-scoped deploy key.
type DeployKey struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Fingerprint string    `json:"fingerprint"`
	ReadOnly    bool      `json:"read_only"`
	CreatedAt   time.Time `json:"created_at"` // B4 sweep grace window; zero if the server omits it
}

// CreateDeployKey registers publicKey as a deploy key on owner/repo. readOnly
// false yields a read-write key (git_sync hubs push draft branches with it).
func (c *Client) CreateDeployKey(owner, repo, title, publicKey string, readOnly bool) (*DeployKey, error) {
	body, err := c.do("POST", fmt.Sprintf("/repos/%s/%s/keys", owner, repo), map[string]interface{}{
		"title":     title,
		"key":       publicKey,
		"read_only": readOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("create deploy key: %w", err)
	}

	var key DeployKey
	if err := json.Unmarshal(body, &key); err != nil {
		return nil, fmt.Errorf("unmarshal deploy key: %w", err)
	}
	return &key, nil
}

// DeleteDeployKey removes a deploy key by id. Idempotent server-side (204 even
// when the id is gone), so callers may retry freely.
func (c *Client) DeleteDeployKey(owner, repo string, keyID int64) error {
	_, err := c.do("DELETE", fmt.Sprintf("/repos/%s/%s/keys/%d", owner, repo, keyID), nil)
	if err != nil {
		return fmt.Errorf("delete deploy key %d: %w", keyID, err)
	}
	return nil
}

// ListDeployKeys returns all deploy keys on owner/repo (sweep/reconcile use).
func (c *Client) ListDeployKeys(owner, repo string) ([]DeployKey, error) {
	body, err := c.do("GET", fmt.Sprintf("/repos/%s/%s/keys", owner, repo), nil)
	if err != nil {
		return nil, fmt.Errorf("list deploy keys: %w", err)
	}

	var keys []DeployKey
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, fmt.Errorf("unmarshal deploy keys: %w", err)
	}
	return keys, nil
}
