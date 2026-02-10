package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/guardrail"
)

// explainQueryHandler returns a handler that runs EXPLAIN ANALYZE on a query
// to show the execution plan, actual vs estimated rows, and timing.
func explainQueryHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		query, _ := args["query"].(string)
		if query == "" {
			return mcp.NewToolResultError("query is required"), nil
		}

		// Validate that it's a SELECT statement.
		if err := guardrail.ValidateReadOnly(query); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("only SELECT queries can be explained: %v", err)), nil
		}

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Connect a PostgreSQL data source first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("The active database connector does not support direct queries."), nil
		}

		analyze := true
		if v, ok := args["analyze"].(bool); ok {
			analyze = v
		}

		buffers := true
		if v, ok := args["buffers"].(bool); ok {
			buffers = v
		}

		outputFormat := "text"
		if v, ok := args["format"].(string); ok && (v == "json" || v == "text") {
			outputFormat = v
		}

		// Build the EXPLAIN command.
		var explainPrefix string
		if analyze {
			opts := []string{"ANALYZE true"}
			if buffers {
				opts = append(opts, "BUFFERS true")
			}
			if outputFormat == "json" {
				opts = append(opts, "FORMAT JSON")
			}
			explainPrefix = fmt.Sprintf("EXPLAIN (%s) ", strings.Join(opts, ", "))
		} else {
			if outputFormat == "json" {
				explainPrefix = "EXPLAIN (FORMAT JSON) "
			} else {
				explainPrefix = "EXPLAIN "
			}
		}

		explainQuery := explainPrefix + query

		result, err := qe.ExecuteReadQuery(ctx, explainQuery)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("EXPLAIN failed: %v", err)), nil
		}

		// Collect the plan output.
		var planLines []string
		for _, row := range result.Rows {
			if len(row) > 0 {
				planLines = append(planLines, fmt.Sprintf("%v", row[0]))
			}
		}
		planText := strings.Join(planLines, "\n")

		// Build warnings by analyzing the plan text.
		warnings := analyzeExplainPlan(planText)

		resp := map[string]any{
			"query": query,
			"plan":  planText,
		}

		if len(warnings) > 0 {
			resp["warnings"] = warnings
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

// analyzeExplainPlan inspects EXPLAIN output text for common performance issues.
func analyzeExplainPlan(plan string) []string {
	var warnings []string
	lower := strings.ToLower(plan)

	if strings.Contains(lower, "seq scan") {
		warnings = append(warnings, "Sequential scan detected — consider adding an index on the filtered columns")
	}
	if strings.Contains(lower, "sort method: external") || strings.Contains(lower, "sort method: disk") {
		warnings = append(warnings, "Sort spilling to disk — consider increasing work_mem or adding an index to avoid the sort")
	}
	if strings.Contains(lower, "nested loop") && strings.Contains(lower, "rows=0") {
		warnings = append(warnings, "Nested loop with zero rows — the planner's estimates may be off, consider running ANALYZE on the tables")
	}
	if strings.Contains(lower, "hash join") && strings.Contains(lower, "batches:") {
		// Multi-batch hash join means it spilled to disk.
		if !strings.Contains(lower, "batches: 1") {
			warnings = append(warnings, "Hash join using multiple batches (disk spill) — consider increasing work_mem")
		}
	}

	return warnings
}
