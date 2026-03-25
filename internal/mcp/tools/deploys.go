package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// DeploysDeps holds the stores needed by the deploys tool.
type DeploysDeps struct {
	DeployStore store.DeployStore
}

// DeploysTool returns the consolidated tool definition for deploy management.
func DeploysTool() mcp.Tool {
	return mcp.NewTool("deploys",
		mcp.WithDescription(`Deploy management: view history and impact metrics.

Actions:
- history: List recent deploys with error rate changes
- impact: Get detailed impact metrics for a specific deploy by commit hash
- record: Record a new deploy event`),
		mcp.WithString("action", mcp.Required(), mcp.Description("Action: history, impact, record")),
		mcp.WithString("service", mcp.Description("Service name filter")),
		mcp.WithNumber("limit", mcp.Description("Max results for history (default: 10, max: 50)")),
		// impact params
		mcp.WithString("commit_hash", mcp.Description("Commit hash (required for impact)")),
		// record params
		mcp.WithString("commit", mcp.Description("Commit hash (required for record)")),
		mcp.WithString("author", mcp.Description("Deploy author")),
		mcp.WithString("branch", mcp.Description("Git branch")),
		mcp.WithString("environment", mcp.Description("Deployment environment")),
		mcp.WithObject("files", mcp.Description("Array of changed file paths")),
	)
}

// DeploysHandler returns a handler for the consolidated deploys tool.
func DeploysHandler(d DeploysDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if d.DeployStore == nil {
			return mcp.NewToolResultError("DeployStore not configured"), nil
		}

		args := request.GetArguments()
		action, _ := args["action"].(string)

		switch action {
		case "history":
			return handleDeployHistory(ctx, d, args)
		case "impact":
			return handleDeployImpact(ctx, d, args)
		case "record":
			return handleDeployRecord(ctx, d, args)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown action: %s (use history, impact, record)", action)), nil
		}
	}
}

func handleDeployHistory(ctx context.Context, d DeploysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	service, _ := args["service"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}
	if limit > 50 {
		limit = 50
	}

	deploys, err := d.DeployStore.GetRecent(ctx, service, limit)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to query deploys: %v", err)), nil
	}

	if len(deploys) == 0 {
		return mcp.NewToolResultText(`{"message":"No deploys recorded","deploys":[]}`), nil
	}

	items := make([]map[string]any, 0, len(deploys))
	for _, dep := range deploys {
		item := map[string]any{
			"id":          dep.ID,
			"service":     dep.Service,
			"environment": dep.Environment,
			"commit_hash": dep.CommitHash,
			"branch":      dep.Branch,
			"author":      dep.Author,
			"status":      dep.Status,
			"deployed_at": dep.DeployedAt.Format(time.RFC3339),
		}
		if dep.PreErrorRate != nil && dep.PostErrorRate != nil {
			item["error_rate_change"] = fmt.Sprintf("%.2f%% → %.2f%%",
				*dep.PreErrorRate*100, *dep.PostErrorRate*100)
		}
		if len(dep.LinkedInvestigationIDs) > 0 {
			item["linked_investigations"] = len(dep.LinkedInvestigationIDs)
		}
		items = append(items, item)
	}

	resp := map[string]any{
		"count":   len(items),
		"deploys": items,
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func handleDeployImpact(ctx context.Context, d DeploysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	commitHash, _ := args["commit_hash"].(string)
	if commitHash == "" {
		return mcp.NewToolResultError("commit_hash is required"), nil
	}

	dep, err := d.DeployStore.GetByCommit(ctx, commitHash)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("deploy not found for commit %q: %v", commitHash, err)), nil
	}

	resp := map[string]any{
		"deploy": map[string]any{
			"id":            dep.ID,
			"service":       dep.Service,
			"environment":   dep.Environment,
			"commit_hash":   dep.CommitHash,
			"branch":        dep.Branch,
			"author":        dep.Author,
			"status":        dep.Status,
			"deployed_at":   dep.DeployedAt.Format(time.RFC3339),
			"files_changed": dep.FilesChanged,
		},
	}

	if dep.PreErrorRate != nil && dep.PostErrorRate != nil {
		errChangePct := 0.0
		if *dep.PreErrorRate > 0 {
			errChangePct = ((*dep.PostErrorRate - *dep.PreErrorRate) / *dep.PreErrorRate) * 100
		}
		durChangePct := 0.0
		if dep.PreAvgDurationMs != nil && dep.PostAvgDurationMs != nil && *dep.PreAvgDurationMs > 0 {
			durChangePct = ((*dep.PostAvgDurationMs - *dep.PreAvgDurationMs) / *dep.PreAvgDurationMs) * 100
		}
		resp["impact"] = map[string]any{
			"pre_error_rate":        *dep.PreErrorRate,
			"post_error_rate":       *dep.PostErrorRate,
			"error_rate_change_pct": errChangePct,
			"is_incident":           dep.Status == store.DeployStatusIncident,
		}
		if dep.PreAvgDurationMs != nil && dep.PostAvgDurationMs != nil {
			resp["impact"].(map[string]any)["pre_avg_duration_ms"] = *dep.PreAvgDurationMs
			resp["impact"].(map[string]any)["post_avg_duration_ms"] = *dep.PostAvgDurationMs
			resp["impact"].(map[string]any)["duration_change_pct"] = durChangePct
		}
	} else {
		resp["impact"] = map[string]any{
			"status":  "pending",
			"message": "Impact has not been measured yet. Deploy metrics are computed ~15min after deployment.",
		}
	}

	if len(dep.LinkedInvestigationIDs) > 0 {
		resp["linked_investigation_ids"] = dep.LinkedInvestigationIDs
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}

func handleDeployRecord(ctx context.Context, d DeploysDeps, args map[string]any) (*mcp.CallToolResult, error) {
	service, _ := args["service"].(string)
	if service == "" {
		return mcp.NewToolResultError("service is required"), nil
	}
	commit, _ := args["commit"].(string)
	if commit == "" {
		return mcp.NewToolResultError("commit is required"), nil
	}

	author, _ := args["author"].(string)
	branch, _ := args["branch"].(string)
	environment, _ := args["environment"].(string)

	var filesChanged []string
	if rawFiles, ok := args["files"]; ok {
		if arr, ok := rawFiles.([]any); ok {
			for _, f := range arr {
				if s, ok := f.(string); ok {
					filesChanged = append(filesChanged, s)
				}
			}
		}
	}

	dep, err := d.DeployStore.Create(ctx, store.CreateDeployParams{
		Service:      service,
		Environment:  environment,
		CommitHash:   commit,
		Branch:       branch,
		Author:       author,
		FilesChanged: filesChanged,
		DeploySource: store.DeploySourceManual,
	})
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to record deploy: %v", err)), nil
	}

	resp := map[string]any{
		"message":     "Deploy recorded successfully",
		"deploy_id":   dep.ID,
		"service":     dep.Service,
		"commit_hash": dep.CommitHash,
		"status":      dep.Status,
		"deployed_at": dep.DeployedAt.Format(time.RFC3339),
	}

	data, _ := json.Marshal(resp)
	return mcp.NewToolResultText(string(data)), nil
}
