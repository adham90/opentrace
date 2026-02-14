package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// smartSearchTool returns the tool definition for smart_search.
func smartSearchTool() mcp.Tool {
	return mcp.NewTool("smart_search",
		mcp.WithDescription("Translate a natural language question into structured log search filters using AI. Returns filters you can pass to log_search. Use when the user describes a problem in plain English (e.g. 'show me authentication failures from the last hour') instead of specifying filters directly."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Natural language search query (e.g. 'errors from checkout service last hour', 'TLS handshake failures', 'slow database queries yesterday')")),
	)
}

// smartSearchHandler returns a handler that uses AI to translate natural language to log filters.
func smartSearchHandler(logStore store.LogStore, cfg *config.Config) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return mcp.NewToolResultError("query is required. Describe what you're looking for in plain English."), nil
		}

		// Get an LLM provider
		provider, err := llm.NewProviderByName(cfg.LLMProvider, cfg)
		if err != nil {
			return mcp.NewToolResultError("Smart search requires a configured LLM provider. Set OPENTRACE_LLM_PROVIDER."), nil
		}

		// Fetch available log metadata for context
		var services, eventTypes, metadataKeys []string
		if logStore != nil {
			params := store.LogCountParams{
				Since: time.Now().Add(-30 * 24 * time.Hour),
				Until: time.Now(),
			}
			if vals, err := logStore.DistinctValues(ctx, "service", params); err == nil {
				services = vals
			}
			if vals, err := logStore.DistinctValues(ctx, "event_type", params); err == nil {
				eventTypes = vals
			}
			if keys, err := logStore.MetadataKeys(ctx, params); err == nil {
				metadataKeys = keys
			}
		}

		// Build prompt
		prompt := buildSmartSearchPrompt(services, eventTypes, metadataKeys)

		resp, err := provider.ChatCompletion(ctx, llm.ChatRequest{
			Messages: []llm.ChatMessage{
				{Role: "system", Content: prompt},
				{Role: "user", Content: query},
			},
			JSONMode:  true,
			MaxTokens: 512,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("AI search failed: %v", err)), nil
		}

		// Parse and return the filters
		var result map[string]any
		if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
			return mcp.NewToolResultError("Failed to parse AI response. Try rephrasing your query."), nil
		}

		// Add usage hint
		result["_hint"] = "Pass these filters to the log_search tool to execute the search."

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func buildSmartSearchPrompt(services, eventTypes, metadataKeys []string) string {
	now := time.Now().UTC()
	var b strings.Builder

	b.WriteString("Convert the user's log search query into JSON filters. Respond with ONLY valid JSON.\n\n")

	b.WriteString("Available services: ")
	if len(services) > 0 {
		b.WriteString(strings.Join(services, ", "))
	} else {
		b.WriteString("(none)")
	}

	b.WriteString("\nAvailable event types: ")
	if len(eventTypes) > 0 {
		b.WriteString(strings.Join(eventTypes, ", "))
	} else {
		b.WriteString("(none)")
	}

	if len(metadataKeys) > 0 {
		b.WriteString("\nAvailable metadata keys: ")
		b.WriteString(strings.Join(metadataKeys, ", "))
	}

	b.WriteString("\nCurrent time: ")
	b.WriteString(now.Format(time.RFC3339))

	b.WriteString(`

JSON fields:
- "level": "ERROR", "WARN", "INFO", or "DEBUG"
- "service": Must match a service from the list above
- "event_type": Must match an event type from the list above
- "time_range": "15m", "1h", "6h", "24h", or "7d"
- "query": Specific keywords to search in log message text
- "reasoning": One sentence summary of interpretation

Leave fields as "" if not applicable. Only set service/event_type if the user mentioned them.
`)

	return b.String()
}
