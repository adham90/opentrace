package testutil

import (
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func newMigrate(databaseURL, migrationsPath string) (*migrate.Migrate, error) {
	// golang-migrate pgx5 driver expects pgx5:// scheme
	dbURL := pgxURL(databaseURL)
	return migrate.New("file://"+migrationsPath, dbURL)
}

// pgxURL converts a postgres:// URL to pgx5:// for golang-migrate
func pgxURL(databaseURL string) string {
	if len(databaseURL) > 11 && databaseURL[:11] == "postgres://" {
		return "pgx5://" + databaseURL[11:]
	}
	if len(databaseURL) > 14 && databaseURL[:14] == "postgresql://" {
		return "pgx5://" + databaseURL[14:]
	}
	return databaseURL
}
