package mcp

import (
	"context"
	"log/slog"

	"github.com/adham90/opentrace/pkg/store"
)

// defaultWorkflowTemplates returns curated workflow templates for cold start.
func defaultWorkflowTemplates() []store.WorkflowTemplate {
	return []store.WorkflowTemplate{
		// Error Spike workflow
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 0, ToolName: "overview", Source: "curated"},
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 1, ToolName: "errors", Source: "curated"},
		{Intent: IntentInvestigation, Name: "error_spike", StepOrder: 2, ToolName: "logs", Source: "curated"},

		// Slow Database workflow
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 0, ToolName: "database", Source: "curated"},
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 1, ToolName: "database", Source: "curated"},
		{Intent: IntentInvestigation, Name: "slow_database", StepOrder: 2, ToolName: "database", Source: "curated"},

		// General Triage workflow
		{Intent: IntentInvestigation, Name: "general_triage", StepOrder: 0, ToolName: "overview", Source: "curated"},
		{Intent: IntentInvestigation, Name: "general_triage", StepOrder: 1, ToolName: "errors", Source: "curated"},

		// Connection Exhaustion workflow
		{Intent: IntentInvestigation, Name: "connection_exhaustion", StepOrder: 0, ToolName: "database", Source: "curated"},
		{Intent: IntentInvestigation, Name: "connection_exhaustion", StepOrder: 1, ToolName: "database", Source: "curated"},

		// Performance Regression workflow
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 0, ToolName: "logs", Source: "curated"},
		{Intent: IntentInvestigation, Name: "performance_regression", StepOrder: 1, ToolName: "database", Source: "curated"},

		// Query-intent workflows
		{Intent: IntentQuery, Name: "system_health", StepOrder: 0, ToolName: "overview", Source: "curated"},
		{Intent: IntentQuery, Name: "system_health", StepOrder: 1, ToolName: "healthchecks", Source: "curated"},
		{Intent: IntentQuery, Name: "system_health", StepOrder: 2, ToolName: "errors", Source: "curated"},
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
