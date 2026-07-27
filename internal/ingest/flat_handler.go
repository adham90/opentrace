package ingest

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/adham90/opentrace/pkg/server"
	"github.com/adham90/opentrace/pkg/store"
)

// jsonInt is an int that also accepts a fractional JSON number (e.g. 12.35),
// rounding to the nearest integer. SDKs report sub-millisecond durations as
// floats (performance.now()), but storage is integer milliseconds. Without
// this, a single fractional value made json.Unmarshal fail and rejected the
// entire batch. Tolerant on read; lossless enough for ms-granularity metrics.
type jsonInt int

// normalizeLevel canonicalizes level aliases so downstream exact-match filters
// (e.g. level=warn) don't miss rows. Callers lowercase first.
func normalizeLevel(level string) string {
	if level == "warning" {
		return "warn"
	}
	return level
}

func (n *jsonInt) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = jsonInt(math.Round(f))
	return nil
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
	Handler      string  `json:"handler,omitempty"`
	Status       jsonInt `json:"status,omitempty"`
	DurationMs   jsonInt `json:"duration_ms,omitempty"`
	DbMs         jsonInt `json:"db_ms,omitempty"`
	DbCount      jsonInt `json:"db_count,omitempty"`
	CacheMs      jsonInt `json:"cache_ms,omitempty"`
	CacheHits    jsonInt `json:"cache_hits,omitempty"`
	CacheMisses  jsonInt `json:"cache_misses,omitempty"`
	ExtMs        jsonInt `json:"ext_ms,omitempty"`
	ExtCount     jsonInt `json:"ext_count,omitempty"`
	RenderMs     jsonInt `json:"render_ms,omitempty"`
	AllocCount   jsonInt `json:"alloc_count,omitempty"`
	MemDeltaMb   jsonInt `json:"mem_delta_mb,omitempty"`
	NPlusOne     *bool   `json:"n_plus_one,omitempty"`
	SlowQueries  jsonInt `json:"slow_queries,omitempty"`
	DupQueries   jsonInt `json:"dup_queries,omitempty"`
	ErrorClass   string  `json:"error_class,omitempty"`
	ErrorMessage string  `json:"error_message,omitempty"`
	SourceFile   string  `json:"source_file,omitempty"`
	SourceLine   jsonInt `json:"source_line,omitempty"`
	JobClass     string  `json:"job_class,omitempty"`
	JobQueue     string  `json:"job_queue,omitempty"`
	JobID        string  `json:"job_id,omitempty"`
	QueueMs      jsonInt `json:"queue_ms,omitempty"`
	Body         json.RawMessage `json:"body,omitempty"`
}

// HandleFlatIngest is the HTTP handler for POST /api/v2/logs. It accepts the
// flat SDK format and routes it through the SAME pipeline as the nested
// /api/logs handler (sampling, insert, batch dedup, error grouping, watch
// evaluation) so the flat-format SDKs get identical treatment. Previously this
// endpoint was never even mounted, so those SDKs' logs were dropped entirely.
func (h *Handler) HandleFlatIngest(w http.ResponseWriter, r *http.Request) {
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
	} else if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	batchID, done := h.checkBatchDup(w, r)
	if done {
		return
	}

	logEntries := make([]store.LogEntry, 0, len(raw))
	for i, rm := range raw {
		var fe flatEntry
		if err := json.Unmarshal(rm, &fe); err != nil {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: %v", i, err))
			return
		}
		if fe.Level == "" || fe.Message == "" {
			server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: level and message are required", i))
			return
		}

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

		logEntries = append(logEntries, h.flatToLogEntry(fe, tsMs))
	}

	h.storeAndRespond(w, r, logEntries, batchID)
}

// flatToLogEntry converts a parsed flat SDK entry into the canonical
// store.LogEntry consumed by the shared ingest pipeline. The rich request/job
// detail the SDK packs into `body` is preserved as metadata; the core request
// metrics map onto RequestSummary. Timestamp is passed in already resolved.
func (h *Handler) flatToLogEntry(fe flatEntry, tsMs int64) store.LogEntry {
	var metadata map[string]any
	var metadataJSON string
	if len(fe.Body) > 0 {
		metadata = make(map[string]any)
		_ = json.Unmarshal(fe.Body, &metadata)
		metadataJSON = string(fe.Body)
	}

	env := fe.Env
	if env == "" && h.Cfg != nil {
		env = h.Cfg.DefaultEnv
	}

	entry := store.LogEntry{
		Timestamp:      time.UnixMilli(tsMs),
		Level:          normalizeLevel(strings.ToLower(fe.Level)),
		Service:        fe.Service,
		Environment:    env,
		CommitHash:     fe.Version,
		TraceID:        fe.TraceID,
		SpanID:         fe.SpanID,
		ParentSpanID:   fe.ParentSpanID,
		RequestID:      fe.RequestID,
		UserID:         fe.UserID,
		Message:        fe.Message,
		EventType:      fe.EventType,
		ExceptionClass: fe.ErrorClass,
		SourceFile:     fe.SourceFile,
		SourceLine:     int(fe.SourceLine),
		Metadata:       metadata,
		MetadataJSON:   metadataJSON,
	}
	if fp := GenerateErrorFingerprint(&entry); fp != "" {
		entry.ErrorFingerprint = fp
	}

	if fe.Method != "" || fe.Path != "" || fe.Status > 0 || fe.DurationMs > 0 {
		nplusone := false
		if fe.NPlusOne != nil {
			nplusone = *fe.NPlusOne
		}
		entry.RequestSummary = &store.RequestSummary{
			Controller:       fe.Handler,
			Method:           fe.Method,
			Path:             fe.Path,
			Status:           int(fe.Status),
			DurationMs:       float64(fe.DurationMs),
			DBTimeMs:         float64(fe.DbMs),
			SQLCount:         int(fe.DbCount),
			NPlusOne:         nplusone,
			DuplicateQueries: int(fe.DupQueries),
		}
	}
	return entry
}
