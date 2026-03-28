package api

import (
	"math"
	"testing"

	"github.com/adham90/opentrace/internal/ingest"
	"github.com/adham90/opentrace/pkg/store"
)

func TestApplySamplingRules_NoRules(t *testing.T) {
	entries := makeEntries(100, "info", "my-service")
	result := ingest.ApplySamplingRules(entries, nil)
	if len(result) != 100 {
		t.Errorf("expected 100 entries, got %d", len(result))
	}

	result = ingest.ApplySamplingRules(entries, []store.SamplingRule{})
	if len(result) != 100 {
		t.Errorf("expected 100 entries, got %d", len(result))
	}
}

func TestApplySamplingRules_DefaultRuleHalf(t *testing.T) {
	entries := makeEntries(10000, "info", "my-service")
	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.5, KeepErrors: true},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	ratio := float64(len(result)) / float64(len(entries))
	// Allow generous tolerance for randomness: 0.3 to 0.7
	if ratio < 0.3 || ratio > 0.7 {
		t.Errorf("expected ~50%% kept, got %.1f%% (%d/%d)", ratio*100, len(result), len(entries))
	}
}

func TestApplySamplingRules_PerServiceRule(t *testing.T) {
	// 100 entries from service-a, 100 from service-b
	var entries []store.LogEntry
	entries = append(entries, makeEntries(100, "info", "service-a")...)
	entries = append(entries, makeEntries(100, "info", "service-b")...)

	rules := []store.SamplingRule{
		{Service: "service-a", Rate: 0.0, KeepErrors: false},
	}

	result := ingest.ApplySamplingRules(entries, rules)

	// service-a should all be dropped (rate=0), service-b should all be kept (no rule)
	var aCount, bCount int
	for _, e := range result {
		switch e.Service {
		case "service-a":
			aCount++
		case "service-b":
			bCount++
		}
	}
	if aCount != 0 {
		t.Errorf("expected 0 service-a entries, got %d", aCount)
	}
	if bCount != 100 {
		t.Errorf("expected 100 service-b entries, got %d", bCount)
	}
}

func TestApplySamplingRules_ErrorsAlwaysKept(t *testing.T) {
	var entries []store.LogEntry
	for _, level := range []string{"error", "warn", "warning", "fatal", "ERROR", "WARN"} {
		entries = append(entries, store.LogEntry{
			Level:   level,
			Service: "svc",
			Message: "test " + level,
		})
	}

	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.0, KeepErrors: true},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	if len(result) != len(entries) {
		t.Errorf("expected all %d error/warn entries kept, got %d", len(entries), len(result))
	}
}

func TestApplySamplingRules_ErrorsNotKeptWhenKeepErrorsFalse(t *testing.T) {
	entries := makeEntries(100, "error", "svc")
	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.0, KeepErrors: false},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("expected 0 entries when KeepErrors=false and rate=0, got %d", len(result))
	}
}

func TestApplySamplingRules_RateZero(t *testing.T) {
	entries := makeEntries(1000, "info", "svc")
	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.0, KeepErrors: true},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("expected 0 entries at rate 0, got %d", len(result))
	}
}

func TestApplySamplingRules_RateOne(t *testing.T) {
	entries := makeEntries(1000, "info", "svc")
	rules := []store.SamplingRule{
		{Service: "*", Rate: 1.0, KeepErrors: true},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	if len(result) != 1000 {
		t.Errorf("expected 1000 entries at rate 1.0, got %d", len(result))
	}
}

func TestApplySamplingRules_MixedLevelsWithSampling(t *testing.T) {
	var entries []store.LogEntry
	// 5000 info entries
	entries = append(entries, makeEntries(5000, "info", "svc")...)
	// 100 error entries
	entries = append(entries, makeEntries(100, "error", "svc")...)

	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.0, KeepErrors: true},
	}

	result := ingest.ApplySamplingRules(entries, rules)
	// All info should be dropped, all errors kept
	if len(result) != 100 {
		t.Errorf("expected 100 error entries kept, got %d", len(result))
	}
	for _, e := range result {
		if e.Level != "error" {
			t.Errorf("expected only error entries, got level=%q", e.Level)
		}
	}
}

func TestApplySamplingRules_DefaultAndServiceSpecific(t *testing.T) {
	var entries []store.LogEntry
	entries = append(entries, makeEntries(10000, "info", "critical-svc")...)
	entries = append(entries, makeEntries(10000, "info", "noisy-svc")...)

	rules := []store.SamplingRule{
		{Service: "*", Rate: 1.0, KeepErrors: true},          // default: keep all
		{Service: "noisy-svc", Rate: 0.0, KeepErrors: false}, // drop all from noisy-svc
	}

	result := ingest.ApplySamplingRules(entries, rules)

	var criticalCount, noisyCount int
	for _, e := range result {
		switch e.Service {
		case "critical-svc":
			criticalCount++
		case "noisy-svc":
			noisyCount++
		}
	}
	if criticalCount != 10000 {
		t.Errorf("expected 10000 critical-svc entries, got %d", criticalCount)
	}
	if noisyCount != 0 {
		t.Errorf("expected 0 noisy-svc entries, got %d", noisyCount)
	}
}

func TestIsErrorLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		{"error", true},
		{"ERROR", true},
		{"warn", true},
		{"WARN", true},
		{"warning", true},
		{"WARNING", true},
		{"fatal", true},
		{"FATAL", true},
		{"info", false},
		{"INFO", false},
		{"debug", false},
		{"DEBUG", false},
		{"trace", false},
		{"", false},
	}
	for _, tc := range tests {
		got := ingest.IsErrorLevel(tc.level)
		if got != tc.want {
			t.Errorf("ingest.IsErrorLevel(%q) = %v, want %v", tc.level, got, tc.want)
		}
	}
}

// makeEntries creates n log entries with the given level and service.
func makeEntries(n int, level, service string) []store.LogEntry {
	entries := make([]store.LogEntry, n)
	for i := range entries {
		entries[i] = store.LogEntry{
			Level:   level,
			Service: service,
			Message: "test message",
		}
	}
	return entries
}

// Ensure the function doesn't use NaN or produce unexpected results with edge values.
func TestApplySamplingRules_BoundaryRates(t *testing.T) {
	entries := makeEntries(100, "info", "svc")

	// Rate exactly 0
	rules := []store.SamplingRule{{Service: "*", Rate: 0.0, KeepErrors: false}}
	result := ingest.ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("rate=0: expected 0, got %d", len(result))
	}

	// Rate exactly 1
	rules = []store.SamplingRule{{Service: "*", Rate: 1.0, KeepErrors: false}}
	result = ingest.ApplySamplingRules(entries, rules)
	if len(result) != 100 {
		t.Errorf("rate=1: expected 100, got %d", len(result))
	}

	// Very small rate
	entries = makeEntries(100000, "info", "svc")
	rules = []store.SamplingRule{{Service: "*", Rate: 0.001, KeepErrors: false}}
	result = ingest.ApplySamplingRules(entries, rules)
	expectedApprox := 100.0
	if math.Abs(float64(len(result))-expectedApprox) > 200 {
		t.Errorf("rate=0.001: expected ~100, got %d", len(result))
	}
}
