package mcp

import (
	"context"
	"log/slog"

	"github.com/adham90/opentrace/internal/store"
)

// defaultWorkflowTemplates returns curated workflow templates for cold start.
func defaultWorkflowTemplates() []store.WorkflowTemplate {
	return []store.WorkflowTemplate{
		// Error Spike workflow
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 0, ToolName: "diagnose", Source: "curated"},
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 1, ToolName: "error_groups", Source: "curated"},
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 2, ToolName: "error_detail", Source: "curated"},
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 3, ToolName: "log_search", Source: "curated"},

		// Slow Database workflow
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 0, ToolName: "db_query_stats", Source: "curated"},
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 1, ToolName: "explain_query", Source: "curated"},
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 2, ToolName: "db_activity", Source: "curated"},
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 3, ToolName: "db_locks", Source: "curated"},

		// General Triage workflow
		{Intent: IntentInvestigation, Name: "general_triage", StepOrder: 0, ToolName: "triage_alerts", Source: "curated"},
		{Intent: IntentInvestigation, Name: "general_triage", StepOrder: 1, ToolName: "diagnose", Source: "curated"},
		{Intent: IntentInvestigation, Name: "general_triage", StepOrder: 2, ToolName: "error_groups", Source: "curated"},

		// Connection Exhaustion workflow
		{Intent: IntentInvestigation, Name: "connection_exhaustion", StepOrder: 0, ToolName: "db_activity", Source: "curated"},
		{Intent: IntentInvestigation, Name: "connection_exhaustion", StepOrder: 1, ToolName: "db_query_stats", Source: "curated"},
		{Intent: IntentInvestigation, Name: "connection_exhaustion", StepOrder: 2, ToolName: "db_locks", Source: "curated"},

		// Performance Regression workflow
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 0, ToolName: "request_performance", Source: "curated"},
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 1, ToolName: "compare_periods", Source: "curated"},
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 2, ToolName: "db_query_stats", Source: "curated"},
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 3, ToolName: "explain_query", Source: "curated"},

		// Query-intent workflows
		{Intent: IntentQuery, Name: "system_health", StepOrder: 0, ToolName: "system_overview", Source: "curated"},
		{Intent: IntentQuery, Name: "system_health", StepOrder: 1, ToolName: "uptime_status", Source: "curated"},
		{Intent: IntentQuery, Name: "system_health", StepOrder: 2, ToolName: "error_groups", Source: "curated"},
	}
}

// SeedDefaultTemplates seeds the default workflow templates into the store.
// It is idempotent — duplicate templates are skipped.
func SeedDefaultTemplates(ctx context.Context, ws store.WorkflowTemplateStore) {
	if ws == nil {
		return
	}
	templates := defaultWorkflowTemplates()
	if err := ws.Seed(ctx, templates); err != nil {
		slog.Warn("failed to seed workflow templates", "error", err)
	} else {
		slog.Debug("workflow templates seeded", "count", len(templates))
	}
}
