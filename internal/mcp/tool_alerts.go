package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// checkAlertsHandler returns a handler that aggregates pending alerts,
// error spikes, deploy incidents, and high-risk entities.
func checkAlertsHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		service, _ := args["service"].(string)

		result := map[string]any{
			"service": service,
		}

		// Pending watch alerts
		if deps.WatchStore != nil {
			count, err := deps.WatchStore.CountPendingAlerts(ctx)
			if err == nil {
				result["pending_alerts"] = count
			}
		}

		// Error spikes (errors with >= 10 occurrences)
		if deps.ErrorGroupStore != nil {
			params := store.ListErrorGroupParams{
				Status: store.ErrorGroupUnresolved,
				Limit:  10,
			}
			if service != "" {
				params.Service = service
			}
			if groups, err := deps.ErrorGroupStore.List(ctx, params); err == nil {
				spikes := make([]map[string]any, 0)
				for _, g := range groups {
					if g.OccurrenceCount >= 10 {
						spikes = append(spikes, map[string]any{
							"fingerprint":     g.Fingerprint,
							"exception_class": g.ExceptionClass,
							"occurrence_count":     g.OccurrenceCount,
							"source_file":        g.SourceFile,
						})
					}
				}
				result["error_spikes"] = spikes
			}
		}

		// Deploy incidents
		if deps.DeployStore != nil {
			if deploys, err := deps.DeployStore.GetRecent(ctx, service, 5); err == nil {
				incidents := make([]map[string]any, 0)
				for _, d := range deploys {
					if d.Status == store.DeployStatusIncident {
						incidents = append(incidents, map[string]any{
							"id":          d.ID,
							"commit_hash": d.CommitHash,
							"service":     d.Service,
							"status":      d.Status,
						})
					}
				}
				result["deploy_incidents"] = incidents
			}
		}

		// High-risk entities (risk >= 0.7)
		if deps.CodeEntityStore != nil {
			if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil {
				highRisk := make([]map[string]any, 0)
				for _, e := range entities {
					if e.RiskScore >= 0.7 {
						highRisk = append(highRisk, map[string]any{
							"entity_name": e.EntityName,
							"entity_type": e.EntityType,
							"risk_score":  e.RiskScore,
							"error_count": e.ErrorCount,
						})
					}
				}
				result["high_risk_entities"] = highRisk
			}
		}

		// Parallel investigations
		if deps.InvestigationSessionStore != nil {
			params := store.ListInvestigationSessionParams{
				Status: store.InvestigationStatusOpen,
				Limit:  5,
			}
			if service != "" {
				params.Service = service
			}
			if sessions, err := deps.InvestigationSessionStore.List(ctx, params); err == nil && len(sessions) > 0 {
				invSummaries := make([]map[string]any, 0, len(sessions))
				for _, s := range sessions {
					invSummaries = append(invSummaries, map[string]any{
						"id":      s.ID,
						"user":    s.UserEmail,
						"intent":  s.Intent,
						"service": s.PrimaryService,
						"steps":   s.TotalSteps,
					})
				}
				result["parallel_investigations"] = invSummaries
			}
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal alerts: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// checkAlertsToolDef returns the mcp.Tool definition for check_alerts.
func checkAlertsToolDef() mcp.Tool {
	return mcp.NewTool("check_alerts",
		mcp.WithDescription(
			"Check for pending alerts, error spikes, deploy incidents, and high-risk entities. "+
				"Returns a unified view of everything that needs attention.",
		),
		mcp.WithString("service",
			mcp.Description("Service to check (optional — checks all services if omitted)")),
	)
}
