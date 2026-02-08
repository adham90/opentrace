package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/store"
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
			Name:        "log_search",
			Description: "Search ingested logs by keyword, service, level, or trace ID.",
			Params: []agent.ToolParam{
				{Name: "query", Type: "string", Required: false},
				{Name: "service", Type: "string", Required: false},
				{Name: "level", Type: "string", Required: false},
				{Name: "trace_id", Type: "string", Required: false},
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
