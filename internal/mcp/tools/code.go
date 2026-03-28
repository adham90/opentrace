package tools

import (
	"context"
	"fmt"


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

// CodeHandler returns a handler for the unified code tool.
func CodeHandler(d CodeDeps) ToolHandlerFunc {
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
		handler ToolHandlerFunc
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

	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action, _ := args["action"].(string)

		entry, ok := actionMap[action]
		if !ok {
			return NewToolResultError(fmt.Sprintf("unknown action: %s", action)), nil
		}

		// Rewrite the action param for the sub-handler, then restore it.
		args["action"] = entry.action
		req := MakeCallToolRequest("code", args)
		result, err := entry.handler(ctx, req)
		args["action"] = action // restore
		return result, err
	}
}
