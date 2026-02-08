package agent

import (
	"testing"
)

func TestValidateArgs_AllPresent(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
		{Name: "limit", Type: "int", Required: true},
	}
	args := map[string]any{
		"query": "error",
		"limit": float64(10),
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateArgs_MissingRequired(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
		{Name: "limit", Type: "int", Required: true},
	}
	args := map[string]any{
		"query": "error",
	}
	if err := ValidateArgs(params, args); err == nil {
		t.Fatal("expected error for missing required param 'limit'")
	}
}

func TestValidateArgs_OptionalMissing(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
		{Name: "limit", Type: "int", Required: false},
	}
	args := map[string]any{
		"query": "error",
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateArgs_WrongType(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
	}
	args := map[string]any{
		"query": float64(42), // number instead of string
	}
	if err := ValidateArgs(params, args); err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestValidateArgs_ExtraArgs(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
	}
	args := map[string]any{
		"query": "error",
		"extra": "ignored",
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
