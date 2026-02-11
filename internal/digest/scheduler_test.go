package digest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockDigestStore implements store.DigestStore for testing.
type mockDigestStore struct {
	digests []store.StoredDigest
}

func (m *mockDigestStore) Save(_ context.Context, d store.SaveDigestParams) error {
	m.digests = append(m.digests, store.StoredDigest{
		ID:             d.ID,
		Environment:    d.Environment,
		Status:         d.Status,
		PeriodStart:    d.PeriodStart,
		PeriodEnd:      d.PeriodEnd,
		AlertTotal:     d.AlertTotal,
		AlertCritical:  d.AlertCritical,
		AlertWarning:   d.AlertWarning,
		WatcherTotal:   d.WatcherTotal,
		WatcherErrored: d.WatcherErrored,
		FailedRuns:     d.FailedRuns,
		Data:           d.Data,
		CreatedAt:      time.Now(),
	})
	return nil
}

func (m *mockDigestStore) GetLatest(_ context.Context, environment string) (*store.StoredDigest, error) {
	for i := len(m.digests) - 1; i >= 0; i-- {
		if environment == "" || m.digests[i].Environment == environment {
			return &m.digests[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockDigestStore) List(_ context.Context, limit int, environment string) ([]store.StoredDigest, error) {
	var result []store.StoredDigest
	for _, d := range m.digests {
		if environment != "" && d.Environment != environment {
			continue
		}
		result = append(result, d)
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockDigestStore) DeleteOlderThan(_ context.Context, before time.Time) (int, error) {
	var kept []store.StoredDigest
	deleted := 0
	for _, d := range m.digests {
		if d.CreatedAt.Before(before) {
			deleted++
		} else {
			kept = append(kept, d)
		}
	}
	m.digests = kept
	return deleted, nil
}

func TestScheduler_GenerateAndStore(t *testing.T) {
	as := &mockAlertStore{}
	ws := &mockWatcherStore{}
	rs := &mockRunStore{}
	ds := &mockDigestStore{}

	builder := NewBuilder(as, ws, rs)
	sched := NewScheduler(SchedulerOpts{
		Builder:       builder,
		DigestStore:   ds,
		RetentionDays: 30,
	})

	err := sched.GenerateAndStore(context.Background(), "")
	if err != nil {
		t.Fatalf("GenerateAndStore: %v", err)
	}

	if len(ds.digests) != 1 {
		t.Fatalf("stored digests = %d, want 1", len(ds.digests))
	}

	stored := ds.digests[0]
	if stored.Status != "healthy" {
		t.Errorf("status = %q, want healthy", stored.Status)
	}

	// Verify data is valid JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stored.Data), &parsed); err != nil {
		t.Errorf("stored data is not valid JSON: %v", err)
	}
}

func TestScheduler_GenerateAndStore_WithEnvironment(t *testing.T) {
	as := &mockAlertStore{}
	ws := &mockWatcherStore{}
	rs := &mockRunStore{}
	ds := &mockDigestStore{}

	builder := NewBuilder(as, ws, rs)
	sched := NewScheduler(SchedulerOpts{
		Builder:       builder,
		DigestStore:   ds,
		RetentionDays: 30,
	})

	err := sched.GenerateAndStore(context.Background(), "production")
	if err != nil {
		t.Fatalf("GenerateAndStore: %v", err)
	}

	if ds.digests[0].Environment != "production" {
		t.Errorf("environment = %q, want production", ds.digests[0].Environment)
	}
}

func TestScheduler_StartStop(t *testing.T) {
	as := &mockAlertStore{}
	ws := &mockWatcherStore{}
	rs := &mockRunStore{}
	ds := &mockDigestStore{}

	builder := NewBuilder(as, ws, rs)
	sched := NewScheduler(SchedulerOpts{
		Builder:       builder,
		DigestStore:   ds,
		RetentionDays: 30,
	})

	ctx := context.Background()
	sched.Start(ctx)

	// Give it a moment to generate the initial digest
	time.Sleep(100 * time.Millisecond)

	sched.Stop()

	// Should have generated at least one digest on startup
	if len(ds.digests) < 1 {
		t.Errorf("expected at least 1 digest after start, got %d", len(ds.digests))
	}
}

func TestScheduler_DefaultRetention(t *testing.T) {
	sched := NewScheduler(SchedulerOpts{
		Builder:       NewBuilder(&mockAlertStore{}, &mockWatcherStore{}, &mockRunStore{}),
		DigestStore:   &mockDigestStore{},
		RetentionDays: 0, // should default to 30
	})

	if sched.retentionDays != 30 {
		t.Errorf("retentionDays = %d, want 30 (default)", sched.retentionDays)
	}
}
