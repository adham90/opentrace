package overview

import (
	"context"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestStatus_AllNilStores(t *testing.T) {
	svc := &Service{}
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
}

func TestStatus_WithLogStore(t *testing.T) {
	ls := mocks.NewLogStore()
	svc := &Service{Logs: ls}
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CountByLevel returns nil map → iterates nothing → LogStats{0, 0}.
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

func TestStatus_WithErrorGroupStore(t *testing.T) {
	egs := mocks.NewErrorGroupStore()
	// No errors seeded → Count returns 0.
	svc := &Service{ErrorGroups: egs}
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count returns 0 → no ErrorGroups section.
	if report.ErrorGroups != nil {
		t.Error("expected nil ErrorGroups when count is 0")
	}
}

func TestStatus_WithWatchStore(t *testing.T) {
	ws := mocks.NewWatchStore()
	svc := &Service{Watches: ws}
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// CountPendingAlerts returns 0 by default.
	if report.WatchAlerts != nil {
		t.Error("expected nil WatchAlerts when count is 0")
	}
}

func TestStatus_WithHealthCheckStore(t *testing.T) {
	hcs := mocks.NewHealthCheckStore()
	svc := &Service{HealthChecks: hcs}
	report, err := svc.Status(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// UptimeSummaries returns nil by default.
	if report.HealthChecks != nil {
		t.Error("expected nil HealthChecks when no summaries")
	}
}

func TestStatus_WithDataSourceStore(t *testing.T) {
	dss := mocks.NewDataSourceStore()
	svc := &Service{DataSources: dss}
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

func TestStatus_WithServerStore(t *testing.T) {
	ss := mocks.NewServerStore()
	svc := &Service{Servers: ss}
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
	t.Run("positive", func(t *testing.T) {
		r := &StatusReport{HealthChecks: &HealthStats{Down: 2}}
		if !r.HasDownChecks() {
			t.Error("should be true with down checks")
		}
	})
}

// Check the mock ServerStore and DataSourceStore are usable.
var _ store.ServerStore = (*mocks.ServerStore)(nil)
var _ store.DataSourceStore = (*mocks.DataSourceStore)(nil)
