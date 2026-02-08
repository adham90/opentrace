# AI Agent Improvements Plan

## Problem Statement

The AI agent has two core weaknesses:
1. **Schema discovery every time** — Burns 2+ agent steps calling `db_schema` (list tables, then get columns) before it can write any SQL query. This wastes the tool call budget (max 8) and slows investigations.
2. **Doesn't always know how to fetch data** — Minimal tool descriptions, no relationship/FK info, no log field awareness. The agent guesses and often gets it wrong.

---

## Improvement 1: Schema Cache in `db_schema` Tool (5-min TTL)

### Goal
Cache schema results inside DatabaseConnector so repeated `db_schema` calls return instantly without hitting the target DB. Cache expires after 5 minutes so schema changes are picked up.

### Current Behavior
- `handleDbSchema` runs a fresh `information_schema` query every time
- Agent calls it 2+ times per investigation (list tables, then columns per table)

### Proposed Changes

**File: `internal/connector/database.go`**

1. Add a `schemaCache` struct to DatabaseConnector:
   ```go
   type schemaCacheEntry struct {
       content   string
       fetchedAt time.Time
   }

   type DatabaseConnector struct {
       pool       *pgxpool.Pool
       maxRows    int
       cacheMu    sync.RWMutex
       schemaCache map[string]schemaCacheEntry // key: "" for table list, "tablename" for columns
       cacheTTL   time.Duration
   }
   ```

2. In `NewDatabaseConnector`, initialize the cache:
   ```go
   cacheTTL: 5 * time.Minute,
   schemaCache: make(map[string]schemaCacheEntry),
   ```

3. In `handleDbSchema`, check cache before querying:
   ```go
   func (c *DatabaseConnector) handleDbSchema(ctx context.Context, args map[string]any) (string, error) {
       table, _ := args["table"].(string)
       cacheKey := table // "" for table list

       c.cacheMu.RLock()
       if entry, ok := c.schemaCache[cacheKey]; ok && time.Since(entry.fetchedAt) < c.cacheTTL {
           c.cacheMu.RUnlock()
           return entry.content, nil
       }
       c.cacheMu.RUnlock()

       // ... existing query logic ...
       result := sb.String()

       // Store in cache
       c.cacheMu.Lock()
       c.schemaCache[cacheKey] = schemaCacheEntry{content: result, fetchedAt: time.Now()}
       c.cacheMu.Unlock()

       return result, nil
   }
   ```

### Tests
- Unit test: call `handleDbSchema` twice, verify second call returns cached result (mock pool to track query count)
- Unit test: verify cache expires after TTL (use a short TTL like 10ms in test)
- Unit test: verify different table keys are cached independently

---

## Improvement 2: Enhanced Schema Metadata (Foreign Keys, Row Counts, Sample Values)

### Goal
When the agent calls `db_schema` with a table name, return richer metadata so it can write correct JOINs and understand data patterns without trial and error.

### Current Behavior
`handleDbSchema` returns only: column name, data type, nullable, default. No FKs, no indexes, no row counts.

### Proposed Changes

**File: `internal/connector/database.go`**

Extend the table-specific branch of `handleDbSchema` to also query:

1. **Foreign keys** — so the agent knows how tables relate:
   ```sql
   SELECT
       kcu.column_name,
       ccu.table_schema || '.' || ccu.table_name AS foreign_table,
       ccu.column_name AS foreign_column
   FROM information_schema.table_constraints tc
   JOIN information_schema.key_column_usage kcu
       ON tc.constraint_name = kcu.constraint_name
   JOIN information_schema.constraint_column_usage ccu
       ON tc.constraint_name = ccu.constraint_name
   WHERE tc.constraint_type = 'FOREIGN KEY'
       AND tc.table_name = $1
   ```

2. **Row count estimate** — so the agent knows table size without COUNT(*):
   ```sql
   SELECT reltuples::bigint AS estimate
   FROM pg_class
   WHERE relname = $1
   ```

3. **Sample values** (top 3 distinct values per column, only for small cardinality columns) — so the agent understands data format:
   ```sql
   SELECT DISTINCT <column> FROM <table> LIMIT 3
   ```
   Only run this for columns where estimated distinct count is < 50 (from `pg_stats.n_distinct`). This prevents scanning huge columns.

4. **Table/column comments** — if users added `COMMENT ON` descriptions:
   ```sql
   -- Table comment
   SELECT obj_description(oid) FROM pg_class WHERE relname = $1;
   -- Column comments
   SELECT a.attname, d.description
   FROM pg_description d
   JOIN pg_attribute a ON d.objsubid = a.attnum AND d.objoid = a.attrelid
   WHERE a.attrelid = $1::regclass;
   ```

### Output Format
```
Columns for orders:
  id integer NOT NULL DEFAULT nextval(...)
  user_id integer NOT NULL  →  public.users(id)
  product_id integer NOT NULL  →  public.products(id)
  total numeric(10,2) NOT NULL
  status text NOT NULL  [values: pending, shipped, delivered]
  created_at timestamp NOT NULL DEFAULT now()

Row count (estimate): ~12,400
```

The `→ public.users(id)` suffix shows the FK target inline. The `[values: ...]` shows sample values for low-cardinality columns.

### Tests
- Unit test: verify FK info is included in output (use a test DB with known FKs)
- Unit test: verify row count estimate is shown
- Unit test: verify sample values only appear for low-cardinality columns
- All behind `testing.Short()` skip since they need a real Postgres

---

## Improvement 3: Better Tool Descriptions and Prompt Instructions

### Goal
Give the agent clearer guidance on how to use each tool effectively, especially for log search which has fields the agent doesn't know about.

### Current Behavior
- `log_search` description: `"Search ingested logs by keyword, service, level, or trace ID."` — doesn't mention `environment`, `start`/`end` time filtering, or metadata field
- `db_search` description mentions examples but not strategies
- System prompt says "first use db_schema" which forces the schema lookup even when the agent might already have it cached

### Proposed Changes

**File: `internal/connector/logs.go`**

Update tool description to be much more specific:
```go
Description: `Search ingested logs. Supports filtering by:
- query: full-text keyword search across log messages
- service: exact match on service name (e.g. "api-gateway", "auth-service")
- level: log level filter (debug, info, warn, error, fatal)
- trace_id: find all logs for a specific trace/request
- environment: filter by environment (e.g. "production", "staging")
- limit: max results (default 50)

Tips:
- To investigate an error, start with level="error" to find failures
- Use trace_id to follow a single request across services
- Combine service + level to narrow down (e.g. service="payments" level="error")
- Results are sorted by timestamp descending (newest first)`,
```

Also add the missing `environment` and time range params to the tool definition:
```go
Params: []agent.ToolParam{
    {Name: "query", Type: "string", Required: false},
    {Name: "service", Type: "string", Required: false},
    {Name: "level", Type: "string", Required: false},
    {Name: "trace_id", Type: "string", Required: false},
    {Name: "environment", Type: "string", Required: false},
    {Name: "start", Type: "string", Required: false},  // ISO 8601
    {Name: "end", Type: "string", Required: false},    // ISO 8601
    {Name: "limit", Type: "int", Required: false},
},
```

Update `handleLogSearch` to parse `environment`, `start`, and `end` from args.

**File: `internal/connector/database.go`**

Update `db_search` description:
```go
Description: `Execute a read-only SQL SELECT query against the PostgreSQL database.
Only SELECT statements are allowed. A LIMIT is auto-applied if you don't include one.

Tips:
- Use db_schema first if you don't know the table structure
- Use JOINs when you see foreign key relationships in the schema
- For counting: SELECT COUNT(*) FROM table WHERE ...
- For aggregation: SELECT col, COUNT(*) FROM table GROUP BY col
- For date filtering: WHERE created_at >= '2024-01-01'
- If a query errors, check column names against db_schema output`,
```

Update `db_schema` description:
```go
Description: `Get database schema information. Call with no args to list all tables.
Call with a table name to see columns, types, foreign keys, and sample values.
Schema results are cached for 5 minutes.

Tips:
- Start here to understand the database structure
- Foreign keys show as → target_table(column) so you know how to JOIN
- Sample values help you understand what data looks like
- Row count estimates help you gauge table size`,
```

**File: `internal/agent/prompt.go`**

Update the Strategy section:
```
## Strategy

- For database questions: use db_schema to understand table structure (it's fast, results are cached). Then write SQL with db_search.
- Look at foreign key references in db_schema output to know how to JOIN tables.
- For log investigation: use log_search with filters. Start broad, then narrow down.
- To trace a request: find the trace_id in logs, then use it to find all related log entries.
- For complex questions: break them into steps. Schema first, then targeted queries.
- Always provide specific numbers and data in your final answer, not vague statements.
- If a query returns an error, read the error message carefully and fix your SQL.
- NEVER mention tools, errors, or internal steps in your final answer.
```

### Tests
- Existing agent tests should still pass (description changes don't break behavior)
- Verify `log_search` now accepts and filters by `environment`, `start`, `end`

---

## Improvement 4: Chat Persistence with DB-Backed Messages

### Goal
Replace the one-shot investigation model with persistent chat conversations. Each chat stores its full message history in the database, so the agent sees all prior context (including schema it already discovered) when the user sends a follow-up message.

### Current Behavior
- Each `handleInvestigateSSE` call creates a fresh agent with no history
- Previous tool results, schema discoveries, and queries are lost
- The existing `investigations` and `traces` tables track single queries, not conversations

### Why DB-Backed (not in-memory)
- Survives server restarts
- Users can see and continue past conversations
- Enables a proper chat UI with history
- The agent sees full message history (tool calls + results) not lossy summaries

### Database Schema

**New migration: `migrations/000003_add_chats.up.sql`**

```sql
-- Chats (conversation sessions)
CREATE TABLE chats (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title      TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

-- Messages within a chat
CREATE TABLE messages (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    chat_id    UUID NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    role       TEXT NOT NULL,   -- 'user', 'assistant', 'tool_call', 'observation'
    content    TEXT NOT NULL,
    tool_name  TEXT,            -- set for tool_call/observation messages
    created_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_messages_chat_id ON messages (chat_id, created_at);
```

**Down migration: `migrations/000003_add_chats.down.sql`**

```sql
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS chats;
```

### Store Layer

**File: `internal/store/models.go`** — Add models:

```go
type Chat struct {
    ID        uuid.UUID `json:"id"`
    Title     string    `json:"title"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}

type Message struct {
    ID        uuid.UUID `json:"id"`
    ChatID    uuid.UUID `json:"chat_id"`
    Role      string    `json:"role"`      // "user", "assistant", "tool_call", "observation"
    Content   string    `json:"content"`
    ToolName  string    `json:"tool_name,omitempty"`
    CreatedAt time.Time `json:"created_at"`
}
```

**File: `internal/store/store.go`** — Add ChatStore interface:

```go
type ChatStore interface {
    CreateChat(ctx context.Context, title string) (*Chat, error)
    GetChat(ctx context.Context, id uuid.UUID) (*Chat, error)
    ListChats(ctx context.Context) ([]Chat, error)
    DeleteChat(ctx context.Context, id uuid.UUID) error
    UpdateChatTitle(ctx context.Context, id uuid.UUID, title string) error
    AddMessage(ctx context.Context, msg Message) error
    GetMessages(ctx context.Context, chatID uuid.UUID) ([]Message, error)
}
```

**New file: `internal/store/chat_store.go`** — PostgreSQL implementation:

- `CreateChat` — inserts into `chats`, returns new Chat
- `GetChat` — fetch by ID
- `ListChats` — ordered by `updated_at DESC` (most recent first)
- `DeleteChat` — cascade deletes messages
- `UpdateChatTitle` — update title + `updated_at`
- `AddMessage` — insert into `messages`, also touch `chats.updated_at`
- `GetMessages` — fetch all messages for a chat ordered by `created_at ASC`

### Agent Loop Changes

**File: `internal/agent/loop.go`**

Update `RunWithCallback` to accept prior message history:

```go
func (a *Agent) RunWithCallback(
    ctx context.Context,
    query string,
    tools []Tool,
    cb EventCallback,
    history []llm.ChatMessage,  // NEW: prior conversation messages
) (string, error) {
```

Build initial messages as:
```go
messages := []llm.ChatMessage{
    {Role: "system", Content: systemPrompt},
}
// Append prior history (user msgs, assistant msgs, tool results from previous turns)
messages = append(messages, history...)
// Append current user query
messages = append(messages, llm.ChatMessage{Role: "user", Content: query})
```

This means if the agent discovered schema 2 messages ago, that tool result is already in context — no need to call `db_schema` again.

**File: `internal/agent/prompt.go`**

Update strategy to mention conversation context:
```
- If you already have schema information or query results from earlier in this conversation, use them directly instead of calling tools again.
```

### Web Layer Changes

**File: `internal/web/server.go`**

- Add `chatStore store.ChatStore` to Server struct
- Update `NewServer` signature to accept ChatStore
- Wire chat API routes

**File: `internal/web/investigate.go`**

Update `handleInvestigateSSE` to work with chat sessions:

1. Accept `chat_id` query param (optional). If not provided, create a new chat.
2. Load existing messages from DB for the chat.
3. Convert stored messages to `[]llm.ChatMessage` for the agent.
4. After each agent step, persist the message to DB:
   - User query → `role: "user"`
   - Agent JSON response → `role: "assistant"`
   - Tool call → `role: "tool_call"` with `tool_name`
   - Tool result → `role: "observation"` with `tool_name`
   - Final answer → `role: "assistant"`
5. Auto-generate chat title from first user query (first 80 chars).
6. Return `chat_id` in the SSE stream (as a `chat_id` event at the start) so the frontend knows which chat this belongs to.

### Chat API Endpoints

**File: `internal/web/chat.go`** (new)

Add REST endpoints for chat management:

```
GET    /api/chats           — list all chats (id, title, updated_at)
GET    /api/chats/{id}      — get chat with all messages
DELETE /api/chats/{id}      — delete chat and its messages
```

Wire in `server.go`:
```go
r.Get("/chats", srv.handleListChats)
r.Get("/chats/{id}", srv.handleGetChat)
r.Delete("/chats/{id}", srv.handleDeleteChat)
```

### Updated Investigate SSE Endpoint

The SSE endpoint changes from:
```
GET /api/investigate?query=...
```
To:
```
GET /api/investigate?query=...&chat_id=...
```

- If `chat_id` is empty: create new chat, return `chat_id` event
- If `chat_id` is provided: load history, continue conversation

### Frontend Changes (minimal, out of scope for backend plan)

The investigate page will need:
- A sidebar showing chat history (list of past chats)
- Clicking a chat loads its messages
- New messages append to the current chat
- "New chat" button to start fresh

These are frontend-only changes and don't affect the backend implementation.

### Tests

**New file: `internal/store/chat_store_test.go`** (integration, skip in short mode):
- CreateChat + GetChat round-trip
- ListChats returns ordered by updated_at
- AddMessage + GetMessages returns ordered by created_at
- DeleteChat cascades to messages
- UpdateChatTitle updates title and updated_at

**New file: `internal/web/chat_test.go`** (unit, with mock store):
- GET /api/chats returns list
- GET /api/chats/{id} returns chat with messages
- DELETE /api/chats/{id} deletes
- GET /api/investigate with chat_id loads history
- GET /api/investigate without chat_id creates new chat

**Update: `internal/agent/loop_test.go`**:
- Test that history messages are prepended to conversation
- Test that agent reuses schema from history instead of calling db_schema again

**Update: `internal/web/mock_test.go`**:
- Add `mockChatStore` implementing `store.ChatStore`

---

## Improvement 5: System Memory (Cross-Conversation Knowledge Base)

### Goal
Give the agent a persistent knowledge base that accumulates facts across all conversations. Unlike chat history (per-conversation), system memory is **global** — every investigation benefits from what the agent learned in previous ones.

### What Gets Stored
The agent learns and stores facts like:
- **Schema insights**: "The `users.status` column is always one of: active, suspended, deleted"
- **Common patterns**: "The `orders.user_id` FK to `users.id` is the most common join"
- **Error patterns**: "The payments service frequently throws timeout errors to Stripe between 2-3am UTC"
- **Data characteristics**: "The `logs` table grows ~10k rows/day, mostly from the api-gateway service"
- **Query patterns**: "To find failed orders, join `orders` with `payments` on `order_id` and filter `payments.status = 'failed'`"

### Why This Matters
- Chat history helps within a conversation, but each new chat starts cold
- System memory means the agent gets smarter over time as it investigates more
- Reduces token waste — the agent doesn't re-discover the same facts repeatedly
- Especially valuable for recurring questions about the same database

### Database Schema

Add to the same migration as chats (`migrations/000003_add_chats.up.sql`):

```sql
-- System memory (cross-conversation knowledge base)
CREATE TABLE agent_memory (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    category   TEXT NOT NULL,   -- 'schema', 'pattern', 'error', 'data', 'query'
    content    TEXT NOT NULL,   -- the fact/insight
    source     TEXT,            -- which chat/query produced this insight
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_agent_memory_category ON agent_memory (category);
```

Update down migration to also drop `agent_memory`.

### Store Layer

**File: `internal/store/store.go`** — Add MemoryStore interface:

```go
type MemoryStore interface {
    AddMemory(ctx context.Context, category, content, source string) error
    ListMemories(ctx context.Context, category string) ([]MemoryEntry, error)
    SearchMemories(ctx context.Context, query string) ([]MemoryEntry, error)
    DeleteMemory(ctx context.Context, id uuid.UUID) error
}
```

**File: `internal/store/models.go`** — Add model:

```go
type MemoryEntry struct {
    ID        uuid.UUID `json:"id"`
    Category  string    `json:"category"`
    Content   string    `json:"content"`
    Source    string    `json:"source,omitempty"`
    CreatedAt time.Time `json:"created_at"`
    UpdatedAt time.Time `json:"updated_at"`
}
```

**New file: `internal/store/memory_store.go`** — PostgreSQL implementation:

- `AddMemory` — insert a new fact, deduplicate by content (upsert on content match updates `updated_at`)
- `ListMemories` — filter by category, ordered by `updated_at DESC`
- `SearchMemories` — full-text search across content: `to_tsvector('english', content) @@ plainto_tsquery(...)`
- `DeleteMemory` — remove outdated or wrong facts

### Agent Tools

Two new tools exposed to the agent:

**`memory_read`** — Retrieve relevant memories before starting an investigation:
```go
{
    Name:        "memory_read",
    Description: `Read from the system knowledge base. Returns facts learned from previous investigations.
Call with no args to get all memories, or filter by category.

Categories: schema, pattern, error, data, query

Tips:
- Call this at the start of an investigation to see what you already know
- Use category="schema" to recall table structures and relationships
- Use category="query" to recall useful SQL patterns`,
    Params: []agent.ToolParam{
        {Name: "category", Type: "string", Required: false},
        {Name: "query", Type: "string", Required: false},
    },
}
```

**`memory_write`** — Save a new fact after discovering something useful:
```go
{
    Name:        "memory_write",
    Description: `Save a fact to the system knowledge base for future investigations.
Use this when you discover something useful that would help in future queries.

Categories: schema, pattern, error, data, query

Examples:
- category="schema", content="users.status is always one of: active, suspended, deleted"
- category="pattern", content="To find failed payments, join orders with payments on order_id"
- category="error", content="The auth-service logs 'token expired' errors when JWT TTL is exceeded"`,
    Params: []agent.ToolParam{
        {Name: "category", Type: "string", Required: true},
        {Name: "content", Type: "string", Required: true},
    },
}
```

### Where Memory Tools Live

Memory tools are not tied to a specific connector — they're system-level. Two approaches:

**Option A: SystemConnector** — A new connector type that's always active, providing `memory_read` and `memory_write` tools. Registered automatically on startup, not user-managed.

**Option B: Built into the agent** — Memory tools are added directly in the investigate handler alongside connector tools. No new connector type needed.

**Recommended: Option A** — Keeps the pattern consistent (all tools come from connectors) and is cleaner to manage.

```go
type SystemConnector struct {
    memoryStore store.MemoryStore
}

func (c *SystemConnector) Type() ConnectorType { return ConnectorSystem }
func (c *SystemConnector) Tools() []agent.Tool {
    return []agent.Tool{memoryReadTool, memoryWriteTool}
}
```

Register automatically in `main.go` or `server.go` on startup — no user action required.

### Prompt Changes

**File: `internal/agent/prompt.go`**

Add to the Strategy section:
```
- At the start of an investigation, check memory_read to see what you already know about the database or common patterns.
- After discovering useful facts (table relationships, data patterns, common errors), save them with memory_write so future investigations benefit.
- Don't save trivial facts or raw query results — save insights and patterns.
```

### Memory Lifecycle
- **Auto-populated**: The agent calls `memory_write` during investigations when it discovers useful facts
- **Deduplication**: `AddMemory` upserts on content match so the same fact isn't stored twice
- **No auto-expiry**: Facts persist until explicitly deleted (schema changes are handled by the agent overwriting with updated info)
- **Manual cleanup**: Admin can delete wrong/outdated memories via API

### Memory API Endpoints

**File: `internal/web/memory.go`** (new)

```
GET    /api/memory              — list all memories (with optional ?category= filter)
DELETE /api/memory/{id}         — delete a memory entry
```

Wire in `server.go`. This lets users see what the agent has learned and clean up wrong facts.

### Tests

**New file: `internal/store/memory_store_test.go`** (integration, skip in short mode):
- AddMemory + ListMemories round-trip
- AddMemory deduplication (same content updates `updated_at`)
- SearchMemories full-text search
- ListMemories filters by category
- DeleteMemory removes entry

**New file: `internal/web/memory_test.go`** (unit, with mock store):
- GET /api/memory returns list
- GET /api/memory?category=schema filters
- DELETE /api/memory/{id} deletes

**Update: `internal/web/mock_test.go`**:
- Add `mockMemoryStore` implementing `store.MemoryStore`

---

## Implementation Order

| Phase | Improvement | Effort | Impact |
|-------|------------|--------|--------|
| 1 | Schema Cache (5-min TTL) | Small | High — eliminates redundant DB calls within a single investigation |
| 2 | Enhanced Schema Metadata | Medium | High — agent writes correct JOINs and understands data |
| 3 | Better Tool Descriptions | Small | Medium — agent uses tools more effectively |
| 4 | Chat Persistence (chats + messages) | Large | High — eliminates redundant schema discovery across conversations, enables multi-turn |
| 5 | System Memory (cross-conversation KB) | Medium | High — agent gets smarter over time, reduces repeat discovery |

Phases 1-3 are independent and can be done in any order. Phase 4 is the largest change but has the biggest long-term impact. Phases 1+3 together make sense as a quick win. Phase 2 can follow. Phase 4 next since it touches the most files. Phase 5 builds on phase 4 (shares the same migration, uses the same connector/tool pattern) and should come last.

---

## Files Changed Summary

| File | Changes |
|------|---------|
| `internal/connector/database.go` | Schema cache, enhanced metadata, better descriptions |
| `internal/connector/logs.go` | Better description, expose environment/time params |
| `internal/agent/prompt.go` | Updated strategy section (cache, memory, conversation context) |
| `internal/agent/loop.go` | Accept history param in RunWithCallback |
| `internal/store/models.go` | Add Chat, Message, MemoryEntry models |
| `internal/store/store.go` | Add ChatStore and MemoryStore interfaces |
| `internal/web/server.go` | Add chatStore + memoryStore fields, update NewServer, wire routes |
| `internal/web/investigate.go` | Chat-aware investigate handler |
| `internal/connector/connector.go` | Add `ConnectorSystem` type |

## New Files

| File | Purpose |
|------|---------|
| `migrations/000003_add_chats.up.sql` | Create chats, messages, and agent_memory tables |
| `migrations/000003_add_chats.down.sql` | Drop chats, messages, and agent_memory tables |
| `internal/store/chat_store.go` | ChatStore PostgreSQL implementation |
| `internal/store/memory_store.go` | MemoryStore PostgreSQL implementation |
| `internal/connector/system.go` | SystemConnector with memory_read/memory_write tools |
| `internal/web/chat.go` | Chat REST API handlers |
| `internal/web/memory.go` | Memory REST API handlers |

## New Test Files

| File | Tests |
|------|-------|
| `internal/connector/database_cache_test.go` | Cache TTL, cache hit/miss, per-table caching |
| `internal/connector/database_schema_test.go` | FK output, row counts, sample values (integration) |
| `internal/store/chat_store_test.go` | CRUD operations for chats and messages (integration) |
| `internal/store/memory_store_test.go` | CRUD, dedup, full-text search for memories (integration) |
| `internal/web/chat_test.go` | Chat API endpoint tests (unit with mocks) |
| `internal/web/memory_test.go` | Memory API endpoint tests (unit with mocks) |
