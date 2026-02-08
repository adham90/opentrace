package connector

import (
	"context"
	"strings"
	"testing"
)

func TestDbSchema_ForeignKeys(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	// Create tables with a foreign key
	c.pool.Exec(ctx, "CREATE TABLE fk_users (id serial PRIMARY KEY, name text)")
	c.pool.Exec(ctx, "CREATE TABLE fk_orders (id serial PRIMARY KEY, user_id integer REFERENCES fk_users(id), total numeric)")

	result, err := c.handleDbSchema(ctx, map[string]any{"table": "fk_orders"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(result, "fk_users") {
		t.Errorf("expected FK reference to fk_users in output:\n%s", result)
	}
	if !strings.Contains(result, "->") {
		t.Errorf("expected -> arrow for FK in output:\n%s", result)
	}
}

func TestDbSchema_RowEstimate(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE est_test (id serial PRIMARY KEY, val text)")
	// Insert some rows and ANALYZE so pg_class has stats
	for i := 0; i < 100; i++ {
		c.pool.Exec(ctx, "INSERT INTO est_test (val) VALUES ('row')")
	}
	c.pool.Exec(ctx, "ANALYZE est_test")

	result, err := c.handleDbSchema(ctx, map[string]any{"table": "est_test"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(result, "Row count (estimate)") {
		t.Errorf("expected row count estimate in output:\n%s", result)
	}
}

func TestDbSchema_SampleValues(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE sample_test (id serial PRIMARY KEY, status text, description text)")
	// Insert low-cardinality status column and high-cardinality description
	for i := 0; i < 50; i++ {
		c.pool.Exec(ctx, "INSERT INTO sample_test (status, description) VALUES ($1, $2)",
			[]string{"active", "inactive", "pending"}[i%3],
			// Use unique descriptions so n_distinct is high
			"desc_"+string(rune('a'+i%26))+string(rune('0'+i/26)),
		)
	}
	c.pool.Exec(ctx, "ANALYZE sample_test")

	result, err := c.handleDbSchema(ctx, map[string]any{"table": "sample_test"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// status has 3 distinct values — should show sample values
	if !strings.Contains(result, "[values:") {
		t.Errorf("expected sample values for low-cardinality column in output:\n%s", result)
	}
}

func TestDbSchema_TableComment(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE comment_test (id serial PRIMARY KEY, val text)")
	c.pool.Exec(ctx, "COMMENT ON TABLE comment_test IS 'This is a test table'")
	c.pool.Exec(ctx, "COMMENT ON COLUMN comment_test.val IS 'A test value column'")

	result, err := c.handleDbSchema(ctx, map[string]any{"table": "comment_test"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if !strings.Contains(result, "This is a test table") {
		t.Errorf("expected table comment in output:\n%s", result)
	}
	if !strings.Contains(result, "A test value column") {
		t.Errorf("expected column comment in output:\n%s", result)
	}
}

func TestDbSchema_NoFKs_StillWorks(t *testing.T) {
	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE nofk_test (id serial PRIMARY KEY, name text NOT NULL)")

	result, err := c.handleDbSchema(ctx, map[string]any{"table": "nofk_test"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	// Should still show columns without FK info
	if !strings.Contains(result, "name") || !strings.Contains(result, "text") {
		t.Errorf("expected column info in output:\n%s", result)
	}
	// Should NOT contain -> since no FKs
	if strings.Contains(result, "->") {
		t.Errorf("unexpected FK arrow in output for table without FKs:\n%s", result)
	}
}
