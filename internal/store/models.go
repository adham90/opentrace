package store

import (
	"encoding/json"
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

// WatcherType represents the evaluation strategy (AI or rule-based).
type WatcherType string

const (
	WatcherTypeAI        WatcherType = "ai"
	WatcherTypeRule      WatcherType = "rule"
	WatcherTypeDeadman   WatcherType = "deadman"
	WatcherTypeDiff      WatcherType = "diff"
	WatcherTypeComposite WatcherType = "composite"
	WatcherTypeTrend     WatcherType = "trend"
	WatcherTypeSequence  WatcherType = "sequence"
)

// RuleSource identifies what the rule evaluates.
type RuleSource string

const (
	RuleSourceQuery  RuleSource = "query"
	RuleSourceLogs   RuleSource = "logs"
	RuleSourceHealth RuleSource = "health"
)

// RuleOperator defines comparison operators.
type RuleOperator string

const (
	OpGreaterThan      RuleOperator = "gt"
	OpGreaterThanEqual RuleOperator = "gte"
	OpLessThan         RuleOperator = "lt"
	OpLessThanEqual    RuleOperator = "lte"
	OpEqual            RuleOperator = "eq"
	OpNotEqual         RuleOperator = "neq"
)

// RuleConfig is the top-level rule configuration.
type RuleConfig struct {
	Source           RuleSource   `json:"source"`
	Query            string       `json:"query,omitempty"`
	Metric           string       `json:"metric,omitempty"`
	Operator         RuleOperator `json:"operator,omitempty"`
	Threshold        float64      `json:"threshold"`
	Filter           *LogFilter   `json:"filter,omitempty"`
	TimeWindow       string       `json:"time_window,omitempty"`
	Checks           []string     `json:"checks,omitempty"`
	LatencyThreshold int          `json:"latency_threshold_ms,omitempty"`
}

// LogFilter defines log search criteria for rule watchers.
type LogFilter struct {
	Service string `json:"service,omitempty"`
	Level   string `json:"level,omitempty"`
	Query   string `json:"query,omitempty"`
}

// WatcherSeverity represents the severity level of a watcher.
type WatcherSeverity string

const (
	SeverityInfo     WatcherSeverity = "info"
	SeverityWarning  WatcherSeverity = "warning"
	SeverityCritical WatcherSeverity = "critical"
)

// WatcherEffort represents how thorough the AI analysis should be.
type WatcherEffort string

const (
	EffortLow    WatcherEffort = "low"
	EffortMedium WatcherEffort = "medium"
	EffortHigh   WatcherEffort = "high"
)

// WatcherStatus represents the operational status of a watcher.
type WatcherStatus string

const (
	WatcherActive  WatcherStatus = "active"
	WatcherPaused  WatcherStatus = "paused"
	WatcherError   WatcherStatus = "error"
	WatcherExpired WatcherStatus = "expired"
)

// AdaptiveState represents the adaptive scheduling state of a watcher.
type AdaptiveState string

const (
	AdaptiveNormal    AdaptiveState = "normal"
	AdaptiveEscalated AdaptiveState = "escalated"
	AdaptiveSustained AdaptiveState = "sustained"
	AdaptiveRelaxed   AdaptiveState = "relaxed"
	AdaptiveBackoff   AdaptiveState = "backing_off"
	AdaptiveError     AdaptiveState = "error"
)

// AdaptiveConfig configures adaptive scheduling for a watcher.
type AdaptiveConfig struct {
	Enabled            bool    `json:"enabled"`
	EscalatedInterval  string  `json:"escalated_interval,omitempty"`
	EscalationDuration string  `json:"escalation_duration,omitempty"`
	CooldownRuns       int     `json:"cooldown_runs,omitempty"`
	RelaxEnabled       bool    `json:"relax_enabled,omitempty"`
	RelaxedInterval    string  `json:"relaxed_interval,omitempty"`
	RelaxAfterRuns     int     `json:"relax_after_runs,omitempty"`
	RelaxSkipRuns      int     `json:"relax_skip_runs,omitempty"`
	BackoffMultiplier  float64 `json:"backoff_multiplier,omitempty"`
	MaxBackoffInterval string  `json:"max_backoff_interval,omitempty"`
	MaxConsecErrors    int     `json:"max_consecutive_errors,omitempty"`
}

// Watcher represents an automated monitoring rule.
type Watcher struct {
	ID           uuid.UUID       `json:"id"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Severity     WatcherSeverity `json:"severity"`
	Filters      json.RawMessage `json:"filters"`
	TimeRange    string          `json:"time_range"`
	Schedule     string          `json:"schedule,omitempty"`
	Model        string          `json:"model"`
	Effort       WatcherEffort   `json:"effort"`
	Status       WatcherStatus   `json:"status"`
	Notify       json.RawMessage `json:"notify"`
	WatcherType  WatcherType     `json:"watcher_type"`
	RuleConfig   *RuleConfig     `json:"rule_config,omitempty"`
	DataSourceID *string          `json:"data_source_id,omitempty"`
	TypeConfig   json.RawMessage `json:"type_config,omitempty"`
	LastRunAt    *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt    *time.Time      `json:"next_run_at,omitempty"`
	LastError    *string         `json:"last_error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`

	// Human summary (Claude-generated plain-English explanation).
	HumanSummary *WatcherHumanSummary `json:"human_summary,omitempty"`

	// Adaptive scheduling fields
	AdaptiveConfig       *AdaptiveConfig `json:"adaptive_config,omitempty"`
	AdaptiveState        AdaptiveState   `json:"adaptive_state"`
	ConsecutiveCleanRuns int             `json:"consecutive_clean_runs"`
	ConsecutiveErrors    int             `json:"consecutive_errors"`
	EscalatedAt          *time.Time      `json:"escalated_at,omitempty"`
	BaseTimeRange        string          `json:"base_time_range,omitempty"`
	ExpiresAt            *time.Time      `json:"expires_at,omitempty"`
}

// WatcherHumanSummary provides a Claude-generated plain-English explanation of a watcher.
type WatcherHumanSummary struct {
	WhatItMonitors string `json:"what_it_monitors"`
	WhyItMatters   string `json:"why_it_matters"`
	WhatToDo       string `json:"what_to_do"`
}

// WatcherEffectiveness holds computed effectiveness metrics for a watcher.
type WatcherEffectiveness struct {
	WatcherID         string         `json:"watcher_id"`
	WatcherTitle      string         `json:"watcher_title"`
	Period            string         `json:"period"`
	TotalRuns         int            `json:"total_runs"`
	CompletedRuns     int            `json:"completed_runs"`
	ErrorRuns         int            `json:"error_runs"`
	TotalAlerts       int            `json:"total_alerts"`
	DismissedAlerts   int            `json:"dismissed_alerts"`
	ActedOnAlerts     int            `json:"acted_on_alerts"`
	FalsePositives    int            `json:"false_positives"`
	SignalRatio       float64        `json:"signal_ratio"`
	FalsePositiveRate float64        `json:"false_positive_rate"`
	AlertRatePct      float64        `json:"alert_rate_pct"`
	AvgDurationMS     int64          `json:"avg_duration_ms"`
	DismissReasons    map[string]int `json:"dismiss_reasons,omitempty"`
	LastAlertAt       *time.Time     `json:"last_alert_at,omitempty"`
	Trend             string         `json:"trend"`
}

// CreateWatcherParams defines the input for creating a watcher.
type CreateWatcherParams struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Severity     WatcherSeverity `json:"severity"`
	Filters      json.RawMessage `json:"filters"`
	TimeRange    string          `json:"time_range"`
	Schedule     string          `json:"schedule,omitempty"`
	Model        string          `json:"model"`
	Effort       WatcherEffort   `json:"effort"`
	Notify       json.RawMessage `json:"notify"`
	WatcherType  WatcherType     `json:"watcher_type"`
	RuleConfig      *RuleConfig          `json:"rule_config,omitempty"`
	DataSourceID    *string              `json:"data_source_id,omitempty"`
	TypeConfig      json.RawMessage      `json:"type_config,omitempty"`
	AdaptiveConfig  *AdaptiveConfig      `json:"adaptive_config,omitempty"`
	HumanSummary    *WatcherHumanSummary `json:"human_summary,omitempty"`
	ExpiresAt       *time.Time           `json:"expires_at,omitempty"`
}

// UpdateWatcherParams defines the input for updating a watcher.
type UpdateWatcherParams struct {
	Title        *string          `json:"title,omitempty"`
	Description  *string          `json:"description,omitempty"`
	Severity     *WatcherSeverity `json:"severity,omitempty"`
	Filters      json.RawMessage  `json:"filters,omitempty"`
	TimeRange    *string          `json:"time_range,omitempty"`
	Schedule     *string          `json:"schedule,omitempty"`
	Model        *string          `json:"model,omitempty"`
	Effort       *WatcherEffort   `json:"effort,omitempty"`
	Notify       json.RawMessage  `json:"notify,omitempty"`
	WatcherType  *WatcherType     `json:"watcher_type,omitempty"`
	RuleConfig      *RuleConfig          `json:"rule_config,omitempty"`
	DataSourceID    *string              `json:"data_source_id,omitempty"`
	TypeConfig      json.RawMessage      `json:"type_config,omitempty"`
	AdaptiveConfig  *AdaptiveConfig      `json:"adaptive_config,omitempty"`
	HumanSummary    *WatcherHumanSummary `json:"human_summary,omitempty"`
	ExpiresAt       *time.Time           `json:"expires_at,omitempty"`
	ClearExpiresAt  bool                 `json:"clear_expires_at,omitempty"`
}

// ListWatcherParams defines filters for listing watchers.
type ListWatcherParams struct {
	WatcherType WatcherType `json:"watcher_type,omitempty"`
}

// WatcherRun represents a single execution of a watcher.
type WatcherRun struct {
	ID            uuid.UUID       `json:"id"`
	WatcherID     uuid.UUID       `json:"watcher_id"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
	Status        string          `json:"status"`
	Summary       *string         `json:"summary,omitempty"`
	Details       json.RawMessage `json:"details,omitempty"`
	HasAlert      bool            `json:"has_alert"`
	Error         *string         `json:"error,omitempty"`
	ParentAlertID *string         `json:"parent_alert_id,omitempty"`
	RunType       string          `json:"run_type"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Alert represents a notification generated by a watcher run.
type Alert struct {
	ID            uuid.UUID       `json:"id"`
	WatcherID     *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID         *uuid.UUID      `json:"run_id,omitempty"`
	WatcherTitle  string          `json:"watcher_title,omitempty"`
	Title         string          `json:"title"`
	Summary       string          `json:"summary"`
	Severity      WatcherSeverity `json:"severity"`
	Details       json.RawMessage `json:"details,omitempty"`
	Read          bool            `json:"read"`
	Dismissed     bool            `json:"dismissed"`
	DismissReason string          `json:"dismiss_reason,omitempty"`
	DismissedAt   *time.Time      `json:"dismissed_at,omitempty"`
	SnoozedUntil  *time.Time      `json:"snoozed_until,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// CreateAlertParams defines the input for creating an alert.
type CreateAlertParams struct {
	WatcherID   *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID       *uuid.UUID      `json:"run_id,omitempty"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Severity    WatcherSeverity `json:"severity"`
	Details     json.RawMessage `json:"details,omitempty"`
}

// ListAlertParams defines filters for listing alerts.
type ListAlertParams struct {
	UnreadOnly    bool            `json:"unread_only,omitempty"`
	DismissedOnly bool            `json:"dismissed_only,omitempty"`
	Severity      WatcherSeverity `json:"severity,omitempty"`
	WatcherID     *uuid.UUID      `json:"watcher_id,omitempty"`
	Limit         int             `json:"limit,omitempty"`
	Offset        int             `json:"offset,omitempty"`
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

// WatcherRun run type constants for investigation tracking.
const (
	RunTypeScheduled     = "scheduled"
	RunTypeManual        = "manual"
	RunTypeInvestigation = "investigation"
)

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

// AlertGroup represents an incident grouping of related alerts.
type AlertGroup struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Status      string     `json:"status"`
	Severity    string     `json:"severity"`
	RootCause   *string    `json:"root_cause,omitempty"`
	Resolution  *string    `json:"resolution,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
	AlertCount  int        `json:"alert_count,omitempty"`
	Alerts      []Alert    `json:"alerts,omitempty"`
}

// CreateAlertGroupParams defines input for creating an alert group.
type CreateAlertGroupParams struct {
	Title    string   `json:"title"`
	Severity string   `json:"severity"`
	AlertIDs []string `json:"alert_ids"`
}

// UpdateAlertGroupParams defines input for updating an alert group.
type UpdateAlertGroupParams struct {
	Title      *string `json:"title,omitempty"`
	Status     *string `json:"status,omitempty"`
	Severity   *string `json:"severity,omitempty"`
	RootCause  *string `json:"root_cause,omitempty"`
	Resolution *string `json:"resolution,omitempty"`
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
