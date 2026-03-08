package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// addResources registers MCP resources on the server.
func addResources(s *server.MCPServer, deps Deps) {
	// 1. Service status template: opentrace://services/{service}/status
	if deps.ErrorGroupStore != nil || deps.DeployStore != nil || deps.CodeEntityStore != nil {
		s.AddResourceTemplate(
			mcp.NewResourceTemplate(
				"opentrace://services/{service}/status",
				"Service Status",
				mcp.WithTemplateDescription("Live status for a service: error counts, recent deploys, top risks"),
				mcp.WithTemplateMIMEType("application/json"),
			),
			serviceStatusHandler(deps),
		)
	}

	// 2. Code risk summary: opentrace://code/risk-summary
	if deps.CodeEntityStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://code/risk-summary",
				"Code Risk Summary",
				mcp.WithResourceDescription("Top 10 risky code entities across all services"),
				mcp.WithMIMEType("application/json"),
			),
			codeRiskSummaryHandler(deps.CodeEntityStore),
		)
	}

	// 3. Active investigations: opentrace://investigations/active
	if deps.InvestigationSessionStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://investigations/active",
				"Active Investigations",
				mcp.WithResourceDescription("Currently open investigation sessions"),
				mcp.WithMIMEType("application/json"),
			),
			activeInvestigationsHandler(deps.InvestigationSessionStore),
		)
	}
}

func serviceStatusHandler(deps Deps) server.ResourceTemplateHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		uri := request.Params.URI
		// Extract service from URI: opentrace://services/{service}/status
		service := ""
		if parts := strings.Split(uri, "/"); len(parts) >= 4 {
			service = parts[3]
		}
		if service == "" {
			return nil, fmt.Errorf("service name required in URI")
		}

		result := map[string]any{
			"service": service,
		}

		// Error count
		if deps.ErrorGroupStore != nil {
			groups, err := deps.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
				Service: service,
				Status:  store.ErrorGroupUnresolved,
				Limit:   5,
			})
			if err == nil {
				result["active_errors"] = len(groups)
				errorSummaries := make([]map[string]any, 0, len(groups))
				for _, g := range groups {
					errorSummaries = append(errorSummaries, map[string]any{
						"fingerprint":     g.Fingerprint,
						"exception_class": g.ExceptionClass,
						"total_count":     g.OccurrenceCount,
						"last_seen_at":    g.LastSeenAt.Format(time.RFC3339),
					})
				}
				result["top_errors"] = errorSummaries
			}
		}

		// Recent deploys
		if deps.DeployStore != nil {
			deploys, err := deps.DeployStore.GetRecent(ctx, service, 3)
			if err == nil {
				deploySummaries := make([]map[string]any, 0, len(deploys))
				for _, d := range deploys {
					deploySummaries = append(deploySummaries, map[string]any{
						"id":          d.ID,
						"commit_hash": d.CommitHash,
						"status":      d.Status,
						"deployed_at": d.DeployedAt.Format(time.RFC3339),
					})
				}
				result["recent_deploys"] = deploySummaries
			}
		}

		// Top risks
		if deps.CodeEntityStore != nil {
			entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 3)
			if err == nil {
				riskSummaries := make([]map[string]any, 0, len(entities))
				for _, e := range entities {
					riskSummaries = append(riskSummaries, map[string]any{
						"entity_name": e.EntityName,
						"entity_type": e.EntityType,
						"risk_score":  e.RiskScore,
						"error_count": e.ErrorCount,
					})
				}
				result["top_risks"] = riskSummaries
			}
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}

func codeRiskSummaryHandler(ces store.CodeEntityStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		entities, err := ces.TopByRisk(ctx, "", 10)
		if err != nil {
			return nil, fmt.Errorf("querying top risks: %w", err)
		}

		result := map[string]any{
			"count":    len(entities),
			"entities": entities,
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://code/risk-summary",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}

func activeInvestigationsHandler(iss store.InvestigationSessionStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		sessions, err := iss.List(ctx, store.ListInvestigationSessionParams{
			Status: store.InvestigationStatusOpen,
			Limit:  20,
		})
		if err != nil {
			return nil, fmt.Errorf("listing active investigations: %w", err)
		}

		summaries := make([]map[string]any, 0, len(sessions))
		for _, s := range sessions {
			summaries = append(summaries, map[string]any{
				"id":         s.ID,
				"user_email": s.UserEmail,
				"intent":     s.Intent,
				"service":    s.PrimaryService,
				"steps":      s.TotalSteps,
				"started_at": s.StartedAt.Format(time.RFC3339),
			})
		}

		result := map[string]any{
			"count":          len(sessions),
			"investigations": summaries,
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://investigations/active",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}
