package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/adham90/opentrace/internal/store"
)

// deployHistoryHandler returns recent deploys.
func deployHistoryHandler(ds store.DeployStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ds == nil {
			return mcp.NewToolResultError("DeployStore not configured"), nil
		}

		args := request.GetArguments()
		service, _ := args["service"].(string)
		limit := 10
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}
		if limit > 50 {
			limit = 50
		}

		deploys, err := ds.GetRecent(ctx, service, limit)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to query deploys: %v", err)), nil
		}

		if len(deploys) == 0 {
			return mcp.NewToolResultText(`{"message":"No deploys recorded","deploys":[]}`), nil
		}

		items := make([]map[string]any, 0, len(deploys))
		for _, d := range deploys {
			item := map[string]any{
				"id":          d.ID,
				"service":     d.Service,
				"environment": d.Environment,
				"commit_hash": d.CommitHash,
				"branch":      d.Branch,
				"author":      d.Author,
				"status":      d.Status,
				"deployed_at": d.DeployedAt.Format(time.RFC3339),
			}
			if d.PreErrorRate != nil && d.PostErrorRate != nil {
				item["error_rate_change"] = fmt.Sprintf("%.2f%% → %.2f%%",
					*d.PreErrorRate*100, *d.PostErrorRate*100)
			}
			if len(d.LinkedInvestigationIDs) > 0 {
				item["linked_investigations"] = len(d.LinkedInvestigationIDs)
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
}

// deployImpactHandler returns impact metrics for a specific deploy.
func deployImpactHandler(ds store.DeployStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ds == nil {
			return mcp.NewToolResultError("DeployStore not configured"), nil
		}

		args := request.GetArguments()
		commitHash, _ := args["commit_hash"].(string)
		if commitHash == "" {
			return mcp.NewToolResultError("commit_hash is required"), nil
		}

		d, err := ds.GetByCommit(ctx, commitHash)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("deploy not found for commit %q: %v", commitHash, err)), nil
		}

		resp := map[string]any{
			"deploy": map[string]any{
				"id":            d.ID,
				"service":       d.Service,
				"environment":   d.Environment,
				"commit_hash":   d.CommitHash,
				"branch":        d.Branch,
				"author":        d.Author,
				"status":        d.Status,
				"deployed_at":   d.DeployedAt.Format(time.RFC3339),
				"files_changed": d.FilesChanged,
			},
		}

		if d.PreErrorRate != nil && d.PostErrorRate != nil {
			errChangePct := 0.0
			if *d.PreErrorRate > 0 {
				errChangePct = ((*d.PostErrorRate - *d.PreErrorRate) / *d.PreErrorRate) * 100
			}
			durChangePct := 0.0
			if d.PreAvgDurationMs != nil && d.PostAvgDurationMs != nil && *d.PreAvgDurationMs > 0 {
				durChangePct = ((*d.PostAvgDurationMs - *d.PreAvgDurationMs) / *d.PreAvgDurationMs) * 100
			}
			resp["impact"] = map[string]any{
				"pre_error_rate":        *d.PreErrorRate,
				"post_error_rate":       *d.PostErrorRate,
				"error_rate_change_pct": errChangePct,
				"is_incident":           d.Status == store.DeployStatusIncident,
			}
			if d.PreAvgDurationMs != nil && d.PostAvgDurationMs != nil {
				resp["impact"].(map[string]any)["pre_avg_duration_ms"] = *d.PreAvgDurationMs
				resp["impact"].(map[string]any)["post_avg_duration_ms"] = *d.PostAvgDurationMs
				resp["impact"].(map[string]any)["duration_change_pct"] = durChangePct
			}
		} else {
			resp["impact"] = map[string]any{
				"status":  "pending",
				"message": "Impact has not been measured yet. Deploy metrics are computed ~15min after deployment.",
			}
		}

		if len(d.LinkedInvestigationIDs) > 0 {
			resp["linked_investigation_ids"] = d.LinkedInvestigationIDs
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}

// recordDeployHandler records a new deploy event.
func recordDeployHandler(ds store.DeployStore) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if ds == nil {
			return mcp.NewToolResultError("DeployStore not configured"), nil
		}

		args := request.GetArguments()
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

		d, err := ds.Create(ctx, store.CreateDeployParams{
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
			"deploy_id":   d.ID,
			"service":     d.Service,
			"commit_hash": d.CommitHash,
			"status":      d.Status,
			"deployed_at": d.DeployedAt.Format(time.RFC3339),
		}

		data, _ := json.Marshal(resp)
		return mcp.NewToolResultText(string(data)), nil
	}
}
