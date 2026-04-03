package deepcapture

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestProcessInRequestLogs_TwoEntries(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp:   "2026-04-03T12:00:00Z",
		Service:     "test-app",
		Environment: "test",
		Context:     json.RawMessage(`{"request_id":"req-001","trace_id":"trace-abc","span_id":"span-xyz"}`),
		Event: &Event{
			Type:    "request",
			Message: "GET /users",
			Logs: json.RawMessage(`[
				{"level":"info","message":"Cache miss for user 42","metadata":{"cache_key":"user:42"},"at":10.5},
				{"level":"warn","message":"Slow query detected","metadata":{"query":"SELECT *","duration_ms":500},"at":55.3}
			]`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("ProcessInRequestLogs failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify 2 rows inserted (excluding the original test log row)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs WHERE event_type = 'in_request_log'`).Scan(&count); err != nil {
		t.Fatalf("counting in_request_log rows: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 in_request_log rows, got %d", count)
	}

	// Verify request_id and trace_id are carried through
	rows, err := db.Query(`SELECT level, message, request_id, trace_id, span_id, metadata, timestamp
		FROM logs WHERE event_type = 'in_request_log' ORDER BY id`)
	if err != nil {
		t.Fatalf("querying in_request_log rows: %v", err)
	}
	defer rows.Close()

	type logRow struct {
		Level     string
		Message   string
		RequestID sql.NullString
		TraceID   sql.NullString
		SpanID    sql.NullString
		Metadata  string
		Timestamp string
	}

	var results []logRow
	for rows.Next() {
		var r logRow
		if err := rows.Scan(&r.Level, &r.Message, &r.RequestID, &r.TraceID, &r.SpanID, &r.Metadata, &r.Timestamp); err != nil {
			t.Fatalf("scanning row: %v", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating rows: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// First log entry
	r0 := results[0]
	if r0.Level != "info" {
		t.Errorf("row 0: expected level 'info', got %q", r0.Level)
	}
	if r0.Message != "Cache miss for user 42" {
		t.Errorf("row 0: expected message 'Cache miss for user 42', got %q", r0.Message)
	}
	if !r0.RequestID.Valid || r0.RequestID.String != "req-001" {
		t.Errorf("row 0: expected request_id 'req-001', got %v", r0.RequestID)
	}
	if !r0.TraceID.Valid || r0.TraceID.String != "trace-abc" {
		t.Errorf("row 0: expected trace_id 'trace-abc', got %v", r0.TraceID)
	}
	if !r0.SpanID.Valid || r0.SpanID.String != "span-xyz" {
		t.Errorf("row 0: expected span_id 'span-xyz', got %v", r0.SpanID)
	}

	// Verify metadata contains cache_key
	if !strings.Contains(r0.Metadata, `"cache_key"`) {
		t.Errorf("row 0: expected metadata to contain cache_key, got %s", r0.Metadata)
	}

	// Second log entry
	r1 := results[1]
	if r1.Level != "warn" {
		t.Errorf("row 1: expected level 'warn', got %q", r1.Level)
	}
	if r1.Message != "Slow query detected" {
		t.Errorf("row 1: expected message 'Slow query detected', got %q", r1.Message)
	}
}

func TestProcessInRequestLogs_EmptyLogs(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp:   "2026-04-03T12:00:00Z",
		Service:     "test-app",
		Environment: "test",
		Event: &Event{
			Type:    "request",
			Message: "GET /users",
			// No Logs field
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("ProcessInRequestLogs failed on empty logs: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs WHERE event_type = 'in_request_log'`).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 in_request_log rows for empty logs, got %d", count)
	}
}

func TestProcessInRequestLogs_NilEvent(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp: "2026-04-03T12:00:00Z",
		Event:     nil,
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("ProcessInRequestLogs failed on nil event: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
}

func TestProcessInRequestLogs_TimestampOffset(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp:   "2026-04-03T12:00:00Z",
		Service:     "test-app",
		Environment: "test",
		Context:     json.RawMessage(`{"request_id":"req-ts"}`),
		Event: &Event{
			Type:    "request",
			Message: "GET /test",
			Logs: json.RawMessage(`[
				{"level":"debug","message":"early log","at":0},
				{"level":"info","message":"late log","at":1500.0}
			]`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("ProcessInRequestLogs failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	rows, err := db.Query(`SELECT timestamp FROM logs WHERE event_type = 'in_request_log' ORDER BY id`)
	if err != nil {
		t.Fatalf("querying timestamps: %v", err)
	}
	defer rows.Close()

	var timestamps []string
	for rows.Next() {
		var ts string
		if err := rows.Scan(&ts); err != nil {
			t.Fatalf("scanning timestamp: %v", err)
		}
		timestamps = append(timestamps, ts)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterating rows: %v", err)
	}

	if len(timestamps) != 2 {
		t.Fatalf("expected 2 timestamps, got %d", len(timestamps))
	}

	// First entry: at=0 should equal the parent timestamp
	if timestamps[0] != "2026-04-03T12:00:00Z" {
		t.Errorf("entry at=0: expected '2026-04-03T12:00:00Z', got %q", timestamps[0])
	}

	// Second entry: at=1500ms = 1.5s offset => 12:00:01.5
	if !strings.Contains(timestamps[1], "12:00:01.5") {
		t.Errorf("entry at=1500: expected timestamp containing '12:00:01.5', got %q", timestamps[1])
	}
}

func TestProcessInRequestLogs_NoMetadata(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp:   "2026-04-03T12:00:00Z",
		Service:     "test-app",
		Environment: "test",
		Context:     json.RawMessage(`{}`),
		Event: &Event{
			Type:    "request",
			Message: "GET /test",
			Logs: json.RawMessage(`[
				{"level":"info","message":"no metadata entry","at":5}
			]`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("ProcessInRequestLogs failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	var metadata string
	if err := db.QueryRow(`SELECT metadata FROM logs WHERE event_type = 'in_request_log'`).Scan(&metadata); err != nil {
		t.Fatalf("querying metadata: %v", err)
	}
	if metadata != "{}" {
		t.Errorf("expected metadata '{}' for nil metadata, got %q", metadata)
	}
}

func TestProcessInRequestLogs_InvalidJSON(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Timestamp: "2026-04-03T12:00:00Z",
		Event: &Event{
			Type: "request",
			Logs: json.RawMessage(`{not valid json}`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	// Invalid JSON should be treated as non-fatal (return nil)
	if err := ProcessInRequestLogs(tx, doc, logID); err != nil {
		t.Fatalf("expected nil error for invalid JSON, got: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM logs WHERE event_type = 'in_request_log'`).Scan(&count); err != nil {
		t.Fatalf("counting rows: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows for invalid JSON, got %d", count)
	}
}
