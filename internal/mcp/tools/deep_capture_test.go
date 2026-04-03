package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"
)

// setupDeepCaptureDB creates an in-memory SQLite database with the deep capture
// schema and returns a DeepCaptureDeps. The database is closed when the test ends.
func setupDeepCaptureDB(t *testing.T) DeepCaptureDeps {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Create the tables matching the migration schema.
	schema := `
	CREATE TABLE IF NOT EXISTS logs (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		timestamp         TEXT NOT NULL DEFAULT '',
		level             TEXT NOT NULL DEFAULT 'info',
		service           TEXT NOT NULL DEFAULT '',
		message           TEXT NOT NULL DEFAULT '',
		request_id        TEXT
	);

	CREATE TABLE IF NOT EXISTS request_captures (
		id                INTEGER PRIMARY KEY AUTOINCREMENT,
		log_id            INTEGER NOT NULL REFERENCES logs(id),
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
		log_id          INTEGER NOT NULL REFERENCES logs(id),
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
		log_id            INTEGER NOT NULL REFERENCES logs(id),
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
		log_id          INTEGER REFERENCES logs(id),
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
		log_id          INTEGER REFERENCES logs(id),
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
		log_id          INTEGER REFERENCES logs(id),
		action          TEXT,
		filename        TEXT,
		size_bytes      INTEGER,
		content_type    TEXT,
		storage_service TEXT,
		key             TEXT,
		duration_ms     REAL,
		created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
	);

	CREATE TABLE IF NOT EXISTS app_config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT '{}'
	);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return DeepCaptureDeps{DB: db}
}

// seedTestLog inserts a log entry and returns its ID.
func seedTestLog(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO logs (timestamp, level, service, message, request_id)
		 VALUES ('2026-04-03T10:00:00Z', 'info', 'api', 'test request', 'req-abc123')`)
	if err != nil {
		t.Fatalf("insert log: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

func TestDeepCaptureHandler_UnknownAction(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	handler := DeepCaptureHandler(deps)

	req := MakeCallToolRequest("deep_capture", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for unknown action")
	}
}

func TestDeepCaptureHandler_MissingAction(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	handler := DeepCaptureHandler(deps)

	req := MakeCallToolRequest("deep_capture", map[string]any{})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when action is missing")
	}
}

// --- request_capture ---

func TestGetRequestCapture_Valid(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO request_captures (log_id, request_headers, request_body, request_size, content_type,
		 ip_address, user_agent, response_headers, response_body, response_size)
		 VALUES (?, '{"Host":"example.com"}', '{"key":"val"}', 42, 'application/json',
		 '10.0.0.1', 'TestAgent/1.0', '{"Content-Type":"text/html"}', '<h1>OK</h1>', 128)`,
		logID)
	if err != nil {
		t.Fatalf("insert request_capture: %v", err)
	}

	args := map[string]any{"action": "request_capture", "log_id": float64(logID)}
	result, err := handleGetRequestCapture(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["ip_address"] != "10.0.0.1" {
		t.Errorf("ip_address = %v, want 10.0.0.1", resp["ip_address"])
	}
	if resp["user_agent"] != "TestAgent/1.0" {
		t.Errorf("user_agent = %v, want TestAgent/1.0", resp["user_agent"])
	}
	if resp["request_size"] != float64(42) {
		t.Errorf("request_size = %v, want 42", resp["request_size"])
	}
}

func TestGetRequestCapture_NotFound(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "request_capture", "log_id": float64(9999)}
	result, err := handleGetRequestCapture(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected no error for missing capture (should be empty result)")
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty result text")
	}
}

func TestGetRequestCapture_MissingLogID(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "request_capture"}
	result, err := handleGetRequestCapture(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when log_id is missing")
	}
}

func TestGetRequestCapture_NilDB(t *testing.T) {
	deps := DeepCaptureDeps{DB: nil}
	args := map[string]any{"action": "request_capture", "log_id": float64(1)}
	result, err := handleGetRequestCapture(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when DB is nil")
	}
}

// --- sql_captures ---

func TestGetSQLCaptures_Valid(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	for i := 0; i < 3; i++ {
		_, err := deps.DB.Exec(
			`INSERT INTO sql_captures (log_id, raw_sql, normalized_sql, duration_ms, fingerprint, table_name)
			 VALUES (?, 'SELECT * FROM users WHERE id = 1', 'SELECT * FROM users WHERE id = ?', ?, 'fp-abc', 'users')`,
			logID, float64(i)*1.5)
		if err != nil {
			t.Fatalf("insert sql_capture: %v", err)
		}
	}

	args := map[string]any{"action": "sql_captures", "log_id": float64(logID)}
	result, err := handleGetSQLCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	total, ok := resp["total"].(float64)
	if !ok || int(total) != 3 {
		t.Errorf("total = %v, want 3", resp["total"])
	}

	queries, ok := resp["queries"].([]any)
	if !ok {
		t.Fatalf("expected queries array, got %T", resp["queries"])
	}
	if len(queries) != 3 {
		t.Errorf("queries length = %d, want 3", len(queries))
	}
}

func TestGetSQLCaptures_Empty(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "sql_captures", "log_id": float64(9999)}
	result, err := handleGetSQLCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected no error for empty result set")
	}
}

// --- http_captures ---

func TestGetHTTPCaptures_Valid(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO http_captures (log_id, method, url, host, vendor, status, duration_ms)
		 VALUES (?, 'GET', 'https://api.stripe.com/v1/charges', 'api.stripe.com', 'stripe', 200, 150.5)`,
		logID)
	if err != nil {
		t.Fatalf("insert http_capture: %v", err)
	}

	args := map[string]any{"action": "http_captures", "log_id": float64(logID)}
	result, err := handleGetHTTPCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

// --- email_captures ---

func TestGetEmailCaptures_ByLogID(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO email_captures (log_id, mailer_class, subject, to_addresses, delivery_status)
		 VALUES (?, 'UserMailer', 'Welcome!', 'user@example.com', 'delivered')`,
		logID)
	if err != nil {
		t.Fatalf("insert email_capture: %v", err)
	}

	args := map[string]any{"action": "email_captures", "log_id": float64(logID)}
	result, err := handleGetEmailCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

func TestGetEmailCaptures_ByLast(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO email_captures (log_id, subject, to_addresses)
		 VALUES (?, 'Recent email', 'test@example.com')`,
		logID)
	if err != nil {
		t.Fatalf("insert email_capture: %v", err)
	}

	args := map[string]any{"action": "email_captures", "last": "1h"}
	result, err := handleGetEmailCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

func TestGetEmailCaptures_MissingBothParams(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "email_captures"}
	result, err := handleGetEmailCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when both log_id and last are missing")
	}
}

// --- audit_trail ---

func TestGetAuditTrail_Valid(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	for i := 0; i < 3; i++ {
		_, err := deps.DB.Exec(
			`INSERT INTO audit_captures (log_id, record_type, record_id, action, actor_id, actor_type)
			 VALUES (?, 'User', '42', 'update', 'admin-1', 'Admin')`,
			logID)
		if err != nil {
			t.Fatalf("insert audit_capture: %v", err)
		}
	}

	args := map[string]any{"action": "audit_trail", "record_type": "User", "record_id": "42"}
	result, err := handleGetAuditTrail(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["record_type"] != "User" {
		t.Errorf("record_type = %v, want User", resp["record_type"])
	}
	if resp["total"] != float64(3) {
		t.Errorf("total = %v, want 3", resp["total"])
	}
}

func TestGetAuditTrail_MissingParams(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "audit_trail", "record_type": "User"}
	result, err := handleGetAuditTrail(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when record_id is missing")
	}
}

func TestGetAuditTrail_NotFound(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "audit_trail", "record_type": "User", "record_id": "nonexistent"}
	result, err := handleGetAuditTrail(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected no error for empty audit trail")
	}
}

// --- search_audit ---

func TestSearchAudit_ByActorID(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO audit_captures (log_id, record_type, record_id, action, actor_id, actor_type)
		 VALUES (?, 'Order', '99', 'create', 'user-5', 'User')`,
		logID)
	if err != nil {
		t.Fatalf("insert audit_capture: %v", err)
	}

	args := map[string]any{"actor_id": "user-5"}
	result, err := handleSearchAudit(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

func TestSearchAudit_MissingFilters(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{}
	result, err := handleSearchAudit(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no filters provided")
	}
}

// --- search_sql ---

func TestSearchSQL_ByFingerprint(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO sql_captures (log_id, raw_sql, duration_ms, fingerprint, table_name)
		 VALUES (?, 'SELECT * FROM orders', 5.2, 'fp-orders', 'orders')`,
		logID)
	if err != nil {
		t.Fatalf("insert sql_capture: %v", err)
	}

	args := map[string]any{"action": "search_sql", "fingerprint": "fp-orders"}
	result, err := handleSearchSQL(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

func TestSearchSQL_ByMinDuration(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	// Insert one fast and one slow query.
	_, err := deps.DB.Exec(
		`INSERT INTO sql_captures (log_id, raw_sql, duration_ms, fingerprint, table_name)
		 VALUES (?, 'SELECT 1', 0.5, 'fp-fast', 'dual')`,
		logID)
	if err != nil {
		t.Fatalf("insert fast: %v", err)
	}
	_, err = deps.DB.Exec(
		`INSERT INTO sql_captures (log_id, raw_sql, duration_ms, fingerprint, table_name)
		 VALUES (?, 'SELECT * FROM big_table', 100.0, 'fp-slow', 'big_table')`,
		logID)
	if err != nil {
		t.Fatalf("insert slow: %v", err)
	}

	args := map[string]any{"action": "search_sql", "min_duration_ms": float64(10)}
	result, err := handleSearchSQL(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1 (only slow query)", resp["total"])
	}
}

func TestSearchSQL_MissingFilters(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "search_sql"}
	result, err := handleSearchSQL(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no filters provided")
	}
}

// --- file_captures ---

func TestGetFileCaptures_Valid(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedTestLog(t, deps.DB)

	_, err := deps.DB.Exec(
		`INSERT INTO file_captures (log_id, action, filename, size_bytes, content_type, storage_service)
		 VALUES (?, 'upload', 'avatar.png', 102400, 'image/png', 's3')`,
		logID)
	if err != nil {
		t.Fatalf("insert file_capture: %v", err)
	}

	args := map[string]any{"action": "file_captures", "log_id": float64(logID)}
	result, err := handleGetFileCaptures(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["total"] != float64(1) {
		t.Errorf("total = %v, want 1", resp["total"])
	}
}

// --- PII config ---

func TestGetPIIConfig(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	// Seed default config.
	_, err := deps.DB.Exec(
		`INSERT INTO app_config (key, value) VALUES ('pii_scrubbing', '{"enabled":true,"builtin":{"credit_cards":true}}')`)
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}

	result, err := handleGetPIIConfig(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["enabled"] != true {
		t.Errorf("enabled = %v, want true", resp["enabled"])
	}
}

func TestUpdatePIIConfig(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	// Seed initial config.
	_, err := deps.DB.Exec(
		`INSERT INTO app_config (key, value) VALUES ('pii_scrubbing', '{"enabled":false}')`)
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}

	args := map[string]any{
		"action": "update_pii_config",
		"config": map[string]any{"enabled": true, "builtin": map[string]any{"emails": true}},
	}
	result, err := handleUpdatePIIConfig(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "updated" {
		t.Errorf("status = %v, want updated", resp["status"])
	}

	// Verify the value was persisted.
	var value string
	err = deps.DB.QueryRow("SELECT value FROM app_config WHERE key = 'pii_scrubbing'").Scan(&value)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal([]byte(value), &persisted); err != nil {
		t.Fatalf("unmarshal persisted: %v", err)
	}
	if persisted["enabled"] != true {
		t.Errorf("persisted enabled = %v, want true", persisted["enabled"])
	}
}

func TestUpdatePIIConfig_MissingConfig(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{"action": "update_pii_config"}
	result, err := handleUpdatePIIConfig(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when config is missing")
	}
}

// --- retention policy ---

func TestGetRetention(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	_, err := deps.DB.Exec(
		`INSERT INTO app_config (key, value) VALUES ('retention_policy', '{"logs":"30d","sql_captures":"14d"}')`)
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}

	result, err := handleGetRetention(context.Background(), deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["logs"] != "30d" {
		t.Errorf("logs = %v, want 30d", resp["logs"])
	}
}

func TestUpdateRetention(t *testing.T) {
	deps := setupDeepCaptureDB(t)

	args := map[string]any{
		"action": "update_retention",
		"config": map[string]any{"logs": "90d", "sql_captures": "30d"},
	}
	result, err := handleUpdateRetention(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}

	var resp map[string]any
	text := extractText(t, result)
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["status"] != "updated" {
		t.Errorf("status = %v, want updated", resp["status"])
	}
}

// --- Handler dispatch integration ---

func TestDeepCaptureHandler_DispatchesCorrectly(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	handler := DeepCaptureHandler(deps)

	// Test that the handler dispatches to each action without panicking.
	actions := []struct {
		name string
		args map[string]any
	}{
		{"request_capture", map[string]any{"action": "request_capture", "log_id": float64(1)}},
		{"sql_captures", map[string]any{"action": "sql_captures", "log_id": float64(1)}},
		{"http_captures", map[string]any{"action": "http_captures", "log_id": float64(1)}},
		{"email_captures", map[string]any{"action": "email_captures", "log_id": float64(1)}},
		{"audit_trail", map[string]any{"action": "audit_trail", "record_type": "User", "record_id": "1"}},
		{"search_audit", map[string]any{"action": "search_audit", "actor_id": "user-1"}},
		{"search_sql", map[string]any{"action": "search_sql", "fingerprint": "fp-test"}},
		{"file_captures", map[string]any{"action": "file_captures", "log_id": float64(1)}},
		{"get_pii_config", map[string]any{"action": "get_pii_config"}},
		{"get_retention", map[string]any{"action": "get_retention"}},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			req := MakeCallToolRequest("deep_capture", tc.args)
			result, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result == nil {
				t.Fatal("expected non-nil result")
			}
			// These should not panic and should return some result (error or data).
		})
	}
}
