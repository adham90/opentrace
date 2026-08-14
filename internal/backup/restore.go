package backup

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// walSuffix and shmSuffix name the sidecar files SQLite keeps alongside a
// database in WAL mode. A database is only a consistent set together with them:
// committed transactions live in the -wal file until a checkpoint moves them
// into the main file.
const (
	walSuffix = "-wal"
	shmSuffix = "-shm"
)

// dbFileMode is the permission used for a restored database when the original
// file's mode cannot be determined. SQLite databases hold captured payloads, so
// they default to owner-only.
const dbFileMode os.FileMode = 0o600

// restoreTempSuffix names the staging file used to make the swap atomic.
const restoreTempSuffix = ".restore-tmp"

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

	// Preserve the live database's permissions across the swap; os.Create would
	// otherwise widen a 0600 database to 0666&^umask.
	mode := dbFileMode
	dbExists := false
	if info, err := os.Stat(dbPath); err == nil {
		dbExists = true
		mode = info.Mode().Perm()
	}

	// Create a safety backup of the current database.
	// Abort restore if the safety backup cannot be created.
	if dbExists {
		if err := createSafetyBackup(ctx, dbPath); err != nil {
			return err
		}
	}

	// Drop the destination's WAL/SHM before swapping in the backup: the backup is
	// a standalone file, and a stale WAL belonging to the previous database would
	// otherwise be replayed over it. The safety backup above already captured
	// them, so a crash in this window is still recoverable.
	os.Remove(dbPath + walSuffix)
	os.Remove(dbPath + shmSuffix)

	// Stage the copy next to the destination and rename it into place, so a
	// crash or ENOSPC mid-copy leaves the previous file intact instead of a
	// truncated, unopenable database.
	if err := copyFileAtomic(srcPath, dbPath, mode); err != nil {
		return fmt.Errorf("copying backup: %w", err)
	}

	elapsed := time.Since(start)
	slog.Info("restore completed", "elapsed_ms", elapsed.Milliseconds())
	return nil
}

// createSafetyBackup snapshots the current database before it is overwritten.
// VACUUM INTO is preferred because it reads through the WAL and emits a single
// standalone file. When it fails — which is exactly when the snapshot matters
// most, e.g. a damaged or locked source — the fallback copies the whole file
// set (main, -wal, -shm), because copying the main file alone would discard
// every transaction still resident in the WAL.
func createSafetyBackup(ctx context.Context, dbPath string) error {
	safetyPath := dbPath + ".pre-restore." + time.Now().Format("20060102-150405")
	slog.Info("creating safety backup of current database", "path", safetyPath)

	safetyDB, err := sql.Open("sqlite", dbPath+"?mode=ro&_journal_mode=WAL")
	if err == nil {
		escaped := strings.ReplaceAll(safetyPath, "'", "''")
		_, execErr := safetyDB.ExecContext(ctx, fmt.Sprintf(`VACUUM INTO '%s'`, escaped))
		safetyDB.Close()
		if execErr == nil {
			slog.Info("safety backup created", "path", safetyPath, "method", "vacuum_into")
			return nil
		}
		slog.Warn("VACUUM INTO safety backup failed, copying database file set", "error", execErr)
	} else {
		slog.Warn("opening database for safety backup failed, copying database file set", "error", err)
	}

	if cpErr := copyDatabaseSet(dbPath, safetyPath); cpErr != nil {
		return fmt.Errorf("restore aborted: cannot create safety backup: %w", cpErr)
	}
	slog.Info("safety backup created", "path", safetyPath, "method", "file_copy")
	return nil
}

// copyDatabaseSet copies a SQLite database together with its -wal and -shm
// sidecars, so the copy still contains transactions that were committed to the
// WAL but not yet checkpointed. Missing sidecars are not an error.
func copyDatabaseSet(src, dst string) error {
	if err := copyFile(src, dst, dbFileMode); err != nil {
		return err
	}
	for _, suffix := range []string{walSuffix, shmSuffix} {
		if _, err := os.Stat(src + suffix); err != nil {
			continue // no sidecar to copy
		}
		if err := copyFile(src+suffix, dst+suffix, dbFileMode); err != nil {
			return fmt.Errorf("copying %s: %w", suffix, err)
		}
	}
	return nil
}

// copyFileAtomic copies src to a temporary file in dst's directory, fsyncs it,
// then renames it over dst. The rename is atomic within a filesystem, so dst is
// never observed half-written.
func copyFileAtomic(src, dst string, mode os.FileMode) error {
	tmp := dst + restoreTempSuffix
	if err := copyFile(src, tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return syncDir(filepath.Dir(dst))
}

// copyFile copies src to dst, creating dst with the given mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	// Re-apply the mode explicitly: O_CREATE honours the umask, and an existing
	// file keeps its old permissions entirely.
	if err := out.Chmod(mode); err != nil {
		return err
	}
	return out.Sync()
}

// syncDir fsyncs a directory so a rename survives a power loss.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some filesystems reject fsync on a directory; the rename itself is
		// still atomic, so this is not fatal.
		slog.Debug("syncing directory after restore failed", "dir", dir, "error", err)
	}
	return nil
}
