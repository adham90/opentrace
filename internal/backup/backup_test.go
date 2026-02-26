package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// createTestDB creates a temporary SQLite database with a schema_version table
// and some test data, returning the path and a cleanup function.
func createTestDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

	// Create schema_version table (matches the real migrations table)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("creating schema_version: %v", err)
	}

	_, err = db.Exec(`INSERT INTO schema_version (version) VALUES (1), (2), (3)`)
	if err != nil {
		t.Fatalf("inserting schema versions: %v", err)
	}

	// Create a data table with some rows
	_, err = db.Exec(`CREATE TABLE test_data (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value REAL
	)`)
	if err != nil {
		t.Fatalf("creating test_data: %v", err)
	}

	_, err = db.Exec(`INSERT INTO test_data (name, value) VALUES
		('alpha', 1.1),
		('beta', 2.2),
		('gamma', 3.3)`)
	if err != nil {
		t.Fatalf("inserting test data: %v", err)
	}

	return dbPath
}

func TestBackup_CreatesValidCopy(t *testing.T) {
	dbPath := createTestDB(t)
	destPath := filepath.Join(t.TempDir(), "backup.db")

	ctx := context.Background()
	if err := Backup(ctx, dbPath, destPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Verify backup file exists
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}

	// Verify backup has correct data
	db, err := sql.Open("sqlite", destPath+"?mode=ro")
	if err != nil {
		t.Fatalf("opening backup: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM test_data").Scan(&count); err != nil {
		t.Fatalf("querying backup: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}

	var maxVersion int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_version").Scan(&maxVersion); err != nil {
		t.Fatalf("querying schema_version: %v", err)
	}
	if maxVersion != 3 {
		t.Errorf("expected schema version 3, got %d", maxVersion)
	}
}

func TestBackup_FailsIfDestinationExists(t *testing.T) {
	dbPath := createTestDB(t)
	destPath := filepath.Join(t.TempDir(), "backup.db")

	// Create the destination file first
	if err := os.WriteFile(destPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("creating dest file: %v", err)
	}

	ctx := context.Background()
	err := Backup(ctx, dbPath, destPath)
	if err == nil {
		t.Fatal("expected error when destination exists, got nil")
	}
}

func TestBackupForce_OverwritesExisting(t *testing.T) {
	dbPath := createTestDB(t)
	destPath := filepath.Join(t.TempDir(), "backup.db")

	// Create the destination file first
	if err := os.WriteFile(destPath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("creating dest file: %v", err)
	}

	ctx := context.Background()
	if err := BackupForce(ctx, dbPath, destPath); err != nil {
		t.Fatalf("BackupForce: %v", err)
	}

	// Verify the backup replaced the old file
	db, err := sql.Open("sqlite", destPath+"?mode=ro")
	if err != nil {
		t.Fatalf("opening backup: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM test_data").Scan(&count); err != nil {
		t.Fatalf("querying backup: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}
}

func TestBackup_FailsIfSourceMissing(t *testing.T) {
	ctx := context.Background()
	err := Backup(ctx, "/nonexistent/path/db.sqlite", filepath.Join(t.TempDir(), "backup.db"))
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func TestRestore_RestoresFromBackup(t *testing.T) {
	dbPath := createTestDB(t)
	backupPath := filepath.Join(t.TempDir(), "backup.db")

	ctx := context.Background()

	// Create backup
	if err := Backup(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Modify the original database
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	_, err = db.Exec("DELETE FROM test_data")
	if err != nil {
		t.Fatalf("deleting data: %v", err)
	}
	db.Close()

	// Restore from backup
	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Verify the restore brought back the original data
	db, err = sql.Open("sqlite", dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("opening restored db: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM test_data").Scan(&count); err != nil {
		t.Fatalf("querying restored db: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 rows after restore, got %d", count)
	}
}

func TestRestore_FailsOnInvalidBackup(t *testing.T) {
	// Create a file that is not a valid SQLite database
	badPath := filepath.Join(t.TempDir(), "bad.db")
	if err := os.WriteFile(badPath, []byte("not a database"), 0o644); err != nil {
		t.Fatalf("creating bad file: %v", err)
	}

	destPath := filepath.Join(t.TempDir(), "dest.db")
	ctx := context.Background()
	err := Restore(ctx, badPath, destPath)
	if err == nil {
		t.Fatal("expected error for invalid backup, got nil")
	}
}

func TestRestore_FailsIfSourceMissing(t *testing.T) {
	ctx := context.Background()
	err := Restore(ctx, "/nonexistent/backup.db", filepath.Join(t.TempDir(), "dest.db"))
	if err == nil {
		t.Fatal("expected error for missing source, got nil")
	}
}

func TestRestore_CreatesSafetyBackup(t *testing.T) {
	dbPath := createTestDB(t)
	backupPath := filepath.Join(t.TempDir(), "backup.db")

	ctx := context.Background()

	// Create backup
	if err := Backup(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	// Restore (should create a .pre-restore.* safety backup)
	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// Check that a safety backup was created
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if matched, _ := filepath.Match("*.pre-restore.*", e.Name()); matched {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected a .pre-restore.* safety backup file to be created")
	}
}
