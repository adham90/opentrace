package db

import (
	"testing"

	"github.com/uptrace/bun"
)

// setupTestDB opens an in-memory SQLite database with migrations applied.
func setupTestDB(t *testing.T) *bun.DB {
	t.Helper()

	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}

	if err := RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}
