package deepcapture

import (
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

// createTestTables sets up the minimal schema needed for deep capture tests.
const createTestTables = `
CREATE TABLE IF NOT EXISTS logs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp       TEXT NOT NULL,
    level           TEXT NOT NULL DEFAULT 'info',
    service         TEXT NOT NULL DEFAULT '',
    message         TEXT NOT NULL DEFAULT '',
    environment     TEXT,
    metadata        TEXT DEFAULT '{}',
    event_type      TEXT NOT NULL DEFAULT '',
    trace_id        TEXT,
    span_id         TEXT,
    parent_span_id  TEXT,
    commit_hash     TEXT,
    request_id      TEXT,
    exception_class TEXT,
    error_fingerprint TEXT,
    source_file     TEXT,
    source_line     INTEGER,
    user_id         TEXT DEFAULT ''
);

CREATE TABLE IF NOT EXISTS request_captures (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id            INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    request_headers   TEXT,
    request_body      TEXT,
    request_size      INTEGER,
    content_type      TEXT,
    ip_address        TEXT,
    user_agent        TEXT,
    referer           TEXT,
    cookies           TEXT,
    session_data      TEXT,
    response_headers  TEXT,
    response_body     TEXT,
    response_size     INTEGER,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS sql_captures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id          INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    raw_sql         TEXT,
    normalized_sql  TEXT,
    bind_values     TEXT,
    row_count       INTEGER,
    duration_ms     REAL,
    cached          INTEGER DEFAULT 0,
    in_transaction  INTEGER DEFAULT 0,
    explain_plan    TEXT,
    pool_size       INTEGER,
    pool_busy       INTEGER,
    pool_waiting    INTEGER,
    caller_location TEXT,
    fingerprint     TEXT,
    table_name      TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS http_captures (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id            INTEGER NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    method            TEXT,
    url               TEXT,
    host              TEXT,
    vendor            TEXT,
    status            INTEGER,
    duration_ms       REAL,
    request_headers   TEXT,
    request_body      TEXT,
    response_headers  TEXT,
    response_body     TEXT,
    response_size     INTEGER,
    retry_attempt     INTEGER DEFAULT 0,
    error_class       TEXT,
    created_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS email_captures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id          INTEGER REFERENCES logs(id) ON DELETE CASCADE,
    mailer_class    TEXT,
    mailer_action   TEXT,
    from_address    TEXT,
    to_addresses    TEXT,
    cc_addresses    TEXT,
    bcc_addresses   TEXT,
    subject         TEXT,
    body_html       TEXT,
    body_text       TEXT,
    template_name   TEXT,
    template_vars   TEXT,
    attachments     TEXT,
    delivery_status TEXT,
    smtp_response   TEXT,
    duration_ms     REAL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS audit_captures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id          INTEGER REFERENCES logs(id) ON DELETE CASCADE,
    record_type     TEXT,
    record_id       TEXT,
    action          TEXT,
    actor_id        TEXT,
    actor_type      TEXT,
    changed_fields  TEXT,
    full_before     TEXT,
    full_after      TEXT,
    request_id      TEXT,
    ip_address      TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE IF NOT EXISTS file_captures (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    log_id          INTEGER REFERENCES logs(id) ON DELETE CASCADE,
    action          TEXT,
    filename        TEXT,
    size_bytes      INTEGER,
    content_type    TEXT,
    storage_service TEXT,
    key             TEXT,
    duration_ms     REAL,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
`

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	db, err := sql.Open("sqlite", dbPath+"?_foreign_keys=on")
	if err != nil {
		t.Fatalf("opening test SQLite: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(createTestTables); err != nil {
		db.Close()
		t.Fatalf("creating tables: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// insertTestLog inserts a dummy log row and returns its ID.
func insertTestLog(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO logs (timestamp, level, service, message) VALUES (?, ?, ?, ?)`,
		"2026-04-03T12:00:00Z", "info", "test-app", "test message",
	)
	if err != nil {
		t.Fatalf("inserting test log: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("getting last insert id: %v", err)
	}
	return id
}

func TestProcessDocument_NilEvent(t *testing.T) {
	db := setupTestDB(t)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	doc := &Document{Event: nil}
	if err := ProcessDocument(tx, doc, 1); err != nil {
		t.Fatalf("expected no error for nil event, got: %v", err)
	}
}

func TestProcessDocument_AllSections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		ID:          "doc-001",
		Timestamp:   "2026-04-03T12:00:00Z",
		Level:       "info",
		Service:     "test-app",
		Environment: "test",
		Event: &Event{
			Type:    "request",
			Message: "GET /users",
			Request: json.RawMessage(`{
				"headers": {"Content-Type": "application/json"},
				"body": "{\"name\":\"test\"}",
				"size": 42,
				"content_type": "application/json",
				"ip_address": "10.0.0.1",
				"user_agent": "TestAgent/1.0",
				"referer": "https://example.com",
				"cookies": {"session": "abc123"},
				"session_data": {"user_id": 1}
			}`),
			Response: json.RawMessage(`{
				"headers": {"X-Request-ID": "req-001"},
				"body": "{\"ok\":true}",
				"size": 11
			}`),
			DB: &DBSection{
				NPlusOne:         true,
				DuplicateQueries: 2,
				SlowQueries:      1,
				Queries: json.RawMessage(`[
					{
						"raw_sql": "SELECT * FROM users WHERE id = ?",
						"normalized_sql": "SELECT * FROM users WHERE id = ?",
						"bind_values": [1],
						"row_count": 1,
						"duration_ms": 1.5,
						"cached": false,
						"in_transaction": true,
						"caller_location": "app/models/user.rb:42",
						"fingerprint": "abc123",
						"table_name": "users"
					},
					{
						"raw_sql": "SELECT * FROM posts WHERE user_id = ?",
						"normalized_sql": "SELECT * FROM posts WHERE user_id = ?",
						"bind_values": [1],
						"row_count": 5,
						"duration_ms": 3.2,
						"cached": false,
						"in_transaction": false,
						"caller_location": "app/models/post.rb:10",
						"fingerprint": "def456",
						"table_name": "posts"
					}
				]`),
			},
			External: json.RawMessage(`[
				{
					"method": "POST",
					"url": "https://api.stripe.com/v1/charges",
					"host": "api.stripe.com",
					"vendor": "stripe",
					"status": 200,
					"duration_ms": 150.5,
					"request_headers": {"Authorization": "[FILTERED]"},
					"request_body": "{\"amount\":1000}",
					"response_headers": {"Content-Type": "application/json"},
					"response_body": "{\"id\":\"ch_xxx\"}",
					"response_size": 500,
					"retry_attempt": 0,
					"error_class": ""
				}
			]`),
			Email: json.RawMessage(`[
				{
					"mailer_class": "UserMailer",
					"mailer_action": "welcome",
					"from_address": "noreply@example.com",
					"to_addresses": ["user@example.com"],
					"cc_addresses": [],
					"bcc_addresses": [],
					"subject": "Welcome!",
					"body_html": "<h1>Welcome</h1>",
					"body_text": "Welcome",
					"template_name": "welcome",
					"template_vars": {"name": "Test User"},
					"attachments": [],
					"delivery_status": "delivered",
					"smtp_response": "250 OK",
					"duration_ms": 45.2
				}
			]`),
			Audit: json.RawMessage(`[
				{
					"record_type": "User",
					"record_id": "42",
					"action": "update",
					"actor_id": "1",
					"actor_type": "User",
					"changed_fields": {"email": ["old@example.com", "new@example.com"]},
					"full_before": {"email": "old@example.com"},
					"full_after": {"email": "new@example.com"},
					"request_id": "req-001",
					"ip_address": "10.0.0.1"
				}
			]`),
			Files: json.RawMessage(`[
				{
					"action": "upload",
					"filename": "avatar.png",
					"size_bytes": 204800,
					"content_type": "image/png",
					"storage_service": "s3",
					"key": "uploads/avatar.png",
					"duration_ms": 120.0
				},
				{
					"action": "delete",
					"filename": "old_avatar.png",
					"size_bytes": 0,
					"content_type": "image/png",
					"storage_service": "s3",
					"key": "uploads/old_avatar.png",
					"duration_ms": 15.0
				}
			]`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessDocument(tx, doc, logID); err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Verify rows were inserted into each detail table.
	assertRowCount(t, db, "request_captures", logID, 1)
	assertRowCount(t, db, "sql_captures", logID, 2)
	assertRowCount(t, db, "http_captures", logID, 1)
	assertRowCount(t, db, "email_captures", logID, 1)
	assertRowCount(t, db, "audit_captures", logID, 1)
	assertRowCount(t, db, "file_captures", logID, 2)

	// Spot-check a few values for correctness.
	var rawSQL string
	if err := db.QueryRow(`SELECT raw_sql FROM sql_captures WHERE log_id = ? ORDER BY id LIMIT 1`, logID).Scan(&rawSQL); err != nil {
		t.Fatalf("querying sql_captures: %v", err)
	}
	if rawSQL != "SELECT * FROM users WHERE id = ?" {
		t.Errorf("expected sql capture raw_sql, got: %s", rawSQL)
	}

	var method, vendor string
	var status int
	if err := db.QueryRow(`SELECT method, vendor, status FROM http_captures WHERE log_id = ?`, logID).Scan(&method, &vendor, &status); err != nil {
		t.Fatalf("querying http_captures: %v", err)
	}
	if method != "POST" || vendor != "stripe" || status != 200 {
		t.Errorf("unexpected http_captures values: method=%s vendor=%s status=%d", method, vendor, status)
	}

	var subject string
	if err := db.QueryRow(`SELECT subject FROM email_captures WHERE log_id = ?`, logID).Scan(&subject); err != nil {
		t.Fatalf("querying email_captures: %v", err)
	}
	if subject != "Welcome!" {
		t.Errorf("expected subject 'Welcome!', got: %s", subject)
	}

	var recordType, action string
	if err := db.QueryRow(`SELECT record_type, action FROM audit_captures WHERE log_id = ?`, logID).Scan(&recordType, &action); err != nil {
		t.Fatalf("querying audit_captures: %v", err)
	}
	if recordType != "User" || action != "update" {
		t.Errorf("unexpected audit_captures: record_type=%s action=%s", recordType, action)
	}

	var fileAction, filename string
	if err := db.QueryRow(`SELECT action, filename FROM file_captures WHERE log_id = ? ORDER BY id LIMIT 1`, logID).Scan(&fileAction, &filename); err != nil {
		t.Fatalf("querying file_captures: %v", err)
	}
	if fileAction != "upload" || filename != "avatar.png" {
		t.Errorf("unexpected file_captures: action=%s filename=%s", fileAction, filename)
	}

	var ipAddress, userAgent string
	if err := db.QueryRow(`SELECT ip_address, user_agent FROM request_captures WHERE log_id = ?`, logID).Scan(&ipAddress, &userAgent); err != nil {
		t.Fatalf("querying request_captures: %v", err)
	}
	if ipAddress != "10.0.0.1" || userAgent != "TestAgent/1.0" {
		t.Errorf("unexpected request_captures: ip=%s ua=%s", ipAddress, userAgent)
	}
}

func TestProcessDocument_EmptySections(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Event: &Event{
			Type:    "request",
			Message: "minimal event",
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessDocument(tx, doc, logID); err != nil {
		t.Fatalf("ProcessDocument failed on empty sections: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// No detail rows should be inserted.
	for _, table := range []string{
		"request_captures", "sql_captures", "http_captures",
		"email_captures", "audit_captures", "file_captures",
	} {
		assertRowCount(t, db, table, logID, 0)
	}
}

func TestProcessDocument_PartialSections(t *testing.T) {
	db := setupTestDB(t)
	logID := insertTestLog(t, db)

	doc := &Document{
		Event: &Event{
			Type:    "request",
			Message: "partial event",
			External: json.RawMessage(`[
				{
					"method": "GET",
					"url": "https://api.example.com/data",
					"host": "api.example.com",
					"status": 200,
					"duration_ms": 50.0
				}
			]`),
		},
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := ProcessDocument(tx, doc, logID); err != nil {
		t.Fatalf("ProcessDocument failed: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Only http_captures should have rows.
	assertRowCount(t, db, "http_captures", logID, 1)
	assertRowCount(t, db, "request_captures", logID, 0)
	assertRowCount(t, db, "sql_captures", logID, 0)
	assertRowCount(t, db, "email_captures", logID, 0)
	assertRowCount(t, db, "audit_captures", logID, 0)
	assertRowCount(t, db, "file_captures", logID, 0)
}

func TestExtractHandlers_Individually(t *testing.T) {
	t.Run("RequestCaptureHandler/nil_sections", func(t *testing.T) {
		h := &RequestCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts for nil request/response, got %d", len(inserts))
		}
	})

	t.Run("SQLCaptureHandler/nil_db", func(t *testing.T) {
		h := &SQLCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts for nil DB, got %d", len(inserts))
		}
	})

	t.Run("SQLCaptureHandler/bad_json", func(t *testing.T) {
		h := &SQLCaptureHandler{}
		doc := &Document{Event: &Event{
			DB: &DBSection{Queries: json.RawMessage(`{not valid}`)},
		}}
		_, err := h.Extract(doc, 1)
		if err == nil {
			t.Error("expected error for bad JSON, got nil")
		}
	})

	t.Run("HTTPCaptureHandler/nil_external", func(t *testing.T) {
		h := &HTTPCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts, got %d", len(inserts))
		}
	})

	t.Run("EmailCaptureHandler/nil_email", func(t *testing.T) {
		h := &EmailCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts, got %d", len(inserts))
		}
	})

	t.Run("AuditCaptureHandler/nil_audit", func(t *testing.T) {
		h := &AuditCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts, got %d", len(inserts))
		}
	})

	t.Run("FileCaptureHandler/nil_files", func(t *testing.T) {
		h := &FileCaptureHandler{}
		doc := &Document{Event: &Event{}}
		inserts, err := h.Extract(doc, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if inserts != nil {
			t.Errorf("expected nil inserts, got %d", len(inserts))
		}
	})
}

func assertRowCount(t *testing.T, db *sql.DB, table string, logID int64, expected int) {
	t.Helper()
	var count int
	// Using Sprintf here is safe since table names come from test constants, not user input.
	if err := db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE log_id = ?", logID).Scan(&count); err != nil {
		t.Fatalf("counting rows in %s: %v", table, err)
	}
	if count != expected {
		t.Errorf("%s: expected %d rows, got %d", table, expected, count)
	}
}
