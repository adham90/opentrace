package guardrail

import (
	"testing"
)

func TestValidateReadOnly_SimpleSelect(t *testing.T) {
	err := ValidateReadOnly("SELECT 1")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_SelectWithJoin(t *testing.T) {
	err := ValidateReadOnly("SELECT u.name, o.total FROM users u JOIN orders o ON u.id = o.user_id")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_SelectWithCTE(t *testing.T) {
	err := ValidateReadOnly("WITH active AS (SELECT * FROM users WHERE active = true) SELECT * FROM active")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestValidateReadOnly_Insert(t *testing.T) {
	err := ValidateReadOnly("INSERT INTO users (name) VALUES ('test')")
	if err == nil {
		t.Fatal("expected error for INSERT, got nil")
	}
}

func TestValidateReadOnly_Update(t *testing.T) {
	err := ValidateReadOnly("UPDATE users SET name = 'test'")
	if err == nil {
		t.Fatal("expected error for UPDATE, got nil")
	}
}

func TestValidateReadOnly_Delete(t *testing.T) {
	err := ValidateReadOnly("DELETE FROM users WHERE id = 1")
	if err == nil {
		t.Fatal("expected error for DELETE, got nil")
	}
}

func TestValidateReadOnly_Drop(t *testing.T) {
	err := ValidateReadOnly("DROP TABLE users")
	if err == nil {
		t.Fatal("expected error for DROP, got nil")
	}
}

func TestValidateReadOnly_CreateTable(t *testing.T) {
	err := ValidateReadOnly("CREATE TABLE test (id int)")
	if err == nil {
		t.Fatal("expected error for CREATE TABLE, got nil")
	}
}

func TestValidateReadOnly_MultiStatement(t *testing.T) {
	err := ValidateReadOnly("SELECT 1; DROP TABLE x")
	if err == nil {
		t.Fatal("expected error for multi-statement with DROP, got nil")
	}
}

func TestValidateReadOnly_InvalidSQL(t *testing.T) {
	err := ValidateReadOnly("NOT VALID SQL AT ALL !!!!")
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}
