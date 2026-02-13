package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

func runSeed() error {
	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.db.Close()

	slog.Info("seeding database")

	// --- Servers ---
	servers := []store.RegisterServerParams{
		{Hostname: "web-01.prod", IPAddress: "10.0.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "web-02.prod", IPAddress: "10.0.1.11", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "api-01.prod", IPAddress: "10.0.2.10", OS: "linux", Arch: "arm64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "api"}},
		{Hostname: "db-01.prod", IPAddress: "10.0.3.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.0", Labels: map[string]string{"env": "production", "role": "database"}},
		{Hostname: "worker-01.staging", IPAddress: "10.1.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "staging", "role": "worker"}},
	}

	serverStore := store.NewServerStore(deps.db)
	var serverIDs []uuid.UUID
	for _, p := range servers {
		s, err := serverStore.Register(ctx, p)
		if err != nil {
			return fmt.Errorf("registering server %s: %w", p.Hostname, err)
		}
		serverIDs = append(serverIDs, s.ID)
		slog.Info("seeded server", "hostname", p.Hostname, "id", s.ID)
	}

	// --- Metrics for each server ---
	metricStore := store.NewMetricStore(deps.db)
	for i, sid := range serverIDs {
		for m := 0; m < 30; m++ {
			ts := time.Now().Add(-time.Duration(30-m) * 2 * time.Minute)
			cpuBase := 15.0 + float64(i*10)
			memBase := 40.0 + float64(i*8)
			samples := []store.MetricSample{
				{Name: "cpu.usage_percent", Value: cpuBase + rand.Float64()*20, Unit: "percent"},
				{Name: "memory.usage_percent", Value: memBase + rand.Float64()*15, Unit: "percent"},
				{Name: "disk.usage_percent", Value: 30 + rand.Float64()*40, Unit: "percent"},
				{Name: "network.rx_bytes", Value: rand.Float64() * 1e8, Unit: "bytes"},
				{Name: "network.tx_bytes", Value: rand.Float64() * 5e7, Unit: "bytes"},
			}
			metricStore.BatchInsert(ctx, sid, ts, samples)
		}
	}
	slog.Info("seeded metrics", "servers", len(serverIDs), "snapshots", 30)

	// --- Logs ---
	services := []string{"payment-api", "user-service", "gateway", "notification-service", "order-service"}
	levels := []string{"DEBUG", "INFO", "INFO", "INFO", "WARN", "ERROR"} // weighted toward INFO
	messages := map[string][]string{
		"DEBUG": {
			"Cache miss for key user:session:abc123",
			"DB query took 12ms: SELECT * FROM orders WHERE id = ?",
			"Retry attempt 1/3 for external API call",
			"Connection pool stats: active=5 idle=15 max=20",
		},
		"INFO": {
			"Request processed successfully in 45ms",
			"User login: alice@example.com from 203.0.113.42",
			"Order #ORD-9821 created successfully",
			"Payment $129.99 processed for order #ORD-9821",
			"Webhook delivered to https://hooks.example.com/notify",
			"Background job completed: email_digest (processed 142 items)",
			"Health check passed for upstream service",
			"New deployment v2.14.3 rolling out",
			"Cache warmed: 2,847 entries loaded in 3.2s",
			"Rate limit reset for client app-mobile-ios",
		},
		"WARN": {
			"Slow query detected: 850ms for /api/v1/reports/summary",
			"Connection pool nearing capacity: 18/20 active connections",
			"Deprecated API endpoint called: POST /api/v1/legacy/users",
			"Circuit breaker half-open for payment-gateway",
			"Memory usage at 82% — approaching threshold",
			"Request retry succeeded after 2 attempts",
		},
		"ERROR": {
			"Failed to process payment: card declined (4111****1234)",
			"Database connection timeout after 30s",
			"Unhandled exception in /api/v1/orders: NullPointerException",
			"External API returned 503: service unavailable",
			"Failed to send notification email: SMTP connection refused",
			"Rate limit exceeded for client app-web-dashboard",
			"TLS handshake failed with upstream: certificate expired",
		},
	}

	var logEntries []store.LogEntry
	now := time.Now()
	for i := 0; i < 500; i++ {
		level := levels[rand.Intn(len(levels))]
		svc := services[rand.Intn(len(services))]
		msgs := messages[level]
		msg := msgs[rand.Intn(len(msgs))]
		ts := now.Add(-time.Duration(rand.Intn(7200)) * time.Second) // last 2 hours

		entry := store.LogEntry{
			Timestamp: ts,
			Level:     level,
			Service:   svc,
			Message:   msg,
		}
		if rand.Float32() < 0.3 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		logEntries = append(logEntries, entry)
	}

	// --- Business event logs (event_type) ---
	eventEntries := []struct {
		eventType string
		service   string
		message   string
	}{
		{"payment.completed", "payment-api", "Payment $129.99 processed for order #ORD-9821"},
		{"payment.completed", "payment-api", "Payment $49.00 processed for order #ORD-9834"},
		{"payment.failed", "payment-api", "Payment declined for order #ORD-9842: insufficient funds"},
		{"payment.refunded", "payment-api", "Refund $49.00 issued for order #ORD-9801"},
		{"auth.login", "user-service", "User alice@example.com logged in via password"},
		{"auth.login", "user-service", "User bob@example.com logged in via Google OAuth"},
		{"auth.logout", "user-service", "User alice@example.com logged out"},
		{"auth.failed", "user-service", "Failed login attempt for unknown@example.com (3rd attempt)"},
		{"order.created", "order-service", "Order #ORD-9850 created: 3 items, total $189.97"},
		{"order.shipped", "order-service", "Order #ORD-9821 shipped via FedEx tracking #FX123456"},
		{"order.delivered", "order-service", "Order #ORD-9800 delivered and confirmed"},
		{"notification.sent", "notification-service", "Welcome email sent to newuser@example.com"},
		{"notification.sent", "notification-service", "Order confirmation SMS sent to +1555123456"},
		{"user.registered", "user-service", "New user registered: charlie@example.com"},
		{"deploy.completed", "gateway", "Deployment v2.14.3 completed successfully"},
	}
	for _, ev := range eventEntries {
		ts := now.Add(-time.Duration(rand.Intn(7200)) * time.Second)
		entry := store.LogEntry{
			Timestamp: ts,
			Level:     "INFO",
			Service:   ev.service,
			Message:   ev.message,
			EventType: ev.eventType,
		}
		if rand.Float32() < 0.3 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		logEntries = append(logEntries, entry)
	}

	logStore := store.NewLogStore(deps.db)
	n, err := logStore.BatchInsert(ctx, logEntries)
	if err != nil {
		return fmt.Errorf("inserting logs: %w", err)
	}
	slog.Info("seeded logs", "count", n)

	// --- Watchers ---
	watcherDefs := []store.CreateWatcherParams{
		{
			Title:       "Payment Error Spike",
			Description: "Alert when payment errors exceed normal levels",

			Severity:    store.SeverityCritical,
			Filters:     json.RawMessage(`{"service":"payment-api","level":"ERROR"}`),
			TimeRange:   "15m",
			Effort:      store.EffortHigh,
			Notify:      json.RawMessage(`["dashboard"]`),
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "1m",
				EscalationDuration: "30m",
				CooldownRuns:       3,
				MaxConsecErrors:    5,
			},
		},
		{
			Title:       "Slow Query Watcher",
			Description: "Detect slow database queries across all services",

			Severity:    store.SeverityWarning,
			Filters:     json.RawMessage(`{"level":"WARN"}`),
			TimeRange:   "30m",
			Effort:      store.EffortMedium,
			Notify:      json.RawMessage(`["dashboard"]`),
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "2m",
				EscalationDuration: "1h",
				CooldownRuns:       3,
				RelaxEnabled:       true,
				RelaxedInterval:    "2h",
				RelaxAfterRuns:     20,
				MaxConsecErrors:    5,
			},
		},
		{
			Title:       "Error Rate Watcher",
			Description: "Watch for unusual error patterns in all services",

			Severity:    store.SeverityWarning,
			Filters:     json.RawMessage(`{"level":"ERROR"}`),
			TimeRange:   "1h",
			Effort:      store.EffortMedium,
			Notify:      json.RawMessage(`["dashboard"]`),
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:         true,
				CooldownRuns:    3,
				MaxConsecErrors: 5,
			},
		},
		{
			Title:       "Staging Health Check",
			Description: "Watch staging environment for issues before prod deploy",
			Severity:    store.SeverityInfo,
			Filters:     json.RawMessage(`{}`),
			TimeRange:   "1h",
			Effort:      store.EffortLow,
			Notify:      json.RawMessage(`["dashboard"]`),
		},
		{
			Title:       "Connection Saturation",
			Description: "Watch PostgreSQL connection pool usage and alert on saturation",

			Severity:    store.SeverityCritical,
			Filters:     json.RawMessage(`{"service":"gateway"}`),
			TimeRange:   "5m",
			Effort:      store.EffortMedium,
			Notify:      json.RawMessage(`["dashboard"]`),
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "30s",
				EscalationDuration: "15m",
				CooldownRuns:       5,
				RelaxEnabled:       true,
				RelaxedInterval:    "30m",
				RelaxAfterRuns:     30,
				MaxConsecErrors:    3,
			},
		},
		{
			Title:       "Replication Lag",
			Description: "Alert when database replication lag exceeds acceptable levels",

			Severity:    store.SeverityCritical,
			Filters:     json.RawMessage(`{"service":"db-replica"}`),
			TimeRange:   "5m",
			Effort:      store.EffortLow,
			Notify:      json.RawMessage(`["dashboard"]`),
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "1m",
				EscalationDuration: "20m",
				CooldownRuns:       3,
				MaxConsecErrors:    5,
				BackoffMultiplier:  2.0,
				MaxBackoffInterval: "1h",
			},
		},

		// --- Rule watchers: query source ---
		{
			Title:       "Idle-in-Transaction Sessions",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT count(*) FROM pg_stat_activity WHERE state = 'idle in transaction' AND xact_start < now() - interval '5 minutes'",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 3,
			},
		},
		{
			Title:       "Dead Tuples on Orders Table",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "15m",
			Schedule:    "*/15 * * * *",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT n_dead_tup FROM pg_stat_user_tables WHERE relname = 'orders'",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 50000,
			},
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "2m",
				EscalationDuration: "30m",
				CooldownRuns:       3,
				MaxConsecErrors:    5,
			},
		},
		{
			Title:       "Long-Running Queries",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceQuery,
				Query:     "SELECT count(*) FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '30 seconds'",
				Metric:    "value",
				Operator:  store.OpGreaterThan,
				Threshold: 5,
			},
		},

		// --- Rule watchers: logs source ---
		{
			Title:       "Error Log Volume",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceLogs,
				Metric:    "count",
				Operator:  store.OpGreaterThan,
				Threshold: 50,
				TimeWindow: "5m",
				Filter: &store.LogFilter{
					Level: "ERROR",
				},
			},
		},
		{
			Title:       "Payment Service Timeouts",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceLogs,
				Metric:    "count",
				Operator:  store.OpGreaterThan,
				Threshold: 10,
				TimeWindow: "5m",
				Filter: &store.LogFilter{
					Service: "payment-api",
					Level:   "ERROR",
					Query:   "timeout",
				},
			},
			AdaptiveConfig: &store.AdaptiveConfig{
				Enabled:            true,
				EscalatedInterval:  "1m",
				EscalationDuration: "15m",
				CooldownRuns:       3,
				MaxConsecErrors:    5,
			},
		},
		{
			Title:       "Gateway 503 Errors",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "15m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source:    store.RuleSourceLogs,
				Metric:    "count",
				Operator:  store.OpGreaterThan,
				Threshold: 20,
				TimeWindow: "15m",
				Filter: &store.LogFilter{
					Service: "gateway",
					Query:   "503",
				},
			},
		},

		// --- Deadman watchers ---
		{
			Title:       "Heartbeat: Payment API",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeDeadman,
			TimeRange:   "5m",
			Schedule:    "*/5 * * * *",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"logs","filter":{"service":"payment-api","level":"INFO"},"min_count":5,"window":"5m"}`),
		},
		{
			Title:       "Heartbeat: Order Processing",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeDeadman,
			TimeRange:   "15m",
			Schedule:    "*/15 * * * *",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"logs","filter":{"service":"order-service","level":"INFO","query":"processed"},"min_count":1,"window":"15m"}`),
		},

		// --- Diff watchers ---
		{
			Title:       "Schema Drift: Users Table",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeDiff,
			TimeRange:   "1h",
			Schedule:    "@hourly",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"query","query":"SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = 'users' ORDER BY ordinal_position","compare_mode":"hash"}`),
		},
		{
			Title:       "Config Table Changes",

			Severity:    store.SeverityInfo,
			WatcherType: store.WatcherTypeDiff,
			TimeRange:   "30m",
			Schedule:    "*/30 * * * *",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"query","query":"SELECT key, value FROM app_config ORDER BY key","compare_mode":"exact","ignore_columns":["updated_at"]}`),
		},

		// --- Composite watchers ---
		{
			Title:       "Payment Pipeline Health",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeComposite,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"logic":"all_of","conditions":[{"label":"Payment errors high","rule_config":"{\"source\":\"logs\",\"metric\":\"count\",\"operator\":\"gt\",\"threshold\":10,\"time_window\":\"5m\",\"filter\":{\"service\":\"payment-api\",\"level\":\"ERROR\"}}"},{"label":"Gateway degraded","rule_config":"{\"source\":\"logs\",\"metric\":\"count\",\"operator\":\"gt\",\"threshold\":5,\"time_window\":\"5m\",\"filter\":{\"service\":\"gateway\",\"query\":\"503\"}}"}]}`),
		},
		{
			Title:       "Any Service Down",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeComposite,
			TimeRange:   "5m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"logic":"any_of","conditions":[{"label":"Payment heartbeat missing","rule_config":"{\"source\":\"logs\",\"metric\":\"count\",\"operator\":\"lt\",\"threshold\":1,\"time_window\":\"5m\",\"filter\":{\"service\":\"payment-api\",\"level\":\"INFO\"}}"},{"label":"Gateway heartbeat missing","rule_config":"{\"source\":\"logs\",\"metric\":\"count\",\"operator\":\"lt\",\"threshold\":1,\"time_window\":\"5m\",\"filter\":{\"service\":\"gateway\",\"level\":\"INFO\"}}"}]}`),
		},

		// --- Trend watchers ---
		{
			Title:       "Error Rate Trend",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeTrend,
			TimeRange:   "1h",
			Schedule:    "@hourly",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"logs","filter":{"level":"ERROR"},"baseline_runs":5,"change_percent":50,"direction":"increase","window":"1h"}`),
		},
		{
			Title:       "Order Volume Trend",

			Severity:    store.SeverityInfo,
			WatcherType: store.WatcherTypeTrend,
			TimeRange:   "1h",
			Schedule:    "@hourly",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"source":"logs","filter":{"service":"order-service","level":"INFO","query":"created"},"baseline_runs":5,"change_percent":30,"direction":"both","window":"1h"}`),
		},

		// --- Sequence watchers ---
		{
			Title:       "Cascading Failure: Auth → API → DB",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeSequence,
			TimeRange:   "1h",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"window":"1h","steps":[{"source":"logs","filter":{"service":"user-service","level":"ERROR"},"label":"auth_errors"},{"source":"logs","filter":{"service":"gateway","level":"ERROR","query":"503"},"label":"gateway_503"},{"source":"logs","filter":{"service":"order-service","level":"ERROR","query":"timeout"},"label":"order_timeouts"}]}`),
		},
		{
			Title:       "Deploy → Error Spike Pattern",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeSequence,
			TimeRange:   "30m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			TypeConfig:  json.RawMessage(`{"window":"30m","steps":[{"source":"logs","filter":{"level":"INFO","query":"deployment"},"label":"deploy_started"},{"source":"logs","filter":{"level":"ERROR"},"label":"error_spike"}]}`),
		},

		// --- Rule watchers: health source ---
		{
			Title:       "Primary DB Health",

			Severity:    store.SeverityCritical,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "1m",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source: store.RuleSourceHealth,
				Checks: []string{"connection", "latency"},
				LatencyThreshold: 2000,
			},
		},
		{
			Title:       "Replica DB Health",

			Severity:    store.SeverityWarning,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "5m",
			Schedule:    "*/5 * * * *",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source: store.RuleSourceHealth,
				Checks: []string{"connection"},
			},
		},
		{
			Title:       "Staging DB Connectivity",

			Severity:    store.SeverityInfo,
			WatcherType: store.WatcherTypeRule,
			TimeRange:   "15m",
			Schedule:    "@hourly",
			Filters:     json.RawMessage(`{}`),
			Notify:      json.RawMessage(`["dashboard"]`),
			RuleConfig: &store.RuleConfig{
				Source: store.RuleSourceHealth,
				Checks: []string{"connection", "latency"},
				LatencyThreshold: 5000,
			},
		},
	}

	watcherStore := store.NewWatcherStore(deps.db)
	var watcherIDs []uuid.UUID
	for _, p := range watcherDefs {
		w, err := watcherStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating watcher %q: %w", p.Title, err)
		}
		watcherIDs = append(watcherIDs, w.ID)
		slog.Info("seeded watcher", "title", p.Title)
	}

	// Pause one watcher for variety
	watcherStore.UpdateStatus(ctx, watcherIDs[3], store.WatcherPaused)

	// --- Adaptive states for variety ---
	// [0] Payment Error Spike → escalated (alert fired, checking every 1m)
	escalatedAt := now.Add(-12 * time.Minute)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[0], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveEscalated,
		ConsecutiveCleanRuns: 0,
		ConsecutiveErrors:    0,
		EscalatedAt:          &escalatedAt,
		TimeRange:            "1m",
		BaseTimeRange:        "15m",
	})

	// [1] Slow Query Watcher → relaxed (been quiet for a while)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[1], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveRelaxed,
		ConsecutiveCleanRuns: 25,
		ConsecutiveErrors:    0,
		TimeRange:            "2h",
		BaseTimeRange:        "30m",
	})

	// [2] Error Rate Watcher → error (paused after 5 consecutive errors)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[2], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveError,
		ConsecutiveCleanRuns: 0,
		ConsecutiveErrors:    5,
	})
	watcherStore.UpdateStatus(ctx, watcherIDs[2], store.WatcherError)

	// [4] Connection Saturation → sustained (escalation expired but still alerting)
	sustainedEscalatedAt := now.Add(-45 * time.Minute)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[4], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveSustained,
		ConsecutiveCleanRuns: 0,
		ConsecutiveErrors:    0,
		EscalatedAt:          &sustainedEscalatedAt,
		TimeRange:            "5m",
		BaseTimeRange:        "5m",
	})

	// [5] Replication Lag → backing_off (2 consecutive errors)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[5], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveBackoff,
		ConsecutiveCleanRuns: 0,
		ConsecutiveErrors:    2,
		TimeRange:            "5m",
		BaseTimeRange:        "5m",
	})

	slog.Info("seeded adaptive states")

	// --- Watcher Runs ---
	runStore := store.NewWatcherRunStore(deps.db)

	// Standard runs for first 3 watchers
	for i, wid := range watcherIDs[:3] {
		for j := 0; j < 3+i; j++ {
			run, err := runStore.Create(ctx, wid)
			if err != nil {
				continue
			}
			if rand.Float32() < 0.2 {
				runStore.Fail(ctx, run.ID, "LLM provider timeout after 30s")
			} else {
				hasAlert := rand.Float32() < 0.3
				summary := "Analysis complete. No anomalies detected."
				if hasAlert {
					summary = "Anomaly detected: elevated error rate in the last 15 minutes."
				}
				runStore.Complete(ctx, run.ID, summary, nil, hasAlert)
			}
		}
	}

	// Escalated watcher (Payment Error Spike) — recent alerting runs
	for j := 0; j < 8; j++ {
		run, err := runStore.Create(ctx, watcherIDs[0])
		if err != nil {
			continue
		}
		if j < 5 {
			runStore.Complete(ctx, run.ID, "ALERT: Payment error rate at 23/min (threshold: 5/min). Card-declined errors spiking from payment-api.", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "Payment error rate returning to normal: 4/min.", nil, false)
		}
	}

	// Error watcher (Error Rate Watcher) — 5 consecutive failures
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[2])
		if err != nil {
			continue
		}
		runStore.Fail(ctx, run.ID, "LLM provider returned 503: service temporarily unavailable")
	}

	// Sustained watcher (Connection Saturation) — long alerting history
	for j := 0; j < 12; j++ {
		run, err := runStore.Create(ctx, watcherIDs[4])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "ALERT: Active connections at 92/100 (threshold: 80). Connection pool nearing exhaustion.", nil, true)
	}

	// Backing off watcher (Replication Lag) — a few errors
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[5])
		if err != nil {
			continue
		}
		if j < 2 {
			runStore.Complete(ctx, run.ID, "Replication lag within acceptable limits: 120ms.", nil, false)
		} else {
			runStore.Fail(ctx, run.ID, "Connection to replica timed out after 10s")
		}
	}

	// Rule watcher runs
	// [6] Idle-in-Transaction — mix of clean and alert
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[6])
		if err != nil {
			continue
		}
		if j == 3 {
			runStore.Complete(ctx, run.ID, "ALERT: 5 sessions idle-in-transaction for >5 minutes (threshold: 3). Longest: 12m.", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 1 idle-in-transaction session (threshold: 3).", nil, false)
		}
	}

	// [7] Dead Tuples — always clean
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[7])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "OK: Dead tuples on orders table: 1,247 (threshold: 50,000).", nil, false)
	}

	// [8] Long-Running Queries — recent alert
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[8])
		if err != nil {
			continue
		}
		if j == 2 {
			runStore.Complete(ctx, run.ID, "ALERT: 7 queries running >30s (threshold: 5). Likely caused by missing index on orders.customer_id.", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 2 queries running >30s (threshold: 5).", nil, false)
		}
	}

	// [9] Error Log Volume — alert triggered
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[9])
		if err != nil {
			continue
		}
		if j >= 2 {
			runStore.Complete(ctx, run.ID, "ALERT: 73 ERROR logs in last 5m (threshold: 50). Top service: payment-api (41 errors).", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 12 ERROR logs in last 5m (threshold: 50).", nil, false)
		}
	}

	// [10] Payment Service Timeouts — some alerts
	for j := 0; j < 6; j++ {
		run, err := runStore.Create(ctx, watcherIDs[10])
		if err != nil {
			continue
		}
		if j >= 4 {
			runStore.Complete(ctx, run.ID, "ALERT: 14 timeout errors from payment-api in last 5m (threshold: 10).", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 2 timeout errors from payment-api in last 5m (threshold: 10).", nil, false)
		}
	}

	// [11] Gateway 503 — clean
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[11])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "OK: 3 gateway 503 errors in last 15m (threshold: 20).", nil, false)
	}

	// [12] Primary DB Health — mostly healthy, one failure
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[12])
		if err != nil {
			continue
		}
		if j == 2 {
			runStore.Complete(ctx, run.ID, "ALERT: Primary DB ping latency 3,200ms exceeds threshold 2,000ms.", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: Primary DB connection healthy. Latency: 45ms.", nil, false)
		}
	}

	// [13] Replica DB Health — clean
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[13])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "OK: Replica DB connection healthy.", nil, false)
	}

	// [14] Staging DB Connectivity — one error
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[14])
		if err != nil {
			continue
		}
		if j == 0 {
			runStore.Fail(ctx, run.ID, "Connection refused: staging DB at 10.1.3.10:5432")
		} else {
			runStore.Complete(ctx, run.ID, "OK: Staging DB connection healthy. Latency: 120ms.", nil, false)
		}
	}

	// --- Typed watcher runs ---

	// [15] Heartbeat: Payment API — mostly OK, one deadman alert
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[15])
		if err != nil {
			continue
		}
		if j == 3 {
			runStore.Complete(ctx, run.ID, "DEADMAN ALERT: Expected at least 5 events but found 0 in last 5m (payment-api INFO logs)", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: Found 12 events in last 5m (min: 5)", nil, false)
		}
	}

	// [16] Heartbeat: Order Processing — clean
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[16])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "OK: Found 3 events in last 15m (min: 1)", nil, false)
	}

	// [17] Schema Drift: Users Table — one diff alert
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[17])
		if err != nil {
			continue
		}
		if j == 3 {
			runStore.Complete(ctx, run.ID, "DIFF ALERT: Query result hash changed (mode: hash). Previous: a1b2c3..., Current: d4e5f6...", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: Query result unchanged (hash match)", nil, false)
		}
	}

	// [18] Config Table Changes — clean (baseline building)
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[18])
		if err != nil {
			continue
		}
		if j == 0 {
			runStore.Complete(ctx, run.ID, "OK: First run — baseline snapshot stored (12 rows)", nil, false)
		} else {
			runStore.Complete(ctx, run.ID, "OK: Query result unchanged (exact match)", nil, false)
		}
	}

	// [19] Payment Pipeline Health (composite) — recent alert
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[19])
		if err != nil {
			continue
		}
		if j >= 3 {
			runStore.Complete(ctx, run.ID, "COMPOSITE ALERT (all_of): All 2 conditions triggered — Payment errors high: TRUE, Gateway degraded: TRUE", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 1/2 conditions met (all_of requires all)", nil, false)
		}
	}

	// [20] Any Service Down (composite) — clean
	for j := 0; j < 3; j++ {
		run, err := runStore.Create(ctx, watcherIDs[20])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "OK: 0/2 conditions met (any_of requires at least one)", nil, false)
	}

	// [21] Error Rate Trend — building baseline then alert
	for j := 0; j < 6; j++ {
		run, err := runStore.Create(ctx, watcherIDs[21])
		if err != nil {
			continue
		}
		if j < 4 {
			snap := fmt.Sprintf(`{"value":%d}`, 10+j*2)
			runStore.Complete(ctx, run.ID, fmt.Sprintf("Building baseline: %.2f (need 5 runs, have %d)", float64(10+j*2), j+1), json.RawMessage(snap), false)
		} else if j == 4 {
			runStore.Complete(ctx, run.ID, "OK: Value 18.00 within 50% of baseline 14.00 (change: 28.6%)", json.RawMessage(`{"value":18}`), false)
		} else {
			runStore.Complete(ctx, run.ID, "TREND ALERT: Value increased by 85.7% (current: 26.00, baseline avg: 14.00, threshold: 50%)", json.RawMessage(`{"value":26}`), true)
		}
	}

	// [22] Order Volume Trend — stable
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[22])
		if err != nil {
			continue
		}
		snap := fmt.Sprintf(`{"value":%d}`, 40+j*2)
		runStore.Complete(ctx, run.ID, fmt.Sprintf("Building baseline: %.2f (need 5 runs, have %d)", float64(40+j*2), j+1), json.RawMessage(snap), false)
	}

	// [23] Cascading Failure sequence — one full match
	for j := 0; j < 4; j++ {
		run, err := runStore.Create(ctx, watcherIDs[23])
		if err != nil {
			continue
		}
		if j == 2 {
			runStore.Complete(ctx, run.ID, "SEQUENCE ALERT: All 3 steps matched in order within 1h0m0s (auth_errors → gateway_503 → order_timeouts)", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 1/3 steps matched (sequence incomplete)", nil, false)
		}
	}

	// [24] Deploy → Error Spike — two matches
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[24])
		if err != nil {
			continue
		}
		if j >= 3 {
			runStore.Complete(ctx, run.ID, "SEQUENCE ALERT: All 2 steps matched in order within 30m0s (deploy_started → error_spike)", nil, true)
		} else {
			runStore.Complete(ctx, run.ID, "OK: 0/2 steps matched (sequence incomplete)", nil, false)
		}
	}

	slog.Info("seeded watcher runs")

	// --- Alerts ---
	alertStore := store.NewAlertStore(deps.db)
	alertDefs := []store.CreateAlertParams{
		{
			WatcherID:   &watcherIDs[0],
			Title:       "Payment errors spiked to 23/min",
			Summary:     "Payment error rate jumped from ~2/min to 23/min over the last 15 minutes. Most failures are card-declined errors from the payment-api service. This correlates with a new deployment (v2.14.3) that rolled out 20 minutes ago.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"error_count":23,"baseline":2,"service":"payment-api","deployment":"v2.14.3"}`),
		},
		{
			WatcherID:   &watcherIDs[1],
			Title:       "Slow queries on order-service",
			Summary:     "3 queries exceeding 500ms threshold detected in order-service. The slowest query (850ms) hits the orders table with a missing index on customer_id.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"slow_queries":3,"max_duration_ms":850,"table":"orders"}`),
		},
		{
			WatcherID:   &watcherIDs[2],
			Title:       "TLS errors from gateway",
			Summary:     "Multiple TLS handshake failures detected from the gateway service connecting to upstream payment-gateway. The upstream certificate appears to have expired.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"error_type":"tls_handshake","upstream":"payment-gateway","count":12}`),
		},
		{
			Title:       "High memory usage on db-01.prod",
			Summary:     "Memory usage on db-01.prod has been above 85% for the last 30 minutes. Current usage: 91%. Consider scaling or investigating memory-intensive queries.",

			Severity:    store.SeverityCritical,
		},
		{
			WatcherID:   &watcherIDs[2],
			Title:       "Notification service connection refused",
			Summary:     "notification-service is experiencing SMTP connection refused errors. 15 email delivery failures in the last hour.",

			Severity:    store.SeverityWarning,
		},
		// Rule watcher alerts
		{
			WatcherID:   &watcherIDs[6],
			Title:       "Idle-in-transaction sessions detected",
			Summary:     "5 sessions idle-in-transaction for >5 minutes (threshold: 3). Longest running: 12 minutes on table pg_catalog.pg_class. Consider checking for uncommitted transactions.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"current_value":5,"threshold":3,"source":"query"}`),
		},
		{
			WatcherID:   &watcherIDs[8],
			Title:       "Long-running queries detected",
			Summary:     "7 queries running >30 seconds (threshold: 5). Most are SELECT queries hitting the orders table without using the customer_id index.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"current_value":7,"threshold":5,"source":"query"}`),
		},
		{
			WatcherID:   &watcherIDs[9],
			Title:       "Error log volume spike",
			Summary:     "73 ERROR logs in last 5 minutes (threshold: 50). Top contributor: payment-api with 41 errors, mostly connection timeouts to upstream payment gateway.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"current_value":73,"threshold":50,"source":"logs","top_service":"payment-api"}`),
		},
		{
			WatcherID:   &watcherIDs[10],
			Title:       "Payment service timeout surge",
			Summary:     "14 timeout errors from payment-api in last 5 minutes (threshold: 10). Upstream payment-gateway appears to be experiencing degraded performance.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"current_value":14,"threshold":10,"source":"logs","service":"payment-api"}`),
		},
		{
			WatcherID:   &watcherIDs[12],
			Title:       "Primary DB high latency",
			Summary:     "Primary database ping latency spiked to 3,200ms (threshold: 2,000ms). This may indicate disk I/O saturation or lock contention.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"current_value":3200,"threshold":2000,"source":"health","check":"latency"}`),
		},
	}

	// Typed watcher alerts
	typedAlertDefs := []store.CreateAlertParams{
		{
			WatcherID:   &watcherIDs[15],
			Title:       "Payment API heartbeat missing",
			Summary:     "No INFO logs from payment-api in the last 5 minutes. Expected at least 5 events but found 0. The service may be down or experiencing a complete outage.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"watcher_type":"deadman","found":0,"min_count":5,"window":"5m","service":"payment-api"}`),
		},
		{
			WatcherID:   &watcherIDs[17],
			Title:       "Users table schema changed",
			Summary:     "Schema drift detected on the users table. Column structure hash changed between runs. A new column may have been added or a type modified. Review recent migrations.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"watcher_type":"diff","compare_mode":"hash","previous_hash":"a1b2c3d4","current_hash":"d4e5f6a7"}`),
		},
		{
			WatcherID:   &watcherIDs[19],
			Title:       "Payment pipeline degraded",
			Summary:     "All composite conditions triggered simultaneously: payment-api errors above 10/5m AND gateway 503 errors above 5/5m. This indicates a correlated failure across the payment pipeline.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"watcher_type":"composite","logic":"all_of","conditions_met":2,"conditions_total":2}`),
		},
		{
			WatcherID:   &watcherIDs[21],
			Title:       "Error rate increased 85%",
			Summary:     "Error log volume increased by 85.7% compared to the rolling baseline (current: 26, baseline avg: 14). This exceeds the 50% threshold. Investigate recent deployments or upstream changes.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"watcher_type":"trend","current_value":26,"baseline_avg":14,"change_percent":85.7,"direction":"increase"}`),
		},
		{
			WatcherID:   &watcherIDs[23],
			Title:       "Cascading failure detected: auth → gateway → orders",
			Summary:     "All 3 steps of the cascading failure sequence matched within the 1-hour window: auth errors appeared first, followed by gateway 503s, then order service timeouts. This pattern indicates a propagating failure originating from the auth layer.",

			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"watcher_type":"sequence","steps_matched":3,"steps_total":3,"window":"1h"}`),
		},
		{
			WatcherID:   &watcherIDs[24],
			Title:       "Deploy triggered error spike",
			Summary:     "Deployment event detected followed by a spike in ERROR logs within 30 minutes. The deployment at v2.14.3 appears to have introduced a regression causing elevated errors.",

			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"watcher_type":"sequence","steps_matched":2,"steps_total":2,"window":"30m","deployment":"v2.14.3"}`),
		},
	}
	alertDefs = append(alertDefs, typedAlertDefs...)

	for _, p := range alertDefs {
		a, err := alertStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating alert %q: %w", p.Title, err)
		}
		slog.Info("seeded alert", "title", p.Title)
		// Mark some as read for variety
		if rand.Float32() < 0.3 {
			alertStore.MarkRead(ctx, a.ID)
		}
	}

	// --- Data Sources (connectors) ---
	dsStore := store.NewDataSourceStore(deps.db)
	dsDefs := []store.CreateDataSourceParams{
		{Type: store.ConnectorLogs, Name: "Production Logs", Config: map[string]any{}},
		{Type: store.ConnectorLogs, Name: "Staging Logs", Config: map[string]any{}},
	}
	for _, p := range dsDefs {
		ds, err := dsStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating data source %q: %w", p.Name, err)
		}
		// Mark as connected
		status := store.StatusConnected
		dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{Status: &status})
		slog.Info("seeded connector", "name", p.Name)
	}

	slog.Info("seed complete")
	return nil
}
