package apiclient

import "time"

// StatusResponse is the JSON response from GET /api/cli/status.
type StatusResponse struct {
	Version       string          `json:"version"`
	UptimeSeconds int64           `json:"uptime_seconds"`
	Database      *DatabaseStatus `json:"database,omitempty"`
	Logs          *LogStats       `json:"logs,omitempty"`
	ErrorGroups   *ErrorStats     `json:"error_groups,omitempty"`
	Services      []ServiceInfo   `json:"services,omitempty"`
	Watches       *WatchStats     `json:"watches,omitempty"`
	WatchAlerts   *AlertStats     `json:"watch_alerts,omitempty"`
	Connectors    *ConnectorStats `json:"connectors,omitempty"`
	Servers       *ServerStats    `json:"servers,omitempty"`
	HealthChecks  *HealthStats    `json:"healthchecks,omitempty"`
}

type DatabaseStatus struct {
	Healthy   bool  `json:"healthy"`
	SizeBytes int64 `json:"size_bytes"`
}

type LogStats struct {
	LastHour       int `json:"last_hour"`
	ErrorsLastHour int `json:"errors_last_hour"`
}

type ErrorStats struct {
	Unresolved int `json:"unresolved"`
}

type ServiceInfo struct {
	Name     string    `json:"name"`
	LastSeen time.Time `json:"last_seen"`
	Status   string    `json:"status"`
}

type WatchStats struct {
	Active    int `json:"active"`
	Triggered int `json:"triggered"`
	Resolved  int `json:"resolved"`
	Expired   int `json:"expired"`
}

type AlertStats struct {
	Pending int `json:"pending"`
}

type ConnectorStats struct {
	Total     int `json:"total"`
	Connected int `json:"connected"`
	Error     int `json:"error"`
}

type ServerStats struct {
	Total   int `json:"total"`
	Online  int `json:"online"`
	Offline int `json:"offline"`
}

type HealthStats struct {
	Total    int `json:"total"`
	Down     int `json:"down"`
	Degraded int `json:"degraded"`
}

// LogTailResponse is the JSON response from GET /api/cli/logs/tail.
type LogTailResponse struct {
	Logs    []LogEntry `json:"logs"`
	Cursor  int64      `json:"cursor"`
	HasMore bool       `json:"has_more"`
}

// LogEntry is a single log line from the API.
type LogEntry struct {
	ID             int64           `json:"id"`
	Timestamp      time.Time       `json:"timestamp"`
	Level          string          `json:"level"`
	Service        string          `json:"service,omitempty"`
	Environment    string          `json:"environment,omitempty"`
	Message        string          `json:"message"`
	EventType      string          `json:"event_type,omitempty"`
	RequestID      string          `json:"request_id,omitempty"`
	ExceptionClass string          `json:"exception_class,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
	RequestSummary *RequestSummary `json:"request_summary,omitempty"`
}

// RequestSummary holds structured performance metrics for an HTTP request.
type RequestSummary struct {
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
	DuplicateQueries    int     `json:"duplicate_queries,omitempty"`
	MemoryDeltaMb       float64 `json:"memory_delta_mb,omitempty"`
	Timeline            string  `json:"timeline,omitempty"`
}

// ErrorGroupsResponse is the JSON response from GET /api/cli/errors/top.
type ErrorGroupsResponse struct {
	ErrorGroups []ErrorGroupEntry `json:"error_groups"`
}

type ErrorGroupEntry struct {
	Fingerprint     string    `json:"fingerprint"`
	ExceptionClass  string    `json:"exception_class"`
	Message         string    `json:"message"`
	Service         string    `json:"service"`
	OccurrenceCount int       `json:"occurrence_count"`
	FirstSeenAt     time.Time `json:"first_seen_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ImpactScore     float64   `json:"impact_score"`
	UniqueUsers     int       `json:"unique_users"`
}

// WatchesResponse is the JSON response from GET /api/cli/watches.
type WatchesResponse struct {
	Watches []WatchEntry `json:"watches"`
	Alerts  struct {
		Pending int          `json:"pending"`
		Recent  []AlertEntry `json:"recent"`
	} `json:"alerts"`
}

type WatchEntry struct {
	ID                  string    `json:"id"`
	Conditions          string    `json:"conditions"`
	Service             string    `json:"service,omitempty"`
	Status              string    `json:"status"`
	Urgency             string    `json:"urgency"`
	CurrentValue        *float64  `json:"current_value,omitempty"`
	ConsecutiveBreaches int       `json:"consecutive_breaches"`
	LastCheckedAt       *string   `json:"last_checked_at,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

type AlertEntry struct {
	ID        string    `json:"id"`
	WatchID   string    `json:"watch_id"`
	Urgency   string    `json:"urgency"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// IngestionStatsResponse is the JSON response from GET /api/cli/ingestion/stats.
type IngestionStatsResponse struct {
	Buckets []HistogramBucket `json:"buckets"`
}

type HistogramBucket struct {
	Timestamp  time.Time `json:"timestamp"`
	Total      int       `json:"total"`
	ErrorCount int       `json:"error_count"`
}
