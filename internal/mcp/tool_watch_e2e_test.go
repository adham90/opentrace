package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

// TestWatch_EndToEnd exercises the full lifecycle:
// 1. Create a watch via MCP tool
// 2. Verify baseline is captured
// 3. Insert logs that breach the threshold
// 4. Evaluate → trigger alert
// 5. Check watch_status shows triggered + pending alert
// 6. Investigate the alert (mode A)
// 7. Dismiss the alert
// 8. Stop the watch
func TestWatch_EndToEnd(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWatchStore(db)
	ls := store.NewLogStore(db)
	metrics := watcher.NewWatchMetrics(ls)
	evaluator := watcher.NewWatchEvaluator(metrics, ws)
	evidenceBuilder := watcher.NewWatchEvidenceBuilder(ls, metrics)
	ctx := context.Background()

	// --- Step 1: Insert some baseline logs (low error rate) ---
	now := time.Now().UTC()
	baselineLogs := make([]store.LogEntry, 100)
	for i := range baselineLogs {
		level := "info"
		if i < 2 { // 2% error rate in baseline
			level = "error"
		}
		baselineLogs[i] = store.LogEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Second),
			Level:     level,
			Service:   "payments",
			Message:   "baseline log entry",
		}
	}
	n, err := ls.BatchInsert(ctx, baselineLogs)
	if err != nil {
		t.Fatalf("inserting baseline logs: %v", err)
	}
	t.Logf("Inserted %d baseline logs", n)

	// --- Step 2: Create a watch via MCP tool ---
	createHandler := watchHandler(ws, ls, metrics)
	createResult := callE2ETool(t, createHandler, map[string]any{
		"metric":          "error_rate",
		"operator":        "gt",
		"threshold":       0.1,
		"service":         "payments",
		"duration":        "2h",
		"urgency":         "high",
		"check_interval":  "10s",
		"min_consecutive": float64(1),
		"session_id":      "claude-session-123",
	})

	var createdWatch store.Watch
	if err := json.Unmarshal([]byte(createResult), &createdWatch); err != nil {
		t.Fatalf("unmarshal created watch: %v", err)
	}

	t.Logf("Created watch: id=%s metric=%s threshold=%.2f", createdWatch.ID, createdWatch.Metric, createdWatch.Threshold)

	if createdWatch.Status != store.WatchStatusActive {
		t.Errorf("watch status = %q, want active", createdWatch.Status)
	}
	if createdWatch.BaselineJSON == nil {
		t.Fatal("expected baseline to be captured on creation")
	}
	t.Logf("Baseline captured: error_rate=%.4f log_count=%d", createdWatch.BaselineJSON.ErrorRate, createdWatch.BaselineJSON.LogCount)

	// --- Step 3: Check watch_status shows the new watch ---
	statusHandler := watchStatusHandler(ws)
	statusResult := callE2ETool(t, statusHandler, map[string]any{})

	var statusResp struct {
		Watches       []json.RawMessage `json:"watches"`
		Total         int               `json:"total"`
		PendingAlerts int               `json:"pending_alerts"`
	}
	if err := json.Unmarshal([]byte(statusResult), &statusResp); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if statusResp.Total != 1 {
		t.Errorf("watch_status total = %d, want 1", statusResp.Total)
	}
	if statusResp.PendingAlerts != 0 {
		t.Errorf("pending_alerts = %d, want 0 before any breach", statusResp.PendingAlerts)
	}
	t.Logf("watch_status: %d watches, %d pending alerts", statusResp.Total, statusResp.PendingAlerts)

	// --- Step 4: Insert bad logs that spike error rate above threshold ---
	badLogs := make([]store.LogEntry, 50)
	for i := range badLogs {
		level := "error" // all errors → 100% error rate for these
		badLogs[i] = store.LogEntry{
			Timestamp:      now.Add(time.Duration(i) * time.Millisecond),
			Level:          level,
			Service:        "payments",
			Message:        "PaymentProcessingError: gateway timeout",
			ExceptionClass: "PaymentProcessingError",
			SourceFile:     "app/services/payment_service.rb",
			TraceID:        "trace-abc-123",
		}
	}
	n, err = ls.BatchInsert(ctx, badLogs)
	if err != nil {
		t.Fatalf("inserting bad logs: %v", err)
	}
	t.Logf("Inserted %d error logs to spike error rate", n)

	// --- Step 5: Evaluate the watch (simulating scheduler) ---
	w, err := ws.GetByID(ctx, createdWatch.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}

	run, err := ws.CreateRun(ctx, w.ID)
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	evalResult, err := evaluator.Evaluate(ctx, w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	t.Logf("Evaluation: value=%.4f breached=%v hasAlert=%v summary=%q",
		evalResult.Value, evalResult.Breached, evalResult.HasAlert, evalResult.Summary)

	if !evalResult.Breached {
		t.Fatal("expected breach after inserting error logs")
	}
	if !evalResult.HasAlert {
		t.Fatal("expected alert to fire")
	}

	// Complete the run
	_ = ws.CompleteRun(ctx, run.ID, evalResult.Value, evalResult.Breached, evalResult.Summary)

	// Build evidence
	evidence, err := evidenceBuilder.Build(ctx, w, evalResult)
	if err != nil {
		t.Fatalf("Build evidence: %v", err)
	}
	t.Logf("Evidence: %d recent errors, %d relevant logs, %d source files, %d trace IDs",
		len(evidence.RecentErrors), len(evidence.RelevantLogs),
		len(evidence.SourceFiles), len(evidence.TraceIDs))

	if len(evidence.RecentErrors) == 0 {
		t.Error("expected recent errors in evidence")
	}
	if len(evidence.SourceFiles) == 0 {
		t.Error("expected source files in evidence")
	}
	if len(evidence.TraceIDs) == 0 {
		t.Error("expected trace IDs in evidence")
	}

	// Check for new errors (not in baseline)
	hasNewError := false
	for _, e := range evidence.NewErrors {
		if e.ExceptionClass == "PaymentProcessingError" && e.IsNew {
			hasNewError = true
			break
		}
	}
	if !hasNewError {
		t.Error("expected PaymentProcessingError to be flagged as new error")
	}

	// Create the alert
	alert, err := ws.CreateAlert(ctx, store.CreateWatchAlertParams{
		WatchID:        w.ID,
		RunID:          run.ID,
		Urgency:        w.Urgency,
		Summary:        evalResult.Summary,
		TriggerMetric:  string(w.Metric),
		TriggerValue:   evalResult.Value,
		ThresholdValue: w.Threshold,
		Evidence:       evidence,
	})
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	t.Logf("Alert created: id=%s urgency=%s", alert.ID, alert.Urgency)

	// --- Step 6: Check watch_status now shows triggered + pending alert ---
	statusResult2 := callE2ETool(t, statusHandler, map[string]any{})
	var statusResp2 struct {
		Watches       []json.RawMessage `json:"watches"`
		Total         int               `json:"total"`
		PendingAlerts int               `json:"pending_alerts"`
	}
	json.Unmarshal([]byte(statusResult2), &statusResp2)

	if statusResp2.PendingAlerts != 1 {
		t.Errorf("pending_alerts after alert = %d, want 1", statusResp2.PendingAlerts)
	}
	t.Logf("watch_status after alert: %d watches, %d pending alerts", statusResp2.Total, statusResp2.PendingAlerts)

	// Verify at least one watch is in triggered status
	foundTriggered := false
	for _, raw := range statusResp2.Watches {
		var ws struct {
			Status string `json:"status"`
		}
		json.Unmarshal(raw, &ws)
		if ws.Status == "triggered" {
			foundTriggered = true
		}
	}
	if !foundTriggered {
		t.Error("expected at least one triggered watch in status")
	}

	// --- Step 7: Investigate alert (Mode A) ---
	investigateH := investigateHandler(ws, ls, metrics)
	investigateResult := callE2ETool(t, investigateH, map[string]any{
		"alert_id": alert.ID,
	})

	var alertWrapper struct {
		Alert store.WatchAlert `json:"alert"`
	}
	if err := json.Unmarshal([]byte(investigateResult), &alertWrapper); err != nil {
		t.Fatalf("unmarshal investigated alert: %v", err)
	}
	investigatedAlert := alertWrapper.Alert
	if investigatedAlert.ID != alert.ID {
		t.Errorf("investigated alert ID = %q, want %q", investigatedAlert.ID, alert.ID)
	}
	if investigatedAlert.EvidenceJSON == nil {
		t.Error("expected evidence in investigated alert")
	} else {
		t.Logf("Investigated: %d recent errors, %d new errors, %d source files",
			len(investigatedAlert.EvidenceJSON.RecentErrors),
			len(investigatedAlert.EvidenceJSON.NewErrors),
			len(investigatedAlert.EvidenceJSON.SourceFiles))
	}

	// --- Step 8: Investigate service (Mode B) ---
	investigateB := callE2ETool(t, investigateH, map[string]any{
		"service": "payments",
		"window":  "1h",
	})
	var invWrapper struct {
		Investigation struct {
			Service    string  `json:"service"`
			ErrorRate  float64 `json:"error_rate"`
			ErrorCount float64 `json:"error_count"`
			LogCount   float64 `json:"log_count"`
		} `json:"investigation"`
	}
	json.Unmarshal([]byte(investigateB), &invWrapper)
	investigation := invWrapper.Investigation
	t.Logf("Investigation (Mode B): service=%s error_rate=%.4f error_count=%.0f log_count=%.0f",
		investigation.Service, investigation.ErrorRate, investigation.ErrorCount, investigation.LogCount)

	if investigation.ErrorRate < 0.1 {
		t.Errorf("investigation error_rate = %.4f, want >= 0.1", investigation.ErrorRate)
	}

	// --- Step 9: Dismiss the alert ---
	dismissH := dismissWatchHandler(ws)
	callE2ETool(t, dismissH, map[string]any{
		"alert_id": alert.ID,
		"action":   "dismiss",
		"reason":   "investigated and deployed fix",
	})

	dismissedAlert, _ := ws.GetAlert(ctx, alert.ID)
	if dismissedAlert.Status != "dismissed" {
		t.Errorf("alert status after dismiss = %q, want dismissed", dismissedAlert.Status)
	}
	if dismissedAlert.DismissReason != "investigated and deployed fix" {
		t.Errorf("dismiss reason = %q, want 'investigated and deployed fix'", dismissedAlert.DismissReason)
	}
	t.Logf("Alert dismissed: reason=%q", dismissedAlert.DismissReason)

	// Verify pending count dropped
	pendingCount, _ := ws.CountPendingAlerts(ctx)
	if pendingCount != 0 {
		t.Errorf("pending alerts after dismiss = %d, want 0", pendingCount)
	}

	// --- Step 10: Stop the watch ---
	callE2ETool(t, dismissH, map[string]any{
		"watch_id": createdWatch.ID,
		"action":   "stop",
	})

	_, err = ws.GetByID(ctx, createdWatch.ID)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after stopping watch, got %v", err)
	}
	t.Log("Watch stopped and deleted successfully")

	// --- Step 11: Verify session filter works ---
	// Create another watch in a different session
	callE2ETool(t, createHandler, map[string]any{
		"metric":     "log_count",
		"operator":   "gt",
		"threshold":  float64(1000),
		"service":    "auth",
		"session_id": "other-session",
	})

	// Filter by session
	sessionResult := callE2ETool(t, statusHandler, map[string]any{
		"session_id": "other-session",
	})
	var sessionResp struct {
		Total int `json:"total"`
	}
	json.Unmarshal([]byte(sessionResult), &sessionResp)
	if sessionResp.Total != 1 {
		t.Errorf("session-filtered total = %d, want 1", sessionResp.Total)
	}
	t.Log("Session filter works correctly")

	t.Log("=== End-to-end test passed ===")
}

// TestWatch_CatalogRegistration verifies all 4 watch tools appear in
// the tool catalog when deps include WatchStore, LogStore, WatchMetrics.
func TestWatch_CatalogRegistration(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWatchStore(db)
	ls := store.NewLogStore(db)
	metrics := watcher.NewWatchMetrics(ls)
	registry := connector.NewRegistry()

	deps := Deps{
		WatchStore:   ws,
		LogStore:     ls,
		WatchMetrics: metrics,
		Registry:     registry,
	}

	// Verify catalog includes all 4 tools
	catalog := BuildCatalog(deps)
	if catalog == nil {
		t.Fatal("BuildCatalog returned nil")
	}

	toolNames := make(map[string]bool)
	for _, cat := range catalog.Categories() {
		for _, tool := range cat.Tools {
			toolNames[tool.Name] = true
		}
	}

	expectedTools := []string{"watch_status", "investigate", "watch", "dismiss_watch"}
	for _, tool := range expectedTools {
		if !toolNames[tool] {
			t.Errorf("catalog missing tool: %s", tool)
		} else {
			t.Logf("Found tool in catalog: %s", tool)
		}
	}

	// Verify watch tools are NOT in catalog when WatchStore is nil
	emptyDeps := Deps{Registry: registry}
	emptyCatalog := BuildCatalog(emptyDeps)
	emptyToolNames := make(map[string]bool)
	for _, cat := range emptyCatalog.Categories() {
		for _, tool := range cat.Tools {
			emptyToolNames[tool.Name] = true
		}
	}
	for _, tool := range expectedTools {
		if emptyToolNames[tool] {
			t.Errorf("tool %s should NOT appear when WatchStore is nil", tool)
		}
	}
	t.Log("Verified watch tools are absent when WatchStore is nil")
}

func callE2ETool(t *testing.T, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error), args map[string]any) string {
	t.Helper()
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = args
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("tool call failed: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("empty result content")
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return text.Text
}

