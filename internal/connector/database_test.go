package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/guardrail"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestDatabaseConnector_Type(t *testing.T) {
	// Unit test — no DB needed. We test with a nil pool, just checking Type().
	c := &DatabaseConnector{maxRows: 500}
	if c.Type() != ConnectorDatabase {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorDatabase)
	}
}

func TestDatabaseConnector_Tools(t *testing.T) {
	c := &DatabaseConnector{maxRows: 500}
	tools := c.Tools()
	if len(tools) != 2 {
		t.Fatalf("len(Tools()) = %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["db_search"] {
		t.Error("missing db_search tool")
	}
	if !names["db_schema"] {
		t.Error("missing db_schema tool")
	}
}

func TestDbSearch_RejectsInsert(t *testing.T) {
	err := guardrail.ValidateReadOnly("INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for INSERT")
	}
}

func TestDbSearch_RejectsUpdate(t *testing.T) {
	err := guardrail.ValidateReadOnly("UPDATE users SET name = 'test'")
	if err == nil {
		t.Fatal("expected error for UPDATE")
	}
}

func TestDbSearch_RejectsMultiStatement(t *testing.T) {
	err := guardrail.ValidateReadOnly("SELECT 1; DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for multi-statement")
	}
}

// --- Integration tests below ---

func setupTargetDB(t *testing.T) (string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx := context.Background()
	pgContainer, err := postgres.Run(ctx,
		"postgres:16",
		postgres.WithDatabase("target_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		pgContainer.Terminate(ctx)
		t.Fatalf("failed to get connection string: %v", err)
	}

	return connStr, func() { pgContainer.Terminate(ctx) }
}

func TestDbSearch_ExecuteSelect(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	// Create a test table
	c.pool.Exec(ctx, "CREATE TABLE test_users (id serial, name text)")
	c.pool.Exec(ctx, "INSERT INTO test_users (name) VALUES ('alice'), ('bob')")

	tools := c.Tools()
	var dbSearch = tools[0]
	if dbSearch.Name != "db_search" {
		dbSearch = tools[1]
	}

	result, err := dbSearch.Handler(ctx, map[string]any{
		"query": "SELECT * FROM test_users ORDER BY id",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "alice") || !strings.Contains(result, "bob") {
		t.Errorf("result missing data: %s", result)
	}
}

func TestDbSearch_RowLimit(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 2, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE many_rows (id serial)")
	for i := 0; i < 10; i++ {
		c.pool.Exec(ctx, "INSERT INTO many_rows DEFAULT VALUES")
	}

	tools := c.Tools()
	var dbSearch = tools[0]
	if dbSearch.Name != "db_search" {
		dbSearch = tools[1]
	}

	result, err := dbSearch.Handler(ctx, map[string]any{
		"query": "SELECT * FROM many_rows",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "truncated at 2 rows") {
		t.Errorf("expected truncation notice, got: %s", result)
	}
}

func TestDbSchema_ListTables(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE schema_test (id serial, name text)")

	tools := c.Tools()
	var dbSchema = tools[1]
	if dbSchema.Name != "db_schema" {
		dbSchema = tools[0]
	}

	result, err := dbSchema.Handler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "schema_test") {
		t.Errorf("result missing table: %s", result)
	}
}

func TestDbSchema_TableColumns(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE col_test (id serial PRIMARY KEY, name text NOT NULL, email text)")

	tools := c.Tools()
	var dbSchema = tools[1]
	if dbSchema.Name != "db_schema" {
		dbSchema = tools[0]
	}

	result, err := dbSchema.Handler(ctx, map[string]any{"table": "col_test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "name") || !strings.Contains(result, "email") {
		t.Errorf("result missing columns: %s", result)
	}
}

func TestDatabaseConnector_ReadOnlyEnforcement(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE readonly_test (id serial, val text)")

	// Try INSERT directly on the pool (should fail due to read-only transaction mode)
	_, err = c.pool.Exec(ctx, "INSERT INTO readonly_test (val) VALUES ('should fail')")
	if err == nil {
		t.Fatal("expected error for INSERT on read-only connection")
	}
}

func TestDatabaseConnector_Close(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("unexpected error on close: %v", err)
	}

	// Pool should be closed — ping should fail
	if err := c.pool.Ping(ctx); err == nil {
		t.Fatal("expected error after closing pool")
	}
}
