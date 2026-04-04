package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/pkg/server"
)

// FlatHandler handles log ingestion in the new flat SDK format.
// This is the preferred format for new SDKs.
type FlatHandler struct {
	Engine *engine.Store
}

// flatEntry is the JSON structure sent by the new SDK.
type flatEntry struct {
	Ts               string          `json:"ts"`
	Level            string          `json:"level"`
	Service          string          `json:"service"`
	Env              string          `json:"env,omitempty"`
	Version          string          `json:"version,omitempty"`
	Message          string          `json:"message"`
	EventType        string          `json:"event_type,omitempty"`
	TraceID          string          `json:"trace_id,omitempty"`
	SpanID           string          `json:"span_id,omitempty"`
	ParentSpanID     string          `json:"parent_span_id,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
	UserID           string          `json:"user_id,omitempty"`
	TenantID         string          `json:"tenant_id,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Method           string          `json:"method,omitempty"`
	Path             string          `json:"path,omitempty"`
	Status           int             `json:"status,omitempty"`
	DurationMs       int             `json:"duration_ms,omitempty"`
	Handler       string          `json:"handler,omitempty"`
	DbMs             int             `json:"db_ms,omitempty"`
	DbCount          int             `json:"db_count,omitempty"`
	NPlusOne         *bool           `json:"n_plus_one,omitempty"`
	SlowQueries      int             `json:"slow_queries,omitempty"`
	DupQueries       int             `json:"dup_queries,omitempty"`
	ErrorClass   string          `json:"error_class,omitempty"`
	SourceFile       string          `json:"source_file,omitempty"`
	SourceLine       int             `json:"source_line,omitempty"`
	Body             json.RawMessage `json:"body,omitempty"`
}

// HandleFlatIngest is the HTTP handler for POST /api/v2/logs.
// Accepts the flat SDK format: top-level keys = columns, body = opaque blob.
func (h *FlatHandler) HandleFlatIngest(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Parse as array or single object
	var raw []json.RawMessage
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 0 && trimmed[0] == '{' {
		raw = []json.RawMessage{json.RawMessage(trimmed)}
	} else {
		if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
			return
		}
	}

	entries := make([]chunk.Entry, 0, len(raw))
	for i, r := range raw {
		var fe flatEntry
		if err := json.Unmarshal(r, &fe); err != nil {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: %v", i, err))
			return
		}

		// Validate required fields
		if fe.Level == "" || fe.Message == "" {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: level and message are required", i))
			return
		}

		// Parse timestamp
		var tsMs int64
		if fe.Ts != "" {
			t, err := time.Parse(time.RFC3339Nano, fe.Ts)
			if err != nil {
				server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: invalid ts format (use RFC3339): %v", i, err))
				return
			}
			tsMs = t.UnixMilli()
		} else {
			tsMs = time.Now().UnixMilli()
		}

		entries = append(entries, chunk.Entry{
			Ts:           tsMs,
			Level:        strings.ToLower(fe.Level),
			Service:      fe.Service,
			Env:          fe.Env,
			Version:      fe.Version,
			Message:      fe.Message,
			EventType:    fe.EventType,
			TraceID:      fe.TraceID,
			SpanID:       fe.SpanID,
			ParentSpanID: fe.ParentSpanID,
			RequestID:    fe.RequestID,
			UserID:       fe.UserID,
			TenantID:     fe.TenantID,
			SessionID:    fe.SessionID,
			Method:       fe.Method,
			Path:         fe.Path,
			Status:       fe.Status,
			DurationMs:   fe.DurationMs,
			Handler:      fe.Handler,
			DbMs:         fe.DbMs,
			DbCount:      fe.DbCount,
			NPlusOne:     fe.NPlusOne,
			SlowQueries:    fe.SlowQueries,
			DupQueries:     fe.DupQueries,
			ErrorClass: fe.ErrorClass,
			SourceFile:     fe.SourceFile,
			SourceLine:     fe.SourceLine,
			Body:           fe.Body,
		})
	}

	result, err := h.Engine.Ingest(entries)
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, fmt.Sprintf("ingest failed: %v", err))
		return
	}

	// Return assigned IDs
	ids := make([]int64, len(result))
	for i, e := range result {
		ids[i] = e.ID
	}

	server.WriteJSON(w, http.StatusOK, map[string]any{
		"count": len(result),
		"ids":   ids,
	})
}
