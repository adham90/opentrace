package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// TestDataSourcesAcceptEveryConnectorType guards the data_sources.type CHECK
// constraint against the ConnectorType constants: the constraint previously
// listed only logs/database/monitoring, so creating a mysql, redis, turso or
// server_metrics connector failed with "CHECK constraint failed".
func TestDataSourcesAcceptEveryConnectorType(t *testing.T) {
	db := setupTestDB(t)
	s := NewDataSourceStore(db)
	ctx := context.Background()

	types := []store.ConnectorType{
		store.ConnectorLogs,
		store.ConnectorDatabase,
		store.ConnectorMySQL,
		store.ConnectorRedis,
		store.ConnectorTurso,
		store.ConnectorMonitoring,
		store.ConnectorServerMetrics,
	}

	for _, ct := range types {
		ds, err := s.Create(ctx, store.CreateDataSourceParams{
			Type:        ct,
			Name:        string(ct) + "-conn",
			Config:      map[string]any{"dsn": "x"},
			Environment: "production",
		})
		if err != nil {
			t.Fatalf("Create(type=%s): %v", ct, err)
		}
		got, err := s.GetByID(ctx, ds.ID)
		if err != nil {
			t.Fatalf("GetByID(type=%s): %v", ct, err)
		}
		if got.Type != ct {
			t.Errorf("Type = %q, want %q", got.Type, ct)
		}
	}
}

// TestDataSourcesRejectUnknownConnectorType keeps the CHECK meaningful: a type
// that is not a ConnectorType constant must still be rejected.
func TestDataSourcesRejectUnknownConnectorType(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	_, err := db.NewRaw(
		`INSERT INTO data_sources (id, type, name, config, status, environment, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"bogus-id", "not_a_connector", "n", "{}", "disconnected", "production", "now", "now",
	).Exec(ctx)
	if err == nil {
		t.Fatal("expected CHECK constraint failure for unknown connector type, got nil")
	}
	if !strings.Contains(err.Error(), "CHECK constraint failed") {
		t.Fatalf("expected CHECK constraint failure, got: %v", err)
	}
}

// TestMCPActivityCreatedAtIsRFC3339 pins the mcp_activity.created_at default to
// RFC3339. With datetime('now') ("YYYY-MM-DD HH:MM:SS") the store's own
// "created_at > <RFC3339 cutoff>" comparisons excluded every same-day row and
// time.Parse(time.RFC3339, ...) always failed.
func TestMCPActivityCreatedAtIsRFC3339(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	if _, err := db.NewRaw(
		`INSERT INTO mcp_activity (session_id, tool_name) VALUES (?, ?)`,
		"sess-1", "logs",
	).Exec(ctx); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var createdAt string
	if err := db.NewRaw(`SELECT created_at FROM mcp_activity WHERE session_id = ?`, "sess-1").
		Scan(ctx, &createdAt); err != nil {
		t.Fatalf("select: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
		t.Fatalf("created_at %q is not RFC3339: %v", createdAt, err)
	}

	// The lexicographic window comparison the store performs must now match.
	cutoff := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	var n int
	if err := db.NewRaw(`SELECT COUNT(*) FROM mcp_activity WHERE created_at > ?`, cutoff).
		Scan(ctx, &n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("rows newer than one-hour cutoff = %d, want 1 (created_at=%q cutoff=%q)", n, createdAt, cutoff)
	}
}

// TestListEventsFiltersByEnvironment covers the cross-env leak: lifecycle
// events must not bleed between environments for an env-scoped caller.
func TestListEventsFiltersByEnvironment(t *testing.T) {
	db := setupTestDB(t)
	s := NewErrorGroupStore(db)
	ctx := context.Background()

	for _, env := range []string{"production", "staging"} {
		if err := s.Upsert(ctx, store.LogEntry{
			Level:            "ERROR",
			Service:          "myapp",
			Environment:      env,
			ExceptionClass:   "Boom",
			Message:          "boom",
			ErrorFingerprint: "fp_env",
			Timestamp:        time.Now(),
		}); err != nil {
			t.Fatalf("Upsert(%s): %v", env, err)
		}
	}

	if err := s.Resolve(ctx, "fp_env", "staging", "fixed in staging deploy"); err != nil {
		t.Fatalf("Resolve staging: %v", err)
	}
	if err := s.Ignore(ctx, "fp_env", "production", "noisy"); err != nil {
		t.Fatalf("Ignore production: %v", err)
	}

	prod, err := s.ListEvents(ctx, "fp_env", "production", 10)
	if err != nil {
		t.Fatalf("ListEvents(production): %v", err)
	}
	if len(prod) != 1 {
		t.Fatalf("production events = %d, want 1: %+v", len(prod), prod)
	}
	if prod[0].Environment != "production" || prod[0].Action != "ignored" {
		t.Errorf("got %+v, want the production 'ignored' event", prod[0])
	}

	all, err := s.ListEvents(ctx, "fp_env", "", 10)
	if err != nil {
		t.Fatalf("ListEvents(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("unscoped events = %d, want 2", len(all))
	}
}
