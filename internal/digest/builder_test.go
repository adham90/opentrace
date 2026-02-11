package digest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// --- mock stores for builder tests ---

type mockAlertStore struct {
	alerts []store.Alert
}

func (m *mockAlertStore) Create(_ context.Context, params store.CreateAlertParams) (*store.Alert, error) {
	sev := params.Severity
	if sev == "" {
		sev = store.SeverityWarning
	}
	a := &store.Alert{
		ID: uuid.New(), WatcherID: params.WatcherID, RunID: params.RunID,
		Title: params.Title, Summary: params.Summary, Environment: params.Environment,
		Severity: sev, Details: params.Details, CreatedAt: time.Now(),
	}
	m.alerts = append(m.alerts, *a)
	return a, nil
}

func (m *mockAlertStore) List(_ context.Context, params store.ListAlertParams) ([]store.Alert, error) {
	var result []store.Alert
	for _, a := range m.alerts {
		if params.Environment != "" && a.Environment != params.Environment {
			continue
		}
		if params.WatcherID != nil && (a.WatcherID == nil || *a.WatcherID != *params.WatcherID) {
			continue
		}
		if a.Dismissed {
			continue
		}
		result = append(result, a)
	}
	if params.Limit > 0 && len(result) > params.Limit {
		result = result[:params.Limit]
	}
	return result, nil
}

func (m *mockAlertStore) CountUnread(_ context.Context, environment string) (int, error) {
	count := 0
	for _, a := range m.alerts {
		if !a.Read && !a.Dismissed {
			if environment != "" && a.Environment != environment {
				continue
			}
			count++
		}
	}
	return count, nil
}

func (m *mockAlertStore) CountTotal(_ context.Context, environment string) (int, error) {
	count := 0
	for _, a := range m.alerts {
		if !a.Dismissed {
			if environment != "" && a.Environment != environment {
				continue
			}
			count++
		}
	}
	return count, nil
}

func (m *mockAlertStore) CountBySeverity(_ context.Context, since, until time.Time, environment string) (map[string]int, error) {
	result := make(map[string]int)
	for _, a := range m.alerts {
		if a.Dismissed {
			continue
		}
		if a.CreatedAt.Before(since) || !a.CreatedAt.Before(until) {
			continue
		}
		if environment != "" && a.Environment != environment {
			continue
		}
		result[string(a.Severity)]++
	}
	return result, nil
}

func (m *mockAlertStore) GetByID(_ context.Context, id uuid.UUID) (*store.Alert, error) {
	for i := range m.alerts {
		if m.alerts[i].ID == id {
			return &m.alerts[i], nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *mockAlertStore) MarkRead(_ context.Context, _ uuid.UUID) error                { return nil }
func (m *mockAlertStore) MarkAllRead(_ context.Context, _ string) error                 { return nil }
func (m *mockAlertStore) Dismiss(_ context.Context, _ uuid.UUID) error                  { return nil }
func (m *mockAlertStore) DismissAll(_ context.Context, _ string) error                  { return nil }
func (m *mockAlertStore) Prune(_ context.Context, _ time.Duration) (int64, error)       { return 0, nil }

type mockWatcherStore struct {
	watchers []store.Watcher
}

func (m *mockWatcherStore) Create(_ context.Context, params store.CreateWatcherParams) (*store.Watcher, error) {
	w := &store.Watcher{
		ID: uuid.New(), Title: params.Title, Environment: params.Environment,
		Severity: params.Severity, Status: store.WatcherActive,
		Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}"),
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	m.watchers = append(m.watchers, *w)
	return w, nil
}

func (m *mockWatcherStore) GetByID(_ context.Context, id uuid.UUID) (*store.Watcher, error) {
	for _, w := range m.watchers {
		if w.ID == id {
			return &w, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockWatcherStore) List(_ context.Context, params store.ListWatcherParams) ([]store.Watcher, error) {
	var result []store.Watcher
	for _, w := range m.watchers {
		if params.Environment != "" && w.Environment != params.Environment {
			continue
		}
		result = append(result, w)
	}
	return result, nil
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
func (m *mockWatcherStore) UpdateAdaptiveState(ctx context.Context, id uuid.UUID, params store.UpdateAdaptiveParams) error {
	return nil
}
func (m *mockWatcherStore) ResumeWatcher(ctx context.Context, id uuid.UUID) error {
	return nil
}

type mockRunStore struct {
	runs []store.WatcherRun
}

func (m *mockRunStore) Create(_ context.Context, watcherID uuid.UUID) (*store.WatcherRun, error) {
	r := &store.WatcherRun{
		ID: uuid.New(), WatcherID: watcherID, StartedAt: time.Now(),
		Status: "running", CreatedAt: time.Now(),
	}
	m.runs = append(m.runs, *r)
	return r, nil
}

func (m *mockRunStore) Complete(_ context.Context, _ uuid.UUID, _ string, _ any, _ bool) error {
	return nil
}
func (m *mockRunStore) Fail(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockRunStore) FailStaleRuns(_ context.Context, _ time.Duration) (int, error) {
	return 0, nil
}
func (m *mockRunStore) Prune(_ context.Context, _ time.Duration) (int64, error) { return 0, nil }

func (m *mockRunStore) List(_ context.Context, watcherID uuid.UUID, limit int) ([]store.WatcherRun, error) {
	var result []store.WatcherRun
	for _, r := range m.runs {
		if r.WatcherID == watcherID {
			result = append(result, r)
		}
	}
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *mockRunStore) GetByID(_ context.Context, id uuid.UUID) (*store.WatcherRun, error) {
	for _, r := range m.runs {
		if r.ID == id {
			return &r, nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *mockRunStore) ListWithFilter(_ context.Context, watcherID uuid.UUID, limit int, status string) ([]store.WatcherRun, error) {
	return m.List(context.Background(), watcherID, limit)
}

func (m *mockRunStore) CountRuns(_ context.Context, params store.CountRunParams) (int, error) {
	count := 0
	for _, r := range m.runs {
		if r.StartedAt.Before(params.Since) || !r.StartedAt.Before(params.Until) {
			continue
		}
		if params.Status != "" && r.Status != params.Status {
			continue
		}
		if params.WatcherID != nil && r.WatcherID != *params.WatcherID {
			continue
		}
		count++
	}
	return count, nil
}

// --- tests ---

func TestBuilder_Generate_EmptyPeriod(t *testing.T) {
	b := NewBuilder(&mockAlertStore{}, &mockWatcherStore{}, &mockRunStore{})

	now := time.Now().UTC()
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: now.Add(-24 * time.Hour),
		PeriodEnd:   now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Status != StatusHealthy {
		t.Errorf("status = %q, want %q", d.Status, StatusHealthy)
	}
	if d.AlertSummary.Total != 0 {
		t.Errorf("alert total = %d, want 0", d.AlertSummary.Total)
	}
	if d.WatcherSummary.Total != 0 {
		t.Errorf("watcher total = %d, want 0", d.WatcherSummary.Total)
	}
	if len(d.TopAlerts) != 0 {
		t.Errorf("top alerts = %d, want 0", len(d.TopAlerts))
	}
	if d.Trends == nil {
		t.Fatal("trends should not be nil")
	}
	if d.Trends.AlertsChangePercent() != 0 {
		t.Errorf("alerts change = %v, want 0", d.Trends.AlertsChangePercent())
	}
}

func TestBuilder_Generate_MixedAlerts(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	wID := uuid.New()
	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), WatcherID: &wID, WatcherTitle: "Watcher A", Title: "Critical issue", Summary: "bad", Severity: store.SeverityCritical, CreatedAt: now.Add(-2 * time.Hour)},
			{ID: uuid.New(), WatcherID: &wID, WatcherTitle: "Watcher A", Title: "Warning issue", Summary: "warn", Severity: store.SeverityWarning, CreatedAt: now.Add(-3 * time.Hour)},
			{ID: uuid.New(), WatcherID: &wID, WatcherTitle: "Watcher A", Title: "Info issue", Summary: "info", Severity: store.SeverityInfo, CreatedAt: now.Add(-4 * time.Hour)},
		},
	}

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: wID, Title: "Watcher A", Status: store.WatcherActive,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
		},
	}

	rs := &mockRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: wID, StartedAt: now.Add(-1 * time.Hour), Status: "completed", CreatedAt: now.Add(-1 * time.Hour)},
		},
	}

	b := NewBuilder(as, ws, rs)
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Status != StatusCritical {
		t.Errorf("status = %q, want %q", d.Status, StatusCritical)
	}
	if d.AlertSummary.Critical != 1 {
		t.Errorf("critical = %d, want 1", d.AlertSummary.Critical)
	}
	if d.AlertSummary.Warning != 1 {
		t.Errorf("warning = %d, want 1", d.AlertSummary.Warning)
	}
	if d.AlertSummary.Info != 1 {
		t.Errorf("info = %d, want 1", d.AlertSummary.Info)
	}
	if d.AlertSummary.Total != 3 {
		t.Errorf("total = %d, want 3", d.AlertSummary.Total)
	}

	// Top alerts should be sorted: critical first
	if len(d.TopAlerts) != 3 {
		t.Fatalf("top alerts = %d, want 3", len(d.TopAlerts))
	}
	if d.TopAlerts[0].Severity != "critical" {
		t.Errorf("top alert[0] severity = %q, want critical", d.TopAlerts[0].Severity)
	}
	if d.TopAlerts[1].Severity != "warning" {
		t.Errorf("top alert[1] severity = %q, want warning", d.TopAlerts[1].Severity)
	}
	if d.TopAlerts[2].Severity != "info" {
		t.Errorf("top alert[2] severity = %q, want info", d.TopAlerts[2].Severity)
	}
}

func TestBuilder_Generate_WatcherHealth(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	w1 := uuid.New()
	w2 := uuid.New()

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: w1, Title: "Active Watcher", Status: store.WatcherActive,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
			{ID: w2, Title: "Error Watcher", Status: store.WatcherError,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
		},
	}

	rs := &mockRunStore{
		runs: []store.WatcherRun{
			{ID: uuid.New(), WatcherID: w1, StartedAt: now.Add(-2 * time.Hour), Status: "completed", CreatedAt: now.Add(-2 * time.Hour)},
			{ID: uuid.New(), WatcherID: w2, StartedAt: now.Add(-3 * time.Hour), Status: "error", CreatedAt: now.Add(-3 * time.Hour)},
		},
	}

	b := NewBuilder(&mockAlertStore{}, ws, rs)
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Watcher in error state → status should be degraded
	if d.Status != StatusDegraded {
		t.Errorf("status = %q, want %q", d.Status, StatusDegraded)
	}

	if d.WatcherSummary.Total != 2 {
		t.Errorf("total watchers = %d, want 2", d.WatcherSummary.Total)
	}
	if d.WatcherSummary.Active != 1 {
		t.Errorf("active = %d, want 1", d.WatcherSummary.Active)
	}
	if d.WatcherSummary.InError != 1 {
		t.Errorf("in_error = %d, want 1", d.WatcherSummary.InError)
	}
	if d.WatcherSummary.RunsInPeriod != 2 {
		t.Errorf("runs = %d, want 2", d.WatcherSummary.RunsInPeriod)
	}
	if d.WatcherSummary.FailedRuns != 1 {
		t.Errorf("failed runs = %d, want 1", d.WatcherSummary.FailedRuns)
	}

	// Check per-watcher health
	if len(d.WatcherHealth) != 2 {
		t.Fatalf("watcher health entries = %d, want 2", len(d.WatcherHealth))
	}
}

func TestBuilder_Generate_WatcherNoRuns(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	w1 := uuid.New()
	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: w1, Title: "Idle Watcher", Status: store.WatcherActive,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
		},
	}

	b := NewBuilder(&mockAlertStore{}, ws, &mockRunStore{})
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Status != StatusHealthy {
		t.Errorf("status = %q, want %q", d.Status, StatusHealthy)
	}

	if len(d.WatcherHealth) != 1 {
		t.Fatalf("watcher health = %d, want 1", len(d.WatcherHealth))
	}
	if !d.WatcherHealth[0].LastRunOK {
		t.Error("watcher with no runs should show LastRunOK=true")
	}
}

func TestBuilder_Generate_Trends(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)
	// Previous period: 48h ago to 24h ago
	prevAlertTime := now.Add(-36 * time.Hour)

	as := &mockAlertStore{
		alerts: []store.Alert{
			// Previous period: 1 alert
			{ID: uuid.New(), Severity: store.SeverityWarning, CreatedAt: prevAlertTime},
			// Current period: 3 alerts
			{ID: uuid.New(), Severity: store.SeverityWarning, CreatedAt: now.Add(-2 * time.Hour)},
			{ID: uuid.New(), Severity: store.SeverityWarning, CreatedAt: now.Add(-3 * time.Hour)},
			{ID: uuid.New(), Severity: store.SeverityCritical, CreatedAt: now.Add(-4 * time.Hour)},
		},
	}

	b := NewBuilder(as, &mockWatcherStore{}, &mockRunStore{})
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Trends == nil {
		t.Fatal("trends should not be nil")
	}
	if d.Trends.AlertsPrevCount != 1 {
		t.Errorf("prev alerts = %d, want 1", d.Trends.AlertsPrevCount)
	}
	if d.Trends.AlertsCurrentCount != 3 {
		t.Errorf("current alerts = %d, want 3", d.Trends.AlertsCurrentCount)
	}
	if d.Trends.AlertsChangePercent() != 200 {
		t.Errorf("change = %v, want 200", d.Trends.AlertsChangePercent())
	}
}

func TestBuilder_Generate_EnvironmentFilter(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)

	w1 := uuid.New()
	w2 := uuid.New()

	as := &mockAlertStore{
		alerts: []store.Alert{
			{ID: uuid.New(), WatcherID: &w1, Environment: "production", Severity: store.SeverityCritical, CreatedAt: now.Add(-1 * time.Hour)},
			{ID: uuid.New(), WatcherID: &w2, Environment: "staging", Severity: store.SeverityWarning, CreatedAt: now.Add(-2 * time.Hour)},
		},
	}

	ws := &mockWatcherStore{
		watchers: []store.Watcher{
			{ID: w1, Title: "Prod Watcher", Environment: "production", Status: store.WatcherActive,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
			{ID: w2, Title: "Staging Watcher", Environment: "staging", Status: store.WatcherActive,
				Filters: json.RawMessage("{}"), Notify: json.RawMessage("{}")},
		},
	}

	b := NewBuilder(as, ws, &mockRunStore{})
	d, err := b.Generate(context.Background(), DigestOpts{
		PeriodStart: periodStart,
		PeriodEnd:   now,
		Environment: "production",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.AlertSummary.Total != 1 {
		t.Errorf("total alerts = %d, want 1 (production only)", d.AlertSummary.Total)
	}
	if d.WatcherSummary.Total != 1 {
		t.Errorf("total watchers = %d, want 1 (production only)", d.WatcherSummary.Total)
	}
}
