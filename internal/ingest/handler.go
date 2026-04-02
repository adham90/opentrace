package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/connector"
	mcpserver "github.com/adham90/opentrace/internal/mcp"
	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	// APIVersion is the current ingestion API version. Increment on breaking changes.
	APIVersion = 1
	// MinClientAPIVersion is the minimum client API version the server accepts.
	MinClientAPIVersion = 1
)

// WatchStreamEvaluator is a minimal interface to avoid importing the watcher package.
type WatchStreamEvaluator interface {
	OnLogsReceived(entries []store.LogEntry)
}

// Handler holds the dependencies for the log ingestion HTTP handler.
type Handler struct {
	LogStore         store.LogStore
	SettingsStore    store.SettingsStore
	ErrorGroupStore  store.ErrorGroupStore
	ErrorImpactStore store.ErrorImpactStore
	CodeEntityStore  store.CodeEntityStore
	TraceStore       store.TraceStore
	DSStore          store.DataSourceStore
	Registry         *connector.Registry
	Cfg              *config.Config
	WatchStream      WatchStreamEvaluator
	Queue            *Queue

	logsConnMu sync.Mutex
}

// BatchIDPattern matches a UUID in 8-4-4-4-12 hex format.
var batchIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidBatchID checks whether the given string is a valid UUID format.
func IsValidBatchID(id string) bool {
	return batchIDPattern.MatchString(id)
}

type ingestLogEntry struct {
	Timestamp        time.Time              `json:"timestamp"`
	Level            string                 `json:"level"`
	Service          string                 `json:"service"`
	Environment      string                 `json:"environment"`
	CommitHash       string                 `json:"commit_hash"`
	TraceID          string                 `json:"trace_id"`
	SpanID           string                 `json:"span_id"`
	ParentSpanID     string                 `json:"parent_span_id"`
	RequestID        string                 `json:"request_id"`
	Message          string                 `json:"message"`
	EventType        string                 `json:"event_type"`
	ExceptionClass   string                 `json:"exception_class"`
	ErrorFingerprint string                 `json:"error_fingerprint"`
	SourceFile       string                 `json:"source_file"`
	SourceLine       int                    `json:"source_line"`
	Metadata         map[string]any         `json:"metadata"`
	RequestSummary   *ingestRequestSummary  `json:"request_summary,omitempty"`
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

// HandleIngestLogs is the HTTP handler for POST /api/logs.
func (h *Handler) HandleIngestLogs(w http.ResponseWriter, r *http.Request) {
	// Check client API version compatibility
	if clientVersion := r.Header.Get("X-API-Version"); clientVersion != "" {
		v, err := strconv.Atoi(clientVersion)
		if err != nil {
			w.Header().Set("X-API-Version", strconv.Itoa(APIVersion))
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid X-API-Version: %s", clientVersion))
			return
		}
		if v < MinClientAPIVersion {
			w.Header().Set("X-API-Version", strconv.Itoa(APIVersion))
			w.Header().Set("X-Min-Client-API-Version", strconv.Itoa(MinClientAPIVersion))
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("client API version %d is below minimum %d, please upgrade", v, MinClientAPIVersion))
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var entries []ingestLogEntry
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var single ingestLogEntry
		if err := json.Unmarshal(trimmed, &single); err != nil {
			server.WriteError(w, http.StatusBadRequest, server.FormatJSONError(err, "object"))
			return
		}
		entries = []ingestLogEntry{single}
	} else {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			server.WriteError(w, http.StatusBadRequest, server.FormatJSONError(err, "array"))
			return
		}
	}

	// Validate required fields and normalize levels to lowercase
	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "warning": true,
		"error": true, "fatal": true,
	}
	for i, e := range entries {
		var missing []string
		if e.Timestamp.IsZero() {
			missing = append(missing, "timestamp")
		}
		if e.Level == "" {
			missing = append(missing, "level")
		}
		if e.Message == "" {
			missing = append(missing, "message")
		}
		if len(missing) > 0 {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: missing required field(s): %s", i, strings.Join(missing, ", ")))
			return
		}
		entries[i].Level = strings.ToLower(e.Level)
		if !validLevels[entries[i].Level] {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: 'level' must be one of: debug, info, warn, error, fatal (got %q)", i, e.Level))
			return
		}
	}

	// Check for duplicate batch
	batchID := r.Header.Get("X-Batch-ID")
	if batchID != "" {
		if !IsValidBatchID(batchID) {
			server.WriteError(w, http.StatusBadRequest, "invalid X-Batch-ID format (expected UUID)")
			return
		}
		existing, err := h.LogStore.GetBatch(r.Context(), batchID)
		if err == nil && existing != nil {
			server.WriteJSON(w, http.StatusOK, map[string]any{
				"count":        existing.LogCount,
				"deduplicated": true,
			})
			return
		}
	}

	logEntries := make([]store.LogEntry, len(entries))
	for i, e := range entries {
		// Pre-marshal metadata once to avoid double marshal in the store layer
		var metadataJSON string
		if e.Metadata != nil {
			if raw, err := json.Marshal(e.Metadata); err == nil {
				metadataJSON = string(raw)
			}
		}

		logEntries[i] = store.LogEntry{
			Timestamp:        e.Timestamp,
			Level:            e.Level,
			Service:          e.Service,
			Environment:      e.Environment,
			CommitHash:       e.CommitHash,
			TraceID:          e.TraceID,
			SpanID:           e.SpanID,
			ParentSpanID:     e.ParentSpanID,
			RequestID:        e.RequestID,
			Message:          e.Message,
			EventType:        e.EventType,
			ExceptionClass:   e.ExceptionClass,
			ErrorFingerprint: e.ErrorFingerprint,
			SourceFile:       e.SourceFile,
			SourceLine:       e.SourceLine,
			Metadata:         e.Metadata,
			MetadataJSON:     metadataJSON,
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

	// Apply sampling rules
	originalCount := len(logEntries)
	if h.SettingsStore != nil {
		rules, _ := h.SettingsStore.GetSamplingRules(r.Context())
		if len(rules) > 0 {
			logEntries = ApplySamplingRules(logEntries, rules)
		}
	}

	// Use async ingest queue if available; otherwise fall back to synchronous insert
	var count int
	if h.Queue != nil {
		count, err = h.Queue.Enqueue(r.Context(), logEntries)
	} else {
		count, err = h.LogStore.BatchInsert(r.Context(), logEntries)
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to insert logs")
		return
	}

	// Record Prometheus metrics for ingested logs
	if count > 0 {
		metrics.RecordLogIngest(count)
	}

	// Record batch ID after successful insert
	if batchID != "" {
		_ = h.LogStore.RecordBatch(r.Context(), batchID, count)
	}

	if count > 0 {
		h.ensureLogsConnector(r.Context())
		if h.WatchStream != nil {
			go h.WatchStream.OnLogsReceived(logEntries)
		}
		// Upsert error groups and track impact for entries with error_fingerprint.
		if h.ErrorGroupStore != nil {
			for _, e := range logEntries {
				if e.ErrorFingerprint != "" {
					_ = h.ErrorGroupStore.Upsert(r.Context(), e)
					// Track user impact if we have a user ID
					if h.ErrorImpactStore != nil && e.UserID != "" {
						_ = h.ErrorImpactStore.TrackImpact(r.Context(), e.ErrorFingerprint, e.UserID, e.Metadata, e.ID, e.Service)
					}
					}
			}
		}
		// Populate code entities from error log stack traces (Stage 5).
		if h.CodeEntityStore != nil {
			for _, e := range logEntries {
				if e.Level == "error" || e.Level == "fatal" {
					go mcpserver.PopulateFromErrorLog(context.Background(), h.CodeEntityStore, e)
				}
			}
		}
		// Update distributed trace reassembly status for entries with a trace_id.
		if h.TraceStore != nil {
			for _, e := range logEntries {
				if e.TraceID != "" {
					_ = h.TraceStore.UpsertTraceStatus(r.Context(), e.TraceID, e)
				}
			}
		}
	}

	status := http.StatusCreated
	if count == 0 {
		status = http.StatusOK
	}
	resp := map[string]any{"count": count}
	if len(logEntries) < originalCount {
		resp["sampled"] = true
	}
	server.WriteJSON(w, status, resp)
}

// ensureLogsConnector auto-creates and registers a logs connector if one
// doesn't already exist. Called after each successful log ingestion so the
// AI agent can use the log_search tool. Uses a mutex instead of sync.Once
// so it can retry after transient failures or re-register after deletion.
func (h *Handler) ensureLogsConnector(ctx context.Context) {
	// Fast path: already registered in memory (no lock needed)
	if h.Registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	h.logsConnMu.Lock()
	defer h.logsConnMu.Unlock()

	// Double-check after acquiring lock
	if h.Registry.Get(connector.ConnectorLogs) != nil {
		return
	}

	// Check if a logs data source row already exists in the DB
	sources, err := h.DSStore.List(ctx, store.ListDataSourceParams{Type: store.ConnectorLogs})
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
		created, err := h.DSStore.Create(ctx, store.CreateDataSourceParams{
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
	lc := connector.NewLogsConnector(h.LogStore)
	h.Registry.Register(lc)

	// Update DB status to connected
	connected := store.StatusConnected
	if _, err := h.DSStore.Update(ctx, dsID.ID, store.UpdateDataSourceParams{
		Status: &connected,
	}); err != nil {
		slog.Warn("ensureLogsConnector: failed to update status", "error", err)
	}

	slog.Info("auto-registered logs connector", "data_source_id", dsID.ID)
}
