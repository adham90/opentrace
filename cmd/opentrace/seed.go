package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/pkg/store"
)

func runSeed() error {
	ctx := context.Background()
	deps, err := initApp(ctx)
	if err != nil {
		return err
	}
	defer deps.DB.Close()

	slog.Info("seeding database (idempotent — clears old seed data)")

	// Clear old seed data for idempotency.
	tables := []string{"watch_alerts", "watch_runs", "watches", "request_summaries",
		"error_group_events", "error_groups", "deploys", "logs", "data_sources",
		"metric_points", "servers"}
	for _, t := range tables {
		deps.DB.ExecContext(ctx, "DELETE FROM "+t)
	}
	slog.Info("cleared old seed data")

	// --- Servers ---
	servers := []store.RegisterServerParams{
		{Hostname: "web-01.prod", IPAddress: "10.0.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "web-02.prod", IPAddress: "10.0.1.11", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "web"}},
		{Hostname: "api-01.prod", IPAddress: "10.0.2.10", OS: "linux", Arch: "arm64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "production", "role": "api"}},
		{Hostname: "db-01.prod", IPAddress: "10.0.3.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.0", Labels: map[string]string{"env": "production", "role": "database"}},
		{Hostname: "worker-01.staging", IPAddress: "10.1.1.10", OS: "linux", Arch: "amd64", AgentVersion: "0.3.1", Labels: map[string]string{"env": "staging", "role": "worker"}},
	}

	serverStore := deps.ServerStore
	var serverIDs []uuid.UUID
	for _, p := range servers {
		s, err := serverStore.Register(ctx, p)
		if err != nil {
			return fmt.Errorf("registering server %s: %w", p.Hostname, err)
		}
		serverIDs = append(serverIDs, s.ID)
	}
	slog.Info("seeded servers", "count", len(serverIDs))

	// --- Metrics for each server (60 points over 2 hours) ---
	metricStore := deps.MetricStore
	for i, sid := range serverIDs {
		for m := 0; m < 60; m++ {
			ts := time.Now().Add(-time.Duration(60-m) * 2 * time.Minute)
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
	slog.Info("seeded metrics", "servers", len(serverIDs))

	// --- Logs (spread over 7 days for time range testing) ---
	services := []string{"payment-api", "user-service", "gateway", "notification-service", "order-service"}
	environments := []string{"production", "production", "production", "staging"} // weighted to production
	levels := []string{"DEBUG", "INFO", "INFO", "INFO", "INFO", "WARN", "ERROR"}

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

	// Error details for realistic error grouping.
	errorDetails := []struct {
		exceptionClass   string
		errorFingerprint string
		message          string
		sourceFile       string
		sourceLine       int
	}{
		{"CardDeclinedException", "fp-card-declined-001", "Failed to process payment: card declined (4111****1234)", "app/services/payment_processor.rb", 142},
		{"ConnectionTimeoutError", "fp-db-timeout-002", "Database connection timeout after 30s", "app/models/order.rb", 67},
		{"NullPointerException", "fp-npe-orders-003", "Unhandled exception in /api/v1/orders: NullPointerException", "app/controllers/orders_controller.rb", 28},
		{"ServiceUnavailableError", "fp-upstream-503-004", "External API returned 503: service unavailable", "app/services/notification_client.rb", 95},
		{"SMTPConnectionError", "fp-smtp-refused-005", "Failed to send notification email: SMTP connection refused", "app/mailers/order_mailer.rb", 33},
		{"RateLimitExceeded", "fp-rate-limit-006", "Rate limit exceeded for client app-web-dashboard", "app/middleware/rate_limiter.rb", 51},
		{"TLSHandshakeError", "fp-tls-expired-007", "TLS handshake failed with upstream: certificate expired", "lib/http_client.rb", 88},
	}

	// NEW errors — first seen recently (for "what's new" testing).
	newErrorDetails := []struct {
		exceptionClass   string
		errorFingerprint string
		message          string
		sourceFile       string
		sourceLine       int
	}{
		{"MemoryQuotaExceeded", "fp-mem-quota-008", "Worker process exceeded memory quota (512MB limit)", "app/jobs/report_generator.rb", 112},
		{"DeadlockDetected", "fp-deadlock-009", "Database deadlock detected on table 'inventory'", "app/models/inventory.rb", 45},
		{"InvalidJWTSignature", "fp-jwt-invalid-010", "JWT signature verification failed for request to /api/v1/admin", "app/middleware/auth.rb", 72},
	}

	now := time.Now()
	var logEntries []store.LogEntry

	// Generate 5000 logs spread over 7 days.
	for i := 0; i < 5000; i++ {
		level := levels[rand.Intn(len(levels))]
		svc := services[rand.Intn(len(services))]
		env := environments[rand.Intn(len(environments))]
		msgs := messages[level]
		msg := msgs[rand.Intn(len(msgs))]
		// Spread over 7 days with more weight on recent.
		hoursAgo := rand.ExpFloat64() * 24 // exponential: most logs recent
		if hoursAgo > 168 {
			hoursAgo = 168 // cap at 7 days
		}
		ts := now.Add(-time.Duration(hoursAgo * float64(time.Hour)))

		entry := store.LogEntry{
			Timestamp:   ts,
			Level:       level,
			Service:     svc,
			Message:     msg,
			Environment: env,
		}
		if rand.Float32() < 0.4 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		if rand.Float32() < 0.2 {
			entry.RequestID = fmt.Sprintf("req-%s", uuid.New().String()[:8])
		}

		// Add error details.
		if level == "ERROR" {
			ed := errorDetails[rand.Intn(len(errorDetails))]
			entry.ExceptionClass = ed.exceptionClass
			entry.ErrorFingerprint = ed.errorFingerprint
			entry.Message = ed.message
			entry.SourceFile = ed.sourceFile
			entry.SourceLine = ed.sourceLine
		}
		logEntries = append(logEntries, entry)
	}

	// Add "new" errors (first seen in last 2 hours).
	for i := 0; i < 30; i++ {
		ned := newErrorDetails[rand.Intn(len(newErrorDetails))]
		svc := services[rand.Intn(len(services))]
		ts := now.Add(-time.Duration(rand.Intn(120)) * time.Minute) // last 2 hours

		entry := store.LogEntry{
			Timestamp:        ts,
			Level:            "ERROR",
			Service:          svc,
			Message:          ned.message,
			ExceptionClass:   ned.exceptionClass,
			ErrorFingerprint: ned.errorFingerprint,
			SourceFile:       ned.sourceFile,
			SourceLine:       ned.sourceLine,
			Environment:      "production",
		}
		if rand.Float32() < 0.5 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		logEntries = append(logEntries, entry)
	}

	// Business event logs.
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
			Timestamp:   ts,
			Level:       "INFO",
			Service:     ev.service,
			Message:     ev.message,
			EventType:   ev.eventType,
			Environment: "production",
		}
		if rand.Float32() < 0.3 {
			entry.TraceID = fmt.Sprintf("trace-%s", uuid.New().String()[:8])
		}
		logEntries = append(logEntries, entry)
	}

	logStore := deps.LogStore
	n, err := logStore.BatchInsert(ctx, logEntries)
	if err != nil {
		return fmt.Errorf("inserting logs: %w", err)
	}
	slog.Info("seeded logs", "count", n)

	// --- Request Summaries (for analytics) ---
	controllers := []struct {
		controller string
		action     string
		method     string
		path       string
		service    string
	}{
		{"OrdersController", "index", "GET", "/api/v1/orders", "order-service"},
		{"OrdersController", "create", "POST", "/api/v1/orders", "order-service"},
		{"OrdersController", "show", "GET", "/api/v1/orders/:id", "order-service"},
		{"PaymentsController", "charge", "POST", "/api/v1/payments/charge", "payment-api"},
		{"PaymentsController", "refund", "POST", "/api/v1/payments/refund", "payment-api"},
		{"UsersController", "login", "POST", "/api/v1/auth/login", "user-service"},
		{"UsersController", "profile", "GET", "/api/v1/users/me", "user-service"},
		{"NotificationsController", "send", "POST", "/api/v1/notifications", "notification-service"},
		{"HealthController", "check", "GET", "/health", "gateway"},
		{"ReportsController", "summary", "GET", "/api/v1/reports/summary", "gateway"},
	}

	// Pick INFO log IDs to link request_summaries to.
	var infoLogIDs []int64
	rows, err := deps.DB.QueryContext(ctx, "SELECT id FROM logs WHERE level = 'INFO' ORDER BY RANDOM() LIMIT 500")
	if err == nil {
		for rows.Next() {
			var id int64
			if rows.Scan(&id) == nil {
				infoLogIDs = append(infoLogIDs, id)
			}
		}
		rows.Close()
	}

	reqCount := 0
	for i, logID := range infoLogIDs {
		c := controllers[i%len(controllers)]
		duration := 20 + rand.Float64()*300 // 20-320ms
		sqlCount := rand.Intn(8)
		status := 200
		if rand.Float32() < 0.05 {
			status = 500
		} else if rand.Float32() < 0.08 {
			status = 404
		} else if rand.Float32() < 0.03 {
			status = 422
		}
		_, err := deps.DB.ExecContext(ctx,
			`INSERT INTO request_summaries (log_id, controller, action, method, path, status, duration_ms, sql_count, sql_total_ms)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			logID, c.controller, c.action, c.method, c.path, status, duration, sqlCount, float64(sqlCount)*5.5,
		)
		if err == nil {
			reqCount++
		}
	}
	slog.Info("seeded request summaries", "count", reqCount)

	// --- Error Groups (from error log entries) ---
	errorGroupStore := deps.ErrorGroupStore
	errorGroupCount := 0
	for _, entry := range logEntries {
		if entry.ErrorFingerprint != "" {
			if err := errorGroupStore.Upsert(ctx, entry); err != nil {
				// Ignore dups.
			} else {
				errorGroupCount++
			}
		}
	}
	slog.Info("seeded error groups", "upserted", errorGroupCount)

	// --- Deploys (for deploy history/impact testing) ---
	deployDefs := []struct {
		service     string
		env         string
		commit      string
		branch      string
		author      string
		daysAgo     int
		status      store.DeployStatus
		preErr      *float64
		postErr     *float64
		files       []string
	}{
		{"gateway", "production", "a1b2c3d", "main", "alice@example.com", 0, store.DeployStatusMeasured, ptr(0.02), ptr(0.05), []string{"lib/http_client.rb", "config/tls.yml"}},
		{"payment-api", "production", "e4f5g6h", "main", "bob@example.com", 1, store.DeployStatusMeasured, ptr(0.01), ptr(0.01), []string{"app/services/payment_processor.rb"}},
		{"order-service", "production", "i7j8k9l", "release/v2.15", "charlie@example.com", 2, store.DeployStatusMeasured, ptr(0.03), ptr(0.04), []string{"app/controllers/orders_controller.rb", "app/models/order.rb"}},
		{"user-service", "production", "m0n1o2p", "main", "alice@example.com", 3, store.DeployStatusMeasured, ptr(0.005), ptr(0.005), []string{"app/controllers/users_controller.rb"}},
		{"notification-service", "production", "q3r4s5t", "main", "bob@example.com", 5, store.DeployStatusMeasured, ptr(0.01), ptr(0.08), []string{"app/services/notification_client.rb", "app/mailers/order_mailer.rb"}},
		{"gateway", "staging", "u6v7w8x", "feature/new-auth", "charlie@example.com", 0, store.DeployStatusPending, nil, nil, []string{"app/middleware/auth.rb"}},
	}

	for _, d := range deployDefs {
		deployedAt := now.Add(-time.Duration(d.daysAgo) * 24 * time.Hour)
		_, err := deps.DB.ExecContext(ctx,
			`INSERT INTO deploys (service, environment, commit_hash, branch, author, files_changed_json, status, pre_error_rate, post_error_rate, deployed_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			d.service, d.env, d.commit, d.branch, d.author, "[]", string(d.status), d.preErr, d.postErr, deployedAt.Format(time.RFC3339),
		)
		if err != nil {
			slog.Warn("failed to seed deploy", "service", d.service, "error", err)
		} else {
			slog.Info("seeded deploy", "service", d.service, "commit", d.commit, "days_ago", d.daysAgo)
		}
	}

	// --- Watches ---
	watchStore := deps.WatchStore

	watchDefs := []store.CreateWatchParams{
		{Metric: store.WatchMetricErrorRate, Operator: store.WatchOpGreaterThan, Threshold: 0.05, Service: "payment-api", Duration: "2h", Urgency: store.WatchUrgencyCritical, CheckInterval: "1m", BaselineWindow: "1h", MinConsecutive: 3, CreatedBy: "claude-code", SessionID: "seed-session-001"},
		{Metric: store.WatchMetricResponseTime, Operator: store.WatchOpGreaterThan, Threshold: 500, Service: "gateway", Duration: "4h", Urgency: store.WatchUrgencyHigh, CheckInterval: "2m", BaselineWindow: "30m", MinConsecutive: 2, CreatedBy: "claude-code", SessionID: "seed-session-001"},
		{Metric: store.WatchMetricP95Response, Operator: store.WatchOpGreaterThan, Threshold: 1200, Service: "order-service", Endpoint: "/api/v1/orders", Duration: "1h", Urgency: store.WatchUrgencyNormal, CheckInterval: "5m", BaselineWindow: "1h", MinConsecutive: 2, CreatedBy: "claude-code", SessionID: "seed-session-002"},
		{Metric: store.WatchMetricErrorCount, Operator: store.WatchOpGreaterThan, Threshold: 50, Service: "notification-service", Duration: "6h", Urgency: store.WatchUrgencyNormal, CheckInterval: "5m", BaselineWindow: "1h", MinConsecutive: 1, CreatedBy: "claude-code", SessionID: "seed-session-002"},
		{Metric: store.WatchMetricHeartbeat, Operator: store.WatchOpLessThan, Threshold: 1, Service: "payment-api", Duration: "8h", Urgency: store.WatchUrgencyCritical, CheckInterval: "1m", BaselineWindow: "5m", MinConsecutive: 3, CreatedBy: "claude-code", SessionID: "seed-session-001"},
		{Metric: store.WatchMetricResponseTime, Operator: store.WatchOpGreaterThan, Threshold: 800, Service: "payment-api", Endpoint: "/api/v1/checkout", Duration: "2h", Urgency: store.WatchUrgencyCritical, CheckInterval: "1m", BaselineWindow: "30m", MinConsecutive: 3, CreatedBy: "claude-code", SessionID: "seed-session-001"},
	}

	var watchIDs []string
	for _, p := range watchDefs {
		w, err := watchStore.Create(ctx, p)
		if err != nil {
			return fmt.Errorf("creating watch: %w", err)
		}
		watchIDs = append(watchIDs, w.ID)
	}
	slog.Info("seeded watches", "count", len(watchIDs))

	// Set statuses.
	watchStore.UpdateStatus(ctx, watchIDs[2], store.WatchStatusTriggered)
	watchStore.UpdateAfterCheck(ctx, watchIDs[2], 1450, 3, now.Add(5*time.Minute))
	watchStore.UpdateStatus(ctx, watchIDs[4], store.WatchStatusTriggered)
	watchStore.UpdateAfterCheck(ctx, watchIDs[4], 0, 4, now.Add(time.Minute))

	// Watch runs.
	runs := []struct {
		idx     int
		value   float64
		breach  bool
		summary string
	}{
		{0, 0.02, false, "error_rate=0.02 (threshold 0.05): OK"},
		{0, 0.08, true, "error_rate=0.08 (threshold 0.05): BREACH"},
		{1, 180, false, "response_time=180ms (threshold 500ms): OK"},
		{1, 220, false, "response_time=220ms (threshold 500ms): OK"},
		{2, 800, false, "p95_response=800ms (threshold 1200ms): OK"},
		{2, 1350, true, "p95_response=1350ms (threshold 1200ms): BREACH"},
		{2, 1500, true, "p95_response=1500ms (threshold 1200ms): BREACH"},
		{3, 12, false, "error_count=12 (threshold 50): OK"},
		{4, 5, false, "heartbeat: 5 events (threshold 1): OK"},
		{4, 0, true, "heartbeat: 0 events (threshold 1): BREACH"},
		{4, 0, true, "heartbeat: 0 events (threshold 1): BREACH"},
		{5, 350, false, "response_time=350ms (threshold 800ms): OK"},
		{5, 920, true, "response_time=920ms (threshold 800ms): BREACH"},
	}
	for _, r := range runs {
		run, err := watchStore.CreateRun(ctx, watchIDs[r.idx])
		if err != nil {
			continue
		}
		watchStore.CompleteRun(ctx, run.ID, r.value, r.breach, r.summary)
	}
	slog.Info("seeded watch runs", "count", len(runs))

	// Watch alerts.
	alertDefs := []store.CreateWatchAlertParams{
		{WatchID: watchIDs[2], Urgency: store.WatchUrgencyNormal, Summary: "p95 response time for order-service exceeded 1200ms for 3 checks. Current: 1500ms.", TriggerMetric: "p95_response", TriggerValue: 1500, ThresholdValue: 1200},
		{WatchID: watchIDs[4], Urgency: store.WatchUrgencyCritical, Summary: "payment-api heartbeat missing. Zero events for 5+ minutes. Service may be down.", TriggerMetric: "heartbeat", TriggerValue: 0, ThresholdValue: 1},
		{WatchID: watchIDs[0], Urgency: store.WatchUrgencyCritical, Summary: "Error rate for payment-api spiked to 8% (threshold 5%).", TriggerMetric: "error_rate", TriggerValue: 0.08, ThresholdValue: 0.05},
		{WatchID: watchIDs[5], Urgency: store.WatchUrgencyHigh, Summary: "Checkout endpoint response time 920ms (threshold 800ms).", TriggerMetric: "response_time", TriggerValue: 920, ThresholdValue: 800},
	}
	for _, p := range alertDefs {
		watchStore.CreateAlert(ctx, p)
	}
	slog.Info("seeded watch alerts", "count", len(alertDefs))

	// --- Data Sources ---
	dsStore := deps.DSStore
	dsDefs := []store.CreateDataSourceParams{
		{Type: store.ConnectorLogs, Name: "Production Logs", Config: map[string]any{}},
		{Type: store.ConnectorLogs, Name: "Staging Logs", Config: map[string]any{}},
	}
	for _, p := range dsDefs {
		ds, err := dsStore.Create(ctx, p)
		if err != nil {
			slog.Warn("failed to create connector", "name", p.Name, "error", err)
			continue
		}
		status := store.StatusConnected
		dsStore.Update(ctx, ds.ID, store.UpdateDataSourceParams{Status: &status})
	}
	slog.Info("seeded connectors", "count", len(dsDefs))

	slog.Info("seed complete",
		"logs", len(logEntries),
		"servers", len(serverIDs),
		"watches", len(watchIDs),
		"deploys", len(deployDefs),
		"request_summaries", reqCount,
	)
	return nil
}

func ptr(f float64) *float64 { return &f }
