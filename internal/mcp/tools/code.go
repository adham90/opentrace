package tools

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
)

// CodeDeps holds all stores needed by the unified code tool.
// This is a superset of CodeIntelDeps, AnnotationsDeps, TestGenDeps, and DependenciesDeps.
type CodeDeps struct {
	CodeEntityStore          store.CodeEntityStore
	ErrorGroupStore          store.ErrorGroupStore
	ErrorImpactStore         store.ErrorImpactStore
	TestCorrelationStore     store.TestCorrelationStore
	DeployStore              store.DeployStore
	AgentNoteStore           store.AgentNoteStore
	InvestigationSessionStore store.InvestigationSessionStore
	AnalyticsStore           store.AnalyticsStore
	LogStore                 store.LogStore
}

// CodeTool returns the unified code intelligence tool definition.
func CodeTool() mcp.Tool {
	return mcp.NewTool("code",
		mcp.WithDescription(`Code intelligence, annotations, test generation, and dependency analysis.

Actions:
- risk: Bulk risk scores for files (pre-deploy check)
- fragile: Riskiest code entities for a service
- context: Error history and risk for a code entity, or task-specific production context
- test_gaps: Uncovered production error paths ranked by impact
- test_priority: Error detail for a fingerprint to help write tests
- annotate_file: Production metrics for a source file (calls, errors, latency)
- annotate_function: Detailed metrics for a specific endpoint
- hotspots: Top error-prone code entities
- gen_context: Test-ready data for a specific error group
- gen_suggest: Rank which errors most need regression tests
- gen_coverage: Production errors with no test coverage
- deps_service: Dependencies for a service (callers/callees)
- deps_blast: Blast radius if a service goes down
- deps_risk: Change risk assessment for a file or endpoint`),
		mcp.WithString("action", mcp.Required(), mcp.Description(
			"Action: risk, fragile, context, test_gaps, test_priority, "+
				"annotate_file, annotate_function, hotspots, "+
				"gen_context, gen_suggest, gen_coverage, "+
				"deps_service, deps_blast, deps_risk")),
		mcp.WithString("service", mcp.Description("Service name filter")),
		mcp.WithString("fingerprint", mcp.Description("Error fingerprint")),
		mcp.WithString("entity_name", mcp.Description("Code entity name (file, controller, endpoint)")),
		mcp.WithString("task", mcp.Description("Task description for context meta-tool")),
		mcp.WithString("path", mcp.Description("Source file path")),
		mcp.WithString("controller", mcp.Description("Controller name")),
		mcp.WithString("function_name", mcp.Description("Function/action name")),
		mcp.WithString("method", mcp.Description("HTTP method")),
		mcp.WithString("endpoint", mcp.Description("Endpoint path")),
		mcp.WithString("file", mcp.Description("Source file for change_risk")),
		mcp.WithObject("files", mcp.Description("Array of file paths for bulk risk")),
		mcp.WithNumber("limit", mcp.Description("Max results")),
	)
}

// CodeHandler returns a handler for the unified code tool.
func CodeHandler(d CodeDeps) server.ToolHandlerFunc {
	// Build sub-deps once.
	ciDeps := CodeIntelDeps{
		CodeEntityStore:          d.CodeEntityStore,
		ErrorGroupStore:          d.ErrorGroupStore,
		TestCorrelationStore:     d.TestCorrelationStore,
		DeployStore:              d.DeployStore,
		AgentNoteStore:           d.AgentNoteStore,
		InvestigationSessionStore: d.InvestigationSessionStore,
	}
	annDeps := AnnotationsDeps{
		AnalyticsStore:  d.AnalyticsStore,
		ErrorGroupStore: d.ErrorGroupStore,
		CodeEntityStore: d.CodeEntityStore,
		LogStore:        d.LogStore,
	}
	tgDeps := TestGenDeps{
		ErrorGroupStore:      d.ErrorGroupStore,
		ErrorImpactStore:     d.ErrorImpactStore,
		CodeEntityStore:      d.CodeEntityStore,
		TestCorrelationStore: d.TestCorrelationStore,
		LogStore:             d.LogStore,
	}
	depDeps := DependenciesDeps{
		AnalyticsStore:  d.AnalyticsStore,
		ErrorGroupStore: d.ErrorGroupStore,
		CodeEntityStore: d.CodeEntityStore,
		DeployStore:     d.DeployStore,
		LogStore:        d.LogStore,
	}

	// Build sub-handlers once.
	ciHandler := CodeIntelHandler(ciDeps)
	annHandler := AnnotationsHandler(annDeps)
	tgHandler := TestGenHandler(tgDeps)
	depHandler := DependenciesHandler(depDeps)

	// Map new action names to sub-tool action names.
	actionMap := map[string]struct {
		handler server.ToolHandlerFunc
		action  string // the action param to pass to the sub-handler
	}{
		// code_intel actions (pass through as-is)
		"risk":          {ciHandler, "risk"},
		"fragile":       {ciHandler, "fragile"},
		"context":       {ciHandler, "context"},
		"test_gaps":     {ciHandler, "test_gaps"},
		"test_priority": {ciHandler, "test_priority"},
		// annotations actions (remap)
		"annotate_file":     {annHandler, "file"},
		"annotate_function": {annHandler, "function"},
		"hotspots":          {annHandler, "hotspots"},
		// test_gen actions (remap)
		"gen_context":  {tgHandler, "context"},
		"gen_suggest":  {tgHandler, "suggest"},
		"gen_coverage": {tgHandler, "coverage"},
		// dependencies actions (remap)
		"deps_service": {depHandler, "service"},
		"deps_blast":   {depHandler, "blast_radius"},
		"deps_risk":    {depHandler, "change_risk"},
	}

	allActions := make([]string, 0, len(actionMap))
	for k := range actionMap {
		allActions = append(allActions, k)
	}

	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		action, _ := args["action"].(string)

		entry, ok := actionMap[action]
		if !ok {
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s", action)), nil
		}

		// Rewrite the action param for the sub-handler, then restore it.
		args["action"] = entry.action
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		result, err := entry.handler(ctx, req)
		args["action"] = action // restore
		return result, err
	}
}
