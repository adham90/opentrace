package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

type ingestLogEntry struct {
	Timestamp      time.Time              `json:"timestamp"`
	Level          string                 `json:"level"`
	Service        string                 `json:"service"`
	TraceID        string                 `json:"trace_id"`
	SpanID         string                 `json:"span_id"`
	ParentSpanID   string                 `json:"parent_span_id"`
	Message        string                 `json:"message"`
	Environment    string                 `json:"environment"`
	EventType      string                 `json:"event_type"`
	Metadata       map[string]any         `json:"metadata"`
	RequestSummary *ingestRequestSummary  `json:"request_summary,omitempty"`
}

type ingestRequestSummary struct {
	Controller          string  `json:"controller"`
	Action              string  `json:"action"`
	Method              string  `json:"method"`
	Path                string  `json:"path"`
	Status              int     `json:"status"`
	DurationMs          float64 `json:"duration_ms"`
	DBTimeMs            float64 `json:"db_time_ms"`
	ViewTimeMs          float64 `json:"view_time_ms"`
	SQLCount            int     `json:"sql_count"`
	SQLTotalMs          float64 `json:"sql_total_ms"`
	SQLSlowestMs        float64 `json:"sql_slowest_ms"`
	SQLSlowestName      string  `json:"sql_slowest_name"`
	NPlusOne            bool    `json:"n_plus_one"`
	ViewCount           int     `json:"view_count"`
	ViewTotalMs         float64 `json:"view_total_ms"`
	ViewSlowestMs       float64 `json:"view_slowest_ms"`
	ViewSlowestTemplate string  `json:"view_slowest_template"`
	CacheReads          int     `json:"cache_reads"`
	CacheHits           int     `json:"cache_hits"`
	CacheWrites         int     `json:"cache_writes"`
	CacheHitRatio       float64 `json:"cache_hit_ratio"`
	HTTPExternalCount   int     `json:"http_external_count"`
	HTTPExternalTotalMs float64 `json:"http_external_total_ms"`
	HTTPSlowestMs       float64 `json:"http_slowest_ms"`
	HTTPSlowestHost     string  `json:"http_slowest_host"`
	MemoryBeforeMb      float64         `json:"memory_before_mb"`
	MemoryAfterMb       float64         `json:"memory_after_mb"`
	MemoryDeltaMb       float64         `json:"memory_delta_mb"`
	Timeline            json.RawMessage `json:"timeline,omitempty"`
	TimeBreakdown       json.RawMessage `json:"time_breakdown,omitempty"`
	DuplicateQueries    int             `json:"duplicate_queries"`
	WorstDuplicateCount int             `json:"worst_duplicate_count"`
	TopDuplicates       json.RawMessage `json:"top_duplicates,omitempty"`
}

func (s *Server) handleIngestLogs(w http.ResponseWriter, r *http.Request) {
	// Check client API version compatibility
	if clientVersion := r.Header.Get("X-API-Version"); clientVersion != "" {
		v, err := strconv.Atoi(clientVersion)
		if err != nil {
			w.Header().Set("X-API-Version", strconv.Itoa(APIVersion))
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid X-API-Version: %s", clientVersion))
			return
		}
		if v < MinClientAPIVersion {
			w.Header().Set("X-API-Version", strconv.Itoa(APIVersion))
			w.Header().Set("X-Min-Client-API-Version", strconv.Itoa(MinClientAPIVersion))
			writeError(w, http.StatusBadRequest, fmt.Sprintf("client API version %d is below minimum %d, please upgrade", v, MinClientAPIVersion))
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var entries []ingestLogEntry
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var single ingestLogEntry
		if err := json.Unmarshal(trimmed, &single); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		entries = []ingestLogEntry{single}
	} else {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}

	// Validate required fields
	for i, e := range entries {
		if e.Timestamp.IsZero() || e.Level == "" || e.Message == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: timestamp, level, and message are required", i))
			return
		}
	}

	// Check for duplicate batch
	batchID := r.Header.Get("X-Batch-ID")
	if batchID != "" {
		existing, err := s.logStore.GetBatch(r.Context(), batchID)
		if err == nil && existing != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"count":        existing.LogCount,
				"deduplicated": true,
			})
			return
		}
	}

	logEntries := make([]store.LogEntry, len(entries))
	for i, e := range entries {
		logEntries[i] = store.LogEntry{
			Timestamp:    e.Timestamp,
			Level:        e.Level,
			Service:      e.Service,
			TraceID:      e.TraceID,
			SpanID:       e.SpanID,
			ParentSpanID: e.ParentSpanID,
			Message:      e.Message,
			Environment:  e.Environment,
			EventType:    e.EventType,
			Metadata:     e.Metadata,
		}
		if e.RequestSummary != nil {
			rs := e.RequestSummary
			logEntries[i].RequestSummary = &store.RequestSummary{
				Controller:          rs.Controller,
				Action:              rs.Action,
				Method:              rs.Method,
				Path:                rs.Path,
				Status:              rs.Status,
				DurationMs:          rs.DurationMs,
				DBTimeMs:            rs.DBTimeMs,
				ViewTimeMs:          rs.ViewTimeMs,
				SQLCount:            rs.SQLCount,
				SQLTotalMs:          rs.SQLTotalMs,
				SQLSlowestMs:        rs.SQLSlowestMs,
				SQLSlowestName:      rs.SQLSlowestName,
				NPlusOne:            rs.NPlusOne,
				ViewCount:           rs.ViewCount,
				ViewTotalMs:         rs.ViewTotalMs,
				ViewSlowestMs:       rs.ViewSlowestMs,
				ViewSlowestTemplate: rs.ViewSlowestTemplate,
				CacheReads:          rs.CacheReads,
				CacheHits:           rs.CacheHits,
				CacheWrites:         rs.CacheWrites,
				CacheHitRatio:       rs.CacheHitRatio,
				HTTPExternalCount:   rs.HTTPExternalCount,
				HTTPExternalTotalMs: rs.HTTPExternalTotalMs,
				HTTPSlowestMs:       rs.HTTPSlowestMs,
				HTTPSlowestHost:     rs.HTTPSlowestHost,
				MemoryBeforeMb:      rs.MemoryBeforeMb,
				MemoryAfterMb:       rs.MemoryAfterMb,
				MemoryDeltaMb:       rs.MemoryDeltaMb,
				Timeline:            string(rs.Timeline),
				TimeBreakdown:       string(rs.TimeBreakdown),
				DuplicateQueries:    rs.DuplicateQueries,
				WorstDuplicateCount: rs.WorstDuplicateCount,
				TopDuplicates:       string(rs.TopDuplicates),
			}
		}
	}

	count, err := s.logStore.BatchInsert(r.Context(), logEntries)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to insert logs")
		return
	}

	// Record batch ID after successful insert
	if batchID != "" {
		_ = s.logStore.RecordBatch(r.Context(), batchID, count)
	}

	if count > 0 {
		s.ensureLogsConnector(r.Context())
	}

	status := http.StatusCreated
	if count == 0 {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]int{"count": count})
}

// ensureLogsConnector auto-creates and registers a logs connector if one
// doesn't already exist. Called after each successful log ingestion so the
// AI agent can use the log_search tool. Uses a mutex instead of sync.Once
// so it can retry after transient failures or re-register after deletion.
func (s *Server) ensureLogsConnector(ctx context.Context) {
	// Fast path: already registered in memory (no lock needed)
	if s.registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	s.logsConnMu.Lock()
	defer s.logsConnMu.Unlock()

	// Double-check after acquiring lock
	if s.registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	// Check if a logs data source row already exists in the DB
	sources, err := s.dsStore.List(ctx, store.ListDataSourceParams{Type: store.ConnectorLogs})
	if err != nil {
		slog.Warn("ensureLogsConnector: failed to list data sources", "error", err)
		return
	}
	var dsID *store.DataSource
	if len(sources) > 0 {
		dsID = &sources[0]
	}

	// Create DB row if it doesn't exist
	if dsID == nil {
		created, err := s.dsStore.Create(ctx, store.CreateDataSourceParams{
			Type:   store.ConnectorLogs,
			Name:   "Log Ingestion",
			Config: map[string]any{},
		})
		if err != nil {
			slog.Warn("ensureLogsConnector: failed to create data source", "error", err)
			return
		}
		dsID = created
	}

	// Create and register the runtime connector
	lc := connector.NewLogsConnector(s.logStore)
	s.registry.Register(lc)

	// Update DB status to connected
	connected := store.StatusConnected
	if _, err := s.dsStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		slog.Warn("ensureLogsConnector: failed to update status", "error", err)
	}

	slog.Info("auto-registered logs connector", "data_source_id", dsID.ID)
}
