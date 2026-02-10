package watcher

import "github.com/adham90/opentrace/internal/store"

// Template represents a pre-built monitor configuration.
type Template struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Category    string           `json:"category"`
	MonitorType store.MonitorType `json:"monitor_type"`
	RuleConfig  store.RuleConfig `json:"rule_config"`
	Severity    string           `json:"severity"`
	TimeRange   string           `json:"time_range"`
}

// BuiltinTemplates returns the list of pre-built monitor templates.
func BuiltinTemplates() []Template {
	return []Template{
		// Database category
		{
			ID:          "stuck-transactions",
			Name:        "Stuck Transactions",
			Description: "Alert when transactions are idle for more than 5 minutes",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - xact_start > interval '5 minutes'",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 0,
			},
			Severity:  "critical",
			TimeRange: "5m",
		},
		{
			ID:          "connection-saturation",
			Name:        "Connection Saturation",
			Description: "Alert when active connections exceed threshold",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT count(*) FROM pg_stat_activity",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 80,
			},
			Severity:  "warning",
			TimeRange: "5m",
		},
		{
			ID:          "long-running-queries",
			Name:        "Long-Running Queries",
			Description: "Alert when queries run for more than 30 seconds",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '30 seconds'",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 3,
			},
			Severity:  "warning",
			TimeRange: "5m",
		},
		{
			ID:          "replication-lag",
			Name:        "Replication Lag",
			Description: "Alert when replication lag exceeds 60 seconds",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT EXTRACT(EPOCH FROM replay_lag) FROM pg_stat_replication",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 60,
			},
			Severity:  "critical",
			TimeRange: "5m",
		},
		{
			ID:          "dead-tuple-bloat",
			Name:        "Dead Tuple Bloat",
			Description: "Alert when dead tuples exceed threshold on any table",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT max(n_dead_tup) FROM pg_stat_user_tables",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 50000,
			},
			Severity:  "info",
			TimeRange: "15m",
		},
		{
			ID:          "table-size-growth",
			Name:        "Table Size Growth",
			Description: "Alert when any table exceeds 1GB",
			Category:    "database",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT max(pg_total_relation_size(relid)) / (1024*1024*1024.0) FROM pg_stat_user_tables",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 1,
			},
			Severity:  "info",
			TimeRange: "1h",
		},

		// Logs category
		{
			ID:          "error-rate-spike",
			Name:        "Error Rate Spike",
			Description: "Alert when error logs exceed threshold in time window",
			Category:    "logs",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:   store.RuleSourceLogs,
				Metric:   "count",
				Operator: store.OpGreaterThan,
				Threshold: 10,
				Filter: &store.LogFilter{
					Level: "ERROR",
				},
				TimeWindow: "5m",
			},
			Severity:  "warning",
			TimeRange: "5m",
		},
		{
			ID:          "silent-service",
			Name:        "Silent Service (Heartbeat)",
			Description: "Alert when a service stops producing logs",
			Category:    "logs",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:     store.RuleSourceLogs,
				Metric:     "count",
				Operator:   store.OpLessThan,
				Threshold:  1,
				Filter:     &store.LogFilter{},
				TimeWindow: "15m",
			},
			Severity:  "critical",
			TimeRange: "15m",
		},
		{
			ID:          "auth-failure-spike",
			Name:        "Auth Failure Spike",
			Description: "Alert when authentication failures exceed threshold",
			Category:    "logs",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:   store.RuleSourceLogs,
				Metric:   "count",
				Operator: store.OpGreaterThan,
				Threshold: 20,
				Filter: &store.LogFilter{
					Query: "401 403 unauthorized",
				},
				TimeWindow: "5m",
			},
			Severity:  "warning",
			TimeRange: "5m",
		},
		{
			ID:          "specific-error-pattern",
			Name:        "Specific Error Pattern",
			Description: "Alert when a specific error message appears",
			Category:    "logs",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:     store.RuleSourceLogs,
				Metric:     "count",
				Operator:   store.OpGreaterThan,
				Threshold:  0,
				Filter:     &store.LogFilter{Level: "ERROR"},
				TimeWindow: "5m",
			},
			Severity:  "warning",
			TimeRange: "5m",
		},

		// Health category
		{
			ID:          "database-connectivity",
			Name:        "Database Connectivity",
			Description: "Alert when database connection fails",
			Category:    "health",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source: store.RuleSourceHealth,
				Checks: []string{"connection"},
			},
			Severity:  "critical",
			TimeRange: "1m",
		},
		{
			ID:          "database-latency",
			Name:        "Database Latency",
			Description: "Alert when database ping latency exceeds 2 seconds",
			Category:    "health",
			MonitorType: store.MonitorTypeRule,
			RuleConfig: store.RuleConfig{
				Source:           store.RuleSourceHealth,
				Checks:           []string{"connection", "latency"},
				LatencyThreshold: 2000,
			},
			Severity:  "warning",
			TimeRange: "1m",
		},
	}
}
