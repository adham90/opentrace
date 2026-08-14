package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addPrompts registers MCP prompts on the server.
func addPrompts(s *mcp.Server) {
	// 1. investigate-errors — Guided error investigation
	s.AddPrompt(
		&mcp.Prompt{
			Name:        "investigate-errors",
			Description: "Guided error investigation for a specific service",
			Arguments: []*mcp.PromptArgument{
				{Name: "service", Description: "The service name to investigate errors for", Required: true},
				{Name: "timeframe", Description: "Time window to investigate (e.g. 1h, 6h, 24h). Defaults to 1h"},
			},
		},
		investigateErrorsHandler,
	)

	// 2. database-health-check — Database health assessment
	s.AddPrompt(
		&mcp.Prompt{
			Name:        "database-health-check",
			Description: "Comprehensive database health assessment",
		},
		databaseHealthCheckHandler,
	)

	// 3. deploy-validation — Post-deploy validation
	s.AddPrompt(
		&mcp.Prompt{
			Name:        "deploy-validation",
			Description: "Post-deploy validation to verify a deployment is healthy",
			Arguments: []*mcp.PromptArgument{
				{Name: "service", Description: "The service that was deployed", Required: true},
				{Name: "commit_hash", Description: "The commit hash of the deployment (optional)"},
			},
		},
		deployValidationHandler,
	)

	// 4. triage — General system triage
	s.AddPrompt(
		&mcp.Prompt{
			Name:        "triage",
			Description: "General system triage to identify and investigate issues",
			Arguments: []*mcp.PromptArgument{
				{Name: "symptom", Description: "Optional symptom or issue description to focus the triage"},
			},
		},
		triageHandler,
	)
}

func investigateErrorsHandler(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	service := request.Params.Arguments["service"]
	if service == "" {
		return nil, fmt.Errorf("service argument is required")
	}

	timeframe := request.Params.Arguments["timeframe"]
	if timeframe == "" {
		timeframe = "1h"
	}

	prompt := fmt.Sprintf(`Investigate errors for service "%s" over the last %s. Follow these steps:

1. **Overview & Triage**: Call opentrace(tool="overview", action="triage") to get the current system status and identify top issues related to this service.

2. **List Error Groups**: Call opentrace(tool="errors", action="list", params={"service": "%s", "status": "unresolved", "limit": 10}) to see the most recent error groups.

3. **Investigate Top Error**: For the top error group by occurrence count, call opentrace(tool="errors", action="detail", params={"fingerprint": "<fingerprint>"}) to get full details including stack traces and affected endpoints.

4. **Search Related Logs**: Call opentrace(tool="logs", action="search", params={"service": "%s", "level": "error", "since": "%s", "limit": 20}) to find related error logs and surrounding context.

5. **Check Recent Changes**: Call opentrace(tool="overview", action="changes", params={"service": "%s"}) to see whether a recent deploy or config change correlates with the error spike.

6. **Summarize Findings**: Provide a summary with:
   - Root cause analysis (or best hypothesis)
   - Affected endpoints and users
   - Whether a recent deploy may have introduced the issue
   - Recommended next steps (fix, rollback, monitor)`,
		service, timeframe, service, service, timeframe, service)

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Error investigation for %s (last %s)", service, timeframe),
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: prompt},
			},
		},
	}, nil
}

func databaseHealthCheckHandler(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	prompt := `Perform a comprehensive database health assessment. Follow these steps:

1. **Query Statistics**: Call opentrace(tool="database", action="queries", params={"order_by": "calls"}) to review the most frequently executed queries and their aggregate cost.

2. **Analyze Slow Queries**: Call opentrace(tool="database", action="queries", params={"order_by": "mean_exec_time", "limit": 10}) to identify the slowest queries and their impact on performance.

3. **Review Locks**: Call opentrace(tool="database", action="locks") to check for any active lock contention that could be causing bottlenecks.

4. **Check Connections**: Call opentrace(tool="database", action="connections") to review connection pool usage and identify potential connection exhaustion.

5. **Assess Indexes**: Call opentrace(tool="database", action="indexes") to identify missing or unused indexes that could improve query performance.

6. **Provide Recommendations**: Summarize your findings with:
   - Overall database health score (healthy/degraded/critical)
   - Top performance bottlenecks identified
   - Specific index recommendations
   - Connection pool tuning suggestions
   - Queries that should be optimized (with suggestions)`

	return &mcp.GetPromptResult{
		Description: "Database health assessment",
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: prompt},
			},
		},
	}, nil
}

func deployValidationHandler(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	service := request.Params.Arguments["service"]
	if service == "" {
		return nil, fmt.Errorf("service argument is required")
	}

	commitHash := request.Params.Arguments["commit_hash"]
	commitClause := ""
	if commitHash != "" {
		commitClause = fmt.Sprintf(` (commit: %s)`, commitHash)
	}

	prompt := fmt.Sprintf(`Validate the deployment of service "%s"%s. Follow these steps:

1. **Check Recent Changes**: Call opentrace(tool="overview", action="changes", params={"service": "%s"}) to find the latest deployment/config change and compare with the previous one.

2. **Compare Error Rates**: Call opentrace(tool="errors", action="list", params={"service": "%s", "status": "unresolved", "limit": 20}) and compare the error count and new fingerprints against the pre-deploy baseline.

3. **Review New Error Fingerprints**: Look for any error groups that appeared after the deploy timestamp. New fingerprints indicate regressions introduced by this deployment.

4. **Check Endpoint Latencies**: Call opentrace(tool="logs", action="search", params={"service": "%s", "level": "info", "limit": 50}) and review request durations for any latency increases post-deploy.

5. **Health Check Status**: Call opentrace(tool="healthchecks", action="uptime") to verify all health check endpoints for this service are passing.

6. **Provide Go/No-Go Decision**: Summarize with:
   - Deploy status: GO (safe) or NO-GO (rollback recommended)
   - New errors introduced (if any)
   - Latency impact assessment
   - Health check status
   - Recommended actions (monitor, rollback, or investigate specific issues)`,
		service, commitClause, service, service, service)

	return &mcp.GetPromptResult{
		Description: fmt.Sprintf("Deploy validation for %s%s", service, commitClause),
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: prompt},
			},
		},
	}, nil
}

func triageHandler(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	symptom := request.Params.Arguments["symptom"]
	symptomClause := ""
	if symptom != "" {
		symptomClause = fmt.Sprintf(` Focus on this reported symptom: "%s".`, symptom)
	}

	prompt := fmt.Sprintf(`Perform a general system triage to identify and investigate the most pressing issues.%s Follow these steps:

1. **System Overview**: Call opentrace(tool="overview", action="status") to get a high-level view of all monitored systems, services, and their current health.

2. **Triage Alerts**: Call opentrace(tool="overview", action="triage") to get a prioritized list of current issues ranked by severity and impact.

3. **Investigate Top Issue**: For the highest-priority issue from triage, drill down using the appropriate tool:
   - For errors: opentrace(tool="errors", action="detail", params={"fingerprint": "<fingerprint>"})
   - For health checks: opentrace(tool="healthchecks", action="uptime")
   - For performance: opentrace(tool="database", action="queries", params={"order_by": "mean_exec_time"})

4. **Search Logs for Context**: Call opentrace(tool="logs", action="search", params={"level": "error", "limit": 20}) to find recent error logs that provide additional context for the identified issues.

5. **Check Watches**: Call opentrace(tool="watches", action="status") to review configured watch rules, then opentrace(tool="watches", action="alerts") for their alert history.

6. **Provide Summary**: Deliver a triage report with:
   - Current system health (healthy/degraded/critical)
   - Top 3 issues ranked by priority
   - Root cause analysis for the most critical issue
   - Immediate actions recommended
   - Items to monitor going forward`, symptomClause)

	return &mcp.GetPromptResult{
		Description: "System triage report",
		Messages: []*mcp.PromptMessage{
			{
				Role:    "user",
				Content: &mcp.TextContent{Text: prompt},
			},
		},
	}, nil
}
