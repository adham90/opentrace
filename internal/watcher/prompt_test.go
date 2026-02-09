package watcher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/store"
)

func TestBuildQuery_Basic(t *testing.T) {
	w := store.Watcher{
		Title:       "Payment errors",
		Description: "Check for payment gateway failures",
		TimeRange:   "15m",
		Filters:     json.RawMessage(`{"service":"payment-api","level":"error"}`),
	}

	q := BuildQuery(w, "")

	if !strings.Contains(q, "Payment errors") {
		t.Error("expected query to contain watcher title")
	}
	if !strings.Contains(q, "Check for payment gateway failures") {
		t.Error("expected query to contain description")
	}
	if !strings.Contains(q, "Service: payment-api") {
		t.Error("expected query to contain service filter")
	}
	if !strings.Contains(q, "Level: error") {
		t.Error("expected query to contain level filter")
	}
	if !strings.Contains(q, "Time range: last 15m") {
		t.Error("expected query to contain time range")
	}
	if strings.Contains(q, "Previous Run") {
		t.Error("expected no previous run section when summary is empty")
	}
}

func TestBuildQuery_WithPreviousRun(t *testing.T) {
	w := store.Watcher{
		Title:       "Memory leak detector",
		Description: "Watch for growing RSS",
		Filters:     json.RawMessage(`{}`),
	}

	q := BuildQuery(w, "Found 3 instances with RSS above 500MB")

	if !strings.Contains(q, "## Previous Run") {
		t.Error("expected Previous Run section")
	}
	if !strings.Contains(q, "Found 3 instances with RSS above 500MB") {
		t.Error("expected previous run summary in query")
	}
	if !strings.Contains(q, "Compare your findings") {
		t.Error("expected comparison instruction when previous run exists")
	}
}

func TestBuildQuery_NoFilters(t *testing.T) {
	w := store.Watcher{
		Title:       "General watcher",
		Description: "Watch everything",
		Filters:     json.RawMessage(`{}`),
	}

	q := BuildQuery(w, "")

	if strings.Contains(q, "## Search Scope") {
		t.Error("expected no Search Scope section when no filters")
	}
}

func TestBuildQuery_AllFilters(t *testing.T) {
	w := store.Watcher{
		Title:       "Full filter watcher",
		Description: "Testing all filters",
		TimeRange:   "1h",
		Filters: json.RawMessage(`{
			"service":"api",
			"level":"error",
			"environment":"production",
			"query":"timeout"
		}`),
	}

	q := BuildQuery(w, "")

	for _, expected := range []string{
		"Service: api",
		"Level: error",
		"Environment: production",
		"Time range: last 1h",
		"Query: timeout",
	} {
		if !strings.Contains(q, expected) {
			t.Errorf("expected query to contain %q", expected)
		}
	}
}

func TestBuildQuery_LowEffort(t *testing.T) {
	w := store.Watcher{
		Title:       "Quick check",
		Description: "Check error count",
		TimeRange:   "5m",
		Effort:      store.EffortLow,
		Filters:     json.RawMessage(`{"service":"api"}`),
	}

	q := BuildQuery(w, "")

	if !strings.Contains(q, "Quickly assess") {
		t.Error("expected low-effort prompt to contain 'Quickly assess'")
	}
	if !strings.Contains(q, "1-2 sentences") {
		t.Error("expected low-effort prompt to mention brief summary")
	}
	// Should NOT contain deep analysis instructions
	if strings.Contains(q, "Cross-reference") {
		t.Error("low-effort prompt should not contain 'Cross-reference'")
	}
}

func TestBuildQuery_HighEffort(t *testing.T) {
	w := store.Watcher{
		Title:       "Deep analysis",
		Description: "Analyze payment failures thoroughly",
		TimeRange:   "24h",
		Effort:      store.EffortHigh,
		Filters:     json.RawMessage(`{"service":"payment-api"}`),
	}

	q := BuildQuery(w, "Previous: 5 errors detected")

	if !strings.Contains(q, "Cross-reference") {
		t.Error("expected high-effort prompt to contain 'Cross-reference'")
	}
	if !strings.Contains(q, "root cause analysis") {
		t.Error("expected high-effort prompt to mention root cause analysis")
	}
	if !strings.Contains(q, "Previous: 5 errors detected") {
		t.Error("expected previous run summary in query")
	}
}

func TestEffortSettings(t *testing.T) {
	low := EffortSettings(store.EffortLow)
	med := EffortSettings(store.EffortMedium)
	high := EffortSettings(store.EffortHigh)

	if low.MaxSteps >= med.MaxSteps {
		t.Errorf("low steps (%d) should be less than medium (%d)", low.MaxSteps, med.MaxSteps)
	}
	if med.MaxSteps >= high.MaxSteps {
		t.Errorf("medium steps (%d) should be less than high (%d)", med.MaxSteps, high.MaxSteps)
	}
	if low.MaxToolCalls >= med.MaxToolCalls {
		t.Errorf("low tool calls (%d) should be less than medium (%d)", low.MaxToolCalls, med.MaxToolCalls)
	}
	if med.MaxToolCalls >= high.MaxToolCalls {
		t.Errorf("medium tool calls (%d) should be less than high (%d)", med.MaxToolCalls, high.MaxToolCalls)
	}

	// Default (empty string) should return medium
	def := EffortSettings("")
	if def.MaxSteps != med.MaxSteps || def.MaxToolCalls != med.MaxToolCalls {
		t.Error("empty effort should default to medium settings")
	}
}

func TestEvaluateFindings(t *testing.T) {
	tests := []struct {
		answer string
		want   bool
	}{
		{"ALERT: Found 47 errors in the last 15 minutes", true},
		{"Alert: Payment failures detected", true},
		{"alert: something is wrong", true},
		{"OK: Everything looks normal", false},
		{"ok: no issues found", false},
		{"No problems detected", false},
		{"", false},
		{"  ALERT: with leading spaces", true},
	}

	for _, tt := range tests {
		got := EvaluateFindings(tt.answer)
		if got != tt.want {
			t.Errorf("EvaluateFindings(%q) = %v, want %v", tt.answer, got, tt.want)
		}
	}
}
