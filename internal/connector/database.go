package connector

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/guardrail"
)

// DatabaseConnector implements DataSource for querying a target PostgreSQL database.
type DatabaseConnector struct {
	pool    *pgxpool.Pool
	maxRows int
}

// NewDatabaseConnector creates a new DatabaseConnector with a connection to the target DB.
func NewDatabaseConnector(ctx context.Context, connStr string, maxRows, stmtTimeoutMS int) (*DatabaseConnector, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %w", err)
	}

	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%d", stmtTimeoutMS)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to target DB: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging target DB: %w", err)
	}

	if maxRows <= 0 {
		maxRows = 500
	}

	return &DatabaseConnector{
		pool:    pool,
		maxRows: maxRows,
	}, nil
}

func (c *DatabaseConnector) Type() ConnectorType { return ConnectorDatabase }

func (c *DatabaseConnector) TestConnection(ctx context.Context) error {
	return c.pool.Ping(ctx)
}

func (c *DatabaseConnector) Tools() []agent.Tool {
	return []agent.Tool{
		{
			Name:        "db_search",
			Description: "Execute a read-only SQL query against the target database. Only SELECT statements are allowed.",
			Params: []agent.ToolParam{
				{Name: "query", Type: "string", Required: true},
			},
			Handler: c.handleDbSearch,
		},
		{
			Name:        "db_schema",
			Description: "Get database schema information. Lists tables or columns for a specific table.",
			Params: []agent.ToolParam{
				{Name: "table", Type: "string", Required: false},
			},
			Handler: c.handleDbSchema,
		},
	}
}

func (c *DatabaseConnector) Close() error {
	c.pool.Close()
	return nil
}

func (c *DatabaseConnector) handleDbSearch(ctx context.Context, args map[string]any) (string, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	// Validate SQL is read-only
	if err := guardrail.ValidateReadOnly(query); err != nil {
		return "", fmt.Errorf("query rejected: %w", err)
	}

	// Add LIMIT if not present
	limitedQuery := query
	upperQuery := strings.ToUpper(strings.TrimSpace(query))
	if !strings.Contains(upperQuery, "LIMIT") {
		limitedQuery = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(query, "; "), c.maxRows)
	}

	rows, err := c.pool.Query(ctx, limitedQuery)
	if err != nil {
		return "", fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	colNames := make([]string, len(descs))
	for i, d := range descs {
		colNames[i] = string(d.Name)
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(colNames, " | "))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("-", len(sb.String())-1))
	sb.WriteString("\n")

	rowCount := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return "", fmt.Errorf("reading row: %w", err)
		}

		strs := make([]string, len(values))
		for i, v := range values {
			strs[i] = fmt.Sprintf("%v", v)
		}
		sb.WriteString(strings.Join(strs, " | "))
		sb.WriteString("\n")
		rowCount++

		if rowCount >= c.maxRows {
			sb.WriteString(fmt.Sprintf("\n... (truncated at %d rows)\n", c.maxRows))
			break
		}
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating rows: %w", err)
	}

	if rowCount == 0 {
		return "Query returned no results.", nil
	}

	sb.WriteString(fmt.Sprintf("\n(%d rows)\n", rowCount))
	return sb.String(), nil
}

func (c *DatabaseConnector) handleDbSchema(ctx context.Context, args map[string]any) (string, error) {
	table, _ := args["table"].(string)

	if table == "" {
		// List all tables
		rows, err := c.pool.Query(ctx,
			`SELECT table_schema, table_name, table_type
			 FROM information_schema.tables
			 WHERE table_schema NOT IN ('pg_catalog', 'information_schema')
			 ORDER BY table_schema, table_name`)
		if err != nil {
			return "", fmt.Errorf("listing tables: %w", err)
		}
		defer rows.Close()

		var sb strings.Builder
		sb.WriteString("Tables:\n")
		for rows.Next() {
			var schema, name, ttype string
			if err := rows.Scan(&schema, &name, &ttype); err != nil {
				return "", fmt.Errorf("scanning table row: %w", err)
			}
			sb.WriteString(fmt.Sprintf("  %s.%s (%s)\n", schema, name, ttype))
		}
		return sb.String(), rows.Err()
	}

	// Columns for a specific table
	rows, err := c.pool.Query(ctx,
		`SELECT column_name, data_type, is_nullable, column_default
		 FROM information_schema.columns
		 WHERE table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return "", fmt.Errorf("listing columns: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Columns for %s:\n", table))
	for rows.Next() {
		var name, dtype, nullable string
		var def *string
		if err := rows.Scan(&name, &dtype, &nullable, &def); err != nil {
			return "", fmt.Errorf("scanning column row: %w", err)
		}
		sb.WriteString(fmt.Sprintf("  %s %s", name, dtype))
		if nullable == "NO" {
			sb.WriteString(" NOT NULL")
		}
		if def != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *def))
		}
		sb.WriteString("\n")
	}
	return sb.String(), rows.Err()
}
