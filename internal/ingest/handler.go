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
	"github.com/adham90/opentrace/internal/safe"
	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/vmihailenco/msgpack/v5"
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
	Ts               string                 `json:"ts" msgpack:"ts"`
	Level            string                 `json:"level" msgpack:"level"`
	Service          string                 `json:"service" msgpack:"service"`
	Env              string                 `json:"env" msgpack:"env"`
	Version          string                 `json:"version" msgpack:"version"`
	Message          string                 `json:"message" msgpack:"message"`
	EventType        string                 `json:"event_type" msgpack:"event_type"`
	TraceID          string                 `json:"trace_id" msgpack:"trace_id"`
	SpanID           string                 `json:"span_id" msgpack:"span_id"`
	ParentSpanID     string                 `json:"parent_span_id" msgpack:"parent_span_id"`
	RequestID        string                 `json:"request_id" msgpack:"request_id"`
	UserID           string                 `json:"user_id" msgpack:"user_id"`
	TenantID         string                 `json:"tenant_id" msgpack:"tenant_id"`
	SessionID        string                 `json:"session_id" msgpack:"session_id"`
	Method           string                 `json:"method" msgpack:"method"`
	Path             string                 `json:"path" msgpack:"path"`
	Controller       string                 `json:"controller" msgpack:"controller"`
	Status           int                    `json:"status" msgpack:"status"`
	DurationMs       int                    `json:"duration_ms" msgpack:"duration_ms"`
	DbMs             int                    `json:"db_ms" msgpack:"db_ms"`
	DbCount          int                    `json:"db_count" msgpack:"db_count"`
	NPlusOne         *bool                  `json:"n_plus_one" msgpack:"n_plus_one"`
	SlowQueries      int                    `json:"slow_queries" msgpack:"slow_queries"`
	DupQueries       int                    `json:"dup_queries" msgpack:"dup_queries"`
	ErrorClass       string                 `json:"error_class" msgpack:"error_class"`
	ErrorMessage     string                 `json:"error_message" msgpack:"error_message"`
	SourceFile       string                 `json:"source_file" msgpack:"source_file"`
	SourceLine       int                    `json:"source_line" msgpack:"source_line"`
	Body             json.RawMessage        `json:"body,omitempty" msgpack:"body,omitempty"`

	// Resolved timestamp (not in JSON — computed from Ts)
	timestamp time.Time
}

// resolveTimestamp parses the "ts" field into a time.Time.
func (e *ingestLogEntry) resolveTimestamp() error {
	if e.Ts != "" {
		t, err := time.Parse(time.RFC3339Nano, e.Ts)
		if err != nil {
			return fmt.Errorf("invalid ts format (use RFC3339): %v", err)
		}
		e.timestamp = t
		return nil
	}
	e.timestamp = time.Now().UTC()
	return nil
}

type ingestRequestSummary struct {
	Controller          string  `json:"controller" msgpack:"controller"`
	Action              string  `json:"action" msgpack:"action"`
	Method              string  `json:"method" msgpack:"method"`
	Path                string  `json:"path" msgpack:"path"`
	Status              int     `json:"status" msgpack:"status"`
	DurationMs          float64 `json:"duration_ms" msgpack:"duration_ms"`
	DBTimeMs            float64 `json:"db_time_ms" msgpack:"db_time_ms"`
	ViewTimeMs          float64 `json:"view_time_ms" msgpack:"view_time_ms"`
	SQLCount            int     `json:"sql_count" msgpack:"sql_count"`
	SQLTotalMs          float64 `json:"sql_total_ms" msgpack:"sql_total_ms"`
	SQLSlowestMs        float64 `json:"sql_slowest_ms" msgpack:"sql_slowest_ms"`
	SQLSlowestName      string  `json:"sql_slowest_name" msgpack:"sql_slowest_name"`
	NPlusOne            bool    `json:"n_plus_one" msgpack:"n_plus_one"`
	ViewCount           int     `json:"view_count" msgpack:"view_count"`
	ViewTotalMs         float64 `json:"view_total_ms" msgpack:"view_total_ms"`
	ViewSlowestMs       float64 `json:"view_slowest_ms" msgpack:"view_slowest_ms"`
	ViewSlowestTemplate string  `json:"view_slowest_template" msgpack:"view_slowest_template"`
	CacheReads          int     `json:"cache_reads" msgpack:"cache_reads"`
	CacheHits           int     `json:"cache_hits" msgpack:"cache_hits"`
	CacheWrites         int     `json:"cache_writes" msgpack:"cache_writes"`
	CacheHitRatio       float64 `json:"cache_hit_ratio" msgpack:"cache_hit_ratio"`
	HTTPExternalCount   int     `json:"http_external_count" msgpack:"http_external_count"`
	HTTPExternalTotalMs float64 `json:"http_external_total_ms" msgpack:"http_external_total_ms"`
	HTTPSlowestMs       float64 `json:"http_slowest_ms" msgpack:"http_slowest_ms"`
	HTTPSlowestHost     string  `json:"http_slowest_host" msgpack:"http_slowest_host"`
	MemoryBeforeMb      float64         `json:"memory_before_mb" msgpack:"memory_before_mb"`
	MemoryAfterMb       float64         `json:"memory_after_mb" msgpack:"memory_after_mb"`
	MemoryDeltaMb       float64         `json:"memory_delta_mb" msgpack:"memory_delta_mb"`
	Timeline            json.RawMessage `json:"timeline,omitempty" msgpack:"timeline,omitempty"`
	TimeBreakdown       json.RawMessage `json:"time_breakdown,omitempty" msgpack:"time_breakdown,omitempty"`
	DuplicateQueries    int             `json:"duplicate_queries" msgpack:"duplicate_queries"`
	WorstDuplicateCount int             `json:"worst_duplicate_count" msgpack:"worst_duplicate_count"`
	TopDuplicates       json.RawMessage `json:"top_duplicates,omitempty" msgpack:"top_duplicates,omitempty"`
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

	contentType := r.Header.Get("Content-Type")

	var entries []ingestLogEntry
	switch {
	case strings.Contains(contentType, "application/msgpack"):
		// MessagePack decoding
		if err := msgpack.Unmarshal(body, &entries); err != nil {
			// Try single object
			var single ingestLogEntry
			if err2 := msgpack.Unmarshal(body, &single); err2 != nil {
				server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid msgpack: %v", err2))
				return
			}
			entries = []ingestLogEntry{single}
		}
	default:
		// JSON decoding (existing behavior, default for all other content types)
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
	}

	// Validate required fields, resolve timestamps, normalize levels
	validLevels := map[string]bool{
		"debug": true, "info": true, "warn": true, "warning": true,
		"error": true, "fatal": true,
	}
	for i := range entries {
		e := &entries[i]
		if err := e.resolveTimestamp(); err != nil {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: %v", i, err))
			return
		}
		var missing []string
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
		e.Level = strings.ToLower(e.Level)
		if !validLevels[e.Level] {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: 'level' must be one of: debug, info, warn, error, fatal (got %q)", i, e.Level))
			return
		}
		e.Level = normalizeLevel(e.Level)
	}

	// Check for duplicate batch
	batchID, done := h.checkBatchDup(w, r)
	if done {
		return
	}

	logEntries := make([]store.LogEntry, len(entries))
	for i, e := range entries {
		// Build metadata from Body JSON blob if present
		var metadata map[string]any
		var metadataJSON string
		if len(e.Body) > 0 {
			metadata = make(map[string]any)
			_ = json.Unmarshal(e.Body, &metadata)
			metadataJSON = string(e.Body)
		}

		// Stamp missing env with the server-configured default so downstream
		// env filters match. Empty env used to mean "legacy/unscoped"; the
		// ingest layer is the one place we canonicalise it.
		env := e.Env
		if env == "" && h.Cfg != nil {
			env = h.Cfg.DefaultEnv
		}

		entry := store.LogEntry{
			Timestamp:      e.timestamp,
			Level:          e.Level,
			Service:        e.Service,
			Environment:    env,
			CommitHash:     e.Version,
			TraceID:        e.TraceID,
			SpanID:         e.SpanID,
			ParentSpanID:   e.ParentSpanID,
			RequestID:      e.RequestID,
			Message:        e.Message,
			EventType:      e.EventType,
			ExceptionClass: e.ErrorClass,
			SourceFile:     e.SourceFile,
			SourceLine:     e.SourceLine,
			Metadata:       metadata,
			MetadataJSON:   metadataJSON,
		}

		// Server-side fingerprinting
		if fp := GenerateErrorFingerprint(&entry); fp != "" {
			entry.ErrorFingerprint = fp
		}

		// Build RequestSummary from flat fields if this is an http.request event
		if e.Method != "" || e.Path != "" || e.Status > 0 || e.DurationMs > 0 {
			nplusone := false
			if e.NPlusOne != nil {
				nplusone = *e.NPlusOne
			}
			entry.RequestSummary = &store.RequestSummary{
				Controller: e.Controller,
				Method:     e.Method,
				Path:       e.Path,
				Status:     e.Status,
				DurationMs: float64(e.DurationMs),
				DBTimeMs:   float64(e.DbMs),
				SQLCount:   e.DbCount,
				NPlusOne:   nplusone,
			}
		}

		logEntries[i] = entry
	}

	h.storeAndRespond(w, r, logEntries, batchID)
}

// checkBatchDup reads and validates the X-Batch-ID header. It returns the batch
// ID and done=true if the response has already been written (invalid ID → 400,
// or a previously-seen batch → 200 deduplicated). Shared by both ingest handlers
// so SDK retries are idempotent regardless of payload format.
func (h *Handler) checkBatchDup(w http.ResponseWriter, r *http.Request) (batchID string, done bool) {
	batchID = r.Header.Get("X-Batch-ID")
	if batchID == "" {
		return "", false
	}
	if !IsValidBatchID(batchID) {
		server.WriteError(w, http.StatusBadRequest, "invalid X-Batch-ID format (expected UUID)")
		return "", true
	}
	if existing, err := h.LogStore.GetBatch(r.Context(), batchID); err == nil && existing != nil {
		server.WriteJSON(w, http.StatusOK, map[string]any{
			"count":        existing.LogCount,
			"deduplicated": true,
		})
		return batchID, true
	}
	return batchID, false
}

// storeAndRespond applies sampling rules, inserts the entries (via the async
// queue when configured), records the batch ID, fires post-insert side-effects
// (error grouping, watch evaluation), and writes the ingest response. Shared by
// the nested (/api/logs) and flat (/api/v2/logs) handlers so both SDKs get
// identical treatment.
func (h *Handler) storeAndRespond(w http.ResponseWriter, r *http.Request, logEntries []store.LogEntry, batchID string) {
	originalCount := len(logEntries)
	if h.SettingsStore != nil {
		rules, _ := h.SettingsStore.GetSamplingRules(r.Context())
		if len(rules) > 0 {
			logEntries = ApplySamplingRules(logEntries, rules)
		}
	}

	// Use async ingest queue if available; otherwise fall back to synchronous insert
	var count int
	var err error
	if h.Queue != nil {
		count, err = h.Queue.Enqueue(r.Context(), logEntries)
	} else {
		count, err = h.LogStore.BatchInsert(r.Context(), logEntries)
	}
	if err != nil {
		server.WriteError(w, http.StatusInternalServerError, "failed to insert logs")
		return
	}

	if count > 0 {
		metrics.RecordLogIngest(count)
	}

	if batchID != "" {
		_ = h.LogStore.RecordBatch(r.Context(), batchID, count)
	}

	if count > 0 {
		h.ensureLogsConnector(r.Context())

		// All post-insert side-effects run async to keep the HTTP response fast.
		// They use context.Background() since r.Context() is canceled after response.
		// Panic-isolated: these parse attacker-controlled log bodies/stacks, and a
		// panic here must not crash the server.
		safe.Go("ingest.processAfterInsert", func() { h.processAfterInsert(logEntries) })
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
	// Requires the connector registry and data-source store; skip when either is
	// absent (minimal/embedded configs and unit tests wire only the log store).
	if h.Registry == nil || h.DSStore == nil {
		return
	}
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

// processAfterInsert runs all post-ingestion side-effects in a single goroutine.
// Uses context.Background() since the HTTP request context is already done.
func (h *Handler) processAfterInsert(entries []store.LogEntry) {
	ctx := context.Background()

	// Evaluate watch rules reactively.
	if h.WatchStream != nil {
		h.WatchStream.OnLogsReceived(entries)
	}

	// Upsert error groups and track user impact.
	if h.ErrorGroupStore != nil {
		for _, e := range entries {
			if e.ErrorFingerprint == "" {
				continue
			}
			_ = h.ErrorGroupStore.Upsert(ctx, e)
			if h.ErrorImpactStore != nil && e.UserID != "" {
				_ = h.ErrorImpactStore.TrackImpact(ctx, e.ErrorFingerprint, e.Environment, e.UserID, e.Metadata, e.ID, e.Service)
			}
		}
	}

	// Populate code entities from error stack traces.
	if h.CodeEntityStore != nil {
		for _, e := range entries {
			if e.Level == "error" || e.Level == "fatal" {
				mcpserver.PopulateFromErrorLog(ctx, h.CodeEntityStore, e)
			}
		}
	}

	// Update distributed trace reassembly status.
	if h.TraceStore != nil {
		for _, e := range entries {
			if e.TraceID != "" {
				_ = h.TraceStore.UpsertTraceStatus(ctx, e.TraceID, e)
			}
		}
	}
}
