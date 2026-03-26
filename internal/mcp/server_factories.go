package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/mcp/tools"
	"github.com/adham90/opentrace/internal/metrics"
	"github.com/adham90/opentrace/pkg/store"
)

// rankingServiceAdapter returns a tools.SuggestionRanker that bridges
// the consolidated tools package's SuggestionRanker interface with the
// parent mcp package's RankingService and SessionTracker.
func rankingServiceAdapter() tools.SuggestionRanker {
	if rankingService == nil || sessionTracker == nil {
		return nil
	}
	return &rankerAdapter{}
}

// rankerAdapter implements tools.SuggestionRanker using the package-level
// rankingService and sessionTracker.
type rankerAdapter struct{}

func (r *rankerAdapter) RankAndTrack(suggestions []tools.ToolSuggestion) []tools.ToolSuggestion {
	if rankingService == nil || sessionTracker == nil {
		return suggestions
	}
	sess := sessionTracker.CurrentSession()
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
	ranked := rankingService.RankSuggestions(context.Background(), RankingRequest{
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
	sessionTracker.SetLastSuggestions(mcpSuggestions)

	return result
}

// maybeAddTool registers a tool on the MCP server if s is non-nil.
// When s is nil (catalog-only mode), this is a no-op.
// If activity logging is enabled, wraps the handler to record tool calls.
func maybeAddTool(s *server.MCPServer, tool mcp.Tool, handler server.ToolHandlerFunc) {
	if s != nil {
		// Wrap with Prometheus metrics recording (always active).
		handler = wrapWithMetrics(tool.Name, handler)
		if activityStoreForLogging != nil {
			handler = wrapWithActivityLog(activityStoreForLogging, tool.Name, handler)
		}
		s.AddTool(tool, handler)
	}
}

// wrapWithMetrics wraps a tool handler to record Prometheus metrics for each call.
func wrapWithMetrics(toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metrics.RecordMCPToolCall(toolName)
		return handler(ctx, request)
	}
}

// wrapWithActivityLog wraps a tool handler to log its execution to the activity store.
func wrapWithActivityLog(as store.MCPActivityStore, toolName string, handler server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Snapshot the suggestions from the PREVIOUS tool's response before this
		// handler runs and overwrites them with its own suggestions.
		var priorSuggestions []ToolSuggestion
		if sessionTracker != nil {
			priorSuggestions = sessionTracker.SnapshotLastSuggestions()
		}

		start := time.Now()
		result, err := handler(ctx, request)
		elapsed := time.Since(start).Milliseconds()

		// Build a brief preview of args
		argsPreview := ""
		if args := request.GetArguments(); len(args) > 0 {
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
			if txt, ok := result.Content[0].(mcp.TextContent); ok {
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
		if sessionTracker != nil {
			if uid := sessionTracker.UserID(); uid != "" {
				userID = uid
			}
			if sid := sessionTracker.CurrentSessionID(); sid != "" {
				invSessionID = sid
				sessionID = sid
			}
			stepIndex = sessionTracker.RecordStep(toolName, isError, priorSuggestions)
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
		// WasSuggested/SuggestionRank and PreviousStepIndex are included so the
		// Log method can handle both INSERT and previous-step UPDATE atomically,
		// avoiding races between async INSERT and sync UPDATE.
		if activityLogger != nil {
			prevStep := 0
			if stepIndex > 1 {
				prevStep = stepIndex - 1
			}
			activityLogger.Log(store.LogMCPActivityParams{
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
		if contextInjector != nil && sessionTracker != nil && result != nil && !result.IsError && len(result.Content) > 0 {
			if sess := sessionTracker.CurrentSession(); sess != nil && sess.Intent == IntentInvestigation {
				if txt, ok := result.Content[0].(mcp.TextContent); ok {
					if enriched := InjectContextIntoResult(contextInjector, sess, toolName, txt.Text); enriched != txt.Text {
						result.Content[0] = mcp.NewTextContent(enriched)
					}
				}
			}
		}

		return result, err
	}
}
