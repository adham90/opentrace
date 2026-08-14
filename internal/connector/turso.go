package connector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/guardrail"
	"github.com/adham90/opentrace/internal/httpclient"
)

// tursoRequestTimeout bounds a single Turso HTTP pipeline call.
const tursoRequestTimeout = 30 * time.Second

// TursoConnector implements DataSource for querying a Turso/libSQL database
// via the Turso HTTP pipeline API. No CGO required.
type TursoConnector struct {
	baseURL     string // e.g. https://mydb-myorg.turso.io
	authToken   string
	httpClient  *http.Client
	maxRows     int
	cacheMu     sync.RWMutex
	schemaCache map[string]schemaCacheEntry
	cacheTTL    time.Duration
	cb          *CircuitBreaker
}

// NewTursoConnector creates a new TursoConnector using the Turso HTTP API.
func NewTursoConnector(ctx context.Context, dbURL, authToken string, maxRows int) (*TursoConnector, error) {
	baseURL := tursoNormalizeURL(dbURL)

	if maxRows <= 0 {
		maxRows = 500
	}

	c := &TursoConnector{
		baseURL:     baseURL,
		authToken:   authToken,
		httpClient:  httpclient.New(tursoRequestTimeout),
		maxRows:     maxRows,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
		cb:          NewCircuitBreaker(ConnectorLabel(baseURL), DefaultCircuitBreakerConfig()),
	}

	// Verify connection
	if err := c.TestConnection(ctx); err != nil {
		return nil, fmt.Errorf("connecting to Turso: %s", RedactSecrets(err.Error()))
	}

	return c, nil
}

func (c *TursoConnector) Type() ConnectorType { return ConnectorTurso }

func (c *TursoConnector) TestConnection(ctx context.Context) error {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return fmt.Errorf("connector: %w", err)
		}
	}

	_, err := c.executeSQL(ctx, "SELECT 1")
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

func (c *TursoConnector) Tools() []Tool {
	return []Tool{
		{
			Name: "turso_search",
			Description: `Execute a read-only SQL SELECT query against the Turso/libSQL database.
Only SELECT statements are allowed. A LIMIT is auto-applied if you don't include one.

Tips:
- Use turso_schema first if you don't know the table structure
- Turso uses SQLite syntax (e.g., || for string concat, IFNULL instead of COALESCE)
- For counting: SELECT COUNT(*) FROM table WHERE ...
- For date filtering: WHERE created_at >= '2024-01-01'
- PRAGMA commands are also allowed for read-only operations
- If a query errors, check column names against turso_schema output`,
			Params: []ToolParam{
				{Name: "query", Type: "string", Required: true},
			},
			Handler: c.handleTursoSearch,
		},
		{
			Name: "turso_schema",
			Description: `Get Turso/libSQL database schema information. Call with no args to list all tables.
Call with a table name to see columns, types, and foreign keys.
Schema results are cached for 5 minutes.

Tips:
- Start here to understand the database structure
- Uses sqlite_master and PRAGMA table_info under the hood
- Foreign keys show as -> target_table(column) so you know how to JOIN`,
			Params: []ToolParam{
				{Name: "table", Type: "string", Required: false},
			},
			Handler: c.handleTursoSchema,
		},
	}
}

// CircuitBreakerState returns the current circuit breaker state.
func (c *TursoConnector) CircuitBreakerState() CircuitState {
	if c.cb != nil {
		return c.cb.State()
	}
	return CircuitClosed
}

// ResetCircuitBreaker resets the circuit breaker to closed state.
func (c *TursoConnector) ResetCircuitBreaker() {
	if c.cb != nil {
		c.cb.Reset()
	}
}

func (c *TursoConnector) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}

// ExecuteReadQuery runs a read-only SQL query and returns structured results.
func (c *TursoConnector) ExecuteReadQuery(ctx context.Context, query string) (*QueryResult, error) {
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

	result, err := c.executeSQL(ctx, limitedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return nil, fmt.Errorf("executing query: %w", err)
	}

	if c.cb != nil {
		c.cb.RecordSuccess()
	}

	colNames := make([]string, len(result.Cols))
	for i, col := range result.Cols {
		colNames[i] = col.Name
	}

	var rows [][]any
	for _, row := range result.Rows {
		vals := make([]any, len(row))
		for i, v := range row {
			vals[i] = v.extractValue()
		}
		rows = append(rows, vals)
	}

	return &QueryResult{
		Columns:  colNames,
		Rows:     rows,
		RowCount: len(rows),
	}, nil
}

// Ping checks connectivity and measures latency.
func (c *TursoConnector) Ping(ctx context.Context) PingResult {
	if c.cb != nil {
		if err := c.cb.Allow(); err != nil {
			return PingResult{Reachable: false, Error: err.Error()}
		}
	}

	start := time.Now()
	_, err := c.executeSQL(ctx, "SELECT 1")
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

func (c *TursoConnector) handleTursoSearch(ctx context.Context, args map[string]any) (string, error) {
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

	result, err := c.executeSQL(ctx, limitedQuery)
	if err != nil {
		if c.cb != nil {
			c.cb.RecordFailure()
		}
		return "", fmt.Errorf("executing query: %w", err)
	}

	colNames := make([]string, len(result.Cols))
	for i, col := range result.Cols {
		colNames[i] = col.Name
	}

	var sb strings.Builder
	sb.WriteString(strings.Join(colNames, " | "))
	sb.WriteString("\n")
	sb.WriteString(strings.Repeat("-", len(sb.String())-1))
	sb.WriteString("\n")

	rowCount := 0
	for _, row := range result.Rows {
		strs := make([]string, len(row))
		for i, v := range row {
			strs[i] = fmt.Sprintf("%v", v.extractValue())
		}
		sb.WriteString(strings.Join(strs, " | "))
		sb.WriteString("\n")
		rowCount++

		if rowCount >= c.maxRows {
			sb.WriteString(fmt.Sprintf("\n... (truncated at %d rows)\n", c.maxRows))
			break
		}
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

func (c *TursoConnector) handleTursoSchema(ctx context.Context, args map[string]any) (string, error) {
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

	result, err := c.queryTursoSchema(ctx, table)
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

func (c *TursoConnector) queryTursoSchema(ctx context.Context, table string) (string, error) {
	if table == "" {
		return c.queryTursoTableList(ctx)
	}
	return c.queryTursoTableColumns(ctx, table)
}

func (c *TursoConnector) queryTursoTableList(ctx context.Context) (string, error) {
	result, err := c.executeSQL(ctx,
		`SELECT name, type FROM sqlite_master
		 WHERE type IN ('table', 'view')
		 AND name NOT LIKE 'sqlite_%'
		 AND name NOT LIKE '_litestream_%'
		 ORDER BY name`)
	if err != nil {
		return "", fmt.Errorf("listing tables: %w", err)
	}

	var sb strings.Builder
	sb.WriteString("Tables:\n")
	for _, row := range result.Rows {
		name := fmt.Sprintf("%v", row[0].extractValue())
		ttype := fmt.Sprintf("%v", row[1].extractValue())

		// Get row count
		countResult, err := c.executeSQL(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", quoteIdent(name)))
		rowCount := ""
		if err == nil && len(countResult.Rows) > 0 {
			rowCount = fmt.Sprintf(", %v rows", countResult.Rows[0][0].extractValue())
		}

		sb.WriteString(fmt.Sprintf("  %s (%s%s)\n", name, ttype, rowCount))
	}
	return sb.String(), nil
}

func (c *TursoConnector) queryTursoTableColumns(ctx context.Context, table string) (string, error) {
	// 1. Get columns via PRAGMA table_info
	result, err := c.executeSQL(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoteIdent(table)))
	if err != nil {
		return "", fmt.Errorf("getting table info: %w", err)
	}

	if len(result.Rows) == 0 {
		return "", fmt.Errorf("table %q not found", table)
	}

	// 2. Get foreign keys
	fkMap := make(map[string]string)
	fkResult, err := c.executeSQL(ctx, fmt.Sprintf("PRAGMA foreign_key_list(%s)", quoteIdent(table)))
	if err == nil {
		for _, row := range fkResult.Rows {
			if len(row) >= 5 {
				fromCol := fmt.Sprintf("%v", row[3].extractValue())
				toTable := fmt.Sprintf("%v", row[2].extractValue())
				toCol := fmt.Sprintf("%v", row[4].extractValue())
				fkMap[fromCol] = fmt.Sprintf("%s(%s)", toTable, toCol)
			}
		}
	}

	// 3. Get indexes
	indexMap := make(map[string]string)
	idxResult, err := c.executeSQL(ctx, fmt.Sprintf("PRAGMA index_list(%s)", quoteIdent(table)))
	if err == nil {
		for _, row := range idxResult.Rows {
			if len(row) >= 2 {
				idxName := fmt.Sprintf("%v", row[1].extractValue())
				idxInfoResult, err := c.executeSQL(ctx, fmt.Sprintf("PRAGMA index_info(%s)", quoteIdent(idxName)))
				if err == nil {
					for _, idxRow := range idxInfoResult.Rows {
						if len(idxRow) >= 3 {
							colName := fmt.Sprintf("%v", idxRow[2].extractValue())
							if existing, ok := indexMap[colName]; ok {
								indexMap[colName] = existing + ", " + idxName
							} else {
								indexMap[colName] = idxName
							}
						}
					}
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Columns for %s:\n", table))

	// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
	for _, row := range result.Rows {
		name := fmt.Sprintf("%v", row[1].extractValue())
		dtype := fmt.Sprintf("%v", row[2].extractValue())
		notnull := fmt.Sprintf("%v", row[3].extractValue())
		dfltVal := row[4].extractValue()
		pk := fmt.Sprintf("%v", row[5].extractValue())

		sb.WriteString(fmt.Sprintf("  %s %s", name, dtype))
		if notnull == "1" {
			sb.WriteString(" NOT NULL")
		}
		if dfltVal != nil && fmt.Sprintf("%v", dfltVal) != "<nil>" {
			sb.WriteString(fmt.Sprintf(" DEFAULT %v", dfltVal))
		}
		if pk == "1" {
			sb.WriteString(" PRIMARY KEY")
		}
		if fk, ok := fkMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  -> %s", fk))
		}
		if idx, ok := indexMap[name]; ok {
			sb.WriteString(fmt.Sprintf("  [index: %s]", idx))
		}
		sb.WriteString("\n")
	}

	// 4. Row count
	countResult, err := c.executeSQL(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", quoteIdent(table)))
	if err == nil && len(countResult.Rows) > 0 {
		sb.WriteString(fmt.Sprintf("\nRow count: %v\n", countResult.Rows[0][0].extractValue()))
	}

	return sb.String(), nil
}

// --- Turso HTTP API types and methods ---

type tursoPipelineRequest struct {
	Requests []tursoStmtRequest `json:"requests"`
}

type tursoStmtRequest struct {
	Type string     `json:"type"`
	Stmt *tursoStmt `json:"stmt,omitempty"`
}

type tursoStmt struct {
	SQL string `json:"sql"`
}

type tursoPipelineResponse struct {
	Results []tursoResultEntry `json:"results"`
}

type tursoResultEntry struct {
	Type     string              `json:"type"` // "ok" or "error"
	Response *tursoResultPayload `json:"response,omitempty"`
	Error    *tursoAPIError      `json:"error,omitempty"`
}

type tursoResultPayload struct {
	Type   string           `json:"type"` // "execute"
	Result tursoQueryResult `json:"result"`
}

type tursoQueryResult struct {
	Cols             []tursoColumn `json:"cols"`
	Rows             []tursoRow    `json:"rows"`
	AffectedRowCount int           `json:"affected_row_count"`
}

type tursoColumn struct {
	Name     string `json:"name"`
	Decltype string `json:"decltype"`
}

type tursoRow = []tursoValue

type tursoValue struct {
	Type  string `json:"type"`  // "integer", "float", "text", "blob", "null"
	Value any    `json:"value"` // string representation of the value
}

func (v tursoValue) extractValue() any {
	switch v.Type {
	case "null":
		return nil
	case "integer", "float", "text":
		return v.Value
	case "blob":
		return fmt.Sprintf("[blob]")
	default:
		return v.Value
	}
}

type tursoAPIError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

// executeSQL executes a single SQL statement via the Turso HTTP pipeline API.
func (c *TursoConnector) executeSQL(ctx context.Context, sql string) (*tursoQueryResult, error) {
	reqBody := tursoPipelineRequest{
		Requests: []tursoStmtRequest{
			{Type: "execute", Stmt: &tursoStmt{SQL: sql}},
			{Type: "close"},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v2/pipeline", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Turso API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var pipeResp tursoPipelineResponse
	if err := json.Unmarshal(respBody, &pipeResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if len(pipeResp.Results) == 0 {
		return nil, fmt.Errorf("empty response from Turso")
	}

	first := pipeResp.Results[0]
	if first.Type == "error" && first.Error != nil {
		return nil, fmt.Errorf("SQL error: %s", first.Error.Message)
	}
	if first.Response == nil {
		return nil, fmt.Errorf("no response payload from Turso")
	}

	return &first.Response.Result, nil
}

// tursoNormalizeURL converts libsql:// URLs to https:// for the HTTP API.
func tursoNormalizeURL(dbURL string) string {
	dbURL = strings.TrimRight(dbURL, "/")

	if strings.HasPrefix(dbURL, "libsql://") {
		return "https://" + strings.TrimPrefix(dbURL, "libsql://")
	}
	if strings.HasPrefix(dbURL, "http://") || strings.HasPrefix(dbURL, "https://") {
		return dbURL
	}
	// Assume https if no scheme
	return "https://" + dbURL
}
