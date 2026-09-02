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

// flatEntry is the JSON structure sent by the SDKs and by any hand-written or
// agent-generated client (45 fields).
//
// The `doc` tags are the single source of truth for GET /spec (see spec.go), so
// the published contract cannot drift from the struct that parses it. Every
// field must carry a doc tag, and every string field must be listed in either
// fieldCaps or uncappedStrings — spec_test.go fails otherwise.
type flatEntry struct {
	Ts           string          `json:"ts" doc:"Event time, RFC3339 with optional fractional seconds (2026-04-04T10:15:30.123Z). Defaults to server receive time when omitted — send it, or queued entries are stamped with their flush time instead of when they happened."`
	Level        string          `json:"level" doc:"Severity. One of: debug, info, warn, error, fatal. 'warning' is accepted on the wire and stored as 'warn'."`
	Service      string          `json:"service" doc:"Logical application name, e.g. 'checkout-api'. Constant for the life of the process; it is the primary grouping key in every query."`
	Message      string          `json:"message" doc:"Human-readable line. Keep varying values (ids, counts, durations) out of it and in body — this string is what error fingerprinting hashes, so interpolated values split one error into thousands of groups."`
	Env          string          `json:"env,omitempty" doc:"Deployment environment: production, staging, development. Falls back to the server's configured default when omitted."`
	Version      string          `json:"version,omitempty" doc:"Build identifier, git commit SHA preferred. Deploy-impact and regression analysis join on this."`
	Host         string          `json:"host,omitempty" doc:"Machine, container or pod that produced the entry."`
	Kind         string          `json:"kind,omitempty" doc:"Row discriminator: log, request, job or event. Inferred from the fields present when omitted (see the notes under 'Kind' below), and several fields are only stored on the matching kind."`
	EventType    string          `json:"event_type,omitempty" doc:"Dotted event name: http.request, job.perform, mail.deliver. Free-form — use a stable vocabulary per service so it stays aggregatable."`
	TraceID      string          `json:"trace_id,omitempty" doc:"Distributed trace id shared by every entry in one trace. Any stable string; 16-byte hex if you have no existing convention."`
	SpanID       string          `json:"span_id,omitempty" doc:"Id of the unit of work this entry belongs to."`
	ParentSpanID string          `json:"parent_span_id,omitempty" doc:"span_id of the enclosing unit of work. Lets the server rebuild the trace tree."`
	RequestID    string          `json:"request_id,omitempty" doc:"Per-request correlation id (typically the X-Request-Id header). Ties in-request log lines to the request row that produced them."`
	UserID       string          `json:"user_id,omitempty" doc:"End-user identifier. Drives the 'users affected' count on error groups. Send an opaque id, not an email address."`
	TenantID     string          `json:"tenant_id,omitempty" doc:"Tenant, organisation or account id for multi-tenant apps."`
	SessionID    string          `json:"session_id,omitempty" doc:"Session identifier, for grouping one user's activity over time."`
	Method       string          `json:"method,omitempty" doc:"HTTP method, uppercase: GET, POST. Stored on kind=request rows only."`
	Path         string          `json:"path,omitempty" doc:"Concrete request path with real values (/users/12345). Stored on kind=request rows only; sensitive segments are scrubbed server-side."`
	Route        string          `json:"route,omitempty" doc:"Route pattern with placeholders (/users/:id). Endpoint latency and error rates aggregate on this, not on path — without it every distinct URL looks like its own endpoint."`
	Handler      string          `json:"handler,omitempty" doc:"Code that handled the request: controller#action, function or module name. Stored on kind=request rows only."`
	Status       jsonInt         `json:"status,omitempty" doc:"HTTP response status code."`
	DurationMs   jsonInt         `json:"duration_ms,omitempty" doc:"Total wall time in milliseconds. Fractional values are accepted and rounded to the nearest millisecond."`
	DbMs         jsonInt         `json:"db_ms,omitempty" doc:"Milliseconds spent executing database queries."`
	DbCount      jsonInt         `json:"db_count,omitempty" doc:"Number of database queries executed."`
	CacheMs      jsonInt         `json:"cache_ms,omitempty" doc:"Milliseconds spent in cache reads and writes."`
	CacheHits    jsonInt         `json:"cache_hits,omitempty" doc:"Number of cache hits."`
	CacheMisses  jsonInt         `json:"cache_misses,omitempty" doc:"Number of cache misses."`
	ExtMs        jsonInt         `json:"ext_ms,omitempty" doc:"Milliseconds spent in outbound HTTP and third-party API calls."`
	ExtCount     jsonInt         `json:"ext_count,omitempty" doc:"Number of outbound HTTP and third-party API calls."`
	RenderMs     jsonInt         `json:"render_ms,omitempty" doc:"Milliseconds spent rendering views or templates."`
	AllocCount   jsonInt         `json:"alloc_count,omitempty" doc:"Objects allocated while handling this unit of work, if your runtime can report it. Omit rather than guess."`
	MemDeltaMb   jsonFloat       `json:"mem_delta_mb,omitempty" doc:"Process memory growth in MB over this unit of work. Fractional; stored to two decimal places."`
	NPlusOne     *bool           `json:"n_plus_one,omitempty" doc:"true when a repeated-query (N+1) pattern was detected. Stored on kind=request rows only."`
	SlowQueries  jsonInt         `json:"slow_queries,omitempty" doc:"Number of queries that exceeded your slow-query threshold."`
	DupQueries   jsonInt         `json:"dup_queries,omitempty" doc:"Number of identical queries executed more than once. Stored on kind=request rows only."`
	ErrorClass   string          `json:"error_class,omitempty" doc:"Exception class name: ActiveRecord::RecordNotFound, TypeError. Part of the error fingerprint, so keep it stable — never append a message to it."`
	ErrorMessage string          `json:"error_message,omitempty" doc:"Exception message."`
	SourceFile   string          `json:"source_file,omitempty" doc:"Path of the file where the error was raised, relative to the repository root where possible — the agent uses it to open the code."`
	SourceLine   jsonInt         `json:"source_line,omitempty" doc:"Line number where the error was raised."`
	JobClass     string          `json:"job_class,omitempty" doc:"Background job class or handler name."`
	JobQueue     string          `json:"job_queue,omitempty" doc:"Name of the queue the job ran on."`
	JobID        string          `json:"job_id,omitempty" doc:"Unique id of this job execution."`
	QueueMs      jsonInt         `json:"queue_ms,omitempty" doc:"Milliseconds the job spent waiting in the queue before it started running."`
	Body         json.RawMessage `json:"body,omitempty" doc:"Free-form JSON object for everything the columns do not cover. Stored whole and searchable. Well-known keys the MCP tools read: backtrace (string), exception_causes, handled (boolean), source_context (source lines around source_line), params, queries, and the deep-capture arrays sql / http / email / file / audit / timeline. body.http carries one row per outbound call (method, url, host, vendor, status, duration_ms) plus ai_model / ai_input_tokens / ai_output_tokens when the call was to an LLM provider. See GET /api/v2/logs/spec."`
}

// HandleFlatIngest is the HTTP handler for POST /api/v2/logs. It accepts the
// flat SDK format and routes it through the SAME pipeline as the nested
// /api/logs handler (sampling, insert, batch dedup, error grouping, watch
// evaluation) so the flat-format SDKs get identical treatment. Previously this
// endpoint was never even mounted, so those SDKs' logs were dropped entirely.
func (h *Handler) HandleFlatIngest(w http.ResponseWriter, r *http.Request) {
	// Dry run: report what this payload would become and store nothing. Checked
	// after the body is read so validate mode sees exactly the same bytes the
	// real path would. See validate.go.
	if isValidateRequest(r) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			server.WriteError(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		h.handleValidate(w, body)
		return
	}

	batchID, done := h.checkBatchDup(w, r)
	if done {
		return
	}

	entries, err := decodeJSONOneOrMany[flatEntry](r.Body)
	if err != nil {
		server.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	logEntries := make([]store.LogEntry, 0, len(entries))
	for i, fe := range entries {
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
		Kind:         truncateField(deriveKind(fe), maxIdentFieldBytes),
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
