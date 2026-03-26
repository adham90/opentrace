package mcp

import (
	"testing"

	"github.com/adham90/opentrace/pkg/store"
)

func TestInferOutcome_QuickQuery(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps: 1,
		Intent:     IntentQuery,
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("quick query: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}

func TestInferOutcome_Configuration(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps: 3,
		Intent:     IntentConfiguration,
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("configuration: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}

func TestInferOutcome_SummaryProvided(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps: 5,
		Intent:     IntentInvestigation,
		Summary:    "Found missing index on users.email",
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("summary provided: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}

func TestInferOutcome_ErrorResolved(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps:   4,
		Intent:       IntentInvestigation,
		ToolSequence: []string{"diagnose", "error_detail", "log_search", "resolve_error"},
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("error resolved: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}

func TestInferOutcome_WatcherCreated(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps:   3,
		Intent:       IntentInvestigation,
		ToolSequence: []string{"diagnose", "log_search", "watch"},
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("watcher created: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}

func TestInferOutcome_HighErrorRate(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps:   5,
		TotalErrors:  4,
		Intent:       IntentInvestigation,
		ToolSequence: []string{"diagnose", "error_detail", "log_search", "db_query_stats", "explain_query"},
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusUnresolved {
		t.Errorf("high error rate: got %q, want %q", got, store.InvestigationStatusUnresolved)
	}
}

func TestInferOutcome_NoSteps(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps: 0,
		Intent:     IntentInvestigation,
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusAbandoned {
		t.Errorf("no steps: got %q, want %q", got, store.InvestigationStatusAbandoned)
	}
}

func TestInferOutcome_ShortInvestigation(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps:   1,
		Intent:       IntentInvestigation,
		ToolSequence: []string{"diagnose"},
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusAbandoned {
		t.Errorf("short investigation: got %q, want %q", got, store.InvestigationStatusAbandoned)
	}
}

func TestInferOutcome_NilSession(t *testing.T) {
	got := InferOutcome(nil)
	if got != store.InvestigationStatusAbandoned {
		t.Errorf("nil session: got %q, want %q", got, store.InvestigationStatusAbandoned)
	}
}

func TestInferOutcome_SuccessfulSteps(t *testing.T) {
	sess := &store.InvestigationSession{
		TotalSteps:   5,
		TotalErrors:  0,
		Intent:       IntentInvestigation,
		ToolSequence: []string{"diagnose", "error_detail", "log_search", "db_query_stats", "system_overview"},
	}
	got := InferOutcome(sess)
	if got != store.InvestigationStatusResolved {
		t.Errorf("successful steps: got %q, want %q", got, store.InvestigationStatusResolved)
	}
}
