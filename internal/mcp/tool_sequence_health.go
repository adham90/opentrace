package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// sequenceHealthHandler returns a handler that checks sequence exhaustion risk.
func sequenceHealthHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active."), nil
		}
		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		query := `SELECT
			schemaname || '.' || sequencename AS sequence_name,
			data_type,
			last_value,
			start_value,
			min_value,
			max_value,
			increment_by,
			cycle,
			CASE
				WHEN max_value > 0 AND last_value IS NOT NULL THEN
					ROUND(100.0 * (last_value - min_value) / NULLIF(max_value - min_value, 0), 2)
				ELSE 0
			END AS usage_pct
		FROM pg_sequences
		ORDER BY usage_pct DESC NULLS LAST`

		result, err := qe.ExecuteReadQuery(ctx, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("sequence query failed: %v", err)), nil
		}

		if result.RowCount == 0 {
			return mcp.NewToolResultText("No sequences found."), nil
		}

		sequences := make([]map[string]any, 0, len(result.Rows))
		var warnings []string

		for _, row := range result.Rows {
			if len(row) < 9 {
				continue
			}
			name := fmt.Sprintf("%v", row[0])
			usagePct, _ := toFloat64(row[8])

			seq := map[string]any{
				"name":         name,
				"data_type":    row[1],
				"last_value":   row[2],
				"start_value":  row[3],
				"min_value":    row[4],
				"max_value":    row[5],
				"increment_by": row[6],
				"cycle":        row[7],
				"usage_pct":    usagePct,
			}
			sequences = append(sequences, seq)

			if usagePct > 75 {
				warnings = append(warnings, fmt.Sprintf("CRITICAL: %s is at %.1f%% capacity — will exhaust soon", name, usagePct))
			} else if usagePct > 50 {
				warnings = append(warnings, fmt.Sprintf("WARNING: %s is at %.1f%% capacity — monitor closely", name, usagePct))
			}

			// Check for int4 sequences (max ~2.1B) which exhaust faster.
			if fmt.Sprintf("%v", row[1]) == "integer" && usagePct > 25 {
				warnings = append(warnings, fmt.Sprintf("%s uses integer type (max 2.1B) at %.1f%% — consider migrating to bigint", name, usagePct))
			}
		}

		resp := map[string]any{
			"sequences":      sequences,
			"total":          len(sequences),
			"warnings":       warnings,
		}

		data, err := json.MarshalIndent(resp, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}
