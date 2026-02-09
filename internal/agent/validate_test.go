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
		{Name: "items", Type: "int", Required: true},
	}
	args := map[string]any{
		"items": true, // bool instead of int — not coercible
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

func TestValidateArgs_CoerceIntFromString(t *testing.T) {
	params := []ToolParam{
		{Name: "limit", Type: "int", Required: true},
	}
	args := map[string]any{
		"limit": "10", // LLM sends string instead of number
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should be coerced to float64 (JSON number type)
	if v, ok := args["limit"].(float64); !ok || v != 10 {
		t.Errorf("expected limit=10 (float64), got %v (%T)", args["limit"], args["limit"])
	}
}

func TestValidateArgs_CoerceBoolFromString(t *testing.T) {
	params := []ToolParam{
		{Name: "verbose", Type: "bool", Required: false},
	}
	args := map[string]any{
		"verbose": "true",
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := args["verbose"].(bool); !ok || !v {
		t.Errorf("expected verbose=true (bool), got %v (%T)", args["verbose"], args["verbose"])
	}
}

func TestValidateArgs_CoerceStringFromNumber(t *testing.T) {
	params := []ToolParam{
		{Name: "query", Type: "string", Required: true},
	}
	args := map[string]any{
		"query": float64(42),
	}
	if err := ValidateArgs(params, args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := args["query"].(string); !ok || v != "42" {
		t.Errorf("expected query=\"42\" (string), got %v (%T)", args["query"], args["query"])
	}
}

func TestValidateArgs_InvalidStringForInt(t *testing.T) {
	params := []ToolParam{
		{Name: "limit", Type: "int", Required: true},
	}
	args := map[string]any{
		"limit": "not-a-number",
	}
	if err := ValidateArgs(params, args); err == nil {
		t.Fatal("expected error for non-numeric string as int")
	}
}
