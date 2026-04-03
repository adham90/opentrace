package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DeepCaptureDeps holds the database connection for deep capture queries.
// These tables have no formal store interface — we query SQLite directly.
type DeepCaptureDeps struct {
	DB *sql.DB
}

// DeepCaptureHandler returns a handler that dispatches to deep capture actions.
func DeepCaptureHandler(deps DeepCaptureDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "request_capture":
			return handleGetRequestCapture(ctx, deps, args)
		case "sql_captures":
			return handleGetSQLCaptures(ctx, deps, args)
		case "http_captures":
			return handleGetHTTPCaptures(ctx, deps, args)
		case "email_captures":
			return handleGetEmailCaptures(ctx, deps, args)
		case "audit_trail":
			return handleGetAuditTrail(ctx, deps, args)
		case "search_audit":
			return handleSearchAudit(ctx, deps, args)
		case "search_sql":
			return handleSearchSQL(ctx, deps, args)
		case "file_captures":
			return handleGetFileCaptures(ctx, deps, args)
		case "get_pii_config":
			return handleGetPIIConfig(ctx, deps)
		case "update_pii_config":
			return handleUpdatePIIConfig(ctx, deps, args)
		case "get_retention":
			return handleGetRetention(ctx, deps)
		case "update_retention":
			return handleUpdateRetention(ctx, deps, args)
		default:
			return NewToolResultError(
				"action is required and must be one of: request_capture, sql_captures, http_captures, " +
					"email_captures, audit_trail, search_audit, search_sql, file_captures, " +
					"get_pii_config, update_pii_config, get_retention, update_retention",
			), nil
		}
	}
}

// handleGetRequestCapture returns full request/response details for a log entry.
func handleGetRequestCapture(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}

	row := deps.DB.QueryRowContext(ctx,
		`SELECT id, log_id, request_headers, request_body, request_size,
		        content_type, ip_address, user_agent, referer, cookies, session_data,
		        response_headers, response_body, response_size, created_at
		 FROM request_captures WHERE log_id = ?`, logID)

	var rc struct {
		ID              int64          `json:"id"`
		LogID           int64          `json:"log_id"`
		RequestHeaders  sql.NullString `json:"-"`
		RequestBody     sql.NullString `json:"-"`
		RequestSize     sql.NullInt64  `json:"-"`
		ContentType     sql.NullString `json:"-"`
		IPAddress       sql.NullString `json:"-"`
		UserAgent       sql.NullString `json:"-"`
		Referer         sql.NullString `json:"-"`
		Cookies         sql.NullString `json:"-"`
		SessionData     sql.NullString `json:"-"`
		ResponseHeaders sql.NullString `json:"-"`
		ResponseBody    sql.NullString `json:"-"`
		ResponseSize    sql.NullInt64  `json:"-"`
		CreatedAt       string         `json:"created_at"`
	}

	err := row.Scan(
		&rc.ID, &rc.LogID, &rc.RequestHeaders, &rc.RequestBody, &rc.RequestSize,
		&rc.ContentType, &rc.IPAddress, &rc.UserAgent, &rc.Referer, &rc.Cookies,
		&rc.SessionData, &rc.ResponseHeaders, &rc.ResponseBody, &rc.ResponseSize,
		&rc.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return EmptyResult("No request capture found for log_id " + fmt.Sprintf("%d", logID))
	}
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}

	result := map[string]any{
		"id":               rc.ID,
		"log_id":           rc.LogID,
		"request_headers":  nullStringToAny(rc.RequestHeaders),
		"request_body":     nullStringToAny(rc.RequestBody),
		"request_size":     nullInt64ToAny(rc.RequestSize),
		"content_type":     nullStringToAny(rc.ContentType),
		"ip_address":       nullStringToAny(rc.IPAddress),
		"user_agent":       nullStringToAny(rc.UserAgent),
		"referer":          nullStringToAny(rc.Referer),
		"cookies":          nullStringToAny(rc.Cookies),
		"session_data":     nullStringToAny(rc.SessionData),
		"response_headers": nullStringToAny(rc.ResponseHeaders),
		"response_body":    nullStringToAny(rc.ResponseBody),
		"response_size":    nullInt64ToAny(rc.ResponseSize),
		"created_at":       rc.CreatedAt,
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

// handleGetSQLCaptures returns all SQL queries for a request.
func handleGetSQLCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}

	rows, err := deps.DB.QueryContext(ctx,
		`SELECT id, log_id, raw_sql, normalized_sql, bind_values, row_count,
		        duration_ms, cached, in_transaction, explain_plan, pool_size,
		        pool_busy, pool_waiting, caller_location, fingerprint, table_name, created_at
		 FROM sql_captures WHERE log_id = ? ORDER BY id`, logID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	var captures []map[string]any
	for rows.Next() {
		var (
			id             int64
			lID            int64
			rawSQL         sql.NullString
			normalizedSQL  sql.NullString
			bindValues     sql.NullString
			rowCount       sql.NullInt64
			durationMs     sql.NullFloat64
			cached         sql.NullInt64
			inTransaction  sql.NullInt64
			explainPlan    sql.NullString
			poolSize       sql.NullInt64
			poolBusy       sql.NullInt64
			poolWaiting    sql.NullInt64
			callerLocation sql.NullString
			fingerprint    sql.NullString
			tableName      sql.NullString
			createdAt      string
		)
		if err := rows.Scan(&id, &lID, &rawSQL, &normalizedSQL, &bindValues, &rowCount,
			&durationMs, &cached, &inTransaction, &explainPlan, &poolSize,
			&poolBusy, &poolWaiting, &callerLocation, &fingerprint, &tableName, &createdAt); err != nil {
			return NewToolResultError(fmt.Sprintf("scan failed: %v", err)), nil
		}
		captures = append(captures, map[string]any{
			"id":              id,
			"log_id":          lID,
			"raw_sql":         nullStringToAny(rawSQL),
			"normalized_sql":  nullStringToAny(normalizedSQL),
			"bind_values":     nullStringToAny(bindValues),
			"row_count":       nullInt64ToAny(rowCount),
			"duration_ms":     nullFloat64ToAny(durationMs),
			"cached":          nullInt64ToBool(cached),
			"in_transaction":  nullInt64ToBool(inTransaction),
			"explain_plan":    nullStringToAny(explainPlan),
			"pool_size":       nullInt64ToAny(poolSize),
			"pool_busy":       nullInt64ToAny(poolBusy),
			"pool_waiting":    nullInt64ToAny(poolWaiting),
			"caller_location": nullStringToAny(callerLocation),
			"fingerprint":     nullStringToAny(fingerprint),
			"table_name":      nullStringToAny(tableName),
			"created_at":      createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No SQL captures found for log_id " + fmt.Sprintf("%d", logID))
	}

	result := map[string]any{
		"log_id":  logID,
		"total":   len(captures),
		"queries": captures,
	}
	return JSONResult(result)
}

// handleGetHTTPCaptures returns external HTTP calls for a request.
func handleGetHTTPCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}

	rows, err := deps.DB.QueryContext(ctx,
		`SELECT id, log_id, method, url, host, vendor, status, duration_ms,
		        request_headers, request_body, response_headers, response_body,
		        response_size, retry_attempt, error_class, created_at
		 FROM http_captures WHERE log_id = ? ORDER BY id`, logID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	var captures []map[string]any
	for rows.Next() {
		var (
			id              int64
			lID             int64
			method          sql.NullString
			url             sql.NullString
			host            sql.NullString
			vendor          sql.NullString
			status          sql.NullInt64
			durationMs      sql.NullFloat64
			requestHeaders  sql.NullString
			requestBody     sql.NullString
			responseHeaders sql.NullString
			responseBody    sql.NullString
			responseSize    sql.NullInt64
			retryAttempt    sql.NullInt64
			errorClass      sql.NullString
			createdAt       string
		)
		if err := rows.Scan(&id, &lID, &method, &url, &host, &vendor, &status, &durationMs,
			&requestHeaders, &requestBody, &responseHeaders, &responseBody,
			&responseSize, &retryAttempt, &errorClass, &createdAt); err != nil {
			return NewToolResultError(fmt.Sprintf("scan failed: %v", err)), nil
		}
		captures = append(captures, map[string]any{
			"id":               id,
			"log_id":           lID,
			"method":           nullStringToAny(method),
			"url":              nullStringToAny(url),
			"host":             nullStringToAny(host),
			"vendor":           nullStringToAny(vendor),
			"status":           nullInt64ToAny(status),
			"duration_ms":      nullFloat64ToAny(durationMs),
			"request_headers":  nullStringToAny(requestHeaders),
			"request_body":     nullStringToAny(requestBody),
			"response_headers": nullStringToAny(responseHeaders),
			"response_body":    nullStringToAny(responseBody),
			"response_size":    nullInt64ToAny(responseSize),
			"retry_attempt":    nullInt64ToAny(retryAttempt),
			"error_class":      nullStringToAny(errorClass),
			"created_at":       createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No HTTP captures found for log_id " + fmt.Sprintf("%d", logID))
	}

	result := map[string]any{
		"log_id": logID,
		"total":  len(captures),
		"calls":  captures,
	}
	return JSONResult(result)
}

// handleGetEmailCaptures returns emails sent during a request or time range.
func handleGetEmailCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	logID := ArgInt(args, "log_id", 0, 1<<31)
	last := ArgString(args, "last")

	if logID == 0 && last == "" {
		return NewToolResultError("log_id or last is required"), nil
	}

	var (
		query    string
		sqlArgs  []any
		joinLogs bool
	)

	if logID > 0 {
		query = `SELECT id, log_id, mailer_class, mailer_action, from_address,
		                to_addresses, cc_addresses, bcc_addresses, subject, body_html,
		                body_text, template_name, template_vars, attachments,
		                delivery_status, smtp_response, duration_ms, created_at
		         FROM email_captures WHERE log_id = ? ORDER BY id`
		sqlArgs = []any{logID}
	} else {
		since := ParseSinceOr(last, 24*time.Hour)
		query = `SELECT ec.id, ec.log_id, ec.mailer_class, ec.mailer_action, ec.from_address,
		                ec.to_addresses, ec.cc_addresses, ec.bcc_addresses, ec.subject, ec.body_html,
		                ec.body_text, ec.template_name, ec.template_vars, ec.attachments,
		                ec.delivery_status, ec.smtp_response, ec.duration_ms, ec.created_at,
		                l.request_id
		         FROM email_captures ec
		         JOIN logs l ON ec.log_id = l.id
		         WHERE ec.created_at > ?
		         ORDER BY ec.created_at DESC LIMIT 50`
		sqlArgs = []any{since.Format(time.RFC3339)}
		joinLogs = true
	}

	rows, err := deps.DB.QueryContext(ctx, query, sqlArgs...)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	var captures []map[string]any
	for rows.Next() {
		var (
			id             int64
			lID            sql.NullInt64
			mailerClass    sql.NullString
			mailerAction   sql.NullString
			fromAddr       sql.NullString
			toAddrs        sql.NullString
			ccAddrs        sql.NullString
			bccAddrs       sql.NullString
			subject        sql.NullString
			bodyHTML       sql.NullString
			bodyText       sql.NullString
			templateName   sql.NullString
			templateVars   sql.NullString
			attachments    sql.NullString
			deliveryStatus sql.NullString
			smtpResponse   sql.NullString
			durationMs     sql.NullFloat64
			createdAt      string
			requestID      sql.NullString
		)

		var scanDest []any
		scanDest = append(scanDest, &id, &lID, &mailerClass, &mailerAction, &fromAddr,
			&toAddrs, &ccAddrs, &bccAddrs, &subject, &bodyHTML,
			&bodyText, &templateName, &templateVars, &attachments,
			&deliveryStatus, &smtpResponse, &durationMs, &createdAt)
		if joinLogs {
			scanDest = append(scanDest, &requestID)
		}

		if err := rows.Scan(scanDest...); err != nil {
			return NewToolResultError(fmt.Sprintf("scan failed: %v", err)), nil
		}

		entry := map[string]any{
			"id":              id,
			"log_id":          nullInt64ToAny(lID),
			"mailer_class":    nullStringToAny(mailerClass),
			"mailer_action":   nullStringToAny(mailerAction),
			"from_address":    nullStringToAny(fromAddr),
			"to_addresses":    nullStringToAny(toAddrs),
			"cc_addresses":    nullStringToAny(ccAddrs),
			"bcc_addresses":   nullStringToAny(bccAddrs),
			"subject":         nullStringToAny(subject),
			"body_html":       nullStringToAny(bodyHTML),
			"body_text":       nullStringToAny(bodyText),
			"template_name":   nullStringToAny(templateName),
			"template_vars":   nullStringToAny(templateVars),
			"attachments":     nullStringToAny(attachments),
			"delivery_status": nullStringToAny(deliveryStatus),
			"smtp_response":   nullStringToAny(smtpResponse),
			"duration_ms":     nullFloat64ToAny(durationMs),
			"created_at":      createdAt,
		}
		if joinLogs {
			entry["request_id"] = nullStringToAny(requestID)
		}
		captures = append(captures, entry)
	}
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No email captures found")
	}

	result := map[string]any{
		"total":  len(captures),
		"emails": captures,
	}
	return JSONResult(result)
}

// handleGetAuditTrail returns audit history for a specific record.
func handleGetAuditTrail(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	recordType := ArgString(args, "record_type")
	recordID := ArgString(args, "record_id")
	if recordType == "" || recordID == "" {
		return NewToolResultError("record_type and record_id are required"), nil
	}

	rows, err := deps.DB.QueryContext(ctx,
		`SELECT id, log_id, record_type, record_id, action, actor_id, actor_type,
		        changed_fields, full_before, full_after, request_id, ip_address, created_at
		 FROM audit_captures
		 WHERE record_type = ? AND record_id = ?
		 ORDER BY created_at DESC LIMIT 100`, recordType, recordID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	captures := scanAuditRows(rows)
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult(fmt.Sprintf("No audit captures found for %s/%s", recordType, recordID))
	}

	result := map[string]any{
		"record_type": recordType,
		"record_id":   recordID,
		"total":       len(captures),
		"entries":     captures,
	}
	return JSONResult(result)
}

// handleSearchAudit searches audit trail across all records.
func handleSearchAudit(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	actorID := ArgString(args, "actor_id")
	action := ArgString(args, "action")
	last := ArgString(args, "last")

	if actorID == "" && action == "" && last == "" {
		return NewToolResultError("at least one filter is required: actor_id, action, or last"), nil
	}

	var conditions []string
	var sqlArgs []any

	if actorID != "" {
		conditions = append(conditions, "actor_id = ?")
		sqlArgs = append(sqlArgs, actorID)
	}
	if action != "" {
		conditions = append(conditions, "action = ?")
		sqlArgs = append(sqlArgs, action)
	}
	if last != "" {
		since := ParseSinceOr(last, 24*time.Hour)
		conditions = append(conditions, "created_at > ?")
		sqlArgs = append(sqlArgs, since.Format(time.RFC3339))
	}

	query := `SELECT id, log_id, record_type, record_id, action, actor_id, actor_type,
	                 changed_fields, full_before, full_after, request_id, ip_address, created_at
	          FROM audit_captures
	          WHERE ` + strings.Join(conditions, " AND ") + `
	          ORDER BY created_at DESC LIMIT 100`

	rows, err := deps.DB.QueryContext(ctx, query, sqlArgs...)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	captures := scanAuditRows(rows)
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No audit captures found matching filters")
	}

	result := map[string]any{
		"total":   len(captures),
		"entries": captures,
	}
	return JSONResult(result)
}

// handleSearchSQL searches SQL captures by fingerprint, table, or duration.
func handleSearchSQL(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	tableName := ArgString(args, "table_name")
	minDurationMs := ArgFloat(args, "min_duration_ms", 0)
	last := ArgString(args, "last")
	limit := ArgInt(args, "limit", 50, 500)

	if fingerprint == "" && tableName == "" && minDurationMs == 0 && last == "" {
		return NewToolResultError("at least one filter is required: fingerprint, table_name, min_duration_ms, or last"), nil
	}

	var conditions []string
	var sqlArgs []any

	if fingerprint != "" {
		conditions = append(conditions, "fingerprint = ?")
		sqlArgs = append(sqlArgs, fingerprint)
	}
	if tableName != "" {
		conditions = append(conditions, "table_name = ?")
		sqlArgs = append(sqlArgs, tableName)
	}
	if minDurationMs > 0 {
		conditions = append(conditions, "duration_ms >= ?")
		sqlArgs = append(sqlArgs, minDurationMs)
	}
	if last != "" {
		since := ParseSinceOr(last, 24*time.Hour)
		conditions = append(conditions, "created_at > ?")
		sqlArgs = append(sqlArgs, since.Format(time.RFC3339))
	}

	whereClause := ""
	if len(conditions) > 0 {
		whereClause = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := fmt.Sprintf(
		`SELECT id, log_id, raw_sql, normalized_sql, bind_values, row_count,
		        duration_ms, cached, in_transaction, explain_plan, pool_size,
		        pool_busy, pool_waiting, caller_location, fingerprint, table_name, created_at
		 FROM sql_captures %s
		 ORDER BY duration_ms DESC LIMIT ?`, whereClause)
	sqlArgs = append(sqlArgs, limit)

	rows, err := deps.DB.QueryContext(ctx, query, sqlArgs...)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	var captures []map[string]any
	for rows.Next() {
		var (
			id             int64
			lID            int64
			rawSQL         sql.NullString
			normalizedSQL  sql.NullString
			bindValues     sql.NullString
			rowCount       sql.NullInt64
			durationMs     sql.NullFloat64
			cached         sql.NullInt64
			inTransaction  sql.NullInt64
			explainPlan    sql.NullString
			poolSize       sql.NullInt64
			poolBusy       sql.NullInt64
			poolWaiting    sql.NullInt64
			callerLocation sql.NullString
			fp             sql.NullString
			tn             sql.NullString
			createdAt      string
		)
		if err := rows.Scan(&id, &lID, &rawSQL, &normalizedSQL, &bindValues, &rowCount,
			&durationMs, &cached, &inTransaction, &explainPlan, &poolSize,
			&poolBusy, &poolWaiting, &callerLocation, &fp, &tn, &createdAt); err != nil {
			return NewToolResultError(fmt.Sprintf("scan failed: %v", err)), nil
		}
		captures = append(captures, map[string]any{
			"id":              id,
			"log_id":          lID,
			"raw_sql":         nullStringToAny(rawSQL),
			"normalized_sql":  nullStringToAny(normalizedSQL),
			"bind_values":     nullStringToAny(bindValues),
			"row_count":       nullInt64ToAny(rowCount),
			"duration_ms":     nullFloat64ToAny(durationMs),
			"cached":          nullInt64ToBool(cached),
			"in_transaction":  nullInt64ToBool(inTransaction),
			"explain_plan":    nullStringToAny(explainPlan),
			"pool_size":       nullInt64ToAny(poolSize),
			"pool_busy":       nullInt64ToAny(poolBusy),
			"pool_waiting":    nullInt64ToAny(poolWaiting),
			"caller_location": nullStringToAny(callerLocation),
			"fingerprint":     nullStringToAny(fp),
			"table_name":      nullStringToAny(tn),
			"created_at":      createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No SQL captures found matching filters")
	}

	result := map[string]any{
		"total":   len(captures),
		"queries": captures,
	}
	return JSONResult(result)
}

// handleGetFileCaptures returns file operations for a request.
func handleGetFileCaptures(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}
	logID := ArgInt(args, "log_id", 0, 1<<31)
	if logID == 0 {
		return NewToolResultError("log_id is required"), nil
	}

	rows, err := deps.DB.QueryContext(ctx,
		`SELECT id, log_id, action, filename, size_bytes, content_type,
		        storage_service, key, duration_ms, created_at
		 FROM file_captures WHERE log_id = ?`, logID)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}
	defer rows.Close()

	var captures []map[string]any
	for rows.Next() {
		var (
			id             int64
			lID            int64
			action         sql.NullString
			filename       sql.NullString
			sizeBytes      sql.NullInt64
			contentType    sql.NullString
			storageService sql.NullString
			key            sql.NullString
			durationMs     sql.NullFloat64
			createdAt      string
		)
		if err := rows.Scan(&id, &lID, &action, &filename, &sizeBytes, &contentType,
			&storageService, &key, &durationMs, &createdAt); err != nil {
			return NewToolResultError(fmt.Sprintf("scan failed: %v", err)), nil
		}
		captures = append(captures, map[string]any{
			"id":              id,
			"log_id":          lID,
			"action":          nullStringToAny(action),
			"filename":        nullStringToAny(filename),
			"size_bytes":      nullInt64ToAny(sizeBytes),
			"content_type":    nullStringToAny(contentType),
			"storage_service": nullStringToAny(storageService),
			"key":             nullStringToAny(key),
			"duration_ms":     nullFloat64ToAny(durationMs),
			"created_at":      createdAt,
		})
	}
	if err := rows.Err(); err != nil {
		return NewToolResultError(fmt.Sprintf("iteration failed: %v", err)), nil
	}

	if len(captures) == 0 {
		return EmptyResult("No file captures found for log_id " + fmt.Sprintf("%d", logID))
	}

	result := map[string]any{
		"log_id": logID,
		"total":  len(captures),
		"files":  captures,
	}
	return JSONResult(result)
}

// handleGetPIIConfig reads the PII scrubbing configuration from app_config.
func handleGetPIIConfig(ctx context.Context, deps DeepCaptureDeps) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	var value string
	err := deps.DB.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = ?", "pii_scrubbing").Scan(&value)
	if err == sql.ErrNoRows {
		return EmptyResult("No PII scrubbing configuration found")
	}
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return NewToolResultError(fmt.Sprintf("invalid JSON in pii_scrubbing config: %v", err)), nil
	}

	return JSONResult(config)
}

// handleUpdatePIIConfig writes the PII scrubbing configuration to app_config.
func handleUpdatePIIConfig(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	config, ok := args["config"]
	if !ok {
		return NewToolResultError("config is required (JSON object with PII scrubbing settings)"), nil
	}

	data, err := json.Marshal(config)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid config: %v", err)), nil
	}

	_, err = deps.DB.ExecContext(ctx,
		"INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"pii_scrubbing", string(data))
	if err != nil {
		return NewToolResultError(fmt.Sprintf("update failed: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status":  "updated",
		"config":  config,
	})
}

// handleGetRetention reads the retention policy configuration from app_config.
func handleGetRetention(ctx context.Context, deps DeepCaptureDeps) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	var value string
	err := deps.DB.QueryRowContext(ctx, "SELECT value FROM app_config WHERE key = ?", "retention_policy").Scan(&value)
	if err == sql.ErrNoRows {
		return EmptyResult("No retention policy configuration found")
	}
	if err != nil {
		return NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		return NewToolResultError(fmt.Sprintf("invalid JSON in retention_policy config: %v", err)), nil
	}

	return JSONResult(config)
}

// handleUpdateRetention writes the retention policy configuration to app_config.
func handleUpdateRetention(ctx context.Context, deps DeepCaptureDeps, args map[string]any) (*CallToolResult, error) {
	if deps.DB == nil {
		return NewToolResultError("database connection not available"), nil
	}

	config, ok := args["config"]
	if !ok {
		return NewToolResultError("config is required (JSON object with retention TTLs)"), nil
	}

	data, err := json.Marshal(config)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("invalid config: %v", err)), nil
	}

	_, err = deps.DB.ExecContext(ctx,
		"INSERT INTO app_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		"retention_policy", string(data))
	if err != nil {
		return NewToolResultError(fmt.Sprintf("update failed: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"status": "updated",
		"config": config,
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// scanAuditRows scans audit_captures rows into a slice of maps.
// The caller must check rows.Err() after this returns.
func scanAuditRows(rows *sql.Rows) []map[string]any {
	var captures []map[string]any
	for rows.Next() {
		var (
			id            int64
			logID         sql.NullInt64
			recordType    sql.NullString
			recordID      sql.NullString
			action        sql.NullString
			actorID       sql.NullString
			actorType     sql.NullString
			changedFields sql.NullString
			fullBefore    sql.NullString
			fullAfter     sql.NullString
			requestID     sql.NullString
			ipAddress     sql.NullString
			createdAt     string
		)
		if err := rows.Scan(&id, &logID, &recordType, &recordID, &action, &actorID, &actorType,
			&changedFields, &fullBefore, &fullAfter, &requestID, &ipAddress, &createdAt); err != nil {
			continue
		}
		captures = append(captures, map[string]any{
			"id":             id,
			"log_id":         nullInt64ToAny(logID),
			"record_type":    nullStringToAny(recordType),
			"record_id":      nullStringToAny(recordID),
			"action":         nullStringToAny(action),
			"actor_id":       nullStringToAny(actorID),
			"actor_type":     nullStringToAny(actorType),
			"changed_fields": nullStringToAny(changedFields),
			"full_before":    nullStringToAny(fullBefore),
			"full_after":     nullStringToAny(fullAfter),
			"request_id":     nullStringToAny(requestID),
			"ip_address":     nullStringToAny(ipAddress),
			"created_at":     createdAt,
		})
	}
	return captures
}

// nullStringToAny converts a sql.NullString to a JSON-friendly value.
func nullStringToAny(ns sql.NullString) any {
	if ns.Valid {
		return ns.String
	}
	return nil
}

// nullInt64ToAny converts a sql.NullInt64 to a JSON-friendly value.
func nullInt64ToAny(ni sql.NullInt64) any {
	if ni.Valid {
		return ni.Int64
	}
	return nil
}

// nullFloat64ToAny converts a sql.NullFloat64 to a JSON-friendly value.
func nullFloat64ToAny(nf sql.NullFloat64) any {
	if nf.Valid {
		return nf.Float64
	}
	return nil
}

// nullInt64ToBool converts a sql.NullInt64 (0/1) to a boolean.
func nullInt64ToBool(ni sql.NullInt64) any {
	if ni.Valid {
		return ni.Int64 != 0
	}
	return false
}
