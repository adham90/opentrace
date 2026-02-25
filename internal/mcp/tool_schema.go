package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/adham90/opentrace/internal/connector"
)

// schemaOverviewHandler returns a handler that provides a database schema overview.
func schemaOverviewHandler(registry *connector.Registry) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()

		schema := "public"
		if v, ok := args["schema"].(string); ok && v != "" {
			schema = v
		}

		tableName, _ := args["table"].(string)

		ds := registry.Get(connector.ConnectorDatabase)
		if ds == nil {
			return mcp.NewToolResultError("No database connector is active. Use test_connector to connect one first."), nil
		}

		qe, ok := ds.(connector.QueryExecutor)
		if !ok {
			return mcp.NewToolResultError("active database connector does not support direct queries"), nil
		}

		if tableName != "" {
			return schemaTableDetail(ctx, qe, schema, tableName)
		}

		return schemaAllTables(ctx, qe, schema)
	}
}

// schemaAllTables returns a compact overview of all tables in a schema.
func schemaAllTables(ctx context.Context, qe connector.QueryExecutor, schema string) (*mcp.CallToolResult, error) {
	query := fmt.Sprintf(`SELECT
		t.table_name,
		(SELECT count(*) FROM information_schema.columns c WHERE c.table_schema = t.table_schema AND c.table_name = t.table_name) AS column_count,
		pg_size_pretty(pg_total_relation_size(quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))) AS size,
		obj_description((quote_ident(t.table_schema) || '.' || quote_ident(t.table_name))::regclass) AS comment,
		s.n_live_tup AS estimated_rows
	FROM information_schema.tables t
	LEFT JOIN pg_stat_user_tables s ON s.schemaname = t.table_schema AND s.relname = t.table_name
	WHERE t.table_schema = '%s' AND t.table_type = 'BASE TABLE'
	ORDER BY t.table_name`, schema)

	result, err := qe.ExecuteReadQuery(ctx, query)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("schema query failed: %v", err)), nil
	}

	if result.RowCount == 0 {
		return mcp.NewToolResultText(fmt.Sprintf("No tables found in schema %q.", schema)), nil
	}

	tables := make([]map[string]any, 0, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) < 5 {
			continue
		}
		t := map[string]any{
			"name":           row[0],
			"column_count":   row[1],
			"size":           row[2],
			"estimated_rows": row[4],
		}
		if row[3] != nil {
			t["comment"] = row[3]
		}
		tables = append(tables, t)
	}

	resp := map[string]any{
		"schema":       schema,
		"table_count":  len(tables),
		"tables":       tables,
	}

	// Fetch foreign key dependencies to show table relationships.
	depQuery := fmt.Sprintf(`SELECT
		tc.table_name AS from_table,
		ccu.table_name AS to_table,
		kcu.column_name AS from_column,
		ccu.column_name AS to_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.constraint_type = 'FOREIGN KEY'
	ORDER BY tc.table_name`, schema)

	depResult, err := qe.ExecuteReadQuery(ctx, depQuery)
	if err == nil && depResult.RowCount > 0 {
		deps := make([]map[string]any, 0, len(depResult.Rows))
		for _, row := range depResult.Rows {
			if len(row) < 4 {
				continue
			}
			deps = append(deps, map[string]any{
				"from_table":  row[0],
				"to_table":    row[1],
				"from_column": row[2],
				"to_column":   row[3],
			})
		}
		resp["dependencies"] = deps
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal schema: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}

// schemaTableDetail returns detailed information for a specific table.
func schemaTableDetail(ctx context.Context, qe connector.QueryExecutor, schema, tableName string) (*mcp.CallToolResult, error) {
	// Columns query.
	colQuery := fmt.Sprintf(`SELECT
		column_name,
		data_type,
		is_nullable,
		column_default,
		character_maximum_length
	FROM information_schema.columns
	WHERE table_schema = '%s' AND table_name = '%s'
	ORDER BY ordinal_position`, schema, tableName)

	colResult, err := qe.ExecuteReadQuery(ctx, colQuery)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("columns query failed: %v", err)), nil
	}

	if colResult.RowCount == 0 {
		return mcp.NewToolResultError(fmt.Sprintf("table %q not found in schema %q", tableName, schema)), nil
	}

	columns := make([]map[string]any, 0, len(colResult.Rows))
	for _, row := range colResult.Rows {
		if len(row) < 5 {
			continue
		}
		col := map[string]any{
			"name":     row[0],
			"type":     row[1],
			"nullable": row[2],
		}
		if row[3] != nil {
			col["default"] = row[3]
		}
		if row[4] != nil {
			col["max_length"] = row[4]
		}
		columns = append(columns, col)
	}

	// Indexes query.
	idxQuery := fmt.Sprintf(`SELECT
		indexname,
		indexdef
	FROM pg_indexes
	WHERE schemaname = '%s' AND tablename = '%s'
	ORDER BY indexname`, schema, tableName)

	indexes := make([]map[string]any, 0)
	idxResult, err := qe.ExecuteReadQuery(ctx, idxQuery)
	if err == nil {
		for _, row := range idxResult.Rows {
			if len(row) < 2 {
				continue
			}
			indexes = append(indexes, map[string]any{
				"name":       row[0],
				"definition": row[1],
			})
		}
	}

	// Foreign keys query.
	fkQuery := fmt.Sprintf(`SELECT
		tc.constraint_name,
		kcu.column_name,
		ccu.table_name AS foreign_table,
		ccu.column_name AS foreign_column
	FROM information_schema.table_constraints tc
	JOIN information_schema.key_column_usage kcu
		ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
	JOIN information_schema.constraint_column_usage ccu
		ON tc.constraint_name = ccu.constraint_name AND tc.table_schema = ccu.table_schema
	WHERE tc.table_schema = '%s' AND tc.table_name = '%s' AND tc.constraint_type = 'FOREIGN KEY'`, schema, tableName)

	foreignKeys := make([]map[string]any, 0)
	fkResult, err := qe.ExecuteReadQuery(ctx, fkQuery)
	if err == nil {
		for _, row := range fkResult.Rows {
			if len(row) < 4 {
				continue
			}
			foreignKeys = append(foreignKeys, map[string]any{
				"constraint":     row[0],
				"column":         row[1],
				"foreign_table":  row[2],
				"foreign_column": row[3],
			})
		}
	}

	resp := map[string]any{
		"schema":       schema,
		"table":        tableName,
		"columns":      columns,
		"column_count": len(columns),
		"indexes":      indexes,
		"foreign_keys": foreignKeys,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to marshal table detail: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
