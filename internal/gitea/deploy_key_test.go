package gitea

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mirrors the A0.2 spike contract (Gitea 1.22.6): create → 201 {id,...},
// delete → 204 idempotent, list → array.

func TestCreateDeployKey(t *testing.T) {
	var gotBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/repos/o/r/keys", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": 7, "title": gotBody["title"], "fingerprint": "SHA256:x", "read_only": false,
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	key, err := c.CreateDeployKey("o", "r", "matea-hub-task-9", "ssh-ed25519 AAAA", false)
	require.NoError(t, err)
	assert.Equal(t, int64(7), key.ID)
	assert.Equal(t, "matea-hub-task-9", gotBody["title"])
	assert.Equal(t, "ssh-ed25519 AAAA", gotBody["key"])
	assert.Equal(t, false, gotBody["read_only"])
}

func TestDeleteDeployKey(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/api/v1/repos/o/r/keys/7", r.URL.Path)
		require.Equal(t, http.MethodDelete, r.Method)
		w.WriteHeader(http.StatusNoContent) // 204 even when missing (spike-verified)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	require.NoError(t, c.DeleteDeployKey("o", "r", 7))
	require.NoError(t, c.DeleteDeployKey("o", "r", 7), "delete must be idempotent-safe")
	assert.Equal(t, 2, calls)
}

func TestListDeployKeys(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": 1, "title": "matea-hub-task-1", "read_only": false},
			{"id": 2, "title": "ro-key", "read_only": true},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	keys, err := c.ListDeployKeys("o", "r")
	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, "matea-hub-task-1", keys[0].Title)
	assert.True(t, keys[1].ReadOnly)
}
