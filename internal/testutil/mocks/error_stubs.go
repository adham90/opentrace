package mocks

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ store.ErrorGroupStore = (*ErrorGroupStore)(nil)
var _ store.ErrorImpactStore = (*ErrorImpactStore)(nil)

// ===========================================================================
// ErrorGroupStore
// ===========================================================================

// ErrorGroupStore is a stub implementing store.ErrorGroupStore.
type ErrorGroupStore struct {
	mu     sync.Mutex
	Groups map[string]*store.ErrorGroup
}

// NewErrorGroupStore returns an initialised ErrorGroupStore stub.
func NewErrorGroupStore() *ErrorGroupStore {
	return &ErrorGroupStore{Groups: make(map[string]*store.ErrorGroup)}
}

func (m *ErrorGroupStore) Upsert(_ context.Context, _ store.LogEntry) error { return nil }

// Get honours environment the way the real store does: a concrete env must
// match the stored row, "" matches anything. Tests for env scoping depend on
// this — a stub that ignored env would pass them no matter what the handler did.
func (m *ErrorGroupStore) Get(_ context.Context, fingerprint, environment string) (*store.ErrorGroup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.Groups[fingerprint]
	if !ok {
		return nil, store.ErrNotFound
	}
	if environment != "" && g.Environment != environment {
		return nil, store.ErrNotFound
	}
	return g, nil
}
func (m *ErrorGroupStore) List(_ context.Context, _ store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Count(_ context.Context, _ store.ErrorGroupStatus, _ string) (int, error) {
	return 0, nil
}
func (m *ErrorGroupStore) Resolve(_ context.Context, _, _, _ string) error { return nil }
func (m *ErrorGroupStore) Ignore(_ context.Context, _, _, _ string) error  { return nil }
func (m *ErrorGroupStore) Reopen(_ context.Context, _, _, _ string) error  { return nil }
func (m *ErrorGroupStore) ListEvents(_ context.Context, _, _ string, _ int) ([]store.ErrorGroupEvent, error) {
	return nil, nil
}
func (m *ErrorGroupStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// ===========================================================================
// ErrorImpactStore
// ===========================================================================

// ErrorImpactStore is a stub implementing store.ErrorImpactStore.
type ErrorImpactStore struct{}

// NewErrorImpactStore returns an initialised ErrorImpactStore stub.
func NewErrorImpactStore() *ErrorImpactStore { return &ErrorImpactStore{} }

func (m *ErrorImpactStore) TrackImpact(_ context.Context, _ string, _ string, _ string, _ map[string]any, _ int64, _ string) error {
	return nil
}
func (m *ErrorImpactStore) GetImpact(_ context.Context, _ string, _ string) (*store.ErrorImpact, error) {
	return nil, store.ErrNotFound
}
func (m *ErrorImpactStore) GetAffectedUsers(_ context.Context, _ string, _ int) ([]store.AffectedUser, error) {
	return nil, nil
}
func (m *ErrorImpactStore) GetUserErrors(_ context.Context, _ string, _ time.Time) ([]store.ErrorSummary, error) {
	return nil, nil
}
func (m *ErrorImpactStore) ComputeImpactScores(_ context.Context) error { return nil }
func (m *ErrorImpactStore) TopByImpact(_ context.Context, _ store.ImpactQueryParams) ([]store.ErrorGroupWithImpact, error) {
	return nil, nil
}
func (m *ErrorImpactStore) FindCommonTraits(_ context.Context, _ string) (map[string]any, error) {
	return nil, nil
}

// IssueURL mirrors the real store: an unfiled group yields "", a missing one
// yields ErrNotFound. Tests for issue dedupe depend on that distinction.
func (m *ErrorGroupStore) IssueURL(_ context.Context, fingerprint, environment string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.Groups[fingerprint]
	if !ok {
		return "", store.ErrNotFound
	}
	if environment != "" && g.Environment != environment {
		return "", store.ErrNotFound
	}
	return g.IssueURL, nil
}

// SetIssueURL claims the group only when it has no issue yet, matching the
// real store's conditional UPDATE — a second filer must not overwrite the
// first and orphan its issue.
func (m *ErrorGroupStore) SetIssueURL(_ context.Context, fingerprint, environment, url string) error {
	if url == "" {
		return errors.New("issue url is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.Groups[fingerprint]
	if !ok {
		return store.ErrNotFound
	}
	if environment != "" && g.Environment != environment {
		return store.ErrNotFound
	}
	if g.IssueURL == "" {
		g.IssueURL = url
	}
	return nil
}
