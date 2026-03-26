package db

import (
	"github.com/uptrace/bun"

	"github.com/adham90/opentrace/pkg/store"
)

// NewStores creates all store implementations backed by the given SQLite database.
func NewStores(db *bun.DB) store.Stores {
	return store.Stores{
		DSStore:                   NewDataSourceStore(db),
		LogStore:                  NewLogStore(db),
		ServerStore:               NewServerStore(db),
		MetricStore:               NewMetricStore(db),
		UserStore:                 NewUserStore(db),
		SessionStore:              NewSessionStore(db),
		SettingsStore:             NewSettingsStore(db),
		MCPActivityStore:          NewMCPActivityStore(db),
		AuditStore:                NewAuditStore(db),
		WatchStore:                NewWatchStore(db),
		ErrorGroupStore:           NewErrorGroupStore(db),
		HealthCheckStore:          NewHealthCheckStore(db),
		AgentNoteStore:            NewAgentNoteStore(db),
		TrendStore:                NewTrendStore(db),
		AnalyticsStore:            NewAnalyticsStore(db),
		JourneyStore:              NewJourneyStore(db),
		ErrorImpactStore:          NewErrorImpactStore(db),
		TraceStore:                NewTraceStore(db),
		InvestigationSessionStore: NewInvestigationSessionStore(db),
		ToolTransitionStore:       NewToolTransitionStore(db),
		WorkflowTemplateStore:     NewWorkflowTemplateStore(db),
		QueryMemoryStore:          NewQueryMemoryStore(db),
		RunbookEffectivenessStore: NewRunbookEffectivenessStore(db),
		CodeEntityStore:           NewCodeEntityStore(db),
		DeployStore:               NewDeployStore(db),
		EventStore:                NewEventStore(db),
		TestCorrelationStore:      NewTestCorrelationStore(db),
	}
}
