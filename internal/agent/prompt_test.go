package agent

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt_NoTools(t *testing.T) {
	prompt := BuildSystemPrompt(nil)
	if !strings.Contains(prompt, "You are OpenTrace") {
		t.Error("expected base prompt to contain 'You are OpenTrace'")
	}
	if strings.Contains(prompt, "Available tools:") {
		t.Error("expected no tool section when tools are nil")
	}
}

func TestBuildSystemPrompt_WithTools(t *testing.T) {
	tools := []Tool{
		{Name: "log_search", Description: "Search logs by keyword"},
		{Name: "db_search", Description: "Query the database"},
	}
	prompt := BuildSystemPrompt(tools)
	if !strings.Contains(prompt, "log_search") {
		t.Error("expected prompt to contain 'log_search'")
	}
	if !strings.Contains(prompt, "db_search") {
		t.Error("expected prompt to contain 'db_search'")
	}
	if !strings.Contains(prompt, "Search logs by keyword") {
		t.Error("expected prompt to contain tool description")
	}
}

func TestBuildSystemPrompt_ContainsResponseFormat(t *testing.T) {
	prompt := BuildSystemPrompt(nil)
	if !strings.Contains(prompt, "final_answer") {
		t.Error("expected prompt to contain 'final_answer' format instruction")
	}
	if !strings.Contains(prompt, "tool_call") {
		t.Error("expected prompt to contain 'tool_call' format instruction")
	}
}
