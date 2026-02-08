# OpenTrace -- MVP Engineering Brief

**Objective**
Build a self-hosted, connector-based AI debugging engine in Go. Users plug in their production environment (logs, database, codebase), connect Ollama, and chat to debug. Determinism, traceability, and trust come before intelligence.

> **Guiding Principle:** This is a **connector-based debugging engine**, not a chatbot. Users configure data source connectors to their production environment, and the agent dynamically assembles tools from active connectors.

> **MVP Scope:** Logs + Database + Codebase connectors, Ollama only. Monitoring connector and Anthropic/OpenAI providers are designed but deferred to post-MVP.

---

## 1. Core Architecture Principles

* **Stdlib-first Go**: Use `net/http` + `chi`. No heavy frameworks.
* **Explicit Agent Loop**: A simple `for` loop + `switch` statement. No LangChain or planners.
* **Two Separate Databases**:
  * **App DB** (`OPENTRACE_APP_DATABASE_URL`): OpenTrace's own Postgres -- stores investigations, traces, logs, embeddings, connector configs. Migrations run here.
  * **Target DB** (user-configured at runtime via UI/API): User's production Postgres, connected read-only. Stored in `data_sources` table. Separate `pgx.Pool` with `default_transaction_read_only=on` and `statement_timeout`.
* **Connector-Based Tool Discovery**: A `DataSource` interface + `Registry` pattern. Each connector type implements the interface and exposes agent tools. The agent loop calls `registry.AllTools()` -- only tools for active connectors appear in the prompt.
* **Deterministic Safety**:
  * LLM output is treated as *untrusted input*.
  * SQL is parsed and validated before execution.
  * PromQL queries are parsed and bounded. *(post-MVP)*
* **SSE-First UI**:
  * Stream `thinking -> tool_call -> observation -> final` events.
  * Server-Sent Events (SSE) + HTMX only.
* **Inspectability over Abstraction**:
  * If behavior is ambiguous, prefer explicit code.

---

## 2. Tech Stack

* **Runtime**: Go 1.24+
* **HTTP Router**: `github.com/go-chi/chi/v5`
* **App Database**: PostgreSQL + `pgvector` (OpenTrace's own data)
* **Target Database**: User's production PostgreSQL (read-only, user-configured at runtime)
* **Driver**: `github.com/jackc/pgx/v5`
* **SQL Parsing**: `github.com/pganalyze/pg_query_go/v5`
* **LLM Provider (MVP)**: Ollama (local) -- no API key required, raw HTTP (`http.Client`), no SDKs
* **LLM Providers (post-MVP)**: Anthropic (Claude), OpenAI (GPT-4) -- interfaces designed now, implemented after loop is proven
* **Embedding**: Ollama `nomic-embed-text` (768d) as default
* **Frontend**: Go `html/template` + HTMX + Tailwind CSS
* **Migrations**: `golang-migrate/migrate` (numbered SQL files in `migrations/`)
* **Deployment**: Docker Compose

---

## 3. Data Source Connectors

Connectors are the first-class concept in OpenTrace. Each connector type implements the `DataSource` interface and exposes agent tools.

```go
// internal/connector/connector.go
type ConnectorType string

const (
    ConnectorLogs       ConnectorType = "logs"
    ConnectorDatabase   ConnectorType = "database"
    ConnectorCodebase   ConnectorType = "codebase"
    ConnectorMonitoring ConnectorType = "monitoring"
)

type DataSource interface {
    Type() ConnectorType
    TestConnection(ctx context.Context) error
    Tools() []agent.Tool
    Close() error
}
```

### Connector Types

| Type | User Provides | Agent Tools |
|---|---|---|
| **Logs** | (push via HTTP) | `log_search` |
| **Database** | PG connection string | `db_search` |
| **Codebase** | Local repo path | `code_search` |
| **Monitoring** *(post-MVP)* | Prometheus URL | `monitoring_query`, `monitoring_targets`, `monitoring_metric_names` |

### Registry Pattern

The `Registry` aggregates tools from all active connectors. The agent loop calls `registry.AllTools()` to get the current tool set. When connectors are added or removed, the registry updates dynamically.

### Connector Management API

```
POST   /api/connectors           -- create data source
GET    /api/connectors           -- list all sources
POST   /api/connectors/{id}/test -- test connectivity
DELETE /api/connectors/{id}      -- remove source
```

MVP constraint: one active connector per type (enforced via unique partial index on `data_sources`).

---

## 4. Data Model

```sql
-- Connector type enum
CREATE TYPE connector_type AS ENUM ('logs', 'database', 'codebase', 'monitoring');

-- Connector status enum
CREATE TYPE connector_status AS ENUM ('connected', 'disconnected', 'error');

-- Data Sources (user-configured connections)
CREATE TABLE data_sources (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    type connector_type NOT NULL,
    name TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}',
    status connector_status NOT NULL DEFAULT 'disconnected',
    status_message TEXT,
    last_tested_at TIMESTAMP,
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);
-- One active connector per type (MVP constraint)
CREATE UNIQUE INDEX idx_data_sources_type ON data_sources (type) WHERE status = 'connected';

-- Investigation Metadata
CREATE TYPE investigation_status AS ENUM ('active', 'completed', 'error');

CREATE TABLE investigations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    query TEXT NOT NULL,
    status investigation_status NOT NULL DEFAULT 'active',
    created_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP
);

-- Trace Step Types
CREATE TYPE trace_step AS ENUM (
    'thinking',
    'tool_call',
    'observation',
    'final',
    'error'
);

-- Investigation Trace (Decision Log)
CREATE TABLE traces (
    id SERIAL PRIMARY KEY,
    investigation_id UUID REFERENCES investigations(id),
    step_type trace_step NOT NULL,
    tool_name TEXT,
    content TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- Ingested Logs
CREATE TABLE logs (
    id SERIAL PRIMARY KEY,
    timestamp TIMESTAMP NOT NULL,
    level TEXT NOT NULL,
    service TEXT,
    trace_id TEXT,
    message TEXT NOT NULL,
    metadata JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);
CREATE INDEX idx_logs_timestamp ON logs (timestamp);
CREATE INDEX idx_logs_level ON logs (level);
CREATE INDEX idx_logs_service ON logs (service);
CREATE INDEX idx_logs_trace_id ON logs (trace_id);
CREATE INDEX idx_logs_message_fts ON logs USING gin(to_tsvector('english', message));

-- Code Embeddings
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE code_embeddings (
    id SERIAL PRIMARY KEY,
    file_path TEXT NOT NULL,
    chunk_index INTEGER NOT NULL DEFAULT 0,
    content TEXT NOT NULL,
    embedding vector(768)
);
CREATE INDEX idx_code_embeddings_vector ON code_embeddings USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100);

-- App Config (key-value store, supplements env vars)
CREATE TABLE app_config (
    key TEXT PRIMARY KEY,
    value JSONB NOT NULL,
    updated_at TIMESTAMP DEFAULT NOW()
);
```

**Note on embedding dimensions:** The vector size depends on the embedding provider:
* Ollama `nomic-embed-text`: 768d (default)
* Ollama `mxbai-embed-large`: 1024d
* OpenAI `text-embedding-3-small`: 1536d
* Anthropic (via Voyage): 1024d

The default is 768d for `nomic-embed-text`. The dimension is set at table creation via migration.

---

## 5. LLM Provider Interface (`internal/llm/provider.go`)

```go
type ChatMessage struct {
    Role    string `json:"role"`    // "system", "user", "assistant"
    Content string `json:"content"`
}

type ChatRequest struct {
    Messages    []ChatMessage `json:"messages"`
    JSONMode    bool          `json:"json_mode"`
    MaxTokens   int           `json:"max_tokens,omitempty"`
}

type ChatResponse struct {
    Content string `json:"content"`
}

type LLMProvider interface {
    ChatCompletion(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

type EmbeddingProvider interface {
    Embed(ctx context.Context, text string) ([]float64, error)
    Dimension() int
}
```

MVP Implementation:
* `OllamaProvider` -- calls `POST /api/chat` and `POST /api/embeddings`

Post-MVP Implementations (interfaces ready, implement after loop is proven):
* `AnthropicProvider` -- calls Anthropic Messages API
* `OpenAIProvider` -- calls OpenAI Chat Completions and Embeddings APIs

Each provider is ~100-150 lines. No SDKs -- raw `http.Client` + JSON marshal/unmarshal.

---

## 6. Agent Architecture

### A. Tool Definition

```go
type ToolParam struct {
    Name     string `json:"name"`
    Type     string `json:"type"` // "string", "int", "bool"
    Required bool   `json:"required"`
}

type Tool struct {
    Name        string
    Description string
    Params      []ToolParam
    Handler     func(ctx context.Context, args map[string]any) (string, error)
}
```

**Tool argument validation:** Before calling `Handler`, the agent loop validates that all required params are present and types match the schema. Malformed tool calls from the LLM are logged as errors and do not burn a tool budget slot.

### B. Agent Loop (`internal/agent/loop.go`)

```go
const maxSteps = 12
const maxToolCalls = 8
const maxObservationBytes = 8192 // truncate tool output beyond this

for step := 0; step < maxSteps; step++ {
    // 1. Build dynamic prompt (system + history + tools from registry + user query)
    // 2. Call LLM provider in JSON mode
    // 3. Parse JSON response (with repair on malformed output)

    // JSON repair: if parsing fails, attempt to extract valid JSON
    // from the response. If repair fails, log error and retry once
    // (does not count as a tool call).

    switch response.Type {
    case "final_answer":
        // write final trace
        return response.Content

    case "tool_call":
        // validate tool args against ToolParam schema
        // enforce tool budget
        // execute tool
        // truncate observation if > maxObservationBytes
        // log tool_call + observation
        // append to history
        continue

    default:
        // log error and abort
    }
}

return error("investigation exceeded max steps")
```

**Rules:**

* All tool calls must be logged.
* All LLM outputs must be persisted to `traces`.
* Tool execution is cancellable via `context.Context`.
* **Observation size limit:** Tool outputs exceeding `maxObservationBytes` are truncated with a `[truncated]` marker. This prevents blowing up the LLM context window.
* **JSON repair:** Malformed LLM JSON gets one retry. If the retry also fails, the step is logged as an error and the loop continues.
* **Dynamic tool registration:** The agent loop calls `registry.AllTools()` before each LLM call. Only tools from active connectors are included in the prompt.

### C. Dynamic System Prompt (`internal/agent/prompt.go`)

The system prompt is built dynamically based on active connectors:
* Base prompt with investigation guidelines
* Tool descriptions only for active connectors
* Context about what data sources are available

---

## 7. Guardrails

### SQL Safety (`internal/guardrail/sql.go`)

```go
// Uses real AST parsing to enforce SELECT-only queries
// Library: github.com/pganalyze/pg_query_go/v5
func ValidateReadOnly(query string) error {
    // 1. Parse query to AST
    // 2. Walk AST nodes
    // 3. Reject any non-SelectStmt
    return nil
}
```

**Non-negotiable:** No regex. No string checks.

Additionally, the Target DB pool is configured with:
* `default_transaction_read_only=on` at the connection level
* `statement_timeout` set via `OPENTRACE_STATEMENT_TIMEOUT_MS`

### PromQL Safety (`internal/guardrail/promql.go`) *(post-MVP)*

```go
func ValidatePromQL(query string, maxRangeHours int) error {
    // 1. Parse PromQL query
    // 2. Enforce max time range (default 24h)
    // 3. Limit response size
    return nil
}
```

Prevents the agent from querying unbounded time ranges that could overwhelm Prometheus. Implemented alongside the Monitoring connector post-MVP.

---

## 8. Connector Tools

### Tool 1: Log Search (`log_search`)

* Full-text search via `to_tsquery` on the `logs` table in the App DB
* Time-bounded keyword search
* Filter by service / level / trace_id
* Return raw log evidence
* Always active (logs are ingested into App DB via HTTP)

### Tool 2: Database Search (`db_search`)

* Read-only SQL execution on the **Target DB** via `pgx`
* Validated by SQL AST guardrail
* Row limits enforced (`OPENTRACE_MAX_QUERY_ROWS`)
* Schema introspection available to the agent
* Only active when a Database connector is configured

### Tool 3: Code Search (`code_search`)

* Embed local repository files (chunked, with `chunk_index`)
* `pgvector` IVFFlat similarity search on the App DB
* Return file path + chunk content
* Only active when a Codebase connector is configured

### Tool 4: Monitoring Query (`monitoring_query`) *(post-MVP)*

* Execute PromQL queries against user's Prometheus
* PromQL guardrail enforces max time range
* Only active when a Monitoring connector is configured

### Tool 5: Monitoring Targets (`monitoring_targets`) *(post-MVP)*

* Discover what targets Prometheus is monitoring
* Helps the agent understand the infrastructure
* Only active when a Monitoring connector is configured

### Tool 6: Monitoring Metric Names (`monitoring_metric_names`) *(post-MVP)*

* Discover available metric names in Prometheus
* Helps the agent construct valid PromQL queries
* Only active when a Monitoring connector is configured

---

## 9. Log Ingestion (`internal/ingest/logs.go`)

* **Input**: JSON lines over HTTP (`POST /api/logs`), append-only
* **Processing**:
  * Batch insert into the App DB `logs` table
  * Index timestamp, level, service, trace_id
  * Full-text search index on message
* **Embeddings**:
  * Only embed logs with level `ERROR` or `CRITICAL`
  * Skip info/debug logs

---

## 10. VM Monitoring via Prometheus *(post-MVP)*

**Why Prometheus:** Already deployed in most production environments, HTTP-based (no SSH/agents to install), inherently read-only, standardized metric names (`node_exporter`).

Three tools exposed by the Monitoring connector:
* `monitoring_targets` -- discover what's being monitored (`/api/v1/targets`)
* `monitoring_metric_names` -- discover available metrics (`/api/v1/label/__name__/values`)
* `monitoring_query` -- execute PromQL queries (`/api/v1/query_range`)

The Prometheus connector stores the Prometheus URL in `data_sources.config` as `{"prometheus_url": "http://..."}`.

**Note:** The `ConnectorMonitoring` type is included in the enum and schema so no migration is needed when this is implemented. The `prometheus.go` connector and `promql.go` guardrail are deferred.

---

## 11. UI Contract

* **Connector Management Page** (`/connectors`): Configure, test, and remove data source connectors
* **Investigation Page** (`/investigate`): Query input + SSE trace stream
* SSE stream of trace events
* Frontend renders steps progressively:
  * Thinking
  * Tool Call
  * Observation
  * Final Answer

No WebSockets. No SPA.

---

## 12. Project Structure

```
opentrace/
├── cmd/opentrace/main.go
├── internal/
│   ├── config/config.go              -- env var parsing
│   ├── store/                        -- App DB access (investigations, traces, logs, embeddings, data_sources)
│   ├── connector/
│   │   ├── connector.go              -- DataSource interface, types
│   │   ├── registry.go               -- tool aggregation from active connectors
│   │   ├── logs.go                   -- logs connector (always active)
│   │   ├── database.go               -- target DB connector (user's PG)
│   │   └── codebase.go               -- codebase connector (embeddings)
│   │   # prometheus.go              -- Prometheus connector (post-MVP)
│   ├── agent/
│   │   ├── loop.go                   -- agent loop (pulls tools from registry)
│   │   ├── tool.go                   -- Tool, ToolParam types
│   │   ├── prompt.go                 -- dynamic system prompt builder
│   │   └── json.go                   -- JSON parsing + repair
│   ├── llm/
│   │   ├── provider.go               -- LLMProvider, EmbeddingProvider interfaces
│   │   └── ollama.go
│   │   # anthropic.go              -- post-MVP
│   │   # openai.go                 -- post-MVP
│   ├── guardrail/
│   │   └── sql.go                    -- SQL AST validation (pg_query_go)
│   │   # promql.go                 -- PromQL validation (post-MVP)
│   ├── ingest/logs.go                -- log ingestion HTTP handler
│   └── web/
│       ├── handler.go, sse.go, connectors.go
│       └── templates/
├── migrations/                        -- numbered SQL files
├── docker-compose.yml
├── Dockerfile
└── go.mod
```

---

## 13. Environment Variables

```bash
# Required
OPENTRACE_APP_DATABASE_URL=postgres://opentrace:opentrace@localhost:5432/opentrace

# LLM (Ollama only for MVP)
OPENTRACE_LLM_PROVIDER=ollama
OPENTRACE_OLLAMA_URL=http://localhost:11434
# Post-MVP:
# OPENTRACE_ANTHROPIC_API_KEY=
# OPENTRACE_OPENAI_API_KEY=

# Embedding
OPENTRACE_EMBEDDING_PROVIDER=ollama
OPENTRACE_EMBEDDING_MODEL=nomic-embed-text

# Server
OPENTRACE_LISTEN_ADDR=:8080

# Guardrails
OPENTRACE_MAX_QUERY_ROWS=500
# OPENTRACE_MAX_PROM_RANGE_HOURS=24   # post-MVP (Monitoring connector)
OPENTRACE_STATEMENT_TIMEOUT_MS=5000
OPENTRACE_MAX_AGENT_STEPS=12
OPENTRACE_MAX_TOOL_CALLS=8
OPENTRACE_MAX_OBSERVATION_BYTES=8192
```

**Important:** Target DB URL and repo path are **not** env vars -- they are user-configured at runtime via UI/API, stored in the `data_sources` table. *(Prometheus URL same pattern, post-MVP.)*

---

## 14. Development Milestones

### Phase 1 -- Infrastructure & Connectors

1. ✅ Docker Compose (App DB with pgvector, Ollama)
2. ✅ Chi server + health check
3. ✅ Migrations (`golang-migrate`) with full schema
4. ✅ Config struct + env parsing
5. ✅ `data_sources` CRUD (store layer)
6. ✅ Connector interface + Registry
7. ✅ Connector management API endpoints
8. ✅ SSE endpoint streaming dummy events

### Phase 2 -- LLM Provider Layer

1. ✅ `LLMProvider` + `EmbeddingProvider` interfaces
2. ✅ Ollama provider (chat + embeddings)
3. ✅ Provider selection via config

### Phase 3 -- Agent Core

1. Agent loop with echo tool
2. JSON-mode responses with repair/retry
3. Tool argument validation
4. Observation truncation
5. Dynamic tool registration from connector registry
6. Dynamic system prompt builder

### Phase 4 -- Connectors & Tools

**4a. Logs:**
* Ingestion endpoint (`POST /api/logs`)
* Batch insert into App DB
* Log Search tool (FTS via `to_tsquery`)

**4b. Database:**
* Target DB pool with read-only enforcement
* SQL AST guardrail (`pg_query_go`)
* DB Search tool
* Schema introspection

**4c. Codebase:**
* File walker + chunker
* Embedding pipeline (via `EmbeddingProvider`)
* Code Search tool (`pgvector` similarity)

### Phase 5 -- UI

1. Layout (Tailwind + HTMX)
2. Connector management page (configure/test/remove sources)
3. Investigation page (query + SSE trace stream)
4. Trace rendering (thinking -> tool_call -> observation -> final)

### Phase 6 -- Integration & Polish

1. End-to-end investigation flow
2. Agent prompt tuning
3. Error handling / graceful degradation
4. Docker Compose with all services
5. README

### Post-MVP -- Deferred (designed, not implemented)

* **Anthropic provider** (`anthropic.go`) -- Anthropic Messages API
* **OpenAI provider** (`openai.go`) -- OpenAI Chat Completions + Embeddings APIs
* **Monitoring connector** (`prometheus.go`) -- Prometheus HTTP client, `monitoring_query`, `monitoring_targets`, `monitoring_metric_names` tools
* **PromQL guardrail** (`promql.go`) -- max time range, response size validation

---

## 15. Non-Goals (Explicitly Out of Scope)

* Multi-tenancy
* Auth / RBAC
* Multiple DB engines (only Postgres for Target DB)
* Anomaly detection
* SaaS / cloud hosting
* SSH-based server monitoring (Prometheus only)

---

## Final Rule

> **If you cannot print the agent's full reasoning and understand it, the implementation is wrong.**

Build boring. Build explicit. Build something you trust during a production incident.
