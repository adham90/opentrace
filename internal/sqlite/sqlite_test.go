package sqlite

import (
	"testing"
)

func TestOpenSQLite_Pragmas(t *testing.T) {
	db := setupTestDB(t)

	// cache_size and synchronous apply to in-memory databases too
	tests := []struct {
		pragma string
		want   string
	}{
		{"PRAGMA cache_size", "-64000"},
		{"PRAGMA synchronous", "1"}, // NORMAL = 1
		{"PRAGMA temp_store", "2"},  // MEMORY = 2
	}

	for _, tt := range tests {
		var got string
		if err := db.QueryRow(tt.pragma).Scan(&got); err != nil {
			t.Fatalf("%s: %v", tt.pragma, err)
		}
		if got != tt.want {
			t.Errorf("%s = %q, want %q", tt.pragma, got, tt.want)
		}
	}
}

func TestOpenSQLite_PragmasApplied(t *testing.T) {
	// Verify the applySQLitePragmas function itself works
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	var cacheSize int
	if err := db.QueryRow("PRAGMA cache_size").Scan(&cacheSize); err != nil {
		t.Fatalf("PRAGMA cache_size: %v", err)
	}
	if cacheSize != -64000 {
		t.Errorf("cache_size = %d, want -64000", cacheSize)
	}

	var sync int
	if err := db.QueryRow("PRAGMA synchronous").Scan(&sync); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	if sync != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}

	var tempStore int
	if err := db.QueryRow("PRAGMA temp_store").Scan(&tempStore); err != nil {
		t.Fatalf("PRAGMA temp_store: %v", err)
	}
	if tempStore != 2 {
		t.Errorf("temp_store = %d, want 2 (MEMORY)", tempStore)
	}
}

func TestPerformanceIndexesExist(t *testing.T) {
	db := setupTestDB(t)

	indexes := []string{
		"idx_metrics_server_name_ts",
		"idx_logs_ts_service_env",
		"idx_logs_ts_level",
		"idx_logs_session_service_ts",
	}

	for _, idx := range indexes {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx,
		).Scan(&name)
		if err != nil {
			t.Errorf("index %s not found: %v", idx, err)
		}
	}
}

func TestOpenSQLite_MaxOpenConns(t *testing.T) {
	db, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	stats := db.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Errorf("MaxOpenConns = %d, want 1", stats.MaxOpenConnections)
	}
}
