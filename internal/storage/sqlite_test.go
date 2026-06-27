package storage

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "x.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var name string
	err = db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='snapshot'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	if name != "snapshot" {
		t.Fatalf("expected table snapshot, got %q", name)
	}
}
