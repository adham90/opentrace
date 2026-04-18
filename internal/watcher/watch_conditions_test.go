package watcher

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestParseCondition_SimpleThreshold(t *testing.T) {
	raw := json.RawMessage(`{"type":"threshold","metric":"error_rate","op":"gt","value":0.05,"service":"api"}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Type != "threshold" {
		t.Errorf("type = %q, want threshold", c.Type)
	}
	if c.Metric != store.WatchMetricErrorRate {
		t.Errorf("metric = %q, want error_rate", c.Metric)
	}
	if c.Value != 0.05 {
		t.Errorf("value = %f, want 0.05", c.Value)
	}
}

func TestParseCondition_CompoundAll(t *testing.T) {
	raw := json.RawMessage(`{
		"all": [
			{"type":"threshold","metric":"error_rate","op":"gt","value":0.05},
			{"type":"threshold","metric":"response_time","op":"gt","value":500}
		]
	}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.All) != 2 {
		t.Fatalf("all has %d conditions, want 2", len(c.All))
	}
	if c.All[0].Metric != store.WatchMetricErrorRate {
		t.Errorf("all[0].metric = %q, want error_rate", c.All[0].Metric)
	}
	if c.All[1].Metric != store.WatchMetricResponseTime {
		t.Errorf("all[1].metric = %q, want response_time", c.All[1].Metric)
	}
}

func TestParseCondition_CompoundAny(t *testing.T) {
	raw := json.RawMessage(`{
		"any": [
			{"type":"threshold","metric":"error_rate","op":"gt","value":0.05},
			{"type":"threshold","metric":"error_count","op":"gt","value":100}
		]
	}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.Any) != 2 {
		t.Fatalf("any has %d conditions, want 2", len(c.Any))
	}
}

func TestParseCondition_NestedAllAny(t *testing.T) {
	raw := json.RawMessage(`{
		"any": [
			{
				"all": [
					{"type":"threshold","metric":"error_rate","op":"gt","value":0.05},
					{"type":"threshold","metric":"response_time","op":"gt","value":500}
				]
			},
			{"type":"threshold","metric":"error_count","op":"gt","value":100}
		]
	}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(c.Any) != 2 {
		t.Fatalf("any has %d conditions, want 2", len(c.Any))
	}
	if len(c.Any[0].All) != 2 {
		t.Fatalf("any[0].all has %d conditions, want 2", len(c.Any[0].All))
	}
}

func TestParseCondition_Not(t *testing.T) {
	raw := json.RawMessage(`{
		"not": {"type":"threshold","metric":"heartbeat","op":"lt","value":60}
	}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Not == nil {
		t.Fatal("expected not condition")
	}
	if c.Not.Type != "threshold" {
		t.Errorf("not.type = %q, want threshold", c.Not.Type)
	}
}

func TestParseCondition_Relative(t *testing.T) {
	raw := json.RawMessage(`{"type":"relative","metric":"error_rate","op":"gt","baseline_multiple":2.0,"service":"api"}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Type != "relative" {
		t.Errorf("type = %q, want relative", c.Type)
	}
	if c.BaselineMultiple != 2.0 {
		t.Errorf("baseline_multiple = %f, want 2.0", c.BaselineMultiple)
	}
}

func TestParseCondition_Delta(t *testing.T) {
	raw := json.RawMessage(`{"type":"delta","metric":"error_rate","op":"gt","change_pct":50,"compare_window":"1h"}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Type != "delta" {
		t.Errorf("type = %q, want delta", c.Type)
	}
	if c.ChangePct != 50 {
		t.Errorf("change_pct = %f, want 50", c.ChangePct)
	}
}

func TestParseCondition_Count(t *testing.T) {
	raw := json.RawMessage(`{"type":"count","field":"error_fingerprint","distinct":true,"op":"gt","value":10,"window":"1h","service":"api"}`)
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Type != "count" {
		t.Errorf("type = %q, want count", c.Type)
	}
	if !c.Distinct {
		t.Error("expected distinct=true")
	}
	if c.Field != "error_fingerprint" {
		t.Errorf("field = %q, want error_fingerprint", c.Field)
	}
}

func TestEvaluateCondition_SimpleThreshold(t *testing.T) {
	// Create a mock metrics that always returns 0.1 for error_rate.
	ls := &mockLogStoreForMetrics{
		levelCounts: map[string]int{"error": 10, "info": 90},
	}
	metrics := NewWatchMetrics(ls)

	c := &Condition{
		Type:    "threshold",
		Metric:  store.WatchMetricErrorRate,
		Op:      store.WatchOpGreaterThan,
		Value:   0.05,
		Service: "api",
	}

	result, err := EvaluateCondition(context.Background(), c, metrics, nil, "", 30*1e9)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !result.Breached {
		t.Error("expected breach: error_rate 0.1 > 0.05")
	}
}

func TestEvaluateCondition_AllRequiresBothTrue(t *testing.T) {
	ls := &mockLogStoreForMetrics{
		levelCounts: map[string]int{"error": 10, "info": 90},
	}
	metrics := NewWatchMetrics(ls)

	// error_rate = 0.1 (breaches 0.05), and error_rate = 0.1 (does NOT breach 0.2)
	c := &Condition{
		All: []*Condition{
			{Type: "threshold", Metric: store.WatchMetricErrorRate, Op: store.WatchOpGreaterThan, Value: 0.05},
			{Type: "threshold", Metric: store.WatchMetricErrorRate, Op: store.WatchOpGreaterThan, Value: 0.2},
		},
	}

	result, err := EvaluateCondition(context.Background(), c, metrics, nil, "", 30*1e9)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if result.Breached {
		t.Error("ALL should not breach when second condition fails")
	}
}

func TestEvaluateCondition_AnyNeedsOneTrue(t *testing.T) {
	ls := &mockLogStoreForMetrics{
		levelCounts: map[string]int{"error": 10, "info": 90},
	}
	metrics := NewWatchMetrics(ls)

	c := &Condition{
		Any: []*Condition{
			{Type: "threshold", Metric: store.WatchMetricErrorRate, Op: store.WatchOpGreaterThan, Value: 0.2},  // false
			{Type: "threshold", Metric: store.WatchMetricErrorRate, Op: store.WatchOpGreaterThan, Value: 0.05}, // true
		},
	}

	result, err := EvaluateCondition(context.Background(), c, metrics, nil, "", 30*1e9)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !result.Breached {
		t.Error("ANY should breach when at least one condition is true")
	}
}

// mockLogStoreForMetrics provides canned responses for condition testing.
type mockLogStoreForMetrics struct {
	store.LogStore // embed to satisfy interface
	levelCounts    map[string]int
}

func (m *mockLogStoreForMetrics) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return m.levelCounts, nil
}
