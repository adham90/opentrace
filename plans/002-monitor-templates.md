# Plan 002: Monitor Templates (Bundled Presets)

## Overview

Ship a library of **ready-to-use monitor templates** so users can set up common monitoring scenarios in one click instead of writing SQL and configuring thresholds from scratch. Templates cover common PostgreSQL operational checks, application health patterns, and performance monitoring.

**Effort**: Medium | **Impact**: High

---

## Current State

- `GET /api/monitors/templates` endpoint exists (returns template definitions)
- Monitor create form requires manual SQL + threshold configuration
- Users need DBA knowledge to write effective monitoring queries
- No template selection UI in the monitor creation flow

---

## Goals

1. Curated library of 15-20 bundled Postgres monitor templates
2. Template selection UI in the "Create Monitor" flow
3. Templates are customizable before saving (adjust thresholds, intervals)
4. Template metadata: category, description, recommended severity, required Postgres version
5. Extensible design: users can save their own monitors as templates later

---

## Template Data Model

### Template Definition

```go
// internal/watcher/templates.go
type MonitorTemplate struct {
    ID          string            `json:"id"`          // e.g., "pg-slow-queries"
    Name        string            `json:"name"`        // "Slow Queries"
    Category    TemplateCategory  `json:"category"`    // performance | health | capacity | security | application
    Description string            `json:"description"` // What this monitors and why
    MonitorType store.MonitorType `json:"monitor_type"` // "rule" or "ai"

    // For rule monitors
    RuleConfig  *store.RuleConfig `json:"rule_config,omitempty"`

    // For AI monitors
    AIPrompt    string            `json:"ai_prompt,omitempty"`

    // Defaults
    Severity    store.WatcherSeverity `json:"severity"`
    TimeRange   string                `json:"time_range"`   // e.g., "5m"
    Environment string                `json:"environment"`  // suggestion, not enforced

    // Metadata
    MinPgVersion string   `json:"min_pg_version,omitempty"` // e.g., "12" — for display only
    Tags         []string `json:"tags"`
    DocURL       string   `json:"doc_url,omitempty"` // link to Postgres docs
}

type TemplateCategory string

const (
    CategoryPerformance  TemplateCategory = "performance"
    CategoryHealth       TemplateCategory = "health"
    CategoryCapacity     TemplateCategory = "capacity"
    CategorySecurity     TemplateCategory = "security"
    CategoryApplication  TemplateCategory = "application"
)
```

Templates are defined in Go code (not database) — they ship with the binary and are always up to date.

---

## Phase 1: Template Library

### 1.1 Template Definitions — `internal/watcher/templates.go`

Embed a `var BundledTemplates []MonitorTemplate` with the following templates:

#### Performance Category
| ID | Name | Type | Query/Logic | Default Threshold |
|----|------|------|-------------|-------------------|
| `pg-slow-queries` | Slow Queries | rule | `SELECT count(*) FROM pg_stat_activity WHERE state='active' AND now()-query_start > '30s'` | value > 3 |
| `pg-long-transactions` | Long-Running Transactions | rule | `SELECT count(*) FROM pg_stat_activity WHERE state='idle in transaction' AND now()-xact_start > '5 min'` | row_count > 0 |
| `pg-cache-hit-ratio` | Cache Hit Ratio | rule | `SELECT ROUND(sum(heap_blks_hit)/(sum(heap_blks_hit)+sum(heap_blks_read)+1)*100,2) FROM pg_statio_user_tables` | value < 95 |
| `pg-index-usage` | Unused Indexes | ai | AI prompt: "Analyze pg_stat_user_indexes for indexes with idx_scan=0 that waste space" | — |
| `pg-seq-scan-heavy` | Sequential Scan Heavy Tables | rule | `SELECT count(*) FROM pg_stat_user_tables WHERE seq_scan > 1000 AND seq_tup_read/GREATEST(seq_scan,1) > 10000` | row_count > 0 |

#### Health Category
| ID | Name | Type | Query/Logic | Default Threshold |
|----|------|------|-------------|-------------------|
| `pg-connection-saturation` | Connection Saturation | rule | `SELECT count(*) FROM pg_stat_activity` | value > 80% of max_connections |
| `pg-replication-lag` | Replication Lag | rule | `SELECT EXTRACT(EPOCH FROM replay_lag) FROM pg_stat_replication` | value > 60 |
| `pg-dead-tuples` | Dead Tuple Bloat | rule | `SELECT relname, n_dead_tup FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 1` | value > 100000 |
| `pg-vacuum-stale` | Stale Auto-Vacuum | rule | `SELECT count(*) FROM pg_stat_user_tables WHERE last_autovacuum < now() - interval '7 days' AND n_dead_tup > 1000` | row_count > 0 |
| `pg-connectivity` | Connection Health | health | Ping data source | latency > 5000ms |

#### Capacity Category
| ID | Name | Type | Query/Logic | Default Threshold |
|----|------|------|-------------|-------------------|
| `pg-table-size` | Large Table Growth | rule | `SELECT pg_total_relation_size('TABLE') / 1024 / 1024` | value > 10000 (MB) |
| `pg-database-size` | Database Size | rule | `SELECT pg_database_size(current_database()) / 1024 / 1024 / 1024` | value > 50 (GB) |
| `pg-txid-wraparound` | Transaction ID Wraparound Risk | rule | `SELECT age(datfrozenxid) FROM pg_database WHERE datname = current_database()` | value > 1000000000 |

#### Security Category
| ID | Name | Type | Query/Logic | Default Threshold |
|----|------|------|-------------|-------------------|
| `pg-superuser-connections` | Superuser Connections | rule | `SELECT count(*) FROM pg_stat_activity WHERE usename IN (SELECT usename FROM pg_user WHERE usesuper)` | value > 2 |
| `pg-failed-auth` | Failed Authentication Attempts | ai | AI: "Analyze recent pg_stat_activity for unusual connection patterns" | — |

#### Application Category
| ID | Name | Type | Query/Logic | Default Threshold |
|----|------|------|-------------|-------------------|
| `app-stuck-jobs` | Stuck Background Jobs | rule | `SELECT count(*) FROM jobs WHERE status='pending' AND created_at < now() - interval '1 hour'` | row_count > 0 |
| `app-failed-payments` | Failed Payments Spike | rule | `SELECT count(*) FROM payments WHERE status='failed' AND created_at > now() - interval '15 min'` | value > 5 |
| `app-signup-drop` | Signup Drop-off | rule | `SELECT count(*) FROM users WHERE created_at > now() - interval '1 hour'` | value < 1 |

**Note**: Application templates have placeholder table/column names that users must customize.

### 1.2 Tests — `internal/watcher/templates_test.go`

- All templates have required fields (ID, name, category, description)
- No duplicate IDs
- Rule templates have valid RuleConfig
- AI templates have non-empty AIPrompt

---

## Phase 2: Template API

### 2.1 List Templates Endpoint

Update existing `GET /api/monitors/templates`:

```json
{
  "templates": [
    {
      "id": "pg-slow-queries",
      "name": "Slow Queries",
      "category": "performance",
      "description": "Detect queries running longer than 30 seconds...",
      "monitor_type": "rule",
      "severity": "warning",
      "time_range": "5m",
      "tags": ["postgres", "performance"],
      "rule_config": { ... }
    }
  ],
  "categories": ["performance", "health", "capacity", "security", "application"]
}
```

### 2.2 Get Single Template

`GET /api/monitors/templates/{id}` — returns full template with all fields.

### 2.3 Create from Template

`POST /api/watchers` already works — the UI pre-fills the form from template data. No new endpoint needed.

---

## Phase 3: Template Selection UI

### 3.1 Monitor Creation Flow Update

When user clicks "Create Monitor", show a **two-step flow**:

**Step 1: Choose Starting Point**
- "Start from template" → shows template gallery
- "Start from scratch" → current form

**Step 2: Template Gallery** (if selected)
- Grid/list of templates grouped by category
- Each card shows: name, category badge, description, severity, monitor type (AI/Rule)
- Filter bar: category filter, search by name
- Click template → pre-fills create form with template values
- "Customize" banner: "This template has been pre-filled. Adjust any values before saving."

### 3.2 Customization Points

When a template is selected, all fields are editable:
- Title (pre-filled with template name, user should rename)
- SQL query (editable, shown with syntax highlighting)
- Threshold/operator (editable)
- Severity (editable)
- Time range / check interval (editable)
- Data source (user must select — templates don't include this)
- Notifications (empty — user adds their own)

### 3.3 Template Badge

Monitors created from templates show a small "From template: Slow Queries" badge in the monitor list for reference. Store `template_id` in watcher metadata or a nullable column.

---

## Phase 4: MCP Integration

### 4.1 List Templates Tool

Add `list_monitor_templates` MCP tool:
- Input: optional `category` filter
- Output: list of template summaries

### 4.2 Create from Template

Update `create_monitor` to accept optional `template_id`:
- Pre-fills defaults from template
- User overrides specific fields (e.g., data_source_id, threshold)
- AI can suggest: "I see you have a `jobs` table. Want me to create a Stuck Jobs monitor?"

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/watcher/templates.go` | New — template definitions + BundledTemplates |
| `internal/watcher/templates_test.go` | New — template validation tests |
| `internal/web/watchers.go` | Update template list endpoint, add get-by-id |
| `internal/web/server.go` | Register new route |
| `internal/web/templates/watchers_new.html` | Add template selection step |
| `internal/web/templates/watchers_form.html` | Add template pre-fill JS, badge |
| `internal/mcp/server.go` | Add list_monitor_templates tool |

---

## Future Extensions (Out of Scope)

- User-defined templates (save any monitor as a template)
- Template versioning (update templates and notify users of improved versions)
- Community template registry (like OpenClaw's ClawHub)
- Template variables (parameterized table names, thresholds)
- Auto-detection: analyze schema and suggest matching templates
