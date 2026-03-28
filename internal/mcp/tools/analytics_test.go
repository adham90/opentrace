package tools

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
)

func TestAnalyticsHandler_UnknownAction(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}
	handler := AnalyticsHandler(deps)

	req := MakeCallToolRequest("analytics", map[string]any{"action": "nonexistent"})
	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError to be true for unknown action")
	}
}

func TestHandleTraffic_Empty(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}

	args := map[string]any{"action": "traffic"}
	result, err := HandleTraffic(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleEndpoints_Empty(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}

	args := map[string]any{"action": "endpoints"}
	result, err := HandleEndpoints(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleHeatmap_Empty(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}

	args := map[string]any{"action": "heatmap"}
	result, err := HandleHeatmap(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleTrends_Empty(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}

	args := map[string]any{"action": "trends"}
	result, err := HandleTrends(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}

func TestHandleMovers_Empty(t *testing.T) {
	deps := AnalyticsDeps{
		AnalyticsStore: mocks.NewAnalyticsStore(),
		TrendStore:     mocks.NewTrendStore(),
	}

	args := map[string]any{"action": "movers"}
	result, err := HandleMovers(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Errorf("expected IsError to be false, got true: %s", extractText(t, result))
	}
	text := extractText(t, result)
	if text == "" {
		t.Error("expected non-empty text response")
	}
}
