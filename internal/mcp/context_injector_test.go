package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockSessionStoreForContext extends the recurrence_test mock with FindSimilar support.
type mockSessionStoreForContext struct {
	sessions map[string]*store.InvestigationSession
}

func newMockSessionStoreForContext() *mockSessionStoreForContext {
	return &mockSessionStoreForContext{sessions: make(map[string]*store.InvestigationSession)}
}

func (m *mockSessionStoreForContext) Create(_ context.Context, params store.CreateInvestigationSessionParams) (*store.InvestigationSession, error) {
	id := "sess-" + params.UserID
	sess := &store.InvestigationSession{
		ID:             id,
		UserID:         params.UserID,
		Status:         store.InvestigationStatusOpen,
		StartedAt:      time.Now().UTC(),
		LastActivityAt: time.Now().UTC(),
		ToolSequence:   []string{},
	}
	m.sessions[id] = sess
	return sess, nil
}

func (m *mockSessionStoreForContext) GetByID(_ context.Context, id string) (*store.InvestigationSession, error) {
	if s, ok := m.sessions[id]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockSessionStoreForContext) Close(_ context.Context, _ string) error { return nil }
func (m *mockSessionStoreForContext) Update(_ context.Context, _ string, _ store.UpdateInvestigationSessionParams) error {
	return nil
}
func (m *mockSessionStoreForContext) FindRecent(_ context.Context, _ store.FindRecentSessionParams) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *mockSessionStoreForContext) List(_ context.Context, params store.ListInvestigationSessionParams) ([]store.InvestigationSession, error) {
	var result []store.InvestigationSession
	for _, s := range m.sessions {
		if params.Status != "" && s.Status != params.Status {
			continue
		}
		if params.Service != "" && s.PrimaryService != params.Service {
			continue
		}
		result = append(result, *s)
	}
	return result, nil
}
func (m *mockSessionStoreForContext) Stats(_ context.Context) (*store.InvestigationSessionStats, error) {
	return &store.InvestigationSessionStats{}, nil
}
func (m *mockSessionStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockSessionStoreForContext) RecordStep(_ context.Context, _ string, _ string, _ bool) error {
	return nil
}
func (m *mockSessionStoreForContext) FindByCreatedWatcher(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *mockSessionStoreForContext) FindByResolvedError(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *mockSessionStoreForContext) FindByCreatedHealthcheck(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}

func (m *mockSessionStoreForContext) FindSimilar(_ context.Context, params store.FindSimilarParams) ([]store.InvestigationSession, error) {
	var result []store.InvestigationSession
	for _, s := range m.sessions {
		if s.ID == params.ExcludeSessionID {
			continue
		}
		if params.Intent != "" && s.Intent != params.Intent {
			continue
		}
		if params.OnlyResolved && s.Status != store.InvestigationStatusResolved {
			continue
		}
		if s.TotalSteps < params.MinSteps {
			continue
		}
		result = append(result, *s)
		if len(result) >= params.MaxResults {
			break
		}
	}
	return result, nil
}

func (m *mockSessionStoreForContext) putSession(s *store.InvestigationSession) {
	m.sessions[s.ID] = s
}

func TestContextInjector_SimilarSessions(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}

	// Create a past resolved session
	ms.putSession(&store.InvestigationSession{
		ID:              "past-1",
		Intent:          IntentInvestigation,
		Status:          store.InvestigationStatusResolved,
		TotalSteps:      5,
		Summary:         "Fixed database connection pool exhaustion",
		RootCause:       "N+1 query in users#index",
		PrimaryService:  "api",
		ToolSequence:    []string{"diagnose", "db_query_stats", "explain_query"},
		ToolFingerprint: "diagnose|db_query_stats|explain_query",
	})

	ci := NewContextInjector(ms, ts)

	// Current session
	currentSess := &store.InvestigationSession{
		ID:              "current-1",
		Intent:          IntentInvestigation,
		Status:          store.InvestigationStatusOpen,
		TotalSteps:      2,
		PrimaryService:  "api",
		ToolFingerprint: "diagnose|db_query_stats",
	}

	ic := ci.BuildContext(context.Background(), currentSess, "db_query_stats")
	if ic == nil {
		t.Fatal("expected investigation context, got nil")
	}
	if len(ic.SimilarPastSessions) == 0 {
		t.Error("expected similar past sessions")
	}
}

func TestContextInjector_NonInvestigationReturnsNil(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	ci := NewContextInjector(ms, ts)

	sess := &store.InvestigationSession{
		ID:     "query-1",
		Intent: IntentQuery,
		Status: store.InvestigationStatusOpen,
	}

	ic := ci.BuildContext(context.Background(), sess, "system_overview")
	if ic != nil {
		t.Error("expected nil for non-investigation intent")
	}
}

func TestContextInjector_EmptyContextReturnsNil(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	ci := NewContextInjector(ms, ts)

	sess := &store.InvestigationSession{
		ID:     "current-1",
		Intent: IntentInvestigation,
		Status: store.InvestigationStatusOpen,
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic != nil {
		t.Error("expected nil when no context available")
	}
}

func TestContextInjector_InjectContext(t *testing.T) {
	ci := &ContextInjector{}
	resp := map[string]any{"status": "ok"}

	// Nil context should not add anything
	ci.InjectContext(resp, nil)
	if _, ok := resp["investigation_context"]; ok {
		t.Error("nil context should not add investigation_context")
	}

	// Non-nil context should be added
	ic := &InvestigationContext{
		SimilarPastSessions: []SessionSummary{{SessionID: "s1", Intent: "investigation"}},
	}
	ci.InjectContext(resp, ic)
	if _, ok := resp["investigation_context"]; !ok {
		t.Error("expected investigation_context in response")
	}
}

func TestContextInjector_SizeCap(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}

	// Create many similar sessions with long summaries
	for i := 0; i < 10; i++ {
		longSummary := ""
		for j := 0; j < 100; j++ {
			longSummary += "This is a very long summary text. "
		}
		ms.putSession(&store.InvestigationSession{
			ID:             "past-" + string(rune('a'+i)),
			Intent:         IntentInvestigation,
			Status:         store.InvestigationStatusResolved,
			TotalSteps:     5,
			Summary:        longSummary,
			RootCause:      longSummary,
			PrimaryService: "api",
		})
	}

	ci := NewContextInjector(ms, ts)

	sess := &store.InvestigationSession{
		ID:             "current",
		Intent:         IntentInvestigation,
		Status:         store.InvestigationStatusOpen,
		PrimaryService: "api",
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic == nil {
		t.Fatal("expected context")
	}

	data, err := json.Marshal(ic)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 2200 { // allow some margin over 2048
		t.Errorf("context too large: %d bytes", len(data))
	}
}

func TestContextInjector_ParallelInvestigations(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}

	// Another user investigating same service
	ms.putSession(&store.InvestigationSession{
		ID:             "parallel-1",
		Intent:         IntentInvestigation,
		Status:         store.InvestigationStatusOpen,
		UserEmail:      "other@example.com",
		TotalSteps:     3,
		PrimaryService: "api",
	})

	ci := NewContextInjector(ms, ts)

	sess := &store.InvestigationSession{
		ID:             "current",
		Intent:         IntentInvestigation,
		Status:         store.InvestigationStatusOpen,
		PrimaryService: "api",
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic == nil {
		t.Fatal("expected context")
	}
	if len(ic.ParallelInvestigations) == 0 {
		t.Error("expected parallel investigations")
	}
	if ic.ParallelInvestigations[0].UserEmail != "other@example.com" {
		t.Errorf("expected other@example.com, got %q", ic.ParallelInvestigations[0].UserEmail)
	}
}

func TestTruncateForContext(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("short string should not be truncated")
	}
	// The existing truncate appends "..." after n characters
	if truncate("hello world", 8) != "hello wo..." {
		t.Errorf("expected 'hello wo...', got %q", truncate("hello world", 8))
	}
	if truncate("hi", 2) != "hi" {
		t.Error("exact length should not truncate")
	}
}
