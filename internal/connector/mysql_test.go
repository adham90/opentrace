package connector

import (
	"context"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/guardrail"
)

func TestMySQLConnector_Type(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	if c.Type() != ConnectorMySQL {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorMySQL)
	}
}

func TestMySQLConnector_Tools(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	tools := c.Tools()
	if len(tools) != 2 {
		t.Fatalf("len(Tools()) = %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["mysql_search"] {
		t.Error("missing mysql_search tool")
	}
	if !names["mysql_schema"] {
		t.Error("missing mysql_schema tool")
	}
}

func TestMySQLConnector_ToolParams(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	tools := c.Tools()

	for _, tool := range tools {
		switch tool.Name {
		case "mysql_search":
			if len(tool.Params) != 1 {
				t.Errorf("mysql_search: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Name != "query" || !tool.Params[0].Required {
				t.Errorf("mysql_search: unexpected param: %+v", tool.Params[0])
			}
		case "mysql_schema":
			if len(tool.Params) != 1 {
				t.Errorf("mysql_schema: len(Params) = %d, want 1", len(tool.Params))
			}
			if tool.Params[0].Name != "table" || tool.Params[0].Required {
				t.Errorf("mysql_schema: unexpected param: %+v", tool.Params[0])
			}
		}
	}
}

func TestMySQLSearch_RejectsInsert(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for INSERT")
	}
}

func TestMySQLSearch_RejectsUpdate(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("UPDATE users SET name = 'test'")
	if err == nil {
		t.Fatal("expected error for UPDATE")
	}
}

func TestMySQLSearch_RejectsDelete(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("DELETE FROM users")
	if err == nil {
		t.Fatal("expected error for DELETE")
	}
}

func TestMySQLSearch_RejectsDrop(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for DROP")
	}
}

func TestMySQLSearch_AllowsSelect(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("SELECT * FROM users WHERE id = 1")
	if err != nil {
		t.Fatalf("unexpected error for SELECT: %v", err)
	}
}

func TestMySQLSearch_AllowsShow(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("SHOW TABLES")
	if err != nil {
		t.Fatalf("unexpected error for SHOW: %v", err)
	}
}

func TestMySQLSearch_AllowsDescribe(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("DESCRIBE users")
	if err != nil {
		t.Fatalf("unexpected error for DESCRIBE: %v", err)
	}
}

func TestMySQLSearch_RejectsMultiStatement(t *testing.T) {
	err := guardrail.ValidateReadOnlyGeneric("SELECT 1; DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for multi-statement")
	}
}

func TestMySQLSearch_RequiresQuery(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	tools := c.Tools()
	var searchTool Tool
	for _, tool := range tools {
		if tool.Name == "mysql_search" {
			searchTool = tool
			break
		}
	}

	// Empty query
	_, err := searchTool.Handler(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}

	// Nil query
	_, err = searchTool.Handler(context.Background(), map[string]any{"query": ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestMySQLSchema_AcceptsOptionalTableArg(t *testing.T) {
	// Verify the schema tool's "table" param is optional by checking cache hits
	// (avoids needing a real DB connection)
	c := &MySQLConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
	}

	// Pre-populate cache for table list (empty key)
	c.schemaCache[""] = schemaCacheEntry{
		content:   "Tables:\n  users (BASE TABLE)\n",
		fetchedAt: time.Now(),
	}

	tools := c.Tools()
	var schemaTool Tool
	for _, tool := range tools {
		if tool.Name == "mysql_schema" {
			schemaTool = tool
			break
		}
	}

	// Call without table arg — should return cached table list
	result, err := schemaTool.Handler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Tables:\n  users (BASE TABLE)\n" {
		t.Errorf("unexpected result: %q", result)
	}
}

func TestMySQLConnector_CircuitBreakerState(t *testing.T) {
	c := &MySQLConnector{
		maxRows: 500,
		cb:      NewCircuitBreaker("test", DefaultCircuitBreakerConfig()),
	}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestMySQLConnector_CircuitBreakerState_NilCB(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	if state := c.CircuitBreakerState(); state != CircuitClosed {
		t.Fatalf("CircuitBreakerState() = %q, want %q", state, CircuitClosed)
	}
}

func TestMySQLConnector_ResetCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker("test", DefaultCircuitBreakerConfig())
	// Trigger open state
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("expected open circuit after failures")
	}

	c := &MySQLConnector{maxRows: 500, cb: cb}
	c.ResetCircuitBreaker()

	if c.CircuitBreakerState() != CircuitClosed {
		t.Fatalf("expected closed circuit after reset")
	}
}

func TestMySQLConnector_ResetCircuitBreaker_NilCB(t *testing.T) {
	c := &MySQLConnector{maxRows: 500}
	// Should not panic
	c.ResetCircuitBreaker()
}

func TestMySQLConnector_SchemaCache_NilMap(t *testing.T) {
	// Verify zero-value struct doesn't panic
	c := &MySQLConnector{maxRows: 500}
	if c.schemaCache != nil {
		t.Error("expected nil schemaCache for zero-value struct")
	}
	if c.Type() != ConnectorMySQL {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorMySQL)
	}
	if len(c.Tools()) != 2 {
		t.Fatalf("len(Tools()) = %d, want 2", len(c.Tools()))
	}
}

func TestMySQLConnector_Implements_Interfaces(t *testing.T) {
	c := &MySQLConnector{}
	var _ DataSource = c
	var _ QueryExecutor = c
	var _ HealthChecker = c
	var _ CircuitBreakerProvider = c
}

// --- DSN normalization tests ---

func TestMySQLNormalizeDSN_PassthroughDSN(t *testing.T) {
	dsn := "user:pass@tcp(localhost:3306)/mydb?parseTime=true"
	got := mysqlNormalizeDSN(dsn)
	if got != dsn {
		t.Errorf("expected passthrough, got %q", got)
	}
}

func TestMySQLNormalizeDSN_AddsParseTime(t *testing.T) {
	dsn := "user:pass@tcp(localhost:3306)/mydb"
	got := mysqlNormalizeDSN(dsn)
	expected := "user:pass@tcp(localhost:3306)/mydb?parseTime=true"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestMySQLNormalizeDSN_AddsParseTimeWithExistingParams(t *testing.T) {
	dsn := "user:pass@tcp(localhost:3306)/mydb?charset=utf8mb4"
	got := mysqlNormalizeDSN(dsn)
	expected := "user:pass@tcp(localhost:3306)/mydb?charset=utf8mb4&parseTime=true"
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestMySQLNormalizeDSN_URLFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		contains []string // substrings the output must contain
	}{
		{
			name:     "basic URL",
			input:    "mysql://user:pass@localhost:3306/mydb",
			contains: []string{"user:pass@tcp(localhost:3306)/mydb", "parseTime=true"},
		},
		{
			name:     "URL without port",
			input:    "mysql://user:pass@localhost/mydb",
			contains: []string{"tcp(localhost:3306)", "mydb"},
		},
		{
			name:     "URL without password",
			input:    "mysql://user@localhost:3306/mydb",
			contains: []string{"user@tcp(localhost:3306)/mydb"},
		},
		{
			name:     "URL with query params",
			input:    "mysql://user:pass@localhost:3306/mydb?charset=utf8",
			contains: []string{"user:pass@tcp(localhost:3306)/mydb", "charset=utf8"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mysqlNormalizeDSN(tt.input)
			for _, sub := range tt.contains {
				if !searchStr(got, sub) {
					t.Errorf("mysqlNormalizeDSN(%q) = %q, missing %q", tt.input, got, sub)
				}
			}
		})
	}
}

func TestMySQLNormalizeDSN_PreservesExistingParseTime(t *testing.T) {
	dsn := "mysql://user:pass@localhost:3306/mydb?parseTime=false"
	got := mysqlNormalizeDSN(dsn)
	// Should not add parseTime=true when it's already set
	if searchStr(got, "parseTime=true") {
		t.Errorf("should not override existing parseTime: %q", got)
	}
}

// --- Schema cache unit tests ---

func TestMySQLSchemaCache_CacheHit(t *testing.T) {
	c := &MySQLConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
	}

	// Pre-populate cache
	c.schemaCache[""] = schemaCacheEntry{
		content:   "Tables:\n  users (BASE TABLE)\n",
		fetchedAt: time.Now(),
	}
	c.schemaCache["users"] = schemaCacheEntry{
		content:   "Columns for users:\n  id int NOT NULL\n",
		fetchedAt: time.Now(),
	}

	// Table list from cache
	result, err := c.handleMySQLSchema(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Tables:\n  users (BASE TABLE)\n" {
		t.Errorf("unexpected cache result: %q", result)
	}

	// Table columns from cache
	result, err = c.handleMySQLSchema(context.Background(), map[string]any{"table": "users"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "Columns for users:\n  id int NOT NULL\n" {
		t.Errorf("unexpected cache result: %q", result)
	}
}

func TestMySQLSchemaCache_TTLExpiry(t *testing.T) {
	c := &MySQLConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    10 * time.Millisecond,
	}

	// Pre-populate cache with entry that will expire
	c.schemaCache[""] = schemaCacheEntry{
		content:   "old data",
		fetchedAt: time.Now(),
	}

	// Should return cached data while fresh
	result, err := c.handleMySQLSchema(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error for fresh cache: %v", err)
	}
	if result != "old data" {
		t.Errorf("expected cached data, got %q", result)
	}

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Verify cache is expired by checking the entry directly
	c.cacheMu.RLock()
	entry := c.schemaCache[""]
	expired := time.Since(entry.fetchedAt) >= c.cacheTTL
	c.cacheMu.RUnlock()

	if !expired {
		t.Error("cache entry should have expired")
	}
}

func TestMySQLSchemaCache_IndependentKeys(t *testing.T) {
	c := &MySQLConnector{
		maxRows:     500,
		schemaCache: make(map[string]schemaCacheEntry),
		cacheTTL:    5 * time.Minute,
	}

	c.schemaCache[""] = schemaCacheEntry{content: "table list", fetchedAt: time.Now()}
	c.schemaCache["users"] = schemaCacheEntry{content: "users columns", fetchedAt: time.Now()}
	c.schemaCache["orders"] = schemaCacheEntry{content: "orders columns", fetchedAt: time.Now()}

	if len(c.schemaCache) != 3 {
		t.Fatalf("cache has %d entries, want 3", len(c.schemaCache))
	}

	r1, _ := c.handleMySQLSchema(context.Background(), map[string]any{})
	r2, _ := c.handleMySQLSchema(context.Background(), map[string]any{"table": "users"})
	r3, _ := c.handleMySQLSchema(context.Background(), map[string]any{"table": "orders"})

	if r1 == r2 || r2 == r3 || r1 == r3 {
		t.Error("cache keys should return different results")
	}
}
