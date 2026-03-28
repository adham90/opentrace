package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/pkg/store"
)

// rankingServiceAdapter returns a tools.SuggestionRanker that bridges
// the consolidated tools package's SuggestionRanker interface with the
// parent mcp package's RankingService and SessionTracker.
func rankingServiceAdapter(deps Deps) tools.SuggestionRanker {
	if deps.RankingService == nil || deps.SessionTracker == nil {
		return nil
	}
	return &rankerAdapter{
		ranking: deps.RankingService,
		session: deps.SessionTracker,
	}
}

// rankerAdapter implements tools.SuggestionRanker using deps-provided services.
type rankerAdapter struct {
	ranking *RankingService
	session *SessionTracker
}

func (r *rankerAdapter) RankAndTrack(suggestions []tools.ToolSuggestion) []tools.ToolSuggestion {
	if r.ranking == nil || r.session == nil {
		return suggestions
	}
	sess := r.session.CurrentSession()
	if sess == nil {
		return suggestions
	}

	// Convert tools.ToolSuggestion -> mcp.ToolSuggestion for ranking.
	mcpSuggestions := make([]ToolSuggestion, len(suggestions))
	for i, s := range suggestions {
		mcpSuggestions[i] = ToolSuggestion{
			Tool:       s.Tool,
			Why:        s.Why,
			Args:       s.Args,
			Confidence: s.Confidence,
			Source:     s.Source,
			Evidence:   s.Evidence,
		}
	}

	currentTool := ""
	if len(sess.ToolSequence) > 0 {
		currentTool = sess.ToolSequence[len(sess.ToolSequence)-1]
	}
	ranked := r.ranking.RankSuggestions(context.Background(), RankingRequest{
		CurrentTool:         currentTool,
		Intent:              sess.Intent,
		StepIndex:           sess.TotalSteps,
		SessionTools:        sess.ToolSequence,
		FallbackSuggestions: mcpSuggestions,
	})

	if len(ranked) == 0 {
		return suggestions
	}

	// Convert back to tools.ToolSuggestion.
	result := make([]tools.ToolSuggestion, len(ranked))
	for i, s := range ranked {
		result[i] = tools.ToolSuggestion{
			Tool:       s.Tool,
			Why:        s.Why,
			Args:       s.Args,
			Confidence: s.Confidence,
			Source:     s.Source,
			Evidence:   s.Evidence,
		}
	}

	// Track for acceptance detection.
	r.session.SetLastSuggestions(mcpSuggestions)

	return result
}

// wrapWithMetrics wraps a tool handler to record Prometheus metrics for each call.
func wrapWithMetrics(toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metrics.RecordMCPToolCall(toolName)
		return handler(ctx, request)
	}
}

// wrapWithActivityLog wraps a tool handler to log its execution to the activity store.
func wrapWithActivityLog(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Snapshot the suggestions from the PREVIOUS tool's response before this
		// handler runs and overwrites them with its own suggestions.
		var priorSuggestions []ToolSuggestion
		if deps.SessionTracker != nil {
			priorSuggestions = deps.SessionTracker.SnapshotLastSuggestions()
		}

		start := time.Now()
		result, err := handler(ctx, request)
		elapsed := time.Since(start).Milliseconds()

		// Build a brief preview of args
		argsPreview := ""
		if args := GetArguments(request); len(args) > 0 {
			data, _ := json.Marshal(args)
			argsPreview = string(data)
			if len(argsPreview) > 500 {
				argsPreview = argsPreview[:500]
			}
		}

		// Build result preview
		isError := err != nil
		resultPreview := ""
		if result != nil && len(result.Content) > 0 {
			if txt, ok := result.Content[0].(*mcp.TextContent); ok {
				resultPreview = txt.Text
				if len(resultPreview) > 500 {
					resultPreview = resultPreview[:500]
				}
			}
			isError = isError || result.IsError
		}

		// Get real identity from session tracker.
		sessionID := "mcp"
		userID := ""
		invSessionID := ""
		stepIndex := 0
		if deps.SessionTracker != nil {
			if uid := deps.SessionTracker.UserID(); uid != "" {
				userID = uid
			}
			if sid := deps.SessionTracker.CurrentSessionID(); sid != "" {
				invSessionID = sid
				sessionID = sid
			}
			stepIndex = deps.SessionTracker.RecordStep(toolName, isError, priorSuggestions)
		}

		// Check if this tool was in the prior suggestions (before handler overwrote them).
		wasSuggested := false
		suggestionRank := 0
		for i, s := range priorSuggestions {
			if s.Tool == toolName {
				wasSuggested = true
				suggestionRank = i + 1
				break
			}
		}

		// Log via bounded activity logger to avoid unbounded goroutine growth.
		if deps.ActivityLogger != nil {
			prevStep := 0
			if stepIndex > 1 {
				prevStep = stepIndex - 1
			}
			deps.ActivityLogger.Log(store.LogMCPActivityParams{
				SessionID:              sessionID,
				UserID:                 userID,
				ToolName:               toolName,
				Arguments:              argsPreview,
				ResultPreview:          resultPreview,
				IsError:                isError,
				DurationMs:             &elapsed,
				EventType:              "tool_call",
				InvestigationSessionID: invSessionID,
				StepIndex:              stepIndex,
				WasSuggested:           wasSuggested,
				SuggestionRank:         suggestionRank,
				PreviousStepIndex:      prevStep,
			})
		}

		// Inject investigation context for investigation-intent sessions.
		if deps.ContextInjector != nil && deps.SessionTracker != nil && result != nil && !result.IsError && len(result.Content) > 0 {
			if sess := deps.SessionTracker.CurrentSession(); sess != nil && sess.Intent == IntentInvestigation {
				if txt, ok := result.Content[0].(*mcp.TextContent); ok {
					if enriched := InjectContextIntoResult(deps.ContextInjector, sess, toolName, txt.Text); enriched != txt.Text {
						result.Content[0] = &mcp.TextContent{Text: enriched}
					}
				}
			}
		}

		return result, err
	}
}

// wrapHandler applies activity logging and metrics to a handler.
func wrapHandler(deps Deps, toolName string, handler ToolHandlerFunc) ToolHandlerFunc {
	handler = wrapWithMetrics(toolName, handler)
	if deps.MCPActivityStore != nil {
		handler = wrapWithActivityLog(deps, toolName, handler)
	}
	return handler
}

// ---------------------------------------------------------------------------
// Legacy helpers (still needed for dynamic connector tools)
// ---------------------------------------------------------------------------

// convertTool maps a connector.Tool to an mcp.Tool with the appropriate
// JSON Schema properties derived from the tool's parameter definitions.
func convertTool(t connector.Tool) *mcp.Tool {
	props := make(map[string]SchemaProperty)
	var required []string
	for _, p := range t.Params {
		schemaType := "string"
		switch p.Type {
		case "int":
			schemaType = "number"
		case "bool":
			schemaType = "boolean"
		}
		props[p.Name] = SchemaProperty{Type: schemaType}
		if p.Required {
			required = append(required, p.Name)
		}
	}
	return &mcp.Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: ToolSchema(props, required),
	}
}

// bridgeHandler wraps a connector.Tool handler as an MCP ToolHandlerFunc.
func bridgeHandler(t connector.Tool) ToolHandlerFunc {
	return func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := GetArguments(request)
		if args == nil {
			args = make(map[string]any)
		}

		result, err := t.Handler(ctx, args)
		if err != nil {
			return NewToolResultError(err.Error()), nil
		}

		return NewToolResultText(result), nil
	}
}
