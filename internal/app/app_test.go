package app

import (
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

func TestNew_EmptyStores(t *testing.T) {
	app := New(store.Stores{})
	if app == nil {
		t.Fatal("expected non-nil App")
	}
	// Services that require stores should be nil.
	if app.Logs != nil {
		t.Error("Logs should be nil without LogStore")
	}
	if app.Errors != nil {
		t.Error("Errors should be nil without ErrorGroupStore")
	}
	if app.Analytics != nil {
		t.Error("Analytics should be nil without AnalyticsStore")
	}
	// Services that always initialize should be non-nil.
	if app.Overview == nil {
		t.Error("Overview should always be non-nil")
	}
	if app.Admin == nil {
		t.Error("Admin should always be non-nil")
	}
	if app.Setup == nil {
		t.Error("Setup should always be non-nil")
	}
}

func TestNew_WithAllStores(t *testing.T) {
	stores := store.Stores{
		LogStore:             mocks.NewLogStore(),
		ErrorGroupStore:      mocks.NewErrorGroupStore(),
		ErrorImpactStore:     mocks.NewErrorImpactStore(),
		WatchStore:           mocks.NewWatchStore(),
		HealthCheckStore:     mocks.NewHealthCheckStore(),
		ServerStore:          mocks.NewServerStore(),
		MetricStore:          mocks.NewMetricStore(),
		AnalyticsStore:       mocks.NewAnalyticsStore(),
		TrendStore:           mocks.NewTrendStore(),
		CodeEntityStore: mocks.NewCodeEntityStore(),
		AgentNoteStore:  mocks.NewAgentNoteStore(),
		DSStore:              mocks.NewDataSourceStore(),
		SettingsStore:        mocks.NewSettingsStore(),
		AuditStore:           mocks.NewAuditStore(),
		UserStore:            mocks.NewUserStore(),
	}

	app := New(stores)

	if app.Logs == nil {
		t.Error("Logs should be non-nil with LogStore")
	}
	if app.Errors == nil {
		t.Error("Errors should be non-nil with ErrorGroupStore")
	}
	if app.Analytics == nil {
		t.Error("Analytics should be non-nil with AnalyticsStore")
	}
	if app.Watches == nil {
		t.Error("Watches should be non-nil with WatchStore")
	}
	if app.HealthChecks == nil {
		t.Error("HealthChecks should be non-nil with HealthCheckStore")
	}
	if app.Servers == nil {
		t.Error("Servers should be non-nil with ServerStore")
	}
	if app.Code == nil {
		t.Error("Code should be non-nil with CodeEntityStore")
	}
	if app.Connectors == nil {
		t.Error("Connectors should be non-nil with DSStore")
	}
	if app.Auth == nil {
		t.Error("Auth should be non-nil with UserStore")
	}
}
