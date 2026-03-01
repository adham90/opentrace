package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// classifyTaskType determines the task category from a free-text description.
// Returns one of: "debugging", "deploying", "testing", "reviewing", "editing".
func classifyTaskType(task string) string {
	lower := strings.ToLower(task)

	// Check in order of specificity
	debugKeywords := []string{
		"debug", "investigating", "bug", "error", "failing", "crash",
		"broken", "outage", "incident", "slow", "timeout", "spike",
	}
	for _, kw := range debugKeywords {
		if strings.Contains(lower, kw) {
			return "debugging"
		}
	}

	deployKeywords := []string{
		"deploy", "releasing", "shipping", "rollback", "rollout",
	}
	for _, kw := range deployKeywords {
		if strings.Contains(lower, kw) {
			return "deploying"
		}
	}

	testKeywords := []string{
		"test", "coverage", "spec", "assert",
	}
	for _, kw := range testKeywords {
		if strings.Contains(lower, kw) {
			return "testing"
		}
	}

	reviewKeywords := []string{
		"review", "pr ", "pull request", "code review",
	}
	for _, kw := range reviewKeywords {
		if strings.Contains(lower, kw) {
			return "reviewing"
		}
	}

	return "editing"
}

// contextMetaToolHandler returns a handler for the `context` meta-tool.
// It builds a task-specific context bundle based on the task description.
func contextMetaToolHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		task, _ := args["task"].(string)
		if task == "" {
			return mcp.NewToolResultError("task parameter is required"), nil
		}

		service, _ := args["service"].(string)

		taskType := classifyTaskType(task)
		result := map[string]any{
			"task":      task,
			"task_type": taskType,
		}

		switch taskType {
		case "debugging":
			buildDebuggingContext(ctx, deps, service, result)
		case "deploying":
			buildDeployingContext(ctx, deps, service, result)
		case "testing":
			buildTestingContext(ctx, deps, service, result)
		case "reviewing":
			buildReviewingContext(ctx, deps, service, result)
		default: // editing
			buildEditingContext(ctx, deps, service, result)
		}

		data, err := json.Marshal(result)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal context: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}
}

func buildDebuggingContext(ctx context.Context, deps Deps, service string, result map[string]any) {
	// Error groups (top 5)
	if deps.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := deps.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	// Recent deploys
	if deps.DeployStore != nil {
		if deploys, err := deps.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	// Code risk (top 3)
	if deps.CodeEntityStore != nil {
		if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	// Active investigations
	if deps.InvestigationSessionStore != nil {
		params := store.ListInvestigationSessionParams{
			Status: store.InvestigationStatusOpen,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if sessions, err := deps.InvestigationSessionStore.List(ctx, params); err == nil && len(sessions) > 0 {
			result["active_investigations"] = sessions
		}
	}

	// Agent notes
	if deps.AgentNoteStore != nil && service != "" {
		if note, err := deps.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}
}

func buildDeployingContext(ctx context.Context, deps Deps, service string, result map[string]any) {
	// Code risk
	if deps.CodeEntityStore != nil {
		if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	// Recent deploys with impact
	if deps.DeployStore != nil {
		if deploys, err := deps.DeployStore.GetRecent(ctx, service, 5); err == nil && len(deploys) > 0 {
			result["recent_deploys"] = deploys
		}
	}

	// Uncovered error paths
	if deps.TestCorrelationStore != nil {
		if paths, err := deps.TestCorrelationStore.TopByPriority(ctx, service, 5); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}
}

func buildTestingContext(ctx context.Context, deps Deps, service string, result map[string]any) {
	// Uncovered paths (top 10)
	if deps.TestCorrelationStore != nil {
		if paths, err := deps.TestCorrelationStore.TopByPriority(ctx, service, 10); err == nil && len(paths) > 0 {
			result["uncovered_paths"] = paths
		}
	}

	// Error groups
	if deps.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := deps.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["error_groups"] = groups
		}
	}

	// Code risk
	if deps.CodeEntityStore != nil {
		if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}
}

func buildReviewingContext(ctx context.Context, deps Deps, service string, result map[string]any) {
	// Code risk
	if deps.CodeEntityStore != nil {
		if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 5); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	// Recent errors
	if deps.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  5,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := deps.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}

	// Related deploys
	if deps.DeployStore != nil {
		if deploys, err := deps.DeployStore.GetRecent(ctx, service, 3); err == nil && len(deploys) > 0 {
			result["related_deploys"] = deploys
		}
	}
}

func buildEditingContext(ctx context.Context, deps Deps, service string, result map[string]any) {
	// Code risk
	if deps.CodeEntityStore != nil {
		if entities, err := deps.CodeEntityStore.TopByRisk(ctx, service, 3); err == nil && len(entities) > 0 {
			result["code_risk"] = entities
		}
	}

	// Agent notes
	if deps.AgentNoteStore != nil && service != "" {
		if note, err := deps.AgentNoteStore.Get(ctx, "service", service); err == nil && note != nil {
			result["agent_notes"] = note.Note
		}
	}

	// Recent errors (top 3)
	if deps.ErrorGroupStore != nil {
		params := store.ListErrorGroupParams{
			Status: store.ErrorGroupUnresolved,
			Limit:  3,
		}
		if service != "" {
			params.Service = service
		}
		if groups, err := deps.ErrorGroupStore.List(ctx, params); err == nil && len(groups) > 0 {
			result["recent_errors"] = groups
		}
	}
}

// contextToolDef returns the mcp.Tool definition for the context meta-tool.
func contextToolDef() mcp.Tool {
	return mcp.NewTool("context",
		mcp.WithDescription(
			"Get task-specific context from OpenTrace. Automatically assembles relevant "+
				"production data based on what you're doing. Returns error groups, deploy history, "+
				"code risk, test gaps, and investigation history tailored to your current task.",
		),
		mcp.WithString("task", mcp.Required(),
			mcp.Description("What you're doing, e.g. 'debugging payment errors', 'deploying api service', 'editing orders_controller.rb'")),
		mcp.WithString("service",
			mcp.Description("Service to focus on (optional, auto-detected from task when possible)")),
	)
}
