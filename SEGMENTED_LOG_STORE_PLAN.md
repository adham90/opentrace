# Segmented Log Store: Design Plan

**Status**: Design Complete — Ready for Implementation
**Goal**: Replace SQLite-backed log storage with a custom time-segmented append-only store with built-in columnar storage and search. Eliminates 7 SQLite tables (logs, request_summaries, and 5 deep capture tables) with a single unified format.
**Target environment**: Self-hosted, $4/month VM (512MB–1GB RAM, ~25GB disk)
**Dependencies**: Only `github.com/klauspost/compress/zstd`

---

## 1. Motivation

### Current SQLite Problems
- **7 tables** for one log document (logs + request_summaries + 5 capture tables)
- **15 B-tree indexes** maintained on every insert (write amplification)
- **FTS5 triggers** fire synchronously on every insert
- **7 INSERTs per rich request** (shredding one document into 7 tables)
- **JOINs to reassemble** — `SearchRequestSummaries` joins logs + request_summaries; `errors_investigate` queries 6 tables
- **Pruning** requires DELETE from 7 tables + VACUUM (locks the DB, minutes)
- **No compression** — raw TEXT storage
- **Monolithic file** — can't drop old data cheaply

### What We Replace

| Current (SQLite) | New (Segmented Store) |
|---|---|
| `logs` table (15 indexes, FTS5) | Columnar columns |
| `request_summaries` table | Performance columns (duration_ms, db_ms, etc.) |
| `request_captures` table | `body` column (JSON blob) |
| `sql_captures` table | `body` column |
| `http_captures` table | `body` column |
| `email_captures` table | `body` column |
| `audit_captures` table | `body` column |
| `file_captures` table | `body` column |
| `ingest_batches` table | Not needed (append file is crash-safe) |
| `logs_fts` virtual table | Custom inverted index |

**7 tables + FTS → 1 columnar file + 1 inverted index per chunk.**

---

## 2. Log Schema (SDK Wire Format)

The SDK sends flat JSON. Every top-level key maps directly to a columnar column. The `body` key is a single opaque blob stored compressed.

### Simple Log (no body — ~70% of traffic)

```json
{
  "ts": "2026-04-04T12:41:00.123Z",
  "level": "error",
  "service": "billing-api",
  "env": "production",
  "version": "a1b2c3d",
  "message": "Failed to charge customer: card declined",
  "trace_id": "trace-xyz789",
  "request_id": "req-abc123",
  "user_id": "42",
  "tenant_id": "7"
}
```
~200 bytes. No `body`. No `event_type`. No performance fields.

### Structured Log With Context (~20% of traffic)

```json
{
  "ts": "2026-04-04T12:41:00.200Z",
  "level": "warn",
  "service": "billing-api",
  "env": "production",
  "version": "a1b2c3d",
  "message": "Slow query detected: 287ms on order_items",
  "event_type": "db.slow_query",
  "trace_id": "trace-xyz789",
  "request_id": "req-abc123",
  "user_id": "42",
  "tenant_id": "7"
}
```
~250 bytes. Still no `body` — just more context fields.

### Rich Request Document (~10% of traffic)

```json
{
  "ts": "2026-04-04T12:41:00.456Z",
  "level": "info",
  "service": "billing-api",
  "env": "production",
  "version": "a1b2c3d",
  "message": "POST /api/orders 201 1243ms",
  "event_type": "http.request",
  "trace_id": "trace-xyz789",
  "span_id": "span-001",
  "parent_span_id": null,
  "request_id": "req-abc123",
  "user_id": "42",
  "tenant_id": "7",
  "session_id": "sess-def456",
  "method": "POST",
  "path": "/api/orders",
  "status": 201,
  "duration_ms": 1243,
  "controller": "Api::OrdersController",
  "action": "create",
  "db_ms": 312,
  "db_count": 8,
  "n_plus_one": false,
  "slow_queries": 1,
  "dup_queries": 0,
  "body": {
    "context": {
      "ip": "41.33.12.5",
      "hostname": "web-01",
      "pid": 12345,
      "thread_id": "70234",
      "tenant_plan": "pro",
      "feature_flags": ["new_checkout", "tax_v2"]
    },
    "request": {
      "params": {"items": [{"product_id": 9, "qty": 2}], "coupon": "SUMMER20"},
      "headers": {"content-type": "application/json", "user-agent": "Mozilla/5.0"}
    },
    "response": {
      "body": {"order_id": 9901, "status": "confirmed", "total": 4999},
      "headers": {"content-type": "application/json"}
    },
    "performance": {
      "view_ms": 45,
      "external_ms": 780,
      "ruby_ms": 106,
      "memory_before_mb": 312.4,
      "memory_after_mb": 314.1,
      "gc_runs": 1,
      "allocated_objects": 12840
    },
    "queries": [
      {"sql": "SELECT * FROM users WHERE id = ?", "binds": [42], "duration_ms": 1.2, "rows": 1, "table": "users", "fingerprint": "a1b2c3"},
      {"sql": "INSERT INTO orders ...", "duration_ms": 4.1, "table": "orders", "fingerprint": "d4e5f6"}
    ],
    "external": [
      {"vendor": "stripe", "method": "POST", "url": "https://api.stripe.com/v1/charges", "duration_ms": 780, "status": 200}
    ],
    "emails": [
      {"mailer": "OrderMailer", "action": "confirmation", "to": "user@example.com", "subject": "Order #9901 confirmed", "delivered": true, "duration_ms": 134}
    ],
    "files": [
      {"action": "upload", "filename": "receipt.pdf", "size_bytes": 48000, "service": "s3", "duration_ms": 220}
    ],
    "audit": [
      {"action": "create", "record_type": "Order", "record_id": "9901", "actor_id": "42", "before": null, "after": {"status": "confirmed", "total": 4999}}
    ],
    "timeline": [
      {"t": "db", "n": "User Load", "ms": 1.2, "at": 0},
      {"t": "db", "n": "Price Calc", "ms": 287.3, "at": 8.0, "slow": true},
      {"t": "external", "n": "stripe charge", "ms": 780, "at": 300},
      {"t": "db", "n": "Order Insert", "ms": 4.1, "at": 1082},
      {"t": "file", "n": "upload receipt.pdf", "ms": 220, "at": 1087},
      {"t": "email", "n": "OrderMailer#confirm", "ms": 134, "at": 1090}
    ],
    "logs": [
      {"level": "info", "message": "Cache miss for user 42", "at": 10.5},
      {"level": "warn", "message": "Slow query detected", "at": 55.3}
    ]
  }
}
```
~3KB on wire. Performance metrics are flat top-level fields (searchable). Everything else is in `body` (stored as opaque compressed blob).

### SDK Schema Rules

| Rule | Rationale |
|---|---|
| Every top-level key = one columnar column | Zero extraction/transformation at ingest |
| `body` is opaque to the **storage engine** | Stored compressed as-is, loaded on demand |
| All IDs are strings (`user_id: "42"`) | Consistent type, no int/string confusion |
| Durations are integers (milliseconds) | Compact, no float precision issues |
| `id` is NOT in the SDK payload | Server assigns composite int64 at write time |
| Simple logs omit absent fields entirely | Sparse columns: null = 1 bit |
| Error fields NOT in SDK payload | Server extracts from `body` at ingest (SDK stays thin) |
| PII scrubbing NOT in SDK | Server scrubs body at ingest before storage |

### Server-Extracted Fields (computed at ingest, not sent by SDK)

These fields are extracted from `body` by the ingest pipeline and stored as top-level columns. The SDK never sends them — keeping the SDK thin and fast.

| Field | Source | How extracted |
|---|---|---|
| `id` | — | Server assigns composite int64 |
| `received_at` | — | Server clock at receive time |
| `exception_class` | `body.exception.class` | Parsed from body if level=error/fatal |
| `source_file` | `body.exception.file` | Parsed from body if level=error/fatal |
| `source_line` | `body.exception.line` | Parsed from body if level=error/fatal |
| `error_fingerprint` | Computed | `hash(exception_class + source_file + source_line)` |

---

## 3. Architecture

```
SDK (Ruby/JS/Python) — thin: just serialize and send
  │
  │  POST /ingest  [flat JSON entries]
  │
  ▼
Server Ingest Pipeline
  │
  │  1. Parse JSON
  │  2. Sampling (drop X% of debug logs, keep all errors)
  │  3. For entries with body:
  │     a. PII scrub body (credit cards, emails, SSNs, sensitive fields)
  │     b. Extract error fields (exception_class, source_file, source_line)
  │     c. Compute error_fingerprint = hash(class + file + line)
  │     d. Expand body.logs → separate entries (linked by trace_id/request_id)
  │  4. Assign composite int64 IDs (atomic counter + bit math)
  │  5. Set received_at = server clock
  │  6. Serialize to compact binary
  │  7. Append to active.wal (fsync)
  │  8. Copy to ring buffer (for tail)
  │  9. Fan out to tail subscribers
  │
  │  Steps 3a-3d only run for ~10% of entries (those with body).
  │  Simple logs skip straight to step 4.
  │
  │  Every hour (background goroutine):
  ▼
Seal process (2 passes)
  │
  ├─ Pass 1: Stats + histogram scan → column stats, per-minute counts
  ├─ Pass 2: Write 32 columns → chunk_000.col
  │          Build inverted index → chunk_000.idx
  │          Write meta.json
  └─ Delete sealing_T12.wal
  │
  ▼
Sealed segment on disk (immutable)
  data/logs/2026-04-04T12/
    chunk_000.col    ~3-5MB  (32 columns, compressed)
    chunk_000.idx    ~1MB    (inverted index for FTS)
    meta.json        ~2KB    (stats, histograms)
```

### Hybrid Storage
- **SQLite (keep)**: users, sessions, servers, config, watches, error_groups, trace_status, metrics — relational/mutable data
- **Segmented Store (new)**: ALL log data — simple logs, rich requests, deep captures — unified in one format

### Search Strategy
- **Structured queries** (level, service, trace_id, etc.) → read columnar columns, filter in Go
- **Full-text search** (message contains "timeout") → custom inverted index per chunk
- **Active hour queries** → linear scan of append file
- **Body details** → loaded on demand for specific entries (GetByID, errors_investigate)

---

## 4. Resolved Design Questions

### Q1: Segment Time Window — 1 Hour
- Matches default MCP query window (`time_range: "1h"`)
- ~720 segments at 30d retention
- Most queries touch exactly 1 sealed segment + active hour

### Q2: Write Buffer — Append-Only File + Ring Buffer
- Write: serialize to compact binary → append to `active.wal` → fsync per batch
- Tail: ring buffer (256 entries, ~256KB) + pub/sub channels
- Active hour queries: linear scan of WAL (fast enough for rare reads)
- Memory: ~260KB total
- Crash safety: the file IS the WAL; fsync after each BatchInsert

### Q3: Data Format — Custom Columnar + Zstd
- 32 columns per chunk (31 searchable + 1 body blob)
- 6 encoding types (dictionary, sparse, zstd block, delta+varint, bitpack, varint)
- Chunked at 50,000 entries or 25MB (whichever first)
- Streaming seal: 3-5MB peak memory
- Zero dependencies beyond zstd

### Q4: Search — Go Column Filtering + Custom Inverted Index
- No Bleve (saves ~20-30GB of index storage)
- Structured filters: decode dictionary/sparse columns, filter in Go
- FTS: custom term→row_ids inverted index, built at seal time, ~1-2MB per chunk
- Zero external dependencies

### Q5: ID Scheme — Composite int64
- `segment_hour(32 bits) + chunk(8 bits) + row(23 bits)` = int64
- O(1) GetByID: decode bits → exact file → exact row
- Time-sortable: SinceID works with `>`
- Lock-free: `atomic.AddInt64` + bit math
- Crash recovery: replay WAL, count entries, IDs recompute

### Q6: Request Summaries — Eliminated as Separate Concept
- Performance metrics (`duration_ms`, `db_ms`, `db_count`, `n_plus_one`, etc.) are flat top-level fields → stored as sparse columnar columns
- `SearchRequestSummaries` → read sparse `duration_ms` column, sort, filter
- `AggregateRequestSummaries` → read sparse columns, compute AVG/SUM in Go
- Full request/response details live in `body` column, loaded on demand
- **No separate table. No JOINs.**

### Q7: Deep Captures — Eliminated as Separate Tables
- SQL queries, external API calls, emails, files, audit entries, timeline — all inside `body`
- `body` is a single sparse column: null for simple logs (1 bit), compressed JSON for rich requests
- Loaded only when full details are requested (GetByID, errors_investigate)
- **No separate capture tables. No shredding. No reassembly.**

### Q8: Log Schema — Flat SDK Wire Format
- Every top-level field = one columnar column (zero transformation at ingest)
- `body` field = opaque compressed blob (server never parses it)
- Server assigns composite int64 ID (not in SDK payload)
- Simple logs: ~200 bytes, no body. Rich requests: ~3KB with body.

---

## 5. Column Schema

### Column → Encoding Assignment (32 columns)

| # | Column | Encoding | Null% | Why |
|---|--------|----------|-------|-----|
| 1 | `id` | Delta + varint | 0% | Sequential, monotonic. O(1) lookup. |
| 2 | `ts` | Zstd block (int64 epoch ms) | 0% | Event time from SDK. User-facing, used for time-range filtering. Not delta-encoded (may not be monotonic). |
| 3 | `received_at` | Delta + varint | 0% | Server receive time. Guaranteed monotonic. Used for segment assignment + delta encoding. |
| 4 | `level` | Dictionary + bitpack | 0% | ≤6 values (trace/debug/info/warn/error/fatal). 3 bits per entry. |
| 5 | `service` | Dictionary + zstd | 0% | ~5-20 unique values. |
| 6 | `env` | Dictionary + zstd | ~5% | 2-3 unique values (production/staging/dev). |
| 7 | `version` | Sparse + dictionary + zstd | ~30% | Few unique commit hashes per hour. |
| 8 | `message` | Zstd block | 0% | Variable-length text. Also feeds inverted index. |
| 9 | `event_type` | Sparse + dictionary + zstd | ~70% | "http.request", "job.run", etc. Only on typed events. |
| 10 | `trace_id` | Sparse + zstd | ~30% | High cardinality, often present. |
| 11 | `span_id` | Sparse + zstd | ~70% | Only on traced requests. |
| 12 | `parent_span_id` | Sparse + zstd | ~85% | Only on child spans. |
| 13 | `request_id` | Sparse + zstd | ~40% | Most request-scoped logs have this. |
| 14 | `user_id` | Sparse + zstd | ~50% | Present when user context available. |
| 15 | `tenant_id` | Sparse + dictionary + zstd | ~50% | Few unique values. |
| 16 | `session_id` | Sparse + zstd | ~80% | Only on web requests. |
| 17 | `method` | Sparse + dictionary + zstd | ~90% | GET/POST/PUT/DELETE/PATCH. Only on HTTP events. |
| 18 | `path` | Sparse + zstd | ~90% | Only on HTTP events. |
| 19 | `status` | Sparse + dictionary + zstd | ~90% | HTTP status codes. ~20 unique values. |
| 20 | `duration_ms` | Sparse + varint + zstd | ~90% | Only on request events. Integer ms. |
| 21 | `controller` | Sparse + dictionary + zstd | ~90% | Only on HTTP events. ~50-200 unique. |
| 22 | `action` | Sparse + dictionary + zstd | ~90% | Only on HTTP events. ~50-200 unique. |
| 23 | `db_ms` | Sparse + varint + zstd | ~90% | DB time in ms. Integer. |
| 24 | `db_count` | Sparse + varint + zstd | ~90% | Number of SQL queries. |
| 25 | `n_plus_one` | Sparse (bool bitmap) | ~90% | Boolean. |
| 26 | `slow_queries` | Sparse + varint + zstd | ~90% | Count of slow queries. |
| 27 | `dup_queries` | Sparse + varint + zstd | ~90% | Count of duplicate queries. |
| 28 | `exception_class` | Sparse + dictionary + zstd | ~95% | Server-extracted from `body.exception.class` at ingest. |
| 29 | `source_file` | Sparse + zstd | ~95% | Server-extracted from `body.exception.file` at ingest. |
| 30 | `source_line` | Sparse + varint + zstd | ~95% | Server-extracted from `body.exception.line` at ingest. |
| 31 | `error_fingerprint` | Sparse + zstd | ~95% | Server-computed: `hash(exception_class + source_file + source_line)`. |
| 32 | `body` | Sparse + zstd block | ~90% | Full event details. Opaque to storage engine. PII-scrubbed at ingest. |

### Encoding Types (6 total)

| Encoding | How it works | Used by |
|----------|-------------|---------|
| **Dictionary + zstd** | Map strings to uint indices, zstd compress indices | service, env, event_type, method, status, controller, action |
| **Dictionary + bitpack** | Same as dictionary but pack indices into N bits (3 bits for ≤8 values) | level |
| **Sparse + zstd** | Null bitmap (1 bit/entry) + non-null values + zstd | trace_id, span_id, request_id, user_id, path, session_id |
| **Delta + varint** | Base value + varint-encoded deltas (exploits monotonic ordering) | id, ts |
| **Varint + zstd** | Variable-length integers + zstd (small ints = 1-2 bytes) | duration_ms, db_ms, db_count, slow_queries |
| **Zstd block** | Length-prefixed variable-length values + zstd block compression | message, body |

Note: Sparse can combine with other encodings. E.g., "sparse + dictionary + zstd" = null bitmap + dictionary-encoded non-null values + zstd. "Sparse + varint + zstd" = null bitmap + varint-encoded non-null values + zstd.

### Chunk File Structure

```
chunk_000.col:

┌─────────────────────────────────────────┐
│ Header (64 bytes)                       │
│   magic: "OTCL" (OpenTrace CoLumnar)    │
│   version: 1                            │
│   entry_count: 50000                    │
│   column_count: 32                      │
│   directory_offset: → footer            │
├─────────────────────────────────────────┤
│ Column 1: id (delta + varint)           │
│ Column 2: ts (delta + varint)           │
│ Column 3: level (dict + bitpack)        │
│ Column 4: service (dict + zstd)         │
│ Column 5: env (dict + zstd)             │
│ Column 6: version (sparse+dict+zstd)    │
│ Column 7: message (zstd block)          │
│ ...                                     │
│ Column 28-31: error fields (sparse)     │
│ Column 32: body (sparse + zstd block)   │
├─────────────────────────────────────────┤
│ Column Directory (footer)               │
│   [{name, type, encoding, offset,       │
│     compressed_size, raw_size,          │
│     null_count}]                        │
│   for each of 32 columns               │
└─────────────────────────────────────────┘
```

### Inverted Index (chunk_000.idx)

```
Built at seal time from message column:

┌──────────────────────────────────┐
│ Header                           │
│   term_count, entry_count        │
├──────────────────────────────────┤
│ Term Table (sorted)              │
│   "card"     → offset 0x1200    │
│   "charge"   → offset 0x1340    │
│   "declined" → offset 0x1520    │
│   "timeout"  → offset 0x16A0    │
├──────────────────────────────────┤
│ Postings (delta-encoded + zstd)  │
│   "card":     [0, +56, +726]    │
│   "declined": [0, +782]         │
└──────────────────────────────────┘

Query "card declined":
  Lookup "card" → [0, 56, 782]
  Lookup "declined" → [0, 782]
  Intersect → [0, 782]
  Time: ~1-5ms
```

### Meta File (meta.json)

```json
{
  "segment": "2026-04-04T12",
  "chunk": 0,
  "entry_count": 50000,
  "id_range": [847291000, 847341000],
  "time_range": ["2026-04-04T12:00:00Z", "2026-04-04T12:59:59Z"],
  "counts": {
    "by_level": {"debug": 31200, "info": 14500, "warn": 2100, "error": 1800, "fatal": 400},
    "by_service": {"billing-api": 28000, "auth": 12000, "worker": 10000}
  },
  "histogram": {
    "2026-04-04T12:00": {"total": 892, "errors": 34},
    "2026-04-04T12:01": {"total": 901, "errors": 28}
  },
  "dictionaries": {
    "service": ["billing-api", "auth", "worker"],
    "level": ["debug", "info", "warn", "error", "fatal"],
    "env": ["production"],
    "event_type": ["http.request", "job.run", ""]
  }
}
```

---

## 6. Binary Append Format (active.wal)

Entries in the active WAL use compact binary, not JSON. Each top-level field is written with a flag bit indicating presence.

```
┌─ Simple log (~45 bytes) ───────────────────────┐
│ [4] entry_length = 41                           │
│ [8] ts = 1743771660123 (epoch ms)               │
│ [1] level = 4 (error)                           │
│ [4] flags = bits indicating which fields present │
│ [1+11] service = "billing-api"                  │
│ [2+42] message                                  │
│ [1+13] trace_id (flag bit set)                  │
│ [1+10] request_id (flag bit set)                │
│ [1+2]  user_id (flag bit set)                   │
│ [1+1]  tenant_id (flag bit set)                 │
│ — no body (flag bit not set) —                  │
└─────────────────────────────────────────────────┘

┌─ Rich request (~100 header + ~1800 body) ──────┐
│ [4] entry_length = 1896                         │
│ [8] ts                                          │
│ [1] level = 2 (info)                            │
│ [4] flags = all searchable fields present       │
│ [1+11] service                                  │
│ [2+35] message                                  │
│ [1+12] event_type = "http.request"              │
│ [1+13] trace_id                                 │
│ [1+8]  span_id                                  │
│ [1+10] request_id                               │
│ [1+2]  user_id                                  │
│ [1+1]  tenant_id                                │
│ [1+12] session_id                               │
│ [1+4]  method = "POST"                          │
│ [1+11] path = "/api/orders"                     │
│ [2]    status = 201                             │
│ [4]    duration_ms = 1243                       │
│ [1+22] controller                               │
│ [1+6]  action                                   │
│ [4]    db_ms = 312                              │
│ [2]    db_count = 8                             │
│ [1]    n_plus_one = 0                           │
│ [1]    slow_queries = 1                         │
│ [1]    dup_queries = 0                          │
│ [4]    body_length = 1800                       │
│ [1800] zstd_compressed(body JSON)               │
└─────────────────────────────────────────────────┘
```

---

## 7. Seal Process (Streaming, Memory-Bounded)

Runs every hour as a background goroutine.

```
Input:  data/logs/2026-04-04T12/sealing.wal  (50K entries, ~25MB)
        (renamed from active.wal at hour rotation — new writes go to fresh active.wal)

Pass 1: Stats + histogram scan                     (~500KB memory)
  Single read through sealing.wal, collect:
  - Per-column: cardinality, null count, min/max, dictionary values
  - Per-minute histogram counts (total + errors)
  - Per-level counts, per-service counts
  - Total entry count
  → All data needed for meta.json collected here (written at end)

Pass 2: Write columns + inverted index             (~3-5MB memory)
  Single read through sealing.wal:
  For each of 32 columns:
    Extract that column's values
    Encode (dictionary/sparse/delta/bitpack/zstd)
    Write to chunk_000.col
    Release encoder memory before next column
  While writing message column:
    Tokenize each message → build term→row_ids map
  After all columns written:
    Write column directory footer
    Sort terms, delta-encode postings, zstd compress → write chunk_000.idx
    Write meta.json (from pass 1 data)

Delete sealing.wal

Output:
  chunk_000.col    ~3-5MB
  chunk_000.idx    ~1MB
  meta.json        ~2KB

Total WAL reads: 2 (not 4)
Peak memory: ~3-5MB (inverted index map is heaviest)
```

---

## 8. Query Examples

### "Find error logs from billing-api in last hour"

```
logs(action="search", level="error", service="billing-api", time_range="1h")

1. Select segments: [T12 sealed, T13 active]

2. Sealed segment T12:
   Read level column (2KB bitpacked) → rows where level=error → [0,23,56,401,...]
   Read service column (12KB dict) → rows where service="billing-api" → [0,1,3,23,56,...]
   Intersect → [0,23,56,401,...]
   Read ts column for matches → filter by time range
   Read message, trace_id columns for top 50 results
   DO NOT read body column

3. Active segment T13:
   Linear scan of active.wal, filter in Go

4. Merge, sort by ts DESC, return top 50

I/O: ~15KB columns + ~50KB for result fields
Time: ~5-10ms
```

### "Search for 'card declined'"

```
logs(action="search", query="card declined", time_range="1h")

1. Sealed T12: open chunk_000.idx
   Binary search "card" → [0, 56, 782]
   Binary search "declined" → [0, 782]
   Intersect → [0, 782]
   Read columns for those 2 rows

2. Active T13: linear scan, substring match

Time: ~2ms
```

### "Show full details of request req-abc123"

```
logs(action="search", request_id="req-abc123")

1. Read request_id sparse column → find row 47231
2. Read ALL 25 searchable columns for row 47231
3. Read body column for row 47231 → decompress JSON
4. Return complete document with queries, timeline, everything

Time: ~1-2ms (O(1) via composite ID if known)
```

### "Top 10 slowest requests in last hour"

```
logs(action="performance", sort_by="duration_ms", time_range="1h")

1. Read duration_ms sparse column → all non-null values (only HTTP events)
2. Sort descending, take top 10
3. Read method, path, controller, status, db_count columns for those 10 rows
4. Optionally read body for deep details

Time: ~3-5ms
```

### "P95 response time" (watcher, every 5 min)

```
Read service column → filter "billing-api"
Read duration_ms for matching rows in last 5 min
Sort, pick 95th percentile

Time: ~0.5-2ms
```

### Pruning (daily cron)

```
rm -rf data/logs/2026-03-20T*/

Done. Instant. No SQL. No VACUUM. No locks.
```

---

## 9. File Layout

```
data/
  logs/
    2026-04-04T10/
      chunk_000.col       3.1MB
      chunk_000.idx       0.9MB
      meta.json           2KB
    2026-04-04T11/
      chunk_000.col       3.4MB
      chunk_001.col       3.2MB    ← second chunk (>50K entries this hour)
      chunk_000.idx       1.0MB
      chunk_001.idx       0.9MB
      meta.json           2KB
    2026-04-04T12/
      chunk_000.col       3.2MB
      chunk_000.idx       1.0MB
      meta.json           2KB
    2026-04-04T13/
      active.wal          12MB     ← current hour, still accumulating
```

---

## 10. Resolved Design Questions (continued)

### Q9: Metadata / Body Querying — Body is Opaque
- Body is never parsed by the storage engine
- If a field needs to be searchable, promote it to a top-level SDK field → becomes a new sparse column automatically
- Keeps the engine simple: columns are searchable, body is stored/retrieved only

### Q10: Concurrency Model
- **WAL writes**: `sync.Mutex` (single writer, fast sequential append)
- **WAL reads**: `atomic.LoadInt64(&walValidBytes)` — readers snapshot the valid length before reading; writer updates after fsync. No lock needed, just atomic read of the safe-to-read boundary.
- **Ring buffer**: `sync.RWMutex` (writer updates head; tail subscribers read)
- **Sealed segments**: **no locks** (immutable files, any goroutine reads freely)
- **Segment directory**: `sync.RWMutex` (seal adds segments; prune removes; queries read list)
- **Seal process**: operates on `sealing.wal` (rotated from active) — never competes with writes
- **Hour rotation**: rename `active.wal → sealing_T12.wal` (includes hour to prevent overwrite if previous seal is slow), create new `active.wal`. Zero write downtime. If previous seal is still running, new seal waits for it to finish before starting.
- **Query fan-out**: one goroutine per segment + one for active WAL, merge results. Parallel reads with zero locking on sealed data.

### Q11: Histogram Pre-computation — Pre-compute in meta.json
- During seal, count entries per minute (free — already scanning WAL)
- Store in meta.json: `{"2026-04-04T12:00": {"total": 892, "errors": 34}, ...}`
- Also pre-compute: `by_level` counts, `by_service` counts
- Query time: read meta.json files (~2KB each), sum integers. <1ms for 24h.
- Active hour: compute on the fly from WAL scan
- `CountByLevel`, `CountByService`, `Histogram` all answered from meta.json — no column reads needed

### Q12: Migration Strategy — Clean Swap
- Still in development — no backwards compatibility or migration needed
- Build new segmented `LogStore` implementation
- Swap it in `NewStores()` replacing `NewLogStore(db)`
- Delete old SQLite log code: `log_store.go`, FTS5 triggers, capture handlers, capture tables
- Delete SQLite tables: `logs`, `logs_fts`, `request_summaries`, `request_captures`, `sql_captures`, `http_captures`, `email_captures`, `audit_captures`, `file_captures`, `ingest_batches`
- Update `LogStore` interface to match new schema (flat fields, body blob, composite int64 ID)

---

## 11. Operational Concerns

### Crash Recovery

**Server crashes during normal operation (WAL intact):**
- On startup: scan `data/logs/` for `active.wal` files
- Replay the WAL to rebuild ring buffer and entry counter
- Resume accepting writes. No data loss.

**Server crashes during seal (partial chunk on disk):**
- On startup: if `sealing_T*.wal` exists alongside a `chunk_*.col`, delete the partial chunk files
- Re-seal from the WAL. No data loss.
- Marker: seal writes a `.seal_complete` file as the very last step. If absent, seal was incomplete.

### Schema Evolution (adding columns)

When a new SDK version sends a new top-level field (e.g., `tenant_plan`):
- New entries have the field → stored in the new column
- Old sealed chunks don't have this column
- Column reader: if column name not found in footer directory → return null for all rows
- **Three lines of code. No migration. No rewriting old data.**

### Disk Full Handling

- **WAL append fails**: `fsync` returns error → `BatchInsert` returns error to SDK → SDK retries
- **Seal can't write chunk**: seal fails, WAL preserved, retry next cycle
- **Proactive monitoring**: check disk space at startup and before each seal. Log warning if <1GB free. Skip seal if <500MB free (preserve WAL for later).
- **Emergency**: if disk is critically low, prune oldest segments immediately (even if retention hasn't expired)

### LogStore Interface Changes

Methods to **remove** (no longer applicable):
- `MetadataKeys` — metadata is inside opaque body
- `RecordBatch` / `GetBatch` / `PruneBatches` — WAL is crash-safe, no batch tracking needed
- `SearchRequestSummaries` — replaced by column filtering on duration_ms, db_count, etc.
- `AggregateRequestSummaries` — replaced by reading sparse columns + Go math

Methods to **add**:
- `Tail(ctx) (<-chan []Entry, func())` — subscribe to live entries via ring buffer
- `GetBody(ctx, id int64) (json.RawMessage, error)` — load body blob for a specific entry

Methods to **update**:
- `Search` — new `LogSearchParams` matching flat schema fields
- `GetByID` — returns flat entry + optional body
- `CountByLevel` / `CountByService` / `Histogram` — read from meta.json, not columns
- `DistinctValues` — dict-encoded columns: read from meta.json dictionaries. Sparse string columns: scan the column.

---

## 12. Implementation Plan

### Estimated Effort: ~18-20 days

| Task | Days | Details |
|------|------|---------|
| **Encodings (6 types)** | | |
| Dictionary + zstd | 1 | String→uint map, byte/uint16 indices, zstd |
| Sparse + zstd | 1 | Null bitmap, packed non-null values, zstd |
| Zstd block | 0.5 | Length-prefixed strings + zstd |
| Delta + varint | 1 | Base value + varint deltas, encode/decode |
| Bitpack | 0.5 | N-bit enum packing |
| Varint + zstd | 0.5 | Variable-length ints + zstd |
| **File format** | | |
| Column writer | 1.5 | Header, 32 columns, footer directory, schema evolution (missing column = null) |
| Column reader | 1.5 | Read footer, seek column, decompress, decode |
| **Binary WAL format** | | |
| Entry serializer | 1 | Flat fields → compact binary with flags |
| Entry deserializer | 0.5 | Binary → struct (for seal + active hour scan) |
| **Ingest pipeline** | | |
| PII scrubber | 1 | Scrub body JSON before storage (port existing `deepcapture.ScrubDocument`) |
| Error field extraction | 0.5 | Parse body.exception → exception_class, source_file, source_line, fingerprint |
| In-request log expansion | 0.5 | Expand body.logs → separate WAL entries linked by trace_id/request_id |
| Sampling | 0.5 | Port existing `ApplySamplingRules` to new entry struct |
| **Inverted index** | | |
| Tokenizer | 0.5 | Whitespace/punctuation split, lowercase |
| Index builder | 1 | Term→row_ids map, sorted terms, delta postings, zstd |
| Index reader | 0.5 | Binary search terms, decompress postings, intersect |
| **Engine** | | |
| WAL writer + ring buffer + tail | 1 | Append, fsync, pub/sub, subscribe/unsubscribe |
| Streaming seal process | 1.5 | 2-pass: stats+histogram → columns+index+meta |
| Segment manager | 0.5 | Open/close segments, load meta, prune old dirs, crash recovery |
| ID assignment | 0.5 | Composite int64, atomic counter, WAL replay on restart |
| Hour rotation | 0.5 | Atomic rename active→sealing, create new active, trigger seal goroutine |
| **Integration** | | |
| `LogStore` interface redesign | 1 | New interface matching flat schema + body. Remove deprecated methods. Add Tail/GetBody. |
| Wire all query methods | 1.5 | Search, GetByID, CountByLevel, CountByService, Histogram, DistinctValues |
| **Quality** | | |
| Testing | 2 | Each encoding round-trip, inverted index, WAL crash recovery, PII scrub, integration |
| CLI dump tool | 0.5 | `opentrace dump-segment` for debugging |

---

## 13. Performance Targets

| Metric | Current SQLite | New Segmented Store |
|--------|---------------|---------------------|
| Write throughput | ~5-10K logs/sec | ~200-500K logs/sec |
| Insert work per rich request | 7 INSERTs across 7 tables | 1 append to WAL |
| Storage (1M logs/hr, 10% rich, 14d) | ~250GB+ | **~35-55GB** |
| Seal memory | N/A | **3-5MB peak** |
| Runtime memory | ~50-100MB (page cache) | **~260KB** (WAL buffer + ring) |
| Structured search (1h) | ~50ms (indexed) | ~5-10ms (column scan) |
| Full-text search (1h) | ~50ms (FTS5) | ~2-5ms (inverted index) |
| GetByID | ~1ms (B-tree) | ~1ms (O(1) composite ID) |
| "Top 10 slowest" | ~5ms (SQL ORDER BY) | ~3-5ms (sparse column sort) |
| P95 response time | ~5ms (SQL + Go sort) | ~0.5-2ms (sparse column) |
| Pruning | DELETE 7 tables + VACUUM | `rm -rf` (instant) |
| "Show full request details" | 6 SQL queries across 6 tables | 1 body blob decompress |
| Binary size impact | 0 | **+0** (only zstd) |
| New dependencies | 0 | **0** |

---

## 14. Interview Log

| # | Decision | Rationale |
|---|----------|-----------|
| Q1 | 1-hour segments | Matches default query window, ~720 segments at 30d |
| Q2 | Append-only WAL + ring buffer for tailing | Memory constrained, write-heavy. ~260KB memory. |
| Q3 | Custom columnar + zstd, 6 encodings | 3-5MB seal memory, ~35-55GB storage, zero deps |
| Q4 | No Bleve — Go column filtering + inverted index | Bleve too storage-heavy. Columnar handles structured filters. Inverted index for FTS. |
| Q5 | Composite int64 (hour+chunk+row) | O(1) GetByID, stays int64, lock-free, crash-recoverable |
| Q6 | Request summaries eliminated | Performance metrics → flat columns. Details → body blob. No JOINs. |
| Q7 | Deep captures eliminated | All capture data → body blob. 7 tables → 1 column. |
| Q8 | Flat SDK schema | Top-level key = column. body = opaque blob. Zero server-side transformation. |
| Q9 | Body is opaque | Never parsed by storage. Need searchable? Promote to top-level SDK field → new column. |
| Q10 | Lock-free sealed reads, mutex WAL writes, parallel query fan-out | Sealed segments immutable = no locks. WAL rotated before seal = no write blocking. Goroutine per segment for parallel queries. |
| Q11 | Pre-compute histograms + counts in meta.json | Free during seal. <1ms query time for 24h. No column reads for aggregations. |
| Q12 | Clean swap — no migration | Still in dev. Build new store, swap in, delete old SQLite log code + tables. |
| Q13 | Dual timestamps: `ts` (event) + `received_at` (server) | `received_at` is guaranteed monotonic → perfect delta encoding + exact segment assignment. `ts` for user-facing time-range queries. No sorting, no edge cases. |

| Q14 | Server-side ingest pipeline | SDK stays thin. Server does: PII scrub, error field extraction, fingerprint computation, in-request log expansion — all before WAL append. |
| Q15 | Crash recovery | Incomplete seal: detect via missing `.seal_complete` marker, delete partial chunks, re-seal from WAL. |
| Q16 | Schema evolution | Missing column in old chunks → return null. Three lines of code, no migration. |
| Q17 | Disk full | WAL append fails → error to SDK. Seal fails → WAL preserved, retry. Proactive: warn <1GB, skip seal <500MB. |

### Post-Review Fixes Applied
- Seal reduced to 2 passes (was 4)
- WAL read safety via `atomic.LoadInt64(&walValidBytes)`
- Seal rotation: `sealing_T12.wal` (hour-stamped, prevents overwrite)
- Column count: 32 (was 26) — added `received_at`, `dup_queries`, `exception_class`, `source_file`, `source_line`, `error_fingerprint`
- `DistinctValues`: dict columns from meta.json; sparse columns scan the column
- `MetadataKeys`: deprecated (metadata inside opaque body)
- `SearchRequestSummaries`: replaced by sparse column filtering
- Multi-chunk meta.json: one per segment, per-chunk ID ranges
- Ingest pipeline: PII scrub + error extraction + log expansion + sampling
- Crash recovery: `.seal_complete` marker, WAL replay
- Schema evolution: missing column = null
- Disk full: proactive monitoring + graceful degradation
