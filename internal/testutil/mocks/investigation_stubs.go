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

var _ store.InvestigationSessionStore = (*InvestigationSessionStore)(nil)
var _ store.MCPActivityStore = (*MCPActivityStore)(nil)
var _ store.AgentNoteStore = (*AgentNoteStore)(nil)

// ===========================================================================
// InvestigationSessionStore
// ===========================================================================

// InvestigationSessionStore is a stub implementing store.InvestigationSessionStore.
type InvestigationSessionStore struct{}

// NewInvestigationSessionStore returns an initialised InvestigationSessionStore stub.
func NewInvestigationSessionStore() *InvestigationSessionStore {
	return &InvestigationSessionStore{}
}

func (m *InvestigationSessionStore) Create(_ context.Context, _ store.CreateInvestigationSessionParams) (*store.InvestigationSession, error) {
	return &store.InvestigationSession{ID: "test-session"}, nil
}
func (m *InvestigationSessionStore) GetByID(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, store.ErrNotFound
}
func (m *InvestigationSessionStore) Close(_ context.Context, _ string) error { return nil }
func (m *InvestigationSessionStore) Update(_ context.Context, _ string, _ store.UpdateInvestigationSessionParams) error {
	return nil
}
func (m *InvestigationSessionStore) FindRecent(_ context.Context, _ store.FindRecentSessionParams) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) List(_ context.Context, _ store.ListInvestigationSessionParams) ([]store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) Stats(_ context.Context) (*store.InvestigationSessionStats, error) {
	return &store.InvestigationSessionStats{}, nil
}
func (m *InvestigationSessionStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *InvestigationSessionStore) RecordStep(_ context.Context, _ string, _ string, _ bool) error {
	return nil
}
func (m *InvestigationSessionStore) FindByCreatedWatcher(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindByResolvedError(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindByCreatedHealthcheck(_ context.Context, _ string) (*store.InvestigationSession, error) {
	return nil, nil
}
func (m *InvestigationSessionStore) FindSimilar(_ context.Context, _ store.FindSimilarParams) ([]store.InvestigationSession, error) {
	return nil, nil
}

// ===========================================================================
// MCPActivityStore
// ===========================================================================

// MCPActivityStore is a stub implementing store.MCPActivityStore.
type MCPActivityStore struct {
	mu     sync.Mutex
	Events []store.MCPActivityEvent
}

// NewMCPActivityStore returns an initialised MCPActivityStore stub.
func NewMCPActivityStore() *MCPActivityStore {
	return &MCPActivityStore{}
}

func (m *MCPActivityStore) Log(_ context.Context, params store.LogMCPActivityParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Events = append(m.Events, store.MCPActivityEvent{
		SessionID: params.SessionID,
		UserID:    params.UserID,
		ToolName:  params.ToolName,
		IsError:   params.IsError,
		EventType: params.EventType,
		CreatedAt: time.Now(),
	})
	return nil
}

func (m *MCPActivityStore) Stats(_ context.Context) (*store.MCPActivityStats, error) {
	return &store.MCPActivityStats{}, nil
}

func (m *MCPActivityStore) Recent(_ context.Context, limit int) ([]store.MCPActivityEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit > len(m.Events) {
		limit = len(m.Events)
	}
	return m.Events[:limit], nil
}

func (m *MCPActivityStore) ListByInvestigationSession(_ context.Context, _ string) ([]store.MCPActivityEvent, error) {
	return nil, nil
}

func (m *MCPActivityStore) Prune(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}

func (m *MCPActivityStore) SetSuggestionTracking(_ context.Context, _ string, _ int, _ bool, _ int) error {
	return nil
}

func (m *MCPActivityStore) UpdateFollowedBy(_ context.Context, _ string, _ int, _ string) error {
	return nil
}

// ===========================================================================
// AgentNoteStore
// ===========================================================================

// AgentNoteStore is a stub implementing store.AgentNoteStore.
type AgentNoteStore struct{}

// NewAgentNoteStore returns an initialised AgentNoteStore stub.
func NewAgentNoteStore() *AgentNoteStore { return &AgentNoteStore{} }

func (m *AgentNoteStore) Upsert(_ context.Context, _, _, _ string) (*store.AgentNote, error) {
	return &store.AgentNote{}, nil
}
func (m *AgentNoteStore) Get(_ context.Context, _, _ string) (*store.AgentNote, error) {
	return nil, store.ErrNotFound
}
func (m *AgentNoteStore) List(_ context.Context, _ string) ([]store.AgentNote, error) {
	return nil, nil
}
func (m *AgentNoteStore) Delete(_ context.Context, _, _ string) error             { return nil }
func (m *AgentNoteStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }
