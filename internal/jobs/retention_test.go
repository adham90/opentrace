package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupRetentionTestDB creates an in-memory SQLite database with the minimal
// schema needed for retention cleanup tests.
// NOTE: logs/request_summaries/captures are no longer in SQLite — only metric_buckets.
func setupRetentionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema := `
		CREATE TABLE app_config (
			key   TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT '{}'
		);

		CREATE TABLE metric_buckets (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			bucket_start     TEXT NOT NULL,
			bucket_interval  TEXT NOT NULL,
			service          TEXT NOT NULL DEFAULT '',
			endpoint         TEXT NOT NULL DEFAULT '',
			environment      TEXT NOT NULL DEFAULT '',
			request_count    INTEGER NOT NULL DEFAULT 0,
			created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return db
}

func TestRunRetentionCleanup_MetricBuckets(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	old := time.Now().Add(-200 * 24 * time.Hour).Format(time.RFC3339)
	recent := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	// Insert old and recent metric buckets
	_, err := db.Exec("INSERT INTO metric_buckets (bucket_start, bucket_interval, created_at) VALUES (?, '1h', ?)", old, old)
	if err != nil {
		t.Fatalf("insert old bucket: %v", err)
	}
	_, err = db.Exec("INSERT INTO metric_buckets (bucket_start, bucket_interval, created_at) VALUES (?, '1h', ?)", recent, recent)
	if err != nil {
		t.Fatalf("insert recent bucket: %v", err)
	}

	// Default config: metric_buckets = 180d
	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	// Old bucket (200d) should be pruned, recent (1h) should remain
	var count int
	db.QueryRow("SELECT COUNT(*) FROM metric_buckets").Scan(&count)
	if count != 1 {
		t.Errorf("metric_buckets: want 1 remaining, got %d", count)
	}
}

func TestRunRetentionCleanup_NeverConfig(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	// Set metric_buckets retention to "never"
	db.Exec(`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"metric_buckets":"never"}')`)

	old := time.Now().Add(-999 * 24 * time.Hour).Format(time.RFC3339)
	db.Exec("INSERT INTO metric_buckets (bucket_start, bucket_interval, created_at) VALUES (?, '1h', ?)", old, old)

	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	// Nothing should be deleted with "never" retention
	var count int
	db.QueryRow("SELECT COUNT(*) FROM metric_buckets").Scan(&count)
	if count != 1 {
		t.Errorf("metric_buckets: want 1 (never deleted), got %d", count)
	}
}

func TestRunRetentionCleanup_EmptyDB(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	// Should not error on empty tables
	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup on empty DB: %v", err)
	}
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"never", 0},
		{"", 0},
		{"1h", time.Hour},
	}
	for _, tt := range tests {
		got := parseTTL(tt.input)
		if got != tt.want {
			t.Errorf("parseTTL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
