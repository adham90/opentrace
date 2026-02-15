package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
	"github.com/adham90/opentrace/internal/watcher"
)

func setupWatchTestDeps(t *testing.T) (store.WatchStore, store.LogStore, *watcher.WatchMetrics) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("opening in-memory SQLite: %v", err)
	}
	if err := store.RunSQLiteMigrations(db); err != nil {
		db.Close()
		t.Fatalf("running migrations: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	ws := store.NewWatchStore(db)
	ls := store.NewLogStore(db)
	metrics := watcher.NewWatchMetrics(ls)
	return ws, ls, metrics
}

func callTool(t *testing.T, handler func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error), args map[string]any) string {
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

func TestWatch_CreateAndStatus(t *testing.T) {
	ws, ls, metrics := setupWatchTestDeps(t)

	// Create a watch
	createHandler := watchHandler(ws, ls, metrics)
	createResult := callTool(t, createHandler, map[string]any{
		"metric":    "error_rate",
		"operator":  "gt",
		"threshold": 0.05,
		"service":   "web",
		"duration":  "2h",
	})

	var created store.Watch
	if err := json.Unmarshal([]byte(createResult), &created); err != nil {
		t.Fatalf("unmarshal create result: %v", err)
	}
	if created.ID == "" {
		t.Error("expected non-empty ID")
	}
	if created.Metric != store.WatchMetricErrorRate {
		t.Errorf("metric = %q, want error_rate", created.Metric)
	}

	// Check status
	statusHandler := watchStatusHandler(ws)
	statusResult := callTool(t, statusHandler, map[string]any{})

	var status struct {
		Watches []json.RawMessage `json:"watches"`
		Total   int               `json:"total"`
	}
	if err := json.Unmarshal([]byte(statusResult), &status); err != nil {
		t.Fatalf("unmarshal status: %v", err)
	}
	if status.Total != 1 {
		t.Errorf("total = %d, want 1", status.Total)
	}
}

func TestWatch_Investigate_ModeB(t *testing.T) {
	ws, ls, metrics := setupWatchTestDeps(t)
	ctx := context.Background()

	// Insert some logs with recent timestamps
	now := time.Now().UTC()
	entries := make([]store.LogEntry, 10)
	for i := range entries {
		level := "info"
		if i < 3 {
			level = "error"
		}
		entries[i] = store.LogEntry{
			Timestamp: now.Add(-time.Duration(i) * time.Minute),
			Service:   "api",
			Level:     level,
			Message:   "test log",
		}
	}
	ls.BatchInsert(ctx, entries)

	handler := investigateHandler(ws, ls, metrics)
	result := callTool(t, handler, map[string]any{
		"service": "api",
		"window":  "1h",
	})

	var inv struct {
		Service    string  `json:"service"`
		ErrorRate  float64 `json:"error_rate"`
		LogCount   float64 `json:"log_count"`
		ErrorCount float64 `json:"error_count"`
	}
	if err := json.Unmarshal([]byte(result), &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if inv.Service != "api" {
		t.Errorf("service = %q, want api", inv.Service)
	}
	if inv.LogCount < 10 {
		t.Errorf("log_count = %v, want >= 10", inv.LogCount)
	}
}

func TestWatch_Investigate_ModeA(t *testing.T) {
	ws, _, _ := setupWatchTestDeps(t)
	ctx := context.Background()

	// Create a watch and alert
	w, _ := ws.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.05,
	})
	alert, _ := ws.CreateAlert(ctx, store.CreateWatchAlertParams{
		WatchID:        w.ID,
		Urgency:        store.WatchUrgencyHigh,
		Summary:        "test alert",
		TriggerMetric:  "error_rate",
		TriggerValue:   0.12,
		ThresholdValue: 0.05,
	})

	handler := investigateHandler(ws, nil, nil)
	result := callTool(t, handler, map[string]any{
		"alert_id": alert.ID,
	})

	var got store.WatchAlert
	if err := json.Unmarshal([]byte(result), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != alert.ID {
		t.Errorf("alert ID = %q, want %q", got.ID, alert.ID)
	}
	if got.TriggerValue != 0.12 {
		t.Errorf("trigger_value = %v, want 0.12", got.TriggerValue)
	}
}

func TestWatch_Dismiss(t *testing.T) {
	ws, _, _ := setupWatchTestDeps(t)
	ctx := context.Background()

	// Create a watch
	w, _ := ws.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.05,
	})

	// Stop the watch
	handler := dismissWatchHandler(ws)
	result := callTool(t, handler, map[string]any{
		"watch_id": w.ID,
		"action":   "stop",
	})
	if result == "" {
		t.Error("expected non-empty result")
	}

	// Verify deleted
	_, err := ws.GetByID(ctx, w.ID)
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after stop, got %v", err)
	}
}

func TestWatch_DismissAlert(t *testing.T) {
	ws, _, _ := setupWatchTestDeps(t)
	ctx := context.Background()

	w, _ := ws.Create(ctx, store.CreateWatchParams{
		Metric:    store.WatchMetricErrorRate,
		Operator:  store.WatchOpGreaterThan,
		Threshold: 0.05,
	})
	alert, _ := ws.CreateAlert(ctx, store.CreateWatchAlertParams{
		WatchID:        w.ID,
		Urgency:        store.WatchUrgencyNormal,
		Summary:        "test",
		TriggerMetric:  "error_rate",
		TriggerValue:   0.1,
		ThresholdValue: 0.05,
	})

	handler := dismissWatchHandler(ws)

	// Dismiss
	callTool(t, handler, map[string]any{
		"alert_id": alert.ID,
		"action":   "dismiss",
		"reason":   "false positive",
	})

	got, _ := ws.GetAlert(ctx, alert.ID)
	if got.Status != "dismissed" {
		t.Errorf("status = %q, want dismissed", got.Status)
	}
}
