package watcher

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
	"github.com/google/uuid"
)

func TestCompareValues(t *testing.T) {
	tests := []struct {
		measured  float64
		op        store.RuleOperator
		threshold float64
		want      bool
	}{
		{10, store.OpGreaterThan, 5, true},
		{5, store.OpGreaterThan, 5, false},
		{5, store.OpGreaterThan, 10, false},
		{5, store.OpGreaterThanEqual, 5, true},
		{4, store.OpGreaterThanEqual, 5, false},
		{3, store.OpLessThan, 5, true},
		{5, store.OpLessThan, 5, false},
		{5, store.OpLessThanEqual, 5, true},
		{6, store.OpLessThanEqual, 5, false},
		{5, store.OpEqual, 5, true},
		{6, store.OpEqual, 5, false},
		{6, store.OpNotEqual, 5, true},
		{5, store.OpNotEqual, 5, false},
		{5, store.RuleOperator("unknown"), 5, false},
	}
	for _, tt := range tests {
		got := compareValues(tt.measured, tt.op, tt.threshold)
		if got != tt.want {
			t.Errorf("compareValues(%v, %q, %v) = %v, want %v", tt.measured, tt.op, tt.threshold, got, tt.want)
		}
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		input any
		want  float64
		err   bool
	}{
		{int(42), 42, false},
		{int16(42), 42, false},
		{int32(42), 42, false},
		{int64(42), 42, false},
		{float32(3.14), 3.140000104904175, false}, // float32 imprecision
		{float64(3.14), 3.14, false},
		{"42.5", 42.5, false},
		{"not a number", 0, true},
		{true, 0, true},
	}
	for _, tt := range tests {
		got, err := toFloat64(tt.input)
		if (err != nil) != tt.err {
			t.Errorf("toFloat64(%v) error = %v, want error=%v", tt.input, err, tt.err)
			continue
		}
		if err == nil && got != tt.want {
			t.Errorf("toFloat64(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestOperatorString(t *testing.T) {
	tests := []struct {
		op   store.RuleOperator
		want string
	}{
		{store.OpGreaterThan, ">"},
		{store.OpGreaterThanEqual, ">="},
		{store.OpLessThan, "<"},
		{store.OpLessThanEqual, "<="},
		{store.OpEqual, "="},
		{store.OpNotEqual, "!="},
		{store.RuleOperator("custom"), "custom"},
	}
	for _, tt := range tests {
		got := operatorString(tt.op)
		if got != tt.want {
			t.Errorf("operatorString(%q) = %q, want %q", tt.op, got, tt.want)
		}
	}
}

// mockLogStore implements store.LogStore for testing.
type mockLogStore struct {
	entries []store.LogEntry
}

func (m *mockLogStore) BatchInsert(_ context.Context, entries []store.LogEntry) (int, error) {
	m.entries = append(m.entries, entries...)
	return len(entries), nil
}

func (m *mockLogStore) Search(_ context.Context, _ store.LogSearchParams) ([]store.LogEntry, error) {
	return m.entries, nil
}

func (m *mockLogStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockLogStore) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return nil, nil
}

func (m *mockLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return nil, nil
}

func (m *mockLogStore) DistinctValues(_ context.Context, _ string, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) MetadataKeys(_ context.Context, _ store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (m *mockLogStore) GetByID(_ context.Context, _ int64) (*store.LogEntry, error) {
	return nil, store.ErrNotFound
}

func (m *mockLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return nil, nil
}
func (m *mockLogStore) RecordBatch(_ context.Context, _ string, _ int) error { return nil }
func (m *mockLogStore) GetBatch(_ context.Context, _ string) (*store.BatchRecord, error) { return nil, nil }
func (m *mockLogStore) PruneBatches(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// mockDSStore implements store.DataSourceStore for testing.
type mockDSStore struct{}

func (m *mockDSStore) Create(_ context.Context, _ store.CreateDataSourceParams) (*store.DataSource, error) {
	return nil, nil
}
func (m *mockDSStore) GetByID(_ context.Context, _ uuid.UUID) (*store.DataSource, error) {
	return nil, nil
}
func (m *mockDSStore) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	return nil, nil
}
func (m *mockDSStore) Update(_ context.Context, _ uuid.UUID, _ store.UpdateDataSourceParams) (*store.DataSource, error) {
	return nil, nil
}
func (m *mockDSStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }

func TestRuleEvaluator_EvaluateLogs(t *testing.T) {
	logs := &mockLogStore{
		entries: []store.LogEntry{
			{ID: 1, Level: "ERROR", Service: "payments", Message: "connection refused"},
			{ID: 2, Level: "ERROR", Service: "payments", Message: "connection refused"},
			{ID: 3, Level: "ERROR", Service: "payments", Message: "connection refused"},
		},
	}

	re := NewRuleEvaluator(connector.NewRegistry(), logs, &mockDSStore{})

	// 3 logs, threshold > 5 → no alert
	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeRule,
		RuleConfig: &store.RuleConfig{
			Source:    store.RuleSourceLogs,
			Metric:    "count",
			Operator:  store.OpGreaterThan,
			Threshold: 5,
			Filter: &store.LogFilter{
				Service: "payments",
				Level:   "ERROR",
			},
			TimeWindow: "5m",
		},
	}

	result, err := re.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if result.HasAlert {
		t.Error("expected no alert (3 < 5)")
	}
	if result.Value == nil || *result.Value != 3 {
		t.Errorf("value = %v, want 3", result.Value)
	}

	// 3 logs, threshold > 2 → alert
	w.RuleConfig.Threshold = 2
	result, err = re.Evaluate(context.Background(), w)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.HasAlert {
		t.Error("expected alert (3 > 2)")
	}
}

func TestRuleEvaluator_NilRuleConfig(t *testing.T) {
	re := NewRuleEvaluator(connector.NewRegistry(), &mockLogStore{}, &mockDSStore{})

	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeRule,
	}

	_, err := re.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for nil rule_config")
	}
}

func TestRuleEvaluator_UnknownSource(t *testing.T) {
	re := NewRuleEvaluator(connector.NewRegistry(), &mockLogStore{}, &mockDSStore{})

	w := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeRule,
		RuleConfig: &store.RuleConfig{
			Source: store.RuleSource("unknown"),
		},
	}

	_, err := re.Evaluate(context.Background(), w)
	if err == nil {
		t.Error("expected error for unknown source")
	}
}

func TestFormatQuerySummary(t *testing.T) {
	cfg := &store.RuleConfig{
		Metric:    "value",
		Operator:  store.OpGreaterThan,
		Threshold: 80,
	}

	s := formatQuerySummary(cfg, 42, false)
	if s != "OK: value is 42 (threshold: > 80)" {
		t.Errorf("formatQuerySummary = %q", s)
	}

	s = formatQuerySummary(cfg, 100, true)
	if s != "ALERT: value is 100 (> 80)" {
		t.Errorf("formatQuerySummary alert = %q", s)
	}
}
