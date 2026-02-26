package backup

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

// Restore replaces the database at dbPath with the backup at srcPath.
// It verifies the backup before overwriting.
func Restore(ctx context.Context, srcPath string, dbPath string) error {
	// Check source exists
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("backup file not found at %s: %w", srcPath, err)
	}

	slog.Info("starting restore", "source", srcPath, "destination", dbPath)
	start := time.Now()

	// Verify the backup first
	if err := verifyBackup(ctx, srcPath); err != nil {
		return fmt.Errorf("backup file is invalid: %w", err)
	}

	// Create a safety backup of current database if it exists
	if _, err := os.Stat(dbPath); err == nil {
		safetyPath := dbPath + ".pre-restore." + time.Now().Format("20060102-150405")
		slog.Info("creating safety backup of current database", "path", safetyPath)
		// Use VACUUM INTO for the safety backup (clean, standalone copy)
		safetyDB, oerr := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
		if oerr == nil {
			_, execErr := safetyDB.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, safetyPath))
			safetyDB.Close()
			if execErr != nil {
				slog.Warn("could not create safety backup via VACUUM INTO, falling back to file copy",
					"error", execErr)
				if cpErr := copyFile(dbPath, safetyPath); cpErr != nil {
					slog.Warn("safety backup file copy also failed", "error", cpErr)
				}
			}
		} else {
			// Fallback: plain file copy
			if cpErr := copyFile(dbPath, safetyPath); cpErr != nil {
				slog.Warn("safety backup file copy failed", "error", cpErr)
			}
		}
	}

	// Remove WAL and SHM files for the destination
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")

	// Copy backup to destination
	if err := copyFile(srcPath, dbPath); err != nil {
		return fmt.Errorf("copying backup: %w", err)
	}

	elapsed := time.Since(start)
	slog.Info("restore completed", "elapsed_ms", elapsed.Milliseconds())
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
