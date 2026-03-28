package ingest

import (
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Tests: IsErrorLevel
// ---------------------------------------------------------------------------

func TestIsErrorLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		// Positive cases (lowercase)
		{"error", true},
		{"warn", true},
		{"warning", true},
		{"fatal", true},
		// Positive cases (uppercase)
		{"ERROR", true},
		{"WARN", true},
		{"WARNING", true},
		{"FATAL", true},
		// Positive cases (mixed case)
		{"Error", true},
		{"Warn", true},
		{"Warning", true},
		{"Fatal", true},
		// Negative cases
		{"info", false},
		{"debug", false},
		{"INFO", false},
		{"DEBUG", false},
		{"trace", false},
		{"", false},
		{"notice", false},
		{"critical", false}, // not in the switch
	}
	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			got := IsErrorLevel(tt.level)
			if got != tt.want {
				t.Errorf("IsErrorLevel(%q) = %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helpers for sampling tests
// ---------------------------------------------------------------------------

func makeLogEntries(services []string, levels []string) []store.LogEntry {
	now := time.Now().UTC()
	entries := make([]store.LogEntry, len(services))
	for i := range services {
		level := "info"
		if i < len(levels) {
			level = levels[i]
		}
		entries[i] = store.LogEntry{
			Timestamp: now,
			Level:     level,
			Service:   services[i],
			Message:   "test message",
		}
	}
	return entries
}

// ---------------------------------------------------------------------------
// Tests: ApplySamplingRules
// ---------------------------------------------------------------------------

func TestApplySamplingRules_NoRules(t *testing.T) {
	entries := makeLogEntries(
		[]string{"svc-a", "svc-b", "svc-c"},
		[]string{"info", "info", "info"},
	)
	result := ApplySamplingRules(entries, nil)
	if len(result) != len(entries) {
		t.Errorf("no rules: expected %d entries, got %d", len(entries), len(result))
	}
}

func TestApplySamplingRules_EmptyRulesSlice(t *testing.T) {
	entries := makeLogEntries(
		[]string{"svc-a", "svc-b"},
		[]string{"info", "info"},
	)
	result := ApplySamplingRules(entries, []store.SamplingRule{})
	if len(result) != len(entries) {
		t.Errorf("empty rules: expected %d entries, got %d", len(entries), len(result))
	}
}

func TestApplySamplingRules_ServiceRate1_KeepsAll(t *testing.T) {
	entries := makeLogEntries(
		[]string{"svc-a", "svc-a", "svc-a"},
		[]string{"info", "debug", "info"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-a", Rate: 1.0},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 3 {
		t.Errorf("rate=1.0: expected 3 entries, got %d", len(result))
	}
}

func TestApplySamplingRules_ServiceRate0_DropsAll(t *testing.T) {
	// Use a large batch to make the test statistically reliable.
	// With rate=0.0, all entries should be dropped.
	services := make([]string, 200)
	levels := make([]string, 200)
	for i := range services {
		services[i] = "svc-drop"
		levels[i] = "info"
	}
	entries := makeLogEntries(services, levels)
	rules := []store.SamplingRule{
		{Service: "svc-drop", Rate: 0.0, KeepErrors: false},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("rate=0.0: expected 0 entries, got %d", len(result))
	}
}

func TestApplySamplingRules_DefaultRuleApplies(t *testing.T) {
	// Default rule (*) should apply to services without a specific rule.
	entries := makeLogEntries(
		[]string{"unknown-svc", "unknown-svc", "unknown-svc"},
		[]string{"info", "info", "info"},
	)
	rules := []store.SamplingRule{
		{Service: "other-svc", Rate: 1.0},
		{Service: "*", Rate: 0.0}, // default: drop all
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("default rule rate=0.0: expected 0 entries, got %d", len(result))
	}
}

func TestApplySamplingRules_DefaultRuleKeepsWhenNoMatch(t *testing.T) {
	entries := makeLogEntries(
		[]string{"unmatched-svc"},
		[]string{"info"},
	)
	rules := []store.SamplingRule{
		{Service: "*", Rate: 1.0},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 1 {
		t.Errorf("default rule rate=1.0: expected 1 entry, got %d", len(result))
	}
}

func TestApplySamplingRules_KeepErrorsTrue(t *testing.T) {
	// Even with rate=0.0, error/warn/fatal should be kept when KeepErrors=true.
	entries := makeLogEntries(
		[]string{"svc-a", "svc-a", "svc-a", "svc-a", "svc-a"},
		[]string{"info", "error", "warn", "fatal", "debug"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-a", Rate: 0.0, KeepErrors: true},
	}
	result := ApplySamplingRules(entries, rules)
	// Should keep error, warn, fatal (3 entries); drop info and debug
	if len(result) != 3 {
		t.Errorf("KeepErrors=true: expected 3 entries, got %d", len(result))
	}
	for _, e := range result {
		if !IsErrorLevel(e.Level) {
			t.Errorf("KeepErrors=true: expected only error-level entries, got level=%q", e.Level)
		}
	}
}

func TestApplySamplingRules_KeepErrorsFalse(t *testing.T) {
	// With KeepErrors=false and rate=0.0, all entries including errors are dropped.
	entries := makeLogEntries(
		[]string{"svc-a", "svc-a", "svc-a"},
		[]string{"info", "error", "fatal"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-a", Rate: 0.0, KeepErrors: false},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 0 {
		t.Errorf("KeepErrors=false rate=0.0: expected 0 entries, got %d", len(result))
	}
}

func TestApplySamplingRules_KeepErrors_WarningLevel(t *testing.T) {
	// "warning" (not just "warn") should also be kept by KeepErrors
	entries := makeLogEntries(
		[]string{"svc-a"},
		[]string{"warning"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-a", Rate: 0.0, KeepErrors: true},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 1 {
		t.Errorf("KeepErrors with 'warning' level: expected 1 entry, got %d", len(result))
	}
}

func TestApplySamplingRules_MultipleRules(t *testing.T) {
	entries := makeLogEntries(
		[]string{"svc-keep", "svc-keep", "svc-drop", "svc-drop", "no-rule"},
		[]string{"info", "info", "info", "info", "info"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-keep", Rate: 1.0},
		{Service: "svc-drop", Rate: 0.0},
		// No rule for "no-rule" and no default rule => kept
	}
	result := ApplySamplingRules(entries, rules)
	// svc-keep: 2 kept, svc-drop: 0 kept, no-rule: 1 kept
	if len(result) != 3 {
		t.Errorf("multiple rules: expected 3 entries, got %d", len(result))
	}
	for _, e := range result {
		if e.Service == "svc-drop" {
			t.Error("expected svc-drop entries to be filtered out")
		}
	}
}

func TestApplySamplingRules_ServiceSpecificOverridesDefault(t *testing.T) {
	entries := makeLogEntries(
		[]string{"svc-special", "svc-generic"},
		[]string{"info", "info"},
	)
	rules := []store.SamplingRule{
		{Service: "svc-special", Rate: 1.0}, // keep all for this service
		{Service: "*", Rate: 0.0},           // default: drop all
	}
	result := ApplySamplingRules(entries, rules)
	// svc-special: 1 kept (specific rule), svc-generic: 0 kept (default rule)
	if len(result) != 1 {
		t.Errorf("specific override default: expected 1 entry, got %d", len(result))
	}
	if result[0].Service != "svc-special" {
		t.Errorf("expected svc-special, got %q", result[0].Service)
	}
}

func TestApplySamplingRules_NoRuleForService(t *testing.T) {
	// When there is no rule for a service and no default rule, all entries are kept.
	entries := makeLogEntries(
		[]string{"unknown"},
		[]string{"info"},
	)
	rules := []store.SamplingRule{
		{Service: "other-svc", Rate: 0.0},
	}
	result := ApplySamplingRules(entries, rules)
	if len(result) != 1 {
		t.Errorf("no matching rule: expected 1 entry, got %d", len(result))
	}
}

func TestApplySamplingRules_EmptyEntries(t *testing.T) {
	rules := []store.SamplingRule{
		{Service: "*", Rate: 0.5},
	}
	result := ApplySamplingRules(nil, rules)
	if len(result) != 0 {
		t.Errorf("empty entries: expected 0, got %d", len(result))
	}
}

func TestApplySamplingRules_PartialSampling(t *testing.T) {
	// With rate=0.5, we expect roughly half the entries to survive.
	// Use a large batch and check it is between reasonable bounds.
	n := 10000
	services := make([]string, n)
	levels := make([]string, n)
	for i := range services {
		services[i] = "svc-half"
		levels[i] = "info"
	}
	entries := makeLogEntries(services, levels)
	rules := []store.SamplingRule{
		{Service: "svc-half", Rate: 0.5},
	}
	result := ApplySamplingRules(entries, rules)
	ratio := float64(len(result)) / float64(n)
	// Expect ratio to be roughly 0.5, give generous bounds (0.4 to 0.6)
	if ratio < 0.35 || ratio > 0.65 {
		t.Errorf("rate=0.5: expected ~50%% kept, got %.1f%% (%d/%d)", ratio*100, len(result), n)
	}
}
