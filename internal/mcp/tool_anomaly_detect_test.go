package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestAnomalyDetectHandler_MissingMetric(t *testing.T) {
	handler := anomalyDetectHandler(nil, nil)

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing metric")
	}
	text := resultText(t, result)
	if !contains(text, "metric is required") {
		t.Errorf("expected metric-required message, got: %s", text)
	}
}

func TestAnomalyDetectHandler_InvalidWindow(t *testing.T) {
	ls := &mockLogStore{}
	handler := anomalyDetectHandler(ls, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "log_volume",
		"window": "invalid",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid window")
	}
}

func TestAnomalyDetectHandler_LogVolume_Normal(t *testing.T) {
	// Create a log store with consistent entries across time periods.
	now := time.Now().UTC()
	entries := make([]store.LogEntry, 0)
	// Generate 80 entries (8 windows x ~10 entries each) spread over 8 hours.
	for i := 0; i < 80; i++ {
		entries = append(entries, store.LogEntry{
			Level:     "info",
			Message:   "test log",
			Timestamp: now.Add(-time.Duration(i*6) * time.Minute),
		})
	}

	ls := &mockLogStore{entries: entries}
	handler := anomalyDetectHandler(ls, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "log_volume",
		"window": "1h",
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
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["metric"] != "log_volume" {
		t.Errorf("metric = %v, want log_volume", resp["metric"])
	}

	// With consistent data, should not be an anomaly.
	baseline, ok := resp["baseline"].(map[string]any)
	if !ok {
		t.Fatal("expected baseline map")
	}
	if baseline["samples"] == float64(0) {
		t.Error("expected some baseline samples")
	}
}

func TestAnomalyDetectHandler_NoStore(t *testing.T) {
	handler := anomalyDetectHandler(nil, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "error_rate",
		"window": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when log store is nil for error_rate")
	}
}

func TestAnomalyDetectHandler_AlertCount(t *testing.T) {
	as := &mockAlertStore{}
	handler := anomalyDetectHandler(nil, as)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "alert_count",
		"window": "1h",
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
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if resp["metric"] != "alert_count" {
		t.Errorf("metric = %v, want alert_count", resp["metric"])
	}
}

func TestMeanStdDev_Empty(t *testing.T) {
	mean, stddev := meanStdDev(nil)
	if mean != 0 || stddev != 0 {
		t.Errorf("expected (0, 0), got (%f, %f)", mean, stddev)
	}
}

func TestMeanStdDev_Single(t *testing.T) {
	mean, stddev := meanStdDev([]float64{42})
	if mean != 42 || stddev != 0 {
		t.Errorf("expected (42, 0), got (%f, %f)", mean, stddev)
	}
}

func TestMeanStdDev_Multiple(t *testing.T) {
	mean, stddev := meanStdDev([]float64{10, 10, 10, 10})
	if mean != 10 {
		t.Errorf("expected mean=10, got %f", mean)
	}
	if stddev != 0 {
		t.Errorf("expected stddev=0, got %f", stddev)
	}
}

func TestAnomalySeverity(t *testing.T) {
	tests := []struct {
		absZ     float64
		expected string
	}{
		{1.5, "low"},
		{2.5, "medium"},
		{3.5, "high"},
		{4.5, "critical"},
	}
	for _, tc := range tests {
		got := anomalySeverity(tc.absZ)
		if got != tc.expected {
			t.Errorf("anomalySeverity(%.1f) = %q, want %q", tc.absZ, got, tc.expected)
		}
	}
}

func TestParseDurationLoose(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
		err      bool
	}{
		{"15m", 15 * time.Minute, false},
		{"1h", 1 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"x", 0, true},
		{"", 0, true},
		{"1s", 0, true}, // unknown unit
	}
	for _, tc := range tests {
		got, err := parseDurationLoose(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("parseDurationLoose(%q) expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDurationLoose(%q) unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.expected {
			t.Errorf("parseDurationLoose(%q) = %v, want %v", tc.input, got, tc.expected)
		}
	}
}
