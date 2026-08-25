package gitea

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFindOpenPRByHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("Expected state=open, got %s", r.URL.Query().Get("state"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"number":   3,
				"title":    "Other PR",
				"state":    "open",
				"html_url": "http://localhost/owner/repo/pulls/3",
				"head":     map[string]string{"ref": "feature/other"},
			},
			{
				"number":   5,
				"title":    "Target PR",
				"state":    "open",
				"html_url": "http://localhost/owner/repo/pulls/5",
				"head":     map[string]string{"ref": "matea/solve-issue-2"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	pr, err := client.FindOpenPRByHead("owner", "repo", "matea/solve-issue-2")
	if err != nil {
		t.Fatalf("FindOpenPRByHead failed: %v", err)
	}
	if pr == nil {
		t.Fatal("Expected PR, got nil")
	}
	if pr.Number != 5 {
		t.Errorf("Expected PR number=5, got %d", pr.Number)
	}
}

func TestFindOpenPRByHeadNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"number": 1,
				"state":  "open",
				"head":   map[string]string{"ref": "other-branch"},
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	pr, err := client.FindOpenPRByHead("owner", "repo", "matea/solve-issue-2")
	if err != nil {
		t.Fatalf("FindOpenPRByHead failed: %v", err)
	}
	if pr != nil {
		t.Errorf("Expected nil PR, got #%d", pr.Number)
	}
}

func TestPRDiff(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls/1.diff" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`diff --git a/file.go b/file.go
index 1234567..abcdefg 100644
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {
`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	diff, err := client.PRDiff("owner", "repo", 1)
	if err != nil {
		t.Fatalf("PRDiff failed: %v", err)
	}

	if diff == "" {
		t.Error("Expected non-empty diff")
	}
	if !contains(diff, "import \"fmt\"") {
		t.Error("Expected diff to contain 'import \"fmt\"'")
	}
}

func TestPRFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/pulls/1/files" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]PRFile{
			{Filename: "file.go", Status: "modified", Additions: 1, Deletions: 0, Changes: 1},
			{Filename: "main.go", Status: "added", Additions: 10, Deletions: 0, Changes: 10},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	files, err := client.PRFiles("owner", "repo", 1)
	if err != nil {
		t.Fatalf("PRFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("Expected 2 files, got %d", len(files))
	}
	if files[0].Filename != "file.go" {
		t.Errorf("Expected filename=file.go, got %s", files[0].Filename)
	}
	if files[1].Additions != 10 {
		t.Errorf("Expected additions=10, got %d", files[1].Additions)
	}
}

func TestIssueComments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/repos/owner/repo/issues/1/comments" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]IssueComment{
			{ID: 1, Body: "First comment", User: User{ID: 1, Login: "user1"}, Created: "2024-01-01T00:00:00Z"},
			{ID: 2, Body: "Second comment", User: User{ID: 2, Login: "user2"}, Created: "2024-01-02T00:00:00Z"},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-token")
	comments, err := client.IssueComments("owner", "repo", 1)
	if err != nil {
		t.Fatalf("IssueComments failed: %v", err)
	}

	if len(comments) != 2 {
		t.Errorf("Expected 2 comments, got %d", len(comments))
	}
	if comments[0].Body != "First comment" {
		t.Errorf("Expected body='First comment', got %s", comments[0].Body)
	}
	if comments[1].User.Login != "user2" {
		t.Errorf("Expected user=user2, got %s", comments[1].User.Login)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
