# Example Workflows

## Workflow 1: First-Time Investigation (No History)

```
Developer: "Payments are broken"
Claude Code connects to OpenTrace MCP

── Initialize ──────────────────────────────────────────────
  → Token validated → user_id: "user_42", role: "admin"
  → Session created: sess_001
  → Client: claude-code v1.5.0, workspace: /app/payments
  → Intent: unknown (no tool calls yet)

── Step 1: list_logs ───────────────────────────────────────
  Claude Code calls: list_logs(level: "error", service: "payments",
                               context: "investigating payment failures")

  OpenTrace:
    • Records step 1, intent classified: "investigation"
    • intent_detail: "investigating payment failures"
    • primary_service: "payments"
    • Checks all subsystems:
      - Agent notes for "payments" service → none yet
      - Recent deploys → deploy abc123 was 15 min ago
      - Traffic heatmap → 2.3x normal traffic
      - Admin actions → connector max_connections changed 30 min ago
    • No similar past sessions found
    • Returns: normal results + static suggestions + deploy/traffic context

  Response:
  {
    "results": { "logs": [...47 error logs...] },
    "investigation_context": {
      "deploy_correlation": {
        "deploy_hash": "abc123",
        "deploy_time": "15 minutes ago"
      },
      "traffic_context": { "current_traffic": "2.3x normal" },
      "recent_admin_actions": [
        { "action": "connector_updated", "details": "max_connections 20→10" }
      ],
      "runbook_suggestion": {
        "name": "error_spike",
        "resolution_rate": 0.65,
        "reason": "This runbook resolved 65% of similar investigations"
      }
    },
    "suggested_tools": [
      { "tool": "error_groups", "why": "See error patterns", "ranking_source": "static" }
    ]
  }

── Step 2: error_groups ────────────────────────────────────
  → Links error fingerprint "fp_abc" to session
  → error_history: first time seeing this fingerprint

── Step 3: query_datasource ────────────────────────────────
  → query: "SELECT * FROM pg_stat_activity WHERE state = 'idle in transaction'"
  → Finds 47 idle connections

── Step 4: explain_query ───────────────────────────────────
  → query fingerprint stored in query_memory
  → No prior memory for this query

── Step 5: resolve_error ───────────────────────────────────
  → Error group "fp_abc" marked resolved
  → session.resolved_error_group_ids: ["fp_abc"]
  → Strong resolution signal

── Step 6: watch ───────────────────────────────────────────
  → Watcher watch_789 created
  → session.created_watcher_ids: ["watch_789"]

── Connection closes ───────────────────────────────────────
  OpenTrace:
    1. MCP Sampling → Claude Code provides summary
    2. Auto-creates agent note on service "payments":
       "[2026-02-25] Connection pool exhaustion from batch import job"
    3. Auto-creates agent note on error "fp_abc":
       "Resolved: idle transactions from batch import"
    4. Updates query_memory for the explained query
    5. Captures post-investigation metrics snapshot
    6. Auto-compares before/after: error rate 8.2% → 0.5%
    7. Updates runbook effectiveness (error_spike runbook was suggested, session resolved)
    8. Records transitions for ranking engine
    9. Logs to audit trail
    10. Session finalized: resolved, 6 steps, 8 minutes
```

## Workflow 2: Recurring Issue (Watcher Fires, Full Context)

```
── 3 days later, watcher watch_789 fires ───────────────────
  Alert alert_555: "error_rate > 5 for payments"

── Developer B opens Claude Code ───────────────────────────
  "The payment alert fired"

── Step 1: triage_alerts ───────────────────────────────────
  Claude Code calls: triage_alerts(context: "payment alert fired")

  OpenTrace detects:
    • Alert alert_555 → watcher watch_789
    • watch_789 was created by sess_001
    • This is recurrence #2
    • Previous fix lasted 3 days

  OpenTrace enriches from ALL subsystems:
    • Agent notes: "Connection pool exhaustion from batch import job"
    • Error group: fp_abc was resolved, may have reopened
    • Query memory: "SELECT * FROM pg_stat_activity..." → previously found 47 idle connections
    • Deploy: no new deploy since last fix
    • Traffic: normal
    • Admin actions: none recent
    • Previous fix impact: error rate went from 8.2% → 0.5% but reverted

  Response:
  {
    "results": {
      "alerts": [{ "id": "alert_555", "summary": "error_rate > 5 for payments" }]
    },
    "investigation_context": {
      "recurrence_info": {
        "is_recurring": true,
        "occurrence_number": 2,
        "previous_fixes": [
          { "fix": "Killed stuck queries, created watcher", "held_for": "3 days" }
        ],
        "root_cause_from_last_time": "Batch import job lacks transaction timeout",
        "escalation_note": "Previous fix was symptomatic. Root cause identified but not fixed."
      },
      "relevant_notes": [
        { "entity": "service:payments", "content": "Connection pool exhaustion from batch import job" }
      ],
      "error_history": {
        "fingerprint": "fp_abc",
        "previously_resolved": true,
        "resolution_summary": "Idle transactions from batch import"
      },
      "query_memory": {
        "fingerprint": "SELECT * FROM pg_stat_activity...",
        "last_root_cause": "47 idle-in-transaction connections from batch import"
      },
      "previous_fix_impact": {
        "error_rate": { "before": 8.2, "after": 0.5, "improvement": "-94%" },
        "note": "Fix was effective but temporary (3 days)"
      }
    },
    "suggested_tools": [
      {
        "tool": "query_datasource",
        "why": "Check for idle-in-transaction connections. Previous investigation found 47 stuck connections from batch import. Root cause: missing transaction timeout.",
        "args": { "id": 3, "query": "SELECT * FROM pg_stat_activity WHERE state = 'idle in transaction'" },
        "confidence": 0.85,
        "ranking_source": "learned"
      }
    ]
  }

  Developer B's Claude Code:
    • Has NEVER seen this issue before
    • But knows: exact root cause, what was tried, what worked temporarily,
      why it came back, what query to run, and what to fix permanently
    • Skips ALL exploratory steps
    • Goes directly to fixing the batch import job's transaction timeout

── Result: 2-3 steps instead of 6 ─────────────────────────
```

## Workflow 3: Database Query Investigation With Memory

```
Developer: "This query is slow"
Claude Code calls: explain_query(query: "SELECT * FROM orders WHERE user_id = 42 ORDER BY created_at")

OpenTrace:
  • Fingerprint: "SELECT * FROM orders WHERE user_id = ? ORDER BY created_at"
  • Checks query_memory → FOUND:
    - Investigated 2 times before
    - Root cause: "Missing index on orders.user_id"
    - Fix: "CREATE INDEX idx_orders_user_id ON orders(user_id)"
    - Performance: 450ms → 12ms after fix last time

Response:
{
  "results": { "...explain plan..." },
  "investigation_context": {
    "query_memory": {
      "fingerprint": "SELECT * FROM orders WHERE user_id = ? ORDER BY created_at",
      "investigation_count": 2,
      "last_root_cause": "Missing index on orders.user_id — sequential scan on 2M rows",
      "last_fix": "CREATE INDEX idx_orders_user_id ON orders(user_id)",
      "performance_after_fix": { "avg_ms_before": 450, "avg_ms_after": 12 },
      "note": "This query has been investigated twice. If the index exists and it's still slow, the issue may be different this time."
    }
  }
}
```

## Workflow 4: Simple Query (Minimal Tracking)

```
Developer: "Show me user activity for last week"

── Step 1: log_stats(window: "7d", context: "weekly user activity summary")

  OpenTrace:
    • Intent: "query" (keyword: "summary")
    • Lightweight tracking — no subsystem linking
    • No investigation context injected
    • Returns: normal stats results

── Connection closes
    • Skip MCP Sampling (intent=query)
    • Auto-mark: resolved
    • No auto-notes, no metric snapshots
    • Session stored but excluded from investigation ranking
```

## Workflow 5: Concurrent Sessions With Cross-Pollination

```
── 10:00 AM ────────────────────────────────────────────────

Developer A: "Payment errors are spiking"
  → Session sess_010
  → list_logs → query_datasource → Finds 47 idle connections

── 10:02 AM ────────────────────────────────────────────────

Developer B: "Payments look broken"
  → Session sess_011
  → list_logs(level: "error", service: "payments")

  OpenTrace detects parallel investigation:

  Response:
  {
    "investigation_context": {
      "parallel_investigations": [{
        "active": true,
        "by": "teammate",
        "started": "2 minutes ago",
        "steps_completed": 2,
        "findings_so_far": "Found 47 idle-in-transaction connections via pg_stat_activity"
      }],
      "relevant_notes": [
        { "entity": "service:payments", "content": "Connection pool exhaustion from batch import job" }
      ]
    }
  }

  Developer B's Claude Code knows a teammate is already on it and what they found.
```

## Workflow 6: Health Check Failure Investigation

```
── Health check "payments-api" goes DOWN ───────────────────

Developer: "Payments API health check is failing"
  → list_healthchecks(context: "payments health check failing")

  OpenTrace:
    • Detects health check "payments-api" is down
    • triggered_by_healthcheck_id set
    • Checks for previous sessions triggered by same health check
    • Previous session 2 weeks ago: "SSL certificate expired on payments load balancer"

  Response:
  {
    "investigation_context": {
      "health_check_history": {
        "endpoint": "https://api.example.com/payments/health",
        "current_status": "down",
        "previous_outages": [{
          "date": "2026-02-14",
          "duration": "2 hours",
          "summary": "SSL certificate expired on payments load balancer",
          "fix": "Renewed certificate, added cert expiry watcher"
        }]
      }
    }
  }
```

## Workflow 7: Runbook-Driven Investigation

```
Developer: "Database is slow"
  → diagnose(context: "slow database queries")

  OpenTrace:
    • Intent: investigation
    • Checks runbook effectiveness:
      - "slow_database" runbook: 82% resolution rate, avg 3 steps after

  Response includes:
  {
    "investigation_context": {
      "runbook_suggestion": {
        "name": "slow_database",
        "resolution_rate": 0.82,
        "avg_steps_after": 3,
        "reason": "This runbook resolved 82% of slow database investigations"
      }
    }
  }

  Claude Code runs: runbook(playbook: "slow_database")
    → OpenTrace records: session.runbooks_executed = [{"name": "slow_database", "step": 1}]
    → Runbook runs db_query_stats, explain_query, db_activity, db_locks
    → Session resolves

  On close:
    → runbook_effectiveness for "slow_database": +1 resolved, avg 2 steps after
```

## Workflow 8: Code-Aware Investigation

```
Developer: "Orders page is slow"
Claude Code connects to OpenTrace MCP

── Step 1: context ─────────────────────────────────────────
  Claude Code calls: context(task: "debugging slow orders page",
                             service: "web")

  OpenTrace returns:
  {
    "task_type": "debugging",
    "context": {
      "code_risk": [{
        "file": "app/controllers/orders_controller.rb",
        "risk_score": 0.72,
        "risk_factors": ["N+1 query", "P95 1200ms"]
      }],
      "investigation_history": [{
        "date": "2026-02-20",
        "summary": "N+1 query in OrdersController#index",
        "fix": "Added includes(:line_items)",
        "fix_held": false
      }],
      "query_memory": {
        "fingerprint": "SELECT * FROM orders...",
        "last_root_cause": "Missing includes causes 47 queries per request"
      },
      "runbook_suggestion": {
        "name": "slow_database",
        "resolution_rate": 0.82
      }
    }
  }

  Claude Code immediately knows:
    • This is a known N+1 query issue
    • It was "fixed" before but the fix didn't hold
    • The specific query and root cause are already documented
    • Goes directly to checking if includes(:line_items) is present
    • Skips: diagnose, log_search, db_query_stats, explain_query
    • 1 step instead of 6
```

## Workflow 9: Pre-Deploy Safety Check

```
Developer: "Ship it"
Claude Code is about to deploy changes to payments service

── Step 1: deploy_risk ─────────────────────────────────────
  Claude Code calls: deploy_risk(files: "app/controllers/orders_controller.rb,app/services/payment_service.rb",
                                  service: "payments")

  OpenTrace returns:
  {
    "deploy_risk_assessment": {
      "overall_risk": "high",
      "files": [
        { "file": "orders_controller.rb", "risk": "high", "incidents_caused": 2 },
        { "file": "payment_service.rb", "risk": "medium", "incidents_caused": 1 }
      ],
      "recommendations": [
        "Deploy during low-traffic hours (after 10 PM)",
        "Create error_rate watcher for payments before deploying"
      ],
      "test_gaps": [
        "PaymentService#charge timeout handling has no test (3 incidents)"
      ]
    }
  }

  Claude Code:
    → Warns the developer about the risk
    → Suggests writing a test for the timeout handling first
    → Creates a watcher before deploying
    → Recommends a deploy window
```

## Workflow 10: Test Writing Guided by Production

```
Developer: "Write tests for the payments service"
Claude Code calls: test_priority(service: "payments")

OpenTrace returns:
{
  "test_priority": [
    {
      "rank": 1,
      "file": "app/services/payment_service.rb",
      "function": "PaymentService#charge",
      "error": "Net::ReadTimeout",
      "impact": { "errors_30d": 47, "users_affected": 2400, "investigations": 3 },
      "suggested_test": "Test charge with slow gateway. Verify timeout handling and payment state consistency."
    },
    {
      "rank": 2,
      "file": "app/controllers/orders_controller.rb",
      "function": "OrdersController#create",
      "error": "RecordNotUnique on duplicate submission",
      "impact": { "errors_30d": 12, "users_affected": 89, "investigations": 1 },
      "suggested_test": "Test create with duplicate idempotency key. Verify existing order returned."
    }
  ]
}

Claude Code writes the highest-impact tests first,
guided by real production error data.
```

## Workflow 11: Proactive Alert During Development

```
Developer is editing payment_service.rb
Claude Code is connected to OpenTrace MCP

── Background: watcher fires ──────────────────────────────
  Watcher "payment-error-rate" triggers: error_rate > 5

  OpenTrace detects:
    • Agent is connected and working on payments service
    • Alert is relevant to current work

  OpenTrace sends MCP notification:
  {
    "method": "notifications/message",
    "params": {
      "level": "warning",
      "logger": "opentrace",
      "data": {
        "type": "production_alert",
        "message": "Payment error rate just spiked to 8%. You're currently editing payment_service.rb — this may be related to a recent deploy.",
        "alert_id": "alert_789",
        "suggested_tool": "triage_alerts"
      }
    }
  }

  Claude Code receives notification and alerts the developer:
    "OpenTrace detected a production alert for the payments service
     you're working on. Want me to investigate?"
```
