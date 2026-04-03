package store

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// LogEntry represents an ingested log line.
type LogEntry struct {
	bun.BaseModel  `bun:"table:logs" json:"-"`
	ID             int64           `bun:"id,pk,autoincrement" json:"id"`
	Timestamp      time.Time       `bun:"timestamp" json:"timestamp"`
	Level          string          `bun:"level" json:"level"`
	Service        string          `bun:"service" json:"service,omitempty"`
	Environment    string          `bun:"environment" json:"environment,omitempty"`
	CommitHash     string          `bun:"commit_hash" json:"commit_hash,omitempty"`
	TraceID        string          `bun:"trace_id" json:"trace_id,omitempty"`
	SpanID         string          `bun:"span_id" json:"span_id,omitempty"`
	ParentSpanID   string          `bun:"parent_span_id" json:"parent_span_id,omitempty"`
	RequestID      string          `bun:"request_id" json:"request_id,omitempty"`
	UserID         string          `bun:"user_id" json:"user_id,omitempty"`
	Message        string          `bun:"message" json:"message"`
	EventType      string          `bun:"event_type" json:"event_type,omitempty"`
	ExceptionClass string          `bun:"exception_class" json:"exception_class,omitempty"`
	ErrorFingerprint string        `bun:"error_fingerprint" json:"error_fingerprint,omitempty"`
	SourceFile     string          `bun:"source_file" json:"source_file,omitempty"`
	SourceLine     int             `bun:"source_line" json:"source_line,omitempty"`
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
	Query            string            `json:"query,omitempty"`
	Service          string            `json:"service,omitempty"`
	Level            string            `json:"level,omitempty"`
	Environment      string            `json:"environment,omitempty"`
	CommitHash       string            `json:"commit_hash,omitempty"`
	TraceID          string            `json:"trace_id,omitempty"`
	RequestID        string            `json:"request_id,omitempty"`
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
	Since   time.Time
	Until   time.Time
	Service string // optional filter
	Level   string // optional filter
}

// ServiceLogCount holds per-service log counts.
type ServiceLogCount struct {
	Service    string `json:"service"`
	Total      int    `json:"total"`
	ErrorCount int    `json:"error_count"`
}

// LogHistogramParams defines parameters for log volume histogram queries.
type LogHistogramParams struct {
	Since    time.Time
	Until    time.Time
	Interval time.Duration
	Service  string
	Level    string
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
	NPlusOneOnly bool
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
	Start    *time.Time
	End      *time.Time
	Service  string
	Endpoint string
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
