package tools

import (
	"context"
	"fmt"
	"time"


	"github.com/adham90/opentrace/pkg/store"
)

// DeploysDeps holds the stores needed by the deploys tool.
type DeploysDeps struct {
	DeployStore store.DeployStore
}

// DeploysHandler returns a handler for the consolidated deploys tool.
func DeploysHandler(d DeploysDeps) ToolHandlerFunc {
	return func(ctx context.Context, request *CallToolRequest) (*CallToolResult, error) {
		if d.DeployStore == nil {
			return NewToolResultError("DeployStore not configured"), nil
		}

		args := GetArguments(request)
		action := ArgString(args, "action")

		switch action {
		case "history":
			return HandleDeployHistory(ctx, d, args)
		case "impact":
			return HandleDeployImpact(ctx, d, args)
		case "record":
			return HandleDeployRecord(ctx, d, args)
		default:
			return NewToolResultError(fmt.Sprintf("unknown action: %s (use history, impact, record)", action)), nil
		}
	}
}

func HandleDeployHistory(ctx context.Context, d DeploysDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	limit := ArgInt(args, "limit", 10, 50)

	deploys, err := d.DeployStore.GetRecent(ctx, service, limit)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("failed to query deploys: %v", err)), nil
	}

	if len(deploys) == 0 {
		return EmptyResult(`{"message":"No deploys recorded","deploys":[]}`)
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

	return JSONResult(map[string]any{
		"count":   len(items),
		"deploys": items,
	})
}

func HandleDeployImpact(ctx context.Context, d DeploysDeps, args map[string]any) (*CallToolResult, error) {
	commitHash := ArgString(args, "commit_hash")
	if commitHash == "" {
		return NewToolResultError("commit_hash is required"), nil
	}

	dep, err := d.DeployStore.GetByCommit(ctx, commitHash)
	if err != nil {
		return NewToolResultError(fmt.Sprintf("deploy not found for commit %q: %v", commitHash, err)), nil
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

	return JSONResult(resp)
}

func HandleDeployRecord(ctx context.Context, d DeploysDeps, args map[string]any) (*CallToolResult, error) {
	service := ArgString(args, "service")
	if service == "" {
		return NewToolResultError("service is required"), nil
	}
	commit := ArgString(args, "commit")
	if commit == "" {
		return NewToolResultError("commit is required"), nil
	}

	author := ArgString(args, "author")
	branch := ArgString(args, "branch")
	environment := ArgString(args, "environment")
	filesChanged := ArgStringSlice(args, "files")

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
		return NewToolResultError(fmt.Sprintf("failed to record deploy: %v", err)), nil
	}

	return JSONResult(map[string]any{
		"message":     "Deploy recorded successfully",
		"deploy_id":   dep.ID,
		"service":     dep.Service,
		"commit_hash": dep.CommitHash,
		"status":      dep.Status,
		"deployed_at": dep.DeployedAt.Format(time.RFC3339),
	})
}
