package mcp

import (
	"testing"

	"github.com/uptrace/bun"

	dbstore "github.com/adham90/opentrace/internal/db"
)

// setupTestDB opens an in-memory SQLite database with migrations applied.
// Used for real integration tests (no mocks).
func setupTestDB(t *testing.T) *bun.DB {
	t.Helper()

	db, err := dbstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}

	if err := dbstore.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}

	t.Cleanup(func() { db.Close() })
	return db
}
