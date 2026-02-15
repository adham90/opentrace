package mcp

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

// mockAlertStore implements store.AlertStore for MCP tests.
type mockAlertStore struct {
	alerts []store.Alert
}

func (m *mockAlertStore) Create(_ context.Context, _ store.CreateAlertParams) (*store.Alert, error) {
	return nil, nil
}
func (m *mockAlertStore) GetByID(_ context.Context, _ uuid.UUID) (*store.Alert, error) {
	return nil, store.ErrNotFound
}
func (m *mockAlertStore) List(_ context.Context, _ store.ListAlertParams) ([]store.Alert, error) {
	return m.alerts, nil
}
func (m *mockAlertStore) CountUnread(_ context.Context) (int, error) { return 0, nil }
func (m *mockAlertStore) CountTotal(_ context.Context) (int, error)  { return len(m.alerts), nil }
func (m *mockAlertStore) CountBySeverity(_ context.Context, since, until time.Time) (map[string]int, error) {
	counts := make(map[string]int)
	for _, a := range m.alerts {
		if a.CreatedAt.Before(since) || a.CreatedAt.After(until) {
			continue
		}
		counts[string(a.Severity)]++
	}
	return counts, nil
}
func (m *mockAlertStore) MarkRead(_ context.Context, _ uuid.UUID) error    { return nil }
func (m *mockAlertStore) MarkAllRead(_ context.Context) error              { return nil }
func (m *mockAlertStore) Dismiss(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockAlertStore) DismissAll(_ context.Context) error               { return nil }
func (m *mockAlertStore) Snooze(_ context.Context, _ uuid.UUID, _ time.Time) error { return nil }
func (m *mockAlertStore) Unsnooze(_ context.Context, _ uuid.UUID) error    { return nil }
func (m *mockAlertStore) WatcherAlertStats(_ context.Context, _ uuid.UUID, _ time.Time) (*store.WatcherEffectiveness, error) {
	return nil, nil
}
func (m *mockAlertStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

// mockWatcherStore implements store.WatcherStore for MCP tests.
type mockWatcherStore struct{}

func (m *mockWatcherStore) Create(_ context.Context, _ store.CreateWatcherParams) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) GetByID(_ context.Context, _ uuid.UUID) (*store.Watcher, error) {
	return nil, store.ErrNotFound
}
func (m *mockWatcherStore) List(_ context.Context, _ store.ListWatcherParams) ([]store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Update(_ context.Context, _ uuid.UUID, _ store.UpdateWatcherParams) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateStatus(_ context.Context, _ uuid.UUID, _ store.WatcherStatus) (*store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockWatcherStore) GetDueWatchers(_ context.Context) ([]store.Watcher, error) {
	return nil, nil
}
func (m *mockWatcherStore) UpdateRunTime(_ context.Context, _ uuid.UUID, _, _ time.Time) error {
	return nil
}
func (m *mockWatcherStore) UpdateAdaptiveState(_ context.Context, _ uuid.UUID, _ store.UpdateAdaptiveParams) error {
	return nil
}
func (m *mockWatcherStore) ResumeWatcher(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockWatcherStore) ExpireWatchers(_ context.Context) (int, error)      { return 0, nil }
