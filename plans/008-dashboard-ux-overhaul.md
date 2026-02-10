# Plan 008: Dashboard UX Overhaul

## Overview

A comprehensive UX improvement plan for the OpenTrace dashboard. The current UI has a strong terminal-inspired aesthetic and solid technical foundations (HTMX, CSS variables, keyboard nav), but several areas create friction — especially for new users. This plan addresses usability gaps across navigation, forms, alerts, monitors, and general interaction patterns.

**Effort**: High (multi-phase) | **Impact**: High

---

## Current State Assessment

### What works well
- Consistent monospace / terminal aesthetic across all pages
- Dark mode first-class (system preference respected, CSS variable theming)
- Keyboard nav with numbered shortcuts (1:logs through 7:tools)
- Live log streaming with polling toggle and pulsing dot indicator
- Global environment filter in topbar
- Expandable rows on alerts/logs (inline detail, no modal needed)
- Monitor type selection grid (Query / Logs / Health / AI) is clear
- HTMX on logs page for filter-without-reload

### What needs improvement
1. Monitor creation modal is overwhelming (~1100 lines, 4 sections, many hidden fields)
2. `alert()` dialogs for errors instead of inline feedback
3. Alert page lacks grouping, filtering by monitor, and bulk actions
4. Schedule vs time_range distinction is confusing
5. No loading states during async operations
6. Keyboard shortcuts not discoverable
7. Inconsistent HTMX usage (logs uses it; alerts/watchers use manual fetch+DOM)
8. No form validation feedback until server-side error
9. No onboarding or contextual help for new users
10. Theme toggle buried in user dropdown
11. Connection strings visible in plain text

---

## Phase 1: Form Validation & Error Handling

**Goal**: Replace all `alert()` dialogs with inline feedback. Add client-side validation.

**Why this first**: Error handling affects every single user interaction. Fixing this removes the most common friction point across the entire app.

### 1.1 Toast notification system

Replace `alert()` calls with a toast/notification component that appears in a fixed position (top-right).

**Files to change:**
- `internal/web/static/style.css` — Add `.toast-container`, `.toast`, `.toast-success`, `.toast-error`, `.toast-info` styles
- `internal/web/templates/layout.html` — Add toast container div before `</body>`
- New JS utility (inline in layout or small file) — `showToast(message, type, duration)`

**Behavior:**
- Toasts auto-dismiss after 5 seconds (configurable)
- Error toasts persist until manually dismissed (click X)
- Stack vertically if multiple toasts appear
- Slide-in animation from right
- Color-coded: green (success), red (error), cyan (info)

**Example — before:**
```js
alert("Error saving monitor");
```

**Example — after:**
```js
showToast("Failed to save monitor: name is required", "error");
```

### 1.2 Inline form validation

Add real-time validation to form inputs across all modals.

**Validation rules by form:**

| Form | Field | Validation |
|------|-------|------------|
| Rule monitor | Name | Required, min 3 chars |
| Rule monitor | Data Source | Required for query/health types |
| Rule monitor | SQL Query | Required for query type, non-empty |
| Rule monitor | Threshold | Required number |
| Rule monitor | Cron expression | Syntax validation (show error below input) |
| Rule monitor | Webhook URL | Valid URL format if provided |
| AI monitor | Name | Required, min 3 chars |
| AI monitor | Analysis prompt | Required, min 10 chars |
| Connector | Name | Required |
| Connector | Connection string | Required for database type, starts with `postgres://` or `postgresql://` |

**Visual feedback:**
- Invalid field: red border + error message below in `var(--red)` 11px text
- Valid field: green border (subtle, 1px)
- Submit button disabled until all required fields pass validation
- Show validation on blur (not on every keystroke — too noisy)

**Files to change:**
- `internal/web/templates/watchers.html` — Update `saveRuleMonitor()` and `saveWatcher()` JS
- `internal/web/templates/connectors.html` — Update `saveConnector()` JS
- `internal/web/static/style.css` — Add `.form-error`, `.input-invalid`, `.input-valid` styles

### 1.3 Loading states

Add visual feedback during async operations (save, delete, test, run).

**Pattern:**
- On submit: disable button, replace text with "saving..." (or spinner character `⟳`)
- On success: show success toast, re-enable button
- On error: show error toast, re-enable button

**Files to change:**
- All modal forms in `watchers.html`, `connectors.html`
- Overview page fetch calls in `overview.html`
- Alert actions (mark read, dismiss) in `alerts.html`

---

## Phase 2: Monitor Creation Wizard

**Goal**: Break the single mega-modal into a step-by-step wizard. Reduce cognitive load for new users while keeping power users fast.

**Why**: The current rule monitor modal has 4 numbered sections (Monitor, Query/Logs/Health, Alert When, Schedule & Severity) plus optional adaptive scheduling and webhook config — all in one scrollable form. This is the most complex interaction in the app.

### 2.1 Wizard flow design

Replace the single modal with a multi-step wizard. Each step is a clean, focused screen.

**Steps:**

```
Step 1: Type & Name
├── Monitor type (already selected from type grid — carried forward)
├── Name (required)
└── Environment (optional)

Step 2: Source Configuration  (varies by type)
├── Query: Data source select + SQL textarea + preview button
├── Logs: Service, level, message filter, time window
└── Health: Data source select + check options + latency threshold

Step 3: Alert Condition
├── Metric (value / row_count)
├── Operator (gt, gte, lt, lte, eq, neq)
├── Threshold
└── Severity (info / warning / critical)

Step 4: Schedule
├── Simple mode: "Check every" dropdown (1m, 5m, 15m, 30m, 1h)
├── Advanced toggle: Cron expression input + live description preview
└── Time range override (for log lookback window if different from schedule)

Step 5 (optional): Notifications & Advanced
├── Webhook URL
├── Adaptive scheduling toggle + config (collapsed by default)
└── Review summary before save
```

**UX details:**
- Progress indicator at top: `[1] ── [2] ── [3] ── [4] ── [5]` with step labels
- "Back" and "Next" buttons at bottom of each step
- "Next" validates current step before proceeding
- Final step shows a summary card with all config, then "Create Monitor" button
- Power users: add "Skip to summary" link on step 1 that expands all fields on one page (preserves current power-user flow)

### 2.2 Edit mode

When editing an existing monitor, open the wizard pre-filled at step 1 with all values loaded. Allow jumping to any step via the progress indicator (since all steps are already valid).

### 2.3 Template integration

When a user selects a template (from the "start from a template" section):
- Pre-fill all wizard steps with template values
- Jump directly to the summary step (step 5)
- User can click any step to modify before saving

**Files to change:**
- `internal/web/templates/watchers.html` — Major refactor of the rule modal
- `internal/web/static/style.css` — Add `.wizard`, `.wizard-steps`, `.wizard-step`, `.wizard-progress` styles
- JS logic for step navigation, validation per step, summary rendering

### 2.4 Schedule clarity

The current confusion between `schedule` (cron) and `time_range` (log lookback) is a UX problem, not a data model problem. Fix with better labeling:

**Current (confusing):**
```
Check Every: [5 min dropdown]
Advanced: use cron expression
```

**Proposed (clear):**
```
How often to check
  ○ Every [5 min ▼]  ← simple mode, sets both schedule + time_range
  ○ Custom schedule   ← reveals cron input

Look back window (how far back to search)
  [Same as check interval ▼]  ← default: mirrors schedule
  [Custom: 15m ▼]             ← override: independent lookback
```

Add a help tooltip: *"Check interval = how often the monitor runs. Look back window = how far back in time it searches. Usually these are the same, but you can set a wider lookback if needed."*

---

## Phase 3: Alert Management

**Goal**: Make the alerts page powerful enough for incident response workflows.

### 3.1 Filter by monitor

Add a monitor/watcher dropdown filter alongside the existing status and severity filters.

**UI:**
```
[all ▾ unread]  [all ▾ crit  warn  info]  [all monitors ▼]
```

The monitor dropdown is populated from the current watcher list.

**Files to change:**
- `internal/web/templates/alerts.html` — Add watcher filter dropdown
- `internal/web/alerts.go` (or equivalent handler) — Accept `watcher_id` query param
- `internal/store/alerts.go` — Add `WatcherID` filter to `ListAlerts` query

### 3.2 Alert grouping

Group identical or similar alerts together instead of showing one row per occurrence.

**Approach:**
- Group by watcher_id + severity
- Show collapsed group header: `"Stuck transactions on production" (critical) × 12 in last 2h`
- Click to expand individual occurrences
- Most recent occurrence shown in header

**Implementation:**
- Client-side grouping in JS (group `allAlerts` by watcher_id before rendering)
- Add `data-watcher-id` attribute to alert rows for easy grouping
- Toggle between "grouped" and "flat" view with a button

### 3.3 Bulk actions

Allow selecting multiple alerts for batch operations.

**UI:**
- Checkbox on each alert row (appears on hover or always visible in admin mode)
- Floating action bar at bottom when items selected: `"12 selected — [Mark Read] [Dismiss]"`
- "Select all visible" checkbox in header

**Files to change:**
- `internal/web/templates/alerts.html` — Add checkboxes, selection bar, bulk action JS
- `internal/web/alerts.go` — Add `POST /api/alerts/bulk` endpoint accepting `{ids: [...], action: "read"|"dismiss"}`
- `internal/store/alerts.go` — Add `BulkMarkRead(ids)` and `BulkDismiss(ids)` methods

### 3.4 Alert history / archive

Currently dismissed alerts simply disappear. Add an "archived" tab.

**UI:**
```
[all  unread  archived]  [all  crit  warn  info]  [all monitors ▼]
```

**Implementation:**
- Dismissed alerts already have `status = 'dismissed'` in the DB
- Add `archived` as a new filter value that shows dismissed alerts
- Show archived alerts in a muted style (dimmed text, no action buttons)

---

## Phase 4: Navigation & Discoverability

**Goal**: Help new users find features and understand the interface.

### 4.1 Keyboard shortcut overlay

Press `?` anywhere to show a modal listing all keyboard shortcuts.

**Content:**
```
Keyboard Shortcuts
──────────────────

Navigation
  1        Go to Logs
  2        Go to Alerts
  3        Go to Watchers
  4        Go to Connectors
  5        Go to Servers
  6        Go to Digests
  7        Go to Tools
  ?        Show this help

Press Esc or ? to close
```

**Files to change:**
- `internal/web/templates/layout.html` — Add keydown listener for `?`, add overlay modal HTML
- `internal/web/static/style.css` — Add `.shortcut-overlay` styles

### 4.2 Contextual tooltips for adaptive states

Add `title` attributes (or small hover popovers) explaining each adaptive state badge.

| Badge | Tooltip |
|-------|---------|
| `normal` | "Running on regular schedule" |
| `escalated` | "Anomaly detected — checking more frequently" |
| `sustained` | "Alert ongoing — returned to normal frequency" |
| `relaxed` | "All clear for a while — checking less often" |
| `backing_off` | "Repeated failures — reducing check frequency" |
| `error` | "Paused after too many failures — click Resume to restart" |

**Files to change:**
- `internal/web/templates/watchers.html` — Add `title` attributes to badge rendering JS

### 4.3 Theme toggle in topbar

Move the theme toggle out of the user dropdown and into the topbar as a small icon button, visible at all times.

**Current location:** User dropdown menu → "theme: system" button
**New location:** Topbar right section, before the user dropdown, as a small icon `◐`

**Files to change:**
- `internal/web/templates/layout.html` — Move theme button to topbar-right, before separator

### 4.4 Breadcrumbs for nested pages

Add breadcrumb navigation for pages that are nested (watcher runs, server detail).

**Examples:**
```
Watchers / Stuck transactions / Runs
Servers / prod-db-01
```

**Implementation:**
- Pass breadcrumb data from Go handlers via template data
- Render in a `.breadcrumb` bar below the topbar on nested pages

**Files to change:**
- `internal/web/templates/layout.html` — Add breadcrumb section (conditionally rendered)
- `internal/web/server.go` — Add `Breadcrumbs []Breadcrumb` to page data struct
- `internal/web/watchers.go` — Pass breadcrumbs for run history page
- `internal/web/servers.go` — Pass breadcrumbs for server detail page

---

## Phase 5: Alerts & Logs Polish

**Goal**: Quality-of-life improvements for the two most-used pages.

### 5.1 Saved log filters

Allow users to save frequently-used log filter combinations and recall them with one click.

**UI:**
- "Save filter" button next to the filter form (appears when any filter is active)
- Saved filters appear as clickable chips above the filter form
- Stored in `localStorage` (no backend change needed)

**Data structure:**
```js
// localStorage key: 'opentrace-saved-filters'
[
  { name: "Prod errors", query: "", service: "api", level: "ERROR", env: "production" },
  { name: "Payment timeouts", query: "timeout", service: "payments", level: "", env: "" }
]
```

**Files to change:**
- `internal/web/templates/logs.html` — Add save/load filter UI and localStorage logic

### 5.2 Log level multi-select

Allow filtering logs by multiple levels simultaneously (e.g., ERROR + WARN).

**Current:** Single `<select>` for level
**Proposed:** Replace with toggle buttons (like severity filters on alerts page):

```
Level: [all  ERROR  WARN  INFO  DEBUG]   ← multiple can be active
```

**Files to change:**
- `internal/web/templates/logs.html` — Replace select with toggle buttons, update filter logic
- `internal/web/logs.go` (or API handler) — Accept comma-separated levels: `?level=ERROR,WARN`

### 5.3 Relative timestamps

Add relative time display ("5m ago", "2h ago") alongside absolute timestamps on alerts and logs.

**Implementation:**
- JS utility: `relativeTime(isoString)` → "5m ago", "2h ago", "3d ago"
- Show relative time by default, absolute on hover (via `title` attribute)
- Auto-update relative times every 30 seconds

**Files to change:**
- `internal/web/templates/layout.html` — Add `relativeTime()` utility to global JS
- `internal/web/templates/alerts.html` — Use relative time in alert rows
- `internal/web/templates/logs.html` — Use relative time in log entries

### 5.4 Watcher run filtering

On the watcher runs page, add filters for run status (success/failure/error).

**UI:**
```
[all  success  failed  error]
```

**Files to change:**
- `internal/web/templates/watcher_runs.html` — Add filter tabs
- Filter client-side (runs are already loaded)

---

## Phase 6: Security & Accessibility

**Goal**: Fix security concerns and basic accessibility gaps.

### 6.1 Mask connection strings

Connection strings on the connectors page should be masked by default.

**Current:** `postgres://user:password@host:5432/dbname` shown in full
**Proposed:** `postgres://user:****@host:5432/dbname` with a "reveal" toggle

**Implementation:**
- Server-side: Add `MaskedConnectionString()` method that replaces password portion
- Client-side: "Show" button toggles between masked and full (fetches full from API only on click)
- Full connection string never rendered in initial HTML

**Files to change:**
- `internal/web/connectors.go` — Return masked string by default, add `/api/connectors/{id}/reveal` endpoint
- `internal/web/templates/connectors.html` — Show masked, add reveal button

### 6.2 Modal accessibility

Add proper ARIA attributes to all modals.

**Changes per modal:**
```html
<!-- Before -->
<div id="rule-modal" class="modal">

<!-- After -->
<div id="rule-modal" class="modal" role="dialog" aria-modal="true" aria-labelledby="rule-modal-title">
```

**Also add:**
- Focus trap: Tab key cycles within modal (doesn't leak to background)
- Return focus to trigger button when modal closes
- `Escape` key closes modal (already works via onclick, but add keydown listener)

**Files to change:**
- `internal/web/templates/watchers.html` — Add ARIA to monitor modals
- `internal/web/templates/connectors.html` — Add ARIA to connector modal
- `internal/web/static/style.css` — Verify focus indicator visibility

### 6.3 Color contrast check

Audit and fix color contrast ratios for WCAG AA compliance.

**Known issues:**
- Green text (`#4ade80`) on dark background (`#0e0e0e`) — likely below 4.5:1 ratio
- Dim text (`var(--text-dim)`) may be too low contrast
- Yellow warning badges on light backgrounds

**Fix:** Adjust CSS variables in both dark and light themes to meet 4.5:1 minimum contrast ratio.

**Files to change:**
- `internal/web/static/style.css` — Adjust color variables

---

## Phase 7: Consistency & Polish

**Goal**: Standardize patterns across all pages for a unified feel.

### 7.1 HTMX everywhere

Extend HTMX usage from logs page to alerts and watchers for consistent no-reload filtering.

**Alerts page:**
- Filter tabs trigger `hx-get="/api/alerts?status=unread&severity=critical"` → replaces `#alerts-list`
- Removes need for manual `renderAlerts()` DOM manipulation

**Watchers page:**
- Watcher list loaded via `hx-get="/api/watchers"` → replaces `#watchers-list`
- After save/delete, `hx-get` re-fetches the list instead of manual DOM update

**Benefits:**
- Less custom JS to maintain
- Consistent loading/error patterns
- Progressive enhancement (works without JS for basic viewing)

### 7.2 Empty states with guidance

Improve empty states to guide users toward next action.

**Current empty states:**
```
--
no monitors configured
create one to start automated monitoring
```

**Proposed:**
```
No monitors yet

Monitors watch your databases and logs for problems,
then alert you when something needs attention.

[+ Create your first monitor]     [Learn more →]
```

Each page should have a contextual empty state that:
1. Explains what the feature does (one sentence)
2. Provides a primary action button
3. Links to setup/docs if relevant

### 7.3 Table sorting

Add clickable column headers for sortable tables (digest history, server list, watcher runs).

**Pattern:**
- Click column header to sort ascending
- Click again for descending
- Arrow indicator `▲` / `▼` on active sort column
- Client-side sort (data already loaded)

---

## Implementation Priority & Dependencies

```
Phase 1 (Foundation) ──────────────────────── No dependencies
  ├── 1.1 Toast system
  ├── 1.2 Inline validation
  └── 1.3 Loading states

Phase 2 (Monitor Wizard) ─────────────────── Depends on Phase 1 (validation)
  ├── 2.1 Wizard flow
  ├── 2.2 Edit mode
  ├── 2.3 Template integration
  └── 2.4 Schedule clarity

Phase 3 (Alert Management) ───────────────── Independent (can parallel Phase 2)
  ├── 3.1 Filter by monitor
  ├── 3.2 Alert grouping
  ├── 3.3 Bulk actions
  └── 3.4 Alert history

Phase 4 (Navigation) ─────────────────────── Independent (can parallel Phase 2/3)
  ├── 4.1 Keyboard overlay
  ├── 4.2 Adaptive tooltips
  ├── 4.3 Theme toggle
  └── 4.4 Breadcrumbs

Phase 5 (Alerts & Logs Polish) ───────────── After Phase 3 (builds on alert changes)
  ├── 5.1 Saved filters
  ├── 5.2 Level multi-select
  ├── 5.3 Relative timestamps
  └── 5.4 Run filtering

Phase 6 (Security & A11y) ────────────────── Independent (can start anytime)
  ├── 6.1 Mask connection strings
  ├── 6.2 Modal accessibility
  └── 6.3 Color contrast

Phase 7 (Consistency) ────────────────────── After Phases 1-5 (standardizes patterns)
  ├── 7.1 HTMX everywhere
  ├── 7.2 Empty states
  └── 7.3 Table sorting
```

**Suggested execution order:**
1. Phase 1 (unblocks everything)
2. Phase 4.1 + 4.2 + 4.3 (quick wins, high discoverability impact)
3. Phase 6.1 (security fix, should be early)
4. Phase 2 (biggest UX win, most effort)
5. Phase 3 (big workflow improvement)
6. Phase 5 (quality of life)
7. Phase 6.2 + 6.3 (accessibility)
8. Phase 7 (polish)

---

## Files Impact Summary

| File | Phases Affected | Change Size |
|------|----------------|-------------|
| `internal/web/static/style.css` | 1, 2, 3, 4, 5, 6, 7 | Large (new components + fixes) |
| `internal/web/templates/layout.html` | 1, 4, 5 | Medium (toast, shortcuts, utilities) |
| `internal/web/templates/watchers.html` | 1, 2, 4 | **Major** (wizard refactor) |
| `internal/web/templates/alerts.html` | 1, 3, 5 | Large (filters, grouping, bulk) |
| `internal/web/templates/connectors.html` | 1, 6 | Small-Medium |
| `internal/web/templates/logs.html` | 5 | Medium (saved filters, multi-level) |
| `internal/web/templates/overview.html` | 1 | Small (loading states) |
| `internal/web/alerts.go` | 3 | Medium (new endpoints, filter params) |
| `internal/web/connectors.go` | 6 | Small (mask/reveal endpoint) |
| `internal/web/server.go` | 4 | Small (breadcrumb data) |
| `internal/store/alerts.go` | 3 | Small (bulk operations, watcher filter) |

---

## Non-Goals (Explicitly Out of Scope)

- **JS framework migration** — No React/Vue/Svelte. Vanilla JS + HTMX is working well.
- **Mobile-first redesign** — This is a desktop-focused devops tool. Responsive is nice-to-have, not primary.
- **Browser push notifications** — Would require service worker; defer to a future plan.
- **Dashboard customization** — Drag-and-drop widgets, reorderable panels; too complex for now.
- **Internationalization** — English only.
- **Log export to CSV** — Useful but separate feature; defer.

---

## Success Criteria

After completing all phases:

1. **New user can create their first monitor in < 2 minutes** without reading docs
2. **Zero `alert()` dialogs** remaining in the codebase
3. **All form errors show inline** with specific guidance
4. **Alert triage takes < 30 seconds** for 20+ alerts (grouping + bulk actions)
5. **Keyboard shortcut `?` overlay** is discoverable within first session
6. **Connection strings never visible** in initial page render
7. **All modals have proper ARIA** attributes and focus trapping
8. **Color contrast meets WCAG AA** (4.5:1 minimum) in both themes
