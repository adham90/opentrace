# Code Entity Registry

Connect source code files, functions, classes, and endpoints to their production history. Populated automatically by parsing stack traces from error groups, endpoint paths from request summaries, and SQL from query stats.

## Data Model

```sql
CREATE TABLE IF NOT EXISTS code_entities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    entity_type TEXT NOT NULL
        CHECK(entity_type IN ('file', 'function', 'class', 'endpoint', 'query')),
    entity_path TEXT NOT NULL,             -- "app/controllers/orders_controller.rb"
    entity_name TEXT NOT NULL DEFAULT '',   -- "OrdersController#index"
    service TEXT NOT NULL DEFAULT '',

    -- Production stats (rolled up from investigations, errors, request summaries)
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    last_incident_at TEXT DEFAULT NULL,
    last_incident_summary TEXT NOT NULL DEFAULT '',
    last_investigation_session_id TEXT DEFAULT NULL,
    avg_response_ms REAL DEFAULT NULL,
    p95_response_ms REAL DEFAULT NULL,
    has_n_plus_one INTEGER NOT NULL DEFAULT 0,

    -- Risk score
    risk_score REAL NOT NULL DEFAULT 0.0,      -- 0.0–1.0
    risk_factors TEXT NOT NULL DEFAULT '[]',    -- JSON: ["3 incidents in 30 days", "N+1 query"]

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE UNIQUE INDEX idx_code_entities_lookup ON code_entities(entity_type, entity_path, entity_name);
CREATE INDEX idx_code_entities_service ON code_entities(service);
CREATE INDEX idx_code_entities_risk ON code_entities(risk_score DESC);
```

## How It Gets Populated

No manual input needed. OpenTrace builds the registry automatically from data it already has:

```go
// 1. Parse stack traces from error groups → extract file paths, function names
func (r *CodeEntityPopulator) FromErrorGroup(eg *ErrorGroup) {
    for _, frame := range parseStackTrace(eg.Backtrace) {
        r.UpsertEntity(ctx, CodeEntity{
            EntityType: "file",
            EntityPath: frame.FilePath,     // "app/controllers/orders_controller.rb"
            EntityName: frame.FunctionName, // "OrdersController#index"
            Service:    eg.Service,
        })
        r.IncrementErrorCount(ctx, frame.FilePath, frame.FunctionName)
    }
}

// 2. Parse endpoint paths from request summaries → map to controllers
func (r *CodeEntityPopulator) FromRequestSummary(rs *RequestSummary) {
    r.UpsertEntity(ctx, CodeEntity{
        EntityType:    "endpoint",
        EntityPath:    rs.Path,           // "/api/orders"
        EntityName:    rs.Controller,     // "OrdersController#index"
        Service:       rs.Service,
        AvgResponseMs: &rs.DurationMs,
        HasNPlusOne:   rs.DuplicateQueries > 0,
    })
}

// 3. Link investigation sessions to code entities via errors investigated
func (r *CodeEntityPopulator) FromSession(session *InvestigationSession) {
    for _, fp := range session.InvestigatedErrorFingerprints {
        eg, _ := r.errorGroupStore.GetByFingerprint(ctx, fp)
        if eg != nil {
            for _, frame := range parseStackTrace(eg.Backtrace) {
                r.LinkInvestigation(ctx, frame.FilePath, session.ID)
            }
        }
    }
}
```

## Risk Score Computation

```go
func computeRiskScore(entity *CodeEntity) float64 {
    score := 0.0
    factors := []string{}

    // Error frequency (0–0.4)
    if entity.ErrorCount30d > 10 {
        score += 0.4
        factors = append(factors, fmt.Sprintf("%d errors in 30 days", entity.ErrorCount30d))
    } else if entity.ErrorCount30d > 3 {
        score += 0.2
        factors = append(factors, fmt.Sprintf("%d errors in 30 days", entity.ErrorCount30d))
    }

    // Investigation frequency (0–0.3)
    if entity.InvestigationCount >= 3 {
        score += 0.3
        factors = append(factors, fmt.Sprintf("Investigated %d times", entity.InvestigationCount))
    } else if entity.InvestigationCount >= 1 {
        score += 0.15
    }

    // Performance issues (0–0.2)
    if entity.HasNPlusOne {
        score += 0.1
        factors = append(factors, "Has N+1 query pattern")
    }
    if entity.P95ResponseMs != nil && *entity.P95ResponseMs > 1000 {
        score += 0.1
        factors = append(factors, fmt.Sprintf("P95 response: %dms", int(*entity.P95ResponseMs)))
    }

    // Recency (0–0.1)
    if entity.LastIncidentAt != nil && time.Since(*entity.LastIncidentAt) < 7*24*time.Hour {
        score += 0.1
        factors = append(factors, "Incident in the last 7 days")
    }

    entity.RiskScore = min(score, 1.0)
    entity.RiskFactors = factors
    return entity.RiskScore
}
```

## MCP Tools

```go
// code_context — Get production history for a file or function
mcp.NewTool("code_context",
    mcp.WithDescription(
        "Get production context for a source code file or function. "+
        "Returns error history, performance data, investigation history, "+
        "risk score, and relevant notes. Use this when editing or reviewing code "+
        "to understand its production behavior.",
    ),
    mcp.WithString("file", mcp.Required(),
        mcp.Description("File path (e.g., 'app/controllers/orders_controller.rb')")),
    mcp.WithString("function",
        mcp.Description("Function or method name (e.g., 'OrdersController#index')")),
)

// code_risk — Risk assessment for one or more files
mcp.NewTool("code_risk",
    mcp.WithDescription(
        "Get risk scores for source code files based on production history. "+
        "Use this before making changes to understand which files are fragile.",
    ),
    mcp.WithString("files", mcp.Required(),
        mcp.Description("Comma-separated file paths to assess")),
)

// whats_fragile — Top riskiest code paths in a service
mcp.NewTool("whats_fragile",
    mcp.WithDescription(
        "Find the riskiest code paths in a service based on production data. "+
        "Returns files/functions ranked by error frequency, investigation count, "+
        "and performance issues.",
    ),
    mcp.WithString("service",
        mcp.Description("Service name to analyze")),
    mcp.WithNumber("limit",
        mcp.Description("Max results (default 10)")),
)
```

## What Claude Code Sees

When editing `orders_controller.rb`:

```json
{
  "code_context": {
    "file": "app/controllers/orders_controller.rb",
    "risk_score": 0.72,
    "risk_factors": [
      "3 production incidents in the last 30 days",
      "N+1 query on OrdersController#index (orders.includes missing)",
      "Investigated twice for timeout errors"
    ],
    "recent_incidents": [
      {
        "date": "2026-02-25",
        "session_id": "sess_001",
        "summary": "Connection pool exhaustion triggered by batch import hitting OrdersController#index",
        "root_cause": "Missing includes(:line_items) causes 47 queries per request under load",
        "fix_applied": "Added includes(:line_items)",
        "fix_held": true
      }
    ],
    "performance": {
      "avg_response_ms": 340,
      "p95_response_ms": 1200,
      "sql_count_avg": 23
    },
    "relevant_notes": [
      { "content": "N+1 query pattern — add .includes(:line_items) when loading orders with items" }
    ],
    "suggested_actions": [
      "Check that includes(:line_items) is present in any new queries loading orders",
      "Consider adding a watcher for this endpoint's SQL count"
    ]
  }
}
```
