# Test-Production Correlation

Connect test coverage to production error paths. Help AI agents write the most impactful tests.

## Data Model

```sql
CREATE TABLE IF NOT EXISTS test_production_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    test_file TEXT NOT NULL,                    -- "spec/controllers/orders_controller_spec.rb"
    production_file TEXT NOT NULL,              -- "app/controllers/orders_controller.rb"
    production_endpoint TEXT DEFAULT NULL,      -- "/api/orders"
    production_function TEXT DEFAULT NULL,      -- "OrdersController#index"

    -- Production impact of the code path this test covers
    error_fingerprints TEXT NOT NULL DEFAULT '[]',  -- errors in this code path
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,

    -- Coverage assessment
    has_error_case_coverage INTEGER NOT NULL DEFAULT 0, -- does the test cover error scenarios?
    has_performance_coverage INTEGER NOT NULL DEFAULT 0, -- does the test assert on performance?

    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_test_prod_links_prod ON test_production_links(production_file);

-- Track production error paths that have NO test coverage
CREATE TABLE IF NOT EXISTS uncovered_error_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    function_name TEXT NOT NULL DEFAULT '',
    error_fingerprint TEXT NOT NULL,
    error_count_30d INTEGER NOT NULL DEFAULT 0,
    investigation_count INTEGER NOT NULL DEFAULT 0,
    impact_score REAL NOT NULL DEFAULT 0.0,     -- from ErrorImpact (users affected)
    suggested_test_description TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX idx_uncovered_impact ON uncovered_error_paths(impact_score DESC);
```

## MCP Tools

```go
// test_gaps — Find production error paths with no test coverage
mcp.NewTool("test_gaps",
    mcp.WithDescription(
        "Find production error paths that have no test coverage. "+
        "Returns code paths ranked by production impact (error frequency, "+
        "user impact, investigation count). Use this to prioritize which tests to write.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithString("file", mcp.Description("Filter by source file")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)

// test_priority — Highest-value tests to write based on production data
mcp.NewTool("test_priority",
    mcp.WithDescription(
        "Get a prioritized list of tests to write based on production impact. "+
        "Combines error frequency, user impact, and investigation history "+
        "to identify the most valuable test coverage gaps.",
    ),
    mcp.WithString("service", mcp.Description("Filter by service")),
    mcp.WithNumber("limit", mcp.Description("Max results (default 10)")),
)
```

## What Claude Code Sees

```json
{
  "test_gaps": {
    "uncovered_error_paths": [
      {
        "file": "app/services/payment_service.rb",
        "function": "PaymentService#charge",
        "error": "Net::ReadTimeout when payment gateway is slow",
        "production_impact": {
          "error_count_30d": 47,
          "users_affected": 2400,
          "investigations": 3
        },
        "suggested_test": "Test PaymentService#charge with a slow/timing out gateway connection. Verify it raises a user-friendly error and doesn't leave the payment in a pending state."
      },
      {
        "file": "app/controllers/orders_controller.rb",
        "function": "OrdersController#create",
        "error": "ActiveRecord::RecordNotUnique on duplicate order submission",
        "production_impact": {
          "error_count_30d": 12,
          "users_affected": 89,
          "investigations": 1
        },
        "suggested_test": "Test OrdersController#create with a duplicate idempotency key. Verify it returns the existing order instead of raising."
      }
    ]
  }
}
```
