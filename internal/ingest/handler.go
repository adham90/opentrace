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

// ingestLogEntry is the payload shape accepted by /api/logs (JSON or msgpack).
// It carries the same field set as the flat /api/v2/logs payload — see toFlat,
// which funnels both endpoints through one wire→model mapping. Numeric fields
// use jsonInt so a fractional value (SDKs report sub-millisecond timings as
// floats) does not fail decoding and reject the entire batch.
type ingestLogEntry struct {
	Ts           string          `json:"ts" msgpack:"ts"`
	Level        string          `json:"level" msgpack:"level"`
	Service      string          `json:"service" msgpack:"service"`
	Env          string          `json:"env" msgpack:"env"`
	Version      string          `json:"version" msgpack:"version"`
	Host         string          `json:"host" msgpack:"host"`
	Kind         string          `json:"kind" msgpack:"kind"`
	Message      string          `json:"message" msgpack:"message"`
	EventType    string          `json:"event_type" msgpack:"event_type"`
	TraceID      string          `json:"trace_id" msgpack:"trace_id"`
	SpanID       string          `json:"span_id" msgpack:"span_id"`
	ParentSpanID string          `json:"parent_span_id" msgpack:"parent_span_id"`
	RequestID    string          `json:"request_id" msgpack:"request_id"`
	UserID       string          `json:"user_id" msgpack:"user_id"`
	TenantID     string          `json:"tenant_id" msgpack:"tenant_id"`
	SessionID    string          `json:"session_id" msgpack:"session_id"`
	Method       string          `json:"method" msgpack:"method"`
	Path         string          `json:"path" msgpack:"path"`
	Route        string          `json:"route" msgpack:"route"`
	Controller   string          `json:"controller" msgpack:"controller"`
	Status       jsonInt         `json:"status" msgpack:"status"`
	DurationMs   jsonInt         `json:"duration_ms" msgpack:"duration_ms"`
	DbMs         jsonInt         `json:"db_ms" msgpack:"db_ms"`
	DbCount      jsonInt         `json:"db_count" msgpack:"db_count"`
	CacheMs      jsonInt         `json:"cache_ms" msgpack:"cache_ms"`
	CacheHits    jsonInt         `json:"cache_hits" msgpack:"cache_hits"`
	CacheMisses  jsonInt         `json:"cache_misses" msgpack:"cache_misses"`
	ExtMs        jsonInt         `json:"ext_ms" msgpack:"ext_ms"`
	ExtCount     jsonInt         `json:"ext_count" msgpack:"ext_count"`
	RenderMs     jsonInt         `json:"render_ms" msgpack:"render_ms"`
	AllocCount   jsonInt         `json:"alloc_count" msgpack:"alloc_count"`
	MemDeltaMb   jsonFloat       `json:"mem_delta_mb" msgpack:"mem_delta_mb"`
	NPlusOne     *bool           `json:"n_plus_one" msgpack:"n_plus_one"`
	SlowQueries  jsonInt         `json:"slow_queries" msgpack:"slow_queries"`
	DupQueries   jsonInt         `json:"dup_queries" msgpack:"dup_queries"`
	ErrorClass   string          `json:"error_class" msgpack:"error_class"`
	ErrorMessage string          `json:"error_message" msgpack:"error_message"`
	SourceFile   string          `json:"source_file" msgpack:"source_file"`
	SourceLine   jsonInt         `json:"source_line" msgpack:"source_line"`
	JobClass     string          `json:"job_class" msgpack:"job_class"`
	JobQueue     string          `json:"job_queue" msgpack:"job_queue"`
	JobID        string          `json:"job_id" msgpack:"job_id"`
	QueueMs      jsonInt         `json:"queue_ms" msgpack:"queue_ms"`
	Body         json.RawMessage `json:"body,omitempty" msgpack:"body,omitempty"`

	// Resolved timestamp (not in JSON — computed from Ts)
	timestamp time.Time
}

// toFlat projects the nested-endpoint payload onto the flat SDK shape so both
// endpoints share flatToLogEntry. Field-for-field: the only naming difference
// is `controller` here vs `handler` there.
func (e *ingestLogEntry) toFlat() flatEntry {
	return flatEntry{
		Level:        e.Level,
		Service:      e.Service,
		Message:      e.Message,
		Env:          e.Env,
		Version:      e.Version,
		Host:         e.Host,
		Kind:         e.Kind,
		EventType:    e.EventType,
		TraceID:      e.TraceID,
		SpanID:       e.SpanID,
		ParentSpanID: e.ParentSpanID,
		RequestID:    e.RequestID,
		UserID:       e.UserID,
		TenantID:     e.TenantID,
		SessionID:    e.SessionID,
		Method:       e.Method,
		Path:         e.Path,
		Route:        e.Route,
		Handler:      e.Controller,
		Status:       e.Status,
		DurationMs:   e.DurationMs,
		DbMs:         e.DbMs,
		DbCount:      e.DbCount,
		CacheMs:      e.CacheMs,
		CacheHits:    e.CacheHits,
		CacheMisses:  e.CacheMisses,
		ExtMs:        e.ExtMs,
		ExtCount:     e.ExtCount,
		RenderMs:     e.RenderMs,
		AllocCount:   e.AllocCount,
		MemDeltaMb:   e.MemDeltaMb,
		NPlusOne:     e.NPlusOne,
		SlowQueries:  e.SlowQueries,
		DupQueries:   e.DupQueries,
		ErrorClass:   e.ErrorClass,
		ErrorMessage: e.ErrorMessage,
		SourceFile:   e.SourceFile,
		SourceLine:   e.SourceLine,
		JobClass:     e.JobClass,
		JobQueue:     e.JobQueue,
		JobID:        e.JobID,
		QueueMs:      e.QueueMs,
		Body:         e.Body,
	}
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
		if !isValidLevel(e.Level) {
			server.WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("entry %d: 'level' must be one of: %s (got %q)", i, validLevelList, e.Level))
			return
		}
		e.Level = normalizeLevel(e.Level)
	}

	// Check for duplicate batch
	batchID, done := h.checkBatchDup(w, r)
	if done {
		return
	}

	// Both endpoints share one wire→model mapping (flatToLogEntry) so they can
	// never disagree again about which fields survive ingestion.
	logEntries := make([]store.LogEntry, len(entries))
	for i := range entries {
		logEntries[i] = h.flatToLogEntry(entries[i].toFlat(), entries[i].timestamp)
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

	// Use the async ingest queue when configured, EXCEPT for batches that carry
	// an X-Batch-ID: recording a batch permanently suppresses the SDK's retry of
	// it, so the batch must not be recorded until the data is actually durable.
	// Enqueue only buffers, so acking a buffered batch made a later flush failure
	// unrecoverable — the retry would be answered with {"deduplicated": true}.
	var count int
	var err error
	if h.Queue != nil && batchID == "" {
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
//
// It is called with the SAME slice that was handed to LogStore.BatchInsert, so
// entry IDs assigned by the store are visible here and flow into
// ErrorGroupStore.Upsert (error_groups.last_log_id) and
// ErrorImpactStore.TrackImpact (error_impacts.last_log_id). A store whose
// BatchInsert does not write IDs back into the slice leaves both linkages
// unset — see TestProcessAfterInsert_PropagatesAssignedLogIDs.
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
