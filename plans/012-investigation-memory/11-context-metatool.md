# Context Meta-Tool, Webhooks & Cross-Agent Sharing

## Webhook/Event Intake

Accept events from the entire development ecosystem so OpenTrace has the full picture.

### Endpoints

```go
// All webhook endpoints validate via API key or bearer token

POST /api/events/deploy    // CI/CD pipeline sends deploy info
POST /api/events/pr        // GitHub webhook for PR events
POST /api/events/test      // Test runner sends results
POST /api/events/alert     // External alerting (PagerDuty, OpsGenie)
POST /api/events/commit    // Git hook sends commit info
POST /api/events/custom    // Generic event for custom integrations
```

### Event Models

```go
type PREvent struct {
    Action       string   `json:"action"`       // "opened", "merged", "closed"
    PRNumber     int      `json:"pr_number"`
    Title        string   `json:"title"`
    Author       string   `json:"author"`
    Branch       string   `json:"branch"`
    BaseBranch   string   `json:"base_branch"`
    FilesChanged []string `json:"files_changed"`
    URL          string   `json:"url"`
}

type TestEvent struct {
    TestFile     string `json:"test_file"`
    TestName     string `json:"test_name"`
    Status       string `json:"status"`       // "passed", "failed", "error"
    DurationMs   int    `json:"duration_ms"`
    ErrorMessage string `json:"error_message,omitempty"`
    CommitHash   string `json:"commit_hash"`
    Branch       string `json:"branch"`
}

type ExternalAlertEvent struct {
    Source       string `json:"source"`        // "pagerduty", "opsgenie", "custom"
    AlertID      string `json:"alert_id"`
    Summary      string `json:"summary"`
    Severity     string `json:"severity"`
    Service      string `json:"service"`
    URL          string `json:"url,omitempty"`
}
```

### How Events Connect

```
commit → PR opened → CI tests run → PR merged → deploy → metrics change → alert → investigation
   │         │            │             │           │            │           │          │
   └─────────┴────────────┴─────────────┴───────────┴────────────┴───────────┴──────────┘
                              All linked in OpenTrace
```

When an investigation starts, OpenTrace can trace the full chain: "This error started after deploy `abc123`, which was PR #456, authored by developer A, which changed `orders_controller.rb`. CI tests passed but there was no test covering the error case."

---

## The `context` Meta-Tool

Instead of requiring agents to know which of 80+ tools to call, provide one tool that returns everything relevant to the current task.

```go
mcp.NewTool("context",
    mcp.WithDescription(
        "Get all relevant OpenTrace context for your current work. "+
        "Describe what you're doing and OpenTrace returns production data, "+
        "investigation history, risk scores, and suggestions tailored to your task. "+
        "This is the recommended starting tool for any interaction with OpenTrace.",
    ),
    mcp.WithString("task", mcp.Required(),
        mcp.Description(
            "What you're currently doing. Examples: "+
            "'editing app/controllers/orders_controller.rb to add pagination', "+
            "'debugging payment timeout errors', "+
            "'reviewing PR #123 that changes payment_service.rb', "+
            "'preparing to deploy payments service', "+
            "'writing tests for OrdersController'")),
    mcp.WithString("files",
        mcp.Description("Comma-separated file paths you're working with")),
    mcp.WithString("service",
        mcp.Description("Service name if known")),
)
```

### Context Bundle by Task Type

```go
func (h *contextHandler) Handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    args := req.GetArguments()
    task := args["task"].(string)
    files := parseFilesList(args["files"])
    service := args["service"]

    taskType := classifyTask(task) // "editing", "debugging", "reviewing", "deploying", "testing"

    bundle := ContextBundle{}

    switch taskType {
    case "editing":
        // Code risk, error history, performance, notes, investigation history
        for _, file := range files {
            bundle.CodeContext = append(bundle.CodeContext, h.getCodeContext(ctx, file))
        }
        bundle.RelevantNotes = h.getNotesForFiles(ctx, files)
        bundle.RecentInvestigations = h.getInvestigationsForFiles(ctx, files)

    case "debugging":
        // Full investigation context (same as investigation memory)
        bundle.InvestigationContext = h.getInvestigationContext(ctx, service, task)
        bundle.RelevantNotes = h.getNotesForService(ctx, service)
        bundle.RunbookSuggestion = h.suggestRunbook(ctx, task)

    case "reviewing":
        // Risk assessment for changed files, production history
        bundle.DeployRisk = h.assessDeployRisk(ctx, files)
        for _, file := range files {
            bundle.CodeContext = append(bundle.CodeContext, h.getCodeContext(ctx, file))
        }
        bundle.TestGaps = h.getTestGaps(ctx, files)

    case "deploying":
        // Pre-deploy risk check, recommended watchers, safe deploy windows
        bundle.DeployRisk = h.assessDeployRisk(ctx, files)
        bundle.RecommendedWatchers = h.suggestWatchers(ctx, files, service)
        bundle.SafeDeployWindow = h.getSafeDeployWindow(ctx, service)

    case "testing":
        // Production error paths, test gaps, priority tests to write
        bundle.TestGaps = h.getTestGaps(ctx, files)
        bundle.TestPriority = h.getTestPriority(ctx, service)
    }

    data, _ := json.Marshal(bundle)
    return mcp.NewToolResultText(string(data)), nil
}
```

### What Claude Code Sees (editing a file)

```json
{
  "task_type": "editing",
  "context": {
    "code_risk": [
      {
        "file": "app/controllers/orders_controller.rb",
        "risk_score": 0.72,
        "risk_factors": ["3 incidents in 30 days", "N+1 query pattern"],
        "last_incident": "Connection pool exhaustion, 3 days ago",
        "watch_out_for": "Ensure .includes(:line_items) is present in any new queries"
      }
    ],
    "relevant_notes": [
      "N+1 query pattern on OrdersController#index — always use includes(:line_items)",
      "Batch import job can exhaust connection pool — avoid long-running transactions"
    ],
    "recent_investigations": [
      {
        "date": "2026-02-25",
        "summary": "Connection pool exhaustion from batch import",
        "related_to_this_file": true
      }
    ],
    "performance_baseline": {
      "avg_response_ms": 340,
      "sql_count": 23,
      "note": "This endpoint is the #3 most trafficked. Changes here affect 15% of total traffic."
    }
  },
  "suggested_tools": [
    { "tool": "code_context", "why": "Get detailed production history for specific functions" },
    { "tool": "deploy_risk", "why": "Assess risk before deploying your changes" }
  ]
}
```

---

## Cross-Agent Knowledge Sharing

Any MCP client (Claude Code, Cursor, Copilot, Continue) benefits from any other client's findings. OpenTrace is the shared brain.

### How It Works

Already built into Investigation Memory — the auth token identifies the user, the session tracks what they did, and context injection shares findings across users (access-scoped). This section makes it explicit for **different AI agent types**:

```
Claude Code (Developer A):
  → Investigates payment timeout
  → Finds root cause: batch import job
  → OpenTrace stores investigation + summary + root cause

Cursor (Developer B, same codebase):
  → Opens payments_controller.rb
  → Calls: context(task: "editing payments_controller.rb")
  → OpenTrace serves Developer A's findings:
    "This file was investigated 2 days ago.
     Root cause: batch import job exhausts connection pool.
     Suggestion: ensure transaction timeouts are set."

Developer B's AI agent writes safer code without ever investigating.
```

The only requirement: both agents authenticate with MCP tokens from the same OpenTrace instance and the users have overlapping data source access.

---

## Agent Assistant Store Interfaces

```go
type CodeEntityStore interface {
    Upsert(ctx context.Context, entity CodeEntity) error
    GetByPath(ctx context.Context, filePath string) (*CodeEntity, error)
    GetByFunction(ctx context.Context, filePath string, functionName string) (*CodeEntity, error)
    GetByService(ctx context.Context, service string, limit int) ([]CodeEntity, error)
    GetTopRisk(ctx context.Context, service string, limit int) ([]CodeEntity, error)
    IncrementErrorCount(ctx context.Context, filePath string) error
    IncrementIncidentCount(ctx context.Context, filePath string) error
    LinkInvestigation(ctx context.Context, filePath string, sessionID string) error
    UpdateRiskScores(ctx context.Context) error  // batch recompute
}

type DeployStore interface {
    Create(ctx context.Context, deploy Deploy) (*Deploy, error)
    GetByCommit(ctx context.Context, commitHash string) (*Deploy, error)
    List(ctx context.Context, params ListDeployParams) ([]Deploy, error)
    UpdateImpact(ctx context.Context, deployID int, impact DeployImpact) error
    GetRiskForFiles(ctx context.Context, files []string) ([]FileDeployRisk, error)
}

type TestProductionLinkStore interface {
    Upsert(ctx context.Context, link TestProductionLink) error
    GetGaps(ctx context.Context, params TestGapParams) ([]UncoveredErrorPath, error)
    GetPriority(ctx context.Context, service string, limit int) ([]UncoveredErrorPath, error)
}

type EventStore interface {
    LogPREvent(ctx context.Context, event PREvent) error
    LogTestEvent(ctx context.Context, event TestEvent) error
    LogExternalAlert(ctx context.Context, event ExternalAlertEvent) error
    LogCustomEvent(ctx context.Context, eventType string, data map[string]any) error
}
```

---

## Agent Assistant Web API Endpoints

```
-- Code Intelligence
GET  /api/code/entities                    — list code entities with risk scores
GET  /api/code/entities/{path}            — single entity detail
GET  /api/code/risk                        — risk assessment for files
GET  /api/code/fragile                     — top risky code paths

-- Deploy Intelligence
GET  /api/deploys                          — deploy history with impact
POST /api/deploys                          — record a deploy
GET  /api/deploys/{id}/impact             — deploy impact detail

-- Test Intelligence
GET  /api/tests/gaps                       — uncovered production error paths
GET  /api/tests/priority                   — highest-value tests to write

-- Events
POST /api/events/deploy                    — CI/CD deploy webhook
POST /api/events/pr                        — GitHub PR webhook
POST /api/events/test                      — test result webhook
POST /api/events/alert                     — external alert webhook
POST /api/events/commit                    — git commit webhook
POST /api/events/custom                    — generic event webhook
```
