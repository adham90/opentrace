package tools

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// --- Admin gate on the config-mutating actions ---

func TestDeepCapture_WriteActionsRequireAdmin(t *testing.T) {
	deps := setupDeepCaptureDB(t) // IsAdmin defaults to false — a member.
	handler := DeepCaptureHandler(deps)

	cases := []struct {
		action string
		key    string
	}{
		{"update_pii_config", "pii_scrubbing"},
		{"update_retention", "retention_policy"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			req := MakeCallToolRequest("deep_capture", map[string]any{
				"action": tc.action,
				"config": map[string]any{"enabled": false},
			})
			result, err := handler(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected a member to be denied %s", tc.action)
			}
			if txt := extractText(t, result); !strings.Contains(txt, "admin") {
				t.Errorf("expected an admin-required message, got: %s", txt)
			}

			// Nothing may have been written.
			var count int
			if err := deps.DB.QueryRow(
				"SELECT count(*) FROM app_config WHERE key = ?", tc.key).Scan(&count); err != nil {
				t.Fatalf("count app_config: %v", err)
			}
			if count != 0 {
				t.Fatalf("member write reached app_config for key %s", tc.key)
			}
		})
	}
}

func TestDeepCapture_AdminMayUpdatePIIConfig(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	deps.IsAdmin = true

	result, err := handleUpdatePIIConfig(context.Background(), deps, map[string]any{
		"config": map[string]any{"enabled": true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("admin update should succeed: %s", extractText(t, result))
	}
}

// --- Env-scope authorization ---

// seedEnvCapture inserts a log row plus a request/sql/audit capture and returns
// the log id. The env lives on the LogStore mock, mirroring production where
// capture tables carry no environment column.
func seedEnvCapture(t *testing.T, db *sql.DB, logs *mocks.LogStore, env string) int64 {
	t.Helper()
	logID := seedTestLog(t, db)

	logs.Entries = append(logs.Entries, store.LogEntry{
		ID:          logID,
		Environment: env,
		Timestamp:   time.Now(),
	})

	if _, err := db.Exec(
		`INSERT INTO request_captures (log_id, cookies, session_data, ip_address)
		 VALUES (?, 'session=secret', '{"user_id":1}', '10.0.0.1')`, logID); err != nil {
		t.Fatalf("insert request_capture: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sql_captures (log_id, raw_sql, bind_values, fingerprint, duration_ms)
		 VALUES (?, 'SELECT * FROM users WHERE id = $1', '[1]', 'fp-1', 5.0)`, logID); err != nil {
		t.Fatalf("insert sql_capture: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO audit_captures (log_id, record_type, record_id, action, actor_id)
		 VALUES (?, 'User', '1', 'update', 'actor-1')`, logID); err != nil {
		t.Fatalf("insert audit_capture: %v", err)
	}
	return logID
}

func stagingCtx() context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})
}

func TestDeepCapture_RequestCaptureDeniesCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	prodLog := seedEnvCapture(t, deps.DB, logs, "production")

	result, err := handleGetRequestCapture(stagingCtx(), deps, map[string]any{
		"log_id": float64(prodLog),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(t, result)
	if strings.Contains(text, "session=secret") || strings.Contains(text, "10.0.0.1") {
		t.Fatalf("staging token read production capture data:\n%s", text)
	}
}

func TestDeepCapture_RequestCaptureAllowsInScope(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	stagingLog := seedEnvCapture(t, deps.DB, logs, "staging")

	result, err := handleGetRequestCapture(stagingCtx(), deps, map[string]any{
		"log_id": float64(stagingLog),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("in-scope read should succeed: %s", extractText(t, result))
	}
	if !strings.Contains(extractText(t, result), "10.0.0.1") {
		t.Errorf("expected the in-scope capture to be returned, got: %s", extractText(t, result))
	}
}

func TestDeepCapture_SQLCapturesDenyCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	prodLog := seedEnvCapture(t, deps.DB, logs, "production")

	result, err := handleGetSQLCaptures(stagingCtx(), deps, map[string]any{
		"log_id": float64(prodLog),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(extractText(t, result), "FROM users") {
		t.Fatalf("staging token read production SQL captures:\n%s", extractText(t, result))
	}
}

func TestDeepCapture_SearchSQLFiltersCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	seedEnvCapture(t, deps.DB, logs, "production")
	seedEnvCapture(t, deps.DB, logs, "staging")

	result, err := handleSearchSQL(stagingCtx(), deps, map[string]any{
		"fingerprint": "fp-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := extractText(t, result)
	// Exactly one of the two matching rows (the staging one) may come back.
	if strings.Count(text, `"fingerprint": "fp-1"`)+strings.Count(text, `"fingerprint":"fp-1"`) != 1 {
		t.Fatalf("expected only the staging row to survive filtering:\n%s", text)
	}
}

func TestDeepCapture_SearchAuditFiltersCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	seedEnvCapture(t, deps.DB, logs, "production")

	result, err := handleSearchAudit(stagingCtx(), deps, map[string]any{
		"actor_id": "actor-1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(extractText(t, result), "actor-1") {
		t.Fatalf("staging token read a production audit row:\n%s", extractText(t, result))
	}
}

// Without a LogStore the environment of a capture cannot be established, so a
// scoped token must be denied rather than served unclassified data.
func TestDeepCapture_FailsClosedWithoutLogStore(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	logID := seedEnvCapture(t, deps.DB, logs, "staging")
	deps.LogStore = nil

	result, err := handleGetRequestCapture(stagingCtx(), deps, map[string]any{
		"log_id": float64(logID),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(extractText(t, result), "10.0.0.1") {
		t.Fatalf("capture served without any way to verify its environment:\n%s", extractText(t, result))
	}
}

// Handlers invoked without the MCP middleware (no scope attached) keep working
// — the same fallback scopeAllowsEnv and ResolveEnv use.
func TestDeepCapture_UnscopedContextUnaffected(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logs := mocks.NewLogStore()
	deps.LogStore = logs
	logID := seedEnvCapture(t, deps.DB, logs, "production")

	result, err := handleGetRequestCapture(context.Background(), deps, map[string]any{
		"log_id": float64(logID),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(extractText(t, result), "10.0.0.1") {
		t.Errorf("unscoped caller should still read captures, got: %s", extractText(t, result))
	}
}
