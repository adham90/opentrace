package store

import "database/sql"

// Stores groups all domain store implementations. Embedding this struct
// in dependency containers lets consumers access e.g. deps.LogStore
// without individual fields.
type Stores struct {
	DSStore                   DataSourceStore
	LogStore                  LogStore
	ServerStore               ServerStore
	MetricStore               MetricStore
	UserStore                 UserStore
	SessionStore              SessionStore
	SettingsStore             SettingsStore
	MCPActivityStore          MCPActivityStore
	AuditStore                AuditStore
	WatchStore                WatchStore
	ErrorGroupStore           ErrorGroupStore
	HealthCheckStore          HealthCheckStore
	AgentNoteStore            AgentNoteStore
	TrendStore                TrendStore
	AnalyticsStore            AnalyticsStore
	JourneyStore              JourneyStore
	ErrorImpactStore          ErrorImpactStore
	TraceStore                TraceStore
	InvestigationSessionStore InvestigationSessionStore
	ToolTransitionStore       ToolTransitionStore
	WorkflowTemplateStore     WorkflowTemplateStore
	QueryMemoryStore          QueryMemoryStore
	RunbookEffectivenessStore RunbookEffectivenessStore
	CodeEntityStore           CodeEntityStore
	DeployStore               DeployStore
	EventStore                EventStore
	TestCorrelationStore      TestCorrelationStore
}

// NewStores creates all store implementations backed by the given database.
func NewStores(db *sql.DB) Stores {
	return Stores{
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
