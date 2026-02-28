# Git & Deploy Intelligence

Track deployments with full context — which files changed, who authored them, and what production impact resulted.

## Data Model

```sql
CREATE TABLE IF NOT EXISTS deploys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    commit_hash TEXT NOT NULL,
    branch TEXT NOT NULL DEFAULT '',
    author TEXT NOT NULL DEFAULT '',
    message TEXT NOT NULL DEFAULT '',
    files_changed TEXT NOT NULL DEFAULT '[]',     -- JSON array of file paths
    service TEXT NOT NULL DEFAULT '',
    environment TEXT NOT NULL DEFAULT 'production',

    -- Impact tracking (filled in asynchronously after deploy)
    error_rate_before REAL DEFAULT NULL,
    error_rate_after REAL DEFAULT NULL,
    error_rate_delta REAL DEFAULT NULL,
    response_time_before_ms REAL DEFAULT NULL,
    response_time_after_ms REAL DEFAULT NULL,
    response_time_delta_ms REAL DEFAULT NULL,
    caused_incident INTEGER NOT NULL DEFAULT 0,
    incident_session_id TEXT DEFAULT NULL,

    deployed_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_deploys_service ON deploys(service, deployed_at DESC);
CREATE INDEX idx_deploys_commit ON deploys(commit_hash);
CREATE INDEX idx_deploys_impact ON deploys(caused_incident, deployed_at DESC);
```

## Webhook Endpoint

```go
// POST /api/events/deploy — called by CI/CD pipeline
type DeployEvent struct {
    CommitHash   string   `json:"commit_hash"`
    Branch       string   `json:"branch"`
    Author       string   `json:"author"`
    Message      string   `json:"message"`
    FilesChanged []string `json:"files_changed"`
    Service      string   `json:"service"`
    Environment  string   `json:"environment"`
}
```

## Impact Tracking (Background Job)

```go
// Run 15 minutes after each deploy to measure impact
func (d *DeployTracker) MeasureImpact(ctx context.Context, deploy *Deploy) {
    // Compare metrics from 15 min before deploy to 15 min after
    before := d.getMetrics(ctx, deploy.Service, deploy.DeployedAt.Add(-15*time.Minute), deploy.DeployedAt)
    after := d.getMetrics(ctx, deploy.Service, deploy.DeployedAt, deploy.DeployedAt.Add(15*time.Minute))

    deploy.ErrorRateBefore = before.ErrorRate
    deploy.ErrorRateAfter = after.ErrorRate
    deploy.ErrorRateDelta = after.ErrorRate - before.ErrorRate
    deploy.ResponseTimeBeforeMs = before.AvgResponseMs
    deploy.ResponseTimeAfterMs = after.AvgResponseMs
    deploy.ResponseTimeDeltaMs = after.AvgResponseMs - before.AvgResponseMs

    // Check if an investigation session started within 30 min of deploy
    session, _ := d.sessionStore.FindByTimeRange(ctx, FindByTimeRangeParams{
        Service: deploy.Service,
        After:   deploy.DeployedAt,
        Before:  deploy.DeployedAt.Add(30 * time.Minute),
        Intent:  "investigation",
    })
    if session != nil {
        deploy.CausedIncident = true
        deploy.IncidentSessionID = &session.ID
    }

    // Update risk scores for all changed files
    for _, file := range deploy.FilesChanged {
        if deploy.CausedIncident {
            d.codeEntityStore.IncrementIncidentCount(ctx, file)
        }
    }
}
```

## MCP Tools

```go
// deploy_history — Recent deploys with impact
mcp.NewTool("deploy_history",
    mcp.WithDescription(
        "View recent deployments with their production impact scores. "+
        "Shows error rate and response time changes caused by each deploy.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

// deploy_risk — Pre-deployment risk assessment
mcp.NewTool("deploy_risk",
    mcp.WithDescription(
        "Assess the risk of deploying changes to specific files. "+
        "Returns historical impact data for each file and an overall risk score. "+
        "Use this before deploying to understand potential production impact.",
    ),
    mcp.WithString("files", mcp.Required(),
        mcp.Description("Comma-separated file paths that will be deployed")),
    mcp.WithString("service",
        mcp.Description("Target service")),
)

// record_deploy — Record a deployment event
mcp.NewTool("record_deploy",
    mcp.WithDescription("Record a deployment for impact tracking."),
    mcp.WithString("commit", mcp.Required(), mcp.Description("Commit hash")),
    mcp.WithString("files", mcp.Description("Comma-separated changed files")),
    mcp.WithString("service", mcp.Description("Service name")),
    mcp.WithString("message", mcp.Description("Commit message")),
)
```

## What Claude Code Sees (Pre-Deploy Check)

```json
{
  "deploy_risk_assessment": {
    "overall_risk": "high",
    "reason": "2 of 3 changed files have recent production incidents",
    "files": [
      {
        "file": "app/controllers/orders_controller.rb",
        "risk": "high",
        "history": {
          "deploys_last_30d": 5,
          "incidents_caused": 2,
          "last_incident": "2026-02-25: Connection pool exhaustion"
        }
      },
      {
        "file": "app/services/payment_service.rb",
        "risk": "medium",
        "history": {
          "deploys_last_30d": 3,
          "incidents_caused": 1,
          "last_incident": "2026-02-10: Payment timeout"
        }
      },
      {
        "file": "app/models/user.rb",
        "risk": "low",
        "history": {
          "deploys_last_30d": 8,
          "incidents_caused": 0
        }
      }
    ],
    "recommendations": [
      "Deploy during low-traffic hours (after 10 PM based on your traffic heatmap)",
      "Create a watcher for error_rate on payments service before deploying",
      "Monitor OrdersController#index response time for 15 minutes after deploy"
    ]
  }
}
```
