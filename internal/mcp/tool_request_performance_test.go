package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockLogStoreWithSummaries extends mockLogStore with request summary support.
type mockLogStoreWithSummaries struct {
	mockLogStore
	summaries []store.RequestSummaryResult
}

func (m *mockLogStoreWithSummaries) SearchRequestSummaries(_ context.Context, params store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	var result []store.RequestSummaryResult
	for _, s := range m.summaries {
		if params.Start != nil && s.Timestamp.Before(*params.Start) {
			continue
		}
		if params.End != nil && s.Timestamp.After(*params.End) {
			continue
		}
		if params.NPlusOneOnly && !s.NPlusOne {
			continue
		}
		if params.MinDurationMs > 0 && s.DurationMs < params.MinDurationMs {
			continue
		}
		if params.MinSQLCount > 0 && s.SQLCount < params.MinSQLCount {
			continue
		}
		if params.Controller != "" && s.Controller != params.Controller {
			continue
		}
		result = append(result, s)
		if params.Limit > 0 && len(result) >= params.Limit {
			break
		}
	}
	return result, nil
}

func TestRequestPerformance_NPlusOne(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStoreWithSummaries{
		summaries: []store.RequestSummaryResult{
			{
				RequestSummary: store.RequestSummary{LogID: 1, Controller: "UsersController", Action: "index", DurationMs: 200, SQLCount: 15, NPlusOne: true, DuplicateQueries: 3, WorstDuplicateCount: 10},
				Timestamp:      now.Add(-1 * time.Hour),
				Service:        "web",
			},
			{
				RequestSummary: store.RequestSummary{LogID: 2, Controller: "PostsController", Action: "show", DurationMs: 50, SQLCount: 3, NPlusOne: false},
				Timestamp:      now.Add(-2 * time.Hour),
				Service:        "web",
			},
			{
				RequestSummary: store.RequestSummary{LogID: 3, Controller: "CommentsController", Action: "index", DurationMs: 500, SQLCount: 30, NPlusOne: true, DuplicateQueries: 5, WorstDuplicateCount: 20},
				Timestamp:      now.Add(-3 * time.Hour),
				Service:        "web",
			},
		},
	}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"n_plus_one_only": true,
		"time_range":      "24h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v\n%s", err, text)
	}

	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("expected 2 N+1 entries, got %d", len(entries))
	}

	summary := resp["summary"].(map[string]any)
	if summary["n_plus_one_count"].(float64) != 2 {
		t.Errorf("n_plus_one_count = %v, want 2", summary["n_plus_one_count"])
	}
}

func TestRequestPerformance_SlowRequests(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStoreWithSummaries{
		summaries: []store.RequestSummaryResult{
			{
				RequestSummary: store.RequestSummary{LogID: 1, DurationMs: 2000, SQLCount: 5},
				Timestamp:      now.Add(-1 * time.Hour),
			},
			{
				RequestSummary: store.RequestSummary{LogID: 2, DurationMs: 100, SQLCount: 2},
				Timestamp:      now.Add(-2 * time.Hour),
			},
			{
				RequestSummary: store.RequestSummary{LogID: 3, DurationMs: 1500, SQLCount: 10},
				Timestamp:      now.Add(-3 * time.Hour),
			},
		},
	}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"min_duration_ms": float64(1000),
		"time_range":      "24h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	entries := resp["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("expected 2 slow entries, got %d", len(entries))
	}
}

func TestRequestPerformance_ControllerFilter(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStoreWithSummaries{
		summaries: []store.RequestSummaryResult{
			{
				RequestSummary: store.RequestSummary{LogID: 1, Controller: "UsersController", DurationMs: 100, SQLCount: 5},
				Timestamp:      now.Add(-1 * time.Hour),
			},
			{
				RequestSummary: store.RequestSummary{LogID: 2, Controller: "PostsController", DurationMs: 200, SQLCount: 8},
				Timestamp:      now.Add(-2 * time.Hour),
			},
		},
	}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"controller": "UsersController",
		"time_range": "24h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	entries := resp["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestRequestPerformance_EmptyResults(t *testing.T) {
	ls := &mockLogStoreWithSummaries{}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	if text == "" {
		t.Fatal("expected non-empty result")
	}
	// Should be a plain text hint, not JSON
	if text[0] == '{' {
		t.Error("expected plain text hint for empty results, got JSON")
	}
}

func TestRequestPerformance_SummaryStats(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStoreWithSummaries{
		summaries: []store.RequestSummaryResult{
			{
				RequestSummary: store.RequestSummary{LogID: 1, DurationMs: 100, SQLCount: 10, NPlusOne: true},
				Timestamp:      now.Add(-1 * time.Hour),
			},
			{
				RequestSummary: store.RequestSummary{LogID: 2, DurationMs: 300, SQLCount: 20, NPlusOne: false},
				Timestamp:      now.Add(-2 * time.Hour),
			},
		},
	}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"time_range": "24h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	summary := resp["summary"].(map[string]any)
	if summary["total_requests"].(float64) != 2 {
		t.Errorf("total_requests = %v, want 2", summary["total_requests"])
	}
	if summary["avg_duration"].(float64) != 200 {
		t.Errorf("avg_duration = %v, want 200", summary["avg_duration"])
	}
	if summary["avg_sql_count"].(float64) != 15 {
		t.Errorf("avg_sql_count = %v, want 15", summary["avg_sql_count"])
	}
	if summary["n_plus_one_count"].(float64) != 1 {
		t.Errorf("n_plus_one_count = %v, want 1", summary["n_plus_one_count"])
	}
}

func TestRequestPerformance_InvalidSortBy(t *testing.T) {
	ls := &mockLogStoreWithSummaries{}

	handler := requestPerformanceHandler(ls)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"sort_by": "invalid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid sort_by")
	}
}
