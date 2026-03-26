package store

import "time"

// LogEntry represents an ingested log line.
type LogEntry struct {
	ID               int64           `json:"id"`
	Timestamp        time.Time       `json:"timestamp"`
	Level            string          `json:"level"`
	Service          string          `json:"service,omitempty"`
	Environment      string          `json:"environment,omitempty"`
	CommitHash       string          `json:"commit_hash,omitempty"`
	TraceID          string          `json:"trace_id,omitempty"`
	SpanID           string          `json:"span_id,omitempty"`
	ParentSpanID     string          `json:"parent_span_id,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
	UserID           string          `json:"user_id,omitempty"`
	SessionID        string          `json:"session_id,omitempty"`
	Message          string          `json:"message"`
	EventType        string          `json:"event_type,omitempty"`
	ExceptionClass   string          `json:"exception_class,omitempty"`
	ErrorFingerprint string          `json:"error_fingerprint,omitempty"`
	SourceFile       string          `json:"source_file,omitempty"`
	SourceLine       int             `json:"source_line,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
	MetadataJSON     string          `json:"-"` // pre-marshaled metadata; avoids double marshal on hot path
	RequestSummary   *RequestSummary `json:"request_summary,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// RequestSummary holds structured performance metrics for an HTTP request.
type RequestSummary struct {
	ID                  int64   `json:"id"`
	LogID               int64   `json:"log_id"`
	Controller          string  `json:"controller,omitempty"`
	Action              string  `json:"action,omitempty"`
	Method              string  `json:"method,omitempty"`
	Path                string  `json:"path,omitempty"`
	Status              int     `json:"status,omitempty"`
	DurationMs          float64 `json:"duration_ms,omitempty"`
	DBTimeMs            float64 `json:"db_time_ms,omitempty"`
	ViewTimeMs          float64 `json:"view_time_ms,omitempty"`
	SQLCount            int     `json:"sql_count,omitempty"`
	SQLTotalMs          float64 `json:"sql_total_ms,omitempty"`
	SQLSlowestMs        float64 `json:"sql_slowest_ms,omitempty"`
	SQLSlowestName      string  `json:"sql_slowest_name,omitempty"`
	NPlusOne            bool    `json:"n_plus_one,omitempty"`
	ViewCount           int     `json:"view_count,omitempty"`
	ViewTotalMs         float64 `json:"view_total_ms,omitempty"`
	ViewSlowestMs       float64 `json:"view_slowest_ms,omitempty"`
	ViewSlowestTemplate string  `json:"view_slowest_template,omitempty"`
	CacheReads          int     `json:"cache_reads,omitempty"`
	CacheHits           int     `json:"cache_hits,omitempty"`
	CacheWrites         int     `json:"cache_writes,omitempty"`
	CacheHitRatio       float64 `json:"cache_hit_ratio,omitempty"`
	HTTPExternalCount   int     `json:"http_external_count,omitempty"`
	HTTPExternalTotalMs float64 `json:"http_external_total_ms,omitempty"`
	HTTPSlowestMs       float64 `json:"http_slowest_ms,omitempty"`
	HTTPSlowestHost     string  `json:"http_slowest_host,omitempty"`
	MemoryBeforeMb      float64 `json:"memory_before_mb,omitempty"`
	MemoryAfterMb       float64 `json:"memory_after_mb,omitempty"`
	MemoryDeltaMb       float64 `json:"memory_delta_mb,omitempty"`
	Timeline            string  `json:"timeline,omitempty"`
	TimeBreakdown       string  `json:"time_breakdown,omitempty"`
	DuplicateQueries    int     `json:"duplicate_queries,omitempty"`
	WorstDuplicateCount int     `json:"worst_duplicate_count,omitempty"`
	TopDuplicates       string  `json:"top_duplicates,omitempty"`
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

// SamplingRule defines a per-service log sampling policy.
type SamplingRule struct {
	Service    string  `json:"service"`     // service name, or "*" for default
	Rate       float64 `json:"rate"`        // 0.0-1.0 (1.0 = keep all)
	KeepErrors bool    `json:"keep_errors"` // always keep error/warn/fatal logs
}
