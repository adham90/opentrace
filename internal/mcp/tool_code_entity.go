package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// codeContextHandler looks up a code entity by name and returns error history + risk score.
func codeContextHandler(ces store.CodeEntityStore, egs store.ErrorGroupStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ces == nil {
			return mcp.NewToolResultError("CodeEntityStore not configured"), nil
		}

		args := request.GetArguments()
		entityName, _ := args["entity_name"].(string)
		if entityName == "" {
			return mcp.NewToolResultError("entity_name is required"), nil
		}
		service, _ := args["service"].(string)

		// Try file → controller → endpoint
		types := []store.CodeEntityType{store.CodeEntityFile, store.CodeEntityController, store.CodeEntityEndpoint}
		var entity *store.CodeEntity
		for _, t := range types {
			e, err := ces.GetByName(ctx, t, entityName, service)
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
			return mcp.NewToolResultText(string(data)), nil
		}

		resp := map[string]any{
			"entity":      entity,
			"risk_summary": formatRiskSummary(entity),
		}

		// Include related error groups if available
		if egs != nil && entity.EntityType == store.CodeEntityFile {
			groups, err := egs.List(ctx, store.ListErrorGroupParams{
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
		return mcp.NewToolResultText(string(data)), nil
	}
}

// whatsFragileHandler returns the riskiest code entities for a service.
func whatsFragileHandler(ces store.CodeEntityStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ces == nil {
			return mcp.NewToolResultError("CodeEntityStore not configured"), nil
		}

		args := request.GetArguments()
		service, _ := args["service"].(string)
		limit := 10
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		if limit > 50 {
			limit = 50
		}

		entities, err := ces.TopByRisk(ctx, service, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query fragile code: %v", err)), nil
		}

		if len(entities) == 0 {
			return mcp.NewToolResultText(`{"message":"No code entities with risk data found","entities":[]}`), nil
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
		return mcp.NewToolResultText(string(data)), nil
	}
}

// codeRiskHandler returns bulk risk scores for a list of files (pre-deploy safety check).
func codeRiskHandler(ces store.CodeEntityStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ces == nil {
			return mcp.NewToolResultError("CodeEntityStore not configured"), nil
		}

		args := request.GetArguments()
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
			return mcp.NewToolResultError("files array is required"), nil
		}

		entities, err := ces.BatchGetRisk(ctx, store.CodeEntityFile, files, service)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to get risk scores: %v", err)), nil
		}

		// Build a map of known entities
		known := make(map[string]*store.CodeEntity, len(entities))
		for i := range entities {
			known[entities[i].EntityName] = &entities[i]
		}

		// Build response with zero-risk for unknown files
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

		// Overall assessment
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
		return mcp.NewToolResultText(string(data)), nil
	}
}

func formatRiskSummary(e *store.CodeEntity) string {
	if e.RiskScore >= 0.7 {
		return fmt.Sprintf("HIGH RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	} else if e.RiskScore >= 0.3 {
		return fmt.Sprintf("MEDIUM RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
	}
	return fmt.Sprintf("LOW RISK: %d errors, %d investigations", e.ErrorCount, e.InvestigationCount)
}
