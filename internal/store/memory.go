package store

import (
	"database/sql"
	"fmt"
	"time"
)

// SetMemory upserts a repo/issue-scoped key/value memory entry (task 2.1.5 — D3
// cross-task memory sharing). The (repo, issue_id, key) tuple is unique, so
// repeated writes replace the prior value while preserving created_at via the
// ON CONFLICT upsert.
func (db *DB) SetMemory(repo string, issueID int, key, value string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(`
		INSERT INTO memories (repo, issue_id, key, value, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(repo, issue_id, key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`, repo, issueID, key, value, now, now)
	if err != nil {
		return fmt.Errorf("set memory: %w", err)
	}
	return nil
}

// GetMemory returns the value for a single repo/issue/key, or "" if absent.
func (db *DB) GetMemory(repo string, issueID int, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM memories WHERE repo=? AND issue_id=? AND key=?`,
		repo, issueID, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get memory: %w", err)
	}
	return v, nil
}

// GetAllMemory returns every key/value pair scoped to a repo+issue, or an empty
// map when none exist. Used to carry prior task conclusions into a subsequent
// hub request (task 2.1.5).
func (db *DB) GetAllMemory(repo string, issueID int) (map[string]string, error) {
	rows, err := db.Query(`SELECT key, value FROM memories WHERE repo=? AND issue_id=?`, repo, issueID)
	if err != nil {
		return nil, fmt.Errorf("query memories: %w", err)
	}
	defer rows.Close()

	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan memory: %w", err)
		}
		m[k] = v
	}
	// Iteration itself can fail (driver/IO error) without any Scan failing.
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memories: %w", err)
	}
	return m, nil
}
