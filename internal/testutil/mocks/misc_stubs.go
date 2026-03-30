package mocks

import (
	"context"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.AuditStore = (*AuditStore)(nil)
var _ store.JourneyStore = (*JourneyStore)(nil)
var _ store.WorkflowTemplateStore = (*WorkflowTemplateStore)(nil)
var _ store.ToolTransitionStore = (*ToolTransitionStore)(nil)

// ===========================================================================
// AuditStore
// ===========================================================================

// AuditStore is a stub implementing store.AuditStore.
type AuditStore struct {
	mu      sync.Mutex
	Entries []store.AuditEntry
}

// NewAuditStore returns an initialised AuditStore stub.
func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

func (m *AuditStore) Log(_ context.Context, params store.LogAuditParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = append(m.Entries, store.AuditEntry{
		UserID:     params.UserID,
		UserEmail:  params.UserEmail,
		Action:     params.Action,
		TargetType: params.TargetType,
		TargetID:   params.TargetID,
		Details:    params.Details,
		IPAddress:  params.IPAddress,
		CreatedAt:  time.Now(),
	})
	return nil
}

func (m *AuditStore) Recent(_ context.Context, limit int) ([]store.AuditEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.Entries) {
		limit = len(m.Entries)
	}
	return m.Entries[:limit], nil
}

func (m *AuditStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// JourneyStore
// ===========================================================================

// JourneyStore is a stub implementing store.JourneyStore.
type JourneyStore struct{}

// NewJourneyStore returns an initialised JourneyStore stub.
func NewJourneyStore() *JourneyStore { return &JourneyStore{} }

func (m *JourneyStore) BuildSessions(_ context.Context, _ time.Time) error { return nil }
func (m *JourneyStore) GetSession(_ context.Context, _ string) (*store.UserSession, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) ListSessions(_ context.Context, _ store.SessionListParams) ([]store.UserSession, error) {
	return nil, nil
}
func (m *JourneyStore) GetSessionRequests(_ context.Context, _ string) ([]store.RequestStep, error) {
	return nil, nil
}
func (m *JourneyStore) GetUserJourney(_ context.Context, _ string, _ time.Time, _ int) ([]store.RequestStep, error) {
	return nil, nil
}
func (m *JourneyStore) CommonPaths(_ context.Context, _ store.PathAnalysisParams) ([]store.PathFrequency, error) {
	return nil, nil
}
func (m *JourneyStore) CreateFunnel(_ context.Context, f store.Funnel) (*store.Funnel, error) {
	f.ID = 1
	return &f, nil
}
func (m *JourneyStore) GetFunnel(_ context.Context, _ int64) (*store.Funnel, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) ListFunnels(_ context.Context) ([]store.Funnel, error) { return nil, nil }
func (m *JourneyStore) AnalyzeFunnel(_ context.Context, _ int64, _ time.Time) (*store.FunnelResult, error) {
	return &store.FunnelResult{}, nil
}
func (m *JourneyStore) DeleteFunnel(_ context.Context, _ int64) error { return nil }
func (m *JourneyStore) GetRequestTimeline(_ context.Context, _ int64) (*store.RequestTimeline, error) {
	return nil, store.ErrNotFound
}
func (m *JourneyStore) GetSessionTimeline(_ context.Context, _ string) ([]store.RequestTimeline, error) {
	return nil, nil
}

// ===========================================================================
// WorkflowTemplateStore
// ===========================================================================

// WorkflowTemplateStore is a stub implementing store.WorkflowTemplateStore.
type WorkflowTemplateStore struct{}

// NewWorkflowTemplateStore returns an initialised WorkflowTemplateStore stub.
func NewWorkflowTemplateStore() *WorkflowTemplateStore { return &WorkflowTemplateStore{} }

func (m *WorkflowTemplateStore) Seed(_ context.Context, _ []store.WorkflowTemplate) error {
	return nil
}
func (m *WorkflowTemplateStore) GetNextStep(_ context.Context, _ string, _ int) ([]store.WorkflowTemplate, error) {
	return nil, nil
}
func (m *WorkflowTemplateStore) GetByName(_ context.Context, _ string) ([]store.WorkflowTemplate, error) {
	return nil, nil
}
func (m *WorkflowTemplateStore) List(_ context.Context, _ string) ([]store.WorkflowTemplate, error) {
	return nil, nil
}

// ===========================================================================
// ToolTransitionStore
// ===========================================================================

// ToolTransitionStore is a stub implementing store.ToolTransitionStore.
type ToolTransitionStore struct{}

// NewToolTransitionStore returns an initialised ToolTransitionStore stub.
func NewToolTransitionStore() *ToolTransitionStore { return &ToolTransitionStore{} }

func (m *ToolTransitionStore) Increment(_ context.Context, _, _, _ string) error { return nil }
func (m *ToolTransitionStore) IncrementWithOutcome(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (m *ToolTransitionStore) GetTransitions(_ context.Context, _ store.GetTransitionsParams) ([]store.ToolTransition, error) {
	return nil, nil
}
func (m *ToolTransitionStore) GetDeadEnds(_ context.Context, _ string) ([]store.ToolTransition, error) {
	return nil, nil
}
func (m *ToolTransitionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
