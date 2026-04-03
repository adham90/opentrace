package deepcapture

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// InRequestLog represents a log entry made during a request via OpenTrace.log()
type InRequestLog struct {
	Level    string         `json:"level"`
	Message  string         `json:"message"`
	Metadata map[string]any `json:"metadata,omitempty"`
	At       float64        `json:"at"` // offset_ms from request start
}

// ProcessInRequestLogs stores event.logs entries as separate rows in the logs table,
// linked to the parent request by request_id and trace_id.
func ProcessInRequestLogs(tx *sql.Tx, doc *Document, parentLogID int64) error {
	if doc.Event == nil || len(doc.Event.Logs) == 0 {
		return nil
	}

	var logs []InRequestLog
	if err := json.Unmarshal(doc.Event.Logs, &logs); err != nil {
		return nil // non-fatal
	}

	// Extract context fields from the document
	var ctx map[string]any
	if len(doc.Context) > 0 {
		_ = json.Unmarshal(doc.Context, &ctx)
	}
	requestID, _ := ctx["request_id"].(string)
	traceID, _ := ctx["trace_id"].(string)
	spanID, _ := ctx["span_id"].(string)

	for _, l := range logs {
		// Compute timestamp from parent timestamp + offset
		ts := doc.Timestamp // fallback
		if parentTS, err := time.Parse(time.RFC3339Nano, doc.Timestamp); err == nil {
			ts = parentTS.Add(time.Duration(l.At * float64(time.Millisecond))).Format(time.RFC3339Nano)
		}

		metaJSON := "{}"
		if l.Metadata != nil {
			if raw, err := json.Marshal(l.Metadata); err == nil {
				metaJSON = string(raw)
			}
		}

		_, err := tx.Exec(`INSERT INTO logs (timestamp, level, service, environment, message, metadata,
			request_id, trace_id, span_id, event_type)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'in_request_log')`,
			ts,
			l.Level,
			doc.Service,
			doc.Environment,
			l.Message,
			metaJSON,
			requestID,
			traceID,
			spanID,
		)
		if err != nil {
			// non-fatal, skip individual log entries
			slog.Warn("deepcapture: in-request log insert failed",
				"error", err,
				"parent_log_id", parentLogID,
			)
			continue
		}
	}

	return nil
}
