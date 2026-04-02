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
