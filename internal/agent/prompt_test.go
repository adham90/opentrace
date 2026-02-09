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
	// Strategy section should include conditional guidance for log_search and db_search
	if !strings.Contains(prompt, "log_search") {
		t.Error("expected prompt to reference 'log_search' in strategy")
	}
	if !strings.Contains(prompt, "db_schema") {
		t.Error("expected prompt to reference 'db_schema' in strategy when db_search is present")
	}
	// Tool definitions should NOT be listed in the prompt text (native API handles that)
	if strings.Contains(prompt, "## Available tools:") {
		t.Error("expected no '## Available tools:' section — native tool calling handles tool definitions")
	}
}

func TestBuildSystemPrompt_ContainsRules(t *testing.T) {
	prompt := BuildSystemPrompt(nil)
	if !strings.Contains(prompt, "## Rules") {
		t.Error("expected prompt to contain '## Rules' section")
	}
	if !strings.Contains(prompt, "final answer") {
		t.Error("expected prompt to contain 'final answer' guidance")
	}
}
