package agents

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/jeeinn/matea/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// B5 real-Gitea E2E harness for the git_sync trust model.
//
// These tests are gated by environment variables so CI never depends on a live
// Gitea instance. To run locally:
//
//	# 1. Start Gitea (Docker example from A0 spike):
//	docker run -d --name gitea-e2e -p 13000:3000 -p 12222:22 \
//	  -e GITEA__security__INSTALL_LOCK=true \
//	  -e GITEA__server__ROOT_URL=http://localhost:13000/ \
//	  -e GITEA__server__SSH_PORT=12222 \
//	  gitea/gitea:1.22
//
//	# 2. Create an access token with write:repository scope.
//
//	# 3. Run:
//	export MATEA_E2E_GITEA_URL=http://localhost:13000
//	export MATEA_E2E_GITEA_TOKEN=your_token
//	export MATEA_E2E_GITEA_SSH_PORT=12222
//	go test ./internal/agents/ -run TestE2EGitSync -v -count=1
//
// A0.2 confirmed write:repository is sufficient to manage deploy keys.

func e2eGiteaClient(t *testing.T) *gitea.Client {
	t.Helper()
	base := os.Getenv("MATEA_E2E_GITEA_URL")
	token := os.Getenv("MATEA_E2E_GITEA_TOKEN")
	if base == "" || token == "" {
		t.Skip("set MATEA_E2E_GITEA_URL and MATEA_E2E_GITEA_TOKEN for real Gitea E2E")
	}
	return gitea.NewClient(base, token)
}

func e2eOwnerRepo(t *testing.T) (string, string) {
	t.Helper()
	owner := os.Getenv("MATEA_E2E_GITEA_OWNER")
	repo := os.Getenv("MATEA_E2E_GITEA_REPO")
	if owner == "" {
		owner = "e2e"
	}
	if repo == "" {
		repo = "git-sync-e2e"
	}
	return owner, repo
}

func e2eSSHPort() string {
	p := os.Getenv("MATEA_E2E_GITEA_SSH_PORT")
	if p == "" {
		return "22"
	}
	return p
}

// e2eRunGit is a test helper that runs git and fails loudly.
func e2eRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	return strings.TrimSpace(string(out))
}

// TestE2EGitSyncDeployKeyLifecycle verifies the A0.2 contract: a token with
// write:repository scope can issue a task-scoped rw deploy key, the key can be
// used for SSH git operations, and DELETE /keys/{id} revokes it immediately.
func TestE2EGitSyncDeployKeyLifecycle(t *testing.T) {
	client := e2eGiteaClient(t)
	owner, repo := e2eOwnerRepo(t)

	// Ensure the repo exists and is reachable.
	info, err := client.GetRepo(owner, repo)
	require.NoError(t, err, "repo %s/%s must exist", owner, repo)
	require.NotEmpty(t, info.SSHURL, "Gitea must expose an ssh_url")
	sshURL := e2eRewriteSSHPort(info.SSHURL, e2eSSHPort())

	// Generate a one-off Ed25519 keypair for this test.
	pub, priv, kerr := e2eGenerateKeyPair(t)
	require.NoError(t, kerr)

	// Issue a task-scoped rw deploy key. Drop any orphan sharing this title
	// from a prior (failed) run first so CreateDeployKey doesn't 422.
	const e2eKeyTitle = "matea-hub-task-e2e-42"
	if existing, lerr := client.ListDeployKeys(owner, repo); lerr == nil {
		for _, k := range existing {
			if k.Title == e2eKeyTitle {
				_ = client.DeleteDeployKey(owner, repo, k.ID)
			}
		}
	}
	key, err := client.CreateDeployKey(owner, repo, e2eKeyTitle, pub, false)
	require.NoError(t, err, "deploy key creation requires write:repository scope")
	require.NotZero(t, key.ID, "deploy key id must be returned")

	// Clone with the deploy key and push a test branch.
	base := t.TempDir()
	work := filepath.Join(base, "work")
	e2eCloneWithKey(t, sshURL, priv, work)
	e2eRunGit(t, work, "config", "user.email", "e2e@matea.test")
	e2eRunGit(t, work, "config", "user.name", "Matea E2E")
	require.NoError(t, os.WriteFile(filepath.Join(work, "E2E.md"), []byte("real Gitea\n"), 0o644))
	e2eRunGit(t, work, "add", "-A")
	e2eRunGit(t, work, "commit", "-q", "-m", "e2e: deploy key write check")
	e2eRunGit(t, work, "push", "-q", "origin", "HEAD:refs/heads/e2e-key-check")

	// Revoke the key.
	require.NoError(t, client.DeleteDeployKey(owner, repo, key.ID), "deploy key delete must be idempotent-safe")

	// A second push must fail now that the key is revoked.
	require.NoError(t, os.WriteFile(filepath.Join(work, "E2E.md"), []byte("after revoke\n"), 0o644))
	e2eRunGit(t, work, "add", "-A")
	e2eRunGit(t, work, "commit", "-q", "-m", "e2e: after revoke")
	cmd := exec.Command("git", "push", "-q", "origin", "HEAD:refs/heads/e2e-key-check-2")
	cmd.Dir = work
	_, pushErr := cmd.CombinedOutput()
	assert.Error(t, pushErr, "push with revoked deploy key must fail")
}

// e2eGenerateKeyPair creates a fresh Ed25519 key pair and returns
// (publicKeyOpenSSH, privateKeyPEM, error). It uses the same Go crypto path as
// the production DeployKeyIssuer (golang.org/x/crypto/ssh) so the generated
// OpenSSH PEM is byte-compatible with what the hub receives — and avoids the
// Windows ssh-keygen/MinGW-ssh key-loading quirk that breaks the harness.
func e2eGenerateKeyPair(t *testing.T) (string, string, error) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return "", "", err
	}
	pubLine := string(ssh.MarshalAuthorizedKey(sshPub))
	privBlock, err := ssh.MarshalPrivateKey(priv, "matea-e2e")
	if err != nil {
		return "", "", err
	}
	privPEM := string(pem.EncodeToMemory(privBlock))
	return strings.TrimSpace(pubLine), privPEM, nil
}

// TestE2EGitSyncFullCycle exercises the complete Matea-side git_sync flow
// against a real Gitea: Prepare issues a deploy key, a simulated hub pushes
// the mandated draft branch with the required footer, Approve validates and
// opens a PR, and Cleanup revokes the key.
func TestE2EGitSyncFullCycle(t *testing.T) {
	client := e2eGiteaClient(t)
	owner, repo := e2eOwnerRepo(t)
	sshPort := e2eSSHPort()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	repoInfo, err := client.GetRepo(owner, repo)
	require.NoError(t, err, "repo %s/%s must exist", owner, repo)
	require.NotEmpty(t, repoInfo.SSHURL)
	cloneURL := e2eRewriteSSHPort(repoInfo.SSHURL, sshPort)

	baseBranch := "main"
	mainCommit, err := client.GetBranch(owner, repo, baseBranch)
	require.NoError(t, err)
	require.NotEmpty(t, mainCommit.Commit.ID)

	taskID := int64(4242)
	branch := DraftBranchName(taskID)
	footer := RequiredFooter(taskID)

	// Prepare: issue the task-scoped deploy key.
	issuer := NewGiteaDeployKeyIssuer(client)
	key, err := issuer.Issue(ctx, owner, repo, fmt.Sprintf("matea-hub-task-%d", taskID))
	require.NoError(t, err)
	require.NotZero(t, key.KeyID)

	// Simulated hub: clone with the deploy key, checkout draft branch, commit
	// with footer, push.
	base := t.TempDir()
	work := filepath.Join(base, "work")
	e2eCloneWithKey(t, cloneURL, key.PrivateKey, work)
	e2eRunGit(t, work, "config", "user.email", "hub@matea.test")
	e2eRunGit(t, work, "config", "user.name", "Simulated Hub")
	e2eRunGit(t, work, "checkout", "-q", "-b", branch)
	require.NoError(t, os.WriteFile(filepath.Join(work, "FIX.md"), []byte("fix from hub\n"), 0o644))
	e2eRunGit(t, work, "add", "-A")
	e2eRunGit(t, work, "commit", "-q", "-m", "feat: real hub fix", "-m", footer)
	e2eRunGit(t, work, "push", "-q", "origin", branch)

	// Approve through the real transport.
	factory := &gitSyncTestGiteaFactory{client: client}
	transport := NewGitSyncTransport(factory, issuer, t.TempDir(), DiffPolicy{})
	gsyncInfo := &GitSyncInfo{
		CloneURL:       cloneURL,
		DraftBranch:    branch,
		BaseBranch:     baseBranch,
		BaseHEAD:       mainCommit.Commit.ID,
		RequiredFooter: footer,
		HubPush:        true,
	}
	result := &GitSyncResult{DraftBranch: branch}

	agent := &store.Agent{ID: 1}
	task := &store.Task{ID: taskID, Repo: fmt.Sprintf("%s/%s", owner, repo), IssueID: 1, TaskType: "solve_issue", Event: "E2E fix"}
	res, aerr := transport.Approve(ctx, task, agent, owner, repo, gsyncInfo, result, "real hub did the work")
	require.NoError(t, aerr)
	require.NotNil(t, res)
	assert.Equal(t, "pr", res.Action)
	assert.Greater(t, res.PRID, 0, "Approve must open a real PR")

	// Cleanup revokes the key.
	require.NoError(t, transport.Cleanup(ctx, owner, repo, key))

	// Verify the key is gone.
	keys, err := client.ListDeployKeys(owner, repo)
	require.NoError(t, err)
	for _, k := range keys {
		require.NotEqual(t, key.KeyID, k.ID, "deploy key %d must be revoked", key.KeyID)
	}
}

// e2eCloneWithKey clones url into dir using the provided PEM private key via
// GIT_SSH_COMMAND. This mirrors how a real hub consumes GitSyncInfo.PrivateKey.
func e2eCloneWithKey(t *testing.T, cloneURL, privateKey, dir string) {
	t.Helper()
	keyFile := filepath.Join(t.TempDir(), "task_key")
	require.NoError(t, os.WriteFile(keyFile, []byte(privateKey), 0o600))

	// Windows portability: t.TempDir() yields a Windows path. ssh on Windows
	// cannot load a deploy key from a backslash -i path ("error in libcrypto:
	// unsupported"), which makes the clone hang; and filepath.ToSlash alone
	// yields "C:/..." which ssh parses as host "C:". Convert to the MSYS
	// POSIX form "/c/..." that Git-bash's ssh accepts, so the deploy key
	// loads and the SSH push succeeds.
	posixKey := filepath.ToSlash(keyFile)
	if len(posixKey) >= 2 && posixKey[1] == ':' {
		posixKey = "/" + strings.ToLower(string(posixKey[0])) + posixKey[2:]
	}
	sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", posixKey)
	// Propagate to every subsequent git op in this test (commit/push) so they
	// also authenticate with the deploy key and trust the host key.
	t.Setenv("GIT_SSH_COMMAND", sshCmd)
	cmd := exec.Command("git", "clone", "-q", cloneURL, dir)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+sshCmd)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git clone with deploy key: %s", out)
}

// e2eRewriteSSHPort replaces the port in a Gitea ssh_url with the configured
// E2E SSH port. Gitea's web UI often reports the container-internal port (22)
// even when it is exposed on a host port.
func e2eRewriteSSHPort(sshURL, port string) string {
	u, err := url.Parse(sshURL)
	if err != nil {
		return sshURL
	}
	if u.Scheme == "ssh" {
		// ssh://git@host:port/path
		parts := strings.Split(u.Host, ":")
		if len(parts) == 2 {
			u.Host = parts[0] + ":" + port
		} else {
			u.Host = u.Host + ":" + port
		}
		return u.String()
	}
	// SCP-style git@host:path — rewrite host portion.
	if idx := strings.Index(sshURL, ":"); idx > strings.Index(sshURL, "@") {
		host := sshURL[:idx]
		if strings.Contains(host, ":") {
			parts := strings.Split(host, ":")
			host = parts[0] + ":" + port
		} else {
			host = host + ":" + port
		}
		return host + sshURL[idx:]
	}
	return sshURL
}
