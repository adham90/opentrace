package connector

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/adham90/opentrace/internal/guardrail"
)

// MySQLConnector implements DataSource for querying a target MySQL database.
type MySQLConnector struct {
	db          *sql.DB
	maxRows     int
	stmtTimeout int // milliseconds
	cacheMu     sync.RWMutex
	schemaCache map[string]schemaCacheEntry
	cacheTTL    time.Duration
	cb          *CircuitBreaker
}

// NewMySQLConnector creates a new MySQLConnector with a connection to the target DB.
func NewMySQLConnector(ctx context.Context, connStr string, maxRows, stmtTimeoutMS int) (*MySQLConnector, error) {
	dsn := mysqlNormalizeDSN(connStr)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening MySQL connection: %s", FriendlyError(err))
	}

	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging MySQL: %s", FriendlyError(err))
	}

	// Enforce read-only session
	if _, err := db.ExecContext(ctx, "SET SESSION transaction_read_only = 1"); err != nil {
		// Older MySQL versions may not support this; log but continue
		// The generic SQL validator is the primary safety net
	}

	if maxRows <= 0 {
		maxRows = 500
	}
	if stmtTimeoutMS <= 0 {
		stmtTimeoutMS = 5000
	}

	return &MySQLConnector{
		db:          db,
		maxRows:     maxRows,
		stmtTimeout: stmtTimeoutMS,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
		cb:          NewCircuitBreaker(ConnectorLabel(connStr), DefaultCircuitBreakerConfig()),
	}, nil
}

func (c *MySQLConnector) Type() ConnectorType { return ConnectorMySQL }

func (c *MySQLConnector) TestConnection(ctx context.Context) error {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return fmt.Errorf("connector: %w", err)
		}
	}
	err := c.db.PingContext(ctx)
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

func (c *MySQLConnector) Tools() []Tool {
	return []Tool{
		{
			Name: "mysql_search",
			Description: `Execute a read-only SQL SELECT query against the MySQL database.
Only SELECT statements are allowed. A LIMIT is auto-applied if you don't include one.

Tips:
- Use mysql_schema first if you don't know the table structure
- Use JOINs when you see foreign key relationships in the schema
- For counting: SELECT COUNT(*) FROM table WHERE ...
- For date filtering: WHERE created_at >= '2024-01-01'
- SHOW commands are also allowed (SHOW TABLES, SHOW PROCESSLIST, etc.)
- If a query errors, check column names against mysql_schema output`,
			Params: []ToolParam{
				{Name: "query", Type: "string", Required: true},
			},
			Handler: c.handleMySQLSearch,
		},
		{
			Name: "mysql_schema",
			Description: `Get MySQL database schema information. Call with no args to list all tables.
Call with a table name to see columns, types, foreign keys, and indexes.
Schema results are cached for 5 minutes.

Tips:
- Start here to understand the database structure
- Foreign keys show as -> target_table(column) so you know how to JOIN
- Column comments help you understand what data means
- TABLE_ROWS gives approximate row counts`,
			Params: []ToolParam{
				{Name: "table", Type: "string", Required: false},
			},
			Handler: c.handleMySQLSchema,
		},
	}
}

// CircuitBreakerState returns the current circuit breaker state for this connector.
func (c *MySQLConnector) CircuitBreakerState() CircuitState {
	if c.cb != nil {
		return c.cb.State()
	}
	return CircuitClosed
}

// ResetCircuitBreaker resets the circuit breaker to closed state.
func (c *MySQLConnector) ResetCircuitBreaker() {
	if c.cb != nil {
		c.cb.Reset()
	}
}

func (c *MySQLConnector) Close() error {
	return c.db.Close()
}

// ExecuteReadQuery runs a read-only SQL query and returns structured results.
func (c *MySQLConnector) ExecuteReadQuery(ctx context.Context, query string) (*QueryResult, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return nil, fmt.Errorf("connector: %w", err)
		}
	}

	if err := guardrail.ValidateReadOnlyGeneric(query); err != nil {
		return nil, fmt.Errorf("query rejected: %w", err)
	}

	limitedQuery := query
	if !guardrail.HasLimitGeneric(query) {
		limitedQuery = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(query, "; \t\r\n"), c.maxRows)
	}

	// Add execution time limit hint
	timedQuery := withMaxExecutionTime(limitedQuery, c.stmtTimeout)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.stmtTimeout)*time.Millisecond)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, timedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return nil, fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("reading columns: %w", err)
	}

	var resultRows [][]any
	for rows.Next() {
		values := make([]any, len(colNames))
		scanArgs := make([]any, len(colNames))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return nil, fmt.Errorf("reading row: %w", err)
		}
		// Convert []byte to string for readability
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
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
func (c *MySQLConnector) Ping(ctx context.Context) PingResult {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return PingResult{Reachable: false, Error: err.Error()}
		}
	}

	start := time.Now()
	err := c.db.PingContext(ctx)
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

func (c *MySQLConnector) handleMySQLSearch(ctx context.Context, args map[string]any) (string, error) {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	query, ok := args["query"].(string)
	if !ok || query == "" {
		return "", fmt.Errorf("query parameter is required")
	}

	if err := guardrail.ValidateReadOnlyGeneric(query); err != nil {
		return "", fmt.Errorf("query rejected: %w", err)
	}

	limitedQuery := query
	if !guardrail.HasLimitGeneric(query) {
		limitedQuery = fmt.Sprintf("%s LIMIT %d", strings.TrimRight(query, "; \t\r\n"), c.maxRows)
	}

	timedQuery := withMaxExecutionTime(limitedQuery, c.stmtTimeout)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.stmtTimeout)*time.Millisecond)
	defer cancel()

	rows, err := c.db.QueryContext(ctx, timedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("executing query: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return "", fmt.Errorf("reading columns: %w", err)
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(colNames, " | "))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("-", len(sb.String())-1))
	sb.WriteString("\n")

	rowCount := 0
	for rows.Next() {
		values := make([]any, len(colNames))
		scanArgs := make([]any, len(colNames))
		for i := range values {
			scanArgs[i] = &values[i]
		}
		if err := rows.Scan(scanArgs...); err != nil {
			if c.cb != nil {
				c.cb.RecordFailure()
			}
			return "", fmt.Errorf("reading row: %w", err)
		}

		strs := make([]string, len(values))
		for i, v := range values {
			switch val := v.(type) {
			case []byte:
				strs[i] = string(val)
			default:
				strs[i] = fmt.Sprintf("%v", val)
			}
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

func (c *MySQLConnector) handleMySQLSchema(ctx context.Context, args map[string]any) (string, error) {
	table, _ := args["table"].(string)
	cacheKey := table

	if c.schemaCache != nil {
		c.cacheMu.RLock()
		if entry, ok := c.schemaCache[cacheKey]; ok && time.Since(entry.fetchedAt) < c.cacheTTL {
			c.cacheMu.RUnlock()
			return entry.content, nil
		}
		c.cacheMu.RUnlock()
	}

	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return "", fmt.Errorf("connector: %w", err)
		}
	}

	result, err := c.queryMySQLSchema(ctx, table)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", err
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	if c.schemaCache != nil {
		c.cacheMu.Lock()
		c.schemaCache[cacheKey] = schemaCacheEntry{content: result, fetchedAt: time.Now()}
		c.cacheMu.Unlock()
	}

	return result, nil
}

func (c *MySQLConnector) queryMySQLSchema(ctx context.Context, table string) (string, error) {
	if table == "" {
		return c.queryMySQLTableList(ctx)
	}
	return c.queryMySQLTableColumns(ctx, table)
}

func (c *MySQLConnector) queryMySQLTableList(ctx context.Context) (string, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT TABLE_NAME, TABLE_TYPE, TABLE_ROWS, TABLE_COMMENT
		 FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE()
		 ORDER BY TABLE_NAME`)
	if err != nil {
		return "", fmt.Errorf("listing tables: %w", err)
	}
	defer rows.Close()

	var sb strings.Builder
	sb.WriteString("Tables:\n")
	for rows.Next() {
		var name, ttype string
		var tableRows *int64
		var comment *string
		if err := rows.Scan(&name, &ttype, &tableRows, &comment); err != nil {
			return "", fmt.Errorf("scanning table row: %w", err)
		}
		sb.WriteString(fmt.Sprintf("  %s (%s", name, ttype))
		if tableRows != nil {
			sb.WriteString(fmt.Sprintf(", ~%d rows", *tableRows))
		}
		if comment != nil && *comment != "" {
			sb.WriteString(fmt.Sprintf(", %s", *comment))
		}
		sb.WriteString(")\n")
	}
	return sb.String(), rows.Err()
}

func (c *MySQLConnector) queryMySQLTableColumns(ctx context.Context, table string) (string, error) {
	// 1. Get foreign keys
	fkMap, err := c.queryMySQLForeignKeys(ctx, table)
	if err != nil {
		fkMap = map[string]string{}
	}

	// 2. Get indexes
	indexMap, err := c.queryMySQLIndexes(ctx, table)
	if err != nil {
		indexMap = map[string]string{}
	}

	// 3. Get columns
	rows, err := c.db.QueryContext(ctx,
		`SELECT COLUMN_NAME, COLUMN_TYPE, IS_NULLABLE, COLUMN_DEFAULT, COLUMN_KEY, EXTRA, COLUMN_COMMENT
		 FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?
		 ORDER BY ORDINAL_POSITION`, table)
	if err != nil {
		return "", fmt.Errorf("listing columns: %w", err)
	}
	defer rows.Close()

	// 4. Get table comment
	tableComment := c.queryMySQLTableComment(ctx, table)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Columns for %s:\n", table))
	if tableComment != "" {
		sb.WriteString(fmt.Sprintf("  -- %s\n", tableComment))
	}

	for rows.Next() {
		var name, colType, nullable, key, extra string
		var def, comment *string
		if err := rows.Scan(&name, &colType, &nullable, &def, &key, &extra, &comment); err != nil {
			return "", fmt.Errorf("scanning column row: %w", err)
		}
		sb.WriteString(fmt.Sprintf("  %s %s", name, colType))
		if nullable == "NO" {
			sb.WriteString(" NOT NULL")
		}
		if def != nil {
			sb.WriteString(fmt.Sprintf(" DEFAULT %s", *def))
		}
		if key == "PRI" {
			sb.WriteString(" PRIMARY KEY")
		} else if key == "UNI" {
			sb.WriteString(" UNIQUE")
		}
		if extra != "" {
			sb.WriteString(fmt.Sprintf(" %s", extra))
		}
		if fk, ok := fkMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  -> %s", fk))
		}
		if idx, ok := indexMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  [index: %s]", idx))
		}
		if comment != nil && *comment != "" {
			sb.WriteString(fmt.Sprintf("  -- %s", *comment))
		}
		sb.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating columns: %w", err)
	}

	// 5. Row count estimate
	var tableRows *int64
	err = c.db.QueryRowContext(ctx,
		`SELECT TABLE_ROWS FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&tableRows)
	if err == nil && tableRows != nil {
		sb.WriteString(fmt.Sprintf("\nRow count (estimate): ~%d\n", *tableRows))
	}

	return sb.String(), nil
}

func (c *MySQLConnector) queryMySQLForeignKeys(ctx context.Context, table string) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT COLUMN_NAME, REFERENCED_TABLE_NAME, REFERENCED_COLUMN_NAME
		 FROM information_schema.KEY_COLUMN_USAGE
		 WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = ?
			AND REFERENCED_TABLE_NAME IS NOT NULL`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	fkMap := make(map[string]string)
	for rows.Next() {
		var col, refTable, refCol string
		if err := rows.Scan(&col, &refTable, &refCol); err != nil {
			return nil, err
		}
		fkMap[col] = fmt.Sprintf("%s(%s)", refTable, refCol)
	}
	return fkMap, rows.Err()
}

func (c *MySQLConnector) queryMySQLIndexes(ctx context.Context, table string) (map[string]string, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT COLUMN_NAME, INDEX_NAME
		 FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE()
			AND TABLE_NAME = ?
			AND INDEX_NAME != 'PRIMARY'
		 ORDER BY INDEX_NAME, SEQ_IN_INDEX`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexMap := make(map[string]string)
	for rows.Next() {
		var col, idxName string
		if err := rows.Scan(&col, &idxName); err != nil {
			return nil, err
		}
		if existing, ok := indexMap[col]; ok {
			indexMap[col] = existing + ", " + idxName
		} else {
			indexMap[col] = idxName
		}
	}
	return indexMap, rows.Err()
}

func (c *MySQLConnector) queryMySQLTableComment(ctx context.Context, table string) string {
	var comment *string
	err := c.db.QueryRowContext(ctx,
		`SELECT TABLE_COMMENT FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&comment)
	if err != nil || comment == nil {
		return ""
	}
	return *comment
}

// selectKeyword is the only statement MySQL accepts a MAX_EXECUTION_TIME hint on.
const selectKeyword = "SELECT"

// withMaxExecutionTime attaches MySQL's MAX_EXECUTION_TIME optimizer hint to a
// SELECT. MySQL only honours an optimizer hint when the /*+ ... */ block
// immediately FOLLOWS the statement's first keyword — a hint placed before
// SELECT is parsed as an ordinary comment and the timeout is silently dropped,
// which is what this function fixes. Statements that are not a plain SELECT are
// returned unchanged (they rely on the context deadline instead).
func withMaxExecutionTime(query string, timeoutMS int) string {
	if timeoutMS <= 0 {
		return query
	}
	trimmed := strings.TrimLeft(query, " \t\r\n\f\v")
	if len(trimmed) < len(selectKeyword) ||
		!strings.EqualFold(trimmed[:len(selectKeyword)], selectKeyword) {
		return query
	}
	rest := trimmed[len(selectKeyword):]
	// Guard against an identifier that merely starts with "select".
	if rest != "" && !isMySQLTokenBreak(rest[0]) {
		return query
	}
	return fmt.Sprintf("%s /*+ MAX_EXECUTION_TIME(%d) */%s", trimmed[:len(selectKeyword)], timeoutMS, rest)
}

// isMySQLTokenBreak reports whether b ends an SQL identifier.
func isMySQLTokenBreak(b byte) bool {
	switch {
	case b == '_':
		return false
	case b >= '0' && b <= '9':
		return false
	case b >= 'a' && b <= 'z':
		return false
	case b >= 'A' && b <= 'Z':
		return false
	}
	return true
}

// mysqlNormalizeDSN converts a mysql:// URL to a go-sql-driver DSN if needed,
// and ensures parseTime=true is set.
func mysqlNormalizeDSN(connStr string) string {
	if !strings.HasPrefix(connStr, "mysql://") {
		// Already a DSN — ensure parseTime is set
		if !strings.Contains(connStr, "parseTime") {
			if strings.Contains(connStr, "?") {
				connStr += "&parseTime=true"
			} else {
				connStr += "?parseTime=true"
			}
		}
		return connStr
	}

	// Convert mysql:// URL to DSN
	u, err := url.Parse(connStr)
	if err != nil {
		return connStr
	}

	user := u.User.Username()
	pass, hasPass := u.User.Password()
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "3306"
	}
	db := strings.TrimPrefix(u.Path, "/")

	var dsn string
	if hasPass {
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%s)/%s", user, pass, host, port, db)
	} else if user != "" {
		dsn = fmt.Sprintf("%s@tcp(%s:%s)/%s", user, host, port, db)
	} else {
		dsn = fmt.Sprintf("tcp(%s:%s)/%s", host, port, db)
	}

	// Preserve query params and ensure parseTime
	params := u.Query()
	if _, ok := params["parseTime"]; !ok {
		params.Set("parseTime", "true")
	}
	if len(params) > 0 {
		dsn += "?" + params.Encode()
	}

	return dsn
}
