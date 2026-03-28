package tools

import (
	"context"
	"encoding/json"
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
		action, _ := args["action"].(string)

		switch action {
		case "risk":
			return handleCodeRisk(ctx, d, args)
		case "fragile":
			return handleFragile(ctx, d, args)
		case "context":
			return handleCodeContext(ctx, d, args)
		case "test_gaps":
			return handleTestGaps(ctx, d, args)
		case "test_priority":
			return handleTestPriority(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use risk, fragile, context, test_gaps, test_priority)", action)), nil
		}
	}
}

func handleCodeRisk(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	service, _ := args["service"].(string)

	var files []string
	if rawFiles, ok := args["files"]; ok {
		switch v := rawFiles.(type) {
		case []any:
			for _, f := range v {
				if s, ok := f.(string); ok {
					files = append(files, s)
				}
			}
		}
	}

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

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func handleFragile(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	service, _ := args["service"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > 50 {
		limit = 50
	}

	entities, err := d.CodeEntityStore.TopByRisk(ctx, service, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query fragile code: %v", err)), nil
	}

	if len(entities) == 0 {
		return NewToolResultText(`{"message":"No code entities with risk data found","entities":[]}`), nil
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

	resp := map[string]any{
		"count":    len(items),
		"entities": items,
	}

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func handleCodeContext(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	// Check if this is a task-based context request
	task, _ := args["task"].(string)
	if task != "" {
		return handleTaskContext(ctx, d, args, task)
	}

	if d.CodeEntityStore == nil {
		return NewToolResultError("CodeEntityStore not configured"), nil
	}

	entityName, _ := args["entity_name"].(string)
	if entityName == "" {
		return NewToolResultError("entity_name is required"), nil
	}
	service, _ := args["service"].(string)

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
		data, _ := json.Marshal(map[string]any{
			"message":     fmt.Sprintf("No code entity found for %q", entityName),
			"entity_name": entityName,
			"service":     service,
		})
		return NewToolResultText(string(data)), nil
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

	data, _ := json.Marshal(resp)
	return NewToolResultText(string(data)), nil
}

func handleTaskContext(ctx context.Context, d CodeIntelDeps, args map[string]any, task string) (*CallToolResult, error) {
	service, _ := args["service"].(string)

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

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal context: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func handleTestGaps(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.TestCorrelationStore == nil {
		return NewToolResultError("TestCorrelationStore not configured"), nil
	}

	service, _ := args["service"].(string)

	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}

	paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query test gaps: %v", err)), nil
	}

	if len(paths) == 0 {
		return NewToolResultText("No uncovered error paths found."), nil
	}

	result := map[string]any{
		"count": len(paths),
		"paths": paths,
	}

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
}

func handleTestPriority(ctx context.Context, d CodeIntelDeps, args map[string]any) (*CallToolResult, error) {
	if d.TestCorrelationStore == nil {
		return NewToolResultError("TestCorrelationStore not configured"), nil
	}

	fingerprint, _ := args["fingerprint"].(string)
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

	data, err := json.Marshal(result)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
	}
	return NewToolResultText(string(data)), nil
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

func buildDebuggingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.InvestigationSessionStore != nil {
		params := store.ListInvestigationSessionParams{
			Status: store.InvestigationStatusOpen,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if sessions, err := d.InvestigationSessionStore.List(ctx, params); err == nil && len(sessions) > 0 {
			result["active_investigations"] = sessions
		}
	}

	if d.AgentNoteStore != nil && service != "" {
		if note, err := d.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}
}

func buildDeployingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 5); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	if d.TestCorrelationStore != nil {
		if paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, 5); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}
}

func buildTestingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.TestCorrelationStore != nil {
		if paths, err := d.TestCorrelationStore.TopByPriority(ctx, service, 10); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}
}

func buildReviewingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}

	if d.DeployStore != nil {
		if deploys, err := d.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["related_deploys"] = deploys
		}
	}
}

func buildEditingContext(ctx context.Context, d CodeIntelDeps, service string, result map[string]any) {
	if d.CodeEntityStore != nil {
		if entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	if d.AgentNoteStore != nil && service != "" {
		if note, err := d.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}

	if d.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  3,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := d.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}
}
