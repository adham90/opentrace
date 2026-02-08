package store

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func migrationsDir() string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..", "migrations")
}

func setupRawDB(t *testing.T) (string, *pgxpool.Pool, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Use testutil indirectly to avoid import cycle — just set up a raw container
	// We import testutil from the test file only
	connStr, cleanup := setupTestContainer(t)

	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		cleanup()
		t.Fatalf("failed to create pool: %v", err)
	}

	wrappedCleanup := func() {
		pool.Close()
		cleanup()
	}

	return connStr, pool, wrappedCleanup
}

func TestMigrationsApply(t *testing.T) {
	connStr, pool, cleanup := setupRawDB(t)
	defer cleanup()

	if err := RunMigrations(connStr, migrationsDir()); err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	ctx := context.Background()

	// Verify pgvector extension
	var extExists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&extExists)
	if err != nil {
		t.Fatalf("failed to check pgvector: %v", err)
	}
	if !extExists {
		t.Fatal("pgvector extension not installed")
	}

	// Verify all 6 tables
	tables := []string{"data_sources", "investigations", "traces", "logs", "code_embeddings", "app_config"}
	for _, table := range tables {
		var exists bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}

	// Verify all 4 enums
	enums := []string{"connector_type", "connector_status", "investigation_status", "trace_step"}
	for _, enum := range enums {
		var exists bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_type WHERE typname = $1)", enum).Scan(&exists)
		if err != nil {
			t.Fatalf("check enum %s: %v", enum, err)
		}
		if !exists {
			t.Fatalf("enum %s does not exist", enum)
		}
	}
}

func TestMigrationsUpDown(t *testing.T) {
	connStr, pool, cleanup := setupRawDB(t)
	defer cleanup()

	// Apply migrations
	if err := RunMigrations(connStr, migrationsDir()); err != nil {
		t.Fatalf("initial up failed: %v", err)
	}

	ctx := context.Background()

	// Verify tables exist
	var exists bool
	pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'data_sources')").Scan(&exists)
	if !exists {
		t.Fatal("data_sources table not found after up")
	}

	// Run down migration
	dbURL := pgxURL(connStr)
	m, err := newMigrate(migrationsDir(), dbURL)
	if err != nil {
		t.Fatalf("creating migrate for down: %v", err)
	}
	if err := m.Down(); err != nil {
		t.Fatalf("down migration failed: %v", err)
	}
	m.Close()

	// Verify tables are gone
	pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'data_sources')").Scan(&exists)
	if exists {
		t.Fatal("data_sources table still exists after down")
	}

	// Run up again
	if err := RunMigrations(connStr, migrationsDir()); err != nil {
		t.Fatalf("second up failed: %v", err)
	}

	// Verify tables restored
	pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = 'data_sources')").Scan(&exists)
	if !exists {
		t.Fatal("data_sources table not restored after second up")
	}
}
