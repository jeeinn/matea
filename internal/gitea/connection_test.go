package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- scope error parsing ---

func TestParseScopeError(t *testing.T) {
	msg := `API error 403: {"message":"token does not have at least one of required scope(s), required=[read:user], token scope=write:admin,write:repository","url":"http://gitea/api/swagger"}`
	required, granted, ok := ParseScopeError(msg)
	require.True(t, ok)
	assert.Equal(t, []string{"read:user"}, required)
	assert.Equal(t, []string{"write:admin", "write:repository"}, granted)

	_, _, ok = ParseScopeError("API error 401: unauthorized")
	assert.False(t, ok)

	_, _, ok = ParseScopeError("API error 403: user is not a site admin")
	assert.False(t, ok)
}

func TestScopeCovers(t *testing.T) {
	granted := []string{"write:admin", "write:repository"}
	assert.True(t, scopeCovers(granted, "write:admin"))
	assert.True(t, scopeCovers(granted, "read:repository"), "write implies read in the same category")
	assert.False(t, scopeCovers(granted, "read:user"), "categories are independent")
	assert.False(t, scopeCovers(granted, "write:issue"))
	assert.True(t, scopeCovers([]string{"all"}, "write:issue"), "all covers everything")
	assert.False(t, scopeCovers(nil, "read:user"))
}

// --- fake Gitea for connection probes ---

// fakeConnGitea serves the endpoints TestConnection probes. A non-empty
// *ScopeRequired field makes that endpoint answer with Gitea's 403
// scope-denied body, with `granted` echoed as the token's scope list.
type fakeConnGitea struct {
	login   string
	isAdmin bool
	repos   []string
	granted string

	userScopeRequired  string
	repoScopeRequired  string
	issueScopeRequired string
	adminScopeRequired string
}

func (f *fakeConnGitea) scopeDenied(w http.ResponseWriter, required string) {
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprintf(w, `{"message":"token does not have at least one of required scope(s), required=[%s], token scope=%s","url":"http://gitea.local/api/swagger"}`,
		required, f.granted)
}

func (f *fakeConnGitea) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/user", func(w http.ResponseWriter, r *http.Request) {
		if f.userScopeRequired != "" {
			f.scopeDenied(w, f.userScopeRequired)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"login": f.login, "is_admin": f.isAdmin})
	})
	mux.HandleFunc("/api/v1/repos/search", func(w http.ResponseWriter, r *http.Request) {
		if f.repoScopeRequired != "" {
			f.scopeDenied(w, f.repoScopeRequired)
			return
		}
		var data []map[string]string
		for _, full := range f.repos {
			data = append(data, map[string]string{"full_name": full})
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	})
	mux.HandleFunc("/api/v1/repos/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/issues") {
			if f.issueScopeRequired != "" {
				f.scopeDenied(w, f.issueScopeRequired)
				return
			}
			json.NewEncoder(w).Encode([]interface{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/v1/admin/hooks", func(w http.ResponseWriter, r *http.Request) {
		if f.adminScopeRequired != "" {
			f.scopeDenied(w, f.adminScopeRequired)
			return
		}
		json.NewEncoder(w).Encode([]interface{}{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func findCheck(t *testing.T, checks []PermissionCheck, key string) PermissionCheck {
	t.Helper()
	for _, c := range checks {
		if c.Key == key {
			return c
		}
	}
	t.Fatalf("check %q not found in %+v", key, checks)
	return PermissionCheck{}
}

// --- TestConnection ---

func TestConnectionAllPass(t *testing.T) {
	srv := (&fakeConnGitea{login: "root", isAdmin: true, repos: []string{"o/r"}}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.True(t, res.OK, res.Message)
	assert.Equal(t, "root", res.Username)
	assert.True(t, res.IsAdmin)
	assert.Equal(t, 1, res.RepoCount)
	assert.Contains(t, res.Message, "连接成功")
	assert.NotEmpty(t, res.RequiredScopes)

	for _, key := range []string{"identity", "repo", "issue", "admin"} {
		chk := findCheck(t, res.Checks, key)
		assert.True(t, chk.OK, "check %s: %+v", key, chk)
	}
}

func TestConnectionMissingReadUser(t *testing.T) {
	// The exact failure from the field: token granted write:admin +
	// write:repository only, and GET /user requires read:user.
	srv := (&fakeConnGitea{
		userScopeRequired: "read:user",
		granted:           "write:admin,write:repository",
	}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.False(t, res.OK)
	assert.Contains(t, res.Message, "read:user")
	assert.Contains(t, res.Message, "write:admin, write:repository", "guidance should echo the granted scopes")

	chk := findCheck(t, res.Checks, "identity")
	assert.False(t, chk.OK)
	assert.True(t, chk.Required)
	assert.Contains(t, chk.Detail, "read:user")
}

func TestConnectionNonAdminDegradesWithWarning(t *testing.T) {
	srv := (&fakeConnGitea{login: "bot", isAdmin: false, repos: []string{"o/r"}}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.True(t, res.OK, res.Message)
	assert.Contains(t, res.Message, "警告")

	chk := findCheck(t, res.Checks, "admin")
	assert.False(t, chk.OK)
	assert.False(t, chk.Required, "admin capability is optional")
	assert.Contains(t, chk.Detail, "非站点管理员")
}

func TestConnectionAdminScopeMissing(t *testing.T) {
	srv := (&fakeConnGitea{
		login:              "root",
		isAdmin:            true,
		repos:              []string{"o/r"},
		granted:            "read:user,write:repository,write:issue",
		adminScopeRequired: "read:admin",
	}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.True(t, res.OK, "missing admin scope is a warning, not a failure: %s", res.Message)

	chk := findCheck(t, res.Checks, "admin")
	assert.False(t, chk.OK)
	assert.False(t, chk.Required)
	assert.Contains(t, chk.Detail, "write:admin")
}

func TestConnectionIssueScopeMissing(t *testing.T) {
	srv := (&fakeConnGitea{
		login:              "root",
		isAdmin:            true,
		repos:              []string{"o/r"},
		granted:            "read:user,write:repository,write:admin",
		issueScopeRequired: "read:issue",
	}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.False(t, res.OK, "comments are core functionality — issue scope is required")

	chk := findCheck(t, res.Checks, "issue")
	assert.False(t, chk.OK)
	assert.True(t, chk.Required)
	assert.Contains(t, chk.Detail, "write:issue", "guidance should recommend the write grant runtime needs")
}

func TestConnectionRepoScopeMissing(t *testing.T) {
	srv := (&fakeConnGitea{
		login:             "root",
		isAdmin:           true,
		granted:           "read:user,write:admin",
		repoScopeRequired: "read:repository",
	}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.False(t, res.OK)

	chk := findCheck(t, res.Checks, "repo")
	assert.False(t, chk.OK)
	assert.Contains(t, chk.Detail, "write:repository")

	// Repo list failed → no issue probe at all.
	for _, c := range res.Checks {
		assert.NotEqual(t, "issue", c.Key)
	}
}

func TestConnectionNoReposSkipsIssueProbe(t *testing.T) {
	srv := (&fakeConnGitea{login: "root", isAdmin: true, repos: nil}).start(t)

	res, err := NewClient(srv.URL, "tok").TestConnection()
	require.NoError(t, err)
	require.True(t, res.OK, res.Message)

	chk := findCheck(t, res.Checks, "issue")
	assert.True(t, chk.Skipped)
	assert.Contains(t, chk.Detail, "write:issue")
}

func TestConnectionEmptyArgs(t *testing.T) {
	res, err := NewClient("", "tok").TestConnection()
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "地址")

	res, err = NewClient("http://x", "").TestConnection()
	require.NoError(t, err)
	assert.False(t, res.OK)
	assert.Contains(t, res.Message, "Token")
}
