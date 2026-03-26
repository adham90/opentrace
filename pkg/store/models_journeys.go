package store

import (
	"time"

	"github.com/uptrace/bun"
)

// UserSession represents a pre-computed user session aggregation.
type UserSession struct {
	bun.BaseModel   `bun:"table:user_sessions" json:"-"`
	ID              int64     `bun:"id,pk,autoincrement" json:"id"`
	SessionID       string    `bun:"session_id" json:"session_id"`
	UserID          string    `bun:"user_id" json:"user_id,omitempty"`
	Service         string    `bun:"service" json:"service"`
	Environment     string    `bun:"environment" json:"environment,omitempty"`
	StartedAt       time.Time `bun:"started_at" json:"started_at"`
	EndedAt         time.Time `bun:"ended_at" json:"ended_at"`
	RequestCount    int       `bun:"request_count" json:"request_count"`
	ErrorCount      int       `bun:"error_count" json:"error_count"`
	TotalDurationMs float64   `bun:"total_duration_ms" json:"total_duration_ms"`
	EntryPath       string    `bun:"entry_path" json:"entry_path"`
	ExitPath        string    `bun:"exit_path" json:"exit_path"`
	ExitStatus      int       `bun:"exit_status" json:"exit_status"`
	HasError        bool      `bun:"has_error" json:"has_error"`
	CreatedAt       time.Time `bun:"created_at" json:"created_at"`
}

// SessionListParams defines filters for listing user sessions.
type SessionListParams struct {
	UserID   string    `json:"user_id,omitempty"`
	Service  string    `json:"service,omitempty"`
	HasError *bool     `json:"has_error,omitempty"`
	Since    time.Time `json:"since"`
	Until    time.Time `json:"until"`
	Limit    int       `json:"limit"`
	Offset   int       `json:"offset"`
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
	bun.BaseModel `bun:"table:funnels" json:"-"`
	ID            int64        `bun:"id,pk,autoincrement" json:"id"`
	Name          string       `bun:"name" json:"name"`
	Service       string       `bun:"service" json:"service,omitempty"`
	Steps         []FunnelStep `bun:"steps" json:"steps"`
	CreatedAt     time.Time    `bun:"created_at" json:"created_at"`
	UpdatedAt     time.Time    `bun:"updated_at" json:"updated_at"`
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
