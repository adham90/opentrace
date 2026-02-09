package watcher

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/opentrace/opentrace/internal/store"
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
