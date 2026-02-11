package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

func TestAlertDetailsHandler_Success(t *testing.T) {
	watcherID := uuid.New()
	runID := uuid.New()
	alertID := uuid.New()
	now := time.Now().UTC()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{
				ID: alertID, WatcherID: &watcherID, RunID: &runID,
				WatcherTitle: "Connection Watcher",
				Title: "High connections", Summary: "92/100 connections",
				Severity: store.SeverityCritical, CreatedAt: now,
			},
		},
	}
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: watcherID, Title: "Connection Watcher", WatcherType: store.WatcherTypeRule, Status: store.WatcherActive, TimeRange: "5m"},
		},
	}
	finished := now.Add(2 * time.Second)
	rs := &mockWatcherRunStore{
		runs: []store.WatcherRun{
			{ID: runID, WatcherID: watcherID, StartedAt: now.Add(-2 * time.Second), FinishedAt: &finished, Status: "completed", HasAlert: true},
		},
	}

	handler := alertDetailsHandler(as, ws, rs)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
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

	alert, ok := resp["alert"].(map[string]any)
	if !ok {
		t.Fatal("expected alert field")
	}
	if alert["severity"] != "critical" {
		t.Errorf("severity = %v, want critical", alert["severity"])
	}

	if _, ok := resp["watcher"]; !ok {
		t.Error("expected watcher field")
	}

	if _, ok := resp["triggering_run"]; !ok {
		t.Error("expected triggering_run field")
	}
}

func TestAlertDetailsHandler_NotFound(t *testing.T) {
	as := &mockAlertStore{}
	handler := alertDetailsHandler(as, nil, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": uuid.New().String(),
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for non-existent alert")
	}
}

func TestAlertDetailsHandler_MissingAlertID(t *testing.T) {
	as := &mockAlertStore{}
	handler := alertDetailsHandler(as, nil, nil)

	result, err := handler(context.Background(), makeRequest(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for missing alert_id")
	}
}

func TestAlertDetailsHandler_WithCorrelatedAlerts(t *testing.T) {
	watcherID := uuid.New()
	alertID := uuid.New()
	now := time.Now().UTC()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{
				ID: alertID, WatcherID: &watcherID,
				Title: "Alert 1", Summary: "First alert",
				Severity: store.SeverityCritical, CreatedAt: now,
			},
			{
				ID: uuid.New(), WatcherID: &watcherID,
				WatcherTitle: "Other Watcher",
				Title: "Alert 2", Summary: "Correlated alert",
				Severity: store.SeverityWarning, CreatedAt: now.Add(-2 * time.Minute),
			},
			{
				ID: uuid.New(), WatcherID: &watcherID,
				Title: "Alert 3", Summary: "Too far away",
				Severity: store.SeverityInfo, CreatedAt: now.Add(-10 * time.Minute),
			},
		},
	}

	handler := alertDetailsHandler(as, nil, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id": alertID.String(),
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

	correlated, ok := resp["correlated_alerts"].([]any)
	if !ok {
		t.Fatal("expected correlated_alerts field")
	}
	// Alert 2 is within 5 min, Alert 3 is not.
	if len(correlated) != 1 {
		t.Errorf("expected 1 correlated alert, got %d", len(correlated))
	}
}

func TestAlertDetailsHandler_NoCorrelated(t *testing.T) {
	alertID := uuid.New()
	now := time.Now().UTC()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{
				ID: alertID, Title: "Alert 1", Summary: "Only alert",
				Severity: store.SeverityWarning, CreatedAt: now,
			},
		},
	}

	handler := alertDetailsHandler(as, nil, nil)
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"alert_id":           alertID.String(),
		"include_correlated": false,
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

	if _, ok := resp["correlated_alerts"]; ok {
		t.Error("expected no correlated_alerts when include_correlated=false")
	}
}
