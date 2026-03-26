package store

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
