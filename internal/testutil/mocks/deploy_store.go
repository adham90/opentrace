package mocks

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

var _ store.DeployStore = (*DeployStore)(nil)

// DeployStore is a thread-safe in-memory mock implementing store.DeployStore.
type DeployStore struct {
	mu      sync.Mutex
	Deploys []store.Deploy
	nextID  int64
}

// NewDeployStore returns an initialised DeployStore mock.
func NewDeployStore() *DeployStore {
	return &DeployStore{}
}

// Record mirrors the SQL store's INSERT OR IGNORE: a commit already recorded
// for the same (service, environment) is a no-op, not a duplicate row.
func (m *DeployStore) Record(_ context.Context, d store.Deploy) error {
	if d.CommitHash == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.Deploys {
		if existing.CommitHash == d.CommitHash &&
			existing.Service == d.Service &&
			existing.Environment == d.Environment {
			return nil
		}
	}
	if d.FirstSeenAt.IsZero() {
		d.FirstSeenAt = time.Now()
	}
	m.nextID++
	d.ID = m.nextID
	m.Deploys = append(m.Deploys, d)
	return nil
}

func (m *DeployStore) Latest(ctx context.Context, service, environment string) (*store.Deploy, error) {
	list, err := m.List(ctx, store.ListDeployParams{Service: service, Environment: environment, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, store.ErrNotFound
	}
	d := list[0]
	return &d, nil
}

func (m *DeployStore) List(_ context.Context, params store.ListDeployParams) ([]store.Deploy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var out []store.Deploy
	for _, d := range m.Deploys {
		if params.Service != "" && d.Service != params.Service {
			continue
		}
		if params.Environment != "" && d.Environment != params.Environment {
			continue
		}
		if params.Since != nil && !params.Since.IsZero() && d.FirstSeenAt.Before(*params.Since) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstSeenAt.Equal(out[j].FirstSeenAt) {
			return out[i].FirstSeenAt.After(out[j].FirstSeenAt)
		}
		return out[i].ID > out[j].ID
	})
	if params.Limit > 0 && len(out) > params.Limit {
		out = out[:params.Limit]
	}
	return out, nil
}

func (m *DeployStore) Prune(_ context.Context, olderThan time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	kept := m.Deploys[:0]
	var removed int64
	for _, d := range m.Deploys {
		if d.FirstSeenAt.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, d)
	}
	m.Deploys = kept
	return removed, nil
}
