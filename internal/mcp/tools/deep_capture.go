package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Deep capture reads the per-request detail the SDKs pack into the log `body`
// blob, which the ingest path stores whole as LogEntry.Metadata. There are no
// `*_captures` tables — an earlier version of this tool queried five of them
// and every one of those actions failed at runtime because the DDL never
// existed. The body blob is the only place this data has ever lived.
//
// Wire keys (opentrace_ruby's payload_builder): body.sql, body.http,
// body.email, body.file, body.audit, plus the request_*/response_* pairs.
const (
	// captureScanLogs caps how many log entries a cross-log search decodes.
	// These searches have no index — they scan recent requests and filter the
	// embedded arrays in Go.
	// ponytail: linear scan over a bounded window. If this gets slow, promote
	// the searched fields to sparse columns rather than adding child tables.
	captureScanLogs = 2000
	// captureMaxResults caps rows returned by a cross-log search.
	captureMaxResults = 100
	// captureDefaultWindow is the lookback when no `last` is supplied.
	captureDefaultWindow = 24 * time.Hour
)

// bodyKeys maps a per-request action to the body blob key it reads and the
// name of the array in the response.
var bodyKeys = map[string]struct{ key, out string }{
	"sql_captures":   {"sql", "queries"},
	"http_captures":  {"http", "calls"},
	"email_captures": {"email", "emails"},
	"file_captures":  {"file", "files"},
}

// DeepCaptureDeps holds what the deep capture actions need.
type DeepCaptureDeps struct {
	// LogStore is the source of every capture: the SDK detail lives in the
	// log entry's body blob. It also carries the environment used to enforce
	// the caller's env scope.
	LogStore store.LogStore
	// DB backs the PII/retention config actions only (app_config).
	DB *sql.DB
	// IsAdmin reports whether the MCP server this tool is registered on serves
	// an admin token. The config-mutating actions (update_pii_config,
	// update_retention) require it — a member must not be able to disable PII
	// scrubbing or shorten retention globally.
	IsAdmin bool
}

// requireDeepCaptureAdmin returns an error result when the caller is not an
// admin. Write actions fail closed.
func requireDeepCaptureAdmin(deps DeepCaptureDeps, action string) *CallToolResult {
	if deps.IsAdmin {
		return nil
	}
	return NewToolResultError(fmt.Sprintf(
		"admin privileges are required for deep_capture action %q", action))
}

// DeepCaptureHandler returns a handler that dispatches to deep capture actions.
func DeepCaptureHandler(deps DeepCaptureDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		if spec, ok := bodyKeys[action]; ok {
			// email_captures alone may be asked for a time window instead of
			// a single request.
			if action == "email_captures" && ArgInt(args, "log_id", 0, 1<<31) == 0 {
				return handleRecentEmails(ctx, deps, args)
			}
			return handleBodyCaptures(ctx, deps, args, spec.key, spec.out)
		}

		switch action {
		case "request_capture":
			return handleRequestCapture(ctx, deps, args)
		case "audit_trail":
			return handleAuditTrail(ctx, deps, args)
		case "search_audit":
			return handleSearchAudit(ctx, deps, args)
		case "search_sql":
			return handleSearchSQL(ctx, deps, args)
		case "get_pii_config":
			return handleGetConfig(ctx, deps, "pii_scrubbing")
		case "update_pii_config":
			return handleUpdateConfig(ctx, deps, args, "update_pii_config", "pii_scrubbing")
		case "get_retention":
			return handleGetConfig(ctx, deps, "retention_policy")
		case "update_retention":
			return handleUpdateConfig(ctx, deps, args, "update_retention", "retention_policy")
		default:
			return NewToolResultError(
				"action is required and must be one of: request_capture, sql_captures, http_captures, " +
					"email_captures, audit_trail, search_audit, search_sql, file_captures, " +
					"get_pii_config, update_pii_config, get_retention, update_retention",
			), nil
		}
	}
}

// ---------------------------------------------------------------------------
// Body blob access
// ---------------------------------------------------------------------------

// loadCaptureLog fetches a log entry for capture reading and enforces the
// caller's env scope. A log the caller may not read is reported as missing so
// a cross-env log_id cannot be probed for existence.
func loadCaptureLog(ctx context.Context, deps DeepCaptureDeps, logID int64) (*store.LogEntry, *CallToolResult) {
	if deps.LogStore == nil {
		return nil, NewToolResultError("log store not available")
	}
	entry, err := deps.LogStore.GetByID(ctx, logID)
	if err != nil || entry == nil || !scopeAllowsEnv(ctx, entry.Environment) {
		res, _ := EmptyResult(fmt.Sprintf("No capture found for log_id %d", logID))
		return nil, res
	}
	return entry, nil
}

// bodyRows returns the body blob array under key as a slice of objects,
// skipping anything that is not an object. A missing key yields nil.
func bodyRows(entry *store.LogEntry, key string) []map[string]any {
	arr, _ := entry.Metadata[key].([]any)
	if len(arr) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(arr))
	for _, v := range arr {
		if m, ok := v.(map[string]any); ok {
			rows = append(rows, m)
		}
	}
	return rows
}

// withSource copies a capture row and stamps it with the log it came from, so
// a row pulled out of a cross-log search can be traced back.
func withSource(row map[string]any, entry *store.LogEntry) map[string]any {
	out := make(map[string]any, len(row)+3)
	for k, v := range row {
		out[k] = v
	}
	out["log_id"] = entry.ID
	out["timestamp"] = entry.Timestamp.Format(time.RFC3339)
	if entry.RequestID != "" {
		out["request_id"] = entry.RequestID
	}
	return out
}

// handleBodyCaptures returns one capture array for a single request.
func handleBodyCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any, key, out string) (*CallToolResult, error) {
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}
	entry, errRes := loadCaptureLog(ctx, deps, int64(logID))
	if errRes != nil {
		return errRes, nil
	}

	rows := bodyRows(entry, key)
	if len(rows) == 0 {
		return EmptyResult(fmt.Sprintf("No %s captures found for log_id %d", key, logID))
	}
	return JSONResult(map[string]any{
		"log_id": logID,
		"total":  len(rows),
		out:      rows,
	})
}

// requestCaptureKeys are the body blob fields that describe the inbound
// request and its response.
var requestCaptureKeys = []string{
	"request_headers", "request_params", "request_body",
	"response_headers", "response_body",
	"performance", "timeline", "context", "params",
}

// handleRequestCapture returns the inbound request/response detail for a log.
func handleRequestCapture(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}
	entry, errRes := loadCaptureLog(ctx, deps, int64(logID))
	if errRes != nil {
		return errRes, nil
	}

	result := map[string]any{
		"log_id":     entry.ID,
		"timestamp":  entry.Timestamp.Format(time.RFC3339),
		"method":     "",
		"path":       entry.Route,
		"status":     entry.Status,
		"user_id":    entry.UserID,
		"request_id": entry.RequestID,
	}
	if s := entry.RequestSummary; s != nil {
		result["method"] = s.Method
		result["path"] = s.Path
		result["status"] = s.Status
		result["duration_ms"] = s.DurationMs
		result["controller"] = s.Controller
		result["action"] = s.Action
	}
	found := false
	for _, k := range requestCaptureKeys {
		if v, ok := entry.Metadata[k]; ok && v != nil {
			result[k] = v
			found = true
		}
	}
	if !found && entry.RequestSummary == nil {
		return EmptyResult(fmt.Sprintf("No request capture found for log_id %d", logID))
	}

	return JSONResult(result,
		Suggest("deep_capture", "View SQL queries for this request", map[string]any{
			"action": "sql_captures", "log_id": float64(logID),
		}),
		Suggest("deep_capture", "View HTTP calls for this request", map[string]any{
			"action": "http_captures", "log_id": float64(logID),
		}),
	)
}

// ---------------------------------------------------------------------------
// Cross-log search
// ---------------------------------------------------------------------------

// scanCaptures walks recent logs in the caller's env scope and applies keep to
// every row of the named body array, collecting matches until limit is hit.
func scanCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any, key string, limit int, keep func(map[string]any) bool) ([]map[string]any, error) {
	if deps.LogStore == nil {
		return nil, fmt.Errorf("log store not available")
	}
	env, err := ResolveEnv(ctx, args)
	if err != nil {
		return nil, err
	}
	since := ParseSinceOr(ArgString(args, "last"), captureDefaultWindow)
	entries, err := deps.LogStore.Search(ctx, store.LogSearchParams{
		Environment: env,
		Start:       &since,
		Limit:       captureScanLogs,
	})
	if err != nil {
		return nil, err
	}

	var matches []map[string]any
	for i := range entries {
		e := &entries[i]
		if !scopeAllowsEnv(ctx, e.Environment) {
			continue
		}
		for _, row := range bodyRows(e, key) {
			if !keep(row) {
				continue
			}
			matches = append(matches, withSource(row, e))
			if len(matches) >= limit {
				return matches, nil
			}
		}
	}
	return matches, nil
}

// rowString reads a string field from a capture row, tolerating the numeric
// ids JSON decoding produces.
func rowString(row map[string]any, key string) string {
	switch v := row[key].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprintf("%.0f", v)
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// rowFloat reads a numeric field from a capture row.
func rowFloat(row map[string]any, key string) float64 {
	f, _ := row[key].(float64)
	return f
}

// handleAuditTrail returns audit history for one record.
func handleAuditTrail(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	recordType := ArgString(args, "record_type")
	recordID := ArgString(args, "record_id")
	if recordType == "" || recordID == "" {
		return NewToolResultError("record_type and record_id are required"), nil
	}

	entries, err := scanCaptures(ctx, deps, args, "audit", captureMaxResults, func(row map[string]any) bool {
		return rowString(row, "record_type") == recordType && rowString(row, "record_id") == recordID
	})
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if len(entries) == 0 {
		return EmptyResult(fmt.Sprintf("No audit captures found for %s/%s", recordType, recordID))
	}
	return JSONResult(map[string]any{
		"record_type": recordType,
		"record_id":   recordID,
		"total":       len(entries),
		"entries":     entries,
	})
}

// handleSearchAudit searches the audit trail across records.
func handleSearchAudit(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	actorID := ArgString(args, "actor_id")
	// `action` is the tool's own dispatch key, so the audit action filter is
	// read from audit_action to avoid colliding with it.
	auditAction := ArgString(args, "audit_action")
	last := ArgString(args, "last")
	if actorID == "" && auditAction == "" && last == "" {
		return NewToolResultError("at least one filter is required: actor_id, audit_action, or last"), nil
	}

	entries, err := scanCaptures(ctx, deps, args, "audit", captureMaxResults, func(row map[string]any) bool {
		if actorID != "" && rowString(row, "actor_id") != actorID {
			return false
		}
		if auditAction != "" && rowString(row, "action") != auditAction {
			return false
		}
		return true
	})
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if len(entries) == 0 {
		return EmptyResult("No audit captures found matching filters")
	}
	return JSONResult(map[string]any{"total": len(entries), "entries": entries})
}

// handleSearchSQL searches SQL captures by fingerprint, table, or duration.
func handleSearchSQL(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	fingerprint := ArgString(args, "fingerprint")
	tableName := ArgString(args, "table_name")
	minDurationMs := ArgFloat(args, "min_duration_ms", 0)
	last := ArgString(args, "last")
	if fingerprint == "" && tableName == "" && minDurationMs == 0 && last == "" {
		return NewToolResultError("at least one filter is required: fingerprint, table_name, min_duration_ms, or last"), nil
	}
	limit := ArgInt(args, "limit", 50, captureMaxResults)

	queries, err := scanCaptures(ctx, deps, args, "sql", limit, func(row map[string]any) bool {
		if fingerprint != "" && rowString(row, "fingerprint") != fingerprint {
			return false
		}
		// The SDK wire key is `table`; the tool param stays `table_name`.
		if tableName != "" && rowString(row, "table") != tableName {
			return false
		}
		return rowFloat(row, "duration_ms") >= minDurationMs
	})
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if len(queries) == 0 {
		return EmptyResult("No SQL captures found matching filters")
	}
	return JSONResult(map[string]any{"total": len(queries), "queries": queries})
}

// handleRecentEmails returns emails sent across a time window.
func handleRecentEmails(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if ArgString(args, "last") == "" {
		return NewToolResultError("log_id or last is required"), nil
	}
	emails, err := scanCaptures(ctx, deps, args, "email", captureMaxResults, func(map[string]any) bool { return true })
	if err != nil {
		return NewToolResultError(err.Error()), nil
	}
	if len(emails) == 0 {
		return EmptyResult("No email captures found")
	}
	return JSONResult(map[string]any{"total": len(emails), "emails": emails})
}

// ---------------------------------------------------------------------------
// PII / retention config (app_config)
// ---------------------------------------------------------------------------

// handleGetConfig reads a JSON config blob out of app_config.
func handleGetConfig(ctx context.Context, deps DeepCaptureDeps, key string) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	var value string
	err := deps.DB.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return EmptyResult("No " + strings.ReplaceAll(key, "_", " ") + " configuration found")
	}
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return NewToolResultError(fmt.Sprintf("invalid JSON in %s config: %v", key, err)), nil
	}
	return JSONResult(config)
}

// handleUpdateConfig writes a JSON config blob into app_config. Admin only.
func handleUpdateConfig(ctx context.Context, deps DeepCaptureDeps, args map[string]any, action, key string) (*CallToolResult, error) {
	if errResult := requireDeepCaptureAdmin(deps, action); errResult != nil {
		return errResult, nil
	}
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	config, ok := args["config"]
	if !ok {
		return NewToolResultError("config is required (JSON object)"), nil
	}
	data, err := json.Marshal(config)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid config: %v", err)), nil
	}
	_, err = deps.DB.ExecContext(ctx,
		"INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, string(data))
	if err != nil {
		return NewToolResultError(fmt.Sprintf("update failed: %v", err)), nil
	}
	return JSONResult(map[string]any{"status": "updated", "config": config})
}
