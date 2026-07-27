package sqlite

import (
	"path/filepath"
	"testing"
)

// TestPragmasActuallyApplied guards the regression where the modernc driver
// silently ignored mattn-style DSN params, leaving the DB in DELETE journal
// mode with busy_timeout=0 (unsafe with synchronous=NORMAL).
func TestPragmasActuallyApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pragma_test.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.DB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want \"wal\"", journalMode)
	}

	var busyTimeout int
	if err := db.DB.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if busyTimeout != sqliteBusyTimeoutMs {
		t.Errorf("busy_timeout = %d, want %d", busyTimeout, sqliteBusyTimeoutMs)
	}

	var fk int
	if err := db.DB.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}
