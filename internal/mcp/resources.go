package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
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

	// 4. Current configuration: opentrace://config/current
	if deps.SettingsStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://config/current",
				"Current Configuration",
				mcp.WithResourceDescription("Current configuration (retention settings, MCP name, limits)"),
				mcp.WithMIMEType("application/json"),
			),
			configCurrentHandler(deps.SettingsStore),
		)
	}

	// 5. Service list: opentrace://services/list
	if deps.LogStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://services/list",
				"Service List",
				mcp.WithResourceDescription("List all known service names from logs"),
				mcp.WithMIMEType("application/json"),
			),
			servicesListHandler(deps.LogStore),
		)
	}

	// 6. Connector status: opentrace://connectors/status
	if deps.DSStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://connectors/status",
				"Connector Status",
				mcp.WithResourceDescription("Active connector status"),
				mcp.WithMIMEType("application/json"),
			),
			connectorsStatusHandler(deps.DSStore),
		)
	}

	// 7. Health check summary: opentrace://healthchecks/summary
	if deps.HealthCheckStore != nil {
		s.AddResource(
			mcp.NewResource(
				"opentrace://healthchecks/summary",
				"Health Check Summary",
				mcp.WithResourceDescription("Current health check status"),
				mcp.WithMIMEType("application/json"),
			),
			healthchecksSummaryHandler(deps.HealthCheckStore),
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

func configCurrentHandler(ss store.SettingsStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		result := map[string]any{}

		retention, err := ss.GetRetention(ctx)
		if err == nil && retention != nil {
			result["retention_days"] = retention.RetentionDays
			result["metric_retention_days"] = retention.MetricRetentionDays
		}

		maxRows, err := ss.GetMaxQueryRows(ctx)
		if err == nil {
			result["max_query_rows"] = maxRows
		}

		timeout, err := ss.GetStatementTimeout(ctx)
		if err == nil {
			result["statement_timeout_ms"] = timeout
		}

		mcpName, err := ss.GetMCPName(ctx)
		if err == nil {
			result["mcp_name"] = mcpName
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://config/current",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}

func servicesListHandler(ls store.LogStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		now := time.Now().UTC()
		services, err := ls.DistinctValues(ctx, "service", store.LogCountParams{
			Since: now.Add(-30 * 24 * time.Hour),
			Until: now,
		})
		if err != nil {
			return nil, fmt.Errorf("listing services: %w", err)
		}

		result := map[string]any{
			"services": services,
			"count":    len(services),
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://services/list",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}

func connectorsStatusHandler(ds store.DataSourceStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		sources, err := ds.List(ctx, store.ListDataSourceParams{})
		if err != nil {
			return nil, fmt.Errorf("listing connectors: %w", err)
		}

		connectors := make([]map[string]any, 0, len(sources))
		for _, src := range sources {
			entry := map[string]any{
				"id":     src.ID.String(),
				"name":   src.Name,
				"type":   string(src.Type),
				"status": string(src.Status),
			}
			if src.LastTestedAt != nil {
				entry["last_tested_at"] = src.LastTestedAt.Format(time.RFC3339)
			}
			connectors = append(connectors, entry)
		}

		result := map[string]any{
			"connectors": connectors,
			"count":      len(connectors),
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://connectors/status",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}

func healthchecksSummaryHandler(hcs store.HealthCheckStore) server.ResourceHandlerFunc {
	return func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		summaries, err := hcs.UptimeSummaries(ctx, time.Now().Add(-24*time.Hour))
		if err != nil {
			return nil, fmt.Errorf("listing health checks: %w", err)
		}

		checks := make([]map[string]any, 0, len(summaries))
		for _, s := range summaries {
			checks = append(checks, map[string]any{
				"id":             s.HealthCheckID,
				"name":           s.Name,
				"url":            s.URL,
				"status":         s.CurrentStatus,
				"uptime_percent": s.UptimePct,
				"total_checks":   s.TotalChecks,
				"down_checks":    s.DownChecks,
			})
		}

		result := map[string]any{
			"healthchecks": checks,
			"count":        len(checks),
		}

		data, _ := json.Marshal(result)
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "opentrace://healthchecks/summary",
				MIMEType: "application/json",
				Text:     string(data),
			},
		}, nil
	}
}
