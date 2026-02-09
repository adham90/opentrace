# Dashboard Redesign Plan

## Goals
1. Sidebar navigation (replace top horizontal nav)
2. Overview page with stats (new home page)
3. Wider content area (1100px → 1400px)
4. Light theme + system default toggle

---

## Phase 1: CSS Variables & Theme System

The theme system uses CSS custom properties (already in `:root`) and adds a light palette. A `data-theme` attribute on `<html>` switches between them. System default uses `prefers-color-scheme` media query as fallback.

### 1.1 Restructure `:root` variables

Current dark values stay as-is but move into `[data-theme="dark"]` and the default `:root` (for system-default fallback).

```css
/* System default: dark */
:root {
  --bg: #0e0e0e;
  --bg-secondary: #161616;
  --bg-tertiary: #1e1e1e;
  --border: #2e2e2e;
  --text: #d0d0d0;
  --text-dim: #777;
  --text-bright: #e0e0e0;
  --green: #4ade80;
  --green-dim: #22633a;
  --red: #f87171;
  --red-dim: #7f1d1d;
  --yellow: #fbbf24;
  --blue: #60a5fa;
  --cyan: #22d3ee;
  --purple: #c084fc;
  /* ...same as today... */
}

/* Explicit dark (same as root default) */
[data-theme="dark"] {
  /* same values — can be omitted if root is dark */
}

/* Light theme */
[data-theme="light"] {
  --bg: #f5f5f5;
  --bg-secondary: #ffffff;
  --bg-tertiary: #eaeaea;
  --border: #d4d4d4;
  --text: #1a1a1a;
  --text-dim: #6b6b6b;
  --text-bright: #000000;
  --green: #16a34a;
  --green-dim: #dcfce7;
  --red: #dc2626;
  --red-dim: #fee2e2;
  --yellow: #ca8a04;
  --blue: #2563eb;
  --cyan: #0891b2;
  --purple: #9333ea;
}

/* System-default override using media query */
@media (prefers-color-scheme: light) {
  :root:not([data-theme="dark"]) {
    /* light values here */
  }
}
```

### 1.2 Theme toggle logic

- Store preference in `localStorage` under key `opentrace-theme`
- Values: `"system"` (default), `"dark"`, `"light"`
- On page load: read preference, set `data-theme` on `<html>` accordingly
- If `"system"`: don't set `data-theme` attr, let CSS media query decide
- Toggle control lives in the sidebar footer (next to setup link)
- Cycle: system → dark → light → system

### 1.3 Files changed
- `style.css` — restructure `:root`, add `[data-theme="light"]` block, add `@media` fallback
- `layout.html` — add inline `<script>` in `<head>` (before CSS paints) to set `data-theme` from localStorage (prevents flash)

### 1.4 Things to watch
- Scrollbar colors (`::-webkit-scrollbar-thumb`)
- `::selection` background
- SVG data URIs in select dropdown arrow (hardcoded `fill='%23777'`) — need to use currentColor or have two variants
- Modal overlay (`rgba(0,0,0,0.7)`) — should be variable
- `box-shadow` on `.live-dot` uses `var(--green)` — already works
- Badge hex backgrounds (`#78350f`, `#1e3a5f`) — need light equivalents

---

## Phase 2: Sidebar Navigation

### 2.1 Layout structure

Replace the current `<header>` + `<main>` with a sidebar + content layout:

```html
<body>
  <aside class="sidebar">
    <div class="sidebar-header">
      <span class="logo">OPENTRACE</span>
    </div>
    <nav class="sidebar-nav">
      <a href="/" class="nav-item {{if eq .Nav "overview"}}active{{end}}">
        <span class="nav-bullet">◆</span> overview
      </a>
      <div class="nav-section">MONITOR</div>
      <a href="/alerts" class="nav-item {{if eq .Nav "alerts"}}active{{end}}">
        <span class="nav-bullet">◆</span> alerts
        <span id="alert-badge" class="nav-badge" style="display:none">0</span>
      </a>
      <a href="/logs" class="nav-item {{if eq .Nav "logs"}}active{{end}}">
        <span class="nav-bullet">◆</span> logs
      </a>
      <div class="nav-section">CONFIGURE</div>
      <a href="/watchers" class="nav-item {{if eq .Nav "watchers"}}active{{end}}">
        <span class="nav-bullet">◆</span> watchers
      </a>
      <a href="/connectors" class="nav-item {{if eq .Nav "connectors"}}active{{end}}">
        <span class="nav-bullet">◆</span> connectors
      </a>
    </nav>
    <div class="sidebar-footer">
      <a href="/setup" class="nav-item {{if eq .Nav "setup"}}active{{end}}">
        <span class="nav-bullet">◆</span> setup
      </a>
      <button class="theme-toggle" onclick="cycleTheme()" title="Toggle theme">
        <span id="theme-icon">◐</span>
      </button>
    </div>
  </aside>
  <main class="content">
    <div class="content-container">
      {{template "content" .}}
    </div>
  </main>
</body>
```

### 2.2 Sidebar CSS

```
Sidebar:
  - width: 180px, fixed, left: 0, top: 0, bottom: 0
  - background: var(--bg)
  - border-right: 1px solid var(--border)
  - display: flex, flex-direction: column
  - padding: 16px 0
  - z-index: 100

.sidebar-header:
  - padding: 0 16px 16px
  - border-bottom: 1px solid var(--border)

.sidebar-nav:
  - flex: 1
  - padding: 12px 0
  - overflow-y: auto

.nav-section:
  - font-size: 10px
  - color: var(--text-dim)
  - text-transform: uppercase
  - letter-spacing: 0.5px
  - padding: 12px 16px 4px
  - user-select: none

.nav-item:
  - display: flex
  - align-items: center
  - gap: 8px
  - padding: 6px 16px
  - font-size: 12px
  - color: var(--text-dim)
  - text-decoration: none
  - border-left: 2px solid transparent

.nav-item:hover:
  - color: var(--text-bright)
  - background: var(--bg-secondary)

.nav-item.active:
  - color: var(--text-bright)
  - border-left-color: var(--green)
  - background: var(--bg-secondary)

.nav-bullet:
  - font-size: 6px (tiny bullet, decorative)
  - color: inherit

.nav-badge:
  - background: var(--red)
  - color: #fff
  - font-size: 10px
  - padding: 0px 5px
  - border-radius: 8px
  - margin-left: auto

.sidebar-footer:
  - border-top: 1px solid var(--border)
  - padding: 8px 0
  - display: flex, flex-direction: column, gap: 4px

.theme-toggle:
  - padding: 6px 16px
  - font-size: 11px
  - color: var(--text-dim)
  - background: none, border: none
  - cursor: pointer
  - text-align: left
  - font-family: inherit

Content area:
  - margin-left: 180px
  - min-height: 100vh
  - padding: 24px 32px
```

### 2.3 Route changes

Current:
- `GET /` → alerts page (Nav: "alerts")

New:
- `GET /` → overview page (Nav: "overview")
- `GET /alerts` → alerts page (Nav: "alerts")

The alerts page moves from `/` to `/alerts`. Need to update:
- `server.go` routes
- `pages.go` handler
- All template links that reference `/` for alerts
- `alerts.html` badge update code

### 2.4 Container width

- Remove `.container` class from layout (no longer needed — sidebar provides structure)
- `.content-container` replaces it: `max-width: 1400px`, `margin: 0`, no auto-center (left-aligned against sidebar)
- All pages automatically get the wider content area

### 2.5 Files changed
- `layout.html` — new sidebar structure, remove `<header>`, change `<main>` wrapper
- `style.css` — remove old header/nav styles, add sidebar styles, update `.container`
- `server.go` — add `GET /` for overview, move alerts to `GET /alerts`
- `pages.go` — add `handleOverviewPage`, update `handleAlertsPage` nav value
- `alerts.html` — update any `/` links to `/alerts`

---

## Phase 3: Overview Page

### 3.1 New template: `overview.html`

Server-rendered stat values + JS for live updates.

```
┌──────────────┐ ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│  ▲ 3         │ │  ◆ 12        │ │  ↑ 847       │ │  ● 4 / 5     │
│  alerts      │ │  watchers    │ │  logs /1h    │ │  connectors  │
│  1 crit      │ │  10 active   │ │  23 errors   │ │  1 error     │
└──────────────┘ └──────────────┘ └──────────────┘ └──────────────┘

┌─────────────────────────────────┐ ┌──────────────────────────────┐
│  RECENT ALERTS                  │ │  LOG STREAM                  │
│                                 │ │                              │
│  (last 5 alerts, linked)        │ │  (last 10 logs, live)        │
│                                 │ │                              │
│  view all alerts →              │ │  view all logs →             │
└─────────────────────────────────┘ └──────────────────────────────┘
```

### 3.2 New API endpoint: `GET /api/overview`

Returns aggregated stats in one request (avoids multiple fetches):

```json
{
  "alerts": {
    "total": 3,
    "critical": 1,
    "warning": 2,
    "info": 0
  },
  "watchers": {
    "total": 12,
    "active": 10,
    "paused": 2,
    "error": 0
  },
  "logs": {
    "last_hour": 847,
    "errors_last_hour": 23
  },
  "connectors": {
    "total": 5,
    "connected": 4,
    "error": 1
  }
}
```

This requires new store methods or queries:
- `alertStore.CountBySeverity(ctx)` → map[string]int
- `watcherStore.CountByStatus(ctx)` → map[string]int
- `logStore.CountRecent(ctx, since time.Time)` → total, errorCount
- `dsStore.CountByStatus(ctx)` → map[string]int

Alternatively, reuse existing List methods and compute in the handler (simpler, good enough for now since data sets are small).

### 3.3 Stat card CSS

```css
.stat-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 24px;
}

.stat-card {
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 16px;
  background: var(--bg-secondary);
  cursor: pointer;
  transition: border-color 0.15s;
}
.stat-card:hover { border-color: var(--text-dim); }

.stat-number {
  font-size: 24px;
  font-weight: 700;
  color: var(--text-bright);
  line-height: 1;
  margin-bottom: 4px;
}
.stat-number.status-ok { color: var(--green); }
.stat-number.status-warn { color: var(--yellow); }
.stat-number.status-error { color: var(--red); }

.stat-label {
  font-size: 11px;
  color: var(--text-dim);
  text-transform: uppercase;
  letter-spacing: 0.3px;
  margin-bottom: 8px;
}

.stat-detail {
  font-size: 11px;
  color: var(--text-dim);
  line-height: 1.5;
}

.stat-icon {
  font-size: 16px;
  margin-right: 4px;
}
```

### 3.4 Overview panels (recent alerts + log stream)

```css
.overview-panels {
  display: grid;
  grid-template-columns: 3fr 2fr;
  gap: 16px;
}

.overview-panel {
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-secondary);
  overflow: hidden;
}

.overview-panel-header {
  padding: 10px 16px;
  border-bottom: 1px solid var(--border);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.5px;
  color: var(--text-dim);
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.overview-panel-body {
  padding: 0;
  max-height: 400px;
  overflow-y: auto;
}
```

### 3.5 Files changed
- New: `templates/overview.html`
- `pages.go` — add `overviewTmpl`, `handleOverviewPage`, overview fragment template
- `server.go` — add `GET /api/overview` route
- `style.css` — add stat-grid, stat-card, overview-panel styles

---

## Phase 4: Cleanup & Polish

### 4.1 Remove old header styles
- Delete `.header`, `nav`, `.header .container` CSS rules
- Delete old `<header>` from layout.html

### 4.2 Update `alerts.html`
- Update the badge-update JS to use new `#alert-badge` location (now in sidebar)
- Change any `href="/"` to `href="/alerts"`

### 4.3 Update `watchers.html`
- Watcher runs link back button: `href="/watchers"` (unchanged)

### 4.4 Test matrix
- All existing tests must pass (route changes may break some)
- Test `GET /` returns overview page
- Test `GET /alerts` returns alerts page
- Test `GET /api/overview` returns stats JSON
- Verify theme toggle works: system → dark → light → system
- Verify sidebar nav active states on every page
- Verify alert badge updates in sidebar

---

## Execution Order

| Step | Phase | Description | Risk |
|------|-------|-------------|------|
| 1 | 1 | Add light theme CSS variables + `@media` query | Low — additive, no breakage |
| 2 | 1 | Add theme toggle JS (localStorage + `data-theme`) | Low — additive |
| 3 | 2 | Rewrite `layout.html` with sidebar structure | **High** — touches all pages |
| 4 | 2 | Update CSS: remove header, add sidebar styles | **High** — paired with step 3 |
| 5 | 2 | Update container width to 1400px | Low — part of step 4 |
| 6 | 2 | Move alerts from `/` to `/alerts`, add overview route | Medium — route change |
| 7 | 3 | Create overview page template | Low — new file |
| 8 | 3 | Add `/api/overview` handler | Low — new endpoint |
| 9 | 3 | Add overview stat card + panel CSS | Low — additive |
| 10 | 4 | Fix tests, clean up old styles, polish | Medium |

Steps 3+4 are the riskiest and should be done together as one atomic change. Steps 1-2 (theme) can be done independently before or after the sidebar.

---

## Files Summary

| File | Changes |
|------|---------|
| `style.css` | Theme variables, remove header, add sidebar, wider container, stat cards, overview panels, light theme |
| `layout.html` | Full rewrite: sidebar nav, theme script, remove old header |
| `pages.go` | Add `overviewTmpl`, `handleOverviewPage`, `handleLogsPoll` MaxLogID, tmplFuncs |
| `server.go` | New routes: `GET /` overview, `GET /alerts`, `GET /api/overview` |
| `overview.html` | New template: stat cards + recent alerts + log stream panels |
| `alerts.html` | Update JS badge selector path, fix `/` → `/alerts` links |
| `logs_test.go` | Update tests for any route changes |
| `server_test.go` / `mock_test.go` | Update if route assertions change |
