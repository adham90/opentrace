// Package tui implements the interactive terminal dashboard for OpenTrace.
package tui

import (
	"fmt"
	"strings"
	"time"

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

	// Data
	status    *apiclient.StatusResponse
	logs      []apiclient.LogEntry
	logCursor int64
	errors    *apiclient.ErrorGroupsResponse
	watches   *apiclient.WatchesResponse
	stats     *apiclient.IngestionStatsResponse

	// Log list
	logIndex    int  // selected log
	logOffset   int  // first visible log
	logExpanded bool // inline detail expanded

	// Filters
	levelFilter   string
	serviceFilter string
	searchFilter  string
	searchActive  bool

	// List views
	errorIndex int
	watchIndex int

	// State
	ready    bool
	err      error
	quitting bool
}

func New(cfg Config) Model {
	if cfg.RefreshRate == 0 {
		cfg.RefreshRate = 5 * time.Second
	}
	return Model{
		config:       cfg,
		view:         viewDashboard,
		focusedPanel: panelLogs,
		levelFilter:  cfg.Level,
		serviceFilter: cfg.Service,
	}
}

// Messages
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
		// Search input mode
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

		// View-specific keys first (so j/k work in any list view)
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
			wasAtBottom := len(m.logs) == 0 || m.logIndex >= len(m.logs)-1
			m.logs = append(m.logs, msg.Logs...)
			if len(m.logs) > 1000 {
				m.logs = m.logs[len(m.logs)-1000:]
			}
			if msg.Cursor > 0 {
				m.logCursor = msg.Cursor
			}
			if wasAtBottom {
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
	h := m.logVisibleLines()
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

func (m Model) logVisibleLines() int {
	// header(1) + stats(~8) + log title(1) + log border(2) + statusbar(1) = 13
	h := m.height - 13
	if h < 3 {
		h = 3
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

// ──────────────────────────────────────────────────────────
// View
// ──────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if !m.ready {
		v := tea.NewView("\n  " + styleTitle.Render("OpenTrace") + " " + styleLabel.Render("connecting..."))
		v.AltScreen = true
		return v
	}
	var s string
	switch m.view {
	case viewDashboard:
		s = m.viewDashboard()
	case viewErrors:
		s = m.viewErrors()
	case viewWatches:
		s = m.viewWatches()
	case viewHelp:
		s = m.viewHelp()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// ──────────────────────────────────────────────────────────
// Dashboard
// ──────────────────────────────────────────────────────────

func (m Model) viewDashboard() string {
	var parts []string
	parts = append(parts, m.renderHeader())
	parts = append(parts, m.renderStats())
	parts = append(parts, m.renderLogPanel())
	parts = append(parts, m.renderStatusBar())
	return strings.Join(parts, "")
}

func (m Model) renderHeader() string {
	ver := "dev"
	if m.status != nil && m.status.Version != "" {
		ver = m.status.Version
	}

	left := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#A78BFA")).
		Render(" ◆ OpenTrace v" + ver)

	var rightParts []string
	if m.levelFilter != "" {
		rightParts = append(rightParts, styleLevelWarn.Render("▪ "+strings.ToUpper(m.levelFilter)))
	}
	if m.serviceFilter != "" {
		rightParts = append(rightParts, lipgloss.NewStyle().Foreground(colorSecondary).Render("▪ "+m.serviceFilter))
	}
	if m.err != nil {
		rightParts = append(rightParts, styleLevelError.Render("⚠ error"))
	}
	right := strings.Join(rightParts, "  ")

	mid := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 1
	if mid < 0 {
		mid = 0
	}
	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("#1a1625")).
		Render(left + strings.Repeat(" ", mid) + right) + "\n"
}

func (m Model) renderStats() string {
	colW := (m.width - 8) / 3
	if colW < 15 {
		colW = 15
	}

	c1 := m.colIngestion(colW)
	c2 := m.colErrors(colW)
	c3 := m.colWatches(colW)

	inner := lipgloss.JoinHorizontal(lipgloss.Top, c1, c2, c3)

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2640")).
		Padding(0, 1).
		Width(m.width - 2)
	if m.focusedPanel == panelStats {
		border = border.BorderForeground(lipgloss.Color("#7c3aed"))
	}

	return border.Render(inner) + "\n"
}

func (m Model) colIngestion(w int) string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("INGESTION"))
	if m.status != nil && m.status.Logs != nil {
		lines = append(lines, styleValue.Render(formatNum(m.status.Logs.LastHour))+" "+styleLabel.Render("logs/hr"))
	} else {
		lines = append(lines, styleLabel.Render("—"))
	}
	if m.stats != nil && len(m.stats.Buckets) > 0 {
		lines = append(lines, m.sparkline(m.stats.Buckets))
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) colErrors(w int) string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("ERRORS"))
	if m.status != nil && m.status.Logs != nil {
		n := m.status.Logs.ErrorsLastHour
		if n > 0 {
			lines = append(lines, styleLevelError.Render(fmt.Sprintf("%d", n))+" "+styleLabel.Render("last 1h"))
		} else {
			lines = append(lines, styleLevelInfo.Render("0")+" "+styleLabel.Render("errors"))
		}
	}
	if m.status != nil && m.status.ErrorGroups != nil && m.status.ErrorGroups.Unresolved > 0 {
		lines = append(lines, styleLevelWarn.Render(fmt.Sprintf("%d", m.status.ErrorGroups.Unresolved))+" "+styleLabel.Render("unresolved"))
	}
	if m.errors != nil {
		for i, eg := range m.errors.ErrorGroups {
			if i >= 3 {
				break
			}
			lines = append(lines, fmt.Sprintf(" %s %s",
				styleLevelError.Render(fmt.Sprintf("%3d", eg.OccurrenceCount)),
				styleLabel.Render(truncate(eg.ExceptionClass, w-7))))
		}
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) colWatches(w int) string {
	var lines []string
	lines = append(lines, lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("WATCHES"))
	if m.status != nil && m.status.Watches != nil {
		ws := m.status.Watches
		line := styleValue.Render(fmt.Sprintf("%d", ws.Active)) + " " + styleLabel.Render("active")
		if ws.Triggered > 0 {
			line += "  " + styleLevelError.Render(fmt.Sprintf("%d triggered", ws.Triggered))
		}
		lines = append(lines, line)
	} else {
		lines = append(lines, styleLabel.Render("—"))
	}
	if m.status != nil && m.status.WatchAlerts != nil && m.status.WatchAlerts.Pending > 0 {
		lines = append(lines, styleLevelWarn.Render(fmt.Sprintf("⚠ %d pending", m.status.WatchAlerts.Pending)))
	}
	if m.status != nil && m.status.Servers != nil && m.status.Servers.Total > 0 {
		s := m.status.Servers
		lines = append(lines, styleLabel.Render(fmt.Sprintf("%d/%d servers up", s.Online, s.Total)))
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(strings.Join(lines, "\n"))
}

func (m Model) renderLogPanel() string {
	maxW := m.width - 6 // border + padding
	visH := m.logVisibleLines()

	// Title
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("LOGS")
	if m.searchActive {
		title += " " + styleLevelWarn.Render("search: "+m.searchFilter+"▌")
	} else if m.searchFilter != "" {
		title += " " + styleLabel.Render("\""+m.searchFilter+"\"")
	}
	if len(m.logs) > 0 {
		title += " " + styleLabel.Render(fmt.Sprintf("(%d)", len(m.logs)))
	}

	// Render visible lines
	var lines []string
	if len(m.logs) == 0 {
		lines = append(lines, styleLabel.Render("  Waiting for logs..."))
	} else {
		end := m.logOffset + visH
		if end > len(m.logs) {
			end = len(m.logs)
		}
		for i := m.logOffset; i < end; i++ {
			lines = append(lines, m.renderLogLine(i, maxW))
			// Inline expand
			if i == m.logIndex && m.logExpanded {
				lines = append(lines, m.renderLogExpanded(m.logs[i], maxW)...)
			}
		}
	}

	// Pad to fill height
	for len(lines) < visH {
		lines = append(lines, "")
	}
	// Trim if expanded made it too long
	if len(lines) > visH {
		lines = lines[:visH]
	}

	content := title + "\n" + strings.Join(lines, "\n")

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2d2640")).
		Padding(0, 1).
		Width(m.width - 2)
	if m.focusedPanel == panelLogs {
		border = border.BorderForeground(lipgloss.Color("#7c3aed"))
	}

	return border.Render(content) + "\n"
}

func (m Model) renderLogLine(idx, maxW int) string {
	e := m.logs[idx]
	sel := idx == m.logIndex && m.focusedPanel == panelLogs

	ts := e.Timestamp.Local().Format("15:04:05")
	lvl := padRight(strings.ToUpper(e.Level), 5)
	svc := padRight(e.Service, 14)

	// Calculate available width for message: total - pointer(2) - ts(8) - space - level(5) - space - svc(14) - space
	msgW := maxW - 32
	if msgW < 10 {
		msgW = 10
	}
	msg := truncate(e.Message, msgW)

	if sel {
		ptr := lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("▸ ")
		return ptr +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca3af")).Render(ts) + " " +
			levelStyle(strings.TrimSpace(lvl)).Bold(true).Render(lvl) + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Render(svc) + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color("#e5e7eb")).Bold(true).Render(msg)
	}

	return "  " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#4b5563")).Render(ts) + " " +
		levelStyle(strings.TrimSpace(lvl)).Render(lvl) + " " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#4b5563")).Render(svc) + " " +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#9ca3af")).Render(msg)
}

func (m Model) renderLogExpanded(e apiclient.LogEntry, maxW int) []string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7280"))
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("#d1d5db"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8"))

	var lines []string
	indent := "     "

	lines = append(lines, indent+dim.Render("ID: ")+val.Render(fmt.Sprintf("%d", e.ID))+
		"   "+dim.Render("Time: ")+val.Render(e.Timestamp.Local().Format("2006-01-02 15:04:05.000")))

	if e.RequestID != "" {
		lines = append(lines, indent+dim.Render("Request: ")+accent.Render(e.RequestID))
	}
	if e.ExceptionClass != "" {
		lines = append(lines, indent+dim.Render("Exception: ")+styleLevelError.Render(e.ExceptionClass))
	}
	if len(e.Metadata) > 0 {
		for k, v := range e.Metadata {
			s := fmt.Sprintf("%v", v)
			lines = append(lines, indent+dim.Render(k+": ")+val.Render(truncate(s, maxW-len(k)-10)))
		}
	}
	lines = append(lines, indent+dim.Render(strings.Repeat("─", 40)))
	return lines
}

func (m Model) renderStatusBar() string {
	var parts []string
	if m.focusedPanel == panelLogs {
		parts = append(parts,
			hk("↑↓")+"scroll",
			hk("enter")+"expand",
			hk("/")+"search",
			hk("l")+"level",
			hk("s")+"svc",
		)
	}
	parts = append(parts, hk("e")+"errors", hk("w")+"watches", hk("?")+"help", hk("q")+"quit")

	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("#1a1625")).
		Foreground(lipgloss.Color("#6b7280")).
		Padding(0, 1).
		Render(strings.Join(parts, "  "))
}

// ──────────────────────────────────────────────────────────
// Errors View
// ──────────────────────────────────────────────────────────

func (m Model) viewErrors() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("  ERROR GROUPS")
	b.WriteString(title + " " + styleLabel.Render("unresolved") + "\n\n")

	if m.errors == nil || len(m.errors.ErrorGroups) == 0 {
		b.WriteString(styleLabel.Render("  No error groups.") + "\n")
	} else {
		for i, eg := range m.errors.ErrorGroups {
			sel := i == m.errorIndex
			ptr := "  "
			if sel {
				ptr = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("▸ ")
			}

			cnt := styleLevelError.Render(fmt.Sprintf("%4d×", eg.OccurrenceCount))
			cls := truncate(eg.ExceptionClass, 28)
			msgW := m.width - 42
			if msgW < 10 {
				msgW = 10
			}

			line1 := fmt.Sprintf("%s%s  %-28s  %s", ptr, cnt, cls,
				styleLabel.Render(truncate(eg.Message, msgW)))
			if sel {
				line1 = lipgloss.NewStyle().Bold(true).Render(line1)
			}
			b.WriteString(line1 + "\n")

			b.WriteString(fmt.Sprintf("        %s  last %s  impact %.1f  users %d\n",
				lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Render(padRight(eg.Service, 16)),
				eg.LastSeenAt.Local().Format("15:04"),
				eg.ImpactScore, eg.UniqueUsers))

			if i < len(m.errors.ErrorGroups)-1 {
				b.WriteString("\n")
			}
		}
	}

	// Fill remaining height
	b.WriteString("\n")
	b.WriteString(m.footerBar("esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Watches View
// ──────────────────────────────────────────────────────────

func (m Model) viewWatches() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("  WATCHES")
	b.WriteString(title + "\n\n")

	if m.watches == nil || len(m.watches.Watches) == 0 {
		b.WriteString(styleLabel.Render("  No watches.") + "\n")
	} else {
		for i, w := range m.watches.Watches {
			sel := i == m.watchIndex
			ptr := "  "
			if sel {
				ptr = lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("▸ ")
			}

			st := styleLabel
			switch w.Status {
			case "triggered":
				st = styleLevelError
			case "active":
				st = styleLevelInfo
			case "expired":
				st = lipgloss.NewStyle().Foreground(colorDim)
			}

			line := fmt.Sprintf("%s%s  %-14s  %-12s  %s",
				ptr, st.Render(padRight(w.Status, 10)),
				w.Conditions, w.Service, styleLabel.Render(w.Urgency))
			if sel {
				line = lipgloss.NewStyle().Bold(true).Render(line)
			}
			b.WriteString(line + "\n")
		}

		if m.watches.Alerts.Pending > 0 {
			b.WriteString(fmt.Sprintf("\n  %s\n",
				styleLevelWarn.Render(fmt.Sprintf("⚠ %d pending alerts", m.watches.Alerts.Pending))))
		}
	}

	b.WriteString("\n")
	b.WriteString(m.footerBar("esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Help View
// ──────────────────────────────────────────────────────────

func (m Model) viewHelp() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#60a5fa")).Render("  KEYBOARD SHORTCUTS")
	b.WriteString(title + "\n\n")

	sections := []struct {
		name  string
		items [][2]string
	}{
		{"Navigation", [][2]string{
			{"↑/k ↓/j", "Move selection"},
			{"enter/space", "Expand/collapse log detail"},
			{"g / G", "Jump to top / bottom"},
			{"tab", "Switch panel focus"},
			{"esc", "Back to dashboard"},
		}},
		{"Views", [][2]string{
			{"d", "Dashboard"}, {"e", "Errors"}, {"w", "Watches"}, {"?/h", "Help"},
		}},
		{"Filters", [][2]string{
			{"/", "Search logs"}, {"l", "Cycle level"}, {"s", "Cycle service"},
		}},
	}

	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b"))
	for _, sec := range sections {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Bold(true).Render(sec.name) + "\n")
		for _, e := range sec.items {
			b.WriteString(fmt.Sprintf("    %s  %s\n", keyStyle.Render(padRight(e[0], 14)), e[1]))
		}
		b.WriteString("\n")
	}

	b.WriteString(m.footerBar("esc back", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Shared renderers
// ──────────────────────────────────────────────────────────

func (m Model) footerBar(hints ...string) string {
	parts := make([]string, len(hints))
	for i, h := range hints {
		parts[i] = styleLabel.Render(h)
	}
	return lipgloss.NewStyle().
		Width(m.width).
		Background(lipgloss.Color("#1a1625")).
		Foreground(lipgloss.Color("#6b7280")).
		Padding(0, 1).
		Render(strings.Join(parts, "    "))
}

func hk(k string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Render(k) + " "
}

func (m Model) sparkline(buckets []apiclient.HistogramBucket) string {
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	maxV := 0
	for _, b := range buckets {
		if b.Total > maxV {
			maxV = b.Total
		}
	}
	if maxV == 0 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#4b5563")).Render(strings.Repeat(string(blocks[0]), len(buckets)))
	}
	var sb strings.Builder
	for _, b := range buckets {
		idx := b.Total * (len(blocks) - 1) / maxV
		sb.WriteRune(blocks[idx])
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#818cf8")).Render(sb.String())
}

// ──────────────────────────────────────────────────────────
// Commands
// ──────────────────────────────────────────────────────────

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.config.RefreshRate, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) fetchStatus() tea.Msg {
	r, err := m.config.Client.Status()
	if err != nil {
		return errMsg(err)
	}
	return statusMsg(r)
}

func (m Model) fetchLogTail() tea.Msg {
	r, err := m.config.Client.LogTail(m.logCursor, 50, m.levelFilter, m.serviceFilter, m.searchFilter)
	if err != nil {
		return errMsg(err)
	}
	return logTailMsg(r)
}

func (m Model) fetchErrors() tea.Msg {
	r, err := m.config.Client.ErrorsTop(10, "1h", m.serviceFilter)
	if err != nil {
		return errMsg(err)
	}
	return errorsMsg(r)
}

func (m Model) fetchWatches() tea.Msg {
	r, err := m.config.Client.Watches()
	if err != nil {
		return errMsg(err)
	}
	return watchesMsg(r)
}

func (m Model) fetchStats() tea.Msg {
	r, err := m.config.Client.IngestionStats("1h", "1m", m.serviceFilter)
	if err != nil {
		return errMsg(err)
	}
	return statsMsg(r)
}

// ──────────────────────────────────────────────────────────
// Utilities
// ──────────────────────────────────────────────────────────

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
	return s[:max-3] + "..."
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
