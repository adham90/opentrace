package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// mockInvestigationSessionStore is a minimal mock for testing RecurrenceDetector.
type mockInvestigationSessionStore struct {
	sessions map[string]*store.InvestigationSession
}

func newMockSessionStore() *mockInvestigationSessionStore {
	return &mockInvestigationSessionStore{
		sessions: make(map[string]*store.InvestigationSession),
	}
}

func (m *mockInvestigationSessionStore) Create(_ context.Context, params store.CreateInvestigationSessionParams) (*store.InvestigationSession, error) {
	id := "sess-" + params.UserID
	sess := &store.InvestigationSession{
		ID:                            id,
		UserID:                        params.UserID,
		Status:                        store.InvestigationStatusOpen,
		StartedAt:                     time.Now().UTC(),
		LastActivityAt:                time.Now().UTC(),
		CreatedWatcherIDs:             []string{},
		ResolvedErrorGroupIDs:         []string{},
		InvestigatedErrorFingerprints: []string{},
		CreatedHealthcheckIDs:         []string{},
		ToolSequence:                  []string{},
	}
	m.sessions[id] = sess
	return sess, nil
}

func (m *mockInvestigationSessionStore) GetByID(_ context.Context, id string) (*store.InvestigationSession, error) {
	if s, ok := m.sessions[id]; ok {
		return s, nil
	}
	return nil, store.ErrNotFound
}

func (m *mockInvestigationSessionStore) Close(_ context.Context, id string) error {
	if s, ok := m.sessions[id]; ok {
		now := time.Now().UTC()
		s.EndedAt = &now
		s.Status = store.InvestigationStatusUnresolved
		return nil
	}
	return store.ErrNotFound
}

func (m *mockInvestigationSessionStore) Update(_ context.Context, id string, params store.UpdateInvestigationSessionParams) error {
	s, ok := m.sessions[id]
	if !ok {
		return store.ErrNotFound
	}
	if params.CreatedWatcherIDs != nil {
		s.CreatedWatcherIDs = params.CreatedWatcherIDs
	}
	if params.TriggeredByWatcherID != nil {
		s.TriggeredByWatcherID = params.TriggeredByWatcherID
	}
	if params.ResolvedErrorGroupIDs != nil {
		s.ResolvedErrorGroupIDs = params.ResolvedErrorGroupIDs
	}
	if params.InvestigatedErrorFingerprints != nil {
		s.InvestigatedErrorFingerprints = params.InvestigatedErrorFingerprints
	}
	if params.CreatedHealthcheckIDs != nil {
		s.CreatedHealthcheckIDs = params.CreatedHealthcheckIDs
	}
	if params.TriggeredByHealthcheckID != nil {
		s.TriggeredByHealthcheckID = params.TriggeredByHealthcheckID
	}
	if params.RecurrenceGroup != nil {
		s.RecurrenceGroup = params.RecurrenceGroup
	}
	if params.RecurrenceCount != nil {
		s.RecurrenceCount = *params.RecurrenceCount
	}
	if params.PreviousSessionID != nil {
		s.PreviousSessionID = params.PreviousSessionID
	}
	if params.FixDurabilitySeconds != nil {
		s.FixDurabilitySeconds = params.FixDurabilitySeconds
	}
	if params.Summary != nil {
		s.Summary = *params.Summary
	}
	if params.RootCause != nil {
		s.RootCause = *params.RootCause
	}
	if params.FixDescription != nil {
		s.FixDescription = *params.FixDescription
	}
	if params.Status != nil {
		s.Status = *params.Status
	}
	return nil
}

func (m *mockInvestigationSessionStore) FindRecent(_ context.Context, _ store.FindRecentSessionParams) (*store.InvestigationSession, error) {
	return nil, nil
}

func (m *mockInvestigationSessionStore) List(_ context.Context, _ store.ListInvestigationSessionParams) ([]store.InvestigationSession, error) {
	return nil, nil
}

func (m *mockInvestigationSessionStore) Stats(_ context.Context) (*store.InvestigationSessionStats, error) {
	return &store.InvestigationSessionStats{}, nil
}

func (m *mockInvestigationSessionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockInvestigationSessionStore) RecordStep(_ context.Context, _ string, _ string, _ bool) error {
	return nil
}

func (m *mockInvestigationSessionStore) FindByCreatedWatcher(_ context.Context, watcherID string) (*store.InvestigationSession, error) {
	for _, s := range m.sessions {
		for _, w := range s.CreatedWatcherIDs {
			if w == watcherID {
				return s, nil
			}
		}
	}
	return nil, nil
}

func (m *mockInvestigationSessionStore) FindByResolvedError(_ context.Context, fingerprint string) (*store.InvestigationSession, error) {
	for _, s := range m.sessions {
		for _, fp := range s.ResolvedErrorGroupIDs {
			if fp == fingerprint {
				return s, nil
			}
		}
	}
	return nil, nil
}

func (m *mockInvestigationSessionStore) FindByCreatedHealthcheck(_ context.Context, hcID string) (*store.InvestigationSession, error) {
	for _, s := range m.sessions {
		for _, id := range s.CreatedHealthcheckIDs {
			if id == hcID {
				return s, nil
			}
		}
	}
	return nil, nil
}

func (m *mockInvestigationSessionStore) FindSimilar(_ context.Context, _ store.FindSimilarParams) ([]store.InvestigationSession, error) {
	return nil, nil
}

// putSession inserts a session directly into the mock store.
func (m *mockInvestigationSessionStore) putSession(s *store.InvestigationSession) {
	m.sessions[s.ID] = s
}

func TestRecurrenceDetector_WatcherLinking(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	// Session 1: creates a watcher.
	sess1 := &store.InvestigationSession{
		ID:                "sess-1",
		Status:            store.InvestigationStatusOpen,
		CreatedWatcherIDs: []string{},
		ToolSequence:      []string{},
	}
	ms.putSession(sess1)

	rd.LinkCreatedWatcher(ctx, "sess-1", "w-100")

	got := ms.sessions["sess-1"]
	if len(got.CreatedWatcherIDs) != 1 || got.CreatedWatcherIDs[0] != "w-100" {
		t.Errorf("CreatedWatcherIDs = %v, want [w-100]", got.CreatedWatcherIDs)
	}
	if got.RecurrenceGroup == nil || *got.RecurrenceGroup != "watcher:w-100" {
		t.Errorf("RecurrenceGroup = %v, want watcher:w-100", got.RecurrenceGroup)
	}

	// Close session 1.
	now := time.Now().Add(-1 * time.Hour)
	sess1.EndedAt = &now
	sess1.RecurrenceCount = 0

	// Session 2: triggered by same watcher — should detect recurrence.
	sess2 := &store.InvestigationSession{
		ID:           "sess-2",
		Status:       store.InvestigationStatusOpen,
		ToolSequence: []string{},
	}
	ms.putSession(sess2)

	rd.LinkTriggeredWatcher(ctx, "sess-2", "w-100")

	got2 := ms.sessions["sess-2"]
	if got2.TriggeredByWatcherID == nil || *got2.TriggeredByWatcherID != "w-100" {
		t.Errorf("TriggeredByWatcherID = %v, want w-100", got2.TriggeredByWatcherID)
	}
	if got2.PreviousSessionID == nil || *got2.PreviousSessionID != "sess-1" {
		t.Errorf("PreviousSessionID = %v, want sess-1", got2.PreviousSessionID)
	}
	if got2.RecurrenceCount != 1 {
		t.Errorf("RecurrenceCount = %d, want 1", got2.RecurrenceCount)
	}
	if got2.FixDurabilitySeconds == nil {
		t.Error("FixDurabilitySeconds should be set")
	}
}

func TestRecurrenceDetector_ErrorLinking(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	// Session 1: investigates and resolves an error.
	sess1 := &store.InvestigationSession{
		ID:                            "sess-1",
		Status:                        store.InvestigationStatusResolved,
		InvestigatedErrorFingerprints: []string{},
		ResolvedErrorGroupIDs:         []string{},
		ToolSequence:                  []string{},
	}
	ms.putSession(sess1)

	rd.LinkInvestigatedError(ctx, "sess-1", "fp-xyz")
	rd.LinkResolvedError(ctx, "sess-1", "fp-xyz")

	got := ms.sessions["sess-1"]
	if len(got.InvestigatedErrorFingerprints) != 1 {
		t.Errorf("InvestigatedErrorFingerprints = %v, want [fp-xyz]", got.InvestigatedErrorFingerprints)
	}
	if len(got.ResolvedErrorGroupIDs) != 1 {
		t.Errorf("ResolvedErrorGroupIDs = %v, want [fp-xyz]", got.ResolvedErrorGroupIDs)
	}

	// Close session 1.
	now := time.Now().Add(-2 * time.Hour)
	sess1.EndedAt = &now

	// Session 2: same error recurs.
	sess2 := &store.InvestigationSession{
		ID:                            "sess-2",
		Status:                        store.InvestigationStatusOpen,
		InvestigatedErrorFingerprints: []string{},
		ToolSequence:                  []string{},
	}
	ms.putSession(sess2)

	rd.LinkInvestigatedError(ctx, "sess-2", "fp-xyz")
	rd.DetectErrorRecurrence(ctx, "sess-2", "fp-xyz", 1)

	got2 := ms.sessions["sess-2"]
	if got2.PreviousSessionID == nil || *got2.PreviousSessionID != "sess-1" {
		t.Errorf("PreviousSessionID = %v, want sess-1", got2.PreviousSessionID)
	}
	if got2.RecurrenceCount != 1 {
		t.Errorf("RecurrenceCount = %d, want 1", got2.RecurrenceCount)
	}
}

func TestRecurrenceDetector_HealthcheckLinking(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	// Session 1: creates a health check.
	sess1 := &store.InvestigationSession{
		ID:                    "sess-1",
		Status:                store.InvestigationStatusOpen,
		CreatedHealthcheckIDs: []string{},
		ToolSequence:          []string{},
	}
	ms.putSession(sess1)

	rd.LinkCreatedHealthcheck(ctx, "sess-1", "hc-100")

	got := ms.sessions["sess-1"]
	if len(got.CreatedHealthcheckIDs) != 1 || got.CreatedHealthcheckIDs[0] != "hc-100" {
		t.Errorf("CreatedHealthcheckIDs = %v, want [hc-100]", got.CreatedHealthcheckIDs)
	}

	// Close session 1.
	now := time.Now().Add(-30 * time.Minute)
	sess1.EndedAt = &now

	// Session 2: triggered by same health check.
	sess2 := &store.InvestigationSession{
		ID:           "sess-2",
		Status:       store.InvestigationStatusOpen,
		ToolSequence: []string{},
	}
	ms.putSession(sess2)

	rd.LinkTriggeredHealthcheck(ctx, "sess-2", "hc-100")

	got2 := ms.sessions["sess-2"]
	if got2.TriggeredByHealthcheckID == nil || *got2.TriggeredByHealthcheckID != "hc-100" {
		t.Errorf("TriggeredByHealthcheckID = %v, want hc-100", got2.TriggeredByHealthcheckID)
	}
	if got2.PreviousSessionID == nil || *got2.PreviousSessionID != "sess-1" {
		t.Errorf("PreviousSessionID = %v, want sess-1", got2.PreviousSessionID)
	}
	if got2.RecurrenceCount != 1 {
		t.Errorf("RecurrenceCount = %d, want 1", got2.RecurrenceCount)
	}
}

func TestRecurrenceDetector_NoRecurrenceWhenNoPrevious(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	sess := &store.InvestigationSession{
		ID:           "sess-1",
		Status:       store.InvestigationStatusOpen,
		ToolSequence: []string{},
	}
	ms.putSession(sess)

	// Should not crash or set recurrence.
	rd.LinkTriggeredWatcher(ctx, "sess-1", "w-nonexistent")

	got := ms.sessions["sess-1"]
	if got.PreviousSessionID != nil {
		t.Errorf("PreviousSessionID should be nil, got %v", got.PreviousSessionID)
	}
	if got.RecurrenceCount != 0 {
		t.Errorf("RecurrenceCount should be 0, got %d", got.RecurrenceCount)
	}
}

func TestBuildEscalationNote(t *testing.T) {
	tests := []struct {
		name     string
		sess     *store.InvestigationSession
		wantSub  string
	}{
		{
			name: "no recurrence",
			sess: &store.InvestigationSession{RecurrenceCount: 0},
			wantSub: "",
		},
		{
			name: "first recurrence with short durability",
			sess: &store.InvestigationSession{
				RecurrenceCount:      1,
				FixDurabilitySeconds: intPtr(1800),
			},
			wantSub: "Recurrence #1. Last fix held for 30m.",
		},
		{
			name: "recurrence with hours",
			sess: &store.InvestigationSession{
				RecurrenceCount:      2,
				FixDurabilitySeconds: intPtr(7200),
			},
			wantSub: "Recurrence #2. Last fix held for 2h.",
		},
		{
			name: "recurrence with days",
			sess: &store.InvestigationSession{
				RecurrenceCount:      1,
				FixDurabilitySeconds: intPtr(172800),
			},
			wantSub: "Recurrence #1. Last fix held for 2d.",
		},
		{
			name: "high recurrence triggers root cause warning",
			sess: &store.InvestigationSession{
				RecurrenceCount:      3,
				FixDurabilitySeconds: intPtr(600),
			},
			wantSub: "consider root cause analysis",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note := BuildEscalationNote(tt.sess)
			if tt.wantSub == "" {
				if note != "" {
					t.Errorf("expected empty note, got %q", note)
				}
				return
			}
			if !containsSubstring(note, tt.wantSub) {
				t.Errorf("note %q should contain %q", note, tt.wantSub)
			}
		})
	}
}

func TestRecurrenceDetector_InjectRecurrenceContext(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	// Previous session.
	prev := &store.InvestigationSession{
		ID:             "sess-prev",
		Status:         store.InvestigationStatusResolved,
		Summary:        "Added index on users.email",
		RootCause:      "Missing database index",
		FixDescription: "CREATE INDEX idx_users_email ON users(email)",
		ToolSequence:   []string{},
	}
	ms.putSession(prev)

	// Current session with recurrence link.
	prevID := "sess-prev"
	dur := 3600
	current := &store.InvestigationSession{
		ID:                   "sess-current",
		Status:               store.InvestigationStatusOpen,
		RecurrenceCount:      1,
		PreviousSessionID:    &prevID,
		FixDurabilitySeconds: &dur,
		ToolSequence:         []string{},
	}
	ms.putSession(current)

	resp := map[string]any{"some_data": "value"}
	rd.InjectRecurrenceContext(ctx, "sess-current", resp)

	invCtx, ok := resp["investigation_context"].(map[string]any)
	if !ok {
		t.Fatal("expected investigation_context in response")
	}
	if invCtx["previous_session"] != "sess-prev" {
		t.Errorf("previous_session = %v, want sess-prev", invCtx["previous_session"])
	}
	if invCtx["previous_fix_summary"] != "Added index on users.email" {
		t.Errorf("previous_fix_summary = %v", invCtx["previous_fix_summary"])
	}
	if invCtx["previous_root_cause"] != "Missing database index" {
		t.Errorf("previous_root_cause = %v", invCtx["previous_root_cause"])
	}
	if _, ok := invCtx["escalation_note"]; !ok {
		t.Error("expected escalation_note in investigation_context")
	}
}

func TestRecurrenceDetector_InjectNoContext(t *testing.T) {
	ms := newMockSessionStore()
	rd := NewRecurrenceDetector(ms)
	ctx := context.Background()

	// Session without recurrence link.
	sess := &store.InvestigationSession{
		ID:           "sess-1",
		Status:       store.InvestigationStatusOpen,
		ToolSequence: []string{},
	}
	ms.putSession(sess)

	resp := map[string]any{"some_data": "value"}
	rd.InjectRecurrenceContext(ctx, "sess-1", resp)

	if _, ok := resp["investigation_context"]; ok {
		t.Error("should not inject context when no previous session")
	}
}

func intPtr(v int) *int { return &v }

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
