# Plan: Terminal Chat UI Redesign

## Goal

Transform the investigate UI from a constrained 960px/1200px centered layout into a full-screen, immersive chat experience that feels like a modern AI chat app (Claude, ChatGPT) blended with the aesthetic of a hacker terminal / Claude Code CLI. Every pixel should feel intentional — dark, monospaced, minimal chrome, maximum content.

---

## Design Principles

1. **Full bleed** — no wasted gutters; content stretches edge-to-edge
2. **Terminal DNA** — monospace everywhere, prompt prefixes (`>`, `$`, `~`), scanline subtlety, cursor blink
3. **Information density** — show more conversation, less UI chrome
4. **Focused interaction** — input area is prominent, everything else recedes
5. **Ambient feedback** — subtle glows, color pulses, no jarring animations

---

## Current State (what changes)

| Element | Now | After |
|---------|-----|-------|
| Layout wrapper | `.container` max-width 1200px, centered | Full viewport, no max-width, no top header |
| Header | 48px tall, always visible, top bar with nav | **Eliminated entirely** — nav moves to sidebar tabs |
| Sidebar | 250px fixed, plain list, no nav | 280px collapsible, nav tabs at top + chat list below |
| Message area | max-width 85% messages | max-width 720px centered in fluid area |
| Input bar | Small form with `>` prefix | Full-width command bar with glow focus state |
| Timeline steps | Colored left-border boxes | Compact collapsible rows with monospace labels |
| Typography | 13px base | 14px for messages, 12px for metadata/timeline |

---

## Changes

### 1. Full-Screen Layout — No Top Header

**Files:** `internal/web/static/style.css`, `internal/web/templates/layout.html`

The top `<header>` bar is completely removed on the investigate page. Navigation moves into the sidebar (see section 2). The chat layout takes 100% of the viewport.

**1a. layout.html changes**

The investigate page gets its own minimal layout — no header, no `.container` wrapper. Other pages (logs, connectors) keep the existing header + container layout unchanged.

```html
{{define "layout"}}
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>OpenTrace{{if .Title}} — {{.Title}}{{end}}</title>
  <link rel="stylesheet" href="/static/style.css">
  <!-- CDN deps unchanged -->
</head>
<body>
  {{if eq .Nav "investigate"}}
    {{template "content" .}}
  {{else}}
    <header class="header">
      <div class="container">
        <span class="logo">OPENTRACE</span>
        <nav>
          <a href="/">investigate</a>
          <a href="/logs" {{if eq .Nav "logs"}}class="active"{{end}}>logs</a>
          <a href="/connectors" {{if eq .Nav "connectors"}}class="active"{{end}}>connectors</a>
        </nav>
      </div>
    </header>
    <main>
      <div class="container">
        {{template "content" .}}
      </div>
    </main>
  {{end}}
</body>
</html>
{{end}}
```

**1b. CSS: chat-layout fills viewport**

```css
/* REMOVE */
.investigate-layout .container { max-width: 1200px; }

/* NEW */
.chat-layout {
  display: flex;
  height: 100vh;
  width: 100vw;
  overflow: hidden;
}
```

No `calc(100vh - Xpx)` needed — there's nothing above the chat layout anymore.

---

### 2. Sidebar with Navigation Tabs

**Files:** `internal/web/static/style.css`, `internal/web/templates/investigate.html`

The sidebar becomes the single control surface: logo, nav tabs, new chat button, and chat history list. Similar to Claude/ChatGPT where navigation is part of the side panel.

**2a. Sidebar structure (HTML)**

```html
<div class="chat-sidebar" id="chat-sidebar">
  <!-- Logo + collapse toggle -->
  <div class="sidebar-brand">
    <span class="logo">OPENTRACE</span>
    <button class="sidebar-toggle" onclick="toggleSidebar()" title="collapse sidebar">[</button>
  </div>

  <!-- Navigation tabs -->
  <nav class="sidebar-nav">
    <a href="/" class="sidebar-nav-item active">
      <span class="nav-icon">&#9656;</span> investigate
    </a>
    <a href="/logs" class="sidebar-nav-item">
      <span class="nav-icon">&#9776;</span> logs
    </a>
    <a href="/connectors" class="sidebar-nav-item">
      <span class="nav-icon">&#9881;</span> connectors
    </a>
  </nav>

  <!-- New chat button -->
  <div class="chat-sidebar-header">
    <button class="btn btn-new-chat" onclick="newChat()">+ new chat</button>
  </div>

  <!-- Chat history list -->
  <div class="chat-list" id="chat-list"></div>
</div>
```

When collapsed, a floating expand button `]` appears at the top-left edge of the viewport.

**2b. Sidebar CSS**

```css
.chat-sidebar {
  width: 280px;
  min-width: 280px;
  background: #080808;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  transition: width 200ms ease, min-width 200ms ease;
  overflow: hidden;
}
.chat-sidebar.collapsed {
  width: 0;
  min-width: 0;
  border-right: none;
}

/* Brand bar: logo left, toggle right */
.sidebar-brand {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 14px;
  border-bottom: 1px solid var(--border);
}
.sidebar-brand .logo {
  font-size: 12px;
  letter-spacing: 0.1em;
  color: var(--green);
}
.sidebar-toggle {
  background: none;
  border: none;
  color: var(--text-dim);
  font-family: var(--font-mono);
  font-size: 14px;
  cursor: pointer;
  padding: 2px 6px;
}
.sidebar-toggle:hover { color: var(--text-bright); }

/* Floating expand button when collapsed */
.sidebar-expand {
  position: fixed;
  top: 12px;
  left: 8px;
  background: #111;
  border: 1px solid var(--border);
  border-radius: 4px;
  color: var(--text-dim);
  font-family: var(--font-mono);
  font-size: 14px;
  cursor: pointer;
  padding: 4px 8px;
  z-index: 10;
  display: none;       /* shown via JS when sidebar collapsed */
}
.sidebar-expand:hover { color: var(--text-bright); border-color: var(--green); }

/* Nav tabs */
.sidebar-nav {
  display: flex;
  flex-direction: column;
  padding: 8px 0;
  border-bottom: 1px solid var(--border);
}
.sidebar-nav-item {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 14px;
  font-size: 12px;
  font-family: var(--font-mono);
  color: var(--text-dim);
  text-decoration: none;
  transition: background 150ms, color 150ms;
}
.sidebar-nav-item:hover {
  background: #111;
  color: var(--text);
}
.sidebar-nav-item.active {
  color: var(--green);
  background: rgba(74, 222, 128, 0.05);
  border-left: 2px solid var(--green);
}
.nav-icon {
  font-size: 11px;
  width: 16px;
  text-align: center;
}
```

**2c. Collapsible sidebar JavaScript**

```javascript
function toggleSidebar() {
  var sidebar = document.getElementById('chat-sidebar');
  var expandBtn = document.getElementById('sidebar-expand-btn');
  sidebar.classList.toggle('collapsed');
  expandBtn.style.display = sidebar.classList.contains('collapsed') ? 'block' : 'none';
  localStorage.setItem('sidebar-collapsed', sidebar.classList.contains('collapsed'));
}

// Restore on load
(function() {
  if (localStorage.getItem('sidebar-collapsed') === 'true') {
    var sidebar = document.getElementById('chat-sidebar');
    var expandBtn = document.getElementById('sidebar-expand-btn');
    if (sidebar) { sidebar.classList.add('collapsed'); }
    if (expandBtn) { expandBtn.style.display = 'block'; }
  }
})();
```

**2d. Chat list items as cards**

Each chat entry becomes a mini-card with:
- Title (truncated, 13px, bright text)
- Relative time (11px, dim)
- Active state: left green accent border (3px), slightly lighter background
- Hover: background `#1a1a1a`
- Delete button only visible on hover (opacity transition)

```css
.chat-list {
  flex: 1;
  overflow-y: auto;
  padding: 4px 0;
}
.chat-list-item {
  display: flex;
  align-items: center;
  padding: 10px 14px;
  border-left: 3px solid transparent;
  cursor: pointer;
  text-decoration: none;
  color: inherit;
  transition: background 150ms, border-color 150ms;
}
.chat-list-item:hover {
  background: #1a1a1a;
}
.chat-list-item.active {
  background: #141414;
  border-left-color: var(--green);
}
.chat-list-item .chat-delete {
  opacity: 0;
  transition: opacity 150ms;
}
.chat-list-item:hover .chat-delete {
  opacity: 1;
}
```

**2e. New Chat button — ghost style**

```css
.chat-sidebar-header {
  padding: 8px 12px;
}
.btn-new-chat {
  width: 100%;
  text-align: left;
  font-family: var(--font-mono);
  font-size: 12px;
  background: transparent;
  border: 1px dashed var(--border);
  color: var(--text-dim);
  padding: 8px 12px;
  border-radius: 4px;
  cursor: pointer;
  transition: border-color 150ms, color 150ms;
}
.btn-new-chat:hover {
  border-color: var(--green);
  color: var(--green);
}
```

---

### 3. Message Area Redesign

**File:** `internal/web/static/style.css`, `internal/web/templates/investigate.html`

**3a. Centered message column**

Messages constrained to a readable width (max 720px) centered in the fluid chat-main area, like Claude/ChatGPT:

```css
.message-area {
  flex: 1;
  overflow-y: auto;
  padding: 24px 0;
}
.message-area > * {
  max-width: 720px;
  margin-left: auto;
  margin-right: auto;
  padding-left: 20px;
  padding-right: 20px;
}
```

**3b. Message bubbles — flatter, wider**

Drop the asymmetric rounded corners. Simpler, cleaner style:

- **User messages:** Full-width within the 720px column, subtle green-tinted left border, no background bubble. Content displayed as monospaced terminal command.
- **Assistant messages:** Full-width, no background. Markdown content with slightly larger font (14px). Clear visual separation via spacing.

```css
.message {
  max-width: 100%;
  margin-bottom: 24px;
}
.message-user {
  padding: 12px 16px;
  border-left: 3px solid var(--green);
  background: rgba(74, 222, 128, 0.04);
  border-radius: 0 4px 4px 0;
}
.message-user .message-content pre {
  margin: 0;
  white-space: pre-wrap;
  color: var(--text-bright);
}
.message-assistant {
  padding: 12px 0;
  background: none;
  border-radius: 0;
}
.message-assistant .message-content {
  font-size: 14px;
  line-height: 1.7;
}
```

**3c. Role labels**

Subtle inline labels:

```css
.message-role {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-dim);
  margin-bottom: 4px;
}
.message-user .message-role { color: var(--green); }
.message-assistant .message-role { color: var(--text-dim); }
```

---

### 4. Timeline Steps Redesign (Thinking / Tool Calls / Observations)

**File:** `internal/web/static/style.css`, `internal/web/templates/investigate.html`

The thinking/tool/observation steps are the "investigation trace" — the core differentiator. Make them compact but scannable.

**4a. Compact accordion rows**

Each step is a single-line summary row that expands on click:

```
  * thinking       Analyzing query structure...               >
  * sql_query      SELECT * FROM logs WHERE level = 'ERR...'  >
  * result         3 rows returned                             >
```

- Collapsed: single line, 12px, monospace, dim colors
- Expanded: shows full content below the summary line
- Left icon: colored dot (purple/cyan/yellow) instead of thick border

```css
.timeline-step {
  max-width: 100%;
  margin-bottom: 2px;
  border-radius: 4px;
  font-size: 12px;
  background: rgba(255,255,255,0.02);
  border: none;
  cursor: pointer;
  transition: background 150ms;
}
.timeline-step:hover {
  background: rgba(255,255,255,0.04);
}
.timeline-label {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  font-size: 11px;
  font-family: var(--font-mono);
}
.timeline-label::before {
  content: '';
  width: 6px;
  height: 6px;
  border-radius: 50%;
  display: inline-block;
}
.timeline-thinking .timeline-label::before { background: var(--purple); }
.timeline-tool_call .timeline-label::before { background: var(--cyan); }
.timeline-observation .timeline-label::before { background: var(--yellow); }
.timeline-error .timeline-label::before { background: var(--red); }

.timeline-body {
  padding: 0 12px 8px 24px;
  display: none;            /* collapsed by default */
  font-size: 12px;
}
.timeline-step.expanded .timeline-body {
  display: block;
}
```

**4b. JavaScript: click-to-expand**

Add click handler on `.timeline-step` to toggle `.expanded` class. During live streaming, auto-expand the latest step and collapse previous ones.

```javascript
// Delegate click on timeline steps
document.getElementById('message-area').addEventListener('click', function(e) {
  var step = e.target.closest('.timeline-step');
  if (step) step.classList.toggle('expanded');
});
```

**4c. Summary text in label**

When creating a timeline step, append a truncated summary inline with the label so collapsed rows are still informative:

```javascript
// In createTimelineStep(), add summary span to label
var summary = document.createElement('span');
summary.className = 'timeline-summary';
summary.textContent = truncate(content, 60);
labelDiv.appendChild(summary);
```

```css
.timeline-summary {
  color: var(--text-dim);
  font-size: 11px;
  margin-left: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 400px;
}
```

---

### 5. Command Input Bar Redesign

**File:** `internal/web/static/style.css`, `internal/web/templates/investigate.html`

The input bar is the primary interaction point. Make it feel like a premium terminal prompt.

**5a. Full-width command bar**

```css
.chat-input-area {
  border-top: 1px solid var(--border);
  padding: 16px 0;
  background: #0a0a0a;
}
.chat-input-area > * {
  max-width: 720px;
  margin: 0 auto;
  padding: 0 20px;
}
.query-form {
  display: flex;
  align-items: center;
  gap: 8px;
  background: #111;
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 10px 14px;
  transition: border-color 200ms, box-shadow 200ms;
}
.query-form:focus-within {
  border-color: var(--green);
  box-shadow: 0 0 0 1px rgba(74, 222, 128, 0.15), 0 0 20px rgba(74, 222, 128, 0.05);
}
```

**5b. Input field — naked inside the form container**

```css
.query-form input[type="text"] {
  flex: 1;
  background: transparent;
  border: none;
  outline: none;
  color: var(--text-bright);
  font-size: 14px;
  font-family: var(--font-mono);
  caret-color: var(--green);
}
```

**5c. Provider selector — subtle pill**

```css
#provider-select {
  font-size: 11px;
  padding: 4px 8px;
  background: transparent;
  border: 1px solid var(--border);
  border-radius: 12px;
  color: var(--text-dim);
  cursor: pointer;
}
```

**5d. Submit button**

```css
#submit-btn {
  font-size: 12px;
  padding: 6px 14px;
  border-radius: 6px;
  background: var(--green-dim);
  border: 1px solid var(--green);
  color: var(--green);
  cursor: pointer;
  transition: background 150ms;
}
#submit-btn:hover {
  background: rgba(74, 222, 128, 0.2);
}
```

**5e. Status bar — centered below input**

```css
#status-bar {
  text-align: center;
  font-size: 11px;
  color: var(--text-dim);
  padding-top: 6px;
  min-height: 20px;
}
```

---

### 6. Plan Panel Redesign

**File:** `internal/web/static/style.css`

Terminal-checklist style:

```css
.plan-panel {
  margin: 16px 0;
  padding: 12px 16px;
  background: rgba(74, 222, 128, 0.03);
  border: 1px solid rgba(74, 222, 128, 0.1);
  border-radius: 6px;
  font-size: 12px;
}
.plan-header {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--green);
  margin-bottom: 8px;
}
.plan-step {
  padding: 4px 0;
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--text-dim);
}
.plan-step.completed { color: var(--green); }
.plan-step.in_progress { color: var(--text-bright); }
```

---

### 7. Empty State Redesign

**Files:** `internal/web/templates/investigate.html`, `internal/web/static/style.css`

Centered, atmospheric empty state with blinking cursor:

```html
<div class="empty-state">
  <div class="empty-state-prompt">$_</div>
  <div class="empty-state-text">start a new investigation</div>
  <div class="empty-state-hint">describe an issue, ask a question, or paste an error</div>
</div>
```

```css
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  height: 100%;
  gap: 8px;
  color: var(--text-dim);
}
.empty-state-prompt {
  font-size: 32px;
  color: var(--green);
  font-family: var(--font-mono);
  animation: blink 1s step-end infinite;
}
@keyframes blink {
  50% { opacity: 0; }
}
.empty-state-text {
  font-size: 16px;
  color: var(--text);
}
.empty-state-hint {
  font-size: 12px;
  color: var(--text-dim);
}
```

---

### 8. Scrollbar & Polish

**File:** `internal/web/static/style.css`

**8a. Custom scrollbar**

```css
::-webkit-scrollbar { width: 6px; }
::-webkit-scrollbar-track { background: transparent; }
::-webkit-scrollbar-thumb { background: #333; border-radius: 3px; }
::-webkit-scrollbar-thumb:hover { background: #555; }
```

**8b. Selection color**

```css
::selection {
  background: rgba(74, 222, 128, 0.2);
  color: var(--text-bright);
}
```

**8c. Smooth scrolling**

```css
.message-area { scroll-behavior: smooth; }
```

**8d. Code block refinement**

```css
.markdown-body pre {
  background: #0d1117;
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  font-size: 13px;
  overflow-x: auto;
}
.markdown-body code:not(pre code) {
  background: rgba(255,255,255,0.06);
  padding: 2px 5px;
  border-radius: 3px;
  font-size: 0.9em;
}
```

---

### 9. Keyboard Shortcuts

**File:** `internal/web/templates/investigate.html`

| Key | Action |
|-----|--------|
| `Ctrl+N` / `Cmd+N` | New chat |
| `Ctrl+B` / `Cmd+B` | Toggle sidebar |
| `Escape` | Focus input / cancel current stream |
| `/` | Focus input (when not already focused) |

```javascript
document.addEventListener('keydown', function(e) {
  var mod = e.metaKey || e.ctrlKey;
  if (mod && e.key === 'n') { e.preventDefault(); newChat(); }
  if (mod && e.key === 'b') { e.preventDefault(); toggleSidebar(); }
  if (e.key === 'Escape') { document.getElementById('query-input').focus(); }
  if (e.key === '/' && document.activeElement.tagName !== 'INPUT') {
    e.preventDefault();
    document.getElementById('query-input').focus();
  }
});
```

---

## Files Modified

| File | Change Summary |
|------|---------------|
| `internal/web/static/style.css` | Full rewrite of chat layout, messages, timeline, input, sidebar, scrollbar, plan panel, empty state. Remove header styles for investigate. |
| `internal/web/templates/layout.html` | Investigate page: no `<header>`, no `.container` wrapper. Other pages unchanged. |
| `internal/web/templates/investigate.html` | Sidebar now includes logo + nav tabs + toggle. Collapsible timeline steps. Empty state markup. Keyboard shortcuts. Sidebar collapse localStorage. |

## Files NOT Modified

- `internal/web/templates/connectors.html` — keeps existing header + centered layout
- `internal/web/templates/logs.html` — keeps existing header + centered layout
- `internal/web/pages.go` — no server-side changes needed
- `internal/web/server.go` — no route changes needed

---

## Implementation Order

1. **Layout + sidebar nav** — full-screen layout, remove top header, sidebar with logo/nav tabs/toggle (CSS + layout.html + investigate.html)
2. **Sidebar chat list** — collapsible panel, restyled cards, localStorage persistence (CSS + JS)
3. **Message area** — centered 720px column, flat message styles (CSS)
4. **Input bar** — command bar with glow, naked input (CSS)
5. **Timeline steps** — compact accordion rows with click-to-expand (CSS + JS)
6. **Plan panel** — terminal checklist style (CSS)
7. **Empty state** — centered with blinking cursor (HTML + CSS)
8. **Polish** — scrollbar, selection, keyboard shortcuts, code blocks (CSS + JS)

Each step is independently shippable — the UI improves incrementally.

---

## Visual Reference

```
+--OPENTRACE-------[  ]--+                                         +
|                         |                                         |
|  > investigate          |                  $_                     |
|  # logs                 |       start a new investigation         |
|  @ connectors           |   describe an issue or paste an error   |
|  ---------------------- |                                         |
|  + new chat             |                                         |
|  ---------------------- |                                         |
|  > Bug in auth flow     |                                         |
|    2h ago               |                                         |
|                         |                                         |
|  > API latency spike    |                                         |
|    1d ago               |                                         |
|                         |                                         |
|                         |-----------------------------------------|
|                         | [> describe the issue...    ] [run]     |
|                         |           connecting...                  |
+-------------------------+-----------------------------------------+
         280px                        flex: 1 (fluid)
```

After user sends a message:

```
+--OPENTRACE-------[  ]--+                                         +
|                         |                                         |
|  > investigate          |  > api latency spiked in production     |
|  # logs                 |                                         |
|  @ connectors           |  * thinking   Analyzing query...        |
|  ---------------------- |  * sql_query  SELECT avg(latency)...    |
|  + new chat             |  * result     avg: 342ms, p99: 1.2s    |
|  ---------------------- |                                         |
|  > Bug in auth flow     |  I found that the average API latency   |
|    2h ago               |  increased 3x in the last hour. The     |
|                         |  root cause appears to be...            |
|  > API latency spike    |                                         |
|    1d ago               |                                         |
|                         |-----------------------------------------|
|                         | [> follow up question...     ] [run]    |
|                         |          done (7 steps)                  |
+-------------------------+-----------------------------------------+
         280px                        flex: 1 (fluid)
```

Sidebar collapsed:

```
+--+                                                                +
|] |                                                                |
|  |                     $_                                         |
|  |          start a new investigation                             |
|  |      describe an issue or paste an error                       |
|  |                                                                |
|  |                                                                |
|  |                                                                |
|  |                                                                |
|  |----------------------------------------------------------------|
|  | [> describe the issue...                          ] [run]      |
|  |                  connecting...                                  |
+--+----------------------------------------------------------------+
48px                      flex: 1 (fluid, nearly full screen)
```

---

## Verification

1. Build compiles: `go build ./...`
2. Tests pass: `go test -short -race ./internal/web/...`
3. Visual: no top header bar on investigate page — full viewport used
4. Visual: sidebar shows logo, nav tabs (investigate/logs/connectors), chat list
5. Visual: clicking "logs" or "connectors" navigates to those pages (full page load, those pages keep their own header)
6. Visual: sidebar toggles smoothly with `Ctrl+B` or `[` button, state persists via localStorage
7. Visual: `]` expand button appears when sidebar is collapsed
8. Visual: messages render in centered 720px column
9. Visual: input bar glows green on focus, code blocks highlight correctly
10. Visual: timeline steps collapse/expand on click
11. Visual: empty state shows blinking `$_` cursor
12. Keyboard: `Ctrl+N` creates new chat, `Ctrl+B` toggles sidebar, `/` focuses input
13. Responsive: works at 1024px+ width (no mobile target)
