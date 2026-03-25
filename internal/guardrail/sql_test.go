package guardrail

import (
	"testing"
)

// These tests were originally written for the pg_query AST-based ValidateReadOnly.
// They now validate the generic keyword-based implementation provides equivalent safety.

func TestValidateReadOnly_SimpleSelect(t *testing.T) {
	err := ValidateReadOnlyGeneric("SELECT 1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_SelectWithJoin(t *testing.T) {
	err := ValidateReadOnlyGeneric("SELECT u.name, o.total FROM users u JOIN orders o ON u.id = o.user_id")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_SelectWithCTE(t *testing.T) {
	err := ValidateReadOnlyGeneric("WITH active AS (SELECT * FROM users WHERE active = true) SELECT * FROM active")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_Insert(t *testing.T) {
	err := ValidateReadOnlyGeneric("INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for INSERT, got nil")
	}
}

func TestValidateReadOnly_Update(t *testing.T) {
	err := ValidateReadOnlyGeneric("UPDATE users SET name = 'test'")
	if err == nil {
		t.Fatal("expected error for UPDATE, got nil")
	}
}

func TestValidateReadOnly_Delete(t *testing.T) {
	err := ValidateReadOnlyGeneric("DELETE FROM users WHERE id = 1")
	if err == nil {
		t.Fatal("expected error for DELETE, got nil")
	}
}

func TestValidateReadOnly_Drop(t *testing.T) {
	err := ValidateReadOnlyGeneric("DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for DROP, got nil")
	}
}

func TestValidateReadOnly_CreateTable(t *testing.T) {
	err := ValidateReadOnlyGeneric("CREATE TABLE test (id int)")
	if err == nil {
		t.Fatal("expected error for CREATE TABLE, got nil")
	}
}

func TestValidateReadOnly_MultiStatement(t *testing.T) {
	err := ValidateReadOnlyGeneric("SELECT 1; DROP TABLE x")
	if err == nil {
		t.Fatal("expected error for multi-statement with DROP, got nil")
	}
}

func TestValidateReadOnly_ExplainSelect(t *testing.T) {
	err := ValidateReadOnlyGeneric("EXPLAIN SELECT 1")
	if err != nil {
		t.Fatalf("expected no error for EXPLAIN SELECT, got: %v", err)
	}
}

func TestValidateReadOnly_ExplainAnalyzeSelect(t *testing.T) {
	// EXPLAIN ANALYZE actually executes the query, so it must be rejected.
	err := ValidateReadOnlyGeneric("EXPLAIN (ANALYZE true, BUFFERS true) SELECT COUNT(*) FROM users")
	if err == nil {
		t.Fatal("expected error for EXPLAIN ANALYZE SELECT, got nil")
	}
}

func TestValidateReadOnly_ExplainAnalyzeSimple(t *testing.T) {
	err := ValidateReadOnlyGeneric("EXPLAIN ANALYZE SELECT 1")
	if err == nil {
		t.Fatal("expected error for EXPLAIN ANALYZE SELECT, got nil")
	}
}

func TestValidateReadOnly_ExplainFormatJSON(t *testing.T) {
	// EXPLAIN with non-ANALYZE options should still be allowed.
	err := ValidateReadOnlyGeneric("EXPLAIN (FORMAT JSON) SELECT 1")
	if err != nil {
		t.Fatalf("expected no error for EXPLAIN FORMAT JSON SELECT, got: %v", err)
	}
}

func TestValidateReadOnly_ExplainInsert(t *testing.T) {
	err := ValidateReadOnlyGeneric("EXPLAIN INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for EXPLAIN INSERT, got nil")
	}
}

func TestValidateReadOnly_ExplainDelete(t *testing.T) {
	err := ValidateReadOnlyGeneric("EXPLAIN DELETE FROM users WHERE id = 1")
	if err == nil {
		t.Fatal("expected error for EXPLAIN DELETE, got nil")
	}
}

func TestValidateReadOnly_InvalidSQL(t *testing.T) {
	// The generic validator rejects any statement that doesn't start with
	// a known read-only keyword — this is equivalent to the old AST parse error.
	err := ValidateReadOnlyGeneric("NOT VALID SQL AT ALL !!!!")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
