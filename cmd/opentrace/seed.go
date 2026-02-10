package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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

	log.Println("seeding database...")

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
		log.Printf("  server: %s (%s)", p.Hostname, s.ID)
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
	log.Printf("  metrics: %d servers x 30 snapshots", len(serverIDs))

	// --- Logs ---
	services := []string{"payment-api", "user-service", "gateway", "notification-service", "order-service"}
	levels := []string{"DEBUG", "INFO", "INFO", "INFO", "WARN", "ERROR"} // weighted toward INFO
	envs := []string{"production", "staging"}

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
		env := envs[rand.Intn(len(envs))]
		msgs := messages[level]
		msg := msgs[rand.Intn(len(msgs))]
		ts := now.Add(-time.Duration(rand.Intn(7200)) * time.Second) // last 2 hours

		entry := store.LogEntry{
			Timestamp:   ts,
			Level:       level,
			Service:     svc,
			Message:     msg,
			Environment: env,
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
	log.Printf("  logs: %d entries", n)

	// --- Watchers ---
	watcherDefs := []store.CreateWatcherParams{
		{
			Title:       "Payment Error Spike",
			Description: "Alert when payment errors exceed normal levels",
			Environment: "production",
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
			Title:       "Slow Query Monitor",
			Description: "Detect slow database queries across all services",
			Environment: "production",
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
			Title:       "Error Rate Monitor",
			Description: "Watch for unusual error patterns in all services",
			Environment: "",
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
			Description: "Monitor staging environment for issues before prod deploy",
			Environment: "staging",
			Severity:    store.SeverityInfo,
			Filters:     json.RawMessage(`{}`),
			TimeRange:   "1h",
			Effort:      store.EffortLow,
			Notify:      json.RawMessage(`["dashboard"]`),
		},
		{
			Title:       "Connection Saturation",
			Description: "Monitor PostgreSQL connection pool usage and alert on saturation",
			Environment: "production",
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
			Environment: "production",
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
	}

	watcherStore := store.NewWatcherStore(deps.db)
	var watcherIDs []uuid.UUID
	for _, p := range watcherDefs {
		w, err := watcherStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating watcher %q: %w", p.Title, err)
		}
		watcherIDs = append(watcherIDs, w.ID)
		log.Printf("  watcher: %s", p.Title)
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

	// [1] Slow Query Monitor → relaxed (been quiet for a while)
	watcherStore.UpdateAdaptiveState(ctx, watcherIDs[1], store.UpdateAdaptiveParams{
		AdaptiveState:        store.AdaptiveRelaxed,
		ConsecutiveCleanRuns: 25,
		ConsecutiveErrors:    0,
		TimeRange:            "2h",
		BaseTimeRange:        "30m",
	})

	// [2] Error Rate Monitor → error (paused after 5 consecutive errors)
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

	log.Println("  adaptive states: escalated, relaxed, error, sustained, backing_off")

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

	// Escalated monitor (Payment Error Spike) — recent alerting runs
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

	// Error monitor (Error Rate Monitor) — 5 consecutive failures
	for j := 0; j < 5; j++ {
		run, err := runStore.Create(ctx, watcherIDs[2])
		if err != nil {
			continue
		}
		runStore.Fail(ctx, run.ID, "LLM provider returned 503: service temporarily unavailable")
	}

	// Sustained monitor (Connection Saturation) — long alerting history
	for j := 0; j < 12; j++ {
		run, err := runStore.Create(ctx, watcherIDs[4])
		if err != nil {
			continue
		}
		runStore.Complete(ctx, run.ID, "ALERT: Active connections at 92/100 (threshold: 80). Connection pool nearing exhaustion.", nil, true)
	}

	// Backing off monitor (Replication Lag) — a few errors
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

	log.Println("  watcher runs: created (including adaptive-state scenarios)")

	// --- Alerts ---
	alertStore := store.NewAlertStore(deps.db)
	alertDefs := []store.CreateAlertParams{
		{
			WatcherID:   &watcherIDs[0],
			Title:       "Payment errors spiked to 23/min",
			Summary:     "Payment error rate jumped from ~2/min to 23/min over the last 15 minutes. Most failures are card-declined errors from the payment-api service. This correlates with a new deployment (v2.14.3) that rolled out 20 minutes ago.",
			Environment: "production",
			Severity:    store.SeverityCritical,
			Details:     json.RawMessage(`{"error_count":23,"baseline":2,"service":"payment-api","deployment":"v2.14.3"}`),
		},
		{
			WatcherID:   &watcherIDs[1],
			Title:       "Slow queries on order-service",
			Summary:     "3 queries exceeding 500ms threshold detected in order-service. The slowest query (850ms) hits the orders table with a missing index on customer_id.",
			Environment: "production",
			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"slow_queries":3,"max_duration_ms":850,"table":"orders"}`),
		},
		{
			WatcherID:   &watcherIDs[2],
			Title:       "TLS errors from gateway",
			Summary:     "Multiple TLS handshake failures detected from the gateway service connecting to upstream payment-gateway. The upstream certificate appears to have expired.",
			Environment: "production",
			Severity:    store.SeverityWarning,
			Details:     json.RawMessage(`{"error_type":"tls_handshake","upstream":"payment-gateway","count":12}`),
		},
		{
			Title:       "High memory usage on db-01.prod",
			Summary:     "Memory usage on db-01.prod has been above 85% for the last 30 minutes. Current usage: 91%. Consider scaling or investigating memory-intensive queries.",
			Environment: "production",
			Severity:    store.SeverityCritical,
		},
		{
			WatcherID:   &watcherIDs[2],
			Title:       "Notification service connection refused",
			Summary:     "notification-service is experiencing SMTP connection refused errors. 15 email delivery failures in the last hour.",
			Environment: "production",
			Severity:    store.SeverityWarning,
		},
	}

	for _, p := range alertDefs {
		a, err := alertStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating alert %q: %w", p.Title, err)
		}
		log.Printf("  alert: %s", p.Title)
		// Mark some as read for variety
		if rand.Float32() < 0.3 {
			alertStore.MarkRead(ctx, a.ID)
		}
	}

	// --- Data Sources (connectors) ---
	dsStore := store.NewDataSourceStore(deps.db)
	dsDefs := []store.CreateDataSourceParams{
		{Type: store.ConnectorLogs, Name: "Production Logs", Environment: "production", Config: map[string]any{}},
		{Type: store.ConnectorLogs, Name: "Staging Logs", Environment: "staging", Config: map[string]any{}},
	}
	for _, p := range dsDefs {
		ds, err := dsStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating data source %q: %w", p.Name, err)
		}
		// Mark as connected
		status := store.StatusConnected
		dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{Status: &status})
		log.Printf("  connector: %s", p.Name)
	}

	// --- Digests (historical health summaries) ---
	digestStore := store.NewDigestStore(deps.db)
	type digestSeed struct {
		daysAgo       int
		status        string
		alertTotal    int
		alertCritical int
		alertWarning  int
		monitorTotal  int
		monitorErr    int
		failedRuns    int
	}
	digestSeeds := []digestSeed{
		{daysAgo: 6, status: "healthy", alertTotal: 1, alertCritical: 0, alertWarning: 1, monitorTotal: 4, monitorErr: 0, failedRuns: 0},
		{daysAgo: 5, status: "healthy", alertTotal: 0, alertCritical: 0, alertWarning: 0, monitorTotal: 4, monitorErr: 0, failedRuns: 0},
		{daysAgo: 4, status: "needs_attention", alertTotal: 3, alertCritical: 0, alertWarning: 3, monitorTotal: 4, monitorErr: 0, failedRuns: 1},
		{daysAgo: 3, status: "critical", alertTotal: 7, alertCritical: 3, alertWarning: 4, monitorTotal: 4, monitorErr: 1, failedRuns: 2},
		{daysAgo: 2, status: "needs_attention", alertTotal: 4, alertCritical: 1, alertWarning: 3, monitorTotal: 4, monitorErr: 1, failedRuns: 1},
		{daysAgo: 1, status: "healthy", alertTotal: 2, alertCritical: 0, alertWarning: 2, monitorTotal: 4, monitorErr: 0, failedRuns: 0},
		{daysAgo: 0, status: "critical", alertTotal: 5, alertCritical: 2, alertWarning: 3, monitorTotal: 4, monitorErr: 0, failedRuns: 1},
	}

	for _, ds := range digestSeeds {
		periodEnd := now.AddDate(0, 0, -ds.daysAgo)
		periodStart := periodEnd.Add(-24 * time.Hour)
		data := fmt.Sprintf(`{"status":"%s","alert_summary":{"total":%d,"critical":%d,"warning":%d},"monitor_summary":{"total":%d,"in_error":%d,"failed_runs":%d}}`,
			ds.status, ds.alertTotal, ds.alertCritical, ds.alertWarning, ds.monitorTotal, ds.monitorErr, ds.failedRuns)
		if err := digestStore.Save(ctx, store.SaveDigestParams{
			ID:             uuid.New().String(),
			Environment:    "",
			Status:         ds.status,
			PeriodStart:    periodStart,
			PeriodEnd:      periodEnd,
			AlertTotal:     ds.alertTotal,
			AlertCritical:  ds.alertCritical,
			AlertWarning:   ds.alertWarning,
			MonitorTotal:   ds.monitorTotal,
			MonitorErrored: ds.monitorErr,
			FailedRuns:     ds.failedRuns,
			Data:           data,
		}); err != nil {
			return fmt.Errorf("creating digest: %w", err)
		}
	}
	log.Printf("  digests: %d daily summaries", len(digestSeeds))

	log.Println("seed complete!")
	return nil
}
