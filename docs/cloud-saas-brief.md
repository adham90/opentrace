# OpenTrace Cloud SaaS — Architecture Brief

## Concept

A cloud SaaS version of OpenTrace where each customer account gets its own isolated SQLite database. The cloud app is a separate Go binary that wraps the open-source OpenTrace core as a library. Self-hosted version stays clean — no multi-tenancy logic added to it.

---

## Architecture: In-Process Instances

One Go process manages all accounts. Each "instance" is a Go struct in memory — not a separate process, container, or VM.

```
Cloud App (single Go binary, single process)
├── HTTP Server (:443)
├── Tenant Middleware (subdomain/header/JWT → account → instance)
├── Instance Manager (map[accountID]*Instance)
│   ├── acct_1: {db, stores, registry, scheduler}  ← loaded, active
│   ├── acct_2: {db, stores, registry, scheduler}  ← loaded, idle
│   └── acct_3: (unloaded, file on disk)            ← closed to save RAM
├── Background Jobs
│   ├── Idle evictor (close instances unused for 30min)
│   ├── Usage tracker (count requests/logs per account for billing)
│   └── Backup scheduler (Litestream → S3)
└── Cloud DB (Postgres — accounts, billing, teams only)

/data/tenants/
├── acct_1/opentrace.db   ← entire account is one SQLite file
├── acct_2/opentrace.db
└── acct_3/opentrace.db
```

### What is an "instance"?

Not a server. Not a process. A Go struct:

```go
type Instance struct {
    db        *sql.DB              // handle to one SQLite file
    stores    store.Stores         // all store implementations (pointers)
    registry  *connector.Registry  // live DB connections for this account
    scheduler *watcher.Scheduler   // one goroutine with a ticker
    router    chi.Router           // pre-built route table
    limits    Limits               // per-account resource caps
    semaphore chan struct{}         // concurrency limiter
    lastAccess time.Time           // for idle eviction
}
```

- Creating an instance = open a SQLite file + allocate structs (~5ms)
- Destroying an instance = close the DB handle, GC cleans up structs
- A loaded instance costs ~2-3MB RAM. An unloaded one costs 0 (just a file on disk).

### How requests are routed

```
1. Request: POST https://acme.opentrace.cloud/api/logs
2. Tenant middleware extracts "acme" → looks up "acct_123"
3. Instance manager: is acct_123 loaded?
   - Yes → use it (one map lookup)
   - No → open SQLite file, create stores, put in map (~5ms)
4. Forward request to instance's router
5. Instance handles it exactly like self-hosted OpenTrace
6. Return response
```

### Lazy loading & eviction

Not all accounts stay in memory. Background evictor closes idle ones:

```
09:00  acme signs in    → instance loaded (open SQLite file)
09:15  acme active      → uses loaded instance
09:45  30min idle       → evictor closes SQLite, removes from map
10:30  acme comes back  → instance reloaded from disk (~5ms)
```

### Instance Manager

The entire orchestration layer:

```go
type Manager struct {
    mu        sync.RWMutex
    instances map[string]*Instance
    dataDir   string
}

func (m *Manager) Get(accountID string) *Instance    // load or return cached
func (m *Manager) Create(accountID string) error      // new account signup
func (m *Manager) Delete(accountID string) error      // account cancelled
func (m *Manager) evictIdle()                          // background goroutine
```

Four functions. That's the entire "orchestration layer."

---

## Why In-Process (Alternatives Considered)

### Container per account (Docker/K8s)
- Each container uses ~30-50MB RAM even idle
- 500 accounts = 15-25GB RAM just existing
- Slow to create (seconds), need orchestrator
- Cost: $200-500/mo for 500 accounts
- **Verdict: too expensive, too complex for our scale**

### Systemd per account (one process per tenant)
- Each process needs its own port → port management nightmare
- No dynamic scaling — must write .service files at runtime
- 500 processes = 15GB of duplicated Go runtimes
- **Verdict: systemd is for fixed services, not dynamic tenants**

### MicroVM per account (Firecracker)
- Each VM uses minimum ~128MB RAM + own kernel
- Designed for running untrusted customer code (Lambda, Fly.io)
- We run our own trusted code — don't need VM isolation
- **Verdict: overkill, solves a problem we don't have**

### Shared Postgres + tenant_id on every row
- Requires adding tenant_id to EVERY table, EVERY query, EVERY index
- One missed WHERE clause = data leak between accounts
- Massive refactor, diverges from open-source codebase
- **Verdict: too invasive, data leak risk, forks the codebase**

### Serverless / on-demand processes
- Cold start latency (1-3 seconds)
- SQLite must live on network storage (slow)
- Watch schedulers can't run if process isn't alive
- **Verdict: doesn't work with background jobs or stateful features**

### Managed SQLite (Turso/LiteFS)
- Good option for later — network latency on every query
- Vendor dependency
- Could be a Phase 2 migration from local SQLite files
- **Verdict: not now, but keep as scaling option**

### Side-by-side comparison

| | In-Process | Containers | Systemd | MicroVM | Shared DB | Turso |
|---|---|---|---|---|---|---|
| Complexity | **Low** | High | Medium | Very High | Medium | Low-Med |
| Isolation | App-level | Full | Process | Full | Row-level | Per-DB |
| RAM per idle account | **~0** | 30-50MB | 30-50MB | 128MB+ | 0 | 0 |
| Cost at 500 accts | **$20-50** | $200-500 | $200+ | $500+ | $50-200 | $200 |
| OpenTrace changes | Small refactor | None | None | None | **Huge** | Medium |
| Background jobs | Easy | Easy | Easy | Hard | Easy | Easy |

---

## Changes Needed in OpenTrace (Open Source)

Structural refactoring only — no new features, no behavior changes for self-hosted users. The open-source version gets cleaner code from this.

### Phase 1: Instance package (foundation)
Extract all dependency wiring from `cmd/opentrace/main.go` into `pkg/instance/Instance` struct with `New(cfg)` and `Close()`. Self-hosted `main.go` calls `New()` once — same behavior. Cloud app calls it per account.

### Phase 2: Programmatic config
Config struct is already a plain struct. Add `Validate()` method so cloud app can construct configs directly without reading env vars.

### Phase 3: Pluggable auth
Add `Authenticator` interface. Self-hosted uses current session-cookie auth. Cloud app injects its own (SSO, team tokens, cloud API keys).

### Phase 4: Router as standalone builder
Separate HTTP handlers from Server struct. Cloud app mounts OpenTrace routes under tenant-scoped middleware (routing, usage metering). Can skip routes it replaces (e.g., login page → cloud SSO).

### Phase 5: Resource limits
```go
type Limits struct {
    MaxLogEntries    int64  // per-account log cap
    MaxDataSources   int   // connector limit
    MaxWatchers      int   // watcher limit
    RetentionDays    int   // auto-purge
    MaxStorageBytes  int64 // SQLite file size cap
}
```
Zero means unlimited (self-hosted default). Cloud sets per pricing tier.

### Phase 6: Lifecycle hooks
Suspend/resume (close/reopen DB for idle accounts), backup/restore exposed as library calls (not just CLI commands).

---

## Account Isolation (Noisy Neighbor Prevention)

### Data isolation
Each account is a separate SQLite file. No SQL query can cross files. Impossible to leak data.

### Rate limiting
Per-account request limiter (e.g., 300/min free, 3000/min pro). Exceeding returns 429.

### Query timeouts
Every DB query gets `context.WithTimeout` (e.g., 5 seconds). Slow queries cancelled, other accounts unaffected.

### Memory limits
Cap result set sizes (MaxQueryRows) and payload sizes per account. Prevents one account from bloating the process.

### Concurrency limits
Per-account semaphore (buffered channel). Max 50 concurrent requests per account. Exceeding returns 503.

```go
// All protections in one middleware (~30 lines)
func AccountGuard(manager *Manager) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            account := getAccount(r)
            inst := manager.Get(account.ID)

            // Rate limit
            if !account.limiter.Allow() { return 429 }

            // Concurrency limit
            select {
            case inst.semaphore <- struct{}{}:
                defer func() { <-inst.semaphore }()
            default:
                return 503
            }

            // Query timeout via context
            ctx, cancel := context.WithTimeout(r.Context(), account.queryTimeout)
            defer cancel()
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

---

## Downsides of In-Process Model (Honest Assessment)

| Downside | Mitigation |
|---|---|
| One crash kills all accounts | systemd Restart=always (back in 2s). Go panics are per-goroutine, Chi Recoverer catches them. |
| One bad account can hog CPU | Rate limiting + query timeouts + concurrency limits (application-level guards) |
| Memory spikes from one account affect all | Cap result sizes, payload limits. Go GC handles transient spikes. |
| Can't update one account independently | Feature flags. Canary deploys at server level (route 5% to new version). |
| No hard security boundary | Acceptable for shared SaaS. Offer dedicated VM as enterprise tier if needed. |
| Horizontal scaling requires sharding | Shard accounts across 2-3 VMs. Only needed at thousands of active accounts. |

---

## Reliability & Failover

### Level 1: Day one (single VM)
```
1 VM + systemd Restart=always + Litestream → S3
Downtime on crash: ~2 seconds (auto-restart)
Cost: ~$20/mo
```

### Level 2: Paying customers (active-passive)
```
VM-1 (active) ──Litestream──→ S3 ──restore──→ VM-2 (standby)
Load balancer health check every 5s
VM-1 dies → LB routes to VM-2 (~10s failover)
Cost: ~$50/mo
```

### Level 3: Growth (active-active)
```
VM-1 (accounts A-M) ←→ VM-2 (accounts N-Z)
Each handles half. One dies → other loads the dead one's accounts from shared storage.
Cost: ~$80/mo
```

### Level 4: Scale (multi-region)
```
US-East (US accounts) ←→ EU-West (EU accounts)
S3 sync between regions. Data stays in region for compliance.
Cost: $300+/mo
```

Each level builds on the previous. Nothing thrown away.

### Litestream (key infrastructure)
Continuously replicates every SQLite file to S3 (every few seconds). Gives you:
- **Backup**: point-in-time restore per account
- **Replication**: standby VM stays ~10s behind
- **Disaster recovery**: new VM restores all files from S3 in minutes
- **Migration**: move an account to another VM by restoring from S3

Free, open source, designed for exactly this use case.

---

## MCP / Claude Code Integration

### Current state
OpenTrace already has an MCP server with 77+ tools. Currently stdio-only (requires local binary).

### What to add
HTTP/SSE transport on the existing MCP server. Same tools, remote access.

### Claude Code marketplace plugin?
**Not needed.** A marketplace plugin is just a pre-filled `.mcp.json` template. The cloud dashboard can provide the same thing with the API key already filled in — better UX than any marketplace.

### How it works
Cloud dashboard shows a copy-paste snippet per account:

```json
{
  "mcpServers": {
    "opentrace": {
      "type": "sse",
      "url": "https://acme.opentrace.cloud/mcp",
      "headers": { "Authorization": "Bearer ot_abc123..." }
    }
  }
}
```

Developer pastes into their `.mcp.json` → connected to production OpenTrace from Claude Code, Cursor, or any MCP-compatible editor.

### Why this matters
- Developers debug production issues without leaving their editor
- AI sees both the code (local) and production state (OpenTrace) simultaneously
- 77 tools already built — log search, error tracking, diagnostics, query analysis
- Natural distribution channel: developers discover OpenTrace through their AI editor
- Potential pricing lever: free tier = 5 tools, pro = all 77

### What NOT to build
- Don't build a VS Code extension — MCP works across all AI editors
- Don't build a separate CLI — developers interact via natural language in their editor
- Don't build a separate plugin codebase — same MCP server, new transport

---

## Cloud App Responsibilities

| Concern | Owner |
|---|---|
| Signup, login, SSO, team management | Cloud app (Postgres) |
| Billing, plans, Stripe integration | Cloud app |
| Usage metering (requests, logs, storage per account) | Cloud app |
| Request routing (subdomain → account → instance) | Cloud app middleware |
| Instance lifecycle (create, suspend, resume, delete) | Cloud app manager |
| Backups (Litestream → S3) | Cloud app |
| Resource limits enforcement | Cloud app middleware |
| MCP HTTP/SSE endpoint | Cloud app (wraps OpenTrace MCP) |
| Everything else (logs, watchers, connectors, alerts, UI, MCP tools) | OpenTrace instance |

---

## Data Separation

```
Cloud App Postgres                    OpenTrace Instance SQLite (per account)
─────────────────                     ─────────────────────────────────────
accounts (id, email, plan, status)    logs, data_sources, watchers,
teams (account_id, user_id, role)     connectors, alerts, users, sessions,
billing_events (amount, date)         metrics, settings, errors, incidents
usage (log_count, storage_bytes)      ... everything OpenTrace already has
```

Cloud app only stores cloud concerns. All product data stays in each account's SQLite file.

---

## Scaling Numbers

| Accounts | Active | RAM | VM cost |
|---|---|---|---|
| 100 total | ~20 active | ~100MB | $20/mo |
| 1,000 total | ~100 active | ~500MB | $20-40/mo |
| 5,000 total | ~500 active | ~2GB | $40-80/mo |
| 10,000+ total | ~1000 active | ~4GB | 2 VMs, $80-160/mo |

SQLite per-account, lazy loading, idle eviction. Most accounts are idle most of the time.

---

## Why SQLite Per Account is Perfect

- **Isolation for free** — separate files, impossible to leak data
- **Backup = copy one file** — per-account backup/restore is trivial
- **Delete account = delete folder** — cleanest possible cleanup
- **Move account = copy file** — migrate between servers easily
- **No shared DB bottleneck** — accounts don't compete for connection pools
- **Same model as Turso, Cloudflare D1, LiteFS** — proven at scale

---

## Implementation Order

1. **Instance package** in OpenTrace (foundation for everything)
2. **Programmatic config + validation**
3. **Cloud app skeleton** (account management, Postgres, tenant middleware)
4. **Instance manager** (create/get/delete/evict)
5. **Auth integration** (pluggable auth in OpenTrace, SSO in cloud)
6. **Resource limits + isolation middleware**
7. **MCP HTTP/SSE transport**
8. **Litestream backup integration**
9. **Billing integration (Stripe)**
10. **Dashboard (account settings, usage, .mcp.json config snippet)**
