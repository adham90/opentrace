package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/adham90/opentrace/internal/store"
)

func TestUserJourneyHandler_ListSessions(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-1", UserID: "user-42",
			Message: "req",
			RequestSummary: &store.RequestSummary{
				Controller: "Home", Action: "index", Status: 200, DurationMs: 100,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	js.BuildSessions(ctx, now.Add(-time.Hour))

	handler := userJourneyHandler(js)
	req := mcp.CallToolRequest{}
	req.Params.Name = "user_journey"
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
	json.Unmarshal([]byte(text), &resp)

	sessions, ok := resp["sessions"].([]any)
	if !ok || len(sessions) == 0 {
		t.Fatal("expected sessions array with entries")
	}
}

func TestUserJourneyHandler_ByUser(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-a", UserID: "user-10",
			Message: "r1",
			RequestSummary: &store.RequestSummary{
				Controller: "A", Action: "b", Status: 200, DurationMs: 50,
			},
		},
		{
			Timestamp: now.Add(-25 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-a", UserID: "user-10",
			Message: "r2",
			RequestSummary: &store.RequestSummary{
				Controller: "C", Action: "d", Status: 200, DurationMs: 70,
			},
		},
	}
	ls.BatchInsert(ctx, entries)
	js.BuildSessions(ctx, now.Add(-time.Hour))

	handler := userJourneyHandler(js)
	req := mcp.CallToolRequest{}
	req.Params.Name = "user_journey"
	req.Params.Arguments = map[string]any{
		"user_id": "user-10",
		"since":   "2h",
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	if resp["user_id"] != "user-10" {
		t.Errorf("user_id = %v, want user-10", resp["user_id"])
	}

	steps, ok := resp["recent_steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected 2 recent steps, got %v", len(steps))
	}
}

func TestPathAnalysisHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	// Create 3 sessions with same path
	for i, sid := range []string{"s1", "s2", "s3"} {
		entries := []store.LogEntry{
			{
				Timestamp: now.Add(-time.Duration(30+i*5) * time.Minute), Level: "INFO", Service: "api",
				SessionID: sid, UserID: "u",
				Message: "s1",
				RequestSummary: &store.RequestSummary{
					Controller: "Login", Action: "show", Status: 200, DurationMs: 50,
				},
			},
			{
				Timestamp: now.Add(-time.Duration(29+i*5) * time.Minute), Level: "INFO", Service: "api",
				SessionID: sid, UserID: "u",
				Message: "s2",
				RequestSummary: &store.RequestSummary{
					Controller: "Dashboard", Action: "index", Status: 200, DurationMs: 100,
				},
			},
		}
		ls.BatchInsert(ctx, entries)
	}

	handler := pathAnalysisHandler(js)
	req := mcp.CallToolRequest{}
	req.Params.Name = "path_analysis"
	req.Params.Arguments = map[string]any{
		"since":           "2h",
		"min_occurrences": float64(2),
		"path_length":     float64(3),
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	paths, ok := resp["paths"].([]any)
	if !ok || len(paths) == 0 {
		t.Fatal("expected paths array with entries")
	}
}

func TestFunnelAnalysisHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()

	// Create via handler
	handler := funnelAnalysisHandler(js)
	createReq := mcp.CallToolRequest{}
	createReq.Params.Name = "funnel_analysis"
	createReq.Params.Arguments = map[string]any{
		"action":  "create",
		"name":    "Test Funnel",
		"service": "api",
		"steps": []any{
			map[string]any{"controller": "Cart", "action": "show", "label": "View cart"},
			map[string]any{"controller": "Orders", "action": "create", "label": "Place order"},
		},
	}
	result, err := handler(ctx, createReq)
	if err != nil {
		t.Fatalf("create error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var createResp map[string]any
	json.Unmarshal([]byte(text), &createResp)

	if createResp["created"] != true {
		t.Fatal("expected created=true")
	}
	funnelID := createResp["funnel_id"].(float64)

	// Insert test data
	entries := []store.LogEntry{
		{Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s1", Message: "r",
			RequestSummary: &store.RequestSummary{Controller: "Cart", Action: "show", Status: 200, DurationMs: 50}},
		{Timestamp: now.Add(-29 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s1", Message: "r",
			RequestSummary: &store.RequestSummary{Controller: "Orders", Action: "create", Status: 201, DurationMs: 200}},
		{Timestamp: now.Add(-20 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "s2", Message: "r",
			RequestSummary: &store.RequestSummary{Controller: "Cart", Action: "show", Status: 200, DurationMs: 50}},
	}
	ls.BatchInsert(ctx, entries)

	// Analyze
	analyzeReq := mcp.CallToolRequest{}
	analyzeReq.Params.Name = "funnel_analysis"
	analyzeReq.Params.Arguments = map[string]any{
		"action":    "analyze",
		"funnel_id": funnelID,
		"since":     "2h",
	}
	result, err = handler(ctx, analyzeReq)
	if err != nil {
		t.Fatalf("analyze error: %v", err)
	}

	text = result.Content[0].(mcp.TextContent).Text
	var analyzeResp map[string]any
	json.Unmarshal([]byte(text), &analyzeResp)

	if analyzeResp["total_entered"].(float64) != 2 {
		t.Errorf("total_entered = %v, want 2", analyzeResp["total_entered"])
	}

	// List
	listReq := mcp.CallToolRequest{}
	listReq.Params.Name = "funnel_analysis"
	listReq.Params.Arguments = map[string]any{"action": "list"}
	result, _ = handler(ctx, listReq)
	text = result.Content[0].(mcp.TextContent).Text
	var listResp map[string]any
	json.Unmarshal([]byte(text), &listResp)

	funnels := listResp["funnels"].([]any)
	if len(funnels) != 1 {
		t.Errorf("expected 1 funnel, got %d", len(funnels))
	}

	// Delete
	deleteReq := mcp.CallToolRequest{}
	deleteReq.Params.Name = "funnel_analysis"
	deleteReq.Params.Arguments = map[string]any{"action": "delete", "funnel_id": funnelID}
	result, _ = handler(ctx, deleteReq)
	text = result.Content[0].(mcp.TextContent).Text
	var delResp map[string]any
	json.Unmarshal([]byte(text), &delResp)
	if delResp["deleted"] != true {
		t.Error("expected deleted=true")
	}
}

func TestRequestTimelineHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-10 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-tl", RequestID: "req-1",
			Message: "request",
			RequestSummary: &store.RequestSummary{
				Controller: "Reports", Action: "show", Method: "GET",
				Path: "/reports/1", Status: 200, DurationMs: 2500,
				Timeline: `[{"type":"sql","name":"User Load","start_ms":0,"duration_ms":5},{"type":"sql","name":"Report Load","start_ms":10,"duration_ms":1800},{"type":"view","name":"show.html","start_ms":1810,"duration_ms":200}]`,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	// Get log ID
	logs, _ := ls.Search(ctx, store.LogSearchParams{Limit: 1})
	logID := logs[0].ID

	handler := requestTimelineHandler(js)
	req := mcp.CallToolRequest{}
	req.Params.Name = "request_timeline"
	req.Params.Arguments = map[string]any{
		"log_id": float64(logID),
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

	events, ok := resp["events"].([]any)
	if !ok || len(events) != 3 {
		t.Fatalf("expected 3 events, got %v", len(events))
	}

	bottleneck, ok := resp["bottleneck"].(map[string]any)
	if !ok {
		t.Fatal("expected bottleneck")
	}
	if bottleneck["name"] != "Report Load" {
		t.Errorf("bottleneck name = %v, want Report Load", bottleneck["name"])
	}

	summary := resp["summary"].(map[string]any)
	if summary["sql_time_ms"].(float64) < 1800 {
		t.Errorf("sql_time_ms = %v, want >= 1800", summary["sql_time_ms"])
	}
}

func TestSessionWaterfallHandler(t *testing.T) {
	db := setupTestDB(t)
	ls := store.NewLogStore(db)
	js := store.NewJourneyStore(db)
	ctx := context.Background()

	now := time.Now().UTC()
	entries := []store.LogEntry{
		{
			Timestamp: now.Add(-30 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-wf", RequestID: "req-a",
			Message: "r1",
			RequestSummary: &store.RequestSummary{
				Controller: "Home", Action: "index", Status: 200, DurationMs: 100,
				Timeline: `[{"type":"sql","name":"Load","start_ms":0,"duration_ms":50}]`,
			},
		},
		{
			Timestamp: now.Add(-25 * time.Minute), Level: "INFO", Service: "api",
			SessionID: "sess-wf", RequestID: "req-b",
			Message: "r2",
			RequestSummary: &store.RequestSummary{
				Controller: "Users", Action: "show", Status: 200, DurationMs: 200,
				Timeline: `[{"type":"sql","name":"User","start_ms":0,"duration_ms":10}]`,
			},
		},
	}
	ls.BatchInsert(ctx, entries)

	handler := sessionWaterfallHandler(js)
	req := mcp.CallToolRequest{}
	req.Params.Name = "session_waterfall"
	req.Params.Arguments = map[string]any{
		"session_id": "sess-wf",
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

	if resp["total_requests"].(float64) != 2 {
		t.Errorf("total_requests = %v, want 2", resp["total_requests"])
	}

	requests, ok := resp["requests"].([]any)
	if !ok || len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %v", len(requests))
	}

	// Check first request has events
	first := requests[0].(map[string]any)
	if first["event_count"].(float64) != 1 {
		t.Errorf("first request event_count = %v, want 1", first["event_count"])
	}
}
