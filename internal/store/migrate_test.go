package store

import (
	"testing"
)

func TestSQLiteMigrations(t *testing.T) {
	db := setupTestDB(t)

	// Verify all tables exist
	tables := []string{"data_sources", "logs", "app_config", "watchers", "watcher_runs", "alerts"}
	for _, table := range tables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	// Verify FTS5 virtual table
	var ftsName string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='logs_fts'`,
	).Scan(&ftsName)
	if err != nil {
		t.Fatalf("logs_fts virtual table not found: %v", err)
	}

	// Verify schema_version was recorded
	var version int
	err = db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	if version < 1 {
		t.Errorf("schema version = %d, want >= 1", version)
	}
}

func TestSQLiteMigrations_Idempotent(t *testing.T) {
	db := setupTestDB(t)

	// Running migrations again should be a no-op
	if err := RunSQLiteMigrations(db); err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}
}
