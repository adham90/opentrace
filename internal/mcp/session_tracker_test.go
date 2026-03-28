package mcp

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestNewSessionTracker_ReturnsNonNil(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	if st == nil {
		t.Fatal("NewSessionTracker returned nil")
	}
}

func TestNewSessionTracker_SetsTransport(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "sse")
	if st.transport != "sse" {
		t.Errorf("transport = %q, want %q", st.transport, "sse")
	}
}

func TestNewSessionTracker_SetsUser(t *testing.T) {
	user := &store.User{ID: "u1", Email: "test@example.com", Role: store.RoleAdmin}
	st := NewSessionTracker(context.Background(), nil, user, "stdio")
	if st.user != user {
		t.Error("expected user to be stored")
	}
}

func TestNewSessionTracker_SetsStore(t *testing.T) {
	ms := mocks.NewInvestigationSessionStore()
	st := NewSessionTracker(context.Background(), ms, nil, "stdio")
	if st.store == nil {
		t.Error("expected store to be non-nil")
	}
}

func TestSessionTracker_SetTransitionStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	ts := mocks.NewToolTransitionStore()
	st.SetTransitionStore(ts)
	if st.transitionStore == nil {
		t.Error("transitionStore should be set")
	}
}

func TestSessionTracker_SetActivityStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	as := mocks.NewMCPActivityStore()
	st.SetActivityStore(as)
	if st.activityStore == nil {
		t.Error("activityStore should be set")
	}
}

func TestSessionTracker_SetNoteStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	ns := mocks.NewAgentNoteStore()
	st.SetNoteStore(ns)
	if st.noteStore == nil {
		t.Error("noteStore should be set")
	}
}

func TestSessionTracker_SetRunbookStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	rs := mocks.NewRunbookEffectivenessStore()
	st.SetRunbookStore(rs)
	if st.runbookStore == nil {
		t.Error("runbookStore should be set")
	}
}

func TestSessionTracker_SetQueryMemoryStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	qs := mocks.NewQueryMemoryStore()
	st.SetQueryMemoryStore(qs)
	if st.queryMemoryStore == nil {
		t.Error("queryMemoryStore should be set")
	}
}

func TestSessionTracker_SetTrendStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	ts := mocks.NewTrendStore()
	st.SetTrendStore(ts)
	if st.trendStore == nil {
		t.Error("trendStore should be set")
	}
}

func TestSessionTracker_SetAuditStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	as := mocks.NewAuditStore()
	st.SetAuditStore(as)
	if st.auditStore == nil {
		t.Error("auditStore should be set")
	}
}

func TestSessionTracker_SetCodeEntityStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	ces := mocks.NewCodeEntityStore()
	st.SetCodeEntityStore(ces)
	if st.codeEntityStore == nil {
		t.Error("codeEntityStore should be set")
	}
}

func TestSessionTracker_SetErrorGroupStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	egs := mocks.NewErrorGroupStore()
	st.SetErrorGroupStore(egs)
	if st.errorGroupStore == nil {
		t.Error("errorGroupStore should be set")
	}
}

func TestSessionTracker_SetDeployStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	ds := mocks.NewDeployStore()
	st.SetDeployStore(ds)
	if st.deployStore == nil {
		t.Error("deployStore should be set")
	}
}

func TestSessionTracker_SetEventStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	es := mocks.NewEventStore()
	st.SetEventStore(es)
	if st.eventStore == nil {
		t.Error("eventStore should be set")
	}
}

func TestSessionTracker_SetTestCorrelationStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	tcs := mocks.NewTestCorrelationStore()
	st.SetTestCorrelationStore(tcs)
	if st.testCorrelationStore == nil {
		t.Error("testCorrelationStore should be set")
	}
}

// --- OnInitialize ---

func TestSessionTracker_OnInitialize_NilStore(t *testing.T) {
	// Should not panic when store is nil.
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.OnInitialize(nil) // should be safe since it checks store == nil first
}

// --- CloseSession ---

func TestSessionTracker_CloseSession_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.CloseSession()
}

func TestSessionTracker_CloseSession_NoSession(t *testing.T) {
	ms := mocks.NewInvestigationSessionStore()
	st := NewSessionTracker(context.Background(), ms, nil, "stdio")
	// session is nil, should be a no-op.
	st.CloseSession()
}

// --- CurrentSession / CurrentSessionID ---

func TestSessionTracker_CurrentSession_InitiallyNil(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	if st.CurrentSession() != nil {
		t.Error("CurrentSession should be nil initially")
	}
}

func TestSessionTracker_CurrentSessionID_InitiallyEmpty(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	if st.CurrentSessionID() != "" {
		t.Error("CurrentSessionID should be empty initially")
	}
}

func TestSessionTracker_CurrentSession_ReturnsSetSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	sess := &store.InvestigationSession{ID: "sess-123"}
	st.mu.Lock()
	st.session = sess
	st.mu.Unlock()

	got := st.CurrentSession()
	if got == nil || got.ID != "sess-123" {
		t.Errorf("CurrentSession().ID = %v, want %q", got, "sess-123")
	}
}

func TestSessionTracker_CurrentSessionID_ReturnsSetID(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.mu.Lock()
	st.session = &store.InvestigationSession{ID: "abc"}
	st.mu.Unlock()

	if st.CurrentSessionID() != "abc" {
		t.Errorf("CurrentSessionID() = %q, want %q", st.CurrentSessionID(), "abc")
	}
}

// --- UserID ---

func TestSessionTracker_UserID_NilUser(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	if st.UserID() != "" {
		t.Error("UserID should be empty when user is nil")
	}
}

func TestSessionTracker_UserID_WithUser(t *testing.T) {
	user := &store.User{ID: "user-42"}
	st := NewSessionTracker(context.Background(), nil, user, "stdio")
	if st.UserID() != "user-42" {
		t.Errorf("UserID() = %q, want %q", st.UserID(), "user-42")
	}
}

// --- RecordStep ---

func TestSessionTracker_RecordStep_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	idx := st.RecordStep("test_tool", false, nil)
	if idx != 1 {
		t.Errorf("RecordStep should return 1 on first call, got %d", idx)
	}
}

func TestSessionTracker_RecordStep_NoSession(t *testing.T) {
	ms := mocks.NewInvestigationSessionStore()
	st := NewSessionTracker(context.Background(), ms, nil, "stdio")
	// No session set, should not panic but still increment step.
	idx := st.RecordStep("logs", false, nil)
	if idx != 1 {
		t.Errorf("RecordStep should return 1, got %d", idx)
	}
}

func TestSessionTracker_RecordStep_WithSession(t *testing.T) {
	ms := mocks.NewInvestigationSessionStore()
	st := NewSessionTracker(context.Background(), ms, nil, "stdio")
	st.mu.Lock()
	st.session = &store.InvestigationSession{ID: "sess-1"}
	st.mu.Unlock()

	idx := st.RecordStep("logs", false, nil)
	if idx != 1 {
		t.Errorf("first RecordStep should return 1, got %d", idx)
	}

	idx2 := st.RecordStep("errors", false, nil)
	if idx2 != 2 {
		t.Errorf("second RecordStep should return 2, got %d", idx2)
	}
}

func TestSessionTracker_RecordStep_IncrementsMonotonically(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	for i := 1; i <= 5; i++ {
		idx := st.RecordStep("tool", false, nil)
		if idx != i {
			t.Errorf("RecordStep call %d returned %d", i, idx)
		}
	}
}

// --- UpdateSession ---

func TestSessionTracker_UpdateSession_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.UpdateSession(store.UpdateInvestigationSessionParams{})
}

func TestSessionTracker_UpdateSession_NoSession(t *testing.T) {
	ms := mocks.NewInvestigationSessionStore()
	st := NewSessionTracker(context.Background(), ms, nil, "stdio")
	// No session, should be a no-op.
	st.UpdateSession(store.UpdateInvestigationSessionParams{})
}

// --- SetLastSuggestions / SnapshotLastSuggestions ---

func TestSessionTracker_SetLastSuggestions(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	suggestions := []ToolSuggestion{
		{Tool: "logs", Why: "check errors"},
		{Tool: "errors", Why: "see error groups"},
	}
	st.SetLastSuggestions(suggestions)
	snap := st.SnapshotLastSuggestions()
	if len(snap) != 2 {
		t.Fatalf("SnapshotLastSuggestions length = %d, want 2", len(snap))
	}
	if snap[0].Tool != "logs" {
		t.Errorf("snap[0].Tool = %q, want %q", snap[0].Tool, "logs")
	}
	if snap[1].Tool != "errors" {
		t.Errorf("snap[1].Tool = %q, want %q", snap[1].Tool, "errors")
	}
}

func TestSessionTracker_SnapshotLastSuggestions_Empty(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	snap := st.SnapshotLastSuggestions()
	if len(snap) != 0 {
		t.Errorf("SnapshotLastSuggestions should be empty initially, got %d", len(snap))
	}
}

func TestSessionTracker_SetLastSuggestions_MakesCopy(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	original := []ToolSuggestion{{Tool: "logs"}}
	st.SetLastSuggestions(original)

	// Mutate the original slice; snapshot should not change.
	original[0].Tool = "mutated"
	snap := st.SnapshotLastSuggestions()
	if snap[0].Tool != "logs" {
		t.Errorf("SetLastSuggestions should copy; got %q after mutation", snap[0].Tool)
	}
}

func TestSessionTracker_SnapshotLastSuggestions_ReturnsCopy(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.SetLastSuggestions([]ToolSuggestion{{Tool: "logs"}})

	snap := st.SnapshotLastSuggestions()
	snap[0].Tool = "mutated"

	snap2 := st.SnapshotLastSuggestions()
	if snap2[0].Tool != "logs" {
		t.Errorf("SnapshotLastSuggestions should return a copy; got %q", snap2[0].Tool)
	}
}

// --- TrackFileRead / TrackFileModified ---

func TestSessionTracker_TrackFileRead_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic with no session.
	st.TrackFileRead("/some/path")
}

func TestSessionTracker_TrackFileModified_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic with no session.
	st.TrackFileModified("/some/path")
}

func TestSessionTracker_TrackFileRead_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.mu.Lock()
	st.session = &store.InvestigationSession{ID: "s1"}
	st.mu.Unlock()
	// store is nil, should return early without panic.
	st.TrackFileRead("/path/to/file")
}

func TestSessionTracker_TrackFileModified_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.mu.Lock()
	st.session = &store.InvestigationSession{ID: "s1"}
	st.mu.Unlock()
	// store is nil, should return early without panic.
	st.TrackFileModified("/path/to/file")
}

// --- ClassifyIntentFromContext ---

func TestSessionTracker_ClassifyIntentFromContext_EmptyString(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic with empty context string.
	st.ClassifyIntentFromContext("", "logs")
}

func TestSessionTracker_ClassifyIntentFromContext_NoSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// No session, should be a no-op.
	st.ClassifyIntentFromContext("investigating error", "logs")
}

// --- deduplicateStrings ---

func TestDeduplicateStrings_Empty(t *testing.T) {
	result := deduplicateStrings(nil)
	if len(result) != 0 {
		t.Errorf("deduplicateStrings(nil) length = %d, want 0", len(result))
	}
}

func TestDeduplicateStrings_NoDuplicates(t *testing.T) {
	result := deduplicateStrings([]string{"a", "b", "c"})
	if len(result) != 3 {
		t.Errorf("length = %d, want 3", len(result))
	}
}

func TestDeduplicateStrings_WithDuplicates(t *testing.T) {
	result := deduplicateStrings([]string{"a", "b", "a", "c", "b"})
	if len(result) != 3 {
		t.Errorf("length = %d, want 3", len(result))
	}
	// Order should be preserved (first occurrence).
	expected := []string{"a", "b", "c"}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("result[%d] = %q, want %q", i, result[i], v)
		}
	}
}

func TestDeduplicateStrings_AllSame(t *testing.T) {
	result := deduplicateStrings([]string{"x", "x", "x"})
	if len(result) != 1 {
		t.Errorf("length = %d, want 1", len(result))
	}
	if result[0] != "x" {
		t.Errorf("result[0] = %q, want %q", result[0], "x")
	}
}

// --- Lifecycle hooks with nil stores ---

func TestSessionTracker_createAutoNotes_NilNoteStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.createAutoNotes(context.Background(), &store.InvestigationSession{
		Status:  store.InvestigationStatusResolved,
		Summary: "fixed it",
	})
}

func TestSessionTracker_updateRunbookEffectiveness_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.updateRunbookEffectiveness(context.Background(), &store.InvestigationSession{
		Status:           store.InvestigationStatusResolved,
		RunbooksExecuted: []string{"rb1"},
	})
}

func TestSessionTracker_updateQueryMemory_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.updateQueryMemory(context.Background(), &store.InvestigationSession{
		ExplainedQueries: []string{"fp1"},
	})
}

func TestSessionTracker_recordAuditEntry_NilStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.recordAuditEntry(context.Background(), &store.InvestigationSession{
		Intent:     IntentInvestigation,
		TotalSteps: 5,
	})
}

func TestSessionTracker_createAutoNotes_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.noteStore = mocks.NewAgentNoteStore()
	// nil session should not panic.
	st.createAutoNotes(context.Background(), nil)
}

func TestSessionTracker_updateRunbookEffectiveness_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.runbookStore = mocks.NewRunbookEffectivenessStore()
	// nil session should not panic.
	st.updateRunbookEffectiveness(context.Background(), nil)
}

func TestSessionTracker_updateQueryMemory_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.queryMemoryStore = mocks.NewQueryMemoryStore()
	// nil session should not panic.
	st.updateQueryMemory(context.Background(), nil)
}

func TestSessionTracker_recordAuditEntry_NilSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.auditStore = mocks.NewAuditStore()
	// nil session should not panic.
	st.recordAuditEntry(context.Background(), nil)
}

func TestSessionTracker_createAutoNotes_NotResolved(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.noteStore = mocks.NewAgentNoteStore()
	// Status is not resolved, should be a no-op.
	st.createAutoNotes(context.Background(), &store.InvestigationSession{
		Status:  store.InvestigationStatusOpen,
		Summary: "still open",
	})
}

func TestSessionTracker_updateRunbookEffectiveness_NoRunbooks(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.runbookStore = mocks.NewRunbookEffectivenessStore()
	// Empty RunbooksExecuted, should be a no-op.
	st.updateRunbookEffectiveness(context.Background(), &store.InvestigationSession{
		Status: store.InvestigationStatusResolved,
	})
}

func TestSessionTracker_updateQueryMemory_NoQueries(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.queryMemoryStore = mocks.NewQueryMemoryStore()
	// Empty ExplainedQueries, should be a no-op.
	st.updateQueryMemory(context.Background(), &store.InvestigationSession{})
}

func TestSessionTracker_recordAuditEntry_NotInvestigation(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.auditStore = mocks.NewAuditStore()
	// Intent is not investigation, should be a no-op.
	st.recordAuditEntry(context.Background(), &store.InvestigationSession{
		Intent:     IntentQuery,
		TotalSteps: 5,
	})
}

func TestSessionTracker_recordAuditEntry_TooFewSteps(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.auditStore = mocks.NewAuditStore()
	// Less than 3 steps, should be a no-op.
	st.recordAuditEntry(context.Background(), &store.InvestigationSession{
		Intent:     IntentInvestigation,
		TotalSteps: 2,
	})
}

// --- correlateDeploy ---

func TestSessionTracker_correlateDeploy_NilTrendStore(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.correlateDeploy(context.Background())
}

func TestSessionTracker_correlateDeploy_NoSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	st.trendStore = mocks.NewTrendStore()
	// No session, should not panic.
	st.correlateDeploy(context.Background())
}

// --- recordTransition ---

func TestSessionTracker_recordTransition_NoSession(t *testing.T) {
	st := NewSessionTracker(context.Background(), nil, nil, "stdio")
	// Should not panic.
	st.recordTransition(context.Background(), "logs", "sess-1", 1)
}
