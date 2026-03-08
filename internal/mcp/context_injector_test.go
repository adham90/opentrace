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
	if len(data) > 4400 { // allow some margin over 4096
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

// --- Mock stores for Stage 4 context sections ---

type mockNoteStoreForContext struct {
	notes []store.AgentNote
}

func (m *mockNoteStoreForContext) Upsert(_ context.Context, entityType, entityID, note string) (*store.AgentNote, error) {
	n := store.AgentNote{EntityType: entityType, EntityID: entityID, Note: note}
	m.notes = append(m.notes, n)
	return &n, nil
}
func (m *mockNoteStoreForContext) Get(_ context.Context, entityType, entityID string) (*store.AgentNote, error) {
	for _, n := range m.notes {
		if n.EntityType == entityType && n.EntityID == entityID {
			return &n, nil
		}
	}
	return nil, nil
}
func (m *mockNoteStoreForContext) List(_ context.Context, entityType string) ([]store.AgentNote, error) {
	var result []store.AgentNote
	for _, n := range m.notes {
		if n.EntityType == entityType {
			result = append(result, n)
		}
	}
	return result, nil
}
func (m *mockNoteStoreForContext) Delete(_ context.Context, _, _ string) error { return nil }
func (m *mockNoteStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type mockRunbookStoreForContext struct {
	best *store.RunbookEffectiveness
}

func (m *mockRunbookStoreForContext) RecordExecution(_ context.Context, _ string) error { return nil }
func (m *mockRunbookStoreForContext) UpdateOutcome(_ context.Context, _ store.UpdateRunbookEffectivenessParams) error {
	return nil
}
func (m *mockRunbookStoreForContext) GetMostEffective(_ context.Context) (*store.RunbookEffectiveness, error) {
	return m.best, nil
}
func (m *mockRunbookStoreForContext) List(_ context.Context) ([]store.RunbookEffectiveness, error) {
	return nil, nil
}

type mockQueryMemoryStoreForContext struct {
	entries map[string]*store.QueryMemory
}

func (m *mockQueryMemoryStoreForContext) Get(_ context.Context, fp string) (*store.QueryMemory, error) {
	if qm, ok := m.entries[fp]; ok {
		return qm, nil
	}
	return nil, store.ErrNotFound
}
func (m *mockQueryMemoryStoreForContext) Upsert(_ context.Context, _ store.UpsertQueryMemoryParams) error {
	return nil
}
func (m *mockQueryMemoryStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type mockAnalyticsStoreForContext struct {
	summary *store.TrafficSummary
	heatmap []store.HeatmapCell
}

func (m *mockAnalyticsStoreForContext) AggregateEndpointStats(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *mockAnalyticsStoreForContext) UpdateTrafficHeatmap(_ context.Context, _ time.Time) error {
	return nil
}
func (m *mockAnalyticsStoreForContext) TopEndpoints(_ context.Context, _ store.TopEndpointParams) ([]store.EndpointStat, error) {
	return nil, nil
}
func (m *mockAnalyticsStoreForContext) TrafficSummary(_ context.Context, _ store.AnalyticsParams) (*store.TrafficSummary, error) {
	return m.summary, nil
}
func (m *mockAnalyticsStoreForContext) TrafficHeatmap(_ context.Context, _ string) ([]store.HeatmapCell, error) {
	return m.heatmap, nil
}
func (m *mockAnalyticsStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type mockAuditStoreForContext struct {
	entries []store.AuditEntry
}

func (m *mockAuditStoreForContext) Log(_ context.Context, _ store.LogAuditParams) error { return nil }
func (m *mockAuditStoreForContext) Recent(_ context.Context, limit int) ([]store.AuditEntry, error) {
	if limit > len(m.entries) {
		limit = len(m.entries)
	}
	return m.entries[:limit], nil
}
func (m *mockAuditStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

type mockTrendStoreForContext struct {
	markers []store.DeployMarker
}

func (m *mockTrendStoreForContext) AggregateBuckets(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *mockTrendStoreForContext) QueryTrends(_ context.Context, _ store.TrendQueryParams) ([]store.MetricBucket, error) {
	return nil, nil
}
func (m *mockTrendStoreForContext) ListDeployMarkers(_ context.Context, _ string, _ time.Time) ([]store.DeployMarker, error) {
	return m.markers, nil
}
func (m *mockTrendStoreForContext) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func TestContextInjector_RelevantNotes(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	ns := &mockNoteStoreForContext{
		notes: []store.AgentNote{
			{EntityType: "service", EntityID: "api", Note: "Known issue with connection pool"},
			{EntityType: "service", EntityID: "web", Note: "Unrelated service note"},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{NoteStore: ns})

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
	if len(ic.RelevantNotes) == 0 {
		t.Fatal("expected relevant notes")
	}
	if ic.RelevantNotes[0].EntityID != "api" {
		t.Errorf("note entity_id = %q, want api", ic.RelevantNotes[0].EntityID)
	}
	if ic.RelevantNotes[0].Note != "Known issue with connection pool" {
		t.Errorf("note = %q", ic.RelevantNotes[0].Note)
	}
}

func TestContextInjector_RunbookSuggestion(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	rs := &mockRunbookStoreForContext{
		best: &store.RunbookEffectiveness{
			RunbookName:      "slow_query_playbook",
			TotalExecutions:  10,
			ResolvedSessions: 8,
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{RunbookStore: rs})

	// Session at step 0 should get suggestion
	sess := &store.InvestigationSession{
		ID:         "current",
		Intent:     IntentInvestigation,
		Status:     store.InvestigationStatusOpen,
		TotalSteps: 0,
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic == nil {
		t.Fatal("expected context")
	}
	if ic.RunbookSuggestion == nil {
		t.Fatal("expected runbook suggestion")
	}
	if ic.RunbookSuggestion.RunbookName != "slow_query_playbook" {
		t.Errorf("runbook = %q", ic.RunbookSuggestion.RunbookName)
	}
	if ic.RunbookSuggestion.ResolutionRate != 0.8 {
		t.Errorf("rate = %f, want 0.8", ic.RunbookSuggestion.ResolutionRate)
	}

	// Session at step > 0 should NOT get suggestion
	sess.TotalSteps = 3
	ic2 := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic2 != nil && ic2.RunbookSuggestion != nil {
		t.Error("should not suggest runbook after step 0")
	}
}

func TestContextInjector_RunbookSuggestion_LowRate(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	rs := &mockRunbookStoreForContext{
		best: &store.RunbookEffectiveness{
			RunbookName:      "bad_playbook",
			TotalExecutions:  10,
			ResolvedSessions: 3, // 30% — below 50% threshold
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{RunbookStore: rs})

	sess := &store.InvestigationSession{
		ID:         "current",
		Intent:     IntentInvestigation,
		Status:     store.InvestigationStatusOpen,
		TotalSteps: 0,
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic != nil && ic.RunbookSuggestion != nil {
		t.Error("should not suggest runbook with <50% resolution rate")
	}
}

func TestContextInjector_QueryMemory(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	qms := &mockQueryMemoryStoreForContext{
		entries: map[string]*store.QueryMemory{
			"SELECT * FROM users WHERE id = ?": {
				Fingerprint:        "SELECT * FROM users WHERE id = ?",
				InvestigationCount: 3,
				LastRootCause:      "Missing index on users.id",
				LastFix:            "Added index",
			},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{QueryMemoryStore: qms})

	sess := &store.InvestigationSession{
		ID:               "current",
		Intent:           IntentInvestigation,
		Status:           store.InvestigationStatusOpen,
		ExplainedQueries: []string{"SELECT * FROM users WHERE id = ?"},
	}

	ic := ci.BuildContext(context.Background(), sess, "explain_query")
	if ic == nil {
		t.Fatal("expected context")
	}
	if len(ic.QueryMemory) == 0 {
		t.Fatal("expected query memory")
	}
	if ic.QueryMemory[0].InvestigationCount != 3 {
		t.Errorf("count = %d, want 3", ic.QueryMemory[0].InvestigationCount)
	}
	if ic.QueryMemory[0].LastRootCause != "Missing index on users.id" {
		t.Errorf("root_cause = %q", ic.QueryMemory[0].LastRootCause)
	}
}

func TestContextInjector_DeployCorrelation(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	trs := &mockTrendStoreForContext{
		markers: []store.DeployMarker{
			{
				Service:     "api",
				CommitHash:  "abc123",
				Environment: "production",
				FirstSeenAt: time.Now().Add(-20 * time.Minute),
			},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{TrendStore: trs})

	sess := &store.InvestigationSession{
		ID:               "current",
		Intent:           IntentInvestigation,
		Status:           store.InvestigationStatusOpen,
		PrimaryService:   "api",
		CorrelatedDeploy: "abc123",
		StartedAt:        time.Now(),
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic == nil {
		t.Fatal("expected context")
	}
	if ic.DeployCorrelation == nil {
		t.Fatal("expected deploy correlation")
	}
	if ic.DeployCorrelation.CommitHash != "abc123" {
		t.Errorf("commit = %q", ic.DeployCorrelation.CommitHash)
	}
	if ic.DeployCorrelation.Service != "api" {
		t.Errorf("service = %q", ic.DeployCorrelation.Service)
	}
}

func TestContextInjector_TrafficContext(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	as := &mockAnalyticsStoreForContext{
		summary: &store.TrafficSummary{
			TotalRequests: 5000,
			ErrorRate:     0.05,
		},
		heatmap: []store.HeatmapCell{
			{
				DayOfWeek:    int(time.Now().UTC().Weekday()),
				HourOfDay:    time.Now().UTC().Hour(),
				RequestCount: 2000, // 5000/2000 = 2.5x — anomaly
			},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{AnalyticsStore: as})

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
	if ic.TrafficContext == nil {
		t.Fatal("expected traffic context")
	}
	if ic.TrafficContext.TotalRequests != 5000 {
		t.Errorf("requests = %d, want 5000", ic.TrafficContext.TotalRequests)
	}
	if ic.TrafficContext.Anomaly == "" {
		t.Error("expected anomaly warning (2.5x higher)")
	}
}

func TestContextInjector_TrafficContext_NoAnomaly(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}
	as := &mockAnalyticsStoreForContext{
		summary: &store.TrafficSummary{
			TotalRequests: 1000,
			ErrorRate:     0.01,
		},
		heatmap: []store.HeatmapCell{
			{
				DayOfWeek:    int(time.Now().UTC().Weekday()),
				HourOfDay:    time.Now().UTC().Hour(),
				RequestCount: 900, // ~1.1x — no anomaly
			},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{AnalyticsStore: as})

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
	if ic.TrafficContext.Anomaly != "" {
		t.Errorf("unexpected anomaly: %q", ic.TrafficContext.Anomaly)
	}
}

func TestContextInjector_RecentAdminActions(t *testing.T) {
	ms := newMockSessionStoreForContext()
	ts := &mockToolTransitionStore{}

	now := time.Now().UTC()
	as := &mockAuditStoreForContext{
		entries: []store.AuditEntry{
			{
				Action:     "connector.update",
				TargetType: "connector",
				UserEmail:  "admin@example.com",
				CreatedAt:  now.Add(-30 * time.Minute), // within 1h window
			},
			{
				Action:     "settings.update",
				TargetType: "settings",
				UserEmail:  "admin@example.com",
				CreatedAt:  now.Add(-2 * time.Hour), // outside 1h window
			},
		},
	}

	ci := NewContextInjector(ms, ts, ContextInjectorDeps{AuditStore: as})

	sess := &store.InvestigationSession{
		ID:             "current",
		Intent:         IntentInvestigation,
		Status:         store.InvestigationStatusOpen,
		PrimaryService: "api",
		StartedAt:      now,
	}

	ic := ci.BuildContext(context.Background(), sess, "diagnose")
	if ic == nil {
		t.Fatal("expected context")
	}
	if len(ic.RecentAdminActions) != 1 {
		t.Fatalf("admin actions = %d, want 1", len(ic.RecentAdminActions))
	}
	if ic.RecentAdminActions[0].Action != "connector.update" {
		t.Errorf("action = %q", ic.RecentAdminActions[0].Action)
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
