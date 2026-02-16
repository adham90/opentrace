package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when a requested resource does not exist.
var ErrNotFound = errors.New("not found")

// DataSourceStore defines CRUD operations for data sources.
type DataSourceStore interface {
	Create(ctx context.Context, params CreateDataSourceParams) (*DataSource, error)
	GetByID(ctx context.Context, id uuid.UUID) (*DataSource, error)
	List(ctx context.Context, params ListDataSourceParams) ([]DataSource, error)
	Update(ctx context.Context, id uuid.UUID, params UpdateDataSourceParams) (*DataSource, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

// LogStore defines operations for log ingestion and search.
type LogStore interface {
	BatchInsert(ctx context.Context, entries []LogEntry) (int, error)
	Search(ctx context.Context, params LogSearchParams) ([]LogEntry, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
	CountByLevel(ctx context.Context, params LogCountParams) (map[string]int, error)
	CountByService(ctx context.Context, params LogCountParams) ([]ServiceLogCount, error)
	DistinctValues(ctx context.Context, field string, params LogCountParams) ([]string, error)
	MetadataKeys(ctx context.Context, params LogCountParams) ([]string, error)
	GetByID(ctx context.Context, id int64) (*LogEntry, error)
	// Request performance
	SearchRequestSummaries(ctx context.Context, params RequestSummarySearchParams) ([]RequestSummaryResult, error)
	// Batch deduplication
	RecordBatch(ctx context.Context, batchID string, logCount int) error
	GetBatch(ctx context.Context, batchID string) (*BatchRecord, error)
	PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error)
}

// BatchRecord tracks a processed batch for deduplication.
type BatchRecord struct {
	BatchID    string
	LogCount   int
	ReceivedAt time.Time
}

// ServerStore manages monitored server registrations.
type ServerStore interface {
	Register(ctx context.Context, params RegisterServerParams) (*Server, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Server, error)
	List(ctx context.Context) ([]Server, error)
	UpdateHeartbeat(ctx context.Context, id uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
	MarkStaleOffline(ctx context.Context, threshold time.Duration) (int, error)
}

// MetricStore manages time-series metric data for servers.
type MetricStore interface {
	BatchInsert(ctx context.Context, serverID uuid.UUID, ts time.Time, samples []MetricSample) (int, error)
	Query(ctx context.Context, params MetricQuery) ([]MetricPoint, error)
	LatestByServer(ctx context.Context, serverID uuid.UUID) ([]MetricPoint, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// ErrEmailTaken is returned when a user attempts to register with an email already in use.
var ErrEmailTaken = errors.New("email already taken")

// ErrLastAdmin is returned when attempting to demote or delete the last admin user.
var ErrLastAdmin = errors.New("cannot remove the last admin")

// UserStore manages user accounts.
type UserStore interface {
	Create(ctx context.Context, params CreateUserParams) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByMCPToken(ctx context.Context, token string) (*User, error)
	List(ctx context.Context) ([]User, error)
	Update(ctx context.Context, id string, params UpdateUserParams) (*User, error)
	UpdatePassword(ctx context.Context, id string, passwordHash string) error
	UpdateMCPToken(ctx context.Context, id string, token string) error
	Delete(ctx context.Context, id string) error
	Count(ctx context.Context) (int, error)
}

// SessionStore manages browser sessions.
type SessionStore interface {
	Create(ctx context.Context, userID string, token string, expiresAt time.Time) (*Session, error)
	GetByToken(ctx context.Context, token string) (*Session, error)
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context) (int, error)
	DeleteAllForUser(ctx context.Context, userID string) error
}

// RetentionSettings holds data retention configuration.
type RetentionSettings struct {
	RetentionDays int `json:"retention_days"`
}

// SettingsStore manages application settings stored in app_config.
type SettingsStore interface {
	GetRetention(ctx context.Context) (*RetentionSettings, error)
	SetRetention(ctx context.Context, settings RetentionSettings) error
	GetAPIKey(ctx context.Context) (string, error)
	SetAPIKey(ctx context.Context, key string) error
}

// MCPActivityStore tracks MCP tool calls and connection events.
type MCPActivityStore interface {
	Log(ctx context.Context, params LogMCPActivityParams) error
	Stats(ctx context.Context) (*MCPActivityStats, error)
	Recent(ctx context.Context, limit int) ([]MCPActivityEvent, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// AuditStore tracks admin actions for security audit trail.
type AuditStore interface {
	Log(ctx context.Context, params LogAuditParams) error
	Recent(ctx context.Context, limit int) ([]AuditEntry, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// ErrorGroupStore manages error groups aggregated by fingerprint.
type ErrorGroupStore interface {
	Upsert(ctx context.Context, entry LogEntry) error
	Get(ctx context.Context, fingerprint string) (*ErrorGroup, error)
	List(ctx context.Context, params ListErrorGroupParams) ([]ErrorGroup, error)
	Count(ctx context.Context, status ErrorGroupStatus) (int, error)
	Resolve(ctx context.Context, fingerprint string, reason string) error
	Ignore(ctx context.Context, fingerprint string, reason string) error
	ListEvents(ctx context.Context, fingerprint string, limit int) ([]ErrorGroupEvent, error)
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// HealthCheckStore manages HTTP endpoint health checks (uptime monitoring).
type HealthCheckStore interface {
	Create(ctx context.Context, params CreateHealthCheckParams) (*HealthCheck, error)
	Get(ctx context.Context, id string) (*HealthCheck, error)
	List(ctx context.Context) ([]HealthCheck, error)
	Delete(ctx context.Context, id string) error
	SetEnabled(ctx context.Context, id string, enabled bool) error
	RecordResult(ctx context.Context, result HealthCheckResult) error
	LatestResults(ctx context.Context, healthcheckID string, limit int) ([]HealthCheckResult, error)
	UptimeSummaries(ctx context.Context, since time.Time) ([]UptimeSummary, error)
	PruneResults(ctx context.Context, olderThan time.Duration) (int64, error)
}

// WatchStore manages agent-first watches (Phase 1).
type WatchStore interface {
	Create(ctx context.Context, params CreateWatchParams) (*Watch, error)
	GetByID(ctx context.Context, id string) (*Watch, error)
	List(ctx context.Context, params ListWatchParams) ([]Watch, error)
	UpdateStatus(ctx context.Context, id string, status WatchStatus) error
	UpdateAfterCheck(ctx context.Context, id string, value float64, breaches int, nextCheck time.Time) error
	UpdateBaseline(ctx context.Context, id string, baseline *WatchBaseline) error
	Delete(ctx context.Context, id string) error
	GetDueWatches(ctx context.Context) ([]Watch, error)
	ExpireWatches(ctx context.Context) (int, error)
	CreateRun(ctx context.Context, watchID string) (*WatchRun, error)
	CompleteRun(ctx context.Context, id string, value float64, breached bool, summary string) error
	FailRun(ctx context.Context, id string, errMsg string) error
	ListRuns(ctx context.Context, watchID string, limit int) ([]WatchRun, error)
	CreateAlert(ctx context.Context, params CreateWatchAlertParams) (*WatchAlert, error)
	GetAlert(ctx context.Context, id string) (*WatchAlert, error)
	ListAlerts(ctx context.Context, watchID string, status string, limit int) ([]WatchAlert, error)
	DismissAlert(ctx context.Context, id string, reason string) error
	AcknowledgeAlert(ctx context.Context, id string) error
	CountPendingAlerts(ctx context.Context) (int, error)
}

