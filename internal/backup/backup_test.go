package backup

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestRestore_PreservesFileMode proves the swap keeps the live database's
// permissions instead of widening them to 0666&^umask via os.Create.
func TestRestore_PreservesFileMode(t *testing.T) {
	dbPath := createTestDB(t)
	backupPath := filepath.Join(t.TempDir(), "backup.db")

	ctx := context.Background()
	if err := Backup(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("restored db mode = %o, want 600", got)
	}
}

// TestRestore_LeavesNoTempFile proves the staged copy is renamed, not left
// behind.
func TestRestore_LeavesNoTempFile(t *testing.T) {
	dbPath := createTestDB(t)
	backupPath := filepath.Join(t.TempDir(), "backup.db")

	ctx := context.Background()
	if err := Backup(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := os.Stat(dbPath + restoreTempSuffix); !os.IsNotExist(err) {
		t.Errorf("staging file still present: %v", err)
	}
}

// TestCopyDatabaseSet_IncludesWAL proves the safety-backup fallback copies the
// -wal (and -shm) sidecars. Copying only the main file would silently discard
// every transaction committed to the WAL but not yet checkpointed — and the
// restore deletes the WAL immediately afterwards.
func TestCopyDatabaseSet_IncludesWAL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	for _, suffix := range []string{"", walSuffix, shmSuffix} {
		if err := os.WriteFile(src+suffix, []byte("payload"+suffix), 0o600); err != nil {
			t.Fatalf("write %s: %v", suffix, err)
		}
	}

	dst := filepath.Join(dir, "dst.db")
	if err := copyDatabaseSet(src, dst); err != nil {
		t.Fatalf("copyDatabaseSet: %v", err)
	}

	for _, suffix := range []string{"", walSuffix, shmSuffix} {
		got, err := os.ReadFile(dst + suffix)
		if err != nil {
			t.Fatalf("read copy %q: %v", suffix, err)
		}
		if string(got) != "payload"+suffix {
			t.Errorf("copy %q = %q, want %q", suffix, got, "payload"+suffix)
		}
	}
}

// TestCopyDatabaseSet_MissingSidecars proves a database without WAL files copies
// cleanly.
func TestCopyDatabaseSet_MissingSidecars(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	if err := os.WriteFile(src, []byte("main"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	dst := filepath.Join(dir, "dst.db")
	if err := copyDatabaseSet(src, dst); err != nil {
		t.Fatalf("copyDatabaseSet: %v", err)
	}
	if _, err := os.Stat(dst + walSuffix); !os.IsNotExist(err) {
		t.Errorf("unexpected -wal copy: %v", err)
	}
}

// TestRestore_RoundTripWithWALResidentRows proves committed rows that live only
// in the WAL are present in the backup and survive a restore.
func TestRestore_RoundTripWithWALResidentRows(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal.db")

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("create schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE rows_t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 200; i++ {
		if _, err := db.Exec(`INSERT INTO rows_t (v) VALUES (?)`, "row"); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	db.Close()

	ctx := context.Background()
	backupPath := filepath.Join(dir, "backup.db")
	if err := Backup(ctx, dbPath, backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer restored.Close()
	var count int
	if err := restored.QueryRow(`SELECT COUNT(*) FROM rows_t`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 200 {
		t.Errorf("restored row count = %d, want 200", count)
	}
}

// TestRestore_SafetyBackupFallsBackToFileSet forces the VACUUM INTO path to
// fail (its output path already exists, which SQLite rejects) and proves
// Restore still produces a usable safety backup via the whole-file-set copy.
//
// It goes through Restore(), not createSafetyBackup() or copyDatabaseSet()
// directly, so the fallback *wiring* is pinned: dropping the fallback, or
// narrowing it to copyFile — which loses the WAL the restore deletes moments
// later — fails this test.
func TestRestore_SafetyBackupFallsBackToFileSet(t *testing.T) {
	backupPath := filepath.Join(t.TempDir(), "backup.db")
	ctx := context.Background()
	if err := Backup(ctx, createTestDB(t), backupPath); err != nil {
		t.Fatalf("Backup: %v", err)
	}

	dbPath, release := newWALDatabase(t)
	defer release()

	wantDB, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("reading live db: %v", err)
	}
	wantWAL, err := os.ReadFile(dbPath + walSuffix)
	if err != nil {
		t.Fatalf("the live database has no WAL sidecar to preserve: %v", err)
	}

	// createSafetyBackup names its output with a second-resolution timestamp.
	// Occupying every name it could pick makes VACUUM INTO fail, because
	// SQLite refuses to write into an existing file.
	const placeholder = "occupied"
	const candidateWindowSecs = 5
	var candidates []string
	for i := 0; i < candidateWindowSecs; i++ {
		p := dbPath + ".pre-restore." + time.Now().Add(time.Duration(i)*time.Second).Format("20060102-150405")
		if err := os.WriteFile(p, []byte(placeholder), 0o600); err != nil {
			t.Fatalf("occupying %s: %v", p, err)
		}
		candidates = append(candidates, p)
	}

	if err := Restore(ctx, backupPath, dbPath); err != nil {
		t.Fatalf("Restore must survive a failed VACUUM INTO: %v", err)
	}

	var safety string
	for _, p := range candidates {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("reading candidate: %v", err)
		}
		if string(got) != placeholder {
			safety = p
			if !bytes.Equal(got, wantDB) {
				t.Errorf("safety backup does not hold the pre-restore database bytes")
			}
		}
	}
	if safety == "" {
		t.Fatal("VACUUM INTO failed and nothing was written: the file-set fallback is not wired into Restore")
	}

	gotWAL, err := os.ReadFile(safety + walSuffix)
	if err != nil {
		t.Fatalf("the fallback dropped the -wal sidecar, discarding uncheckpointed transactions: %v", err)
	}
	if !bytes.Equal(gotWAL, wantWAL) {
		t.Error("safety backup WAL does not match the live WAL")
	}
}

// newWALDatabase creates a SQLite database in WAL mode with an uncommitted
// transaction held open, so a populated -wal sidecar exists on disk for the
// duration of the test. The returned func releases the connection.
func newWALDatabase(t *testing.T) (string, func()) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "live.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("opening live db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("creating schema_version: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (1)`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (2)`); err != nil {
		t.Fatalf("write in tx: %v", err)
	}
	return dbPath, func() {
		_ = tx.Rollback()
		_ = db.Close()
	}
}
