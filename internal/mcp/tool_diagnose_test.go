package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockLogStoreForDiagnose is a minimal LogStore mock for diagnose tests.
type mockLogStoreForDiagnose struct {
	levels    map[string]int
	summaries []store.RequestSummaryResult
}

func (m *mockLogStoreForDiagnose) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return m.levels, nil
}
func (m *mockLogStoreForDiagnose) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return m.summaries, nil
}

// Stub the rest of LogStore interface — only the methods we use.
func (m *mockLogStoreForDiagnose) BatchInsert(context.Context, []store.LogEntry) (int, error) {
	return 0, nil
}
func (m *mockLogStoreForDiagnose) Search(context.Context, store.LogSearchParams) ([]store.LogEntry, error) {
	return nil, nil
}
func (m *mockLogStoreForDiagnose) CountByService(context.Context, store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}
func (m *mockLogStoreForDiagnose) Prune(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockLogStoreForDiagnose) RecordBatch(context.Context, string, int) error { return nil }
func (m *mockLogStoreForDiagnose) GetBatch(context.Context, string) (*store.BatchRecord, error) {
	return nil, nil
}
func (m *mockLogStoreForDiagnose) PruneBatches(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockLogStoreForDiagnose) DistinctValues(context.Context, string, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *mockLogStoreForDiagnose) MetadataKeys(context.Context, store.LogCountParams) ([]string, error) {
	return nil, nil
}
func (m *mockLogStoreForDiagnose) GetByID(context.Context, int64) (*store.LogEntry, error) {
	return nil, nil
}

func (m *mockLogStoreForDiagnose) Histogram(context.Context, store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

// mockWatchStoreForDiagnose implements enough of WatchStore for diagnose.
type mockWatchStoreForDiagnose struct {
	watches       []store.Watch
	pendingAlerts int
}

func (m *mockWatchStoreForDiagnose) List(_ context.Context, params store.ListWatchParams) ([]store.Watch, error) {
	var result []store.Watch
	for _, w := range m.watches {
		if params.Status != "" && w.Status != params.Status {
			continue
		}
		result = append(result, w)
	}
	return result, nil
}
func (m *mockWatchStoreForDiagnose) CountPendingAlerts(context.Context) (int, error) {
	return m.pendingAlerts, nil
}

// Stub the rest
func (m *mockWatchStoreForDiagnose) Create(context.Context, store.CreateWatchParams) (*store.Watch, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) GetByID(context.Context, string) (*store.Watch, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) UpdateStatus(context.Context, string, store.WatchStatus) error {
	return nil
}
func (m *mockWatchStoreForDiagnose) UpdateAfterCheck(context.Context, string, float64, int, time.Time) error {
	return nil
}
func (m *mockWatchStoreForDiagnose) UpdateBaseline(context.Context, string, *store.WatchBaseline) error {
	return nil
}
func (m *mockWatchStoreForDiagnose) Delete(context.Context, string) error          { return nil }
func (m *mockWatchStoreForDiagnose) GetDueWatches(context.Context) ([]store.Watch, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) ExpireWatches(context.Context) (int, error) { return 0, nil }
func (m *mockWatchStoreForDiagnose) CreateRun(context.Context, string) (*store.WatchRun, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) CompleteRun(context.Context, string, float64, bool, string) error {
	return nil
}
func (m *mockWatchStoreForDiagnose) FailRun(context.Context, string, string) error { return nil }
func (m *mockWatchStoreForDiagnose) ListRuns(context.Context, string, int) ([]store.WatchRun, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) CreateAlert(context.Context, store.CreateWatchAlertParams) (*store.WatchAlert, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) GetAlert(context.Context, string) (*store.WatchAlert, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) ListAlerts(context.Context, string, string, int) ([]store.WatchAlert, error) {
	return nil, nil
}
func (m *mockWatchStoreForDiagnose) DismissAlert(context.Context, string, string) error { return nil }
func (m *mockWatchStoreForDiagnose) AcknowledgeAlert(context.Context, string) error { return nil }

// --- Tests ---

func TestDiagnoseHandler_AllSections(t *testing.T) {
	ls := &mockLogStoreForDiagnose{
		levels: map[string]int{"INFO": 100, "ERROR": 5, "FATAL": 1},
		summaries: []store.RequestSummaryResult{
			{RequestSummary: store.RequestSummary{Path: "/api/users", DurationMs: 250, SQLCount: 12}},
			{RequestSummary: store.RequestSummary{Path: "/api/orders", DurationMs: 150, SQLCount: 3}},
		},
	}

	egs := newMockErrorGroupStore()
	egs.groups["fp1"] = &store.ErrorGroup{
		Fingerprint:     "fp1",
		Service:         "web",
		ExceptionClass:  "Redis::TimeoutError",
		Message:         "Connection timed out",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 42,
		FirstSeenAt:     time.Now().Add(-10 * time.Minute),
		LastSeenAt:      time.Now(),
	}

	hcs := newMockHealthCheckStore()
	hcs.Create(context.Background(), store.CreateHealthCheckParams{
		Name: "API", URL: "https://api.example.com",
	})

	ws := &mockWatchStoreForDiagnose{
		watches: []store.Watch{
			{ID: "w1", Metric: store.WatchMetricErrorRate, Status: store.WatchStatusTriggered, Urgency: store.WatchUrgencyHigh},
		},
		pendingAlerts: 1,
	}

	handler := diagnoseHandler(diagnoseDeps{
		logStore:         ls,
		errorGroupStore:  egs,
		healthCheckStore: hcs,
		watchStore:       ws,
	})

	result, err := handler(context.Background(), makeRequest(map[string]any{
		"service":   "web",
		"timeframe": "1h",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}

	// Check all sections present
	for _, key := range []string{"error_summary", "log_volume", "request_performance", "watch_alerts", "healthcheck_status", "suggested_tools"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("missing section: %s", key)
		}
	}

	// Check service passed through
	if resp["service"] != "web" {
		t.Errorf("service = %v, want web", resp["service"])
	}
}

func TestDiagnoseHandler_DefaultTimeframe(t *testing.T) {
	ls := &mockLogStoreForDiagnose{
		levels: map[string]int{"INFO": 10},
	}

	handler := diagnoseHandler(diagnoseDeps{
		logStore: ls,
	})

	result, err := handler(context.Background(), makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := resultText(t, result)

	var resp map[string]any
	json.Unmarshal([]byte(text), &resp)
	if resp["timeframe"] != "1h" {
		t.Errorf("timeframe = %v, want 1h", resp["timeframe"])
	}
}

func TestDiagnoseHandler_InvalidTimeframe(t *testing.T) {
	handler := diagnoseHandler(diagnoseDeps{})
	result, err := handler(context.Background(), makeRequest(map[string]any{
		"timeframe": "xyz",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for invalid timeframe")
	}
}

func TestParseTimeframe(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		err   bool
	}{
		{"30m", 30 * time.Minute, false},
		{"1h", time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"x", 0, true},
		{"", 0, true},
		{"10z", 0, true},
	}
	for _, tt := range tests {
		got, err := parseTimeframe(tt.input)
		if tt.err && err == nil {
			t.Errorf("parseTimeframe(%q) = %v, want error", tt.input, got)
		}
		if !tt.err && err != nil {
			t.Errorf("parseTimeframe(%q) error: %v", tt.input, err)
		}
		if !tt.err && got != tt.want {
			t.Errorf("parseTimeframe(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
