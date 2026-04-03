package deepcapture

import (
	"database/sql"
	"encoding/json"
	"log/slog"
)

// Document represents the deep capture wire format sent by the Ruby gem.
type Document struct {
	ID          string          `json:"id" msgpack:"id"`
	Timestamp   string          `json:"timestamp" msgpack:"timestamp"`
	Level       string          `json:"level" msgpack:"level"`
	Service     string          `json:"service" msgpack:"service"`
	Environment string          `json:"environment" msgpack:"environment"`
	Version     string          `json:"version" msgpack:"version"`
	Context     json.RawMessage `json:"context" msgpack:"context"`
	Event       *Event          `json:"event" msgpack:"event"`
}

// Event holds the typed event payload within a deep capture document.
type Event struct {
	Type     string          `json:"type" msgpack:"type"`
	Message  string          `json:"message" msgpack:"message"`
	Metadata json.RawMessage `json:"metadata,omitempty" msgpack:"metadata,omitempty"`

	// HTTP request fields
	Request    json.RawMessage `json:"request,omitempty" msgpack:"request,omitempty"`
	Response   json.RawMessage `json:"response,omitempty" msgpack:"response,omitempty"`
	Controller string          `json:"controller,omitempty" msgpack:"controller,omitempty"`
	Action     string          `json:"action,omitempty" msgpack:"action,omitempty"`
	DurationMs float64         `json:"duration_ms,omitempty" msgpack:"duration_ms,omitempty"`

	// Deep capture sections
	DB          *DBSection      `json:"db,omitempty" msgpack:"db,omitempty"`
	External    json.RawMessage `json:"external,omitempty" msgpack:"external,omitempty"`
	Email       json.RawMessage `json:"email,omitempty" msgpack:"email,omitempty"`
	Files       json.RawMessage `json:"files,omitempty" msgpack:"files,omitempty"`
	Audit       json.RawMessage `json:"audit,omitempty" msgpack:"audit,omitempty"`
	Performance json.RawMessage `json:"performance,omitempty" msgpack:"performance,omitempty"`
	Timeline    json.RawMessage `json:"timeline,omitempty" msgpack:"timeline,omitempty"`
	Logs        json.RawMessage `json:"logs,omitempty" msgpack:"logs,omitempty"`

	// Job fields
	Job json.RawMessage `json:"job,omitempty" msgpack:"job,omitempty"`
}

// DBSection holds SQL capture data including query-level details.
type DBSection struct {
	Queries          json.RawMessage `json:"queries" msgpack:"queries"`
	NPlusOne         bool            `json:"n_plus_one" msgpack:"n_plus_one"`
	DuplicateQueries int             `json:"duplicate_queries" msgpack:"duplicate_queries"`
	SlowQueries      int             `json:"slow_queries" msgpack:"slow_queries"`
}

// DetailHandler knows how to extract and insert detail rows from a document.
type DetailHandler interface {
	// Extract returns SQL and args for bulk inserting detail rows.
	// Returns nil if this section is not present in the document.
	Extract(doc *Document, logID int64) (queries []DetailInsert, err error)
}

// DetailInsert holds a single SQL INSERT statement with its bind arguments.
type DetailInsert struct {
	SQL  string
	Args []interface{}
}

// registry holds all registered detail handlers.
var registry []DetailHandler

func init() {
	registry = []DetailHandler{
		&RequestCaptureHandler{},
		&SQLCaptureHandler{},
		&HTTPCaptureHandler{},
		&EmailCaptureHandler{},
		&AuditCaptureHandler{},
		&FileCaptureHandler{},
	}
}

// ProcessDocument decomposes a deep capture document into detail table rows.
// Called within the existing ingest transaction after the core log row is inserted.
func ProcessDocument(tx *sql.Tx, doc *Document, logID int64) error {
	if doc.Event == nil {
		return nil
	}

	for _, handler := range registry {
		inserts, err := handler.Extract(doc, logID)
		if err != nil {
			slog.Warn("deepcapture: handler extract failed",
				"error", err,
				"log_id", logID,
			)
			continue
		}
		for _, ins := range inserts {
			if _, err := tx.Exec(ins.SQL, ins.Args...); err != nil {
				slog.Warn("deepcapture: detail insert failed",
					"error", err,
					"log_id", logID,
					"sql", ins.SQL,
				)
				continue
			}
		}
	}
	return nil
}
