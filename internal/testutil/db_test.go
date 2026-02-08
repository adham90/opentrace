package testutil

import (
	"context"
	"testing"
)

func TestSetupTestDB_SmokeTest(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Verify pgvector extension exists
	var extExists bool
	err := pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&extExists)
	if err != nil {
		t.Fatalf("failed to check pgvector extension: %v", err)
	}
	if !extExists {
		t.Fatal("pgvector extension not installed")
	}

	// Verify all 6 tables exist
	tables := []string{"data_sources", "investigations", "traces", "logs", "code_embeddings", "app_config"}
	for _, table := range tables {
		var exists bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table).Scan(&exists)
		if err != nil {
			t.Fatalf("failed to check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("table %s does not exist", table)
		}
	}

	// Verify all 4 enums exist
	enums := []string{"connector_type", "connector_status", "investigation_status", "trace_step"}
	for _, enum := range enums {
		var exists bool
		err := pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM pg_type WHERE typname = $1)", enum).Scan(&exists)
		if err != nil {
			t.Fatalf("failed to check enum %s: %v", enum, err)
		}
		if !exists {
			t.Fatalf("enum %s does not exist", enum)
		}
	}
}
