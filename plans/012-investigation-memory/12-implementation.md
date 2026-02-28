# Implementation Order

## Staged Rollout

Implementation is split into 6 stages. Each stage is independently deployable, testable, and delivers value on its own. Ship each stage, validate it with real usage, then proceed to the next.

| Stage | Name | Phases | Tasks | What It Proves |
|---|---|---|---|---|
| 1 | Session Foundation | 1-2 | 1-10 | Identity, tracking, MCP Sampling work correctly |
| 2 | Recurrence Detection | 3 | 11-18 | "This happened before" context is accurate and useful |
| 3 | Ranking + Context | 7-8 | 34-45 | Learned suggestions reduce investigation steps |
| 4 | All Integrations | 4-6, 9 | 19-33, 46-48 | Full subsystem enrichment adds value to context |
| 5 | Code + Deploy Intel | 10-11 | 49-61 | Code-to-production bridge works with real data |
| 6 | Full Agent Assistant | 12-15 | 62-81 | Complete AI coding agent assistant experience |

### Stage Gate Criteria

Before moving to the next stage, validate:

| Stage | Gate Criteria |
|---|---|
| 1 → 2 | Sessions created with correct identity. Intent classification >70% accurate on manual review. MCP Sampling returns summaries (or fallback works). |
| 2 → 3 | Recurrence chains form correctly when watchers fire. Escalation notes appear. Error group reopening detected. |
| 3 → 4 | Ranked suggestions differ from static ones. At least one dead-end tool penalized. Context injection response < 2KB. |
| 4 → 5 | All 12 subsystem sections appear in investigation_context when relevant. No P99 latency regression > 5ms. |
| 5 → 6 | Code entities auto-populated from error groups. Risk scores correlate with known fragile code. Deploy impact measured correctly. |

---

## Stage 1: Session Foundation

**Goal:** Replace hardcoded `sessionID = "mcp"` with real identity. Start collecting session data.

**Migration:** `investigation_sessions`, `mcp_activity` ALTER TABLEs only. Other tables can wait.

### Phase 1: Foundation
1. Migration: `investigation_sessions` table + `mcp_activity` ALTER TABLEs
2. `InvestigationSession` model and store
3. `SessionTracker` — creates session on Initialize, tracks steps, closes on Shutdown
4. Wire into `wrapWithActivityLog` — replace hardcoded `sessionID = "mcp"` with real identity
5. Update all mock stores

### Phase 2: Intent + Outcome
6. Add `context` parameter to top 10 most-used tools
7. Intent classification (3 layers)
8. Outcome inference with all subsystem signals
9. MCP Sampling integration
10. `set_session_summary` fallback tool

### Stage 1 Tests
- Session lifecycle: create → step tracking → close
- Intent classification: all 3 layers with edge cases
- Outcome inference: all signal types
- MCP Sampling summary flow with fallback
- Backward compat: all existing tools work unchanged
- Missing `context` parameter doesn't break tools

### Stage 1 Validation
Open Claude Code, run a few tools, close the connection. Check:
- Session exists in DB with correct user, intent, outcome
- Summary populated via MCP Sampling (or fallback inference)
- `mcp_activity` rows have real `investigation_session_id` instead of `"mcp"`
- Multiple concurrent Claude Code windows create separate sessions

---

## Stage 2: Recurrence Detection

**Goal:** "This happened before. Here's what worked." Link investigations to watchers, error groups, and health checks.

**Migration:** None — uses columns already on `investigation_sessions`.

### Phase 3: Watcher + Error Group + Health Check Linking
11. Auto-link on watcher creation
12. Auto-link on alert investigation + recurrence tracking
13. Auto-link on error investigation (`error_detail`, `investigate_error`)
14. Auto-link on error resolution (`resolve_error`, `ignore_error`)
15. Error group recurrence detection (reopened errors)
16. Health check failure as investigation trigger
17. Health check creation linking
18. Escalation note generation

### Stage 2 Tests
- Watcher creation → alert fires → recurrence detected (full chain)
- Error resolution → error reopens → recurrence detected
- Health check down → investigated → health check created
- Fix durability tracked correctly (time between fix and recurrence)
- Escalation notes generated when fixes degrade
- Recurrence count increments correctly across 3+ occurrences

### Stage 2 Validation
1. Investigate an issue → create a watcher → close session
2. Manually trigger the watcher
3. Open new Claude Code session → call `triage_alerts`
4. Verify: recurrence context appears with previous fix details, escalation note

---

## Stage 3: Ranking + Context

**Goal:** Replace static tool suggestions with learned rankings. Inject investigation context into tool responses.

**Migration:** `tool_transitions`, `workflow_templates` tables.

### Phase 7: Ranking Engine
34. `ToolTransitionStore` — upsert, query, dead-end detection
35. Transition rollup background job
36. `RankingService` — score formula with runbook integration
37. Replace static suggestions with ranked suggestions
38. Suggestion acceptance tracking
39. Seed workflow templates for cold start

### Phase 8: Context Injection
40. `ContextInjector` — assembles investigation_context from sessions + linked subsystems
41. Similar session matching
42. Parallel investigation detection
43. Dead-end warnings
44. Access-scoped filtering
45. Response size capping

### Stage 3 Tests
- Ranking: score formula, fallback chain, time decay
- Transition rollup: aggregation correctness
- Suggestion acceptance tracking
- Context injection: similar sessions, recurrence info, access filtering
- Response size capping (< 2KB)
- Dead-end tools penalized in rankings
- Cold start: workflow templates used when no learned data
- Multiple concurrent sessions: cross-pollination without cross-contamination

### Stage 3 Validation
After 5-10 investigation sessions:
- Suggestions evolve from static → learned (check `ranking_source` field)
- Dead-end tools appear in `dead_ends` section
- `investigation_context` appears on tool responses for investigation-intent sessions
- Simple queries (intent=query) get NO investigation_context

---

## Stage 4: All Integrations

**Goal:** Connect remaining subsystems. Each integration is additive and can ship incrementally.

**Migration:** `query_memory`, `runbook_effectiveness` tables.

### Phase 4: Agent Notes + Runbooks
19. Surface existing notes during investigations
20. Auto-create notes from resolved sessions
21. Track runbook execution in sessions
22. Runbook effectiveness tracking
23. Auto-suggest runbooks based on effectiveness

### Phase 5: Trends + Performance + Database
24. Pre/post investigation metric snapshots
25. Deploy marker correlation
26. Request performance baseline tracking
27. Query memory (explain_query fingerprints)
28. kill_query as resolution signal
29. Post-fix metric comparison

### Phase 6: Traces + Analytics + Audit
30. Trace ID tracking in sessions
31. Traffic context from web analytics
32. Traffic anomaly detection
33. Audit log integration (record investigations + surface admin actions)

### Phase 9: Web + Analytics (Part 1)
46. Web API endpoints for investigation sessions
47. Admin UI: investigation dashboard
48. Retention/pruning jobs for Part 1 tables

### Stage 4 Tests
- Agent notes auto-created on resolution
- Runbook execution → effectiveness tracked → suggested next time
- Query investigated → stored in memory → served next time
- Deploy correlation detected and served
- Pre/post metric comparison on resolved session
- Audit trail recorded for investigation
- Reconnection and session resume
- Cross-team knowledge sharing (access-scoped)
- Retention pruning across all tables

### Stage 4 Validation
Each integration adds a new section to `investigation_context`. Verify individually:
- `relevant_notes` appears after an auto-note was created
- `runbook_suggestion` appears with resolution rate > 50%
- `query_memory` appears for previously-investigated queries
- `deploy_correlation` appears when deploy preceded the issue
- `traffic_context` shows anomaly when traffic is unusual
- `recent_admin_actions` shows config changes before the issue
- Admin UI displays session list, detail view, and effectiveness stats

---

## Stage 5: Code + Deploy Intel

**Goal:** Bridge source code to production. Auto-populate code entity registry from existing data.

**Migration:** `code_entities`, `deploys` tables.

**Prerequisite:** System has been running with Stages 1-4 for at least a few days so there are error groups and request summaries to populate from.

### Phase 10: Code Entity Registry
49. Migration: `code_entities` table
50. `CodeEntityStore` implementation
51. `CodeEntityPopulator` — auto-populate from error groups, request summaries, investigation sessions
52. Risk score computation + batch recompute job
53. `code_context`, `code_risk`, `whats_fragile` MCP tools
54. Update all mock stores

### Phase 11: Deploy Intelligence + Webhook Intake
55. Migration: `deploys` table
56. `DeployStore` implementation
57. `EventStore` implementation (PR, test, alert, commit, custom events)
58. Webhook HTTP endpoints (`POST /api/events/{type}`) with API key validation
59. `DeployTracker` background job — measures impact 15 min after deploy
60. `deploy_history`, `deploy_risk`, `record_deploy` MCP tools
61. Link deploys to code entities — increment incident counts on impacted files

### Stage 5 Tests
- Code entity populator: from error groups (stack trace parsing), request summaries, sessions
- Risk score computation: all factor weights, edge cases
- Deploy impact measurement: error rate delta, response time delta, incident detection
- Full code entity lifecycle: error group created → entity populated → risk computed → served via `code_context`
- Deploy lifecycle: webhook received → deploy recorded → impact measured → linked to investigation
- `code_entities` CRUD, unique index, risk score ordering
- `deploys` CRUD, commit lookup, impact update, risk-for-files query
- Webhook authentication: invalid API key rejected, valid key accepted

### Stage 5 Validation
- `code_context("orders_controller.rb")` returns real error history and risk score
- `whats_fragile(service: "payments")` returns the riskiest code paths
- `deploy_risk(files: "orders_controller.rb")` shows incident history for the file
- Webhook `POST /api/events/deploy` records a deploy, impact measured 15 min later

---

## Stage 6: Full Agent Assistant

**Goal:** Complete the AI coding agent assistant experience with proactive context, test correlation, and the `context` meta-tool.

**Migration:** `test_production_links`, `uncovered_error_paths` tables. `investigation_sessions` ALTER for dev session columns.

**Prerequisite:** Code entity registry and deploy tracking from Stage 5 are populated with real data.

### Phase 12: Development Session Tracking
62. Extended session intents: `development`, `review`, `deployment`
63. `classifyDevelopmentIntent` — keyword-based classification
64. `files_modified`, `files_read`, `linked_deploy_id` columns on `investigation_sessions`
65. Development → deploy → investigation chain linking

### Phase 13: Proactive Context Delivery
66. MCP notifications infrastructure (`sendNotification` on MCPServer)
67. Background alert checker — polls for alerts relevant to connected sessions
68. Code risk change notifications — detects error count increases for modified files
69. Team finding notifications — detects parallel investigation discoveries
70. MCP resources: `opentrace://services/{service}/status`, `opentrace://code/risk-summary`, `opentrace://investigations/active`

### Phase 14: Test-Production Correlation
71. Migration: `test_production_links` and `uncovered_error_paths` tables
72. `TestProductionLinkStore` implementation
73. Auto-populate from test events + error groups
74. `test_gaps`, `test_priority` MCP tools
75. Suggested test description generation

### Phase 15: Context Meta-Tool + Cross-Agent + Web API
76. `context` meta-tool implementation
77. Task type classifier: `editing`, `debugging`, `reviewing`, `deploying`, `testing`
78. Context bundle assembly — per task type, pulls from relevant subsystems
79. Cross-agent knowledge sharing verification (already built into auth + sessions)
80. Web API endpoints for code entities, deploys, tests, events
81. Admin UI: code risk dashboard, deploy impact view, test coverage gaps

### Stage 6 Tests
- Task type classification: editing, debugging, reviewing, deploying, testing keywords
- Context bundle assembly: correct subsystems queried for each task type
- Test-production link matching: file path mapping, coverage detection
- Uncovered error path ranking: impact score calculation
- Development session → deploy → investigation chain correctly linked
- `context` meta-tool returns different bundles for editing vs debugging vs deploying
- MCP notifications delivered when alert fires during active session
- MCP resources return correct real-time data
- Test gaps populated from error groups with no matching test files
- Cross-agent: session from Claude Code enriches Cursor's `context` call (access-scoped)
- `context` meta-tool works with empty code entity registry (returns empty sections gracefully)
- Deploy risk returns "no data" assessment for unknown files (not an error)
- New webhook endpoints don't affect existing API routes

### Stage 6 Validation
- `context(task: "editing orders_controller.rb")` returns code risk + investigation history
- `context(task: "debugging payment errors")` returns full investigation context
- `context(task: "deploying payments service")` returns deploy risk + recommended watchers
- `test_priority(service: "payments")` returns production error paths ranked by impact
- MCP notification received when watcher fires during active session
- Two different MCP clients share knowledge through the same OpenTrace instance

---

# Success Metrics

## Per-Stage Metrics

### Stage 1
| Metric | Target |
|---|---|
| Sessions created with correct identity | 100% (no more hardcoded "mcp") |
| Intent classification accuracy | >70% on manual review of 20 sessions |
| Session summary coverage | >80% of investigation sessions have non-empty summary |
| Zero latency impact | <5ms overhead on P99 tool response time |

### Stage 2
| Metric | Target |
|---|---|
| Recurrence detection accuracy | >90% (all trigger types correctly grouped) |
| Fix durability tracked | 100% of recurrences have durability measurement |

### Stage 3
| Metric | Target |
|---|---|
| Median steps to resolution | 20-30% reduction over 30-day window |
| First-suggestion acceptance | >50% |
| Dead-end rate | 15% reduction |

### Stage 4
| Metric | Target |
|---|---|
| Query memory hit rate | Track |
| Runbook suggestion acceptance | Track |
| Auto-note creation | Track per resolved session |
| Cross-team knowledge transfer | Track sessions enriched by other users' findings |

### Stage 5
| Metric | Target |
|---|---|
| Code entity coverage | >80% of error groups mapped to entities |
| Risk score accuracy | Manual review of top 20 matches known fragile code |
| Deploy impact detection | >90% of incident-causing deploys flagged |

### Stage 6
| Metric | Target |
|---|---|
| `context` tool adoption | >60% of sessions start with `context` |
| Context bundle relevance | Track (next tool matches suggested_tools) |
| Test gap identification | Track (uncovered paths that get tests within 7 days) |
| Proactive notification hit rate | >70% actionable (led to tool call within 5 min) |
| Webhook event ingestion | Track events/day, processing latency |
| Cross-agent knowledge hits | Track `context` calls enriched by different agent's findings |

---

# Risks and Mitigations

## Stage 1 Risks
| Risk | Mitigation |
|---|---|
| MCP Sampling not supported by client | `set_session_summary` manual fallback + automatic inference from 5+ signal types |
| SQLite write pressure from activity logging | Existing async ActivityLogger (bounded channel, 2 workers) handles this |
| Session detection wrong on reconnect | Ask Claude Code via tool response, don't guess |

## Stage 2 Risks
| Risk | Mitigation |
|---|---|
| False recurrence (unrelated issues grouped) | Recurrence requires watcher/error/healthcheck link — not just "same service" |

## Stage 3 Risks
| Risk | Mitigation |
|---|---|
| Cold start — no data for weeks | Curated workflow templates + runbook suggestions as fallback |
| Stale historical patterns | Time-decay in ranking, 30-day rolling windows |
| Low-volume instances → noisy rankings | Minimum support threshold (3 occurrences), template fallback |
| Context injection makes responses too large | Cap `investigation_context` to 2KB, only include for investigation intent |
| Too many subsystem queries per tool call | Lazy loading: only query subsystems relevant to current tool + cached per-session |

## Stage 4 Risks
| Risk | Mitigation |
|---|---|
| Privacy — leaking findings across teams | Access-scoped filtering on all context injection |
| Subsystem integration failure | Each integration wrapped in error recovery — tool always works even if enrichment fails |

## Stage 5 Risks
| Risk | Mitigation |
|---|---|
| Stack trace parsing fails for unfamiliar frameworks | Pluggable parsers per language (Ruby, Go, Python, JS). Unknown formats stored raw and skipped |
| Code entity registry grows unbounded | Prune entities with zero errors and zero investigations older than 90 days. Cap at 50K entities |
| Deploy impact measurement window misses slow-onset issues | 15-min default + second check at 1 hour. Incident linking catches sessions up to 30 min after deploy |
| Webhook endpoint abuse / spam | Rate limiting per API key (100 events/min). Payload size limit (64KB). Event deduplication |

## Stage 6 Risks
| Risk | Mitigation |
|---|---|
| MCP notifications not supported by all clients | Graceful degradation — fire-and-forget. Agent still gets context via tool calls |
| `context` tool returns too much data | Cap response to 4KB. Prioritize by task type. `more_available: true` flag when truncated |
| Risk scores are noisy with little data | Minimum thresholds: 3 errors before risk > 0.2. Show "insufficient data" for new entities |
| Test-production linking accuracy (wrong file mapping) | Exact path matching first. Convention-based fallback. Manual override via MCP tool |
| Cross-agent data leakage between organizations | Auth token scopes data to user's accessible data sources. No cross-org queries possible |

---

# Expected Behavior Summary

## Stage 1 Behavior
| Scenario | What Happens |
|---|---|
| Any MCP tool call | Session tracked with real user identity. No more `sessionID = "mcp"`. |
| Investigation session ends | MCP Sampling requests summary. Outcome inferred from signals. |
| Simple data query | Minimal tracking. Auto-marked resolved. No summary needed. |
| MCP Sampling not available | Automatic inference + `set_session_summary` tool as fallback. |
| Multiple Claude Code windows | Independent sessions via `connection_id`. No cross-contamination. |
| Claude Code crashes mid-session | On reconnect: "Continue this investigation?" prompt in tool response. |

## Stage 2 Behavior
| Scenario | What Happens |
|---|---|
| Watcher fires from previous investigation | Full recurrence chain with fix durability and escalation notes. |
| Error group reopens after being resolved | Treated as recurrence. Previous resolution context served. |
| Health check goes down again | Same recurrence pattern. Previous outage history served. |
| Recurring issue with degrading fixes | "Fixes holding for less time. Consider root cause." |

## Stage 3 Behavior
| Scenario | What Happens |
|---|---|
| Second investigation of same issue (same dev) | Full history: past session, notes, fix impact. Learned suggestions. |
| Same issue, different developer | Teammate's findings served (access-scoped). |
| Multiple developers investigating simultaneously | Independent sessions. Cross-pollination of findings. |
| Brand new OpenTrace install | Templates + runbook suggestions until real data accumulates. |

## Stage 4 Behavior
| Scenario | What Happens |
|---|---|
| Same slow query investigated again | Query memory: "Investigated 3 times. Root cause: missing index. Last fix improved 450ms→12ms." |
| Runbook available for this type of issue | "This runbook resolved 82% of similar investigations." |
| Deploy happened before issue started | "Deploy abc123 happened 15 minutes before error spike." |
| Admin changed config before issue | "Connector max_connections changed from 20 to 10, 30 min ago." |
| Traffic spike correlates with errors | "Traffic is 2.3x normal for this hour." |
| Subsystem integration fails | Tool works normally. Missing context section logged as warning. |
| First investigation of a new issue | Normal results + deploy/traffic/admin context. Session recorded. |

## Stage 5 Behavior
| Scenario | What Happens |
|---|---|
| Agent edits a high-risk file | `code_context` returns risk score, recent incidents, notes, performance baseline. |
| Agent edits a file with no production data | `code_context` returns empty sections. No warnings. No errors. |
| CI/CD sends deploy webhook | Deploy recorded. Impact measured after 15 min. Linked to code entities. |
| Agent about to deploy risky files | `deploy_risk` returns file-by-file risk, incident history, recommended deploy window. |
| Deploy has no measurable impact | Impact fields remain at zero/null. `caused_incident = false`. No risk score inflation. |

## Stage 6 Behavior
| Scenario | What Happens |
|---|---|
| Agent calls `context` for the first time | Returns tailored bundle for task type (editing/debugging/reviewing/deploying/testing). |
| Agent writing tests | `test_priority` returns production error paths ranked by impact. |
| Watcher fires while agent is connected | MCP notification pushed. Agent alerts developer with context. |
| Error count increases for a file agent modified | MCP notification: "Error count for X increased since your last edit." |
| Teammate finds root cause in parallel session | MCP notification: "A teammate found something relevant." |
| Different AI agents on same codebase | Both authenticate via MCP tokens. Both share investigation memory + code entity registry. |
| Webhook for unknown event type | `POST /api/events/custom` accepts any structured event. Stored for future correlation. |
| Test file maps to wrong production file | Exact path matching prevents false links. Manual override available. |
