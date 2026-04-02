package tools

import (
	"context"
	"fmt"


	"github.com/adham90/opentrace/pkg/store"
)

// DependenciesDeps holds stores needed by the dependencies tool.
type DependenciesDeps struct {
	AnalyticsStore  store.AnalyticsStore
	ErrorGroupStore store.ErrorGroupStore
	CodeEntityStore store.CodeEntityStore
	LogStore        store.LogStore
}

// DependenciesHandler returns a handler for the dependencies tool.
func DependenciesHandler(d DependenciesDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "service":
			return HandleDepsService(ctx, d, args)
		case "blast_radius":
			return HandleDepsBlastRadius(ctx, d, args)
		case "change_risk":
			return HandleDepsChangeRisk(ctx, d, args)
		default:
			return NewToolResultError("unknown action: " + action + ". Use: service, blast_radius, change_risk"), nil
		}
	}
}

func HandleDepsService(ctx context.Context, d DependenciesDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	if service == "" {
		return NewToolResultError("service is required"), nil
	}

	result := map[string]any{
		"service": service,
	}

	// Get endpoint analytics for this service
	if d.AnalyticsStore != nil {
		summary, err := d.AnalyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
			Service: service,
		})
		if err == nil && summary != nil {
			result["traffic"] = map[string]any{
				"total_requests":   summary.TotalRequests,
				"error_rate":       fmt.Sprintf("%.2f%%", summary.ErrorRate*100),
				"avg_duration_ms":  summary.AvgDurationMs,
				"p95_duration_ms":  summary.P95DurationMs,
				"unique_endpoints": summary.UniqueEndpoints,
			}
		}

		eps, err := d.AnalyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
			Service: service,
			SortBy:  "request_count",
			Limit:   10,
		})
		if err == nil && len(eps) > 0 {
			var endpoints []map[string]any
			for _, ep := range eps {
				path := ep.PathPattern
				if path == "" {
					path = ep.Controller + "#" + ep.Action
				}
				endpoints = append(endpoints, map[string]any{
					"method":   ep.Method,
					"path":     path,
					"requests": ep.RequestCount,
					"errors":   ep.ErrorCount,
					"avg_ms":   ep.AvgDurationMs,
					"p95_ms":   ep.P95DurationMs,
				})
			}
			result["endpoints"] = endpoints
		}
	}

	// Get error groups for this service
	if d.ErrorGroupStore != nil {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Service: service,
			Status:  store.ErrorGroupUnresolved,
			Limit:   5,
			SortBy:  "last_seen_at",
		})
		if err == nil && len(groups) > 0 {
			var errors []map[string]any
			for _, g := range groups {
				errors = append(errors, map[string]any{
					"fingerprint":    g.Fingerprint,
					"class":          g.ExceptionClass,
					"occurrences":    g.OccurrenceCount,
					"last_seen":      g.LastSeenAt.Format("2006-01-02 15:04"),
				})
			}
			result["open_errors"] = errors
		}
	}

	// Get code entity risks for this service
	if d.CodeEntityStore != nil {
		entities, err := d.CodeEntityStore.TopByRisk(ctx, service, 5)
		if err == nil && len(entities) > 0 {
			var risky []map[string]any
			for _, e := range entities {
				risky = append(risky, map[string]any{
					"name":       e.EntityName,
					"risk_score": e.RiskScore,
					"errors":     e.ErrorCount,
				})
			}
			result["risky_code"] = risky
		}
	}

	return JSONResult(result)
}

func HandleDepsBlastRadius(ctx context.Context, d DependenciesDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	if service == "" {
		return NewToolResultError("service is required"), nil
	}

	result := map[string]any{
		"service": service,
	}

	// Traffic volume indicates blast radius
	if d.AnalyticsStore != nil {
		summary, err := d.AnalyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
			Service: service,
		})
		if err == nil && summary != nil {
			impact := "low"
			if summary.TotalRequests > 10000 {
				impact = "critical"
			} else if summary.TotalRequests > 1000 {
				impact = "high"
			} else if summary.TotalRequests > 100 {
				impact = "medium"
			}

			result["impact_level"] = impact
			result["requests_affected"] = summary.TotalRequests
			result["unique_endpoints_affected"] = summary.UniqueEndpoints

			if impact == "critical" || impact == "high" {
				result["recommendation"] = "Use canary deployment. Set up watchers for error rate and latency before deploying changes."
			} else {
				result["recommendation"] = "Standard deployment acceptable. Monitor error rates after deploy."
			}
		}
	}

	return JSONResult(result)
}

func HandleDepsChangeRisk(ctx context.Context, d DependenciesDeps, args map[string]any) (*CallToolResult, error) {
	file := ArgString(args, "file")
	endpoint := ArgString(args, "endpoint")
	service := ArgString(args, "service")

	if file == "" && endpoint == "" {
		return NewToolResultError("file or endpoint is required"), nil
	}

	result := map[string]any{}
	var riskFactors []map[string]any
	totalRisk := 0.0

	// Check traffic volume for the endpoint
	if d.AnalyticsStore != nil && service != "" {
		summary, err := d.AnalyticsStore.TrafficSummary(ctx, store.AnalyticsParams{
			Service: service,
		})
		if err == nil && summary != nil && summary.TotalRequests > 0 {
			trafficRisk := 0.0
			if summary.TotalRequests > 10000 {
				trafficRisk = 0.9
			} else if summary.TotalRequests > 1000 {
				trafficRisk = 0.6
			} else if summary.TotalRequests > 100 {
				trafficRisk = 0.3
			}
			riskFactors = append(riskFactors, map[string]any{
				"factor": "traffic_volume",
				"score":  trafficRisk,
				"detail": fmt.Sprintf("%d requests in analysis window", summary.TotalRequests),
			})
			totalRisk += trafficRisk * 0.3
		}
	}

	// Check code entity risk
	if d.CodeEntityStore != nil && file != "" {
		controller := controllerFromPath(file)
		entity, err := d.CodeEntityStore.GetByName(ctx, store.CodeEntityEndpoint, controller, service)
		if err == nil && entity != nil {
			riskFactors = append(riskFactors, map[string]any{
				"factor": "code_risk_score",
				"score":  entity.RiskScore,
				"detail": fmt.Sprintf("Risk score %.2f (%d errors, %d investigations)", entity.RiskScore, entity.ErrorCount, entity.InvestigationCount),
			})
			totalRisk += entity.RiskScore * 0.3
		}
	}

	// Check for open errors
	if d.ErrorGroupStore != nil && service != "" {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Service: service,
			Status:  store.ErrorGroupUnresolved,
			Limit:   5,
		})
		if err == nil && len(groups) > 0 {
			errorRisk := float64(len(groups)) * 0.1
			if errorRisk > 0.5 {
				errorRisk = 0.5
			}
			riskFactors = append(riskFactors, map[string]any{
				"factor": "existing_errors",
				"score":  errorRisk,
				"detail": fmt.Sprintf("%d unresolved error groups in this service", len(groups)),
			})
			totalRisk += errorRisk * 0.2
		}
	}

	overallRisk := "low"
	if totalRisk > 0.7 {
		overallRisk = "critical"
	} else if totalRisk > 0.4 {
		overallRisk = "high"
	} else if totalRisk > 0.2 {
		overallRisk = "medium"
	}

	result["overall_risk"] = overallRisk
	result["risk_score"] = fmt.Sprintf("%.2f", totalRisk)
	result["risk_factors"] = riskFactors

	// Deployment recommendation
	switch overallRisk {
	case "critical":
		result["deployment_recommendation"] = "canary deployment with 30 minute observation window"
	case "high":
		result["deployment_recommendation"] = "canary deployment with 15 minute observation window"
	case "medium":
		result["deployment_recommendation"] = "standard deployment with error rate monitoring"
	default:
		result["deployment_recommendation"] = "standard deployment"
	}

	// Suggested watchers
	if service != "" {
		result["suggested_watchers"] = []map[string]string{
			{"metric": "error_rate", "threshold": "> 1%", "window": "15 minutes"},
			{"metric": "p95_duration", "threshold": "> 2x baseline", "window": "15 minutes"},
		}
	}

	return JSONResult(result)
}
