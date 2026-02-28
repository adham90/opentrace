package mcp

import "testing"

func TestClassifyIntent_ContextKeywords(t *testing.T) {
	tests := []struct {
		context    string
		wantIntent string
	}{
		{"investigating payment timeout errors", IntentInvestigation},
		{"There's a bug in checkout", IntentInvestigation},
		{"errors are spiking", IntentInvestigation},
		{"the service is slow today", IntentInvestigation},
		{"production outage", IntentInvestigation},
		{"weekly activity report", IntentQuery},
		{"show me the stats for this week", IntentQuery},
		{"how many errors last hour", IntentQuery},
		{"system overview for stakeholders", IntentQuery},
		{"set up monitoring for checkout", IntentConfiguration},
		{"create an alert for error rate", IntentConfiguration},
		{"configure the database connector", IntentConfiguration},
		{"let me look around", IntentExploration},
		{"what tools are available", IntentExploration},
	}

	for _, tt := range tests {
		t.Run(tt.context, func(t *testing.T) {
			intent, detail := ClassifyIntent(tt.context, "")
			if intent != tt.wantIntent {
				t.Errorf("ClassifyIntent(%q) = %q, want %q", tt.context, intent, tt.wantIntent)
			}
			if detail != tt.context {
				t.Errorf("detail = %q, want %q", detail, tt.context)
			}
		})
	}
}

func TestClassifyIntent_ToolFallback(t *testing.T) {
	tests := []struct {
		tool       string
		wantIntent string
	}{
		{"diagnose", IntentInvestigation},
		{"triage_alerts", IntentInvestigation},
		{"error_groups", IntentInvestigation},
		{"error_detail", IntentInvestigation},
		{"runbook", IntentInvestigation},
		{"log_search", IntentInvestigation},
		{"explain_query", IntentInvestigation},
		{"watch", IntentConfiguration},
		{"create_healthcheck", IntentConfiguration},
		{"system_overview", IntentQuery},
		{"web_analytics", IntentQuery},
		{"uptime_status", IntentQuery},
		{"db_query_stats", IntentQuery},
		{"some_unknown_tool", IntentExploration},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			intent, _ := ClassifyIntent("", tt.tool)
			if intent != tt.wantIntent {
				t.Errorf("ClassifyIntent(\"\", %q) = %q, want %q", tt.tool, intent, tt.wantIntent)
			}
		})
	}
}

func TestClassifyIntent_ContextOverridesTool(t *testing.T) {
	// Even if tool is "system_overview" (query), context about a bug takes priority.
	intent, _ := ClassifyIntent("investigating a bug in the payment service", "system_overview")
	if intent != IntentInvestigation {
		t.Errorf("context should override tool, got %q want %q", intent, IntentInvestigation)
	}
}
