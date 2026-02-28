package store

import (
	"time"

	"github.com/google/uuid"
)

type ConnectorType string

const (
	ConnectorLogs          ConnectorType = "logs"
	ConnectorDatabase      ConnectorType = "database"
	ConnectorMySQL         ConnectorType = "mysql"
	ConnectorRedis         ConnectorType = "redis"
	ConnectorTurso         ConnectorType = "turso"
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
	DisplayName  string           `json:"display_name,omitempty"`
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

// UpdateServerParams defines the input for updating a server's user-facing fields.
type UpdateServerParams struct {
	DisplayName *string `json:"display_name,omitempty"`
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
	SessionID              string
	UserID                 string
	ToolName               string
	Arguments              string
	ResultPreview          string
	IsError                bool
	DurationMs             *int64
	EventType              string // "tool_call", "connect", "disconnect"
	InvestigationSessionID string // links to investigation_sessions.id
	StepIndex              int    // step number within the investigation session
	WasSuggested           bool   // whether this tool was in the previous suggested_tools
	SuggestionRank         int    // rank in the suggestion list (0 if not suggested)
	FollowedBy             string // tool name that followed this one
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

	// Phase 3: Impact tracking
	UniqueUsers   int            `json:"unique_users"`
	ImpactScore   float64        `json:"impact_score"`
	CommonContext map[string]any `json:"common_context,omitempty"`
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
	ExpectedBody   string    `json:"expected_body,omitempty"`
	Retries        int       `json:"retries,omitempty"`
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
	ExpectedBody   string `json:"expected_body,omitempty"`
	Retries        int    `json:"retries,omitempty"`
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
	Reliability   float64 `json:"reliability,omitempty"`
}

// ---------------------------------------------------------------------------
// Agent Notes — persistent memory for the AI agent
// ---------------------------------------------------------------------------

// AgentNote represents a persistent note attached to an entity by the AI agent.
type AgentNote struct {
	ID         int64     `json:"id"`
	EntityType string    `json:"entity_type"` // "query", "endpoint", "service", "healthcheck", "error"
	EntityID   string    `json:"entity_id"`   // fingerprint, URL, query hash, etc.
	Note       string    `json:"note"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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

// ListServerParams controls pagination for server listing.
type ListServerParams struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// ListHealthCheckParams controls pagination for health check listing.
type ListHealthCheckParams struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// ListWatchParams defines filters for listing watches.
type ListWatchParams struct {
	Status    WatchStatus `json:"status,omitempty"`
	Service   string      `json:"service,omitempty"`
	SessionID string      `json:"session_id,omitempty"`
	Limit     int         `json:"limit,omitempty"`
	Offset    int         `json:"offset,omitempty"`
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

// ---------------------------------------------------------------------------
// Trends Dashboard
// ---------------------------------------------------------------------------

// MetricBucket holds pre-aggregated metrics for a time bucket.
type MetricBucket struct {
	ID              int64     `json:"id"`
	BucketStart     time.Time `json:"bucket_start"`
	BucketInterval  string    `json:"bucket_interval"`
	Service         string    `json:"service,omitempty"`
	Endpoint        string    `json:"endpoint,omitempty"`
	Environment     string    `json:"environment,omitempty"`
	RequestCount    int       `json:"request_count"`
	ErrorCount      int       `json:"error_count"`
	LogCount        int       `json:"log_count"`
	AvgDurationMs   float64   `json:"avg_duration_ms"`
	P50DurationMs   float64   `json:"p50_duration_ms"`
	P95DurationMs   float64   `json:"p95_duration_ms"`
	P99DurationMs   float64   `json:"p99_duration_ms"`
	MaxDurationMs   float64   `json:"max_duration_ms"`
	AvgSQLCount     float64   `json:"avg_sql_count"`
	AvgDBTimeMs     float64   `json:"avg_db_time_ms"`
	AvgCacheHitRatio  float64 `json:"avg_cache_hit_ratio"`
	AvgHTTPExternalMs float64 `json:"avg_http_external_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

// TrendQueryParams defines filters for querying trend data.
type TrendQueryParams struct {
	Service     string    `json:"service,omitempty"`
	Endpoint    string    `json:"endpoint,omitempty"`
	Environment string    `json:"environment,omitempty"`
	Interval    string    `json:"interval,omitempty"` // "5m", "15m", "1h", "1d"
	Metric      string    `json:"metric,omitempty"`   // field to extract
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
}

// DeployMarker records when a new commit was first seen.
type DeployMarker struct {
	ID           int64     `json:"id"`
	Service      string    `json:"service"`
	Environment  string    `json:"environment,omitempty"`
	CommitHash   string    `json:"commit_hash"`
	FirstSeenAt  time.Time `json:"first_seen_at"`
	RequestCount int       `json:"request_count"`
}

// ---------------------------------------------------------------------------
// Web Analytics
// ---------------------------------------------------------------------------

// EndpointStat holds aggregated stats for a single endpoint in a time period.
type EndpointStat struct {
	ID               int64     `json:"id"`
	Period           string    `json:"period"`
	PeriodStart      time.Time `json:"period_start"`
	Service          string    `json:"service,omitempty"`
	Method           string    `json:"method,omitempty"`
	Controller       string    `json:"controller,omitempty"`
	Action           string    `json:"action,omitempty"`
	PathPattern      string    `json:"path_pattern,omitempty"`
	RequestCount     int       `json:"request_count"`
	ErrorCount       int       `json:"error_count"`
	ClientErrorCount int       `json:"client_error_count"`
	AvgDurationMs    float64   `json:"avg_duration_ms"`
	P95DurationMs    float64   `json:"p95_duration_ms"`
	MaxDurationMs    float64   `json:"max_duration_ms"`
	AvgSQLCount      float64   `json:"avg_sql_count"`
	Status2xx        int       `json:"status_2xx"`
	Status3xx        int       `json:"status_3xx"`
	Status4xx        int       `json:"status_4xx"`
	Status5xx        int       `json:"status_5xx"`
	CreatedAt        time.Time `json:"created_at"`
}

// TopEndpointParams defines filters for ranking endpoints.
type TopEndpointParams struct {
	Service     string    `json:"service,omitempty"`
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
	SortBy      string    `json:"sort_by,omitempty"` // "request_count", "error_rate", "avg_duration", "p95_duration"
	Limit       int       `json:"limit,omitempty"`
	MinRequests int       `json:"min_requests,omitempty"`
}

// HeatmapCell holds one cell of the 24x7 traffic heatmap.
type HeatmapCell struct {
	Service       string  `json:"service,omitempty"`
	DayOfWeek     int     `json:"day_of_week"` // 0=Sunday
	HourOfDay     int     `json:"hour_of_day"` // 0-23
	RequestCount  int     `json:"request_count"`
	ErrorCount    int     `json:"error_count"`
	AvgDurationMs float64 `json:"avg_duration_ms"`
}

// TrafficSummary holds high-level traffic overview.
type TrafficSummary struct {
	TotalRequests   int            `json:"total_requests"`
	UniqueEndpoints int            `json:"unique_endpoints"`
	ErrorRate       float64        `json:"error_rate"`
	AvgDurationMs   float64        `json:"avg_duration_ms"`
	P95DurationMs   float64        `json:"p95_duration_ms"`
	StatusBreakdown map[string]int `json:"status_breakdown"`
	MethodBreakdown map[string]int `json:"method_breakdown"`
}

// AnalyticsParams defines common filters for analytics queries.
type AnalyticsParams struct {
	Service string    `json:"service,omitempty"`
	Since   time.Time `json:"since"`
	Until   time.Time `json:"until"`
}

// --- Phase 2: User Journey / Session Timeline ---

// UserSession represents a pre-computed user session aggregation.
type UserSession struct {
	ID              int64     `json:"id"`
	SessionID       string    `json:"session_id"`
	UserID          string    `json:"user_id,omitempty"`
	Service         string    `json:"service"`
	Environment     string    `json:"environment,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	RequestCount    int       `json:"request_count"`
	ErrorCount      int       `json:"error_count"`
	TotalDurationMs float64   `json:"total_duration_ms"`
	EntryPath       string    `json:"entry_path"`
	ExitPath        string    `json:"exit_path"`
	ExitStatus      int       `json:"exit_status"`
	HasError        bool      `json:"has_error"`
	CreatedAt       time.Time `json:"created_at"`
}

// SessionListParams defines filters for listing user sessions.
type SessionListParams struct {
	UserID      string    `json:"user_id,omitempty"`
	Service     string    `json:"service,omitempty"`
	HasError    *bool     `json:"has_error,omitempty"`
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
	Limit       int       `json:"limit"`
	Offset      int       `json:"offset"`
}

// RequestStep represents one HTTP request in a user journey.
type RequestStep struct {
	Timestamp  time.Time `json:"timestamp"`
	Controller string    `json:"controller"`
	Action     string    `json:"action"`
	Path       string    `json:"path"`
	Method     string    `json:"method"`
	Status     int       `json:"status"`
	DurationMs float64   `json:"duration_ms"`
	SQLCount   int       `json:"sql_count"`
	HasError   bool      `json:"has_error"`
	ErrorClass string    `json:"error_class,omitempty"`
	RequestID  string    `json:"request_id"`
	LogID      int64     `json:"log_id"`
}

// PathFrequency represents a common navigation path and its frequency.
type PathFrequency struct {
	Steps            []string `json:"steps"`
	Count            int      `json:"count"`
	AvgTotalDuration float64  `json:"avg_total_duration_ms"`
	ErrorRate        float64  `json:"error_rate"`
}

// PathAnalysisParams defines filters for path analysis queries.
type PathAnalysisParams struct {
	Service        string    `json:"service,omitempty"`
	Since          time.Time `json:"since"`
	MinOccurrences int       `json:"min_occurrences"`
	PathLength     int       `json:"path_length"`
	ErrorPathsOnly bool      `json:"error_paths_only"`
	StartingFrom   string    `json:"starting_from,omitempty"`
}

// Funnel represents a user-defined conversion funnel.
type Funnel struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Service   string       `json:"service,omitempty"`
	Steps     []FunnelStep `json:"steps"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// FunnelStep represents one step in a conversion funnel.
type FunnelStep struct {
	Controller string `json:"controller"`
	Action     string `json:"action"`
	Label      string `json:"label"`
}

// FunnelResult contains the analysis results for a funnel.
type FunnelResult struct {
	FunnelName        string             `json:"funnel_name"`
	TotalEntered      int                `json:"total_entered"`
	Steps             []FunnelStepResult `json:"steps"`
	OverallConversion float64            `json:"overall_conversion"`
}

// FunnelStepResult contains the result for one funnel step.
type FunnelStepResult struct {
	Label   string  `json:"label"`
	Count   int     `json:"count"`
	Pct     float64 `json:"pct"`
	DropOff int     `json:"drop_off"`
}

// RequestTimeline contains parsed timeline data for a single request.
type RequestTimeline struct {
	LogID      int64           `json:"log_id"`
	RequestID  string          `json:"request_id"`
	Controller string          `json:"controller"`
	Action     string          `json:"action"`
	Path       string          `json:"path"`
	Status     int             `json:"status"`
	DurationMs float64         `json:"duration_ms"`
	StartedAt  time.Time       `json:"started_at"`
	Events     []TimelineEvent `json:"events"`
	Bottleneck *TimelineEvent  `json:"bottleneck,omitempty"`
}

// TimelineEvent represents a single event in a request timeline.
type TimelineEvent struct {
	Type       string         `json:"type"`
	Name       string         `json:"name"`
	StartMs    float64        `json:"start_ms"`
	DurationMs float64        `json:"duration_ms"`
	Details    map[string]any `json:"details,omitempty"`
}

// --- Phase 3: Cohort-Based Error Impact ---

// ErrorImpact summarizes the user impact of an error group.
type ErrorImpact struct {
	Fingerprint      string         `json:"fingerprint"`
	UniqueUsers      int            `json:"unique_users"`
	TotalOccurrences int            `json:"total_occurrences"`
	ImpactScore      float64        `json:"impact_score"`
	CommonTraits     map[string]any `json:"common_traits,omitempty"`
}

// AffectedUser represents a user affected by an error.
type AffectedUser struct {
	UserID          string         `json:"user_id"`
	OccurrenceCount int            `json:"occurrence_count"`
	FirstSeenAt     time.Time      `json:"first_seen_at"`
	LastSeenAt      time.Time      `json:"last_seen_at"`
	LastContext      map[string]any `json:"last_context,omitempty"`
}

// ErrorSummary is a lightweight error record for user-scoped queries.
type ErrorSummary struct {
	Fingerprint     string           `json:"fingerprint"`
	ExceptionClass  string           `json:"exception_class"`
	Message         string           `json:"message"`
	OccurrenceCount int              `json:"occurrence_count"`
	FirstSeenAt     time.Time        `json:"first_seen_at"`
	LastSeenAt      time.Time        `json:"last_seen_at"`
	Status          ErrorGroupStatus `json:"status"`
}

// ImpactQueryParams defines filters for querying errors by impact.
type ImpactQueryParams struct {
	Status  ErrorGroupStatus `json:"status,omitempty"`
	Service string           `json:"service,omitempty"`
	Since   time.Time        `json:"since"`
	SortBy  string           `json:"sort_by,omitempty"` // impact_score, unique_users, occurrence_count, last_seen
	Limit   int              `json:"limit,omitempty"`
}

// ErrorGroupWithImpact extends ErrorGroup with impact details.
type ErrorGroupWithImpact struct {
	ErrorGroup
	TopAffectedUsers []AffectedUser `json:"top_affected_users,omitempty"`
}

// ---------------------------------------------------------------------------
// Investigation Sessions (MCP session tracking)
// ---------------------------------------------------------------------------

// InvestigationSessionStatus represents the lifecycle state of an investigation session.
type InvestigationSessionStatus string

const (
	InvestigationStatusOpen       InvestigationSessionStatus = "open"
	InvestigationStatusResolved   InvestigationSessionStatus = "resolved"
	InvestigationStatusUnresolved InvestigationSessionStatus = "unresolved"
	InvestigationStatusAbandoned  InvestigationSessionStatus = "abandoned"
)

// InvestigationSession represents a complete MCP investigation session.
type InvestigationSession struct {
	ID string `json:"id"`

	// Identity
	UserID    string `json:"user_id"`
	UserEmail string `json:"user_email"`
	UserRole  string `json:"user_role"`

	// Client
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
	Workspace     string `json:"workspace"`
	Transport     string `json:"transport"`
	ConnectionID  string `json:"connection_id"`

	// Classification
	Intent              string `json:"intent"`
	IntentDetail        string `json:"intent_detail"`
	PrimaryService      string `json:"primary_service"`
	PrimaryDatasourceID *int   `json:"primary_datasource_id,omitempty"`

	// Outcome
	Status         InvestigationSessionStatus `json:"status"`
	Summary        string                     `json:"summary"`
	RootCause      string                     `json:"root_cause"`
	FixDescription string                     `json:"fix_description"`

	// Metrics
	TotalSteps      int      `json:"total_steps"`
	TotalErrors     int      `json:"total_errors"`
	ToolSequence    []string `json:"tool_sequence"`
	ToolFingerprint string   `json:"tool_fingerprint"`
	ArgSignature    string   `json:"arg_signature"`

	// Timing
	StartedAt       time.Time  `json:"started_at"`
	LastActivityAt  time.Time  `json:"last_activity_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	DurationSeconds int        `json:"duration_seconds"`

	// Subsystem Links
	CreatedWatcherIDs             []string `json:"created_watcher_ids,omitempty"`
	TriggeredByWatcherID          *string  `json:"triggered_by_watcher_id,omitempty"`
	ResolvedErrorGroupIDs         []string `json:"resolved_error_group_ids,omitempty"`
	InvestigatedErrorFingerprints []string `json:"investigated_error_fingerprints,omitempty"`
	CreatedHealthcheckIDs         []string `json:"created_healthcheck_ids,omitempty"`
	TriggeredByHealthcheckID      *string  `json:"triggered_by_healthcheck_id,omitempty"`

	// Stage 4: Additional Subsystem Links
	CreatedNoteIDs            []string           `json:"created_note_ids,omitempty"`
	AutoNoteIDs               []string           `json:"auto_note_ids,omitempty"`
	RunbooksExecuted          []string           `json:"runbooks_executed,omitempty"`
	ExplainedQueries          []string           `json:"explained_queries,omitempty"`
	KilledQueries             []string           `json:"killed_queries,omitempty"`
	TraceIDs                  []string           `json:"trace_ids,omitempty"`
	CorrelatedDeploy          string             `json:"correlated_deploy,omitempty"`
	PreInvestigationSnapshot  map[string]float64 `json:"pre_investigation_snapshot,omitempty"`
	PostInvestigationSnapshot map[string]float64 `json:"post_investigation_snapshot,omitempty"`
	TriggeredByAlertID        *string            `json:"triggered_by_alert_id,omitempty"`

	// Recurrence
	RecurrenceGroup      *string `json:"recurrence_group,omitempty"`
	RecurrenceCount      int     `json:"recurrence_count"`
	PreviousSessionID    *string `json:"previous_session_id,omitempty"`
	FixDurabilitySeconds *int    `json:"fix_durability_seconds,omitempty"`
}

// CreateInvestigationSessionParams defines input for creating an investigation session.
type CreateInvestigationSessionParams struct {
	UserID        string `json:"user_id"`
	UserEmail     string `json:"user_email"`
	UserRole      string `json:"user_role"`
	ClientName    string `json:"client_name"`
	ClientVersion string `json:"client_version"`
	Workspace     string `json:"workspace"`
	Transport     string `json:"transport"`
	ConnectionID  string `json:"connection_id"`
}

// UpdateInvestigationSessionParams defines fields that can be updated on a session.
type UpdateInvestigationSessionParams struct {
	Intent              *string                     `json:"intent,omitempty"`
	IntentDetail        *string                     `json:"intent_detail,omitempty"`
	PrimaryService      *string                     `json:"primary_service,omitempty"`
	PrimaryDatasourceID *int                        `json:"primary_datasource_id,omitempty"`
	Status              *InvestigationSessionStatus `json:"status,omitempty"`
	Summary             *string                     `json:"summary,omitempty"`
	RootCause           *string                     `json:"root_cause,omitempty"`
	FixDescription      *string                     `json:"fix_description,omitempty"`
	TotalSteps          *int                        `json:"total_steps,omitempty"`
	TotalErrors         *int                        `json:"total_errors,omitempty"`
	ToolSequence        []string                    `json:"tool_sequence,omitempty"`
	ToolFingerprint     *string                     `json:"tool_fingerprint,omitempty"`
	ArgSignature        *string                     `json:"arg_signature,omitempty"`

	// Subsystem links
	CreatedWatcherIDs             []string `json:"created_watcher_ids,omitempty"`
	TriggeredByWatcherID          *string  `json:"triggered_by_watcher_id,omitempty"`
	ResolvedErrorGroupIDs         []string `json:"resolved_error_group_ids,omitempty"`
	InvestigatedErrorFingerprints []string `json:"investigated_error_fingerprints,omitempty"`
	CreatedHealthcheckIDs         []string `json:"created_healthcheck_ids,omitempty"`
	TriggeredByHealthcheckID      *string  `json:"triggered_by_healthcheck_id,omitempty"`

	// Stage 4: Additional Subsystem Links
	RunbooksExecuted          []string `json:"runbooks_executed,omitempty"`
	ExplainedQueries          []string `json:"explained_queries,omitempty"`
	KilledQueries             []string `json:"killed_queries,omitempty"`
	TraceIDs                  []string `json:"trace_ids,omitempty"`
	CorrelatedDeploy          *string  `json:"correlated_deploy,omitempty"`
	PreInvestigationSnapshot  *string  `json:"pre_investigation_snapshot,omitempty"`  // JSON string
	PostInvestigationSnapshot *string  `json:"post_investigation_snapshot,omitempty"` // JSON string
	AutoNoteIDs               []string `json:"auto_note_ids,omitempty"`
	CreatedNoteIDs            []string `json:"created_note_ids,omitempty"`

	// Recurrence
	RecurrenceGroup      *string `json:"recurrence_group,omitempty"`
	RecurrenceCount      *int    `json:"recurrence_count,omitempty"`
	PreviousSessionID    *string `json:"previous_session_id,omitempty"`
	FixDurabilitySeconds *int    `json:"fix_durability_seconds,omitempty"`
}

// FindRecentSessionParams defines criteria for finding a recent resumable session.
type FindRecentSessionParams struct {
	UserID    string
	Workspace string
	MaxAge    time.Duration
	Status    InvestigationSessionStatus
}

// ListInvestigationSessionParams defines filters for listing investigation sessions.
type ListInvestigationSessionParams struct {
	UserID  string                     `json:"user_id,omitempty"`
	Status  InvestigationSessionStatus `json:"status,omitempty"`
	Intent  string                     `json:"intent,omitempty"`
	Service string                     `json:"service,omitempty"`
	Since   time.Time                  `json:"since"`
	Limit   int                        `json:"limit,omitempty"`
	Offset  int                        `json:"offset,omitempty"`
}

// InvestigationSessionStats holds aggregated investigation session statistics.
type InvestigationSessionStats struct {
	TotalSessions    int     `json:"total_sessions"`
	OpenSessions     int     `json:"open_sessions"`
	ResolvedSessions int     `json:"resolved_sessions"`
	AvgSteps         float64 `json:"avg_steps"`
	AvgDurationSecs  float64 `json:"avg_duration_secs"`
}

// ---------------------------------------------------------------------------
// Tool Transitions & Workflow Templates (Investigation Memory Stage 3)
// ---------------------------------------------------------------------------

// ToolTransition tracks how often tool A is followed by tool B.
type ToolTransition struct {
	FromTool       string    `json:"from_tool"`
	ToTool         string    `json:"to_tool"`
	Intent         string    `json:"intent"`
	TotalCount     int       `json:"total_count"`
	ResolvedCount  int       `json:"resolved_count"`
	AbandonedCount int       `json:"abandoned_count"`
	AvgDurationMs  int       `json:"avg_duration_ms"`
	LastSeenAt     time.Time `json:"last_seen_at"`
}

// WorkflowTemplate defines a curated or learned workflow step.
type WorkflowTemplate struct {
	ID                   int       `json:"id"`
	Intent               string    `json:"intent"`
	Name                 string    `json:"name"`
	StepOrder            int       `json:"step_order"`
	ToolName             string    `json:"tool_name"`
	ArgsHint             string    `json:"args_hint"`
	Source               string    `json:"source"` // "curated" or "learned"
	ResolvedSessionCount int       `json:"resolved_session_count"`
	CreatedAt            time.Time `json:"created_at"`
}

// FindSimilarParams defines criteria for finding similar past sessions.
type FindSimilarParams struct {
	Service          string
	Intent           string
	ToolFingerprint  string
	ExcludeSessionID string
	MaxResults       int
	MinSteps         int
	OnlyResolved     bool
}

// GetTransitionsParams defines criteria for querying tool transitions.
type GetTransitionsParams struct {
	FromTool   string
	Intent     string
	MinSupport int // minimum total_count
	MaxAgeDays int // exclude older than N days
}

// ---------------------------------------------------------------------------
// Log Sampling
// ---------------------------------------------------------------------------

// SamplingRule defines a per-service log sampling policy.
type SamplingRule struct {
	Service    string  `json:"service"`     // service name, or "*" for default
	Rate       float64 `json:"rate"`        // 0.0-1.0 (1.0 = keep all)
	KeepErrors bool    `json:"keep_errors"` // always keep error/warn/fatal logs
}

// ---------------------------------------------------------------------------
// Distributed Trace Reassembly
// ---------------------------------------------------------------------------

// TraceStatus tracks the reassembly status of a distributed trace.
type TraceStatus struct {
	TraceID       string   `json:"trace_id"`
	SpanCount     int      `json:"span_count"`
	RootSpanID    string   `json:"root_span_id,omitempty"`
	Services      []string `json:"services"`
	FirstSeenAt   time.Time `json:"first_seen_at"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
	DurationMs    float64  `json:"duration_ms"`
	Status        string   `json:"status"`     // "partial", "complete", "timeout"
	HasErrors     bool     `json:"has_errors"`
}

// ---------------------------------------------------------------------------
// Query Memory & Runbook Effectiveness (Investigation Memory Stage 4)
// ---------------------------------------------------------------------------

// QueryMemory stores historical explain_query findings.
type QueryMemory struct {
	Fingerprint                string    `json:"fingerprint"`
	LastInvestigationSessionID string    `json:"last_investigation_session_id"`
	InvestigationCount         int       `json:"investigation_count"`
	LastRootCause              string    `json:"last_root_cause"`
	LastFix                    string    `json:"last_fix"`
	AvgDurationBeforeMs        *int      `json:"avg_duration_before_ms,omitempty"`
	AvgDurationAfterMs         *int      `json:"avg_duration_after_ms,omitempty"`
	FirstSeenAt                time.Time `json:"first_seen_at"`
	LastSeenAt                 time.Time `json:"last_seen_at"`
}

// UpsertQueryMemoryParams defines input for creating/updating query memory.
type UpsertQueryMemoryParams struct {
	Fingerprint string
	SessionID   string
	RootCause   string
	Fix         string
	DurationMs  *int
}

// RunbookEffectiveness tracks how well each playbook resolves issues.
type RunbookEffectiveness struct {
	RunbookName               string    `json:"runbook_name"`
	TotalExecutions           int       `json:"total_executions"`
	ResolvedSessions          int       `json:"resolved_sessions"`
	AbandonedSessions         int       `json:"abandoned_sessions"`
	AvgStepsAfter             int       `json:"avg_steps_after"`
	AvgSessionDurationSeconds int       `json:"avg_session_duration_seconds"`
	LastExecutedAt            time.Time `json:"last_executed_at"`
}

// UpdateRunbookEffectivenessParams defines input for updating runbook effectiveness.
type UpdateRunbookEffectivenessParams struct {
	RunbookName string
	Outcome     string // "resolved" or "abandoned"
	StepsAfter  int
	DurationSec int
}
