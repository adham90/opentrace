// Package testutil provides reusable test helpers for OpenTrace packages.
// It centralises common setup patterns (in-memory SQLite, test stores) so
// that every test package does not need its own copy.
package testutil

import (
	"database/sql"
	"testing"

	"github.com/uptrace/bun"

	dbstore "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/pkg/store"
)

// SetupTestBunDB opens an in-memory SQLite database (bun) with all migrations applied.
// The database is automatically closed when the test completes.
func SetupTestBunDB(t *testing.T) *bun.DB {
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

// SetupTestDB opens an in-memory SQLite database with all migrations applied.
// Returns the underlying *sql.DB for backward compatibility.
// The database is automatically closed when the test completes.
func SetupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	return SetupTestBunDB(t).DB
}

// SetupTestStores creates a full Stores instance backed by an in-memory SQLite database.
// Useful when tests need multiple stores wired together.
func SetupTestStores(t *testing.T) (*sql.DB, store.Stores) {
	t.Helper()
	bunDB := SetupTestBunDB(t)
	return bunDB.DB, dbstore.NewStores(bunDB)
}
