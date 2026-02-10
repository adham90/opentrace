package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestComparePeriodsHandler_ErrorsIncrease(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			// Current period (last hour): 5 errors
			{Level: "error", Service: "web", Timestamp: now.Add(-30 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-25 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-20 * time.Minute)},
			{Level: "error", Service: "api", Timestamp: now.Add(-15 * time.Minute)},
			{Level: "fatal", Service: "api", Timestamp: now.Add(-10 * time.Minute)},
			// Baseline period (1-2 hours ago): 1 error
			{Level: "error", Service: "web", Timestamp: now.Add(-90 * time.Minute)},
			{Level: "info", Service: "web", Timestamp: now.Add(-80 * time.Minute)},
		},
	}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric":         "errors",
		"current_period": "last_1h",
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

	changes, ok := resp["changes"].(map[string]any)
	if !ok {
		t.Fatal("expected changes field")
	}
	if changes["direction"] != "increase" {
		t.Errorf("direction = %v, want increase", changes["direction"])
	}

	current, ok := resp["current"].(map[string]any)
	if !ok {
		t.Fatal("expected current field")
	}
	if current["total_errors"].(float64) != 5 {
		t.Errorf("current total_errors = %v, want 5", current["total_errors"])
	}
}

func TestComparePeriodsHandler_ErrorsDecrease(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			// Current period (last hour): 1 error
			{Level: "error", Service: "web", Timestamp: now.Add(-30 * time.Minute)},
			// Baseline period (1-2 hours ago): 4 errors
			{Level: "error", Service: "web", Timestamp: now.Add(-90 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-80 * time.Minute)},
			{Level: "error", Service: "api", Timestamp: now.Add(-75 * time.Minute)},
			{Level: "fatal", Service: "api", Timestamp: now.Add(-70 * time.Minute)},
		},
	}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "errors",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	changes := resp["changes"].(map[string]any)
	if changes["direction"] != "decrease" {
		t.Errorf("direction = %v, want decrease", changes["direction"])
	}
	changePct := changes["total_change_pct"].(float64)
	if changePct >= 0 {
		t.Errorf("change_pct = %v, want negative", changePct)
	}
}

func TestComparePeriodsHandler_LogVolume(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "info", Service: "web", Timestamp: now.Add(-30 * time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-25 * time.Minute)},
			{Level: "info", Service: "web", Timestamp: now.Add(-90 * time.Minute)},
		},
	}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "log_volume",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	if resp["metric"] != "log_volume" {
		t.Errorf("metric = %v, want log_volume", resp["metric"])
	}
	current := resp["current"].(map[string]any)
	if current["total"].(float64) != 2 {
		t.Errorf("current total = %v, want 2", current["total"])
	}
}

func TestComparePeriodsHandler_Alerts(t *testing.T) {
	now := time.Now().UTC()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{Severity: store.SeverityCritical, CreatedAt: now.Add(-30 * time.Minute)},
			{Severity: store.SeverityWarning, CreatedAt: now.Add(-25 * time.Minute)},
			{Severity: store.SeverityCritical, CreatedAt: now.Add(-90 * time.Minute)},
		},
	}

	handler := comparePeriodsHandler(&mockLogStore{}, as)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "alerts",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	if resp["metric"] != "alerts" {
		t.Errorf("metric = %v, want alerts", resp["metric"])
	}
}

func TestComparePeriodsHandler_BaselineZero(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			// Only entries in the current period, none in baseline.
			{Level: "error", Service: "web", Timestamp: now.Add(-30 * time.Minute)},
		},
	}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "errors",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	changes := resp["changes"].(map[string]any)
	// When baseline is zero and current is non-zero, direction should be "new".
	if changes["direction"] != "new" {
		t.Errorf("direction = %v, want new", changes["direction"])
	}
}

func TestComparePeriodsHandler_YesterdayBaseline(t *testing.T) {
	now := time.Now().UTC()
	ls := &mockLogStore{
		entries: []store.LogEntry{
			{Level: "error", Service: "web", Timestamp: now.Add(-30 * time.Minute)},
			// Yesterday same time.
			{Level: "error", Service: "web", Timestamp: now.Add(-24*time.Hour - 30*time.Minute)},
			{Level: "error", Service: "web", Timestamp: now.Add(-24*time.Hour - 20*time.Minute)},
		},
	}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric":          "errors",
		"baseline_period": "yesterday_same_time",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	changes := resp["changes"].(map[string]any)
	if changes["direction"] != "decrease" {
		t.Errorf("direction = %v, want decrease", changes["direction"])
	}
}

func TestComparePeriodsHandler_EmptyPeriods(t *testing.T) {
	ls := &mockLogStore{}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "errors",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected success, got error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)

	changes := resp["changes"].(map[string]any)
	if changes["direction"] != "unchanged" {
		t.Errorf("direction = %v, want unchanged", changes["direction"])
	}
}

func TestComparePeriodsHandler_MissingMetric(t *testing.T) {
	ls := &mockLogStore{}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing metric")
	}
}

func TestComparePeriodsHandler_InvalidMetric(t *testing.T) {
	ls := &mockLogStore{}

	handler := comparePeriodsHandler(ls, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"metric": "banana",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for invalid metric")
	}
}

func TestResolvePeriod(t *testing.T) {
	now := time.Date(2025, 2, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		period    string
		wantStart time.Time
		wantEnd   time.Time
		wantErr   bool
	}{
		{"last_1h", now.Add(-time.Hour), now, false},
		{"last_6h", now.Add(-6 * time.Hour), now, false},
		{"last_24h", now.Add(-24 * time.Hour), now, false},
		{"today", time.Date(2025, 2, 10, 0, 0, 0, 0, time.UTC), now, false},
		{"banana", time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		start, end, err := resolvePeriod(tt.period, now)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolvePeriod(%q) error = %v, wantErr %v", tt.period, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && !start.Equal(tt.wantStart) {
			t.Errorf("resolvePeriod(%q) start = %v, want %v", tt.period, start, tt.wantStart)
		}
		if !tt.wantErr && !end.Equal(tt.wantEnd) {
			t.Errorf("resolvePeriod(%q) end = %v, want %v", tt.period, end, tt.wantEnd)
		}
	}
}

func TestCalcChange(t *testing.T) {
	tests := []struct {
		baseline  int
		current   int
		wantPct   float64
		wantDir   string
	}{
		{100, 200, 100, "increase"},
		{200, 100, -50, "decrease"},
		{100, 100, 0, "unchanged"},
		{0, 100, 100, "new"},
		{0, 0, 0, "unchanged"},
	}
	for _, tt := range tests {
		pct, dir := calcChange(tt.baseline, tt.current)
		if pct != tt.wantPct {
			t.Errorf("calcChange(%d, %d) pct = %v, want %v", tt.baseline, tt.current, pct, tt.wantPct)
		}
		if dir != tt.wantDir {
			t.Errorf("calcChange(%d, %d) dir = %v, want %v", tt.baseline, tt.current, dir, tt.wantDir)
		}
	}
}
