package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestTrendsHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	ts := store.NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{
			Timestamp:  now.Add(-30 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "request ok",
			RequestSummary: &store.RequestSummary{
				Controller: "Users",
				Action:     "index",
				Status:     200,
				DurationMs: 120,
				SQLCount:   5,
			},
		},
		{
			Timestamp:  now.Add(-20 * time.Minute),
			Level:      "ERROR",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "request failed",
			RequestSummary: &store.RequestSummary{
				Controller: "Users",
				Action:     "show",
				Status:     500,
				DurationMs: 800,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-time.Hour))

	handler := trendsHandler(ts)
	req := mcp.CallToolRequest{}
	req.Params.Name = "trends"
	req.Params.Arguments = map[string]any{
		"metric":   "request_volume",
		"interval": "1h",
		"since":    "2h",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %v", result.Content)
	}

	// Parse the response
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp["metric"] != "request_volume" {
		t.Errorf("metric = %v, want request_volume", resp["metric"])
	}
	points, ok := resp["data_points"].([]any)
	if !ok || len(points) == 0 {
		t.Error("expected data_points")
	}
}

func TestTrendsHandler_WithComparison(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	ts := store.NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	// Current period data
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "current",
			RequestSummary: &store.RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 100,
			},
		},
	}
	// Baseline period data (previous hour)
	entries = append(entries, store.LogEntry{
		Timestamp: now.Add(-90 * time.Minute),
		Level:     "INFO",
		Service:   "api",
		Message:   "baseline",
		RequestSummary: &store.RequestSummary{
			Controller: "A", Action: "b", Status: 200, DurationMs: 50,
		},
	})
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-2*time.Hour))

	handler := trendsHandler(ts)
	req := mcp.CallToolRequest{}
	req.Params.Name = "trends"
	req.Params.Arguments = map[string]any{
		"metric":     "request_volume",
		"interval":   "1h",
		"since":      "1h",
		"compare_to": "previous_period",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatal("expected summary")
	}
	// Should have a trend field when comparing
	if _, ok := summary["trend"]; !ok {
		t.Error("expected trend field in summary")
	}
}

func TestTopMoversHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	ts := store.NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "current",
			RequestSummary: &store.RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 500,
			},
		},
		{
			Timestamp: now.Add(-90 * time.Minute),
			Level:     "INFO",
			Service:   "api",
			Message:   "baseline",
			RequestSummary: &store.RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 100,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-2*time.Hour))

	handler := topMoversHandler(ts)
	req := mcp.CallToolRequest{}
	req.Params.Name = "top_movers"
	req.Params.Arguments = map[string]any{
		"metric":   "avg_response",
		"since":    "3h",
		"baseline": "previous_period",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool error: %v", result.Content)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	movers, ok := resp["movers"].([]any)
	if !ok {
		t.Fatal("expected movers array")
	}
	if len(movers) == 0 {
		t.Error("expected at least one mover")
	}
}

func TestTrendsHandler_DeployMarkers(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	ts := store.NewTrendStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{
			Timestamp:  now.Add(-30 * time.Minute),
			Level:      "INFO",
			Service:    "api",
			CommitHash: "abc123",
			Message:    "deployed",
			RequestSummary: &store.RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 100,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	ts.AggregateBuckets(ctx, "1h", now.Add(-time.Hour))

	handler := trendsHandler(ts)
	req := mcp.CallToolRequest{}
	req.Params.Name = "trends"
	req.Params.Arguments = map[string]any{
		"since": "2h",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	markers, ok := resp["deploy_markers"].([]any)
	if !ok || len(markers) == 0 {
		t.Error("expected deploy_markers in response")
	}
}
