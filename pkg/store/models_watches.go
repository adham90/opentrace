package store

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

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

// ConditionsSummary returns a short human-readable description of a watch's
// conditions_json. For simple single-condition watches it extracts the metric;
// for compound conditions it returns a generic summary.
func ConditionsSummary(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return "unknown"
	}
	var peek struct {
		Type   string `json:"type,omitempty"`
		Metric string `json:"metric,omitempty"`
		All    []any  `json:"all,omitempty"`
		Any    []any  `json:"any,omitempty"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return "conditions"
	}
	if peek.Type != "" && peek.Metric != "" {
		return peek.Metric
	}
	if len(peek.All) > 0 {
		return "compound(all)"
	}
	if len(peek.Any) > 0 {
		return "compound(any)"
	}
	return "conditions"
}

// ConditionsMetric extracts the primary metric from a simple single-condition
// watch. Returns an empty WatchMetric for compound conditions.
func ConditionsMetric(raw json.RawMessage) WatchMetric {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var peek struct {
		Type   string      `json:"type,omitempty"`
		Metric WatchMetric `json:"metric,omitempty"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return ""
	}
	if peek.Type != "" {
		return peek.Metric
	}
	return ""
}

// ConditionsThreshold extracts the threshold value from a simple threshold
// condition. Returns 0 for compound or non-threshold conditions.
func ConditionsThreshold(raw json.RawMessage) float64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var peek struct {
		Type  string  `json:"type,omitempty"`
		Value float64 `json:"value,omitempty"`
	}
	if err := json.Unmarshal(raw, &peek); err != nil {
		return 0
	}
	if peek.Type == "threshold" {
		return peek.Value
	}
	return 0
}

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

// Watch represents a metric threshold monitor using a JSON condition tree.
//
// The ConditionsJSON field stores the full rule definition as a JSON condition
// tree which supports AND/OR/NOT combinators and multiple condition types
// (threshold, relative, delta, count). The Service, Endpoint, Environment, and
// CommitHash fields are kept for filtering/display and stream matching.
type Watch struct {
	bun.BaseModel       `bun:"table:watches" json:"-"`
	ID                  string              `bun:"id,pk" json:"id"`
	ConditionsJSON      json.RawMessage     `bun:"conditions_json" json:"conditions"`
	Service             string              `bun:"service" json:"service,omitempty"`
	Endpoint            string              `bun:"endpoint" json:"endpoint,omitempty"`
	Environment         string              `bun:"environment" json:"environment,omitempty"`
	CommitHash          string              `bun:"commit_hash" json:"commit_hash,omitempty"`
	Duration            string              `bun:"duration" json:"duration"`
	Urgency             WatchUrgency        `bun:"urgency" json:"urgency"`
	CheckInterval       string              `bun:"check_interval" json:"check_interval"`
	BaselineWindow      string              `bun:"baseline_window" json:"baseline_window"`
	MinConsecutive      int                 `bun:"min_consecutive" json:"min_consecutive"`
	Status              WatchStatus         `bun:"status" json:"status"`
	BaselineJSON        *WatchBaseline      `bun:"baseline_json" json:"baseline,omitempty"`
	ConsecutiveBreaches int                 `bun:"consecutive_breaches" json:"consecutive_breaches"`
	CurrentValue        *float64            `bun:"current_value" json:"current_value,omitempty"`
	ExpiresAt           *time.Time          `bun:"expires_at" json:"expires_at,omitempty"`
	CreatedBy           string              `bun:"created_by" json:"created_by,omitempty"`
	SessionID           string              `bun:"session_id" json:"session_id,omitempty"`
	LastCheckedAt       *time.Time          `bun:"last_checked_at" json:"last_checked_at,omitempty"`
	NextCheckAt         *time.Time          `bun:"next_check_at" json:"next_check_at,omitempty"`
	CreatedAt           time.Time           `bun:"created_at" json:"created_at"`
	UpdatedAt           time.Time           `bun:"updated_at" json:"updated_at"`
}

// CreateWatchParams defines the input for creating a watch.
// ConditionsJSON is required and contains the full condition tree.
type CreateWatchParams struct {
	ConditionsJSON json.RawMessage `json:"conditions"`
	Service        string          `json:"service,omitempty"`
	Endpoint       string          `json:"endpoint,omitempty"`
	Environment    string          `json:"environment,omitempty"`
	CommitHash     string          `json:"commit_hash,omitempty"`
	Duration       string          `json:"duration,omitempty"`
	Urgency        WatchUrgency    `json:"urgency,omitempty"`
	CheckInterval  string          `json:"check_interval,omitempty"`
	BaselineWindow string          `json:"baseline_window,omitempty"`
	MinConsecutive int             `json:"min_consecutive,omitempty"`
	CreatedBy      string          `json:"created_by,omitempty"`
	SessionID      string          `json:"session_id,omitempty"`
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
	bun.BaseModel `bun:"table:watch_runs" json:"-"`
	ID            string     `bun:"id,pk" json:"id"`
	WatchID       string     `bun:"watch_id" json:"watch_id"`
	Status        string     `bun:"status" json:"status"`
	MetricValue   *float64   `bun:"metric_value" json:"metric_value,omitempty"`
	Breached      bool       `bun:"breached" json:"breached"`
	Summary       string     `bun:"summary" json:"summary,omitempty"`
	ErrorMessage  string     `bun:"error_message" json:"error_message,omitempty"`
	StartedAt     time.Time  `bun:"started_at" json:"started_at"`
	FinishedAt    *time.Time `bun:"finished_at" json:"finished_at,omitempty"`
}

// WatchAlert represents an alert generated by a watch breach.
type WatchAlert struct {
	bun.BaseModel      `bun:"table:watch_alerts" json:"-"`
	ID                 string               `bun:"id,pk" json:"id"`
	WatchID            string               `bun:"watch_id" json:"watch_id"`
	RunID              string               `bun:"run_id" json:"run_id,omitempty"`
	Urgency            WatchUrgency         `bun:"urgency" json:"urgency"`
	Summary            string               `bun:"summary" json:"summary"`
	ConditionsSnapshot string               `bun:"conditions_snapshot" json:"conditions_snapshot"`
	EvidenceJSON       *WatchEvidenceBundle `bun:"evidence_json" json:"evidence,omitempty"`
	Status             string               `bun:"status" json:"status"`
	DismissReason      string               `bun:"dismiss_reason" json:"dismiss_reason,omitempty"`
	CreatedAt          time.Time            `bun:"created_at" json:"created_at"`
}

// Convenience accessors for backward-compatible reads from conditions_snapshot.
func (a WatchAlert) TriggerMetric() string  { return jsonField(a.ConditionsSnapshot, "metric") }
func (a WatchAlert) TriggerValue() float64  { return jsonFloat(a.ConditionsSnapshot, "actual_value") }
func (a WatchAlert) ThresholdValue() float64 { return jsonFloat(a.ConditionsSnapshot, "threshold") }

// CreateWatchAlertParams defines the input for creating a watch alert.
type CreateWatchAlertParams struct {
	WatchID            string               `json:"watch_id"`
	RunID              string               `json:"run_id,omitempty"`
	Urgency            WatchUrgency         `json:"urgency"`
	Summary            string               `json:"summary"`
	ConditionsSnapshot string               `json:"conditions_snapshot"`
	Evidence           *WatchEvidenceBundle `json:"evidence,omitempty"`
}

// WatchBaseline holds a snapshot of baseline metrics at watch creation time.
type WatchBaseline struct {
	ErrorRate        float64                 `json:"error_rate"`
	AvgResponseMs    float64                 `json:"avg_response_ms"`
	P95ResponseMs    float64                 `json:"p95_response_ms"`
	LogCount         int                     `json:"log_count"`
	ErrorCount       int                     `json:"error_count"`
	SQLCount         float64                 `json:"sql_count"`
	CacheHitRate     float64                 `json:"cache_hit_rate"`
	ExceptionClasses []string                `json:"exception_classes,omitempty"`
	Endpoints        []WatchEndpointBaseline `json:"endpoints,omitempty"`
	CapturedAt       time.Time               `json:"captured_at"`
	WindowDuration   string                  `json:"window_duration"`
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
	RecentErrors      []WatchEvidenceError `json:"recent_errors,omitempty"`
	NewErrors         []WatchEvidenceError `json:"new_errors,omitempty"`
	AffectedEndpoints []WatchEndpointDelta `json:"affected_endpoints,omitempty"`
	RelevantLogs      []WatchEvidenceLog   `json:"relevant_logs,omitempty"`
	Timeline          []WatchTimelineEvent `json:"timeline,omitempty"`
	BaselineDiff      *WatchBaselineDiff   `json:"baseline_diff,omitempty"`
	SourceFiles       []string             `json:"source_files,omitempty"`
	TraceIDs          []string             `json:"trace_ids,omitempty"`
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
	Path               string  `json:"path"`
	CurrentDurationMs  float64 `json:"current_duration_ms"`
	BaselineDurationMs float64 `json:"baseline_duration_ms"`
	DeltaPct           float64 `json:"delta_pct"`
}

// WatchBaselineDiff shows overall metric drift from baseline.
type WatchBaselineDiff struct {
	MetricName    string  `json:"metric_name"`
	BaselineValue float64 `json:"baseline_value"`
	CurrentValue  float64 `json:"current_value"`
	DeltaPct      float64 `json:"delta_pct"`
}

// WatchTimelineEvent marks a point in the alert timeline.
type WatchTimelineEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Event     string    `json:"event"`
	Value     *float64  `json:"value,omitempty"`
}

// BuildConditionsSnapshot creates a JSON snapshot for an alert.
func BuildConditionsSnapshot(metric string, actualValue, threshold float64) string {
	b, _ := json.Marshal(map[string]any{
		"metric":       metric,
		"actual_value": actualValue,
		"threshold":    threshold,
	})
	return string(b)
}

func jsonField(jsonStr, key string) string {
	var m map[string]any
	if json.Unmarshal([]byte(jsonStr), &m) != nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func jsonFloat(jsonStr, key string) float64 {
	var m map[string]any
	if json.Unmarshal([]byte(jsonStr), &m) != nil {
		return 0
	}
	f, _ := m[key].(float64)
	return f
}
