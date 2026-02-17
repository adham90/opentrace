package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestWebAnalyticsHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	as := store.NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Method: "GET", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-20 * time.Minute), Level: "ERROR", Service: "api", Message: "err",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "c", Method: "POST", Status: 500, DurationMs: 500}},
		{Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Method: "GET", Status: 200, DurationMs: 80}},
	}
	ls.BatchInsert(ctx, entries)

	handler := webAnalyticsHandler(as)
	req := mcp.CallToolRequest{}
	req.Params.Name = "web_analytics"
	req.Params.Arguments = map[string]any{
		"since": "2h",
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
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	summary, ok := resp["summary"].(map[string]any)
	if !ok {
		t.Fatal("expected summary")
	}

	totalReqs := summary["total_requests"].(float64)
	if totalReqs != 3 {
		t.Errorf("total_requests = %v, want 3", totalReqs)
	}

	// Verify status breakdown exists
	status, ok := resp["status_breakdown"].(map[string]any)
	if !ok {
		t.Fatal("expected status_breakdown")
	}
	if status["2xx"].(float64) != 2 {
		t.Errorf("2xx = %v, want 2", status["2xx"])
	}

	// Verify suggested tools
	if _, ok := resp["suggested_tools"]; !ok {
		t.Error("expected suggested_tools in response")
	}
}

func TestTopEndpointsHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	as := store.NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Hour)
	entries := []store.LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &store.RequestSummary{Controller: "Users", Action: "index", Method: "GET", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-29 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &store.RequestSummary{Controller: "Users", Action: "index", Method: "GET", Status: 200, DurationMs: 120}},
		{Timestamp: now.Add(-28 * time.Minute), Level: "INFO", Service: "api", Message: "ok",
			RequestSummary: &store.RequestSummary{Controller: "Posts", Action: "create", Method: "POST", Status: 201, DurationMs: 250}},
	}
	ls.BatchInsert(ctx, entries)
	as.AggregateEndpointStats(ctx, "1h", now.Add(-time.Hour))

	handler := topEndpointsHandler(as)
	req := mcp.CallToolRequest{}
	req.Params.Name = "top_endpoints"
	req.Params.Arguments = map[string]any{
		"since":        "2h",
		"sort_by":      "request_count",
		"min_requests": float64(1),
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

	endpoints, ok := resp["endpoints"].([]any)
	if !ok || len(endpoints) == 0 {
		t.Fatal("expected endpoints array with entries")
	}

	// Users#index should be first (2 requests)
	first := endpoints[0].(map[string]any)
	if first["endpoint"] != "Users#index" {
		t.Errorf("first endpoint = %v, want Users#index", first["endpoint"])
	}
}

func TestTrafficHeatmapHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	as := store.NewAnalyticsStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api", Message: "req1",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 100}},
		{Timestamp: now.Add(-5 * time.Minute), Level: "INFO", Service: "api", Message: "req2",
			RequestSummary: &store.RequestSummary{Controller: "A", Action: "b", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries)
	as.UpdateTrafficHeatmap(ctx, now.Add(-time.Hour))

	handler := trafficHeatmapHandler(as)
	req := mcp.CallToolRequest{}
	req.Params.Name = "traffic_heatmap"
	req.Params.Arguments = map[string]any{
		"service": "api",
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

	cells, ok := resp["cells"].([]any)
	if !ok || len(cells) == 0 {
		t.Fatal("expected cells array with entries")
	}

	if resp["peak"] == nil {
		t.Error("expected peak in response")
	}
}
