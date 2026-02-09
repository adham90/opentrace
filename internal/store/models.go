package store

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type ConnectorType string

const (
	ConnectorLogs       ConnectorType = "logs"
	ConnectorDatabase   ConnectorType = "database"
	ConnectorMonitoring ConnectorType = "monitoring"
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
	Type   ConnectorType  `json:"type"`
	Name   string         `json:"name"`
	Config map[string]any `json:"config"`
}

type UpdateDataSourceParams struct {
	Status        *ConnectorStatus `json:"status,omitempty"`
	StatusMessage *string          `json:"status_message,omitempty"`
	LastTestedAt  *time.Time       `json:"last_tested_at,omitempty"`
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

// WatcherSeverity represents the severity level of a watcher.
type WatcherSeverity string

const (
	SeverityInfo     WatcherSeverity = "info"
	SeverityWarning  WatcherSeverity = "warning"
	SeverityCritical WatcherSeverity = "critical"
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
	ID          uuid.UUID       `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Severity    WatcherSeverity `json:"severity"`
	Filters     json.RawMessage `json:"filters"`
	TimeRange   string          `json:"time_range"`
	Model       string          `json:"model"`
	Status      WatcherStatus   `json:"status"`
	Notify      json.RawMessage `json:"notify"`
	LastRunAt   *time.Time      `json:"last_run_at,omitempty"`
	NextRunAt   *time.Time      `json:"next_run_at,omitempty"`
	LastError   *string         `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateWatcherParams defines the input for creating a watcher.
type CreateWatcherParams struct {
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Severity    WatcherSeverity `json:"severity"`
	Filters     json.RawMessage `json:"filters"`
	TimeRange   string          `json:"time_range"`
	Model       string          `json:"model"`
	Notify      json.RawMessage `json:"notify"`
}

// UpdateWatcherParams defines the input for updating a watcher.
type UpdateWatcherParams struct {
	Title       *string          `json:"title,omitempty"`
	Description *string          `json:"description,omitempty"`
	Severity    *WatcherSeverity `json:"severity,omitempty"`
	Filters     json.RawMessage  `json:"filters,omitempty"`
	TimeRange   *string          `json:"time_range,omitempty"`
	Model       *string          `json:"model,omitempty"`
	Notify      json.RawMessage  `json:"notify,omitempty"`
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
	ID        uuid.UUID       `json:"id"`
	WatcherID *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID     *uuid.UUID      `json:"run_id,omitempty"`
	Title     string          `json:"title"`
	Summary   string          `json:"summary"`
	Severity  WatcherSeverity `json:"severity"`
	Details   json.RawMessage `json:"details,omitempty"`
	Read      bool            `json:"read"`
	Dismissed bool            `json:"dismissed"`
	CreatedAt time.Time       `json:"created_at"`
}

// CreateAlertParams defines the input for creating an alert.
type CreateAlertParams struct {
	WatcherID *uuid.UUID      `json:"watcher_id,omitempty"`
	RunID     *uuid.UUID      `json:"run_id,omitempty"`
	Title     string          `json:"title"`
	Summary   string          `json:"summary"`
	Severity  WatcherSeverity `json:"severity"`
	Details   json.RawMessage `json:"details,omitempty"`
}

// ListAlertParams defines filters for listing alerts.
type ListAlertParams struct {
	UnreadOnly bool       `json:"unread_only,omitempty"`
	WatcherID  *uuid.UUID `json:"watcher_id,omitempty"`
	Limit      int        `json:"limit,omitempty"`
}
