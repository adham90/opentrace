package watcher

import "encoding/json"

// DeadmanConfig configures the deadman evaluator.
// Alerts when expected events are NOT seen within the window.
type DeadmanConfig struct {
	Source   string     `json:"source"`              // "logs" or "query"
	Filter   *LogFilter `json:"filter,omitempty"`    // log search criteria (for source=logs)
	Query    string     `json:"query,omitempty"`     // SQL query (for source=query)
	Metric   string     `json:"metric,omitempty"`    // "row_count" or "value" (for source=query)
	MinCount int        `json:"min_count"`           // minimum expected events
	Window   string     `json:"window"`              // time window, e.g. "30m"
}

// LogFilter defines log search criteria (mirrors store.LogFilter for decoupling).
type LogFilter struct {
	Service     string `json:"service,omitempty"`
	Level       string `json:"level,omitempty"`
	Query       string `json:"query,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// DiffConfig configures the diff evaluator.
// Compares current query results against the previous run's snapshot.
type DiffConfig struct {
	Source          string   `json:"source"`                     // "query"
	Query           string   `json:"query"`                      // SQL query to execute
	CompareMode     string   `json:"compare_mode"`               // "hash", "row_count", or "exact"
	IgnoreColumns   []string `json:"ignore_columns,omitempty"`   // columns to exclude from comparison
	ChangeThreshold float64  `json:"change_threshold,omitempty"` // % threshold for row_count mode
}

// CompositeConfig configures the composite evaluator.
// Correlates multiple signals with boolean logic.
type CompositeConfig struct {
	Logic      string               `json:"logic"`      // "all_of", "any_of", or "none_of"
	Conditions []CompositeCondition `json:"conditions"`
}

// CompositeCondition is a single check within a composite watcher.
type CompositeCondition struct {
	WatcherID  string          `json:"watcher_id,omitempty"`  // reference an existing watcher's latest run
	RuleConfig json.RawMessage `json:"rule_config,omitempty"` // inline rule evaluation
	Label      string          `json:"label,omitempty"`       // human-readable label
}

// TrendConfig configures the trend evaluator.
// Detects metric changes relative to a rolling baseline.
type TrendConfig struct {
	Source        string     `json:"source"`                   // "query" or "logs"
	Query         string     `json:"query,omitempty"`          // SQL query (for source=query)
	Filter        *LogFilter `json:"filter,omitempty"`         // log search criteria (for source=logs)
	Metric        string     `json:"metric,omitempty"`         // "row_count" or "value"
	BaselineRuns  int        `json:"baseline_runs"`            // number of past runs to average
	ChangePercent float64    `json:"change_percent"`           // % change threshold
	Direction     string     `json:"direction"`                // "increase", "decrease", or "both"
	Window        string     `json:"window,omitempty"`         // time window for log queries
}

// SequenceConfig configures the sequence evaluator.
// Detects ordered patterns of events within a time window.
type SequenceConfig struct {
	Steps  []SequenceStep `json:"steps"`
	Window string         `json:"window"` // overall time window
}

// SequenceStep is a single ordered event within a sequence.
type SequenceStep struct {
	Source    string     `json:"source"`              // "logs" or "alert"
	Filter   *LogFilter `json:"filter,omitempty"`    // log search criteria (for source=logs)
	WatcherID string    `json:"watcher_id,omitempty"` // watcher to check alerts for (for source=alert)
	Label    string     `json:"label,omitempty"`      // human-readable label
}
