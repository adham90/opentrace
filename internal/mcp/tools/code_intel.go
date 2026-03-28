package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/adham90/opentrace/pkg/store"
)

// CodeIntelDeps holds the stores needed by the code_intel tool.
type CodeIntelDeps struct {
	CodeEntityStore      store.CodeEntityStore
	ErrorGroupStore      store.ErrorGroupStore
	TestCorrelationStore store.TestCorrelationStore
	DeployStore          store.DeployStore
	AgentNoteStore       store.AgentNoteStore
	InvestigationSessionStore store.InvestigationSessionStore
}

// CodeIntelHandler returns a handler for the consolidated code_intel tool.
func CodeIntelHandler(d CodeIntelDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "risk":
			return HandleCodeRisk(ctx, d, args)
		case "fragile":
			return HandleFragile(ctx, d, args)
		case "context":
			return HandleCodeContext(ctx, d, args)
		case "test_gaps":
			return HandleTestGaps(ctx, d, args)
		case "test_priority":
			return HandleTestPriority(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use risk, fragile, context, test_gaps, test_priority)", action)), nil
		}
	}
}

func HandleCodeRisk(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	service := ArgString(args, "service")
	files := ArgStringSlice(args, "files")

	if len(files) == 0 {
		return NewToolResultError("files array is required"), nil
	}

	entities, err := d.CodeEntityStore.BatchGetRisk(ctx, store.CodeEntityFile, files, service)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to get risk scores: %v", err)), nil
	}

	known := make(map[string]*store.CodeEntity, len(entities))
	for i := range entities {
		known[entities[i].EntityName] = &entities[i]
	}

	riskMap := make([]map[string]any, 0, len(files))
	maxRisk := 0.0
	for _, f := range files {
		entry := map[string]any{"file": f}
		if e, ok := known[f]; ok {
			entry["risk_score"] = e.RiskScore
			entry["error_count"] = e.ErrorCount
			entry["investigation_count"] = e.InvestigationCount
			if e.RiskScore > maxRisk {
				maxRisk = e.RiskScore
			}
		} else {
			entry["risk_score"] = 0.0
			entry["error_count"] = 0
			entry["investigation_count"] = 0
		}
		riskMap = append(riskMap, entry)
	}

	assessment := "low"
	if maxRisk > 0.7 {
		assessment = "high"
	} else if maxRisk > 0.3 {
		assessment = "medium"
	}

	resp := map[string]any{
		"files":      riskMap,
		"max_risk":   maxRisk,
		"assessment": assessment,
	}

	return JSONResult(resp)
}

func HandleFragile(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	service := ArgString(args, "service")
	limit := ArgInt(args, "limit", 10, 50)

	entities, err := d.CodeEntityStore.TopByRisk(ctx, service, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query fragile code: %v", err)), nil
	}

	if len(entities) == 0 {
		return EmptyResult(`{"message":"No code entities with risk data found","entities":[]}`)
	}

	items := make([]map[string]any, 0, len(entities))
	for _, e := range entities {
		items = append(items, map[string]any{
			"entity_name":         e.EntityName,
			"entity_type":         e.EntityType,
			"service":             e.Service,
			"risk_score":          e.RiskScore,
			"error_count":         e.ErrorCount,
			"investigation_count": e.InvestigationCount,
			"risk_summary":        formatRiskSummary(&e),
		})
	}

	return JSONResult(map[string]any{
		"count":    len(items),
		"entities": items,
	})
}

func HandleCodeContext(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	// Check if this is a task-based context request
	task := ArgString(args, "task")
	if task != "" {
		return HandleTaskContext(ctx, d, args, task)
	}

	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	entityName := ArgString(args, "entity_name")
	if entityName == "" {
		return NewToolResultError("entity_name is required"), nil
	}
	service := ArgString(args, "service")

	types := []store.CodeEntityType{store.CodeEntityFile, store.CodeEntityController, store.CodeEntityEndpoint}
	var entity *store.CodeEntity
	for _, t := range types {
		e, err := d.CodeEntityStore.GetByName(ctx, t, entityName, service)
		if err == nil && e != nil {
			entity = e
			break
		}
	}

	if entity == nil {
		return JSONResult(map[string]any{
			"message":     fmt.Sprintf("No code entity found for %q", entityName),
			"entity_name": entityName,
			"service":     service,
		})
	}

	resp := map[string]any{
		"entity":       entity,
		"risk_summary": formatRiskSummary(entity),
	}

	if d.ErrorGroupStore != nil && entity.EntityType == store.CodeEntityFile {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Service: entity.Service,
			Limit:   5,
		})
		if err == nil {
			var related []map[string]any
			for _, g := range groups {
				if g.SourceFile == entity.EntityName {
					related = append(related, map[string]any{
						"fingerprint":      g.Fingerprint,
						"exception_class":  g.ExceptionClass,
						"status":           g.Status,
						"occurrence_count": g.OccurrenceCount,
					})
				}
			}
			if len(related) > 0 {
				resp["related_errors"] = related
			}
		}
	}

	return JSONResult(resp)
}

func HandleTaskContext(ctx context.Context, d CodeIntelDeps, args map[string]any, task string) (*CallToolResult, error) {
	service := ArgString(args, "service")

	taskType := classifyTaskType(task)
	result := map[string]any{
		"task":      task,
		"task_type": taskType,
	}

	switch taskType {
	case "debugging":
		buildDebuggingContext(ctx, d, service, result)
	case "deploying":
		buildDeployingContext(ctx, d, service, result)
	case "testing":
		buildTestingContext(ctx, d, service, result)
	case "reviewing":
		buildReviewingContext(ctx, d, service, result)
	default:
		buildEditingContext(ctx, d, service, result)
	}

	return JSONResult(result)
}

func HandleTestGaps(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.TestCorrelationStore == nil {
		return NewToolResultError("TestCorrelationStore not configured"), nil
	}

	service := ArgString(args, "service")
	limit := ArgInt(args, "limit", 10, 100)

	paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query test gaps: %v", err)), nil
	}

	if len(paths) == 0 {
		return EmptyResult("No uncovered error paths found.")
	}

	return JSONResult(map[string]any{
		"count": len(paths),
		"paths": paths,
	})
}

func HandleTestPriority(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.TestCorrelationStore == nil {
		return NewToolResultError("TestCorrelationStore not configured"), nil
	}

	fingerprint := ArgString(args, "fingerprint")
	if fingerprint == "" {
		return NewToolResultError("fingerprint parameter is required"), nil
	}

	result := map[string]any{
		"fingerprint": fingerprint,
	}

	path, err := d.TestCorrelationStore.GetByFingerprint(ctx, fingerprint)
	if err == nil && path != nil {
		result["uncovered_path"] = path
	}

	if d.ErrorGroupStore != nil {
		group, err := d.ErrorGroupStore.Get(ctx, fingerprint)
		if err == nil && group != nil {
			result["error_group"] = group
		}

		events, err := d.ErrorGroupStore.ListEvents(ctx, fingerprint, 5)
		if err == nil && len(events) > 0 {
			result["recent_events"] = events
		}
	}

	return JSONResult(result)
}

// --- helpers ---

func formatRiskSummary(e *store.CodeEntity) string {
	if e.RiskScore >= 0.7 {
		return fmt.Sprintf("HIGH RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	} else if e.RiskScore >= 0.3 {
		return fmt.Sprintf("MEDIUM RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	}
	return fmt.Sprintf("LOW RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
}

func classifyTaskType(task string) string {
	lower := strings.ToLower(task)

	debugKeywords := []string{
		"debug", "investigating", "bug", "error", "failing", "crash",
		"broken", "outage", "incident", "slow", "timeout", "spike",
	}
	for _, kw := range debugKeywords {
		if strings.Contains(lower, kw) {
			return "debugging"
		}
	}

	deployKeywords := []string{
		"deploy", "releasing", "shipping", "rollback", "rollout",
	}
	for _, kw := range deployKeywords {
		if strings.Contains(lower, kw) {
			return "deploying"
		}
	}

	testKeywords := []string{
		"test", "coverage", "spec", "assert",
	}
	for _, kw := range testKeywords {
		if strings.Contains(lower, kw) {
			return "testing"
		}
	}

	reviewKeywords := []string{
		"review", "pr ", "pull request", "code review",
	}
	for _, kw := range reviewKeywords {
		if strings.Contains(lower, kw) {
			return "reviewing"
		}
	}

	return "editing"
}
