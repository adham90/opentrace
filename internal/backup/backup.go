package backup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Backup creates a safe copy of the SQLite database using VACUUM INTO.
// This creates a clean, standalone copy without WAL files.
func Backup(ctx context.Context, dbPath string, destPath string) error {
	// Check source exists
	if _, err := os.Stat(dbPath); err != nil {
		return fmt.Errorf("database not found at %s: %w", dbPath, err)
	}

	// Check destination doesn't already exist (don't overwrite)
	if _, err := os.Stat(destPath); err == nil {
		return fmt.Errorf("destination already exists: %s (use --force to overwrite)", destPath)
	}

	slog.Info("starting backup", "source", dbPath, "destination", destPath)
	start := time.Now()

	// Open source database read-only
	db, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// VACUUM INTO creates a clean standalone copy (no WAL needed).
	// Escape single quotes in path to prevent SQL injection.
	safePath := strings.ReplaceAll(destPath, "'", "''")
	_, err = db.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, safePath))
	if err != nil {
		// Clean up partial file
		os.Remove(destPath)
		return fmt.Errorf("vacuum into: %w", err)
	}

	// Verify the backup
	if err := verifyBackup(ctx, destPath); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("backup verification failed: %w", err)
	}

	elapsed := time.Since(start)
	logAttrs := []any{"destination", destPath, "elapsed_ms", elapsed.Milliseconds()}
	if info, err := os.Stat(destPath); err == nil {
		logAttrs = append(logAttrs, "size_mb", info.Size()/(1024*1024))
	}
	slog.Info("backup completed", logAttrs...)

	return nil
}

// BackupForce creates a backup, overwriting the destination if it exists.
func BackupForce(ctx context.Context, dbPath string, destPath string) error {
	os.Remove(destPath)
	return Backup(ctx, dbPath, destPath)
}

func verifyBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path+"?mode=ro")
	if err != nil {
		return fmt.Errorf("opening backup: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}

	// Check schema version exists
	var version int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	slog.Info("backup verified", "integrity", "ok", "schema_version", version)
	return nil
}
