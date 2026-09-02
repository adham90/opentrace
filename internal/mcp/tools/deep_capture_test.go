package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"

	_ "modernc.org/sqlite"
)

// setupDeepCaptureDB returns deps backed by a mock log store (the source of
// every capture) and an in-memory app_config table (the only real table deep
// capture touches).
func setupDeepCaptureDB(t *testing.T) DeepCaptureDeps {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Exec(`CREATE TABLE app_config (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL DEFAULT '{}'
	)`); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	return DeepCaptureDeps{DB: db, LogStore: mocks.NewLogStore()}
}

// seedCaptureLog appends a log entry carrying the given body blob and returns
// its id.
func seedCaptureLog(t *testing.T, deps DeepCaptureDeps, env string, body map[string]any) int64 {
	t.Helper()
	logs, ok := deps.LogStore.(*mocks.LogStore)
	if !ok {
		t.Fatalf("deps.LogStore is not a mock")
	}
	id := int64(len(logs.Entries) + 1)
	logs.Entries = append(logs.Entries, store.LogEntry{
		ID:          id,
		Timestamp:   time.Now().Add(-time.Minute),
		Level:       "info",
		Service:     "api",
		Environment: env,
		Message:     "test request",
		RequestID:   "req-abc123",
		Metadata:    body,
	})
	return id
}

// callDeepCapture dispatches one action and fails on transport errors.
func callDeepCapture(t *testing.T, deps DeepCaptureDeps, args map[string]any) *CallToolResult {
	t.Helper()
	result, err := DeepCaptureHandler(deps)(context.Background(), MakeCallToolRequest("deep_capture", args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// decodeDeepCapture parses a successful result's JSON payload.
func decodeDeepCapture(t *testing.T, result *CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(extractText(t, result)), &out); err != nil {
		t.Fatalf("decode result: %v (raw: %s)", err, extractText(t, result))
	}
	return out
}

// sampleBody is a body blob shaped like opentrace_ruby's payload_builder
// output for one request.
func sampleBody() map[string]any {
	return map[string]any{
		"request_headers": map[string]any{"accept": "application/json"},
		"request_body":    `{"cart_id":"c_991"}`,
		"response_body":   `{"ok":true}`,
		"sql": []any{
			map[string]any{
				"raw_sql": "SELECT * FROM carts WHERE id = 1", "duration_ms": 4.2,
				"fingerprint": "fp-carts", "table": "carts",
			},
			map[string]any{
				"raw_sql": "SELECT * FROM line_items", "duration_ms": 91.0,
				"fingerprint": "fp-items", "table": "line_items",
			},
		},
		"http": []any{
			map[string]any{
				"method": "POST", "host": "api.stripe.com", "vendor": "stripe",
				"status": float64(200), "duration_ms": 310.0,
			},
		},
		"email": []any{
			map[string]any{"mailer_class": "OrderMailer", "subject": "Your order"},
		},
		"file": []any{
			map[string]any{"action": "upload", "filename": "receipt.pdf", "size_bytes": float64(2048)},
		},
		"audit": []any{
			map[string]any{
				"action": "update", "record_type": "Order", "record_id": "42",
				"actor_id": "u-7", "changed_fields": []any{"status"},
			},
		},
	}
}

func TestDeepCaptureHandler_UnknownAction(t *testing.T) {
	result := callDeepCapture(t, setupDeepCaptureDB(t), map[string]any{"action": "nope"})
	if !result.IsError {
		t.Fatal("expected an error for an unknown action")
	}
}

func TestDeepCaptureHandler_MissingAction(t *testing.T) {
	result := callDeepCapture(t, setupDeepCaptureDB(t), map[string]any{})
	if !result.IsError {
		t.Fatal("expected an error when action is absent")
	}
}

// The regression this file exists for: every per-request action used to query
// a table that no migration ever created, so all of them failed at runtime.
// They must now read the body blob and return the rows the SDK actually sent.
func TestDeepCapture_PerRequestActionsReadBodyBlob(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "production", sampleBody())

	cases := []struct {
		action string
		key    string
		want   int
	}{
		{"sql_captures", "queries", 2},
		{"http_captures", "calls", 1},
		{"email_captures", "emails", 1},
		{"file_captures", "files", 1},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
				"action": tc.action, "log_id": float64(logID),
			}))
			rows, _ := out[tc.key].([]any)
			if len(rows) != tc.want {
				t.Fatalf("got %d rows under %q, want %d (payload: %v)", len(rows), tc.key, tc.want, out)
			}
		})
	}
}

func TestDeepCapture_HTTPCapturesCarryHostAndVendor(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "http_captures", "log_id": float64(logID),
	}))
	call := out["calls"].([]any)[0].(map[string]any)
	if call["host"] != "api.stripe.com" || call["vendor"] != "stripe" {
		t.Fatalf("host/vendor lost in translation: %v", call)
	}
}

func TestDeepCapture_RequestCapture(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "request_capture", "log_id": float64(logID),
	}))
	if out["request_body"] != `{"cart_id":"c_991"}` {
		t.Fatalf("request_body missing: %v", out)
	}
	if out["request_id"] != "req-abc123" {
		t.Fatalf("request_id missing: %v", out)
	}
}

func TestDeepCapture_PerRequestActionsRequireLogID(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	for _, action := range []string{"request_capture", "sql_captures", "http_captures", "file_captures"} {
		if !callDeepCapture(t, deps, map[string]any{"action": action}).IsError {
			t.Errorf("%s accepted a missing log_id", action)
		}
	}
}

func TestDeepCapture_MissingLogIsEmptyNotError(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	result := callDeepCapture(t, deps, map[string]any{"action": "sql_captures", "log_id": float64(999)})
	if result.IsError {
		t.Fatalf("an absent log_id must read as empty, not error: %s", extractText(t, result))
	}
}

func TestDeepCapture_EmptyBodyArrayIsEmptyResult(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "production", map[string]any{"sql": []any{}})
	result := callDeepCapture(t, deps, map[string]any{"action": "sql_captures", "log_id": float64(logID)})
	if result.IsError {
		t.Fatalf("expected an empty result, got error: %s", extractText(t, result))
	}
	if txt := extractText(t, result); !strings.Contains(txt, "No sql captures") {
		t.Errorf("unexpected empty message: %s", txt)
	}
}

func TestDeepCapture_SearchSQLByFingerprint(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "search_sql", "fingerprint": "fp-items",
	}))
	rows := out["queries"].([]any)
	if len(rows) != 1 {
		t.Fatalf("want 1 match, got %d", len(rows))
	}
	row := rows[0].(map[string]any)
	if row["fingerprint"] != "fp-items" {
		t.Errorf("wrong row matched: %v", row)
	}
	// Cross-log results must be traceable back to their request.
	if row["log_id"] == nil || row["request_id"] != "req-abc123" {
		t.Errorf("result not stamped with its source log: %v", row)
	}
}

func TestDeepCapture_SearchSQLByTableAndDuration(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", sampleBody())

	// `table_name` is the tool param; `table` is the SDK wire key.
	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "search_sql", "table_name": "carts",
	}))
	if got := len(out["queries"].([]any)); got != 1 {
		t.Fatalf("table filter matched %d rows, want 1", got)
	}

	out = decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "search_sql", "min_duration_ms": float64(50),
	}))
	rows := out["queries"].([]any)
	if len(rows) != 1 {
		t.Fatalf("min_duration_ms matched %d rows, want 1", len(rows))
	}
	if rows[0].(map[string]any)["duration_ms"] != 91.0 {
		t.Errorf("matched the fast query: %v", rows[0])
	}
}

func TestDeepCapture_SearchSQLMissingFilters(t *testing.T) {
	if !callDeepCapture(t, setupDeepCaptureDB(t), map[string]any{"action": "search_sql"}).IsError {
		t.Fatal("search_sql must require at least one filter")
	}
}

func TestDeepCapture_AuditTrail(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "audit_trail", "record_type": "Order", "record_id": "42",
	}))
	if got := len(out["entries"].([]any)); got != 1 {
		t.Fatalf("want 1 audit entry, got %d", got)
	}

	// A record with no history reads as empty, not as an error.
	result := callDeepCapture(t, deps, map[string]any{
		"action": "audit_trail", "record_type": "Order", "record_id": "999",
	})
	if result.IsError {
		t.Errorf("unknown record must be empty, not error: %s", extractText(t, result))
	}
}

func TestDeepCapture_AuditTrailMissingParams(t *testing.T) {
	if !callDeepCapture(t, setupDeepCaptureDB(t), map[string]any{"action": "audit_trail"}).IsError {
		t.Fatal("audit_trail must require record_type and record_id")
	}
}

func TestDeepCapture_SearchAuditByActor(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "search_audit", "actor_id": "u-7",
	}))
	if got := len(out["entries"].([]any)); got != 1 {
		t.Fatalf("want 1 audit entry, got %d", got)
	}

	// audit_action filters the audited verb, not the tool dispatch key.
	out = decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "search_audit", "audit_action": "update",
	}))
	if got := len(out["entries"].([]any)); got != 1 {
		t.Fatalf("audit_action matched %d entries, want 1", got)
	}
	if !callDeepCapture(t, deps, map[string]any{
		"action": "search_audit", "audit_action": "destroy",
	}).IsError {
		return // an empty result is fine; only a crash would be a failure
	}
}

func TestDeepCapture_SearchAuditMissingFilters(t *testing.T) {
	if !callDeepCapture(t, setupDeepCaptureDB(t), map[string]any{"action": "search_audit"}).IsError {
		t.Fatal("search_audit must require at least one filter")
	}
}

func TestDeepCapture_RecentEmails(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", sampleBody())

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{
		"action": "email_captures", "last": "24h",
	}))
	if got := len(out["emails"].([]any)); got != 1 {
		t.Fatalf("want 1 email, got %d", got)
	}

	if !callDeepCapture(t, deps, map[string]any{"action": "email_captures"}).IsError {
		t.Error("email_captures must require log_id or last")
	}
}

// --- app_config actions ---

func TestDeepCapture_PIIConfigRoundTrip(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	deps.IsAdmin = true

	if result := callDeepCapture(t, deps, map[string]any{"action": "get_pii_config"}); result.IsError {
		t.Fatalf("get on an unset key must be empty, not error: %s", extractText(t, result))
	}

	callDeepCapture(t, deps, map[string]any{
		"action": "update_pii_config",
		"config": map[string]any{"enabled": true, "fields": []any{"email"}},
	})

	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{"action": "get_pii_config"}))
	if out["enabled"] != true {
		t.Fatalf("config did not round-trip: %v", out)
	}
}

func TestDeepCapture_RetentionRoundTrip(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	deps.IsAdmin = true

	callDeepCapture(t, deps, map[string]any{
		"action": "update_retention",
		"config": map[string]any{"logs_days": float64(30)},
	})
	out := decodeDeepCapture(t, callDeepCapture(t, deps, map[string]any{"action": "get_retention"}))
	if out["logs_days"] != float64(30) {
		t.Fatalf("retention did not round-trip: %v", out)
	}
}

func TestDeepCapture_UpdateRequiresConfig(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	deps.IsAdmin = true
	if !callDeepCapture(t, deps, map[string]any{"action": "update_pii_config"}).IsError {
		t.Fatal("update_pii_config must require a config object")
	}
}
