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

		CREATE TABLE logs (
			id        INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			level     TEXT NOT NULL DEFAULT 'info',
			message   TEXT NOT NULL DEFAULT ''
		);

		CREATE TABLE request_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE sql_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE http_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE email_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE audit_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE file_captures (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE request_summaries (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			log_id     INTEGER NOT NULL,
			created_at TEXT NOT NULL
		);

		CREATE TABLE metric_buckets (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
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
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count rows in %s: %v", table, err)
	}
	return count
}

func TestParseTTL(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"365d", 365 * 24 * time.Hour},
		{"1d", 24 * time.Hour},
		{"never", 0},
		{"NEVER", 0},
		{"", 0},
		{"  never  ", 0},
		{"2h", 2 * time.Hour},
		{"30m", 30 * time.Minute},
	}
	for _, tc := range tests {
		got := parseTTL(tc.input)
		if got != tc.want {
			t.Errorf("parseTTL(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestRunRetentionCleanup_DeletesExpiredRows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupRetentionTestDB(t)
	ctx := context.Background()

	// Insert retention config: logs=2d, request_captures=1d
	_, err := db.Exec(`INSERT INTO app_config (key, value) VALUES ('retention_policy',
		'{"logs":"2d","request_captures":"1d","sql_captures":"1d","http_captures":"1d","email_captures":"1d","audit_captures":"1d","request_summaries":"1d","error_groups":"never","metric_buckets":"1d","deploy_markers":"never"}')`)
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-3 * 24 * time.Hour).Format(time.RFC3339) // 3 days ago
	recent := now.Add(-6 * time.Hour).Format(time.RFC3339)   // 6 hours ago

	// Insert old and recent logs
	_, err = db.Exec("INSERT INTO logs (timestamp, message) VALUES (?, 'old log')", old)
	if err != nil {
		t.Fatalf("insert old log: %v", err)
	}
	_, err = db.Exec("INSERT INTO logs (timestamp, message) VALUES (?, 'recent log')", recent)
	if err != nil {
		t.Fatalf("insert recent log: %v", err)
	}

	// Insert old and recent request_captures
	_, err = db.Exec("INSERT INTO request_captures (log_id, created_at) VALUES (1, ?)", old)
	if err != nil {
		t.Fatalf("insert old capture: %v", err)
	}
	_, err = db.Exec("INSERT INTO request_captures (log_id, created_at) VALUES (2, ?)", recent)
	if err != nil {
		t.Fatalf("insert recent capture: %v", err)
	}

	// Run cleanup
	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	// Verify: old rows deleted, recent rows kept
	if got := countRows(t, db, "logs"); got != 1 {
		t.Errorf("logs: got %d rows, want 1", got)
	}
	if got := countRows(t, db, "request_captures"); got != 1 {
		t.Errorf("request_captures: got %d rows, want 1", got)
	}

	// Verify the surviving log is the recent one
	var msg string
	if err := db.QueryRow("SELECT message FROM logs").Scan(&msg); err != nil {
		t.Fatalf("scan surviving log: %v", err)
	}
	if msg != "recent log" {
		t.Errorf("surviving log message = %q, want %q", msg, "recent log")
	}
}

func TestRunRetentionCleanup_RespectsNever(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupRetentionTestDB(t)
	ctx := context.Background()

	// Config where logs = "never" -- nothing should be deleted from logs
	_, err := db.Exec(`INSERT INTO app_config (key, value) VALUES ('retention_policy',
		'{"logs":"never","request_captures":"never","sql_captures":"never","http_captures":"never","email_captures":"never","audit_captures":"never","request_summaries":"never","error_groups":"never","metric_buckets":"never","deploy_markers":"never"}')`)
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}

	old := time.Now().UTC().Add(-365 * 24 * time.Hour).Format(time.RFC3339)
	_, err = db.Exec("INSERT INTO logs (timestamp, message) VALUES (?, 'ancient log')", old)
	if err != nil {
		t.Fatalf("insert old log: %v", err)
	}

	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	if got := countRows(t, db, "logs"); got != 1 {
		t.Errorf("logs: got %d rows, want 1 (never should not delete)", got)
	}
}

func TestRunRetentionCleanup_NoConfigFallsBackToDefaults(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupRetentionTestDB(t)
	ctx := context.Background()

	// No retention_policy row in app_config -- should use DefaultRetentionConfig (logs=30d)
	old := time.Now().UTC().Add(-60 * 24 * time.Hour).Format(time.RFC3339) // 60 days ago
	_, err := db.Exec("INSERT INTO logs (timestamp, message) VALUES (?, 'very old log')", old)
	if err != nil {
		t.Fatalf("insert old log: %v", err)
	}

	if err := RunRetentionCleanup(ctx, db); err != nil {
		t.Fatalf("RunRetentionCleanup: %v", err)
	}

	// 60 days > 30d default, so it should be deleted
	if got := countRows(t, db, "logs"); got != 0 {
		t.Errorf("logs: got %d rows, want 0", got)
	}
}

func TestDeleteInBatches_EmptyTable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupRetentionTestDB(t)
	ctx := context.Background()

	cutoff := time.Now().UTC().Format(time.RFC3339)
	deleted, err := deleteInBatches(ctx, db, "logs", "timestamp", cutoff, 1000)
	if err != nil {
		t.Fatalf("deleteInBatches on empty table: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
}

func TestDeleteInBatches_MultipleBatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupRetentionTestDB(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)

	// Insert 50 old rows
	for i := 0; i < 50; i++ {
		_, err := db.Exec("INSERT INTO logs (timestamp, message) VALUES (?, 'old')", old)
		if err != nil {
			t.Fatalf("insert row %d: %v", i, err)
		}
	}

	cutoff := time.Now().UTC().Format(time.RFC3339)
	// Use batch size of 10 to force multiple iterations
	deleted, err := deleteInBatches(ctx, db, "logs", "timestamp", cutoff, 10)
	if err != nil {
		t.Fatalf("deleteInBatches: %v", err)
	}
	if deleted != 50 {
		t.Errorf("deleted = %d, want 50", deleted)
	}
	if got := countRows(t, db, "logs"); got != 0 {
		t.Errorf("remaining rows = %d, want 0", got)
	}
}
