// Package tui implements the interactive terminal dashboard for OpenTrace.
package tui

import (
	"fmt"
	"strings"
	"time"

	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/adham90/opentrace/internal/apiclient"
)

type viewMode int

const (
	viewDashboard viewMode = iota
	viewErrors
	viewWatches
	viewHelp
)

type panel int

const (
	panelStats panel = iota
	panelLogs
)

// Config holds the TUI configuration.
type Config struct {
	Client      *apiclient.Client
	Service     string
	Level       string
	RefreshRate time.Duration
}

// Model is the root bubbletea model.
type Model struct {
	config Config
	width  int
	height int

	view         viewMode
	focusedPanel panel

	status    *apiclient.StatusResponse
	logs      []apiclient.LogEntry
	logCursor int64
	errors    *apiclient.ErrorGroupsResponse
	watches   *apiclient.WatchesResponse
	stats     *apiclient.IngestionStatsResponse

	logIndex    int
	logOffset   int
	logExpanded bool

	levelFilter   string
	serviceFilter string
	searchFilter  string
	searchActive  bool

	errorIndex int
	watchIndex int

	ready    bool
	err      error
	quitting bool
}

func New(cfg Config) Model {
	if cfg.RefreshRate == 0 {
		cfg.RefreshRate = 5 * time.Second
	}
	return Model{
		config:        cfg,
		view:          viewDashboard,
		focusedPanel:  panelLogs,
		levelFilter:   cfg.Level,
		serviceFilter: cfg.Service,
	}
}

type (
	statusMsg  *apiclient.StatusResponse
	logTailMsg *apiclient.LogTailResponse
	errorsMsg  *apiclient.ErrorGroupsResponse
	watchesMsg *apiclient.WatchesResponse
	statsMsg   *apiclient.IngestionStatsResponse
	tickMsg    time.Time
	errMsg     error
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.fetchStatus, m.fetchLogTail, m.fetchErrors,
		m.fetchWatches, m.fetchStats, m.tick(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		return m, nil

	case tea.KeyPressMsg:
		if m.searchActive {
			switch msg.String() {
			case "enter":
				m.searchActive = false
				m.resetLogs()
				return m, m.fetchLogTail
			case "esc":
				m.searchActive = false
				m.searchFilter = ""
				m.resetLogs()
				return m, m.fetchLogTail
			case "backspace":
				if len(m.searchFilter) > 0 {
					m.searchFilter = m.searchFilter[:len(m.searchFilter)-1]
				}
				return m, nil
			default:
				if len(msg.String()) == 1 {
					m.searchFilter += msg.String()
				}
				return m, nil
			}
		}

		// View-specific navigation FIRST
		switch m.view {
		case viewDashboard:
			if m.focusedPanel == panelLogs {
				switch msg.String() {
				case "j", "down":
					if m.logIndex < len(m.logs)-1 {
						m.logExpanded = false
						m.logIndex++
						m.ensureLogVisible()
					}
					return m, nil
				case "k", "up":
					if m.logIndex > 0 {
						m.logExpanded = false
						m.logIndex--
						m.ensureLogVisible()
					}
					return m, nil
				case "g":
					m.logExpanded = false
					m.logIndex = 0
					m.logOffset = 0
					return m, nil
				case "G":
					if len(m.logs) > 0 {
						m.logExpanded = false
						m.logIndex = len(m.logs) - 1
						m.ensureLogVisible()
					}
					return m, nil
				case "enter", " ":
					m.logExpanded = !m.logExpanded
					return m, nil
				}
			}
		case viewErrors:
			switch msg.String() {
			case "j", "down":
				if m.errors != nil && m.errorIndex < len(m.errors.ErrorGroups)-1 {
					m.errorIndex++
				}
				return m, nil
			case "k", "up":
				if m.errorIndex > 0 {
					m.errorIndex--
				}
				return m, nil
			}
		case viewWatches:
			switch msg.String() {
			case "j", "down":
				if m.watches != nil && m.watchIndex < len(m.watches.Watches)-1 {
					m.watchIndex++
				}
				return m, nil
			case "k", "up":
				if m.watchIndex > 0 {
					m.watchIndex--
				}
				return m, nil
			}
		}

		// Global keys
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "tab":
			if m.view == viewDashboard {
				if m.focusedPanel == panelStats {
					m.focusedPanel = panelLogs
				} else {
					m.focusedPanel = panelStats
				}
			}
			return m, nil
		case "/":
			if m.view == viewDashboard {
				m.searchActive = true
			}
			return m, nil
		case "e":
			m.view = viewErrors
			m.errorIndex = 0
			return m, nil
		case "w":
			m.view = viewWatches
			m.watchIndex = 0
			return m, nil
		case "d":
			m.view = viewDashboard
			return m, nil
		case "?", "h":
			if m.view == viewHelp {
				m.view = viewDashboard
			} else {
				m.view = viewHelp
			}
			return m, nil
		case "esc":
			m.view = viewDashboard
			m.logExpanded = false
			return m, nil
		case "l":
			if m.view == viewDashboard {
				m.cycleLevelFilter()
				return m, m.fetchLogTail
			}
		case "s":
			if m.view == viewDashboard {
				m.cycleServiceFilter()
				return m, m.fetchLogTail
			}
		}
		return m, nil

	case statusMsg:
		m.status = msg
		return m, nil
	case logTailMsg:
		if msg != nil && len(msg.Logs) > 0 {
			atBottom := len(m.logs) == 0 || m.logIndex >= len(m.logs)-1
			m.logs = append(m.logs, msg.Logs...)
			if len(m.logs) > 1000 {
				m.logs = m.logs[len(m.logs)-1000:]
			}
			if msg.Cursor > 0 {
				m.logCursor = msg.Cursor
			}
			if atBottom {
				m.logIndex = len(m.logs) - 1
				m.ensureLogVisible()
			}
		}
		return m, nil
	case errorsMsg:
		m.errors = msg
		return m, nil
	case watchesMsg:
		m.watches = msg
		return m, nil
	case statsMsg:
		m.stats = msg
		return m, nil
	case tickMsg:
		return m, tea.Batch(
			m.fetchStatus, m.fetchLogTail, m.fetchErrors,
			m.fetchWatches, m.fetchStats, m.tick(),
		)
	case errMsg:
		m.err = msg
		return m, nil
	}
	return m, nil
}

func (m *Model) resetLogs() {
	m.logs = nil
	m.logCursor = 0
	m.logIndex = 0
	m.logOffset = 0
	m.logExpanded = false
}

func (m *Model) ensureLogVisible() {
	h := m.logVisibleH()
	if m.logIndex < m.logOffset {
		m.logOffset = m.logIndex
	}
	if m.logIndex >= m.logOffset+h {
		m.logOffset = m.logIndex - h + 1
	}
	if m.logOffset < 0 {
		m.logOffset = 0
	}
}

func (m Model) logVisibleH() int {
	h := m.height - 14
	if h < 3 {
		return 3
	}
	return h
}

func (m *Model) cycleLevelFilter() {
	levels := []string{"", "error", "warn", "info", "debug"}
	for i, l := range levels {
		if l == m.levelFilter {
			m.levelFilter = levels[(i+1)%len(levels)]
			m.resetLogs()
			return
		}
	}
	m.levelFilter = ""
}

func (m *Model) cycleServiceFilter() {
	if m.status == nil || len(m.status.Services) == 0 {
		m.serviceFilter = ""
		return
	}
	services := []string{""}
	for _, s := range m.status.Services {
		services = append(services, s.Name)
	}
	for i, s := range services {
		if s == m.serviceFilter {
			m.serviceFilter = services[(i+1)%len(services)]
			m.resetLogs()
			return
		}
	}
	m.serviceFilter = ""
}

// ═══════════════════════════════════════════════════════════
// VIEW
// ═══════════════════════════════════════════════════════════

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if !m.ready {
		v := tea.NewView("\n " + lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("◆ OpenTrace") +
			styleDim.Render(" connecting..."))
		v.AltScreen = true
		return v
	}

	var s string
	switch m.view {
	case viewDashboard:
		s = m.dashView()
	case viewErrors:
		s = m.errView()
	case viewWatches:
		s = m.watchView()
	case viewHelp:
		s = m.helpView()
	}

	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// ═══════════════════════════════════════════════════════════
// DASHBOARD
// ═══════════════════════════════════════════════════════════

func (m Model) dashView() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString(m.metricsRow())
	b.WriteString(m.logList())
	b.WriteString(m.hotkeys())
	return b.String()
}

func (m Model) header() string {
	ver := "dev"
	uptime := ""
	if m.status != nil {
		if m.status.Version != "" {
			ver = m.status.Version
		}
		uptime = fmtUptime(m.status.UptimeSeconds)
	}

	// Left: branding
	brand := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("◆ OpenTrace")
	version := styleDim.Render(" v" + ver)

	// Center: status pills
	var pills []string
	if m.status != nil && m.status.Database != nil {
		if m.status.Database.Healthy {
			pills = append(pills, pill("DB", "OK", colorGreen))
		} else {
			pills = append(pills, pill("DB", "DOWN", colorRed))
		}
	}
	if m.status != nil && m.status.Servers != nil && m.status.Servers.Total > 0 {
		s := m.status.Servers
		c := colorGreen
		if s.Offline > 0 {
			c = colorYellow
		}
		if s.Online == 0 {
			c = colorRed
		}
		pills = append(pills, pill("SRV", fmt.Sprintf("%d/%d", s.Online, s.Total), c))
	}
	if m.status != nil && m.status.Connectors != nil && m.status.Connectors.Total > 0 {
		pills = append(pills, pill("CONN", fmt.Sprintf("%d", m.status.Connectors.Connected), colorCyan))
	}

	// Right: uptime + filters
	var right []string
	if uptime != "" {
		right = append(right, styleDim.Render("up "+uptime))
	}
	if m.levelFilter != "" {
		right = append(right, lipgloss.NewStyle().Foreground(colorYellow).Render("◉ "+strings.ToUpper(m.levelFilter)))
	}
	if m.serviceFilter != "" {
		right = append(right, lipgloss.NewStyle().Foreground(colorCyan).Render("◉ "+m.serviceFilter))
	}

	left := brand + version
	center := strings.Join(pills, " ")
	rightStr := strings.Join(right, "  ")

	gap1 := (m.width/2 - lipgloss.Width(left) - lipgloss.Width(center)/2)
	if gap1 < 1 {
		gap1 = 1
	}
	gap2 := m.width - lipgloss.Width(left) - gap1 - lipgloss.Width(center) - lipgloss.Width(rightStr) - 1
	if gap2 < 1 {
		gap2 = 1
	}

	return lipgloss.NewStyle().
		Width(m.width).
		Background(colorBg).
		Render(left + strings.Repeat(" ", gap1) + center + strings.Repeat(" ", gap2) + rightStr) + "\n"
}

func (m Model) metricsRow() string {
	colW := (m.width - 8) / 3
	if colW < 16 {
		colW = 16
	}

	c1 := m.metricIngestion(colW)
	c2 := m.metricErrors(colW)
	c3 := m.metricWatches(colW)

	inner := lipgloss.JoinHorizontal(lipgloss.Top, c1, c2, c3)
	return panelStyle(m.focusedPanel == panelStats).Width(m.width - 2).Render(inner) + "\n"
}

func (m Model) metricIngestion(w int) string {
	var lines []string
	lines = append(lines, sectionTitle("INGESTION"))

	if m.status != nil && m.status.Logs != nil {
		n := m.status.Logs.LastHour
		lines = append(lines, bigNumber(formatNum(n), "logs/hr"))
		// Rate bar (normalized to an assumed max of 2000/hr)
		rate := float64(n) / 2000.0
		if rate > 1 {
			rate = 1
		}
		lines = append(lines, miniBar(rate, w-4, "#22d3ee"))
	} else {
		lines = append(lines, styleDim.Render("  No data"))
	}

	if m.stats != nil && len(m.stats.Buckets) > 0 {
		lines = append(lines, m.sparkline(m.stats.Buckets, w-4))
	}

	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) metricErrors(w int) string {
	var lines []string
	lines = append(lines, sectionTitle("ERRORS"))

	if m.status != nil && m.status.Logs != nil {
		n := m.status.Logs.ErrorsLastHour
		if n > 0 {
			lines = append(lines, bigNumber(fmt.Sprintf("%d", n), "last 1h"))
		} else {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorGreen).Render("  ✓")+" "+styleDim.Render("clean"))
		}
	}
	if m.status != nil && m.status.ErrorGroups != nil && m.status.ErrorGroups.Unresolved > 0 {
		u := m.status.ErrorGroups.Unresolved
		lines = append(lines, lipgloss.NewStyle().Foreground(colorOrange).Render(fmt.Sprintf("  %d", u))+" "+styleDim.Render("unresolved"))
	}

	// Top 3 error classes
	if m.errors != nil {
		for i, eg := range m.errors.ErrorGroups {
			if i >= 3 {
				break
			}
			cnt := lipgloss.NewStyle().Foreground(colorRed).Render(fmt.Sprintf(" %3d", eg.OccurrenceCount))
			cls := styleDim.Render(" " + truncate(eg.ExceptionClass, w-8))
			lines = append(lines, cnt+cls)
		}
	}

	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) metricWatches(w int) string {
	var lines []string
	lines = append(lines, sectionTitle("WATCHES"))

	if m.status != nil && m.status.Watches != nil {
		ws := m.status.Watches
		lines = append(lines, bigNumber(fmt.Sprintf("%d", ws.Active), "active"))
		if ws.Triggered > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorRed).Bold(true).
				Render(fmt.Sprintf("  ▲ %d triggered", ws.Triggered)))
		}
	} else {
		lines = append(lines, styleDim.Render("  No watches"))
	}

	if m.status != nil && m.status.WatchAlerts != nil && m.status.WatchAlerts.Pending > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(colorYellow).
			Render(fmt.Sprintf("  ⚠ %d pending", m.status.WatchAlerts.Pending)))
	}

	if m.status != nil && m.status.HealthChecks != nil && m.status.HealthChecks.Total > 0 {
		hc := m.status.HealthChecks
		if hc.Down > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorRed).
				Render(fmt.Sprintf("  ✗ %d/%d checks", hc.Down, hc.Total)))
		} else {
			lines = append(lines, lipgloss.NewStyle().Foreground(colorGreen).
				Render(fmt.Sprintf("  ✓ %d checks", hc.Total)))
		}
	}

	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) logList() string {
	maxW := m.width - 6
	visH := m.logVisibleH()

	// Title with metadata
	title := sectionTitle("LOGS")
	if m.searchActive {
		title += " " + lipgloss.NewStyle().Foreground(colorYellow).Render("/" + m.searchFilter + "▌")
	} else if m.searchFilter != "" {
		title += " " + styleDim.Render("\"" + m.searchFilter + "\"")
	}
	if len(m.logs) > 0 {
		title += " " + styleDim.Render(fmt.Sprintf("(%d)", len(m.logs)))
	}

	// Render visible log lines
	var lines []string
	if len(m.logs) == 0 {
		lines = append(lines, styleDim.Render("  Waiting for logs..."))
	} else {
		end := m.logOffset + visH
		if end > len(m.logs) {
			end = len(m.logs)
		}
		for i := m.logOffset; i < end; i++ {
			lines = append(lines, m.fmtLog(i, maxW))
			if i == m.logIndex && m.logExpanded {
				lines = append(lines, m.fmtLogDetail(m.logs[i], maxW)...)
			}
		}
	}

	for len(lines) < visH {
		lines = append(lines, "")
	}
	if len(lines) > visH {
		lines = lines[:visH]
	}

	content := title + "\n" + strings.Join(lines, "\n")
	return panelStyle(m.focusedPanel == panelLogs).Width(m.width - 2).Render(content) + "\n"
}

func (m Model) fmtLog(idx, maxW int) string {
	e := m.logs[idx]
	sel := idx == m.logIndex && m.focusedPanel == panelLogs

	ts := e.Timestamp.Local().Format("15:04:05")
	lvl := padRight(strings.ToUpper(e.Level), 5)
	svc := padRight(e.Service, 14)

	msgW := maxW - 32
	if msgW < 10 {
		msgW = 10
	}
	msg := truncate(e.Message, msgW)

	if sel {
		ptr := lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("▸ ")
		tsR := lipgloss.NewStyle().Foreground(colorFgDim).Render(ts)
		lvlR := levelStyle(strings.TrimSpace(lvl)).Bold(true).Render(lvl)
		svcR := lipgloss.NewStyle().Foreground(colorCyan).Render(svc)
		msgR := lipgloss.NewStyle().Foreground(colorFg).Bold(true).Render(msg)
		return ptr + tsR + " " + lvlR + " " + svcR + " " + msgR
	}

	tsR := styleDim.Render(ts)
	lvlR := levelStyle(strings.TrimSpace(lvl)).Render(lvl)
	svcR := styleDim.Render(svc)
	msgR := lipgloss.NewStyle().Foreground(colorFgDim).Render(msg)
	return "  " + tsR + " " + lvlR + " " + svcR + " " + msgR
}

func (m Model) fmtLogDetail(e apiclient.LogEntry, maxW int) []string {
	d := styleDim
	v := lipgloss.NewStyle().Foreground(colorFgDim)
	a := lipgloss.NewStyle().Foreground(colorCyan)

	indent := "     "
	sep := indent + styleDim.Render(strings.Repeat("─", 50))

	var lines []string
	lines = append(lines, sep)

	// Row 1: ID + full timestamp
	lines = append(lines,
		indent+d.Render("id ")+v.Render(fmt.Sprintf("%d", e.ID))+
			"  "+d.Render("time ")+v.Render(e.Timestamp.Local().Format("2006-01-02 15:04:05.000")))

	// Row 2: full message (may be long, but truncated to width)
	if len(e.Message) > msgW(maxW) {
		lines = append(lines, indent+d.Render("msg ")+v.Render(truncate(e.Message, maxW-10)))
	}

	if e.RequestID != "" {
		lines = append(lines, indent+d.Render("req ")+a.Render(e.RequestID))
	}
	if e.ExceptionClass != "" {
		lines = append(lines, indent+d.Render("err ")+styleLevelError.Render(e.ExceptionClass))
	}

	// Metadata
	for k, val := range e.Metadata {
		s := fmt.Sprintf("%v", val)
		lines = append(lines, indent+d.Render(k+" ")+v.Render(truncate(s, maxW-len(k)-10)))
	}

	lines = append(lines, sep)
	return lines
}

func msgW(maxW int) int {
	w := maxW - 32
	if w < 10 {
		return 10
	}
	return w
}

func (m Model) hotkeys() string {
	var parts []string
	if m.focusedPanel == panelLogs {
		parts = append(parts,
			hk("↑↓")+"scroll",
			hk("⏎")+"expand",
			hk("/")+"search",
			hk("l")+"level",
			hk("s")+"svc",
		)
	} else {
		parts = append(parts, hk("tab")+"logs")
	}
	parts = append(parts, hk("e")+"errors", hk("w")+"watches", hk("?")+"help", hk("q")+"quit")

	return lipgloss.NewStyle().
		Width(m.width).
		Background(colorBg).
		Foreground(colorFgMuted).
		Padding(0, 1).
		Render(strings.Join(parts, "  "))
}

// ═══════════════════════════════════════════════════════════
// ERRORS VIEW
// ═══════════════════════════════════════════════════════════

func (m Model) errView() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(" " + sectionTitle("ERROR GROUPS") + " " + styleDim.Render("unresolved") + "\n\n")

	if m.errors == nil || len(m.errors.ErrorGroups) == 0 {
		b.WriteString(styleDim.Render("  No errors. ✓") + "\n")
	} else {
		for i, eg := range m.errors.ErrorGroups {
			sel := i == m.errorIndex
			ptr := "  "
			if sel {
				ptr = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("▸ ")
			}

			cnt := lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(fmt.Sprintf("%4d×", eg.OccurrenceCount))
			cls := truncate(eg.ExceptionClass, 28)
			mW := m.width - 42
			if mW < 10 {
				mW = 10
			}

			line1 := fmt.Sprintf("%s%s  %-28s  %s", ptr, cnt, cls,
				styleDim.Render(truncate(eg.Message, mW)))
			if sel {
				line1 = lipgloss.NewStyle().Bold(true).Render(line1)
			}
			b.WriteString(line1 + "\n")

			meta := fmt.Sprintf("        %s  %s  impact %s  users %s",
				lipgloss.NewStyle().Foreground(colorCyan).Render(padRight(eg.Service, 16)),
				styleDim.Render("last "+eg.LastSeenAt.Local().Format("15:04")),
				lipgloss.NewStyle().Foreground(colorOrange).Render(fmt.Sprintf("%.1f", eg.ImpactScore)),
				lipgloss.NewStyle().Foreground(colorBlue).Render(fmt.Sprintf("%d", eg.UniqueUsers)))
			b.WriteString(meta + "\n")

			if i < len(m.errors.ErrorGroups)-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(footer(m.width, "esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ═══════════════════════════════════════════════════════════
// WATCHES VIEW
// ═══════════════════════════════════════════════════════════

func (m Model) watchView() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(" " + sectionTitle("WATCHES") + "\n\n")

	if m.watches == nil || len(m.watches.Watches) == 0 {
		b.WriteString(styleDim.Render("  No watches configured.") + "\n")
	} else {
		for i, w := range m.watches.Watches {
			sel := i == m.watchIndex
			ptr := "  "
			if sel {
				ptr = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("▸ ")
			}

			st := styleDim
			switch w.Status {
			case "triggered":
				st = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
			case "active":
				st = lipgloss.NewStyle().Foreground(colorGreen)
			}

			line := fmt.Sprintf("%s%s  %-14s  %-12s  %s",
				ptr, st.Render(padRight(w.Status, 10)),
				w.Conditions, w.Service,
				styleDim.Render(w.Urgency))
			if sel {
				line = lipgloss.NewStyle().Bold(true).Render(line)
			}
			b.WriteString(line + "\n")
		}

		if m.watches.Alerts.Pending > 0 {
			b.WriteString(fmt.Sprintf("\n  %s\n",
				lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("⚠ %d pending alerts", m.watches.Alerts.Pending))))
		}
	}

	b.WriteString("\n")
	b.WriteString(footer(m.width, "esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ═══════════════════════════════════════════════════════════
// HELP VIEW
// ═══════════════════════════════════════════════════════════

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(" " + sectionTitle("KEYBOARD SHORTCUTS") + "\n\n")

	sections := []struct {
		name  string
		items [][2]string
	}{
		{"Navigation", [][2]string{
			{"↑/k  ↓/j", "Move selection"},
			{"enter/space", "Expand/collapse log inline"},
			{"g / G", "Jump to top / bottom"},
			{"tab", "Switch panel focus"},
			{"esc", "Back to dashboard"},
		}},
		{"Views", [][2]string{
			{"d", "Dashboard"}, {"e", "Errors"}, {"w", "Watches"}, {"?/h", "Help"},
		}},
		{"Filters", [][2]string{
			{"/", "Search logs"}, {"l", "Cycle log level"}, {"s", "Cycle service"},
		}},
	}

	keyCol := lipgloss.NewStyle().Foreground(colorYellow)
	for _, sec := range sections {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render(sec.name) + "\n")
		for _, e := range sec.items {
			b.WriteString(fmt.Sprintf("    %s  %s\n", keyCol.Render(padRight(e[0], 14)), e[1]))
		}
		b.WriteString("\n")
	}

	b.WriteString(footer(m.width, "esc back", "q quit"))
	return b.String()
}

// ═══════════════════════════════════════════════════════════
// SHARED COMPONENTS
// ═══════════════════════════════════════════════════════════

func sectionTitle(s string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render(s)
}

func bigNumber(num, label string) string {
	return "  " + lipgloss.NewStyle().Bold(true).Foreground(colorFg).Render(num) +
		" " + styleDim.Render(label)
}

func pill(label, value string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(label + ":" + value)
}

func hk(k string) string {
	return lipgloss.NewStyle().Foreground(colorYellow).Render(k) + " "
}

func footer(w int, hints ...string) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = styleDim.Render(h)
	}
	return lipgloss.NewStyle().
		Width(w).Background(colorBg).Foreground(colorFgMuted).Padding(0, 1).
		Render(strings.Join(parts, "    "))
}

func (m Model) sparkline(buckets []apiclient.HistogramBucket, w int) string {
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	maxV := 0
	for _, b := range buckets {
		if b.Total > maxV {
			maxV = b.Total
		}
	}
	if maxV == 0 {
		return styleDim.Render(strings.Repeat(string(blocks[0]), min(len(buckets), w)))
	}
	n := len(buckets)
	if n > w {
		n = w
	}
	var sb strings.Builder
	start := len(buckets) - n
	for i := start; i < len(buckets); i++ {
		idx := buckets[i].Total * (len(blocks) - 1) / maxV
		sb.WriteRune(blocks[idx])
	}
	return lipgloss.NewStyle().Foreground(colorCyan).Render(sb.String())
}

func fmtUptime(sec int64) string {
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd%dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// ═══════════════════════════════════════════════════════════
// COMMANDS
// ═══════════════════════════════════════════════════════════

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.config.RefreshRate, func(t time.Time) tea.Msg { return tickMsg(t) })
}
func (m Model) fetchStatus() tea.Msg {
	r, e := m.config.Client.Status()
	if e != nil {
		return errMsg(e)
	}
	return statusMsg(r)
}
func (m Model) fetchLogTail() tea.Msg {
	r, e := m.config.Client.LogTail(m.logCursor, 50, m.levelFilter, m.serviceFilter, m.searchFilter)
	if e != nil {
		return errMsg(e)
	}
	return logTailMsg(r)
}
func (m Model) fetchErrors() tea.Msg {
	r, e := m.config.Client.ErrorsTop(10, "1h", m.serviceFilter)
	if e != nil {
		return errMsg(e)
	}
	return errorsMsg(r)
}
func (m Model) fetchWatches() tea.Msg {
	r, e := m.config.Client.Watches()
	if e != nil {
		return errMsg(e)
	}
	return watchesMsg(r)
}
func (m Model) fetchStats() tea.Msg {
	r, e := m.config.Client.IngestionStats("1h", "1m", m.serviceFilter)
	if e != nil {
		return errMsg(e)
	}
	return statsMsg(r)
}

// ═══════════════════════════════════════════════════════════
// UTILITIES
// ═══════════════════════════════════════════════════════════

func truncate(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "…"
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

func formatNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
