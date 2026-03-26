package sqlite

import (
	"database/sql"
	"testing"
)

// setupTestDB opens an in-memory SQLite database with migrations applied.
func setupTestDB(t *testing.T) *sql.DB {
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
