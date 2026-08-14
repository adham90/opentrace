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
// NOTE: logs/request_summaries/captures are no longer in SQLite.
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

		CREATE TABLE error_groups (
			fingerprint   TEXT NOT NULL,
			environment   TEXT NOT NULL DEFAULT '',
			service       TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL,
			last_seen_at  TEXT NOT NULL,
			PRIMARY KEY (fingerprint, environment)
		);

		CREATE TABLE deploy_markers (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			service       TEXT NOT NULL,
			environment   TEXT NOT NULL DEFAULT '',
			commit_hash   TEXT NOT NULL,
			first_seen_at TEXT NOT NULL,
			request_count INTEGER NOT NULL DEFAULT 1
		);

		CREATE TABLE watch_runs (
			id         TEXT PRIMARY KEY,
			watch_id   TEXT NOT NULL,
			status     TEXT NOT NULL DEFAULT 'running',
			started_at TEXT NOT NULL
		);

		CREATE TABLE watch_alerts (
			id         TEXT PRIMARY KEY,
			watch_id   TEXT NOT NULL,
			summary    TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return db
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestCleanupRetentionTables_MetricBuckets(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-200 * 24 * time.Hour).Format(time.RFC3339)
	recent := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	for _, ts := range []string{old, recent} {
		if _, err := db.Exec(
			"INSERT INTO metric_buckets (bucket_start, bucket_interval, created_at) VALUES (?, '1h', ?)",
			ts, ts); err != nil {
			t.Fatalf("insert bucket: %v", err)
		}
	}

	// Default config: metric_buckets = 180d
	if _, err := cleanupRetentionTables(ctx, db); err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}

	if got := countRows(t, db, "metric_buckets"); got != 1 {
		t.Errorf("metric_buckets: want 1 remaining, got %d", got)
	}
}

func TestCleanupRetentionTables_NeverConfig(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"metric_buckets":"never"}')`,
	); err != nil {
		t.Fatalf("insert config: %v", err)
	}

	old := time.Now().UTC().Add(-999 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(
		"INSERT INTO metric_buckets (bucket_start, bucket_interval, created_at) VALUES (?, '1h', ?)",
		old, old); err != nil {
		t.Fatalf("insert bucket: %v", err)
	}

	if _, err := cleanupRetentionTables(ctx, db); err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}

	if got := countRows(t, db, "metric_buckets"); got != 1 {
		t.Errorf("metric_buckets: want 1 (never deleted), got %d", got)
	}
}

func TestCleanupRetentionTables_EmptyDB(t *testing.T) {
	db := setupRetentionTestDB(t)
	if _, err := cleanupRetentionTables(context.Background(), db); err != nil {
		t.Fatalf("cleanupRetentionTables on empty DB: %v", err)
	}
}

// TestCleanupRetentionTables_MissingTable proves retention is a no-op (not an
// error) on a database that lacks one of the retention tables.
func TestCleanupRetentionTables_MissingTable(t *testing.T) {
	db := setupRetentionTestDB(t)
	if _, err := db.Exec("DROP TABLE watch_runs"); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"watch_runs":"1d"}')`,
	); err != nil {
		t.Fatalf("insert config: %v", err)
	}
	if _, err := cleanupRetentionTables(context.Background(), db); err != nil {
		t.Fatalf("cleanupRetentionTables with missing table: %v", err)
	}
}

// TestCleanupRetentionTables_ConfiguredTTLsEnforced proves the error_groups,
// deploy_markers, watch_runs and watch_alerts TTLs accepted by the admin tool
// are actually applied — previously they were parsed and silently dropped.
func TestCleanupRetentionTables_ConfiguredTTLsEnforced(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO app_config (key, value) VALUES ('retention_policy',
		'{"error_groups":"30d","deploy_markers":"30d","watch_runs":"7d","watch_alerts":"7d"}')`,
	); err != nil {
		t.Fatalf("insert config: %v", err)
	}

	old := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	recent := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)

	inserts := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO error_groups (fingerprint, first_seen_at, last_seen_at) VALUES ('a', ?, ?)", []any{old, old}},
		{"INSERT INTO error_groups (fingerprint, first_seen_at, last_seen_at) VALUES ('b', ?, ?)", []any{recent, recent}},
		{"INSERT INTO deploy_markers (service, commit_hash, first_seen_at) VALUES ('api', 'c1', ?)", []any{old}},
		{"INSERT INTO deploy_markers (service, commit_hash, first_seen_at) VALUES ('api', 'c2', ?)", []any{recent}},
		{"INSERT INTO watch_runs (id, watch_id, started_at) VALUES ('r1', 'w', ?)", []any{old}},
		{"INSERT INTO watch_runs (id, watch_id, started_at) VALUES ('r2', 'w', ?)", []any{recent}},
		{"INSERT INTO watch_alerts (id, watch_id, created_at) VALUES ('a1', 'w', ?)", []any{old}},
		{"INSERT INTO watch_alerts (id, watch_id, created_at) VALUES ('a2', 'w', ?)", []any{recent}},
	}
	for _, in := range inserts {
		if _, err := db.Exec(in.query, in.args...); err != nil {
			t.Fatalf("insert (%s): %v", in.query, err)
		}
	}

	deleted, err := cleanupRetentionTables(ctx, db)
	if err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4", deleted)
	}

	for _, table := range []string{"error_groups", "deploy_markers", "watch_runs", "watch_alerts"} {
		if got := countRows(t, db, table); got != 1 {
			t.Errorf("%s: want 1 remaining, got %d", table, got)
		}
	}
}

// TestCleanupRetentionTables_BunTimestampFormat proves rows written in bun's
// "2006-01-02 15:04:05.999999-07:00" layout are compared correctly against an
// RFC3339 cutoff — a plain lexicographic compare gets this wrong because
// ' ' sorts before 'T'.
func TestCleanupRetentionTables_BunTimestampFormat(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"watch_runs":"7d"}')`,
	); err != nil {
		t.Fatalf("insert config: %v", err)
	}

	const bunLayout = "2006-01-02 15:04:05.999999-07:00"
	// A non-UTC zone makes the naive substring comparison wrong as well.
	zone := time.FixedZone("plus5", 5*60*60)
	old := time.Now().Add(-90 * 24 * time.Hour).In(zone).Format(bunLayout)
	recent := time.Now().Add(-1 * time.Hour).In(zone).Format(bunLayout)

	for id, ts := range map[string]string{"r-old": old, "r-new": recent} {
		if _, err := db.Exec(
			"INSERT INTO watch_runs (id, watch_id, started_at) VALUES (?, 'w', ?)", id, ts); err != nil {
			t.Fatalf("insert run: %v", err)
		}
	}

	if _, err := cleanupRetentionTables(ctx, db); err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}

	var remaining string
	if err := db.QueryRow("SELECT id FROM watch_runs").Scan(&remaining); err != nil {
		t.Fatalf("scan remaining: %v", err)
	}
	if remaining != "r-new" {
		t.Errorf("remaining run = %q, want %q", remaining, "r-new")
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
