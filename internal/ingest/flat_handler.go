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
	"github.com/vmihailenco/msgpack/v5"
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

// DecodeMsgpack gives jsonInt the same float tolerance over MessagePack that
// UnmarshalJSON gives it over JSON. The Ruby SDK posts msgpack and reports
// sub-millisecond timings as floats; without this a single fractional value
// failed to decode into an int field and rejected the whole batch.
func (n *jsonInt) DecodeMsgpack(dec *msgpack.Decoder) error {
	v, err := dec.DecodeInterfaceLoose()
	if err != nil {
		return err
	}
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		*n = jsonInt(t)
	case uint64:
		*n = jsonInt(t)
	case float64:
		*n = jsonInt(math.Round(t))
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return fmt.Errorf("expected a number, got %q", t)
		}
		*n = jsonInt(math.Round(f))
	default:
		return fmt.Errorf("expected a number, got %T", v)
	}
	return nil
}

// jsonFloat is jsonInt's tolerance without its rounding, for wire values whose
// fraction is meaningful. mem_delta_mb is the case: the column stores
// hundredths of a MB, so rounding 1.7 to 2 at parse time throws away exactly
// the precision the column exists for.
type jsonFloat float64

func (n *jsonFloat) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*n = jsonFloat(f)
	return nil
}

func (n *jsonFloat) DecodeMsgpack(dec *msgpack.Decoder) error {
	v, err := dec.DecodeInterfaceLoose()
	if err != nil {
		return err
	}
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		*n = jsonFloat(t)
	case uint64:
		*n = jsonFloat(t)
	case float64:
		*n = jsonFloat(t)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return fmt.Errorf("expected a number, got %q", t)
		}
		*n = jsonFloat(f)
	default:
		return fmt.Errorf("expected a number, got %T", v)
	}
	return nil
}

// flatEntry is the JSON structure sent by the new SDK (45 fields).
type flatEntry struct {
	Ts           string          `json:"ts"`
	Level        string          `json:"level"`
	Service      string          `json:"service"`
	Message      string          `json:"message"`
	Env          string          `json:"env,omitempty"`
	Version      string          `json:"version,omitempty"`
	Host         string          `json:"host,omitempty"`
	Kind         string          `json:"kind,omitempty"`
	EventType    string          `json:"event_type,omitempty"`
	TraceID      string          `json:"trace_id,omitempty"`
	SpanID       string          `json:"span_id,omitempty"`
	ParentSpanID string          `json:"parent_span_id,omitempty"`
	RequestID    string          `json:"request_id,omitempty"`
	UserID       string          `json:"user_id,omitempty"`
	TenantID     string          `json:"tenant_id,omitempty"`
	SessionID    string          `json:"session_id,omitempty"`
	Method       string          `json:"method,omitempty"`
	Path         string          `json:"path,omitempty"`
	Route        string          `json:"route,omitempty"`
	Handler      string          `json:"handler,omitempty"`
	Status       jsonInt         `json:"status,omitempty"`
	DurationMs   jsonInt         `json:"duration_ms,omitempty"`
	DbMs         jsonInt         `json:"db_ms,omitempty"`
	DbCount      jsonInt         `json:"db_count,omitempty"`
	CacheMs      jsonInt         `json:"cache_ms,omitempty"`
	CacheHits    jsonInt         `json:"cache_hits,omitempty"`
	CacheMisses  jsonInt         `json:"cache_misses,omitempty"`
	ExtMs        jsonInt         `json:"ext_ms,omitempty"`
	ExtCount     jsonInt         `json:"ext_count,omitempty"`
	RenderMs     jsonInt         `json:"render_ms,omitempty"`
	AllocCount   jsonInt         `json:"alloc_count,omitempty"`
	MemDeltaMb   jsonFloat       `json:"mem_delta_mb,omitempty"`
	NPlusOne     *bool           `json:"n_plus_one,omitempty"`
	SlowQueries  jsonInt         `json:"slow_queries,omitempty"`
	DupQueries   jsonInt         `json:"dup_queries,omitempty"`
	ErrorClass   string          `json:"error_class,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	SourceFile   string          `json:"source_file,omitempty"`
	SourceLine   jsonInt         `json:"source_line,omitempty"`
	JobClass     string          `json:"job_class,omitempty"`
	JobQueue     string          `json:"job_queue,omitempty"`
	JobID        string          `json:"job_id,omitempty"`
	QueueMs      jsonInt         `json:"queue_ms,omitempty"`
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
		// Same level validation as /api/logs. Unknown levels used to be stored
		// verbatim here, which silently disabled error grouping, keep-errors
		// sampling and every level filter for those rows.
		fe.Level = strings.ToLower(fe.Level)
		if !isValidLevel(fe.Level) {
			server.WriteError(w, http.StatusBadRequest,
				fmt.Sprintf("entry %d: 'level' must be one of: %s (got %q)", i, validLevelList, fe.Level))
			return
		}

		ts := time.Now().UTC()
		if fe.Ts != "" {
			t, err := time.Parse(time.RFC3339Nano, fe.Ts)
			if err != nil {
				server.WriteError(w, http.StatusBadRequest, fmt.Sprintf("entry %d: invalid ts format (use RFC3339): %v", i, err))
				return
			}
			ts = t
		}

		logEntries = append(logEntries, h.flatToLogEntry(fe, ts))
	}

	h.storeAndRespond(w, r, logEntries, batchID)
}

// flatToLogEntry converts a parsed flat SDK entry into the canonical
// store.LogEntry consumed by the shared ingest pipeline. This is the SINGLE
// wire→model mapping for both ingest endpoints (/api/logs converts its nested
// payload to a flatEntry first) so the two can never drift apart again.
//
// Every field the SDK sends and the columnar store has a column for is mapped
// here; anything dropped is dropped on purpose. The rich request/job detail the
// SDK packs into `body` is preserved as metadata. String fields are capped at
// the boundary (see limits.go). Timestamp is passed in already resolved.
func (h *Handler) flatToLogEntry(fe flatEntry, ts time.Time) store.LogEntry {
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
		Timestamp:    ts,
		Level:        normalizeLevel(strings.ToLower(fe.Level)),
		Service:      truncateField(fe.Service, maxIdentFieldBytes),
		Environment:  truncateField(env, maxIdentFieldBytes),
		CommitHash:   truncateField(fe.Version, maxIdentFieldBytes),
		Host:         truncateField(fe.Host, maxIdentFieldBytes),
		Kind:         deriveKind(fe),
		TraceID:      truncateField(fe.TraceID, maxIdentFieldBytes),
		SpanID:       truncateField(fe.SpanID, maxIdentFieldBytes),
		ParentSpanID: truncateField(fe.ParentSpanID, maxIdentFieldBytes),
		RequestID:    truncateField(fe.RequestID, maxIdentFieldBytes),
		UserID:       truncateField(fe.UserID, maxIdentFieldBytes),
		TenantID:     truncateField(fe.TenantID, maxIdentFieldBytes),
		SessionID:    truncateField(fe.SessionID, maxIdentFieldBytes),
		Message:      truncateField(fe.Message, maxMessageBytes),
		EventType:    truncateField(fe.EventType, maxIdentFieldBytes),

		Route:       truncateField(fe.Route, maxPathFieldBytes),
		CacheMs:     int(fe.CacheMs),
		CacheHits:   int(fe.CacheHits),
		CacheMisses: int(fe.CacheMisses),
		ExtMs:       int(fe.ExtMs),
		ExtCount:    int(fe.ExtCount),
		RenderMs:    int(fe.RenderMs),
		AllocCount:  int(fe.AllocCount),
		// The column is hundredths of a MB. Writing raw MB here read back 100x
		// too small, while the RequestSummary path scaled correctly — the same
		// column disagreeing with itself depending on which handler wrote it.
		MemDeltaMb:  int(math.Round(float64(fe.MemDeltaMb) * store.MemDeltaScale)),
		SlowQueries: int(fe.SlowQueries),

		ExceptionClass: truncateField(fe.ErrorClass, maxIdentFieldBytes),
		ErrorMessage:   truncateField(fe.ErrorMessage, maxMessageBytes),
		SourceFile:     truncateField(fe.SourceFile, maxPathFieldBytes),
		SourceLine:     int(fe.SourceLine),

		JobClass: truncateField(fe.JobClass, maxIdentFieldBytes),
		JobQueue: truncateField(fe.JobQueue, maxIdentFieldBytes),
		JobID:    truncateField(fe.JobID, maxIdentFieldBytes),
		QueueMs:  int(fe.QueueMs),

		Metadata:     metadata,
		MetadataJSON: metadataJSON,
	}
	if fp := GenerateErrorFingerprint(&entry); fp != "" {
		entry.ErrorFingerprint = fp
	}

	// Timing belongs to the row whatever its kind. Request rows carry it on the
	// RequestSummary below; jobs and events had nowhere to put it and lost it
	// entirely, which silently discarded job latency — the whole point of a
	// job.perform payload.
	if entry.Kind != kindRequest {
		entry.DurationMs = float64(fe.DurationMs)
		entry.DbMs = float64(fe.DbMs)
		entry.DbCount = int(fe.DbCount)
		entry.Status = int(fe.Status)
	}

	// A RequestSummary marks the row as an HTTP request in storage (the adapter
	// stamps event_type=http.request off it), so build one only for rows that
	// really are requests. duration_ms alone is not an HTTP signal.
	if entry.Kind == kindRequest {
		nplusone := false
		if fe.NPlusOne != nil {
			nplusone = *fe.NPlusOne
		}
		entry.RequestSummary = &store.RequestSummary{
			Controller:       truncateField(fe.Handler, maxPathFieldBytes),
			Method:           truncateField(fe.Method, maxIdentFieldBytes),
			Path:             truncateField(fe.Path, maxPathFieldBytes),
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
