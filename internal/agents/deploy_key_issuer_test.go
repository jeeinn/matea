package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jeeinn/matea/internal/gitea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// A6 issuer coverage: fresh keypair per task, PEM private key round-trips,
// public half registered read-write, revoke idempotent with retry.

func TestGiteaDeployKeyIssuerIssue(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/o/r/keys", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{"id": 42, "read_only": false})
	}))
	defer srv.Close()

	issuer := NewGiteaDeployKeyIssuer(gitea.NewClient(srv.URL, "tok"))
	k1, err := issuer.Issue(context.Background(), "o", "r", "matea-hub-task-1")
	require.NoError(t, err)
	assert.Equal(t, int64(42), k1.KeyID)
	assert.Equal(t, false, gotBody["read_only"], "git_sync keys must be read-write")
	assert.Equal(t, "matea-hub-task-1", gotBody["title"])

	// Private key is a parseable PEM; public half matches the private half.
	priv, err := ssh.ParsePrivateKey([]byte(k1.PrivateKey))
	require.NoError(t, err, "issued private key must parse")
	assert.Equal(t, string(ssh.MarshalAuthorizedKey(priv.PublicKey())), k1.PublicKey)
	assert.Equal(t, k1.PublicKey, gotBody["key"])

	// Second task gets a DIFFERENT keypair (Gitea 422s on duplicate material).
	k2, err := issuer.Issue(context.Background(), "o", "r", "matea-hub-task-2")
	require.NoError(t, err)
	assert.NotEqual(t, k1.PublicKey, k2.PublicKey)
}

func TestGiteaDeployKeyIssuerIssueCreateFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity) // e.g. duplicate key material
	}))
	defer srv.Close()

	issuer := NewGiteaDeployKeyIssuer(gitea.NewClient(srv.URL, "tok"))
	_, err := issuer.Issue(context.Background(), "o", "r", "t")
	require.Error(t, err)
}

func TestGiteaDeployKeyIssuerRevokeRetriesTransientFailures(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			w.WriteHeader(http.StatusBadGateway) // transient
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	issuer := NewGiteaDeployKeyIssuer(gitea.NewClient(srv.URL, "tok")).(*giteaDeployKeyIssuer)
	issuer.retryDelay = func(int) time.Duration { return 0 }

	require.NoError(t, issuer.Revoke(context.Background(), "o", "r", 7))
	assert.Equal(t, int32(3), atomic.LoadInt32(&calls), "2 transient failures then success")
}

func TestGiteaDeployKeyIssuerRevokeExhaustsRetries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	issuer := NewGiteaDeployKeyIssuer(gitea.NewClient(srv.URL, "tok")).(*giteaDeployKeyIssuer)
	issuer.retryDelay = func(int) time.Duration { return 0 }

	err := issuer.Revoke(context.Background(), "o", "r", 7)
	require.Error(t, err, "persistent failure must surface (orphaned key warning path)")
}

func TestGiteaDeployKeyIssuerRevokeRespectsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	issuer := NewGiteaDeployKeyIssuer(gitea.NewClient(srv.URL, "tok")).(*giteaDeployKeyIssuer)
	issuer.retryDelay = func(int) time.Duration { return time.Hour }

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := issuer.Revoke(ctx, "o", "r", 7)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}
