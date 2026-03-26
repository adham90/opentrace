package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"

	dbstore "github.com/adham90/opentrace/internal/db"
	"github.com/adham90/opentrace/pkg/store"
)

func runSeed() error {
	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()

	slog.Info("seeding database")

	// --- Servers ---
	servers := []store.RegisterServerParams{
		{Hostname: "web-01.prod", IPAddress: "10.0.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "web-02.prod", IPAddress: "10.0.1.11", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "api-01.prod", IPAddress: "10.0.2.10", OS: "linux", Arch: "arm64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "api"}},
		{Hostname: "db-01.prod", IPAddress: "10.0.3.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.0", Labels: map[string]string{"env": "production", "role": "database"}},
		{Hostname: "worker-01.staging", IPAddress: "10.1.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "staging", "role": "worker"}},
	}

	serverStore := dbstore.NewServerStore(deps.DB)
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
	metricStore := dbstore.NewMetricStore(deps.DB)
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

	// Error fingerprints and exception classes for realistic error grouping
	errorDetails := []struct {
		exceptionClass   string
		errorFingerprint string
		message          string
	}{
		{"CardDeclinedException", "fp-card-declined-001", "Failed to process payment: card declined (4111****1234)"},
		{"ConnectionTimeoutError", "fp-db-timeout-002", "Database connection timeout after 30s"},
		{"NullPointerException", "fp-npe-orders-003", "Unhandled exception in /api/v1/orders: NullPointerException"},
		{"ServiceUnavailableError", "fp-upstream-503-004", "External API returned 503: service unavailable"},
		{"SMTPConnectionError", "fp-smtp-refused-005", "Failed to send notification email: SMTP connection refused"},
		{"RateLimitExceeded", "fp-rate-limit-006", "Rate limit exceeded for client app-web-dashboard"},
		{"TLSHandshakeError", "fp-tls-expired-007", "TLS handshake failed with upstream: certificate expired"},
	}

	var logEntries []store.LogEntry
	now := time.Now()
	for i := 0; i < 2000; i++ {
		level := levels[rand.Intn(len(levels))]
		svc := services[rand.Intn(len(services))]
		msgs := messages[level]
		msg := msgs[rand.Intn(len(msgs))]
		ts := now.Add(-time.Duration(rand.Intn(86400)) * time.Second) // last 24 hours

		entry := store.LogEntry{
			Timestamp: ts,
			Level:     level,
			Service:   svc,
			Message:   msg,
		}
		if rand.Float32() < 0.3 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		// Add error details to ERROR entries
		if level == "ERROR" {
			ed := errorDetails[rand.Intn(len(errorDetails))]
			entry.ExceptionClass = ed.exceptionClass
			entry.ErrorFingerprint = ed.errorFingerprint
			entry.Message = ed.message
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
		ts := now.Add(-time.Duration(rand.Intn(86400)) * time.Second)
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

	logStore := dbstore.NewLogStore(deps.DB)
	n, err := logStore.BatchInsert(ctx, logEntries)
	if err != nil {
		return fmt.Errorf("inserting logs: %w", err)
	}
	slog.Info("seeded logs", "count", n)

	// --- Error Groups (from error log entries) ---
	errorGroupStore := dbstore.NewErrorGroupStore(deps.DB)
	errorGroupCount := 0
	for _, entry := range logEntries {
		if entry.ErrorFingerprint != "" {
			if err := errorGroupStore.Upsert(ctx, entry); err != nil {
				slog.Warn("failed to upsert error group", "fingerprint", entry.ErrorFingerprint, "error", err)
			} else {
				errorGroupCount++
			}
		}
	}
	slog.Info("seeded error groups", "upserted", errorGroupCount)

	// --- Watches (new agent-first system) ---
	watchStore := dbstore.NewWatchStore(deps.DB)

	watchDefs := []store.CreateWatchParams{
		{
			Metric:         store.WatchMetricErrorRate,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      0.05,
			Service:        "payment-api",
			Duration:       "2h",
			Urgency:        store.WatchUrgencyCritical,
			CheckInterval:  "1m",
			BaselineWindow: "1h",
			MinConsecutive: 3,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-001",
		},
		{
			Metric:         store.WatchMetricResponseTime,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      500,
			Service:        "gateway",
			Duration:       "4h",
			Urgency:        store.WatchUrgencyHigh,
			CheckInterval:  "2m",
			BaselineWindow: "30m",
			MinConsecutive: 2,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-001",
		},
		{
			Metric:         store.WatchMetricP95Response,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      1200,
			Service:        "order-service",
			Endpoint:       "/api/v1/orders",
			Duration:       "1h",
			Urgency:        store.WatchUrgencyNormal,
			CheckInterval:  "5m",
			BaselineWindow: "1h",
			MinConsecutive: 2,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-002",
		},
		{
			Metric:         store.WatchMetricErrorCount,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      50,
			Service:        "notification-service",
			Duration:       "6h",
			Urgency:        store.WatchUrgencyNormal,
			CheckInterval:  "5m",
			BaselineWindow: "1h",
			MinConsecutive: 1,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-002",
		},
		{
			Metric:         store.WatchMetricHeartbeat,
			Operator:       store.WatchOpLessThan,
			Threshold:      1,
			Service:        "payment-api",
			Duration:       "8h",
			Urgency:        store.WatchUrgencyCritical,
			CheckInterval:  "1m",
			BaselineWindow: "5m",
			MinConsecutive: 3,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-001",
		},
		{
			Metric:         store.WatchMetricLogCount,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      1000,
			Service:        "user-service",
			Environment:    "production",
			Duration:       "3h",
			Urgency:        store.WatchUrgencyLow,
			CheckInterval:  "10m",
			BaselineWindow: "1h",
			MinConsecutive: 2,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-003",
		},
		{
			Metric:         store.WatchMetricErrorRate,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      0.10,
			Service:        "gateway",
			CommitHash:     "a1b2c3d",
			Duration:       "30m",
			Urgency:        store.WatchUrgencyHigh,
			CheckInterval:  "30s",
			BaselineWindow: "15m",
			MinConsecutive: 2,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-003",
		},
		{
			Metric:         store.WatchMetricResponseTime,
			Operator:       store.WatchOpGreaterThan,
			Threshold:      800,
			Service:        "payment-api",
			Endpoint:       "/api/v1/checkout",
			Duration:       "2h",
			Urgency:        store.WatchUrgencyCritical,
			CheckInterval:  "1m",
			BaselineWindow: "30m",
			MinConsecutive: 3,
			CreatedBy:      "claude-code",
			SessionID:      "seed-session-001",
		},
	}

	var watchIDs []string
	for _, p := range watchDefs {
		w, err := watchStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating watch (metric=%s, service=%s): %w", p.Metric, p.Service, err)
		}
		watchIDs = append(watchIDs, w.ID)
		slog.Info("seeded watch", "metric", p.Metric, "service", p.Service, "id", w.ID)
	}

	// Set some watches to different statuses for variety
	// [2] triggered — p95 breached
	watchStore.UpdateStatus(ctx, watchIDs[2], store.WatchStatusTriggered)
	watchStore.UpdateAfterCheck(ctx, watchIDs[2], 1450, 3, now.Add(5*time.Minute))

	// [6] expired — short-lived deploy watch
	watchStore.UpdateStatus(ctx, watchIDs[6], store.WatchStatusExpired)

	// [4] triggered — heartbeat missing
	watchStore.UpdateStatus(ctx, watchIDs[4], store.WatchStatusTriggered)
	watchStore.UpdateAfterCheck(ctx, watchIDs[4], 0, 4, now.Add(time.Minute))

	slog.Info("seeded watch statuses")

	// --- Watch Runs ---
	type runDef struct {
		idx     int
		value   float64
		breach  bool
		summary string
		fail    string
	}
	runs := []runDef{
		// [0] payment-api error_rate — mostly clean, recent breach
		{0, 0.02, false, "error_rate=0.02 (threshold 0.05): OK", ""},
		{0, 0.03, false, "error_rate=0.03 (threshold 0.05): OK", ""},
		{0, 0.04, false, "error_rate=0.04 (threshold 0.05): OK", ""},
		{0, 0.08, true, "error_rate=0.08 (threshold 0.05): BREACH", ""},
		{0, 0.06, true, "error_rate=0.06 (threshold 0.05): BREACH", ""},
		// [1] gateway response_time — clean
		{1, 180, false, "response_time=180ms (threshold 500ms): OK", ""},
		{1, 220, false, "response_time=220ms (threshold 500ms): OK", ""},
		{1, 310, false, "response_time=310ms (threshold 500ms): OK", ""},
		// [2] order-service p95 — triggered
		{2, 800, false, "p95_response=800ms (threshold 1200ms): OK", ""},
		{2, 1100, false, "p95_response=1100ms (threshold 1200ms): OK", ""},
		{2, 1350, true, "p95_response=1350ms (threshold 1200ms): BREACH", ""},
		{2, 1450, true, "p95_response=1450ms (threshold 1200ms): BREACH", ""},
		{2, 1500, true, "p95_response=1500ms (threshold 1200ms): BREACH", ""},
		// [3] notification error_count — clean
		{3, 12, false, "error_count=12 (threshold 50): OK", ""},
		{3, 8, false, "error_count=8 (threshold 50): OK", ""},
		// [4] payment-api heartbeat — triggered
		{4, 5, false, "heartbeat: 5 events in window (threshold 1): OK", ""},
		{4, 3, false, "heartbeat: 3 events in window (threshold 1): OK", ""},
		{4, 0, true, "heartbeat: 0 events in window (threshold 1): BREACH", ""},
		{4, 0, true, "heartbeat: 0 events in window (threshold 1): BREACH", ""},
		{4, 0, true, "heartbeat: 0 events in window (threshold 1): BREACH", ""},
		// [5] user-service log_count — one failure
		{5, 450, false, "log_count=450 (threshold 1000): OK", ""},
		{5, 0, false, "", "failed to query log store: context deadline exceeded"},
		{5, 520, false, "log_count=520 (threshold 1000): OK", ""},
		// [7] payment-api checkout response_time — escalating
		{7, 350, false, "response_time=350ms (threshold 800ms): OK", ""},
		{7, 600, false, "response_time=600ms (threshold 800ms): OK", ""},
		{7, 850, true, "response_time=850ms (threshold 800ms): BREACH", ""},
		{7, 920, true, "response_time=920ms (threshold 800ms): BREACH", ""},
	}

	for _, r := range runs {
		run, err := watchStore.CreateRun(ctx, watchIDs[r.idx])
		if err != nil {
			continue
		}
		if r.fail != "" {
			watchStore.FailRun(ctx, run.ID, r.fail)
		} else {
			watchStore.CompleteRun(ctx, run.ID, r.value, r.breach, r.summary)
		}
	}
	slog.Info("seeded watch runs", "count", len(runs))

	// --- Watch Alerts ---
	watchAlertDefs := []store.CreateWatchAlertParams{
		{
			WatchID:        watchIDs[2],
			Urgency:        store.WatchUrgencyNormal,
			Summary:        "p95 response time for order-service /api/v1/orders has exceeded 1200ms for 3 consecutive checks. Current value: 1500ms. Likely caused by missing index on orders.customer_id — recent EXPLAIN shows sequential scan.",
			TriggerMetric:  "p95_response",
			TriggerValue:   1500,
			ThresholdValue: 1200,
		},
		{
			WatchID:        watchIDs[4],
			Urgency:        store.WatchUrgencyCritical,
			Summary:        "payment-api heartbeat missing. Zero events detected in the last 5 minutes across 4 consecutive checks. The service may be down or experiencing a complete outage. Last known activity was 22 minutes ago.",
			TriggerMetric:  "heartbeat",
			TriggerValue:   0,
			ThresholdValue: 1,
		},
		{
			WatchID:        watchIDs[0],
			Urgency:        store.WatchUrgencyCritical,
			Summary:        "Error rate for payment-api spiked to 8% (threshold 5%). 2 consecutive breaches detected. Top errors: CardDeclinedException (62%), TimeoutException (28%). Correlates with deployment a1b2c3d rolled out 18 minutes ago.",
			TriggerMetric:  "error_rate",
			TriggerValue:   0.08,
			ThresholdValue: 0.05,
		},
		{
			WatchID:        watchIDs[7],
			Urgency:        store.WatchUrgencyHigh,
			Summary:        "Checkout endpoint response time at 920ms (threshold 800ms). 2 consecutive breaches. Slow queries on payment_transactions table detected. Consider checking database connection pool saturation.",
			TriggerMetric:  "response_time",
			TriggerValue:   920,
			ThresholdValue: 800,
		},
	}

	for _, p := range watchAlertDefs {
		a, err := watchStore.CreateAlert(ctx, p)
		if err != nil {
			return fmt.Errorf("creating watch alert: %w", err)
		}
		slog.Info("seeded watch alert", "id", a.ID, "urgency", p.Urgency)
	}

	// Acknowledge one alert for variety
	alerts, _ := watchStore.ListAlerts(ctx, watchIDs[0], "", 10)
	if len(alerts) > 0 {
		watchStore.AcknowledgeAlert(ctx, alerts[0].ID)
	}

	slog.Info("seeded watch alerts")

	// --- Data Sources (connectors) ---
	dsStore := dbstore.NewDataSourceStore(deps.DB)
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
