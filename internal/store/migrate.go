package store

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// RunMigrations runs all pending database migrations.
func RunMigrations(databaseURL, migrationsPath string) error {
	dbURL := pgxURL(databaseURL)
	m, err := migrate.New("file://"+migrationsPath, dbURL)
	if err != nil {
		return fmt.Errorf("creating migrate instance: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("running migrations: %w", err)
	}
	return nil
}

// pgxURL converts a postgres:// URL to pgx5:// for golang-migrate.
func pgxURL(databaseURL string) string {
	if len(databaseURL) > 11 && databaseURL[:11] == "postgres://" {
		return "pgx5://" + databaseURL[11:]
	}
	if len(databaseURL) > 14 && databaseURL[:14] == "postgresql://" {
		return "pgx5://" + databaseURL[14:]
	}
	return databaseURL
}
