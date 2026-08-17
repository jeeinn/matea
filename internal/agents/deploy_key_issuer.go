package agents

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"log"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"golang.org/x/crypto/ssh"
)

// giteaDeployKeyIssuer is the task A6 DeployKeyIssuer: it generates a fresh
// ed25519 keypair per task and registers/revokes the public half through the
// Gitea deploy keys API. Task-scoped, repo-scoped, read-write (the draft
// branch prefix restriction is enforced by Approve's three-element validation,
// not by the credential — Gitea deploy keys have no per-branch granularity).
//
// Spike-derived behaviors (A0.2): delete is idempotent so Revoke retries are
// safe; the repo-scoped token on the passed client is sufficient (no admin).
type giteaDeployKeyIssuer struct {
	client *gitea.Client
	// retryDelay overrides the revoke backoff (attempt 1-based). nil → default.
	retryDelay func(attempt int) time.Duration
}

// NewGiteaDeployKeyIssuer builds the production DeployKeyIssuer. The client
// must carry a token with write:repository scope on the target repos.
func NewGiteaDeployKeyIssuer(client *gitea.Client) DeployKeyIssuer {
	return &giteaDeployKeyIssuer{client: client}
}

func (i *giteaDeployKeyIssuer) Issue(ctx context.Context, owner, repo, title string) (*IssuedDeployKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 keypair: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("marshal public key: %w", err)
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))

	privBlock, err := ssh.MarshalPrivateKey(priv, title)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	privPEM := string(pem.EncodeToMemory(privBlock))

	key, err := i.client.CreateDeployKey(owner, repo, title, pubLine, false)
	if err != nil {
		return nil, err
	}
	return &IssuedDeployKey{
		KeyID:      key.ID,
		PrivateKey: privPEM,
		PublicKey:  pubLine,
	}, nil
}

func (i *giteaDeployKeyIssuer) Revoke(ctx context.Context, owner, repo string, keyID int64) error {
	// Delete is idempotent (204 on missing), so retrying transient failures is
	// safe and preferred over leaking a read-write credential.
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if err := i.client.DeleteDeployKey(owner, repo, keyID); err != nil {
			lastErr = err
			delay := time.Duration(attempt) * 2 * time.Second
			if i.retryDelay != nil {
				delay = i.retryDelay(attempt)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			continue
		}
		return nil
	}
	log.Printf("[WARN] deploy key %d on %s/%s: revoke failed after 3 attempts: %v (orphaned key needs manual sweep or a lifecycle hook)",
		keyID, owner, repo, lastErr)
	return fmt.Errorf("revoke deploy key %d after retries: %w", keyID, lastErr)
}
