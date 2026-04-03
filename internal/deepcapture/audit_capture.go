package deepcapture

import (
	"encoding/json"
)

// auditEntry represents a single audit trail entry from the Audit section.
type auditEntry struct {
	RecordType    string          `json:"record_type"`
	RecordID      string          `json:"record_id"`
	Action        string          `json:"action"`
	ActorID       string          `json:"actor_id"`
	ActorType     string          `json:"actor_type"`
	ChangedFields json.RawMessage `json:"changed_fields"`
	FullBefore    json.RawMessage `json:"full_before"`
	FullAfter     json.RawMessage `json:"full_after"`
	RequestID     string          `json:"request_id"`
	IPAddress     string          `json:"ip_address"`
}

// AuditCaptureHandler extracts audit trail captures from doc.Event.Audit
// into the audit_captures table.
type AuditCaptureHandler struct{}

const auditCaptureSQL = `INSERT INTO audit_captures (
	log_id, record_type, record_id, action, actor_id, actor_type,
	changed_fields, full_before, full_after, request_id, ip_address
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (h *AuditCaptureHandler) Extract(doc *Document, logID int64) ([]DetailInsert, error) {
	if doc.Event.Audit == nil {
		return nil, nil
	}

	var entries []auditEntry
	if err := json.Unmarshal(doc.Event.Audit, &entries); err != nil {
		return nil, err
	}

	inserts := make([]DetailInsert, 0, len(entries))
	for _, e := range entries {
		inserts = append(inserts, DetailInsert{
			SQL: auditCaptureSQL,
			Args: []interface{}{
				logID,
				nullableStr(e.RecordType),
				nullableStr(e.RecordID),
				nullableStr(e.Action),
				nullableStr(e.ActorID),
				nullableStr(e.ActorType),
				nullableJSON(e.ChangedFields),
				nullableJSON(e.FullBefore),
				nullableJSON(e.FullAfter),
				nullableStr(e.RequestID),
				nullableStr(e.IPAddress),
			},
		})
	}

	return inserts, nil
}
