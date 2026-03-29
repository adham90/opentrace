package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/migrations"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite"
)

// SQLite tuning constants — extracted from pragmas and DSN parameters.
const (
	sqliteBusyTimeoutMs = 5000      // milliseconds to wait for a locked table
	sqliteCacheSizeKB   = -64000    // negative = KB; 64 MB page cache (default ~2 MB)
	sqliteMmapSize      = 30000000  // 30 MB memory-mapped I/O window
)

// OpenSQLite opens a SQLite database with recommended settings.
// Returns a *bun.DB wrapping the underlying *sql.DB. Use db.DB to access the raw *sql.DB.
func OpenSQLite(path string) (*bun.DB, error) {
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=%d&_foreign_keys=on", path, sqliteBusyTimeoutMs)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	sqldb.SetMaxOpenConns(1) // SQLite handles one writer at a time

	db := bun.NewDB(sqldb, sqlitedialect.New())

	// Performance PRAGMAs — safe with WAL mode
	if err := applySQLitePragmas(db.DB); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying pragmas: %w", err)
	}

	return db, nil
}

// applySQLitePragmas sets performance-critical PRAGMAs on the database.
// These are safe to use with WAL journal mode.
func applySQLitePragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",                               // enforce FK constraints (DSN param not supported by modernc driver)
		fmt.Sprintf("PRAGMA cache_size = %d", sqliteCacheSizeKB), // 64MB page cache (default: 2MB)
		"PRAGMA synchronous = NORMAL",                            // safe with WAL, skips fsync per write
		fmt.Sprintf("PRAGMA mmap_size = %d", sqliteMmapSize),    // 30MB memory-mapped I/O
		"PRAGMA temp_store = MEMORY",                             // temp tables in RAM
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("executing %q: %w", p, err)
		}
	}
	return nil
}

// RunSQLiteMigrations applies all pending SQLite migrations using a simple
// schema_version tracking table.
func RunSQLiteMigrations(db *bun.DB) error {
	sqldb := db.DB // underlying *sql.DB for migration execution

	// Create version tracking table
	if _, err := sqldb.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := sqldb.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	// Read migration files
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("reading migrations: %w", err)
	}

	// Filter and sort .up.sql files
	var upFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			upFiles = append(upFiles, e.Name())
		}
	}
	sort.Strings(upFiles)

	// Apply migrations
	for _, name := range upFiles {
		// Extract version number from filename (e.g., "000001_init.up.sql" -> 1)
		var version int
		if _, err := fmt.Sscanf(name, "%06d_", &version); err != nil {
			continue
		}

		if version <= currentVersion {
			continue
		}

		content, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		start := time.Now()
		if err := applyMigration(sqldb, name, string(content), version); err != nil {
			return err
		}
		slog.Info("migration applied", "name", name, "version", version, "elapsed_ms", time.Since(start).Milliseconds())
	}

	return nil
}

// applyMigration runs a single migration in a transaction. If the migration
// contains "-- pragma:foreign_keys_off", foreign key checks are disabled on a
// dedicated connection for the duration (required for table-recreation migrations
// since PRAGMA foreign_keys cannot be changed inside a transaction).
func applyMigration(db *sql.DB, name, content string, version int) error {
	needsFKOff := strings.Contains(content, "-- pragma:foreign_keys_off")

	if needsFKOff {
		conn, err := db.Conn(context.Background())
		if err != nil {
			return fmt.Errorf("getting conn for migration %s: %w", name, err)
		}
		defer conn.Close()

		if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys=OFF"); err != nil {
			return fmt.Errorf("disabling FK for migration %s: %w", name, err)
		}
		defer conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")

		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			return fmt.Errorf("begin tx for migration %s: %w", name, err)
		}

		if _, err := tx.Exec(content); err != nil {
			tx.Rollback()
			return fmt.Errorf("executing migration %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %s: %w", name, err)
		}
		return tx.Commit()
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for migration %s: %w", name, err)
	}

	if _, err := tx.Exec(content); err != nil {
		tx.Rollback()
		return fmt.Errorf("executing migration %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		tx.Rollback()
		return fmt.Errorf("recording migration %s: %w", name, err)
	}
	return tx.Commit()
}
