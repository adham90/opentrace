package sqlite

import (
	"testing"
)

func TestSQLiteMigrations(t *testing.T) {
	db := setupTestDB(t)

	// Verify key tables exist (logs table removed — now in segmented store)
	tables := []string{"data_sources", "app_config", "users", "error_groups", "watches", "metric_buckets"}
	for _, table := range tables {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	// Verify logs table does NOT exist (moved to segmented store)
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='logs'`).Scan(&count)
	if count > 0 {
		t.Error("logs table should not exist — log storage is in segmented store")
	}

	// Verify schema_version was recorded
	var version int
	err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
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
