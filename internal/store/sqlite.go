package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed sqlite_migrations/*.sql
var sqliteMigrations embed.FS

// OpenSQLite opens a SQLite database with recommended settings.
func OpenSQLite(path string) (*sql.DB, error) {
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite handles one writer at a time
	return db, nil
}

// RunSQLiteMigrations applies all pending SQLite migrations using a simple
// schema_version tracking table.
func RunSQLiteMigrations(db *sql.DB) error {
	// Create version tracking table
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	// Read migration files
	entries, err := fs.ReadDir(sqliteMigrations, "sqlite_migrations")
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

		content, err := fs.ReadFile(sqliteMigrations, "sqlite_migrations/"+name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", name, err)
		}

		if err := applyMigration(db, name, string(content), version); err != nil {
			return err
		}
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
