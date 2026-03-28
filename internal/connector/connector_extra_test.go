package connector

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// DatabaseConnector unit tests (no real Postgres needed)
// ---------------------------------------------------------------------------

func TestDatabaseConnector_CircuitBreakerState_WithCB(t *testing.T) {
	cb := NewCircuitBreaker("test-db", DefaultCircuitBreakerConfig())
	c := &DatabaseConnector{maxRows: 500, cb: cb}

	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestDatabaseConnector_CircuitBreakerState_NilCB(t *testing.T) {
	c := &DatabaseConnector{maxRows: 500}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q (nil CB)", state, CircuitClosed)
	}
}

func TestDatabaseConnector_CircuitBreakerState_OpenCB(t *testing.T) {
	cb := NewCircuitBreaker("test-db", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	c := &DatabaseConnector{maxRows: 500, cb: cb}

	if state := c.CircuitBreakerState(); state != CircuitOpen {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitOpen)
	}
}

func TestDatabaseConnector_ResetCircuitBreaker_WithCB(t *testing.T) {
	cb := NewCircuitBreaker("test-db", DefaultCircuitBreakerConfig())
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	c := &DatabaseConnector{maxRows: 500, cb: cb}
	c.ResetCircuitBreaker()

	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("expected closed after reset, got %q", state)
	}
}

func TestDatabaseConnector_ResetCircuitBreaker_NilCB(t *testing.T) {
	c := &DatabaseConnector{maxRows: 500}
	// Should not panic
	c.ResetCircuitBreaker()
}

func TestDatabaseConnector_Implements_Interfaces(t *testing.T) {
	c := &DatabaseConnector{}
	var _ DataSource = c
	var _ QueryExecutor = c
	var _ HealthChecker = c
	var _ CircuitBreakerProvider = c
}

// ---------------------------------------------------------------------------
// TursoConnector: executeSQL edge cases via httptest
// ---------------------------------------------------------------------------

func TestTursoExecuteSQL_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return an empty results array
		resp := tursoPipelineResponse{Results: []tursoResultEntry{}}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.executeSQL(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
	if !strings.Contains(err.Error(), "empty response") {
		t.Errorf("expected 'empty response' error, got: %v", err)
	}
}

func TestTursoExecuteSQL_NoResponsePayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return an "ok" result with nil Response
		resp := tursoPipelineResponse{
			Results: []tursoResultEntry{
				{Type: "ok", Response: nil},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.executeSQL(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for nil response payload")
	}
	if !strings.Contains(err.Error(), "no response payload") {
		t.Errorf("expected 'no response payload' error, got: %v", err)
	}
}

func TestTursoExecuteSQL_SQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := tursoPipelineResponse{
			Results: []tursoResultEntry{
				{
					Type:  "error",
					Error: &tursoAPIError{Message: "no such table: foo", Code: "UNKNOWN"},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.executeSQL(context.Background(), "SELECT * FROM foo")
	if err == nil {
		t.Fatal("expected error for SQL error response")
	}
	if !strings.Contains(err.Error(), "no such table") {
		t.Errorf("expected SQL error message, got: %v", err)
	}
}

func TestTursoExecuteSQL_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("not valid json"))
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.executeSQL(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error for invalid JSON response")
	}
	if !strings.Contains(err.Error(), "parsing response") {
		t.Errorf("expected parsing error, got: %v", err)
	}
}

func TestTursoExecuteSQL_NoAuthToken(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		resp := tursoPipelineResponse{
			Results: []tursoResultEntry{
				{
					Type: "ok",
					Response: &tursoResultPayload{
						Type:   "execute",
						Result: tursoQueryResult{Cols: []tursoColumn{{Name: "1"}}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	c.authToken = "" // no auth token
	c.executeSQL(context.Background(), "SELECT 1")

	if receivedAuth != "" {
		t.Errorf("expected no Authorization header, got %q", receivedAuth)
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: handleTursoSearch row truncation path
// ---------------------------------------------------------------------------

func TestTursoSearch_Truncation(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		// Return more rows than maxRows
		rows := make([]tursoRow, 5)
		for i := range rows {
			rows[i] = tursoRow{tursoValue{Type: "integer", Value: "1"}}
		}
		return &tursoQueryResult{
			Cols: []tursoColumn{{Name: "id"}},
			Rows: rows,
		}
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	c.maxRows = 3 // truncate at 3

	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_search" {
			searchTool = tool
			break
		}
	}

	result, err := searchTool.Handler(context.Background(), map[string]any{
		"query": "SELECT * FROM test LIMIT 10",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "truncated at 3 rows") {
		t.Errorf("expected truncation notice, got: %s", result)
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: ExecuteReadQuery circuit breaker blocks
// ---------------------------------------------------------------------------

func TestTursoExecuteReadQuery_CircuitBreakerBlocks(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not reach server when circuit is open")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	for i := 0; i < 10; i++ {
		c.cb.RecordFailure()
	}

	_, err := c.ExecuteReadQuery(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: Ping circuit breaker blocks
// ---------------------------------------------------------------------------

func TestTursoPing_CircuitBreakerBlocks(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not reach server when circuit is open")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	for i := 0; i < 10; i++ {
		c.cb.RecordFailure()
	}

	pr := c.Ping(context.Background())
	if pr.Reachable {
		t.Fatal("expected not reachable when circuit is open")
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: TestConnection circuit breaker blocks
// ---------------------------------------------------------------------------

func TestTursoTestConnection_CircuitBreakerBlocks(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not reach server when circuit is open")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	for i := 0; i < 10; i++ {
		c.cb.RecordFailure()
	}

	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: handleTursoSchema circuit breaker blocks
// ---------------------------------------------------------------------------

func TestTursoSchema_CircuitBreakerBlocks(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		t.Fatal("should not reach server when circuit is open")
		return nil
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	for i := 0; i < 10; i++ {
		c.cb.RecordFailure()
	}

	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "turso_schema" {
			schemaTool = tool
			break
		}
	}

	_, err := schemaTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected circuit breaker error")
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: ExecuteReadQuery with query execution error
// ---------------------------------------------------------------------------

func TestTursoExecuteReadQuery_ExecutionError(t *testing.T) {
	srv := mockTursoServer(t, func(sql string) *tursoQueryResult {
		return nil // triggers error
	})
	defer srv.Close()

	c := newTestTursoConnector(t, srv)
	_, err := c.ExecuteReadQuery(context.Background(), "SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for failed query execution")
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: Turso schema with indexes
// ---------------------------------------------------------------------------

func TestTursoSchema_Indexes(t *testing.T) {
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
				Cols: []tursoColumn{{Name: "id"}, {Name: "seq"}, {Name: "table"}, {Name: "from"}, {Name: "to"}},
				Rows: []tursoRow{},
			}
		}
		if strings.Contains(sql, "index_list") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "seq"}, {Name: "name"}, {Name: "unique"}},
				Rows: []tursoRow{
					{tursoValue{Type: "integer", Value: "0"}, tursoValue{Type: "text", Value: "idx_email"}, tursoValue{Type: "integer", Value: "1"}},
				},
			}
		}
		if strings.Contains(sql, "index_info") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "seqno"}, {Name: "cid"}, {Name: "name"}},
				Rows: []tursoRow{
					{tursoValue{Type: "integer", Value: "0"}, tursoValue{Type: "integer", Value: "1"}, tursoValue{Type: "text", Value: "email"}},
				},
			}
		}
		if strings.Contains(sql, "COUNT(*)") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "COUNT(*)"}},
				Rows: []tursoRow{{tursoValue{Type: "integer", Value: "10"}}},
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
	if !strings.Contains(result, "[index: idx_email]") {
		t.Errorf("result missing index info: %s", result)
	}
}

// ---------------------------------------------------------------------------
// TursoConnector: Turso schema with DEFAULT value
// ---------------------------------------------------------------------------

func TestTursoSchema_DefaultValue(t *testing.T) {
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
						tursoValue{Type: "text", Value: "status"},
						tursoValue{Type: "text", Value: "TEXT"},
						tursoValue{Type: "integer", Value: "0"},
						tursoValue{Type: "text", Value: "'active'"}, // default value
						tursoValue{Type: "integer", Value: "0"},
					},
				},
			}
		}
		if strings.Contains(sql, "foreign_key_list") || strings.Contains(sql, "index_list") {
			return &tursoQueryResult{Cols: []tursoColumn{}, Rows: []tursoRow{}}
		}
		if strings.Contains(sql, "COUNT(*)") {
			return &tursoQueryResult{
				Cols: []tursoColumn{{Name: "COUNT(*)"}},
				Rows: []tursoRow{{tursoValue{Type: "integer", Value: "5"}}},
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

	result, err := schemaTool.Handler(context.Background(), map[string]any{"table": "items"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "DEFAULT") {
		t.Errorf("result missing DEFAULT info: %s", result)
	}
}
