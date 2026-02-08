package connector

import (
	"context"
	"testing"
	"time"
)

func TestSchemaCache_HitAfterFirstCall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE cache_test (id serial, name text)")

	// First call — cache miss, queries DB
	result1, err := c.handleDbSchema(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Drop the table — second call should still return cached result
	c.pool.Exec(ctx, "DROP TABLE cache_test")

	result2, err := c.handleDbSchema(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if result1 != result2 {
		t.Errorf("cache miss: results differ\nfirst:  %s\nsecond: %s", result1, result2)
	}
}

func TestSchemaCache_TTLExpiry(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	// Override TTL to very short duration for testing
	c.cacheTTL = 10 * time.Millisecond

	c.pool.Exec(ctx, "CREATE TABLE ttl_test (id serial)")

	// First call — populates cache
	result1, err := c.handleDbSchema(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}

	// Wait for cache to expire
	time.Sleep(20 * time.Millisecond)

	// Create another table — after expiry, should see it
	c.pool.Exec(ctx, "CREATE TABLE ttl_test2 (id serial)")

	result2, err := c.handleDbSchema(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}

	if result1 == result2 {
		t.Error("cache did not expire: results are identical after TTL")
	}
}

func TestSchemaCache_IndependentKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	connStr, cleanup := setupTargetDB(t)
	defer cleanup()

	ctx := context.Background()
	c, err := NewDatabaseConnector(ctx, connStr, 500, 5000)
	if err != nil {
		t.Fatalf("failed to create connector: %v", err)
	}
	defer c.Close()

	c.pool.Exec(ctx, "CREATE TABLE key_test_a (id serial, name text)")
	c.pool.Exec(ctx, "CREATE TABLE key_test_b (id serial, val integer)")

	// Cache table list
	_, err = c.handleDbSchema(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("table list error: %v", err)
	}

	// Cache table A columns
	resultA, err := c.handleDbSchema(ctx, map[string]any{"table": "key_test_a"})
	if err != nil {
		t.Fatalf("table A error: %v", err)
	}

	// Cache table B columns
	resultB, err := c.handleDbSchema(ctx, map[string]any{"table": "key_test_b"})
	if err != nil {
		t.Fatalf("table B error: %v", err)
	}

	// Results should be different (different tables)
	if resultA == resultB {
		t.Error("table A and B have identical results — cache keys not independent")
	}

	// Verify cache has 3 entries: "", "key_test_a", "key_test_b"
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	if len(c.schemaCache) != 3 {
		t.Errorf("cache has %d entries, want 3", len(c.schemaCache))
	}
}

func TestSchemaCache_NilMap_Unit(t *testing.T) {
	// Verify that constructing a DatabaseConnector directly (as existing tests do)
	// doesn't panic when schema cache map is nil — it just won't use caching.
	c := &DatabaseConnector{maxRows: 500}
	if c.schemaCache != nil {
		t.Error("expected nil schemaCache for zero-value struct")
	}
	// Type() and Tools() should still work
	if c.Type() != ConnectorDatabase {
		t.Fatalf("Type() = %q, want %q", c.Type(), ConnectorDatabase)
	}
	if len(c.Tools()) != 2 {
		t.Fatalf("len(Tools()) = %d, want 2", len(c.Tools()))
	}
}
