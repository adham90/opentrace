package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/guardrail"
)

func TestTursoConnector_Type(t *testing.T) {
	c := &TursoConnector{maxRows: 500}
	if c.Type() != ConnectorTurso {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorTurso)
	}
}

func TestTursoConnector_Tools(t *testing.T) {
	c := &TursoConnector{maxRows: 500}
	tools := c.Tools()
	if len(tools) != 2 {
		t.Fatalf("len(Tools()) = %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["turso_search"] {
		t.Error("missing turso_search tool")
	}
	if !names["turso_schema"] {
		t.Error("missing turso_schema tool")
	}
}

func TestTursoConnector_ToolParams(t *testing.T) {
	c := &TursoConnector{maxRows: 500}
	tools := c.Tools()

	for _, tool := range tools {
		switch tool.Name {
		case "turso_search":
			if len(tool.Params) != 1 {
				t.Errorf("turso_search: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Name != "query" || !tool.Params[0].Required {
				t.Errorf("turso_search: unexpected param: %+v", tool.Params[0])
			}
		case "turso_schema":
			if len(tool.Params) != 1 {
				t.Errorf("turso_schema: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Name != "table" || tool.Params[0].Required {
				t.Errorf("turso_schema: unexpected param: %+v", tool.Params[0])
			}
		}
	}
}

func TestTursoSearch_RejectsInsert(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for INSERT")
	}
}

func TestTursoSearch_RejectsDrop(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for DROP")
	}
}

func TestTursoSearch_AllowsPragma(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("PRAGMA table_info(users)")
	if err != nil {
		t.Fatalf("unexpected error for PRAGMA: %v", err)
	}
}

func TestTursoSearch_RequiresQuery(t *testing.T) {
	c := &TursoConnector{
		maxRows: 500,
		cb:      NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	_, err := searchTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}

	_, err = searchTool.Handler(context.Background(), map[string]any{"query": ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestTursoConnector_CircuitBreakerState(t *testing.T) {
	c := &TursoConnector{
		maxRows: 500,
		cb:      NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestTursoConnector_CircuitBreakerState_NilCB(t *testing.T) {
	c := &TursoConnector{maxRows: 500}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestTursoConnector_ResetCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	c := &TursoConnector{maxRows: 500, cb: cb}
	c.ResetCircuitBreaker()

	if c.CircuitBreakerState() != CircuitClosed {
		t.Fatalf("expected closed circuit after reset")
	}
}

func TestTursoConnector_Implements_Interfaces(t *testing.T) {
	c := &TursoConnector{}
	var _ DataSource = c
	var _ QueryExecutor = c
	var _ HealthChecker = c
	var _ CircuitBreakerProvider = c
}

func TestTursoConnector_Close(t *testing.T) {
	c := &TursoConnector{
		httpClient: &http.Client{},
	}
	if err := c.Close(); err != nil {
		t.Fatalf("unexpected error on close: %v", err)
	}
}

// --- URL normalization tests ---

func TestTursoNormalizeURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"libsql://mydb-org.turso.io", "https://mydb-org.turso.io"},
		{"https://mydb-org.turso.io", "https://mydb-org.turso.io"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"mydb-org.turso.io", "https://mydb-org.turso.io"},
		{"libsql://mydb-org.turso.io/", "https://mydb-org.turso.io"},
		{"https://mydb-org.turso.io/", "https://mydb-org.turso.io"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := tursoNormalizeURL(tt.input)
			if got != tt.want {
				t.Errorf("tursoNormalizeURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// --- tursoValue tests ---

func TestTursoValue_ExtractValue(t *testing.T) {
	tests := []struct {
		name  string
		value tursoValue
		want  any
	}{
		{"null", tursoValue{Type: "null"}, nil},
		{"integer", tursoValue{Type: "integer", Value: "42"}, "42"},
		{"float", tursoValue{Type: "float", Value: "3.14"}, "3.14"},
		{"text", tursoValue{Type: "text", Value: "hello"}, "hello"},
		{"blob", tursoValue{Type: "blob", Value: "base64data"}, "[blob]"},
		{"unknown", tursoValue{Type: "unknown", Value: "data"}, "data"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.value.extractValue()
			if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", tt.want) {
				t.Errorf("extractValue() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Schema cache unit tests ---

func TestTursoSchemaCache_CacheHit(t *testing.T) {
	c := &TursoConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
	}

	c.schemaCache[""] = schemaCacheEntry{
		content:   "Tables:\n  users (table)\n",
		fetchedAt: time.Now(),
	}
	c.schemaCache["users"] = schemaCacheEntry{
		content:   "Columns for users:\n  id INTEGER NOT NULL PRIMARY KEY\n",
		fetchedAt: time.Now(),
	}

	result, err := c.handleTursoSchema(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Tables:\n  users (table)\n" {
		t.Errorf("unexpected cache result: %q", result)
	}

	result, err = c.handleTursoSchema(context.Background(), map[string]any{"table": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Columns for users:\n  id INTEGER NOT NULL PRIMARY KEY\n" {
		t.Errorf("unexpected cache result: %q", result)
	}
}

func TestTursoSchemaCache_Expired(t *testing.T) {
	c := &TursoConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    10 * time.Millisecond,
		cb:          NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
		httpClient:  &http.Client{Timeout: 1 * time.Second},
		baseURL:     "http://localhost:1", // invalid, will fail
	}

	c.schemaCache[""] = schemaCacheEntry{
		content:   "old data",
		fetchedAt: time.Now().Add(-1 * time.Second),
	}

	_, err := c.handleTursoSchema(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error from expired cache miss (no Turso)")
	}
}

// --- HTTP API integration tests using httptest ---

// mockTursoServer creates a test HTTP server that simulates the Turso pipeline API.
func mockTursoServer(t *testing.T, handler func(sql string) *tursoQueryResult) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/pipeline" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != "POST" {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var req tursoPipelineRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}

		var results []tursoResultEntry
		for _, r := range req.Requests {
			if r.Type == "close" {
				results = append(results, tursoResultEntry{
					Type:     "ok",
					Response: &tursoResultPayload{Type: "close"},
				})
				continue
			}
			if r.Type == "execute" && r.Stmt != nil {
				qr := handler(r.Stmt.SQL)
				if qr != nil {
					results = append(results, tursoResultEntry{
						Type: "ok",
						Response: &tursoResultPayload{
							Type:   "execute",
							Result: *qr,
						},
					})
				} else {
					results = append(results, tursoResultEntry{
						Type:  "error",
						Error: &tursoAPIError{Message: "mock error", Code: "UNKNOWN"},
					})
				}
			}
		}

		resp := tursoPipelineResponse{Results: results}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func newTestTursoConnector(t *testing.T, srv *httptest.Server) *TursoConnector {
	t.Helper()
	return &TursoConnector{
		baseURL:     srv.URL,
		authToken:   "test-token",
		httpClient:  srv.Client(),
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
		cb:          NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
}

func TestTursoConnector_TestConnection(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "1"}},
			Rows: []tursoRow{{tursoValue{Type: "integer", Value: "1"}}},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	err := c.TestConnection(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTursoConnector_TestConnection_Failure(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return nil // triggers error response
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected error for failed connection test")
	}
}

func TestTursoSearch_ExecuteSelect(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return &tursoQueryResult{
			Cols: []tursoColumn{
				{Name: "id", Decltype: "INTEGER"},
				{Name: "name", Decltype: "TEXT"},
			},
			Rows: []tursoRow{
				{tursoValue{Type: "integer", Value: "1"}, tursoValue{Type: "text", Value: "alice"}},
				{tursoValue{Type: "integer", Value: "2"}, tursoValue{Type: "text", Value: "bob"}},
			},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	result, err := searchTool.Handler(context.Background(), map[string]any{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alice") || !strings.Contains(result, "bob") {
		t.Errorf("result missing data: %s", result)
	}
	if !strings.Contains(result, "2 rows") {
		t.Errorf("result missing row count: %s", result)
	}
}

func TestTursoSearch_NoResults(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "id"}, {Name: "name"}},
			Rows: []tursoRow{},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	result, err := searchTool.Handler(context.Background(), map[string]any{
		"query": "SELECT * FROM users WHERE id = 999",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no results") {
		t.Errorf("expected 'no results' message, got: %s", result)
	}
}

func TestTursoSearch_RejectsWriteQueries(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not have sent query to server")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	writeQueries := []string{
		"INSERT INTO users (name) VALUES ('hacker')",
		"UPDATE users SET name = 'hacker'",
		"DELETE FROM users",
		"DROP TABLE users",
	}

	for _, query := range writeQueries {
		_, err := searchTool.Handler(context.Background(), map[string]any{"query": query})
		if err == nil {
			t.Errorf("expected error for query: %s", query)
		}
	}
}

func TestTursoSearch_AutoAppliesLimit(t *testing.T) {
	var receivedSQL string
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		receivedSQL = sql
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "id"}},
			Rows: []tursoRow{{tursoValue{Type: "integer", Value: "1"}}},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	c.maxRows = 100

	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	_, err := searchTool.Handler(context.Background(), map[string]any{
		"query": "SELECT * FROM users",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedSQL, "LIMIT 100") {
		t.Errorf("expected auto-applied LIMIT, got SQL: %s", receivedSQL)
	}
}

func TestTursoSearch_PreservesExistingLimit(t *testing.T) {
	var receivedSQL string
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		receivedSQL = sql
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "id"}},
			Rows: []tursoRow{{tursoValue{Type: "integer", Value: "1"}}},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	c.maxRows = 500

	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	_, err := searchTool.Handler(context.Background(), map[string]any{
		"query": "SELECT * FROM users LIMIT 10",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(receivedSQL, "LIMIT 500") {
		t.Errorf("should not add extra LIMIT when already present, got SQL: %s", receivedSQL)
	}
}

func TestTursoExecuteReadQuery(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return &tursoQueryResult{
			Cols: []tursoColumn{
				{Name: "id", Decltype: "INTEGER"},
				{Name: "name", Decltype: "TEXT"},
			},
			Rows: []tursoRow{
				{tursoValue{Type: "integer", Value: "1"}, tursoValue{Type: "text", Value: "alice"}},
				{tursoValue{Type: "integer", Value: "2"}, tursoValue{Type: "text", Value: "bob"}},
			},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	result, err := c.ExecuteReadQuery(context.Background(), "SELECT * FROM users")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RowCount != 2 {
		t.Errorf("RowCount = %d, want 2", result.RowCount)
	}
	if len(result.Columns) != 2 {
		t.Errorf("len(Columns) = %d, want 2", len(result.Columns))
	}
	if result.Columns[0] != "id" || result.Columns[1] != "name" {
		t.Errorf("Columns = %v, want [id, name]", result.Columns)
	}
}

func TestTursoExecuteReadQuery_RejectsWrite(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not have reached server")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.ExecuteReadQuery(context.Background(), "DELETE FROM users")
	if err == nil {
		t.Fatal("expected error for DELETE")
	}
}

func TestTursoPing(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "1"}},
			Rows: []tursoRow{{tursoValue{Type: "integer", Value: "1"}}},
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	pr := c.Ping(context.Background())
	if !pr.Reachable {
		t.Fatalf("expected reachable, got error: %s", pr.Error)
	}
	if pr.LatencyMS < 0 {
		t.Errorf("LatencyMS = %d, want >= 0", pr.LatencyMS)
	}
}

func TestTursoPing_Failure(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return nil // error
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	pr := c.Ping(context.Background())
	if pr.Reachable {
		t.Fatal("expected not reachable")
	}
	if pr.Error == "" {
		t.Error("expected error message")
	}
}

func TestTursoSchema_ListTables(t *testing.T) {
	callCount := 0
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		callCount++
		if strings.Contains(sql, "sqlite_master") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "name"}, {Name: "type"}},
				Rows: []tursoRow{
					{tursoValue{Type: "text", Value: "users"}, tursoValue{Type: "text", Value: "table"}},
					{tursoValue{Type: "text", Value: "orders"}, tursoValue{Type: "text", Value: "table"}},
				},
			}
		}
		// COUNT(*) queries for row counts
		if strings.Contains(sql, "COUNT(*)") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "COUNT(*)"}},
				Rows: []tursoRow{{tursoValue{Type: "integer", Value: "42"}}},
			}
		}
		return &tursoQueryResult{Cols: []tursoColumn{}, Rows: []tursoRow{}}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_schema" {
			schemaTool = tool
			break
		}
	}

	result, err := schemaTool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "users") || !strings.Contains(result, "orders") {
		t.Errorf("result missing tables: %s", result)
	}
	if !strings.Contains(result, "42 rows") {
		t.Errorf("result missing row count: %s", result)
	}
}

func TestTursoSchema_TableColumns(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		if strings.Contains(sql, "table_info") {
			// PRAGMA table_info columns: cid, name, type, notnull, dflt_value, pk
			return &tursoQueryResult{
				Cols: []tursoColumn{
					{Name: "cid"}, {Name: "name"}, {Name: "type"},
					{Name: "notnull"}, {Name: "dflt_value"}, {Name: "pk"},
				},
				Rows: []tursoRow{
					{
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "text", Value: "id"},
						tursoValue{Type: "text", Value: "INTEGER"},
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "null"},
						tursoValue{Type: "integer", Value: "1"},
					},
					{
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "text", Value: "name"},
						tursoValue{Type: "text", Value: "TEXT"},
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "null"},
						tursoValue{Type: "integer", Value: "0"},
					},
					{
						tursoValue{Type: "integer", Value: "2"},
						tursoValue{Type: "text", Value: "email"},
						tursoValue{Type: "text", Value: "TEXT"},
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "null"},
						tursoValue{Type: "integer", Value: "0"},
					},
				},
			}
		}
		if strings.Contains(sql, "foreign_key_list") {
			return &tursoQueryResult{
				Cols: []tursoColumn{
					{Name: "id"}, {Name: "seq"}, {Name: "table"},
					{Name: "from"}, {Name: "to"},
				},
				Rows: []tursoRow{},
			}
		}
		if strings.Contains(sql, "index_list") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "seq"}, {Name: "name"}, {Name: "unique"}},
				Rows: []tursoRow{},
			}
		}
		if strings.Contains(sql, "COUNT(*)") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "COUNT(*)"}},
				Rows: []tursoRow{{tursoValue{Type: "integer", Value: "100"}}},
			}
		}
		return &tursoQueryResult{Cols: []tursoColumn{}, Rows: []tursoRow{}}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_schema" {
			schemaTool = tool
			break
		}
	}

	result, err := schemaTool.Handler(context.Background(), map[string]any{"table": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "id INTEGER") {
		t.Errorf("result missing id column: %s", result)
	}
	if !strings.Contains(result, "name TEXT") {
		t.Errorf("result missing name column: %s", result)
	}
	if !strings.Contains(result, "NOT NULL") {
		t.Errorf("result missing NOT NULL: %s", result)
	}
	if !strings.Contains(result, "PRIMARY KEY") {
		t.Errorf("result missing PRIMARY KEY: %s", result)
	}
	if !strings.Contains(result, "Row count: 100") {
		t.Errorf("result missing row count: %s", result)
	}
}

func TestTursoSchema_ForeignKeys(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		if strings.Contains(sql, "table_info") {
			return &tursoQueryResult{
				Cols: []tursoColumn{
					{Name: "cid"}, {Name: "name"}, {Name: "type"},
					{Name: "notnull"}, {Name: "dflt_value"}, {Name: "pk"},
				},
				Rows: []tursoRow{
					{
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "text", Value: "id"},
						tursoValue{Type: "text", Value: "INTEGER"},
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "null"},
						tursoValue{Type: "integer", Value: "1"},
					},
					{
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "text", Value: "user_id"},
						tursoValue{Type: "text", Value: "INTEGER"},
						tursoValue{Type: "integer", Value: "1"},
						tursoValue{Type: "null"},
						tursoValue{Type: "integer", Value: "0"},
					},
				},
			}
		}
		if strings.Contains(sql, "foreign_key_list") {
			return &tursoQueryResult{
				Cols: []tursoColumn{
					{Name: "id"}, {Name: "seq"}, {Name: "table"},
					{Name: "from"}, {Name: "to"},
				},
				Rows: []tursoRow{
					{
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "text", Value: "users"},
						tursoValue{Type: "text", Value: "user_id"},
						tursoValue{Type: "text", Value: "id"},
					},
				},
			}
		}
		if strings.Contains(sql, "index_list") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "seq"}, {Name: "name"}, {Name: "unique"}},
				Rows: []tursoRow{},
			}
		}
		if strings.Contains(sql, "COUNT(*)") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "COUNT(*)"}},
				Rows: []tursoRow{{tursoValue{Type: "integer", Value: "50"}}},
			}
		}
		return &tursoQueryResult{Cols: []tursoColumn{}, Rows: []tursoRow{}}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_schema" {
			schemaTool = tool
			break
		}
	}

	result, err := schemaTool.Handler(context.Background(), map[string]any{"table": "orders"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "-> users(id)") {
		t.Errorf("result missing FK reference: %s", result)
	}
}

func TestTursoSchema_TableNotFound(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		if strings.Contains(sql, "table_info") {
			return &tursoQueryResult{
				Cols: []tursoColumn{
					{Name: "cid"}, {Name: "name"}, {Name: "type"},
					{Name: "notnull"}, {Name: "dflt_value"}, {Name: "pk"},
				},
				Rows: []tursoRow{}, // empty = table not found
			}
		}
		return &tursoQueryResult{Cols: []tursoColumn{}, Rows: []tursoRow{}}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_schema" {
			schemaTool = tool
			break
		}
	}

	_, err := schemaTool.Handler(context.Background(), map[string]any{"table": "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestTursoConnector_AuthHeader(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		resp := tursoPipelineResponse{
			Results: []tursoResultEntry{
				{
					Type: "ok",
					Response: &tursoResultPayload{
						Type: "execute",
						Result: tursoQueryResult{
							Cols: []tursoColumn{{Name: "1"}},
							Rows: []tursoRow{{tursoValue{Type: "integer", Value: "1"}}},
						},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	c.authToken = "my-secret-token"
	c.executeSQL(context.Background(), "SELECT 1")

	expected := "Bearer my-secret-token"
	if receivedAuth != expected {
		t.Errorf("Authorization header = %q, want %q", receivedAuth, expected)
	}
}

func TestTursoConnector_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.executeSQL(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected HTTP status in error, got: %v", err)
	}
}

func TestTursoSearch_CircuitBreakerBlocks(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not reach server when circuit is open")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	// Force circuit open
	for i := 0; i < 10; i++ {
		c.cb.RecordFailure()
	}

	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	_, err := searchTool.Handler(context.Background(), map[string]any{"query": "SELECT 1"})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}
