package connector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/adham90/opentrace/internal/guardrail"
)

// schemaCacheEntry holds a cached schema query result with its fetch time.
type schemaCacheEntry struct {
	content   string
	fetchedAt time.Time
}

// DatabaseConnector implements DataSource for querying a target PostgreSQL database.
type DatabaseConnector struct {
	pool        *pgxpool.Pool
	maxRows     int
	cacheMu     sync.RWMutex
	schemaCache map[string]schemaCacheEntry // key: "" for table list, "tablename" for columns
	cacheTTL    time.Duration
	cb          *CircuitBreaker
}

// NewDatabaseConnector creates a new DatabaseConnector with a connection to the target DB.
func NewDatabaseConnector(ctx context.Context, connStr string, maxRows, stmtTimeoutMS int) (*DatabaseConnector, error) {
	cfg, err := pgxpool.ParseConfig(connStr)
	if err != nil {
		return nil, fmt.Errorf("parsing connection string: %s", FriendlyError(err))
	}

	cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = fmt.Sprintf("%d", stmtTimeoutMS)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to target DB: %s", FriendlyError(err))
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging target DB: %s", FriendlyError(err))
	}

	if maxRows <= 0 {
		maxRows = 500
	}

	return &DatabaseConnector{
		pool:        pool,
		maxRows:     maxRows,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
		cb:          NewCircuitBreaker(connStr, DefaultCircuitBreakerConfig()),
	}, nil
}

func (c *DatabaseConnector) Type() ConnectorType { return ConnectorDatabase }

func (c *DatabaseConnector) TestConnection(ctx context.Context) error {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return fmt.Errorf("connector: %w", err)
		}
	}
	err := c.pool.Ping(ctx)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return fmt.Errorf("%s", FriendlyError(err))
	}
	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return nil
}

func (c *DatabaseConnector) Tools() []Tool {
	return []Tool{
		{
			Name: "db_search",
			Description: `Execute a read-only SQL SELECT query against the PostgreSQL database.
Only SELECT statements are allowed. A LIMIT is auto-applied if you don't include one.

Tips:
- Use db_schema first if you don't know the table structure
- Use JOINs when you see foreign key relationships in the schema
- For counting: SELECT COUNT(*) FROM table WHERE ...
- For aggregation: SELECT col, COUNT(*) FROM table GROUP BY col
- For date filtering: WHERE created_at >= '2024-01-01'
- If a query errors, check column names against db_schema output`,
			Params: []ToolParam{
				{Name: "query", Type: "string", Required: true},
			},
			Handler: c.handleDbSearch,
		},
		{
			Name: "db_schema",
			Description: `Get database schema information. Call with no args to list all tables.
Call with a table name to see columns, types, foreign keys, and sample values.
Schema results are cached for 5 minutes.

Tips:
- Start here to understand the database structure
- Foreign keys show as -> target_table(column) so you know how to JOIN
- Sample values help you understand what data looks like
- Row count estimates help you gauge table size`,
			Params: []ToolParam{
				{Name: "table", Type: "string", Required: false},
			},
			Handler: c.handleDbSchema,
		},
	}
}

// CircuitBreakerState returns the current circuit breaker state for this connector.
func (c *DatabaseConnector) CircuitBreakerState() CircuitState {
	if c.cb != nil {
		return c.cb.State()
	}
	return CircuitClosed
}

// ResetCircuitBreaker resets the circuit breaker to closed state.
func (c *DatabaseConnector) ResetCircuitBreaker() {
	if c.cb != nil {
		c.cb.Reset()
	}
}

func (c *DatabaseConnector) Close() error {
	c.pool.Close()
	return nil
}

// ExecuteReadQuery runs a read-only SQL query and returns structured results.
func (c *DatabaseConnector) ExecuteReadQuery(ctx context.Context, query string) (*QueryResult, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return nil, fmt.Errorf("connector: %w", err)
		}
	}

	// Validate SQL is read-only
	if err := guardrail.ValidateReadOnly(query); err != nil {
		return nil, fmt.Errorf("query rejected: %w", err)
	}

	// Add LIMIT if the AST doesn't already contain one
	limitedQuery := query
	if !guardrail.HasLimit(query) {
		limitedQuery = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(query, "; "), c.maxRows)
	}

	rows, err := c.pool.Query(ctx, limitedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	colNames := make([]string, len(descs))
	for i, d := range descs {
		colNames[i] = string(d.Name)
	}

	var resultRows [][]any
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return nil, fmt.Errorf("reading row: %w", err)
		}
		resultRows = append(resultRows, values)
	}
	if err := rows.Err(); err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	return &QueryResult{
		Columns:  colNames,
		Rows:     resultRows,
		RowCount: len(resultRows),
	}, nil
}

// Ping checks connectivity and measures latency.
func (c *DatabaseConnector) Ping(ctx context.Context) PingResult {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return PingResult{Reachable: false, Error: err.Error()}
		}
	}

	start := time.Now()
	err := c.pool.Ping(ctx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return PingResult{Reachable: false, LatencyMS: latency, Error: err.Error()}
	}
	if c.cb != nil {
		c.cb.RecordSuccess()
	}
	return PingResult{Reachable: true, LatencyMS: latency}
}

func (c *DatabaseConnector) handleDbSearch(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	// Validate SQL is read-only
	if err := guardrail.ValidateReadOnly(query); err != nil {
		return "", fmt.Errorf("query rejected: %w", err)
	}

	// Add LIMIT if the AST doesn't already contain one
	limitedQuery := query
	if !guardrail.HasLimit(query) {
		limitedQuery = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(query, "; "), c.maxRows)
	}

	rows, err := c.pool.Query(ctx, limitedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
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
			if c.cb != nil {
				c.cb.RecordFailure()
			}
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
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("iterating rows: %w", err)
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	if rowCount == 0 {
		return "Query returned no results.", nil
	}

	sb.WriteString(fmt.Sprintf("\n(%d rows)\n", rowCount))
	return sb.String(), nil
}

func (c *DatabaseConnector) handleDbSchema(ctx context.Context, args map[string]any) (string, error) {
	table, _ := args["table"].(string)
	cacheKey := table // "" for table list

	// Check cache (nil-safe for zero-value structs in tests)
	if c.schemaCache != nil {
		c.cacheMu.RLock()
		if entry, ok := c.schemaCache[cacheKey]; ok && time.Since(entry.fetchedAt) < c.cacheTTL {
			c.cacheMu.RUnlock()
			return entry.content, nil
		}
		c.cacheMu.RUnlock()
	}

	// Cache miss — check circuit breaker before querying the database
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	result, err := c.querySchema(ctx, table)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", err
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	// Store in cache
	if c.schemaCache != nil {
		c.cacheMu.Lock()
		c.schemaCache[cacheKey] = schemaCacheEntry{content: result, fetchedAt: time.Now()}
		c.cacheMu.Unlock()
	}

	return result, nil
}

func (c *DatabaseConnector) querySchema(ctx context.Context, table string) (string, error) {
	if table == "" {
		return c.queryTableList(ctx)
	}
	return c.queryTableColumns(ctx, table)
}

func (c *DatabaseConnector) queryTableList(ctx context.Context) (string, error) {
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

func (c *DatabaseConnector) queryTableColumns(ctx context.Context, table string) (string, error) {
	// 1. Get foreign keys for this table
	fkMap, err := c.queryForeignKeys(ctx, table)
	if err != nil {
		// Non-fatal: proceed without FK info
		fkMap = map[string]string{}
	}

	// 2. Get sample values for low-cardinality columns
	sampleMap, err := c.querySampleValues(ctx, table)
	if err != nil {
		// Non-fatal: proceed without samples
		sampleMap = map[string]string{}
	}

	// 3. Get column comments
	commentMap, err := c.queryColumnComments(ctx, table)
	if err != nil {
		commentMap = map[string]string{}
	}

	// 4. Get columns
	rows, err := c.pool.Query(ctx,
		`SELECT column_name, data_type, is_nullable, column_default
		 FROM information_schema.columns
		 WHERE table_name = $1
		 ORDER BY ordinal_position`, table)
	if err != nil {
		return "", fmt.Errorf("listing columns: %w", err)
	}
	defer rows.Close()

	// 5. Get table comment
	tableComment, _ := c.queryTableComment(ctx, table)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Columns for %s:\n", table))
	if tableComment != "" {
		sb.WriteString(fmt.Sprintf("  -- %s\n", tableComment))
	}

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
		if fk, ok := fkMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  -> %s", fk))
		}
		if sv, ok := sampleMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  [values: %s]", sv))
		}
		if cm, ok := commentMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  -- %s", cm))
		}
		sb.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating columns: %w", err)
	}

	// 6. Row count estimate
	rowEstimate, err := c.queryRowEstimate(ctx, table)
	if err == nil && rowEstimate >= 0 {
		sb.WriteString(fmt.Sprintf("\nRow count (estimate): ~%d\n", rowEstimate))
	}

	return sb.String(), nil
}

// queryForeignKeys returns a map of column_name -> "schema.table(column)" for FK columns.
func (c *DatabaseConnector) queryForeignKeys(ctx context.Context, table string) (map[string]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT
			kcu.column_name,
			ccu.table_schema || '.' || ccu.table_name AS foreign_table,
			ccu.column_name AS foreign_column
		 FROM information_schema.table_constraints tc
		 JOIN information_schema.key_column_usage kcu
			ON tc.constraint_name = kcu.constraint_name
			AND tc.table_schema = kcu.table_schema
		 JOIN information_schema.constraint_column_usage ccu
			ON tc.constraint_name = ccu.constraint_name
			AND tc.table_schema = ccu.table_schema
		 WHERE tc.constraint_type = 'FOREIGN KEY'
			AND tc.table_name = $1`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fkMap := make(map[string]string)
	for rows.Next() {
		var col, fTable, fCol string
		if err := rows.Scan(&col, &fTable, &fCol); err != nil {
			return nil, err
		}
		fkMap[col] = fmt.Sprintf("%s(%s)", fTable, fCol)
	}
	return fkMap, rows.Err()
}

// queryRowEstimate returns the estimated row count from pg_class.
func (c *DatabaseConnector) queryRowEstimate(ctx context.Context, table string) (int64, error) {
	var estimate int64
	err := c.pool.QueryRow(ctx,
		`SELECT reltuples::bigint FROM pg_class WHERE relname = $1`, table).Scan(&estimate)
	return estimate, err
}

// querySampleValues returns sample values for low-cardinality columns (< 50 distinct values).
func (c *DatabaseConnector) querySampleValues(ctx context.Context, table string) (map[string]string, error) {
	// Find columns with low cardinality from pg_stats
	rows, err := c.pool.Query(ctx,
		`SELECT attname, n_distinct
		 FROM pg_stats
		 WHERE tablename = $1
			AND n_distinct > 0 AND n_distinct < 50`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lowCardCols []string
	for rows.Next() {
		var col string
		var ndistinct float64
		if err := rows.Scan(&col, &ndistinct); err != nil {
			return nil, err
		}
		lowCardCols = append(lowCardCols, col)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sampleMap := make(map[string]string)
	for _, col := range lowCardCols {
		// Use quote_ident to prevent SQL injection on column/table names
		sampleRows, err := c.pool.Query(ctx,
			fmt.Sprintf(`SELECT DISTINCT %s::text FROM %s WHERE %s IS NOT NULL LIMIT 5`,
				quoteIdent(col), quoteIdent(table), quoteIdent(col)))
		if err != nil {
			continue
		}
		var vals []string
		for sampleRows.Next() {
			var v string
			if err := sampleRows.Scan(&v); err != nil {
				break
			}
			vals = append(vals, v)
		}
		sampleRows.Close()
		if len(vals) > 0 {
			sampleMap[col] = strings.Join(vals, ", ")
		}
	}
	return sampleMap, nil
}

// queryTableComment returns the table-level comment (COMMENT ON TABLE), if any.
func (c *DatabaseConnector) queryTableComment(ctx context.Context, table string) (string, error) {
	var comment *string
	err := c.pool.QueryRow(ctx,
		`SELECT obj_description(c.oid)
		 FROM pg_class c
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relname = $1
			AND n.nspname NOT IN ('pg_catalog', 'information_schema')`, table).Scan(&comment)
	if err != nil || comment == nil {
		return "", err
	}
	return *comment, nil
}

// queryColumnComments returns a map of column_name -> comment for columns with COMMENT ON COLUMN.
func (c *DatabaseConnector) queryColumnComments(ctx context.Context, table string) (map[string]string, error) {
	rows, err := c.pool.Query(ctx,
		`SELECT a.attname, d.description
		 FROM pg_description d
		 JOIN pg_attribute a ON d.objsubid = a.attnum AND d.objoid = a.attrelid
		 JOIN pg_class c ON c.oid = a.attrelid
		 JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE c.relname = $1
			AND n.nspname NOT IN ('pg_catalog', 'information_schema')
			AND a.attnum > 0`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	commentMap := make(map[string]string)
	for rows.Next() {
		var col, desc string
		if err := rows.Scan(&col, &desc); err != nil {
			return nil, err
		}
		commentMap[col] = desc
	}
	return commentMap, rows.Err()
}

// quoteIdent quotes a SQL identifier to prevent injection.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
