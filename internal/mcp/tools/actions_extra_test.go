package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
	"github.com/google/uuid"
)

// ===========================================================================
// LogsTrace — additional action-branch tests
// ===========================================================================

func TestLogsTrace_EmptyTraceID(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "trace", "trace_id": ""}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true when trace_id is empty string")
	}
}

func TestLogsTrace_WithEntries(t *testing.T) {
	ls := mocks.NewLogStore()
	now := time.Now().UTC()
	ls.Entries = []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "info", Service: "api", Message: "request started", TraceID: "trace-1"},
		{ID: 2, Timestamp: now.Add(50 * time.Millisecond), Level: "info", Service: "db", Message: "query executed", TraceID: "trace-1"},
		{ID: 3, Timestamp: now.Add(100 * time.Millisecond), Level: "error", Service: "api", Message: "request failed", TraceID: "trace-1"},
	}

	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "trace", "trace_id": "trace-1"}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}

	t.Run("total entries", func(t *testing.T) {
		if total, ok := resp["total_entries"].(float64); !ok || int(total) != 3 {
			t.Errorf("total_entries = %v, want 3", resp["total_entries"])
		}
	})

	t.Run("has errors", func(t *testing.T) {
		if hasErrors, ok := resp["has_errors"].(bool); !ok || !hasErrors {
			t.Error("expected has_errors to be true")
		}
	})

	t.Run("timeline populated", func(t *testing.T) {
		timeline, ok := resp["timeline"].([]any)
		if !ok {
			t.Fatal("expected timeline array")
		}
		if len(timeline) != 3 {
			t.Errorf("timeline length = %d, want 3", len(timeline))
		}
	})

	t.Run("service summary present", func(t *testing.T) {
		if _, ok := resp["service_summary"]; !ok {
			t.Error("expected 'service_summary' field in response")
		}
	})

	t.Run("warnings present for errors", func(t *testing.T) {
		if _, ok := resp["warnings"]; !ok {
			t.Error("expected 'warnings' field when trace has errors")
		}
	})
}

func TestLogsTrace_StoreError(t *testing.T) {
	ls := mocks.NewLogStore()
	ls.Err = errors.New("database down")

	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "trace", "trace_id": "trace-1"}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true when store returns error")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "failed to search logs") {
		t.Errorf("expected store error message, got %q", text)
	}
}

func TestLogsTrace_WithIncludeContext(t *testing.T) {
	ls := mocks.NewLogStore()
	now := time.Now().UTC()
	ls.Entries = []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "info", Service: "api", Message: "started", TraceID: "trace-ctx"},
		{ID: 2, Timestamp: now.Add(10 * time.Millisecond), Level: "info", Service: "api", Message: "finished", TraceID: "trace-ctx"},
	}

	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "trace", "trace_id": "trace-ctx", "include_context": true}
	result, err := LogsTrace(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	// context_entries key should be present when include_context is true.
	if _, ok := resp["context_entries"]; !ok {
		t.Error("expected 'context_entries' field in response when include_context=true")
	}
}


// ===========================================================================
// LogsCompare — additional action-branch tests
// ===========================================================================

func TestLogsCompare_MissingMetric(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "compare"}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true when metric is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "metric is required") {
		t.Errorf("expected 'metric is required' error, got %q", text)
	}
}

func TestLogsCompare_InvalidMetric(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "compare", "metric": "invalid_metric"}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for invalid metric")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid metric") {
		t.Errorf("expected 'invalid metric' error, got %q", text)
	}
}

func TestLogsCompare_InvalidBaselinePeriod(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{
		"action":          "compare",
		"metric":          "errors",
		"current_period":  "last_1h",
		"baseline_period": "bogus_baseline",
	}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for invalid baseline_period")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid baseline_period") {
		t.Errorf("expected 'invalid baseline_period' error, got %q", text)
	}
}

func TestLogsCompareLogVolume_HappyPath(t *testing.T) {
	ls := mocks.NewLogStore()
	deps := LogsDeps{
		LogStore:        ls,
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{
		"action":         "compare",
		"metric":         "log_volume",
		"current_period": "last_1h",
	}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("unexpected IsError: %s", text)
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if resp["metric"] != "log_volume" {
		t.Errorf("metric = %v, want %q", resp["metric"], "log_volume")
	}
	if _, ok := resp["current"]; !ok {
		t.Error("expected 'current' field in response")
	}
	if _, ok := resp["baseline"]; !ok {
		t.Error("expected 'baseline' field in response")
	}
	if _, ok := resp["changes"]; !ok {
		t.Error("expected 'changes' field in response")
	}
}

func TestLogsCompare_WithServiceFilter(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{
		"action":  "compare",
		"metric":  "errors",
		"service": "payments",
	}
	result, err := LogsCompare(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("unexpected IsError: %s", text)
	}
}

// ===========================================================================
// LogsPerformance — additional action-branch tests
// ===========================================================================

func TestLogsPerformance_InvalidTimeRange(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "performance", "time_range": "invalid"}
	result, err := LogsPerformance(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for invalid time_range")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid time_range") {
		t.Errorf("expected 'invalid time_range' error, got %q", text)
	}
}

func TestLogsPerformance_InvalidSortBy(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "performance", "sort_by": "nonexistent_field"}
	result, err := LogsPerformance(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError to be true for invalid sort_by")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid sort_by") {
		t.Errorf("expected 'invalid sort_by' error, got %q", text)
	}
}

func TestLogsPerformance_NPlusOneHint(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	args := map[string]any{"action": "performance", "n_plus_one_only": true}
	result, err := LogsPerformance(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "N+1") {
		t.Errorf("expected N+1 hint in response, got %q", text)
	}
}

func TestLogsPerformance_ValidSortByOptions(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	for _, sortBy := range []string{"duration_ms", "sql_count", "db_time_ms", "duplicate_queries"} {
		t.Run(sortBy, func(t *testing.T) {
			args := map[string]any{"action": "performance", "sort_by": sortBy}
			result, err := LogsPerformance(context.Background(), args, deps)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				text := extractText(t, result)
				t.Errorf("unexpected IsError for sort_by=%q: %s", sortBy, text)
			}
		})
	}
}

func TestLogsPerformance_CustomLimit(t *testing.T) {
	deps := LogsDeps{
		LogStore:        mocks.NewLogStore(),
		ErrorGroupStore: mocks.NewErrorGroupStore(),
	}

	// Limit should be capped at 100.
	args := map[string]any{"action": "performance", "limit": float64(500)}
	result, err := LogsPerformance(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("unexpected IsError: %s", text)
	}
}

// ===========================================================================
// HandleConnectorTest — action-branch tests
// ===========================================================================

func TestHandleConnectorTest_NilDSStore(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: nil,
	}

	args := map[string]any{"connector_id": "some-id"}
	result, err := HandleConnectorTest(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when DSStore is nil")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "DataSourceStore not configured") {
		t.Errorf("expected 'DataSourceStore not configured' error, got %q", text)
	}
}

func TestHandleConnectorTest_MissingID(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{}
	result, err := HandleConnectorTest(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when connector_id is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "connector_id is required") {
		t.Errorf("expected 'connector_id is required' error, got %q", text)
	}
}

func TestHandleConnectorTest_InvalidUUID(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{"connector_id": "not-a-uuid"}
	result, err := HandleConnectorTest(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for invalid UUID format")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "invalid connector_id format") {
		t.Errorf("expected 'invalid connector_id format' error, got %q", text)
	}
}

func TestHandleConnectorTest_NotFound(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{"connector_id": uuid.New().String()}
	result, err := HandleConnectorTest(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when connector not found")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' error, got %q", text)
	}
}

// ===========================================================================
// HandleConnectorUpdate — action-branch tests
// ===========================================================================

func TestHandleConnectorUpdate_NilDSStore(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: nil,
	}

	args := map[string]any{"connector_id": "some-id"}
	result, err := HandleConnectorUpdate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when DSStore is nil")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "DataSourceStore not configured") {
		t.Errorf("expected 'DataSourceStore not configured' error, got %q", text)
	}
}

func TestHandleConnectorUpdate_MissingID(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{}
	result, err := HandleConnectorUpdate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when connector_id is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "connector_id is required") {
		t.Errorf("expected 'connector_id is required' error, got %q", text)
	}
}

func TestHandleConnectorUpdate_InvalidUUID(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{"connector_id": "not-a-valid-uuid"}
	result, err := HandleConnectorUpdate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError for invalid UUID format")
	}
}

func TestHandleConnectorUpdate_NotFound(t *testing.T) {
	d := ConnectorsDeps{
		DSStore: mocks.NewDataSourceStore(),
	}

	args := map[string]any{"connector_id": uuid.New().String()}
	result, err := HandleConnectorUpdate(context.Background(), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when connector not found")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' error, got %q", text)
	}
}

func TestHandleConnectorUpdate_HappyPath(t *testing.T) {
	dsStore := mocks.NewDataSourceStore()
	ctx := context.Background()

	created, err := dsStore.Create(ctx, store.CreateDataSourceParams{
		Type:   "database",
		Name:   "my-db",
		Config: map[string]any{"connection_string": "postgres://localhost/test"},
	})
	if err != nil {
		t.Fatalf("failed to create test connector: %v", err)
	}

	d := ConnectorsDeps{
		DSStore:  dsStore,
		Registry: connector.NewRegistry(),
	}

	args := map[string]any{
		"connector_id": created.ID.String(),
		"name":         "renamed-db",
	}
	result, err := HandleConnectorUpdate(ctx, d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("unexpected IsError: %s", text)
	}
	text := extractText(t, result)
	if !strings.Contains(text, "updated successfully") {
		t.Errorf("expected 'updated successfully' message, got %q", text)
	}
}

func TestHandleConnectorUpdate_ConnectionStringChanged(t *testing.T) {
	dsStore := mocks.NewDataSourceStore()
	ctx := context.Background()

	created, err := dsStore.Create(ctx, store.CreateDataSourceParams{
		Type:   "database",
		Name:   "my-db",
		Config: map[string]any{"connection_string": "postgres://localhost/old"},
	})
	if err != nil {
		t.Fatalf("failed to create test connector: %v", err)
	}

	d := ConnectorsDeps{
		DSStore:  dsStore,
		Registry: connector.NewRegistry(),
	}

	args := map[string]any{
		"connector_id":      created.ID.String(),
		"connection_string": "postgres://localhost/new",
	}
	result, err := HandleConnectorUpdate(ctx, d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		text := extractText(t, result)
		t.Fatalf("unexpected IsError: %s", text)
	}
	text := extractText(t, result)
	if !strings.Contains(text, "action=test") {
		t.Errorf("expected hint to re-test connection, got %q", text)
	}
}

// ===========================================================================
// HandleWatchInvestigate — action-branch tests
// ===========================================================================

func TestHandleWatchInvestigate_WithAlertID_NotFound(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	// The default mock GetAlert returns ErrNotFound, which wraps into a Go error.
	args := map[string]any{"action": "investigate", "alert_id": "alert-123"}
	_, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err == nil {
		t.Fatal("expected error when alert not found, got nil")
	}
	if !strings.Contains(err.Error(), "getting alert") {
		t.Errorf("expected 'getting alert' in error, got %q", err.Error())
	}
}

func TestHandleWatchInvestigate_MissingBothParams(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "investigate"}
	_, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err == nil {
		t.Fatal("expected error when both alert_id and service are missing")
	}
	if !strings.Contains(err.Error(), "either alert_id or service is required") {
		t.Errorf("expected 'either alert_id or service is required' error, got %q", err.Error())
	}
}

func TestHandleWatchInvestigate_ByService(t *testing.T) {
	ls := mocks.NewLogStore()
	now := time.Now().UTC()
	ls.Entries = []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "error", Service: "payments", Message: "charge failed", ExceptionClass: "PaymentError"},
		{ID: 2, Timestamp: now.Add(-time.Minute), Level: "info", Service: "payments", Message: "processing charge"},
	}

	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   ls,
	}

	args := map[string]any{"action": "investigate", "service": "payments", "window": "30m"}
	result, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	inv, ok := resp["investigation"].(map[string]any)
	if !ok {
		t.Fatal("expected 'investigation' field in response")
	}
	if inv["service"] != "payments" {
		t.Errorf("service = %v, want %q", inv["service"], "payments")
	}
	if inv["window"] != "30m" {
		t.Errorf("window = %v, want %q", inv["window"], "30m")
	}

	// Verify recent errors are populated.
	recentErrors, ok := inv["recent_errors"].([]any)
	if !ok {
		t.Fatal("expected 'recent_errors' in investigation")
	}
	if len(recentErrors) == 0 {
		t.Error("expected at least one recent error entry")
	}
}

func TestHandleWatchInvestigate_ByServiceDefaultWindow(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	args := map[string]any{"action": "investigate", "service": "api"}
	result, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false")
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	inv := resp["investigation"].(map[string]any)
	if inv["window"] != "1h" {
		t.Errorf("default window = %v, want %q", inv["window"], "1h")
	}
}

func TestHandleWatchInvestigate_NilLogStore(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   nil,
	}

	args := map[string]any{"action": "investigate", "service": "api"}
	result, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false even with nil LogStore")
	}

	text := extractText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	if _, ok := resp["investigation"]; !ok {
		t.Error("expected 'investigation' field in response")
	}
}

func TestHandleWatchInvestigate_InvalidWindowFallback(t *testing.T) {
	deps := WatchesDeps{
		WatchStore: mocks.NewWatchStore(),
		LogStore:   mocks.NewLogStore(),
	}

	// An invalid duration string should fall back to 1h.
	args := map[string]any{"action": "investigate", "service": "api", "window": "not-a-duration"}
	result, err := HandleWatchInvestigate(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("expected IsError to be false with fallback window")
	}
}

// ===========================================================================
// HandleKillQuery — action-branch tests
// ===========================================================================

func TestHandleKillQuery_MissingPID(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	args := map[string]any{}
	result, err := HandleKillQuery(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when pid is missing")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "pid is required") {
		t.Errorf("expected 'pid is required' error, got %q", text)
	}
}

func TestHandleKillQuery_InvalidPID(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	t.Run("zero pid", func(t *testing.T) {
		args := map[string]any{"pid": float64(0)}
		result, err := HandleKillQuery(context.Background(), deps, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError for zero PID")
		}
	})

	t.Run("negative pid", func(t *testing.T) {
		args := map[string]any{"pid": float64(-5)}
		result, err := HandleKillQuery(context.Background(), deps, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError for negative PID")
		}
	})

	t.Run("non-numeric pid", func(t *testing.T) {
		args := map[string]any{"pid": "string-pid"}
		result, err := HandleKillQuery(context.Background(), deps, args)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected IsError for non-numeric PID")
		}
	})
}

func TestHandleKillQuery_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	args := map[string]any{"pid": float64(12345)}
	result, err := HandleKillQuery(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' error, got %q", text)
	}
}

func TestHandleKillQuery_WithForceFlag(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	// Even with force=true, should still fail with no connector.
	args := map[string]any{"pid": float64(12345), "force": true}
	result, err := HandleKillQuery(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
}

// ===========================================================================
// HandleLongTransactions — action-branch tests
// ===========================================================================

func TestHandleLongTransactions_NoConnector(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	args := map[string]any{}
	result, err := HandleLongTransactions(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
	text := extractText(t, result)
	if !strings.Contains(text, "No database connector") {
		t.Errorf("expected 'No database connector' error, got %q", text)
	}
}

func TestHandleLongTransactions_DefaultMinDuration(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	// No min_duration_seconds provided; should use default of 30s.
	// Still fails because no connector, but validates arg parsing.
	args := map[string]any{}
	result, err := HandleLongTransactions(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
}

func TestHandleLongTransactions_WithMinDuration(t *testing.T) {
	deps := DatabaseDeps{
		Registry: connector.NewRegistry(),
	}

	// With no connector, this still validates the argument parsing before failing.
	args := map[string]any{"min_duration_seconds": float64(120)}
	result, err := HandleLongTransactions(context.Background(), deps, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError when no database connector is registered")
	}
}
