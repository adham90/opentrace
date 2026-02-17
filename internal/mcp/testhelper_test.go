package mcp

import (
	"database/sql"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

// setupTestDB opens an in-memory SQLite database with migrations applied.
// Used for real integration tests (no mocks).
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}

	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}
