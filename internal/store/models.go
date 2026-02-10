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
	Environment   string          `json:"environment,omitempty"`
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
	Environment string         `json:"environment,omitempty"`
	Config      map[string]any `json:"config"`
}

type UpdateDataSourceParams struct {
	Status        *ConnectorStatus `json:"status,omitempty"`
	StatusMessage *string          `json:"status_message,omitempty"`
	LastTestedAt  *time.Time       `json:"last_tested_at,omitempty"`
	Environment   *string          `json:"environment,omitempty"`
}

// ListDataSourceParams defines filters for listing data sources.
type ListDataSourceParams struct {
	Environment string `json:"environment,omitempty"`
}

// LogEntry represents an ingested log line.
type LogEntry struct {
	ID          int64          `json:"id"`
	Timestamp   time.Time      `json:"timestamp"`
	Level       string         `json:"level"`
	Service     string         `json:"service,omitempty"`
	TraceID     string         `json:"trace_id,omitempty"`
	Message     string         `json:"message"`
	Environment string         `json:"environment,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// LogSearchParams defines filters for log search.
type LogSearchParams struct {
	Query       string     `json:"query,omitempty"`
	Service     string     `json:"service,omitempty"`
	Level       string     `json:"level,omitempty"`
	TraceID     string     `json:"trace_id,omitempty"`
	Environment string     `json:"environment,omitempty"`
	Start       *time.Time `json:"start,omitempty"`
	End         *time.Time `json:"end,omitempty"`
	Limit       int        `json:"limit,omitempty"`
	Offset      int        `json:"offset,omitempty"`
	SinceID     int64      `json:"since_id,omitempty"`
}

// MonitorType represents the evaluation strategy.
type MonitorType string

const (
	MonitorTypeAI   MonitorType = "ai"
	MonitorTypeRule MonitorType = "rule"
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

// LogFilter defines log search criteria for rule monitors.
type LogFilter struct {
	Service     string `json:"service,omitempty"`
	Level       string `json:"level,omitempty"`
	Query       string `json:"query,omitempty"`
	Environment string `json:"environment,omitempty"`
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
	WatcherActive WatcherStatus = "active"
	WatcherPaused WatcherStatus = "paused"
	WatcherError  WatcherStatus = "error"
)

// Watcher represents an automated monitoring rule.
type Watcher struct {
	ID           uuid.UUID       `json:"id"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Environment  string          `json:"environment,omitempty"`
	Severity     WatcherSeverity `json:"severity"`
	Filters      json.RawMessage `json:"filters"`
	TimeRange    string          `json:"time_range"`
	Model        string          `json:"model"`
	Effort       WatcherEffort   `json:"effort"`
	Status       WatcherStatus   `json:"status"`
	Notify       json.RawMessage `json:"notify"`
	MonitorType  MonitorType     `json:"monitor_type"`
	RuleConfig   *RuleConfig     `json:"rule_config,omitempty"`
	DataSourceID *string         `json:"data_source_id,omitempty"`
	LastRunAt    *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt    *time.Time      `json:"next_run_at,omitempty"`
	LastError    *string         `json:"last_error,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// CreateWatcherParams defines the input for creating a watcher.
type CreateWatcherParams struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Environment  string          `json:"environment,omitempty"`
	Severity     WatcherSeverity `json:"severity"`
	Filters      json.RawMessage `json:"filters"`
	TimeRange    string          `json:"time_range"`
	Model        string          `json:"model"`
	Effort       WatcherEffort   `json:"effort"`
	Notify       json.RawMessage `json:"notify"`
	MonitorType  MonitorType     `json:"monitor_type"`
	RuleConfig   *RuleConfig     `json:"rule_config,omitempty"`
	DataSourceID *string         `json:"data_source_id,omitempty"`
}

// UpdateWatcherParams defines the input for updating a watcher.
type UpdateWatcherParams struct {
	Title        *string          `json:"title,omitempty"`
	Description  *string          `json:"description,omitempty"`
	Environment  *string          `json:"environment,omitempty"`
	Severity     *WatcherSeverity `json:"severity,omitempty"`
	Filters      json.RawMessage  `json:"filters,omitempty"`
	TimeRange    *string          `json:"time_range,omitempty"`
	Model        *string          `json:"model,omitempty"`
	Effort       *WatcherEffort   `json:"effort,omitempty"`
	Notify       json.RawMessage  `json:"notify,omitempty"`
	MonitorType  *MonitorType     `json:"monitor_type,omitempty"`
	RuleConfig   *RuleConfig      `json:"rule_config,omitempty"`
	DataSourceID *string          `json:"data_source_id,omitempty"`
}

// ListWatcherParams defines filters for listing watchers.
type ListWatcherParams struct {
	Environment string      `json:"environment,omitempty"`
	MonitorType MonitorType `json:"monitor_type,omitempty"`
}

// WatcherRun represents a single execution of a watcher.
type WatcherRun struct {
	ID         uuid.UUID       `json:"id"`
	WatcherID  uuid.UUID       `json:"watcher_id"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Status     string          `json:"status"`
	Summary    *string         `json:"summary,omitempty"`
	Details    json.RawMessage `json:"details,omitempty"`
	HasAlert   bool            `json:"has_alert"`
	Error      *string         `json:"error,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// Alert represents a notification generated by a watcher run.
type Alert struct {
	ID           uuid.UUID       `json:"id"`
	WatcherID    *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID        *uuid.UUID      `json:"run_id,omitempty"`
	WatcherTitle string          `json:"watcher_title,omitempty"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	Environment  string          `json:"environment,omitempty"`
	Severity     WatcherSeverity `json:"severity"`
	Details      json.RawMessage `json:"details,omitempty"`
	Read         bool            `json:"read"`
	Dismissed    bool            `json:"dismissed"`
	CreatedAt    time.Time       `json:"created_at"`
}

// CreateAlertParams defines the input for creating an alert.
type CreateAlertParams struct {
	WatcherID   *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID       *uuid.UUID      `json:"run_id,omitempty"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Environment string          `json:"environment,omitempty"`
	Severity    WatcherSeverity `json:"severity"`
	Details     json.RawMessage `json:"details,omitempty"`
}

// ListAlertParams defines filters for listing alerts.
type ListAlertParams struct {
	UnreadOnly  bool            `json:"unread_only,omitempty"`
	Severity    WatcherSeverity `json:"severity,omitempty"`
	WatcherID   *uuid.UUID      `json:"watcher_id,omitempty"`
	Environment string          `json:"environment,omitempty"`
	Limit       int             `json:"limit,omitempty"`
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

// Session represents an authenticated browser session.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}
