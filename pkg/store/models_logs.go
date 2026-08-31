package store

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// MemDeltaScale is the fixed-point factor for LogEntry.MemDeltaMb: the column
// holds hundredths of a megabyte, so 1.70 MB is stored as 170. It lives here,
// beside the field, because it is a storage contract every writer shares.
const MemDeltaScale = 100

// LogEntry represents an ingested log line.
type LogEntry struct {
	bun.BaseModel    `bun:"table:logs" json:"-"`
	ID               int64     `bun:"id,pk,autoincrement" json:"id"`
	Timestamp        time.Time `bun:"timestamp" json:"timestamp"`
	Level            string    `bun:"level" json:"level"`
	Service          string    `bun:"service" json:"service,omitempty"`
	Environment      string    `bun:"environment" json:"environment,omitempty"`
	CommitHash       string    `bun:"commit_hash" json:"commit_hash,omitempty"`
	TraceID          string    `bun:"trace_id" json:"trace_id,omitempty"`
	SpanID           string    `bun:"span_id" json:"span_id,omitempty"`
	ParentSpanID     string    `bun:"parent_span_id" json:"parent_span_id,omitempty"`
	RequestID        string    `bun:"request_id" json:"request_id,omitempty"`
	UserID           string    `bun:"user_id" json:"user_id,omitempty"`
	Message          string    `bun:"message" json:"message"`
	EventType        string    `bun:"event_type" json:"event_type,omitempty"`
	ExceptionClass   string    `bun:"exception_class" json:"exception_class,omitempty"`
	ErrorFingerprint string    `bun:"error_fingerprint" json:"error_fingerprint,omitempty"`
	SourceFile       string    `bun:"source_file" json:"source_file,omitempty"`
	SourceLine       int       `bun:"source_line" json:"source_line,omitempty"`

	// Flat-SDK fields. The columnar log store (internal/logstore/chunk) has a
	// column for each of these and the flat ingest handler parses them off the
	// wire; without them on the canonical entry the values were dropped between
	// the two. Names/types/JSON tags mirror chunk.Entry and the SDK wire format.
	Host        string `bun:"host" json:"host,omitempty"`
	Kind        string `bun:"kind" json:"kind,omitempty"` // log, request, job, event
	TenantID    string `bun:"tenant_id" json:"tenant_id,omitempty"`
	SessionID   string `bun:"session_id" json:"session_id,omitempty"`
	Route       string `bun:"route" json:"route,omitempty"`
	CacheMs     int    `bun:"cache_ms" json:"cache_ms,omitempty"`
	CacheHits   int    `bun:"cache_hits" json:"cache_hits,omitempty"`
	CacheMisses int    `bun:"cache_misses" json:"cache_misses,omitempty"`
	ExtMs       int    `bun:"ext_ms" json:"ext_ms,omitempty"`
	ExtCount    int    `bun:"ext_count" json:"ext_count,omitempty"`
	RenderMs    int    `bun:"render_ms" json:"render_ms,omitempty"`
	AllocCount  int    `bun:"alloc_count" json:"alloc_count,omitempty"`
	// MemDeltaMb is stored as value * MemDeltaScale (e.g. 170 = 1.70 MB),
	// matching the chunk column encoding. Every writer must apply the scale;
	// one path storing raw MB reads back 100x too small.
	MemDeltaMb   int    `bun:"mem_delta_mb" json:"mem_delta_mb,omitempty"`
	SlowQueries  int    `bun:"slow_queries" json:"slow_queries,omitempty"`
	ErrorMessage string `bun:"error_message" json:"error_message,omitempty"`
	JobClass     string `bun:"job_class" json:"job_class,omitempty"`
	JobQueue     string `bun:"job_queue" json:"job_queue,omitempty"`
	JobID        string `bun:"job_id" json:"job_id,omitempty"`
	QueueMs      int    `bun:"queue_ms" json:"queue_ms,omitempty"`

	// Timing and query counts for rows that are NOT http requests — background
	// jobs and events. Request rows carry these on RequestSummary instead, and
	// the adapter prefers that when both are present. Without these, a
	// `job.perform` row reporting duration_ms/db_ms/db_count had them dropped
	// on the way to storage: job latency is exactly what those payloads exist
	// to report.
	DurationMs float64 `bun:"duration_ms" json:"duration_ms,omitempty"`
	DbMs       float64 `bun:"db_ms" json:"db_ms,omitempty"`
	DbCount    int     `bun:"db_count" json:"db_count,omitempty"`
	Status     int     `bun:"status" json:"status,omitempty"`

	Metadata       map[string]any  `bun:"metadata" json:"metadata,omitempty"`
	MetadataJSON   string          `bun:"-" json:"-"` // pre-marshaled metadata; avoids double marshal on hot path
	DeepCapture    json.RawMessage `bun:"-" json:"-"` // carrier field: raw deep capture document for in-tx processing
	RequestSummary *RequestSummary `bun:"rel:has-one,join:id=log_id" json:"request_summary,omitempty"`
	CreatedAt      time.Time       `bun:"created_at" json:"created_at"`
}

// RequestSummary holds structured performance metrics for an HTTP request.
type RequestSummary struct {
	bun.BaseModel       `bun:"table:request_summaries" json:"-"`
	ID                  int64   `bun:"id,pk,autoincrement" json:"id"`
	LogID               int64   `bun:"log_id" json:"log_id"`
	Controller          string  `bun:"controller" json:"controller,omitempty"`
	Action              string  `bun:"action" json:"action,omitempty"`
	Method              string  `bun:"method" json:"method,omitempty"`
	Path                string  `bun:"path" json:"path,omitempty"`
	Status              int     `bun:"status" json:"status,omitempty"`
	DurationMs          float64 `bun:"duration_ms" json:"duration_ms,omitempty"`
	DBTimeMs            float64 `bun:"db_time_ms" json:"db_time_ms,omitempty"`
	ViewTimeMs          float64 `bun:"view_time_ms" json:"view_time_ms,omitempty"`
	SQLCount            int     `bun:"sql_count" json:"sql_count,omitempty"`
	SQLTotalMs          float64 `bun:"sql_total_ms" json:"sql_total_ms,omitempty"`
	SQLSlowestMs        float64 `bun:"sql_slowest_ms" json:"sql_slowest_ms,omitempty"`
	SQLSlowestName      string  `bun:"sql_slowest_name" json:"sql_slowest_name,omitempty"`
	NPlusOne            bool    `bun:"n_plus_one" json:"n_plus_one,omitempty"`
	ViewCount           int     `bun:"view_count" json:"view_count,omitempty"`
	ViewTotalMs         float64 `bun:"view_total_ms" json:"view_total_ms,omitempty"`
	ViewSlowestMs       float64 `bun:"view_slowest_ms" json:"view_slowest_ms,omitempty"`
	ViewSlowestTemplate string  `bun:"view_slowest_template" json:"view_slowest_template,omitempty"`
	CacheReads          int     `bun:"cache_reads" json:"cache_reads,omitempty"`
	CacheHits           int     `bun:"cache_hits" json:"cache_hits,omitempty"`
	CacheWrites         int     `bun:"cache_writes" json:"cache_writes,omitempty"`
	CacheHitRatio       float64 `bun:"cache_hit_ratio" json:"cache_hit_ratio,omitempty"`
	HTTPExternalCount   int     `bun:"http_external_count" json:"http_external_count,omitempty"`
	HTTPExternalTotalMs float64 `bun:"http_external_total_ms" json:"http_external_total_ms,omitempty"`
	HTTPSlowestMs       float64 `bun:"http_slowest_ms" json:"http_slowest_ms,omitempty"`
	HTTPSlowestHost     string  `bun:"http_slowest_host" json:"http_slowest_host,omitempty"`
	MemoryBeforeMb      float64 `bun:"memory_before_mb" json:"memory_before_mb,omitempty"`
	MemoryAfterMb       float64 `bun:"memory_after_mb" json:"memory_after_mb,omitempty"`
	MemoryDeltaMb       float64 `bun:"memory_delta_mb" json:"memory_delta_mb,omitempty"`
	Timeline            string  `bun:"timeline" json:"timeline,omitempty"`
	TimeBreakdown       string  `bun:"time_breakdown" json:"time_breakdown,omitempty"`
	DuplicateQueries    int     `bun:"duplicate_queries" json:"duplicate_queries,omitempty"`
	WorstDuplicateCount int     `bun:"worst_duplicate_count" json:"worst_duplicate_count,omitempty"`
	TopDuplicates       string  `bun:"top_duplicates" json:"top_duplicates,omitempty"`
}

// LogSearchParams defines filters for log search.
type LogSearchParams struct {
	Query       string `json:"query,omitempty"`
	Service     string `json:"service,omitempty"`
	Level       string `json:"level,omitempty"`
	Environment string `json:"environment,omitempty"`
	CommitHash  string `json:"commit_hash,omitempty"`
	TraceID     string `json:"trace_id,omitempty"`
	RequestID   string `json:"request_id,omitempty"`
	// UserID and TenantID answer "what did this customer actually hit", which
	// is the question support work starts from. Both are stored columns; the
	// engine has always filtered on them, they were just never reachable from
	// here.
	UserID           string            `json:"user_id,omitempty"`
	TenantID         string            `json:"tenant_id,omitempty"`
	EventType        string            `json:"event_type,omitempty"`
	ExceptionClass   string            `json:"exception_class,omitempty"`
	ErrorFingerprint string            `json:"error_fingerprint,omitempty"`
	SourceFile       string            `json:"source_file,omitempty"`
	Start            *time.Time        `json:"start,omitempty"`
	End              *time.Time        `json:"end,omitempty"`
	Limit            int               `json:"limit,omitempty"`
	Offset           int               `json:"offset,omitempty"`
	SinceID          int64             `json:"since_id,omitempty"`
	MetadataFilter   map[string]string `json:"metadata_filter,omitempty"` // key-value filters on metadata JSON
	Exclude          map[string]string `json:"exclude,omitempty"`         // field -> comma-separated values to exclude (NOT IN)
	SortAsc          bool              `json:"sort_asc,omitempty"`        // true for oldest-first (default: newest-first)
}

// LogCountParams defines filters for log aggregation queries.
type LogCountParams struct {
	Since       time.Time
	Until       time.Time
	Service     string // optional filter
	Level       string // optional filter
	Environment string // optional filter — empty matches every env
}

// ServiceLogCount holds per-service log counts.
type ServiceLogCount struct {
	Service    string `json:"service"`
	Total      int    `json:"total"`
	ErrorCount int    `json:"error_count"`
}

// LogHistogramParams defines parameters for log volume histogram queries.
type LogHistogramParams struct {
	Since       time.Time
	Until       time.Time
	Interval    time.Duration
	Service     string
	Level       string
	Environment string
}

// LogHistogramBucket holds aggregated log counts for a single time bucket.
type LogHistogramBucket struct {
	Timestamp  time.Time `json:"timestamp"`
	Total      int       `json:"total"`
	ErrorCount int       `json:"error_count"`
}

// RequestSummarySearchParams defines filters for searching request summaries.
type RequestSummarySearchParams struct {
	Start         *time.Time
	End           *time.Time
	Controller    string
	Action        string
	Path          string
	Environment   string
	NPlusOneOnly  bool
	MinDurationMs float64
	MinSQLCount   int
	SortBy        string // "duration_ms", "sql_count", "db_time_ms", "duplicate_queries"
	Limit         int
	Offset        int
}

// RequestSummaryResult extends RequestSummary with log-level fields.
type RequestSummaryResult struct {
	RequestSummary
	Timestamp time.Time `json:"timestamp"`
	Service   string    `json:"service,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

// RequestSummaryAggregateParams defines filters for SQL-level aggregation.
type RequestSummaryAggregateParams struct {
	Start       *time.Time
	End         *time.Time
	Service     string
	Endpoint    string
	Environment string
}

// RequestSummaryAggregates holds pre-computed aggregate metrics.
type RequestSummaryAggregates struct {
	Count        int     `json:"count"`
	AvgDuration  float64 `json:"avg_duration_ms"`
	AvgSQLCount  float64 `json:"avg_sql_count"`
	TotalReads   int     `json:"total_cache_reads"`
	TotalHits    int     `json:"total_cache_hits"`
	CacheHitRate float64 `json:"cache_hit_rate"`
}

// SamplingRule defines a per-service log sampling policy.
type SamplingRule struct {
	Service    string  `json:"service"`     // service name, or "*" for default
	Rate       float64 `json:"rate"`        // 0.0-1.0 (1.0 = keep all)
	KeepErrors bool    `json:"keep_errors"` // always keep error/warn/fatal logs
}
