package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/store"
)

// LogsConnector implements DataSource for log search.
type LogsConnector struct {
	logStore store.LogStore
}

// NewLogsConnector creates a new LogsConnector.
func NewLogsConnector(logStore store.LogStore) *LogsConnector {
	return &LogsConnector{logStore: logStore}
}

func (c *LogsConnector) Type() ConnectorType { return ConnectorLogs }

func (c *LogsConnector) TestConnection(ctx context.Context) error {
	// Verify we can search (empty search)
	_, err := c.logStore.Search(ctx, store.LogSearchParams{Limit: 1})
	return err
}

func (c *LogsConnector) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Name: "log_search",
			Description: `Search ingested logs. Supports filtering by:
- query: full-text keyword search across log messages
- service: exact match on service name (e.g. "api-gateway", "auth-service")
- level: log level filter (debug, info, warn, error, fatal)
- trace_id: find all logs for a specific trace/request
- environment: filter by environment (e.g. "production", "staging")
- start: only logs after this time (ISO 8601, e.g. "2024-01-15T00:00:00Z")
- end: only logs before this time (ISO 8601, e.g. "2024-01-16T00:00:00Z")
- limit: max results (default 50)

Tips:
- To investigate an error, start with level="error" to find failures
- Use trace_id to follow a single request across services
- Combine service + level to narrow down (e.g. service="payments" level="error")
- Results are sorted by timestamp descending (newest first)`,
			Params: []agent.ToolParam{
				{Name: "query", Type: "string", Required: false},
				{Name: "service", Type: "string", Required: false},
				{Name: "level", Type: "string", Required: false},
				{Name: "trace_id", Type: "string", Required: false},
				{Name: "environment", Type: "string", Required: false},
				{Name: "start", Type: "string", Required: false},
				{Name: "end", Type: "string", Required: false},
				{Name: "limit", Type: "int", Required: false},
			},
			Handler: c.handleLogSearch,
		},
	}
}

func (c *LogsConnector) Close() error { return nil }

func (c *LogsConnector) handleLogSearch(ctx context.Context, args map[string]any) (string, error) {
	params := store.LogSearchParams{}

	if v, ok := args["query"].(string); ok {
		params.Query = v
	}
	if v, ok := args["service"].(string); ok {
		params.Service = v
	}
	if v, ok := args["level"].(string); ok {
		params.Level = v
	}
	if v, ok := args["trace_id"].(string); ok {
		params.TraceID = v
	}
	if v, ok := args["environment"].(string); ok {
		params.Environment = v
	}
	if v, ok := args["start"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.Start = &t
		}
	}
	if v, ok := args["end"].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			params.End = &t
		}
	}
	if v, ok := args["limit"].(float64); ok {
		params.Limit = int(v)
	}
	if params.Limit <= 0 {
		params.Limit = 50
	}

	results, err := c.logStore.Search(ctx, params)
	if err != nil {
		return "", fmt.Errorf("log search: %w", err)
	}

	if len(results) == 0 {
		return "No logs found matching the given criteria.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d log entries:\n\n", len(results)))
	for _, entry := range results {
		sb.WriteString(fmt.Sprintf("[%s] %s", entry.Timestamp.Format("2006-01-02 15:04:05"), entry.Level))
		if entry.Service != "" {
			sb.WriteString(fmt.Sprintf(" [%s]", entry.Service))
		}
		if entry.TraceID != "" {
			sb.WriteString(fmt.Sprintf(" trace=%s", entry.TraceID))
		}
		sb.WriteString(fmt.Sprintf(": %s\n", entry.Message))
	}

	return sb.String(), nil
}
