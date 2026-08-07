package overview

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Local fakes — implement only the minimal interfaces
// ---------------------------------------------------------------------------

type fakeLogCounter struct {
	counts map[string]int
	err    error
}

func (f *fakeLogCounter) CountByLevel(_ context.Context, _ store.LogCountParams) (map[string]int, error) {
	return f.counts, f.err
}

type fakeErrorCounter struct {
	count int
	err   error
}

func (f *fakeErrorCounter) Count(_ context.Context, _ store.ErrorGroupStatus, _ string) (int, error) {
	return f.count, f.err
}

type fakeAlertCounter struct {
	count int
	err   error
}

func (f *fakeAlertCounter) CountPendingAlerts(_ context.Context) (int, error) {
	return f.count, f.err
}

type fakeHealthSummariser struct {
	summaries []store.UptimeSummary
	err       error
}

func (f *fakeHealthSummariser) UptimeSummaries(_ context.Context, _ time.Time) ([]store.UptimeSummary, error) {
	return f.summaries, f.err
}

type fakeConnectorLister struct {
	items []store.DataSource
	err   error
}

func (f *fakeConnectorLister) List(_ context.Context, _ store.ListDataSourceParams) ([]store.DataSource, error) {
	return f.items, f.err
}

type fakeServerLister struct {
	items []store.Server
	err   error
}

func (f *fakeServerLister) List(_ context.Context, _ store.ListServerParams) ([]store.Server, error) {
	return f.items, f.err
}

// Compile-time interface checks for fakes.
var (
	_ LogCounter       = (*fakeLogCounter)(nil)
	_ ErrorCounter     = (*fakeErrorCounter)(nil)
	_ AlertCounter     = (*fakeAlertCounter)(nil)
	_ HealthSummariser = (*fakeHealthSummariser)(nil)
	_ ConnectorLister  = (*fakeConnectorLister)(nil)
	_ ServerLister     = (*fakeServerLister)(nil)
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestStatus_AllNilStores(t *testing.T) {
	svc := NewService()
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Logs != nil {
		t.Error("expected nil Logs")
	}
	if report.ErrorGroups != nil {
		t.Error("expected nil ErrorGroups")
	}
	if report.HealthChecks != nil {
		t.Error("expected nil HealthChecks")
	}
	if report.WatchAlerts != nil {
		t.Error("expected nil WatchAlerts")
	}
	if report.Connectors != nil {
		t.Error("expected nil Connectors")
	}
	if report.Servers != nil {
		t.Error("expected nil Servers")
	}
}

func TestStatus_WithLogCounter(t *testing.T) {
	svc := NewService(WithLogCounter(&fakeLogCounter{}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Logs == nil {
		t.Fatal("expected non-nil Logs")
	}
	if report.Logs.LastHour != 0 {
		t.Errorf("expected 0 last_hour, got %d", report.Logs.LastHour)
	}
	if report.Logs.ErrorsLastHour != 0 {
		t.Errorf("expected 0 errors_last_hour, got %d", report.Logs.ErrorsLastHour)
	}
}

func TestStatus_WithLogCounter_Counts(t *testing.T) {
	svc := NewService(WithLogCounter(&fakeLogCounter{
		counts: map[string]int{"info": 50, "error": 3, "fatal": 1},
	}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Logs == nil {
		t.Fatal("expected non-nil Logs")
	}
	if report.Logs.LastHour != 54 {
		t.Errorf("expected 54 last_hour, got %d", report.Logs.LastHour)
	}
	if report.Logs.ErrorsLastHour != 4 {
		t.Errorf("expected 4 errors_last_hour, got %d", report.Logs.ErrorsLastHour)
	}
}

func TestStatus_LogCounterError(t *testing.T) {
	svc := NewService(WithLogCounter(&fakeLogCounter{
		err: context.DeadlineExceeded,
	}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Store error is swallowed — Logs section remains nil.
	if report.Logs != nil {
		t.Error("expected nil Logs when counter errors")
	}
}

func TestStatus_WithErrorCounter(t *testing.T) {
	svc := NewService(WithErrorCounter(&fakeErrorCounter{count: 0}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count returns 0 -> no ErrorGroups section.
	if report.ErrorGroups != nil {
		t.Error("expected nil ErrorGroups when count is 0")
	}
}

func TestStatus_WithErrorCounter_Positive(t *testing.T) {
	svc := NewService(WithErrorCounter(&fakeErrorCounter{count: 7}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.ErrorGroups == nil {
		t.Fatal("expected non-nil ErrorGroups")
	}
	if report.ErrorGroups.Unresolved != 7 {
		t.Errorf("expected 7 unresolved, got %d", report.ErrorGroups.Unresolved)
	}
}

func TestStatus_WithAlertCounter(t *testing.T) {
	svc := NewService(WithAlertCounter(&fakeAlertCounter{count: 0}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.WatchAlerts != nil {
		t.Error("expected nil WatchAlerts when count is 0")
	}
}

func TestStatus_WithAlertCounter_Positive(t *testing.T) {
	svc := NewService(WithAlertCounter(&fakeAlertCounter{count: 3}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.WatchAlerts == nil {
		t.Fatal("expected non-nil WatchAlerts")
	}
	if report.WatchAlerts.Pending != 3 {
		t.Errorf("expected 3 pending, got %d", report.WatchAlerts.Pending)
	}
}

func TestStatus_WithHealthSummariser(t *testing.T) {
	svc := NewService(WithHealthSummariser(&fakeHealthSummariser{}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.HealthChecks != nil {
		t.Error("expected nil HealthChecks when no summaries")
	}
}

func TestStatus_WithHealthSummariser_Mixed(t *testing.T) {
	svc := NewService(WithHealthSummariser(&fakeHealthSummariser{
		summaries: []store.UptimeSummary{
			{HealthCheckID: "1", Name: "api", CurrentStatus: string(store.HealthCheckUp)},
			{HealthCheckID: "2", Name: "db", CurrentStatus: string(store.HealthCheckDown)},
			{HealthCheckID: "3", Name: "cache", CurrentStatus: string(store.HealthCheckDegraded)},
		},
	}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.HealthChecks == nil {
		t.Fatal("expected non-nil HealthChecks")
	}
	if report.HealthChecks.Total != 3 {
		t.Errorf("expected 3 total, got %d", report.HealthChecks.Total)
	}
	if report.HealthChecks.Down != 1 {
		t.Errorf("expected 1 down, got %d", report.HealthChecks.Down)
	}
	if report.HealthChecks.Degraded != 1 {
		t.Errorf("expected 1 degraded, got %d", report.HealthChecks.Degraded)
	}
}

func TestStatus_WithConnectorLister(t *testing.T) {
	svc := NewService(WithConnectorLister(&fakeConnectorLister{}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Connectors == nil {
		t.Fatal("expected non-nil Connectors")
	}
	if report.Connectors.Total != 0 {
		t.Errorf("expected 0 total connectors, got %d", report.Connectors.Total)
	}
}

func TestStatus_WithConnectorLister_Mixed(t *testing.T) {
	svc := NewService(WithConnectorLister(&fakeConnectorLister{
		items: []store.DataSource{
			{Status: store.StatusConnected},
			{Status: store.StatusError},
			{Status: store.StatusConnected},
			{Status: store.StatusDisconnected},
		},
	}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Connectors == nil {
		t.Fatal("expected non-nil Connectors")
	}
	if report.Connectors.Total != 4 {
		t.Errorf("expected 4 total, got %d", report.Connectors.Total)
	}
	if report.Connectors.Connected != 2 {
		t.Errorf("expected 2 connected, got %d", report.Connectors.Connected)
	}
	if report.Connectors.Error != 1 {
		t.Errorf("expected 1 error, got %d", report.Connectors.Error)
	}
}

func TestStatus_WithServerLister(t *testing.T) {
	svc := NewService(WithServerLister(&fakeServerLister{}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Servers == nil {
		t.Fatal("expected non-nil Servers")
	}
	if report.Servers.Total != 0 {
		t.Errorf("expected 0 total servers, got %d", report.Servers.Total)
	}
}

func TestStatus_WithServerLister_Mixed(t *testing.T) {
	svc := NewService(WithServerLister(&fakeServerLister{
		items: []store.Server{
			{Status: store.ServerOnline},
			{Status: store.ServerOffline},
			{Status: store.ServerOnline},
		},
	}))
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Servers == nil {
		t.Fatal("expected non-nil Servers")
	}
	if report.Servers.Total != 3 {
		t.Errorf("expected 3 total, got %d", report.Servers.Total)
	}
	if report.Servers.Online != 2 {
		t.Errorf("expected 2 online, got %d", report.Servers.Online)
	}
	if report.Servers.Offline != 1 {
		t.Errorf("expected 1 offline, got %d", report.Servers.Offline)
	}
}

func TestStatus_FullStack(t *testing.T) {
	svc := NewService(
		WithLogCounter(&fakeLogCounter{counts: map[string]int{"INFO": 10, "error": 2}}),
		WithErrorCounter(&fakeErrorCounter{count: 5}),
		WithAlertCounter(&fakeAlertCounter{count: 1}),
		WithHealthSummariser(&fakeHealthSummariser{
			summaries: []store.UptimeSummary{
				{CurrentStatus: string(store.HealthCheckDown)},
			},
		}),
		WithConnectorLister(&fakeConnectorLister{
			items: []store.DataSource{{Status: store.StatusConnected}},
		}),
		WithServerLister(&fakeServerLister{
			items: []store.Server{{Status: store.ServerOnline}},
		}),
	)

	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Logs == nil || report.Logs.LastHour != 12 {
		t.Errorf("unexpected Logs: %+v", report.Logs)
	}
	if report.ErrorGroups == nil || report.ErrorGroups.Unresolved != 5 {
		t.Errorf("unexpected ErrorGroups: %+v", report.ErrorGroups)
	}
	if report.WatchAlerts == nil || report.WatchAlerts.Pending != 1 {
		t.Errorf("unexpected WatchAlerts: %+v", report.WatchAlerts)
	}
	if report.HealthChecks == nil || report.HealthChecks.Down != 1 {
		t.Errorf("unexpected HealthChecks: %+v", report.HealthChecks)
	}
	if report.Connectors == nil || report.Connectors.Connected != 1 {
		t.Errorf("unexpected Connectors: %+v", report.Connectors)
	}
	if report.Servers == nil || report.Servers.Online != 1 {
		t.Errorf("unexpected Servers: %+v", report.Servers)
	}
}

// ---------------------------------------------------------------------------
// StatusReport helper tests
// ---------------------------------------------------------------------------

func TestStatusReport_HasErrors(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		r := &StatusReport{}
		if r.HasErrors() {
			t.Error("should be false with nil ErrorGroups")
		}
	})
	t.Run("zero", func(t *testing.T) {
		r := &StatusReport{ErrorGroups: &ErrorGroupStats{Unresolved: 0}}
		if r.HasErrors() {
			t.Error("should be false with 0 unresolved")
		}
	})
	t.Run("positive", func(t *testing.T) {
		r := &StatusReport{ErrorGroups: &ErrorGroupStats{Unresolved: 5}}
		if !r.HasErrors() {
			t.Error("should be true with 5 unresolved")
		}
	})
}

func TestStatusReport_HasLogErrors(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		r := &StatusReport{}
		if r.HasLogErrors() {
			t.Error("should be false with nil Logs")
		}
	})
	t.Run("zero", func(t *testing.T) {
		r := &StatusReport{Logs: &LogStats{ErrorsLastHour: 0}}
		if r.HasLogErrors() {
			t.Error("should be false with 0 errors")
		}
	})
	t.Run("positive", func(t *testing.T) {
		r := &StatusReport{Logs: &LogStats{ErrorsLastHour: 3}}
		if !r.HasLogErrors() {
			t.Error("should be true with errors")
		}
	})
}

func TestStatusReport_HasDownChecks(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		r := &StatusReport{}
		if r.HasDownChecks() {
			t.Error("should be false with nil HealthChecks")
		}
	})
	t.Run("zero", func(t *testing.T) {
		r := &StatusReport{HealthChecks: &HealthStats{Down: 0}}
		if r.HasDownChecks() {
			t.Error("should be false with 0 down")
		}
	})
	t.Run("positive", func(t *testing.T) {
		r := &StatusReport{HealthChecks: &HealthStats{Down: 2}}
		if !r.HasDownChecks() {
			t.Error("should be true with down checks")
		}
	})
}

func TestNewService_Options(t *testing.T) {
	lc := &fakeLogCounter{}
	ec := &fakeErrorCounter{}
	ac := &fakeAlertCounter{}
	hs := &fakeHealthSummariser{}
	cl := &fakeConnectorLister{}
	sl := &fakeServerLister{}

	svc := NewService(
		WithLogCounter(lc),
		WithErrorCounter(ec),
		WithAlertCounter(ac),
		WithHealthSummariser(hs),
		WithConnectorLister(cl),
		WithServerLister(sl),
	)

	if svc.Logs != lc {
		t.Error("LogCounter not set")
	}
	if svc.ErrorGroups != ec {
		t.Error("ErrorCounter not set")
	}
	if svc.Watches != ac {
		t.Error("AlertCounter not set")
	}
	if svc.HealthChecks != hs {
		t.Error("HealthSummariser not set")
	}
	if svc.DataSources != cl {
		t.Error("ConnectorLister not set")
	}
	if svc.Servers != sl {
		t.Error("ServerLister not set")
	}
}
