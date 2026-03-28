package mcp

import "strings"

// Intent constants for investigation session classification.
const (
	IntentInvestigation = "investigation"
	IntentQuery         = "query"
	IntentConfiguration = "configuration"
	IntentExploration   = "exploration"
	IntentDevelopment   = "development"
	IntentReview        = "review"
	IntentDeployment    = "deployment"
)

// ClassifyIntent determines the session intent from an optional context string
// and the tool name. Uses a 2-layer approach:
//   - Layer 1 (high confidence): context parameter from the MCP client
//   - Layer 2 (fallback): tool name / argument inference
//
// Layer 0 (trigger-based, highest confidence) is handled separately by the
// SessionTracker when a watcher alert or health check triggers the session.
func ClassifyIntent(contextStr string, toolName string) (intent string, detail string) {
	if contextStr != "" {
		return classifyFromContext(contextStr)
	}
	return classifyFromTool(toolName)
}

// classifyFromContext classifies intent by keyword matching against the
// user-provided context string. Multi-word phrases are checked first for
// higher accuracy, then single-word fallbacks.
func classifyFromContext(ctx string) (string, string) {
	lower := strings.ToLower(ctx)

	// Check query phrases first — these override single investigation keywords
	// (e.g. "how many errors" is a query, not an investigation).
	queryPhrases := []string{
		"how many", "show me", "report on", "summary of",
	}
	for _, kw := range queryPhrases {
		if strings.Contains(lower, kw) {
			return IntentQuery, ctx
		}
	}

	// Config phrases that contain words like "alert" or "error" but are clearly config.
	configPhrases := []string{
		"set up", "create a", "create an", "configure a", "configure the",
		"set up monitoring", "monitor for",
	}
	for _, kw := range configPhrases {
		if strings.Contains(lower, kw) {
			return IntentConfiguration, ctx
		}
	}

	// Deployment keywords — signals a deploy/release workflow.
	deployKeywords := []string{
		"deploying", "deploy", "releasing", "shipping", "rollback", "rollout",
	}
	for _, kw := range deployKeywords {
		if strings.Contains(lower, kw) {
			return IntentDeployment, ctx
		}
	}

	// Review keywords — code review / PR review workflow.
	reviewKeywords := []string{
		"reviewing", "code review", "pr review", "pull request",
	}
	for _, kw := range reviewKeywords {
		if strings.Contains(lower, kw) {
			return IntentReview, ctx
		}
	}

	// Development keywords — building/coding workflow.
	devKeywords := []string{
		"implementing", "building", "refactoring", "coding", "developing",
		"editing", "writing code",
	}
	for _, kw := range devKeywords {
		if strings.Contains(lower, kw) {
			return IntentDevelopment, ctx
		}
	}

	// Investigation keywords — strong signals that something is wrong.
	investigationKeywords := []string{
		"bug", "broken", "investigating", "failing", "incident",
		"outage", "slow", "timeout", "crash", "spike", "issue",
		"debug", "wrong", "problem", "regression", "down", "degraded",
		"error", "alert",
	}
	for _, kw := range investigationKeywords {
		if strings.Contains(lower, kw) {
			return IntentInvestigation, ctx
		}
	}

	// Query keywords.
	queryKeywords := []string{
		"report", "summary", "list", "count",
		"activity", "usage", "stats", "overview", "check",
	}
	for _, kw := range queryKeywords {
		if strings.Contains(lower, kw) {
			return IntentQuery, ctx
		}
	}

	// Config keywords.
	configKeywords := []string{
		"configure", "monitor", "watch",
	}
	for _, kw := range configKeywords {
		if strings.Contains(lower, kw) {
			return IntentConfiguration, ctx
		}
	}

	return IntentExploration, ctx
}

// classifyFromTool infers intent from the tool name when no context is provided.
// Tool names use the consolidated names (logs, database, errors, etc.).
func classifyFromTool(toolName string) (string, string) {
	switch toolName {
	// Investigation tools
	case "overview", "errors", "logs":
		return IntentInvestigation, ""
	case "database":
		return IntentInvestigation, ""

	// Deployment tools
	case "deploys":
		return IntentDeployment, ""

	// Development/code tools
	case "code":
		return IntentDevelopment, ""

	// Configuration tools
	case "watches", "healthchecks", "connectors":
		return IntentConfiguration, ""

	// Query/reporting tools
	case "analytics", "servers":
		return IntentQuery, ""

	// Setup
	case "setup":
		return IntentExploration, ""

	// Admin
	case "admin":
		return IntentConfiguration, ""

	default:
		return IntentExploration, ""
	}
}
