package sqlite

import (
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// NewStores creates all store implementations backed by the given SQLite database.
// logStore is the segmented log store adapter (passed in from main, not created here).
func NewStores(db *bun.DB, logStore store.LogStore) store.Stores {
	return store.Stores{
		DSStore:          NewDataSourceStore(db),
		LogStore:         logStore,
		ServerStore:      NewServerStore(db),
		MetricStore:      NewMetricStore(db),
		UserStore:        NewUserStore(db),
		SessionStore:     NewSessionStore(db),
		SettingsStore:    NewSettingsStore(db),
		MCPActivityStore: NewMCPActivityStore(db),
		AuditStore:       NewAuditStore(db),
		WatchStore:       NewWatchStore(db),
		ErrorGroupStore:  NewErrorGroupStore(db),
		HealthCheckStore: NewHealthCheckStore(db),
		AgentNoteStore:   NewAgentNoteStore(db),
		TrendStore:       NewTrendStore(db),
		AnalyticsStore:   NewAnalyticsStore(db),
		ErrorImpactStore: NewErrorImpactStore(db),
		TraceStore:       NewTraceStore(db),
		CodeEntityStore:  NewCodeEntityStore(db),
	}
}
