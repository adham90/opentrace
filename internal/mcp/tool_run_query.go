package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// runQueryHandler returns a handler that executes a read-only SQL query.
func runQueryHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		query, _ := args["query"].(string)
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Use test_connector to connect one first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("query failed: %v", err)), nil
		}

		// Format as a compact table.
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Columns: %s\n", strings.Join(result.Columns, " | ")))
		b.WriteString(strings.Repeat("-", 40) + "\n")

		for _, row := range result.Rows {
			vals := make([]string, len(row))
			for i, v := range row {
				vals[i] = fmt.Sprintf("%v", v)
			}
			b.WriteString(strings.Join(vals, " | ") + "\n")
		}

		b.WriteString(fmt.Sprintf("\n%d row(s) returned.", result.RowCount))

		return mcp.NewToolResultText(b.String()), nil
	}
}

// runQueryJSONHandler is an alternative that returns results as JSON.
// Not registered directly but can be used for structured output.
func formatQueryResultJSON(result *connector.QueryResult) (string, error) {
	rows := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		m := make(map[string]any, len(result.Columns))
		for i, col := range result.Columns {
			if i < len(row) {
				m[col] = row[i]
			}
		}
		rows = append(rows, m)
	}

	resp := map[string]any{
		"columns":   result.Columns,
		"rows":      rows,
		"row_count": result.RowCount,
	}

	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
