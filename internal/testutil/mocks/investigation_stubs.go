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

var _ store.MCPActivityStore = (*MCPActivityStore)(nil)
var _ store.AgentNoteStore = (*AgentNoteStore)(nil)

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
