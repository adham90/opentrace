# Database History & Change Tracking Plan

## Problem Statement

The DatabaseConnector only shows **current state**. During investigations, the agent can't answer "what changed?" questions, which are critical for root cause analysis. Examples:

- "Orders started failing at 3pm — what changed in the DB around then?"
- "This user was active yesterday but now shows inactive. What happened?"
- "The feature flag was working yesterday, who turned it off?"

## Constraints

- OpenTrace connects to user databases **read-only** — cannot create triggers or history tables on the target DB
- If the target DB already has audit tables, the agent can query them today (no changes needed)

---

## Feature 1: Audit Table Discovery (Small)

### Goal
Auto-detect existing history/audit tables in the user's database so the agent uses them without being told.

### Approach
Enhance `db_schema` to look for common audit patterns during schema introspection:
- Table name suffixes: `_history`, `_audit`, `_log`, `_changelog`
- Column patterns: `changed_at`, `modified_at`, `operation`, `old_value`, `new_value`
- Trigger functions that suggest audit logging

### Value
Agent automatically finds and leverages existing audit infrastructure. Zero setup for users who already have history tables.

### Effort
Small — pattern matching on top of existing schema introspection in `DatabaseConnector.handleDbSchema()`.

---

## Feature 2: Query Audit Log (Small)

### Goal
Log every `db_search` query the agent runs during investigations for replay and review.

### Schema
```sql
CREATE TABLE query_audit_log (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID REFERENCES investigations(id),
    query TEXT NOT NULL,
    row_count INTEGER,
    duration_ms INTEGER,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

### Approach
- Add logging in `DatabaseConnector.executeQuery()` after each successful query
- Store in OpenTrace's own database (not the target DB)
- Make opt-in via config flag to avoid storing sensitive query results

### Value
- Replay past investigations: "what did the agent look at?"
- Compare investigation approaches across runs
- Debugging agent behavior

### Effort
Small — insert after each query execution, new migration for the table.

---

## Feature 3: Snapshot Diffing (Medium)

### Goal
Let the agent take a "snapshot" of a query result and compare it to a later run to show what changed.

### Schema
```sql
CREATE TABLE query_snapshots (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id UUID REFERENCES investigations(id),
    name TEXT NOT NULL,
    query TEXT NOT NULL,
    result_hash TEXT NOT NULL,
    result_data JSONB,
    row_count INTEGER,
    created_at TIMESTAMPTZ DEFAULT now()
);
```

### Approach
- New agent tool: `db_snapshot` — saves current query result with a name
- New agent tool: `db_diff` — re-runs a saved snapshot's query and shows row-level diff
- Size limits: hash + sample rows for large results, don't store 500-row results verbatim

### Value
- "Compare the users table now vs when I last checked"
- Track data drift across recurring investigations
- Powerful for monitoring-style use cases

### Effort
Medium — two new tools, storage, diff logic, result serialization.

---

## Feature 4: Audit Setup Guide (Minimal)

### Goal
Provide users with SQL templates to set up audit triggers on their own databases.

### Approach
Documentation or in-app guide with template SQL:
```sql
-- Generic audit trigger template
CREATE TABLE {table}_history (LIKE {table} INCLUDING ALL, changed_at TIMESTAMPTZ DEFAULT now(), operation TEXT);

CREATE FUNCTION audit_{table}() RETURNS trigger AS $$
BEGIN
  INSERT INTO {table}_history SELECT OLD.*, now(), TG_OP;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER {table}_audit
  AFTER UPDATE OR DELETE ON {table}
  FOR EACH ROW EXECUTE FUNCTION audit_{table}();
```

### Value
Users who want change tracking can set it up, then OpenTrace can query it via Feature 1.

### Effort
Minimal — documentation only.

---

## Implementation Order

1. **Audit Table Discovery** — small win, immediate value, no new tables
2. **Query Audit Log** — small effort, enables investigation replay
3. **Snapshot Diffing** — medium effort, powerful for recurring investigations
4. **Audit Setup Guide** — nice-to-have documentation
