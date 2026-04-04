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

// flatEntry is the JSON structure sent by the new SDK (45 fields).
type flatEntry struct {
	Ts           string `json:"ts"`
	Level        string `json:"level"`
	Service      string `json:"service"`
	Message      string `json:"message"`
	Env          string `json:"env,omitempty"`
	Version      string `json:"version,omitempty"`
	Host         string `json:"host,omitempty"`
	Kind         string `json:"kind,omitempty"`
	EventType    string `json:"event_type,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	SpanID       string `json:"span_id,omitempty"`
	ParentSpanID string `json:"parent_span_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	UserID       string `json:"user_id,omitempty"`
	TenantID     string `json:"tenant_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	Method       string `json:"method,omitempty"`
	Path         string `json:"path,omitempty"`
	Route        string `json:"route,omitempty"`
	Handler      string `json:"handler,omitempty"`
	Status       int    `json:"status,omitempty"`
	DurationMs   int    `json:"duration_ms,omitempty"`
	DbMs         int    `json:"db_ms,omitempty"`
	DbCount      int    `json:"db_count,omitempty"`
	CacheMs      int    `json:"cache_ms,omitempty"`
	CacheHits    int    `json:"cache_hits,omitempty"`
	CacheMisses  int    `json:"cache_misses,omitempty"`
	ExtMs        int    `json:"ext_ms,omitempty"`
	ExtCount     int    `json:"ext_count,omitempty"`
	RenderMs     int    `json:"render_ms,omitempty"`
	AllocCount   int    `json:"alloc_count,omitempty"`
	MemDeltaMb   int    `json:"mem_delta_mb,omitempty"`
	NPlusOne     *bool  `json:"n_plus_one,omitempty"`
	SlowQueries  int    `json:"slow_queries,omitempty"`
	DupQueries   int    `json:"dup_queries,omitempty"`
	ErrorClass   string `json:"error_class,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	SourceFile   string `json:"source_file,omitempty"`
	SourceLine   int    `json:"source_line,omitempty"`
	JobClass     string `json:"job_class,omitempty"`
	JobQueue     string `json:"job_queue,omitempty"`
	JobID        string `json:"job_id,omitempty"`
	QueueMs      int    `json:"queue_ms,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
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
			Ts: tsMs, Level: strings.ToLower(fe.Level),
			Service: fe.Service, Message: fe.Message,
			Env: fe.Env, Version: fe.Version, Host: fe.Host,
			Kind: fe.Kind, EventType: fe.EventType,
			TraceID: fe.TraceID, SpanID: fe.SpanID,
			ParentSpanID: fe.ParentSpanID, RequestID: fe.RequestID,
			UserID: fe.UserID, TenantID: fe.TenantID, SessionID: fe.SessionID,
			Method: fe.Method, Path: fe.Path, Route: fe.Route,
			Handler: fe.Handler, Status: fe.Status, DurationMs: fe.DurationMs,
			DbMs: fe.DbMs, DbCount: fe.DbCount,
			CacheMs: fe.CacheMs, CacheHits: fe.CacheHits, CacheMisses: fe.CacheMisses,
			ExtMs: fe.ExtMs, ExtCount: fe.ExtCount, RenderMs: fe.RenderMs,
			AllocCount: fe.AllocCount, MemDeltaMb: fe.MemDeltaMb,
			NPlusOne: fe.NPlusOne, SlowQueries: fe.SlowQueries, DupQueries: fe.DupQueries,
			ErrorClass: fe.ErrorClass, ErrorMessage: fe.ErrorMessage,
			SourceFile: fe.SourceFile, SourceLine: fe.SourceLine,
			JobClass: fe.JobClass, JobQueue: fe.JobQueue, JobID: fe.JobID, QueueMs: fe.QueueMs,
			Body: fe.Body,
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
