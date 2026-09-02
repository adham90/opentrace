package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/mcp/envscope"
)

// --- Admin gate on the config-mutating actions ---

func TestDeepCapture_WriteActionsRequireAdmin(t *testing.T) {
	deps := setupDeepCaptureDB(t) // IsAdmin defaults to false — a member.

	for _, tc := range []struct{ action, key string }{
		{"update_pii_config", "pii_scrubbing"},
		{"update_retention", "retention_policy"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			result := callDeepCapture(t, deps, map[string]any{
				"action": tc.action,
				"config": map[string]any{"enabled": false},
			})
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

	result := callDeepCapture(t, deps, map[string]any{
		"action": "update_pii_config",
		"config": map[string]any{"enabled": true},
	})
	if result.IsError {
		t.Fatalf("admin update should succeed: %s", extractText(t, result))
	}
}

// --- Env-scope authorization ---

// secretBody is a body blob whose every array carries a value a cross-env
// caller must never see.
func secretBody() map[string]any {
	return map[string]any{
		"request_body": "session=secret from 10.0.0.1",
		"sql": []any{
			map[string]any{
				"raw_sql": "SELECT * FROM users WHERE id = $1",
				"binds":   []any{float64(1)}, "fingerprint": "fp-1", "duration_ms": 5.0,
			},
		},
		"audit": []any{
			map[string]any{"record_type": "User", "record_id": "1", "action": "update", "actor_id": "actor-1"},
		},
		"email": []any{map[string]any{"subject": "secret receipt"}},
	}
}

func stagingCtx() context.Context {
	return envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"staging"}})
}

// callScoped dispatches an action under the given context.
func callScoped(t *testing.T, ctx context.Context, deps DeepCaptureDeps, args map[string]any) *CallToolResult {
	t.Helper()
	result, err := DeepCaptureHandler(deps)(ctx, MakeCallToolRequest("deep_capture", args))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return result
}

// Every per-request read resolves the log's environment before returning body
// data. A staging token asking for a production log_id gets the same
// not-found answer as for a log that does not exist.
func TestDeepCapture_PerRequestReadsDenyCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	prodLog := seedCaptureLog(t, deps, "production", secretBody())

	for _, action := range []string{"request_capture", "sql_captures", "email_captures"} {
		t.Run(action, func(t *testing.T) {
			text := extractText(t, callScoped(t, stagingCtx(), deps, map[string]any{
				"action": action, "log_id": float64(prodLog),
			}))
			for _, secret := range []string{"session=secret", "FROM users", "secret receipt"} {
				if strings.Contains(text, secret) {
					t.Fatalf("staging token read production data via %s:\n%s", action, text)
				}
			}
		})
	}
}

func TestDeepCapture_RequestCaptureAllowsInScope(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	stagingLog := seedCaptureLog(t, deps, "staging", secretBody())

	result := callScoped(t, stagingCtx(), deps, map[string]any{
		"action": "request_capture", "log_id": float64(stagingLog),
	})
	if result.IsError {
		t.Fatalf("in-scope read should succeed: %s", extractText(t, result))
	}
	if !strings.Contains(extractText(t, result), "session=secret") {
		t.Errorf("expected the in-scope capture to be returned, got: %s", extractText(t, result))
	}
}

// Cross-log searches filter every row by the environment of the log it came
// from, not just by the store-level env filter.
func TestDeepCapture_SearchSQLFiltersCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", secretBody())
	seedCaptureLog(t, deps, "staging", secretBody())

	text := extractText(t, callScoped(t, stagingCtx(), deps, map[string]any{
		"action": "search_sql", "fingerprint": "fp-1",
	}))
	// Exactly one of the two matching rows (the staging one) may come back.
	if got := strings.Count(text, `"fp-1"`); got != 1 {
		t.Fatalf("expected only the staging row to survive filtering (got %d):\n%s", got, text)
	}
}

func TestDeepCapture_SearchAuditFiltersCrossEnv(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	seedCaptureLog(t, deps, "production", secretBody())

	text := extractText(t, callScoped(t, stagingCtx(), deps, map[string]any{
		"action": "search_audit", "actor_id": "actor-1",
	}))
	if strings.Contains(text, "actor-1") {
		t.Fatalf("staging token read a production audit row:\n%s", text)
	}
}

// Without a LogStore there is no capture data at all, and nothing may be
// served from an unverified source.
func TestDeepCapture_FailsClosedWithoutLogStore(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "staging", secretBody())
	deps.LogStore = nil

	text := extractText(t, callScoped(t, stagingCtx(), deps, map[string]any{
		"action": "request_capture", "log_id": float64(logID),
	}))
	if strings.Contains(text, "session=secret") {
		t.Fatalf("capture served without any way to verify its environment:\n%s", text)
	}
}

// Handlers invoked without the MCP middleware (no scope attached) keep working
// — the same fallback scopeAllowsEnv and ResolveEnv use.
func TestDeepCapture_UnscopedContextUnaffected(t *testing.T) {
	deps := setupDeepCaptureDB(t)
	logID := seedCaptureLog(t, deps, "production", secretBody())

	result := callScoped(t, context.Background(), deps, map[string]any{
		"action": "request_capture", "log_id": float64(logID),
	})
	if !strings.Contains(extractText(t, result), "session=secret") {
		t.Errorf("unscoped caller should still read captures, got: %s", extractText(t, result))
	}
}
