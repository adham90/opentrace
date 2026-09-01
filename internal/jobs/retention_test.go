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

		CREATE TABLE error_groups (
			fingerprint   TEXT NOT NULL,
			environment   TEXT NOT NULL DEFAULT '',
			service       TEXT NOT NULL DEFAULT '',
			first_seen_at TEXT NOT NULL,
			last_seen_at  TEXT NOT NULL,
			PRIMARY KEY (fingerprint, environment)
		);

		CREATE TABLE watch_runs (
			id         TEXT PRIMARY KEY,
			watch_id   TEXT NOT NULL,
			status     TEXT NOT NULL DEFAULT 'running',
			started_at TEXT NOT NULL
		);

		CREATE TABLE trace_status (
			trace_id        TEXT PRIMARY KEY,
			span_count      INTEGER NOT NULL DEFAULT 0,
			services        TEXT NOT NULL DEFAULT '[]',
			first_seen_at   TEXT NOT NULL,
			last_updated_at TEXT NOT NULL,
			status          TEXT NOT NULL DEFAULT 'partial',
			has_errors      INTEGER NOT NULL DEFAULT 0
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

// "never" must skip the table entirely, not fall through to a zero cutoff
// that deletes everything.
func TestCleanupRetentionTables_NeverConfig(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(
		`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"error_groups":"never"}')`,
	); err != nil {
		t.Fatalf("insert config: %v", err)
	}

	old := time.Now().UTC().Add(-999 * 24 * time.Hour).Format(time.RFC3339)
	if _, err := db.Exec(
		"INSERT INTO error_groups (fingerprint, first_seen_at, last_seen_at) VALUES ('a', ?, ?)",
		old, old); err != nil {
		t.Fatalf("insert error group: %v", err)
	}

	if _, err := cleanupRetentionTables(ctx, db); err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}

	if got := countRows(t, db, "error_groups"); got != 1 {
		t.Errorf("error_groups: want 1 (never deleted), got %d", got)
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
// watch_runs and watch_alerts TTLs accepted by the admin tool
// are actually applied — previously they were parsed and silently dropped.
func TestCleanupRetentionTables_ConfiguredTTLsEnforced(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO app_config (key, value) VALUES ('retention_policy',
		'{"error_groups":"30d","watch_runs":"7d","watch_alerts":"7d"}')`,
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
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}

	for _, table := range []string{"error_groups", "watch_runs", "watch_alerts"} {
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

// TestCleanupRetentionTables_PrunesTraceStatus covers trace_status being added
// to retention. It holds one row per trace id and had no TTL at all, so it grew
// for the life of the deployment — a stress run put 740k rows in it in five
// minutes, and the periodic stale-trace sweep over that backlog was enough on
// its own to OOM-kill a 256MB container.
func TestCleanupRetentionTables_PrunesTraceStatus(t *testing.T) {
	db := setupRetentionTestDB(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	recent := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	for _, r := range []struct{ id, ts string }{{"t-old", old}, {"t-recent", recent}} {
		if _, err := db.Exec(
			`INSERT INTO trace_status (trace_id, span_count, services, first_seen_at, last_updated_at, status, has_errors)
			 VALUES (?, 1, '[]', ?, ?, 'partial', 0)`, r.id, r.ts, r.ts,
		); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}

	// No explicit policy: the default TTL has to be bounded, or the table is
	// never pruned on a deployment nobody configured.
	if _, err := cleanupRetentionTables(ctx, db); err != nil {
		t.Fatalf("cleanupRetentionTables: %v", err)
	}

	if got := countRows(t, db, "trace_status"); got != 1 {
		t.Errorf("trace_status: want 1 remaining (the recent trace), got %d", got)
	}
	var survivor string
	if err := db.QueryRow(`SELECT trace_id FROM trace_status`).Scan(&survivor); err != nil {
		t.Fatalf("read survivor: %v", err)
	}
	if survivor != "t-recent" {
		t.Errorf("kept %q, want t-recent", survivor)
	}
}
