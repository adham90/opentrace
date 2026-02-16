package store

import (
	"time"

	"github.com/google/uuid"
)

type ConnectorType string

const (
	ConnectorLogs          ConnectorType = "logs"
	ConnectorDatabase      ConnectorType = "database"
	ConnectorMonitoring    ConnectorType = "monitoring"
	ConnectorServerMetrics ConnectorType = "server_metrics"
)

type ConnectorStatus string

const (
	StatusConnected    ConnectorStatus = "connected"
	StatusDisconnected ConnectorStatus = "disconnected"
	StatusError        ConnectorStatus = "error"
)

type DataSource struct {
	ID            uuid.UUID       `json:"id"`
	Type          ConnectorType   `json:"type"`
	Name          string          `json:"name"`
	Config        map[string]any  `json:"config"`
	Status        ConnectorStatus `json:"status"`
	StatusMessage *string         `json:"status_message,omitempty"`
	LastTestedAt  *time.Time      `json:"last_tested_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type CreateDataSourceParams struct {
	Type        ConnectorType  `json:"type"`
	Name        string         `json:"name"`
	Config      map[string]any `json:"config"`
}

type UpdateDataSourceParams struct {
	Name          *string          `json:"name,omitempty"`
	Config        map[string]any   `json:"config,omitempty"`
	Status        *ConnectorStatus `json:"status,omitempty"`
	StatusMessage *string          `json:"status_message,omitempty"`
	LastTestedAt  *time.Time       `json:"last_tested_at,omitempty"`
}

// ListDataSourceParams defines filters for listing data sources.
type ListDataSourceParams struct {
	Type ConnectorType `json:"type,omitempty"`
}

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
	Message          string          `json:"message"`
	EventType        string          `json:"event_type,omitempty"`
	ExceptionClass   string          `json:"exception_class,omitempty"`
	ErrorFingerprint string          `json:"error_fingerprint,omitempty"`
	SourceFile       string          `json:"source_file,omitempty"`
	SourceLine       int             `json:"source_line,omitempty"`
	Metadata         map[string]any  `json:"metadata,omitempty"`
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

// ServerStatus represents the health status of a monitored server.
type ServerStatus string

const (
	ServerOnline  ServerStatus = "online"
	ServerOffline ServerStatus = "offline"
	ServerUnknown ServerStatus = "unknown"
)

// Server represents a monitored VM or host.
type Server struct {
	ID           uuid.UUID        `json:"id"`
	Hostname     string           `json:"hostname"`
	IPAddress    string           `json:"ip_address,omitempty"`
	OS           string           `json:"os,omitempty"`
	Arch         string           `json:"arch,omitempty"`
	AgentVersion string           `json:"agent_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Status       ServerStatus     `json:"status"`
	LastSeenAt   *time.Time       `json:"last_seen_at,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

// RegisterServerParams defines the input for registering/updating a server.
type RegisterServerParams struct {
	Hostname     string            `json:"hostname"`
	IPAddress    string            `json:"ip_address,omitempty"`
	OS           string            `json:"os,omitempty"`
	Arch         string            `json:"arch,omitempty"`
	AgentVersion string            `json:"agent_version,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
}

// MetricPoint represents a single metric data point returned by queries.
type MetricPoint struct {
	ID          int64             `json:"id"`
	ServerID    uuid.UUID         `json:"server_id"`
	Timestamp   time.Time         `json:"timestamp"`
	MetricName  string            `json:"metric_name"`
	MetricValue float64           `json:"metric_value"`
	Unit        string            `json:"unit,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
}

// MetricSample is a single metric in an ingestion batch (no server_id or timestamp — those come from the batch context).
type MetricSample struct {
	Name   string            `json:"name"`
	Value  float64           `json:"value"`
	Unit   string            `json:"unit,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// MetricQuery defines filters for querying metrics.
type MetricQuery struct {
	ServerID   uuid.UUID `json:"server_id"`
	MetricName string    `json:"metric_name,omitempty"`
	Start      *time.Time `json:"start,omitempty"`
	End        *time.Time `json:"end,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}

// UserRole represents the access level of a user.
type UserRole string

const (
	RoleAdmin  UserRole = "admin"
	RoleMember UserRole = "member"
)

// User represents an authenticated user.
type User struct {
	ID           string   `json:"id"`
	Email        string   `json:"email"`
	PasswordHash string   `json:"-"`
	DisplayName  string   `json:"display_name"`
	Role         UserRole `json:"role"`
	MCPEnabled   bool     `json:"mcp_enabled"`
	MCPToken     *string  `json:"mcp_token,omitempty"`
	IsActive     bool     `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateUserParams defines the input for creating a user.
type CreateUserParams struct {
	Email        string   `json:"email"`
	PasswordHash string   `json:"-"`
	DisplayName  string   `json:"display_name"`
	Role         UserRole `json:"role"`
	MCPToken     *string  `json:"-"`
}

// UpdateUserParams defines the input for updating a user.
type UpdateUserParams struct {
	DisplayName *string   `json:"display_name,omitempty"`
	Role        *UserRole `json:"role,omitempty"`
	MCPEnabled  *bool     `json:"mcp_enabled,omitempty"`
	IsActive    *bool     `json:"is_active,omitempty"`
}

// MCPActivityEvent represents a single MCP tool call or connection event.
type MCPActivityEvent struct {
	ID            int64     `json:"id"`
	SessionID     string    `json:"session_id"`
	UserID        string    `json:"user_id,omitempty"`
	ToolName      string    `json:"tool_name"`
	Arguments     string    `json:"arguments,omitempty"`
	ResultPreview string    `json:"result_preview,omitempty"`
	IsError       bool      `json:"is_error"`
	DurationMs    *int64    `json:"duration_ms,omitempty"`
	EventType     string    `json:"event_type"`
	CreatedAt     time.Time `json:"created_at"`
}

// LogMCPActivityParams defines input for logging an MCP activity event.
type LogMCPActivityParams struct {
	SessionID     string
	UserID        string
	ToolName      string
	Arguments     string
	ResultPreview string
	IsError       bool
	DurationMs    *int64
	EventType     string // "tool_call", "connect", "disconnect"
}

// MCPActivityStats holds aggregated MCP activity statistics.
type MCPActivityStats struct {
	ActiveSessions int        `json:"active_sessions"`
	CallsLastHour  int        `json:"calls_last_hour"`
	ErrorsLastHour int        `json:"errors_last_hour"`
	LastActivity   *time.Time `json:"last_activity,omitempty"`
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

// Session represents an authenticated browser session.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// ---------------------------------------------------------------------------
// Error Groups (Sentry-lite)
// ---------------------------------------------------------------------------

// ErrorGroupStatus represents the lifecycle state of an error group.
type ErrorGroupStatus string

const (
	ErrorGroupUnresolved ErrorGroupStatus = "unresolved"
	ErrorGroupResolved   ErrorGroupStatus = "resolved"
	ErrorGroupIgnored    ErrorGroupStatus = "ignored"
)

// ErrorGroup aggregates errors by fingerprint.
type ErrorGroup struct {
	Fingerprint     string           `json:"fingerprint"`
	Service         string           `json:"service"`
	Environment     string           `json:"environment"`
	ExceptionClass  string           `json:"exception_class"`
	Message         string           `json:"message"`
	SourceFile      string           `json:"source_file"`
	SourceLine      int              `json:"source_line"`
	Status          ErrorGroupStatus `json:"status"`
	FirstSeenAt     time.Time        `json:"first_seen_at"`
	LastSeenAt      time.Time        `json:"last_seen_at"`
	OccurrenceCount int              `json:"occurrence_count"`
	LastLogID       *int64           `json:"last_log_id,omitempty"`
	ReopenedCount   int              `json:"reopened_count"`
	ResolvedAt      *time.Time       `json:"resolved_at,omitempty"`
	IgnoredAt       *time.Time       `json:"ignored_at,omitempty"`
	Events          []ErrorGroupEvent `json:"events,omitempty"`
}

// ErrorGroupEvent records a lifecycle action on an error group.
type ErrorGroupEvent struct {
	ID          int64     `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Action      string    `json:"action"` // "resolved", "ignored", "reopened"
	Reason      string    `json:"reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// ListErrorGroupParams defines filters for listing error groups.
type ListErrorGroupParams struct {
	Status      ErrorGroupStatus `json:"status,omitempty"`
	Service     string           `json:"service,omitempty"`
	Environment string           `json:"environment,omitempty"`
	SortBy      string           `json:"sort_by,omitempty"` // "occurrence_count", "last_seen_at", "first_seen_at"
	Limit       int              `json:"limit,omitempty"`
	Offset      int              `json:"offset,omitempty"`
}

// ---------------------------------------------------------------------------
// Uptime / Health Check monitoring
// ---------------------------------------------------------------------------

// HealthCheckStatus represents the current state of a health check target.
type HealthCheckStatus string

const (
	HealthCheckUp       HealthCheckStatus = "up"
	HealthCheckDown     HealthCheckStatus = "down"
	HealthCheckDegraded HealthCheckStatus = "degraded"
)

// HealthCheck represents a configured HTTP endpoint monitor.
type HealthCheck struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	URL            string    `json:"url"`
	Method         string    `json:"method"`
	IntervalSecs   int       `json:"interval_secs"`
	TimeoutSecs    int       `json:"timeout_secs"`
	ExpectedStatus int       `json:"expected_status"`
	Enabled        bool      `json:"enabled"`
	CreatedAt      time.Time `json:"created_at"`
}

// HealthCheckResult records a single probe result.
type HealthCheckResult struct {
	ID            int64             `json:"id"`
	HealthCheckID string            `json:"healthcheck_id"`
	Status        HealthCheckStatus `json:"status"`
	StatusCode    *int              `json:"status_code,omitempty"`
	ResponseMs    *int              `json:"response_ms,omitempty"`
	Error         string            `json:"error,omitempty"`
	CheckedAt     time.Time         `json:"checked_at"`
}

// CreateHealthCheckParams defines the input for creating a health check.
type CreateHealthCheckParams struct {
	Name           string `json:"name"`
	URL            string `json:"url"`
	Method         string `json:"method,omitempty"`
	IntervalSecs   int    `json:"interval_secs,omitempty"`
	TimeoutSecs    int    `json:"timeout_secs,omitempty"`
	ExpectedStatus int    `json:"expected_status,omitempty"`
}

// UptimeSummary aggregates uptime stats for a single health check.
type UptimeSummary struct {
	HealthCheckID string  `json:"healthcheck_id"`
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	CurrentStatus string  `json:"current_status"`
	UptimePct     float64 `json:"uptime_pct"`
	AvgResponseMs float64 `json:"avg_response_ms"`
	TotalChecks   int     `json:"total_checks"`
	DownChecks    int     `json:"down_checks"`
}

// ---------------------------------------------------------------------------
// Agent-first Watch system (Phase 1)
// ---------------------------------------------------------------------------

// WatchMetric identifies what the watch measures.
type WatchMetric string

const (
	WatchMetricErrorRate    WatchMetric = "error_rate"
	WatchMetricResponseTime WatchMetric = "response_time"
	WatchMetricP95Response  WatchMetric = "p95_response"
	WatchMetricLogCount     WatchMetric = "log_count"
	WatchMetricErrorCount   WatchMetric = "error_count"
	WatchMetricHeartbeat    WatchMetric = "heartbeat"
	WatchMetricSQLCount     WatchMetric = "sql_count"
	WatchMetricCacheHitRate WatchMetric = "cache_hit_rate"
)

// WatchOperator defines comparison operators for watches.
type WatchOperator string

const (
	WatchOpGreaterThan      WatchOperator = "gt"
	WatchOpGreaterThanEqual WatchOperator = "gte"
	WatchOpLessThan         WatchOperator = "lt"
	WatchOpLessThanEqual    WatchOperator = "lte"
	WatchOpEqual            WatchOperator = "eq"
	WatchOpNotEqual         WatchOperator = "neq"
)

// WatchStatus represents the lifecycle state of a watch.
type WatchStatus string

const (
	WatchStatusActive    WatchStatus = "active"
	WatchStatusTriggered WatchStatus = "triggered"
	WatchStatusResolved  WatchStatus = "resolved"
	WatchStatusExpired   WatchStatus = "expired"
)

// WatchUrgency represents alert urgency for a watch.
type WatchUrgency string

const (
	WatchUrgencyLow      WatchUrgency = "low"
	WatchUrgencyNormal   WatchUrgency = "normal"
	WatchUrgencyHigh     WatchUrgency = "high"
	WatchUrgencyCritical WatchUrgency = "critical"
)

// Watch represents a simplified metric threshold monitor.
type Watch struct {
	ID                 string        `json:"id"`
	Metric             WatchMetric   `json:"metric"`
	Operator           WatchOperator `json:"operator"`
	Threshold          float64       `json:"threshold"`
	Service            string        `json:"service,omitempty"`
	Endpoint           string        `json:"endpoint,omitempty"`
	Environment        string        `json:"environment,omitempty"`
	CommitHash         string        `json:"commit_hash,omitempty"`
	Duration           string        `json:"duration"`
	Urgency            WatchUrgency  `json:"urgency"`
	CheckInterval      string        `json:"check_interval"`
	BaselineWindow     string        `json:"baseline_window"`
	MinConsecutive     int           `json:"min_consecutive"`
	Status             WatchStatus   `json:"status"`
	BaselineJSON       *WatchBaseline `json:"baseline,omitempty"`
	ConsecutiveBreaches int          `json:"consecutive_breaches"`
	CurrentValue       *float64      `json:"current_value,omitempty"`
	ExpiresAt          *time.Time    `json:"expires_at,omitempty"`
	CreatedBy          string        `json:"created_by,omitempty"`
	SessionID          string        `json:"session_id,omitempty"`
	LastCheckedAt      *time.Time    `json:"last_checked_at,omitempty"`
	NextCheckAt        *time.Time    `json:"next_check_at,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
}

// CreateWatchParams defines the input for creating a watch.
type CreateWatchParams struct {
	Metric         WatchMetric   `json:"metric"`
	Operator       WatchOperator `json:"operator"`
	Threshold      float64       `json:"threshold"`
	Service        string        `json:"service,omitempty"`
	Endpoint       string        `json:"endpoint,omitempty"`
	Environment    string        `json:"environment,omitempty"`
	CommitHash     string        `json:"commit_hash,omitempty"`
	Duration       string        `json:"duration,omitempty"`
	Urgency        WatchUrgency  `json:"urgency,omitempty"`
	CheckInterval  string        `json:"check_interval,omitempty"`
	BaselineWindow string        `json:"baseline_window,omitempty"`
	MinConsecutive int           `json:"min_consecutive,omitempty"`
	CreatedBy      string        `json:"created_by,omitempty"`
	SessionID      string        `json:"session_id,omitempty"`
}

// ListWatchParams defines filters for listing watches.
type ListWatchParams struct {
	Status    WatchStatus `json:"status,omitempty"`
	Service   string      `json:"service,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
}

// WatchRun represents a single evaluation of a watch.
type WatchRun struct {
	ID           string     `json:"id"`
	WatchID      string     `json:"watch_id"`
	Status       string     `json:"status"`
	MetricValue  *float64   `json:"metric_value,omitempty"`
	Breached     bool       `json:"breached"`
	Summary      string     `json:"summary,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

// WatchAlert represents an alert generated by a watch breach.
type WatchAlert struct {
	ID             string               `json:"id"`
	WatchID        string               `json:"watch_id"`
	RunID          string               `json:"run_id,omitempty"`
	Urgency        WatchUrgency         `json:"urgency"`
	Summary        string               `json:"summary"`
	TriggerMetric  string               `json:"trigger_metric"`
	TriggerValue   float64              `json:"trigger_value"`
	ThresholdValue float64              `json:"threshold_value"`
	EvidenceJSON   *WatchEvidenceBundle `json:"evidence,omitempty"`
	Status         string               `json:"status"`
	DismissReason  string               `json:"dismiss_reason,omitempty"`
	CreatedAt      time.Time            `json:"created_at"`
}

// CreateWatchAlertParams defines the input for creating a watch alert.
type CreateWatchAlertParams struct {
	WatchID        string               `json:"watch_id"`
	RunID          string               `json:"run_id,omitempty"`
	Urgency        WatchUrgency         `json:"urgency"`
	Summary        string               `json:"summary"`
	TriggerMetric  string               `json:"trigger_metric"`
	TriggerValue   float64              `json:"trigger_value"`
	ThresholdValue float64              `json:"threshold_value"`
	Evidence       *WatchEvidenceBundle `json:"evidence,omitempty"`
}

// WatchBaseline holds a snapshot of baseline metrics at watch creation time.
type WatchBaseline struct {
	ErrorRate      float64                  `json:"error_rate"`
	AvgResponseMs  float64                  `json:"avg_response_ms"`
	P95ResponseMs  float64                  `json:"p95_response_ms"`
	LogCount       int                      `json:"log_count"`
	ErrorCount     int                      `json:"error_count"`
	SQLCount       float64                  `json:"sql_count"`
	CacheHitRate   float64                  `json:"cache_hit_rate"`
	ExceptionClasses []string               `json:"exception_classes,omitempty"`
	Endpoints      []WatchEndpointBaseline  `json:"endpoints,omitempty"`
	CapturedAt     time.Time                `json:"captured_at"`
	WindowDuration string                   `json:"window_duration"`
}

// WatchEndpointBaseline holds per-endpoint baseline stats.
type WatchEndpointBaseline struct {
	Path          string  `json:"path"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
	AvgSQLCount   float64 `json:"avg_sql_count"`
	RequestCount  int     `json:"request_count"`
}

// WatchEvidenceBundle contains all evidence collected when an alert fires.
type WatchEvidenceBundle struct {
	RecentErrors      []WatchEvidenceError    `json:"recent_errors,omitempty"`
	NewErrors         []WatchEvidenceError    `json:"new_errors,omitempty"`
	AffectedEndpoints []WatchEndpointDelta    `json:"affected_endpoints,omitempty"`
	RelevantLogs      []WatchEvidenceLog      `json:"relevant_logs,omitempty"`
	Timeline          []WatchTimelineEvent    `json:"timeline,omitempty"`
	BaselineDiff      *WatchBaselineDiff      `json:"baseline_diff,omitempty"`
	SourceFiles       []string                `json:"source_files,omitempty"`
	TraceIDs          []string                `json:"trace_ids,omitempty"`
}

// WatchEvidenceLog represents a relevant log entry in evidence.
type WatchEvidenceLog struct {
	Timestamp time.Time `json:"timestamp"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	Service   string    `json:"service,omitempty"`
	TraceID   string    `json:"trace_id,omitempty"`
}

// WatchEvidenceError represents an error found during investigation.
type WatchEvidenceError struct {
	ExceptionClass string `json:"exception_class"`
	Message        string `json:"message"`
	Count          int    `json:"count"`
	IsNew          bool   `json:"is_new"`
}

// WatchEndpointDelta shows how an endpoint changed vs baseline.
type WatchEndpointDelta struct {
	Path              string  `json:"path"`
	CurrentDurationMs float64 `json:"current_duration_ms"`
	BaselineDurationMs float64 `json:"baseline_duration_ms"`
	DeltaPct          float64 `json:"delta_pct"`
}

// WatchBaselineDiff shows overall metric drift from baseline.
type WatchBaselineDiff struct {
	MetricName     string  `json:"metric_name"`
	BaselineValue  float64 `json:"baseline_value"`
	CurrentValue   float64 `json:"current_value"`
	DeltaPct       float64 `json:"delta_pct"`
}

// WatchTimelineEvent marks a point in the alert timeline.
type WatchTimelineEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
	Value     *float64  `json:"value,omitempty"`
}
