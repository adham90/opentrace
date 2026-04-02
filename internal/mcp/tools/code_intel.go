package tools

import (
	"context"
	"fmt"

	"github.com/adham90/opentrace/pkg/store"
)

// CodeIntelDeps holds the stores needed by the code_intel tool.
type CodeIntelDeps struct {
	CodeEntityStore store.CodeEntityStore
	ErrorGroupStore store.ErrorGroupStore
	AgentNoteStore  store.AgentNoteStore
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
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use risk, fragile)", action)), nil
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


// --- helpers ---

func formatRiskSummary(e *store.CodeEntity) string {
	if e.RiskScore >= 0.7 {
		return fmt.Sprintf("HIGH RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	} else if e.RiskScore >= 0.3 {
		return fmt.Sprintf("MEDIUM RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	}
	return fmt.Sprintf("LOW RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
}

