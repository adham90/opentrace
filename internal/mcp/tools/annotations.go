package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/pkg/store"
)

// AnnotationsDeps holds stores needed by the annotations tool.
type AnnotationsDeps struct {
	AnalyticsStore   store.AnalyticsStore
	ErrorGroupStore  store.ErrorGroupStore
	CodeEntityStore  store.CodeEntityStore
	LogStore         store.LogStore
}

// AnnotationsTool returns the tool definition for production code annotations.
func AnnotationsTool() mcp.Tool {
	return mcp.NewTool("annotations",
		mcp.WithDescription(`Production code annotations — live runtime metrics for source code.

When you're about to modify a file, call this tool to see how it behaves in production:
call counts, error rates, latency percentiles, risk scores, and open errors.

Actions:
- file: Get production metrics for all endpoints/functions mapped to a source file
- function: Get detailed metrics for a specific endpoint (method + path)
- hotspots: Get the top N most error-prone or risky code entities across the codebase`),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: file, function, hotspots")),
		mcp.WithString("path", mcp.Description("Source file path for 'file' action (e.g., app/controllers/payments_controller.rb)")),
		mcp.WithString("controller", mcp.Description("Controller name for 'function' action (e.g., PaymentsController)")),
		mcp.WithString("function_name", mcp.Description("Function/action name for 'function' action (e.g., charge)")),
		mcp.WithString("method", mcp.Description("HTTP method for 'function' action (e.g., POST)")),
		mcp.WithString("endpoint", mcp.Description("Endpoint path for 'function' action (e.g., /api/payments/charge)")),
		mcp.WithString("service", mcp.Description("Filter by service name")),
		mcp.WithNumber("limit", mcp.Description("Number of results for 'hotspots' (default: 10, max: 50)")),
	)
}

// AnnotationsHandler returns a handler for the annotations tool.
func AnnotationsHandler(d AnnotationsDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		action, _ := args["action"].(string)

		switch action {
		case "file":
			return handleAnnotationsFile(ctx, d, args)
		case "function":
			return handleAnnotationsFunction(ctx, d, args)
		case "hotspots":
			return handleAnnotationsHotspots(ctx, d, args)
		default:
			return mcp.NewToolResultError("unknown action: " + action + ". Use: file, function, hotspots"), nil
		}
	}
}

func handleAnnotationsFile(ctx context.Context, d AnnotationsDeps, args map[string]any) (*mcp.CallToolResult, error) {
	filePath, _ := args["path"].(string)
	if filePath == "" {
		return mcp.NewToolResultError("path is required for file action"), nil
	}

	result := map[string]any{
		"file": filePath,
	}

	// Derive controller name from file path using conventions
	controller := controllerFromPath(filePath)

	// Get code entity risk if available
	if d.CodeEntityStore != nil && controller != "" {
		entity, err := d.CodeEntityStore.GetByName(ctx, store.CodeEntityEndpoint, controller, "")
		if err == nil && entity != nil {
			result["risk_score"] = entity.RiskScore
			result["error_count"] = entity.ErrorCount
			result["investigation_count"] = entity.InvestigationCount
		}
	}

	// Get endpoint analytics if available
	if d.AnalyticsStore != nil && controller != "" {
		service, _ := args["service"].(string)
		eps, err := d.AnalyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
			Service: service,
			Limit:   20,
		})
		if err == nil && len(eps) > 0 {
			var annotations []map[string]any
			for _, ep := range eps {
				path := ep.PathPattern
				if path == "" {
					path = ep.Controller + "#" + ep.Action
				}
				errRate := 0.0
				if ep.RequestCount > 0 {
					errRate = float64(ep.ErrorCount) / float64(ep.RequestCount) * 100
				}
				ann := map[string]any{
					"function":      ep.Action,
					"method":        ep.Method,
					"path":          path,
					"calls_per_day": ep.RequestCount,
					"error_rate":    fmt.Sprintf("%.2f%%", errRate),
					"avg_ms":        ep.AvgDurationMs,
					"p95_ms":        ep.P95DurationMs,
				}
				annotations = append(annotations, ann)
			}
			result["endpoints"] = annotations
		}
	}

	// Get open errors for this file/controller
	if d.ErrorGroupStore != nil && controller != "" {
		groups, err := d.ErrorGroupStore.List(ctx, store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		})
		if err == nil {
			var openErrors []map[string]any
			for _, g := range groups {
				// Match errors that mention this controller/file
				if strings.Contains(g.ExceptionClass, controller) ||
					strings.Contains(g.Fingerprint, controller) ||
					strings.Contains(g.SourceFile, filePath) {
					openErrors = append(openErrors, map[string]any{
						"fingerprint":     g.Fingerprint,
						"exception_class": g.ExceptionClass,
						"message":         truncate(g.Message, 100),
						"occurrences":     g.OccurrenceCount,
						"last_seen":       g.LastSeenAt.Format("2006-01-02 15:04"),
					})
				}
			}
			if len(openErrors) > 0 {
				result["open_errors"] = openErrors
			}
		}
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func handleAnnotationsFunction(ctx context.Context, d AnnotationsDeps, args map[string]any) (*mcp.CallToolResult, error) {
	controller, _ := args["controller"].(string)
	endpoint, _ := args["endpoint"].(string)
	service, _ := args["service"].(string)

	if controller == "" && endpoint == "" {
		return mcp.NewToolResultError("controller or endpoint is required for function action"), nil
	}

	result := map[string]any{}

	if d.AnalyticsStore != nil {
		eps, err := d.AnalyticsStore.TopEndpoints(ctx, store.TopEndpointParams{
			Service: service,
			Limit:   10,
		})
		if err == nil && len(eps) > 0 {
			ep := eps[0]
			errRate := 0.0
			if ep.RequestCount > 0 {
				errRate = float64(ep.ErrorCount) / float64(ep.RequestCount) * 100
			}
			result["endpoint"] = ep.PathPattern
			result["method"] = ep.Method
			result["controller"] = ep.Controller
			result["action"] = ep.Action
			result["calls_per_day"] = ep.RequestCount
			result["error_count"] = ep.ErrorCount
			result["error_rate"] = fmt.Sprintf("%.2f%%", errRate)
			result["avg_duration_ms"] = ep.AvgDurationMs
			result["p95_duration_ms"] = ep.P95DurationMs
		} else {
			result["message"] = "No analytics data found for this endpoint"
		}
	}

	data, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(data)), nil
}

func handleAnnotationsHotspots(ctx context.Context, d AnnotationsDeps, args map[string]any) (*mcp.CallToolResult, error) {
	limit := 10
	if v, ok := args["limit"].(float64); ok && v > 0 {
		limit = int(v)
	}
	if limit > 50 {
		limit = 50
	}
	service, _ := args["service"].(string)

	var hotspots []map[string]any

	// Get risky code entities
	if d.CodeEntityStore != nil {
		entities, err := d.CodeEntityStore.TopByRisk(ctx, service, limit)
		if err == nil {
			for _, e := range entities {
				hotspots = append(hotspots, map[string]any{
					"name":                e.EntityName,
					"entity_type":         e.EntityType,
					"service":             e.Service,
					"risk_score":          e.RiskScore,
					"error_count":         e.ErrorCount,
					"investigation_count": e.InvestigationCount,
				})
			}
		}
	}

	if len(hotspots) == 0 {
		return mcp.NewToolResultText(`{"message": "No code entity data available. Code risk scores are computed from error and investigation data over time."}`), nil
	}

	data, _ := json.Marshal(map[string]any{
		"hotspots": hotspots,
		"count":    len(hotspots),
	})
	return mcp.NewToolResultText(string(data)), nil
}

// controllerFromPath extracts a controller name from a file path using conventions.
// e.g., "app/controllers/payments_controller.rb" → "PaymentsController"
// e.g., "internal/handlers/payment.go" → "payment"
func controllerFromPath(path string) string {
	// Get filename without extension
	parts := strings.Split(path, "/")
	filename := parts[len(parts)-1]

	// Remove extension
	if idx := strings.LastIndex(filename, "."); idx > 0 {
		filename = filename[:idx]
	}

	// Rails convention: payments_controller → PaymentsController
	if strings.HasSuffix(filename, "_controller") {
		return toPascalCase(filename)
	}

	// Generic: return the filename as-is (works for Go handlers, Node routes, etc.)
	return filename
}

func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var result strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			result.WriteString(strings.ToUpper(p[:1]))
			result.WriteString(p[1:])
		}
	}
	return result.String()
}

// truncate is defined in errors.go
