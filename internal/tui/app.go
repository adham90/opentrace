// Package tui implements the interactive terminal dashboard for OpenTrace.
// Built with bubbletea v2 and the Charm ecosystem.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"

	"github.com/adham90/opentrace/internal/apiclient"
)

// viewMode tracks which view is active.
type viewMode int

const (
	viewDashboard viewMode = iota
	viewErrors
	viewWatches
	viewHelp
	viewLogDetail
)

// panel tracks which panel is focused in the dashboard view.
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

	// Current view
	view         viewMode
	focusedPanel panel

	// Data
	status  *apiclient.StatusResponse
	logs    []apiclient.LogEntry
	logCursor int64
	errors  *apiclient.ErrorGroupsResponse
	watches *apiclient.WatchesResponse
	stats   *apiclient.IngestionStatsResponse

	// Log list state (custom selection, no viewport)
	logIndex  int // selected log index
	logOffset int // scroll offset (first visible line)

	// Filters
	levelFilter   string
	serviceFilter string
	searchFilter  string
	searchActive  bool

	// Selection indices for list views
	errorIndex int
	watchIndex int

	// Log detail
	selectedLog *apiclient.LogEntry

	// State
	ready    bool
	err      error
	quitting bool
}

// New creates a new TUI model.
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
		m.fetchStatus,
		m.fetchLogTail,
		m.fetchErrors,
		m.fetchWatches,
		m.fetchStats,
		m.tick(),
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
		return m.handleKey(msg)

	case statusMsg:
		m.status = msg
		return m, nil

	case logTailMsg:
		if msg != nil {
			prevLen := len(m.logs)
			m.logs = append(m.logs, msg.Logs...)
			if len(m.logs) > 1000 {
				m.logs = m.logs[len(m.logs)-1000:]
			}
			if msg.Cursor > 0 {
				m.logCursor = msg.Cursor
			}
			// Auto-scroll to bottom if we were at the bottom
			if prevLen == 0 || m.logIndex >= prevLen-1 {
				m.logIndex = len(m.logs) - 1
				m.scrollLogsToSelection()
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

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Search input mode
	if m.searchActive {
		switch msg.String() {
		case "enter":
			m.searchActive = false
			m.logs = nil
			m.logCursor = 0
			m.logIndex = 0
			m.logOffset = 0
			return m, m.fetchLogTail
		case "esc":
			m.searchActive = false
			m.searchFilter = ""
			m.logs = nil
			m.logCursor = 0
			m.logIndex = 0
			m.logOffset = 0
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

	// Global keys
	switch {
	case key.Matches(msg, keys.Quit):
		m.quitting = true
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		if m.view == viewHelp {
			m.view = viewDashboard
		} else {
			m.view = viewHelp
		}
		return m, nil
	case key.Matches(msg, keys.Escape):
		if m.view != viewDashboard {
			m.view = viewDashboard
			m.selectedLog = nil
		}
		return m, nil
	case key.Matches(msg, keys.Dashboard):
		m.view = viewDashboard
		return m, nil
	case key.Matches(msg, keys.Errors):
		m.view = viewErrors
		m.errorIndex = 0
		return m, nil
	case key.Matches(msg, keys.Watches):
		m.view = viewWatches
		m.watchIndex = 0
		return m, nil
	case key.Matches(msg, keys.Filter):
		if m.view == viewDashboard {
			m.searchActive = true
		}
		return m, nil
	case key.Matches(msg, keys.Level):
		if m.view == viewDashboard {
			m.cycleLevelFilter()
			return m, m.fetchLogTail
		}
	case key.Matches(msg, keys.Service):
		if m.view == viewDashboard {
			m.cycleServiceFilter()
			return m, m.fetchLogTail
		}
	case key.Matches(msg, keys.Tab):
		if m.view == viewDashboard {
			if m.focusedPanel == panelStats {
				m.focusedPanel = panelLogs
			} else {
				m.focusedPanel = panelStats
			}
		}
		return m, nil
	}

	// View-specific navigation
	switch m.view {
	case viewDashboard:
		if m.focusedPanel == panelLogs {
			return m.handleLogNav(msg)
		}
	case viewErrors:
		return m.handleListNav(msg, &m.errorIndex, m.errorCount())
	case viewWatches:
		return m.handleListNav(msg, &m.watchIndex, m.watchCount())
	}

	return m, nil
}

func (m Model) handleLogNav(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Up):
		if m.logIndex > 0 {
			m.logIndex--
			m.scrollLogsToSelection()
		}
	case key.Matches(msg, keys.Down):
		if m.logIndex < len(m.logs)-1 {
			m.logIndex++
			m.scrollLogsToSelection()
		}
	case key.Matches(msg, keys.Top):
		m.logIndex = 0
		m.logOffset = 0
	case key.Matches(msg, keys.Bottom):
		if len(m.logs) > 0 {
			m.logIndex = len(m.logs) - 1
			m.scrollLogsToSelection()
		}
	}

	if msg.String() == "enter" && len(m.logs) > 0 && m.logIndex < len(m.logs) {
		entry := m.logs[m.logIndex]
		m.selectedLog = &entry
		m.view = viewLogDetail
	}

	return m, nil
}

func (m Model) handleListNav(msg tea.KeyPressMsg, idx *int, count int) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Up):
		if *idx > 0 {
			*idx--
		}
	case key.Matches(msg, keys.Down):
		if *idx < count-1 {
			*idx++
		}
	}
	return m, nil
}

func (m *Model) scrollLogsToSelection() {
	visibleH := m.logPanelHeight()
	if m.logIndex < m.logOffset {
		m.logOffset = m.logIndex
	}
	if m.logIndex >= m.logOffset+visibleH {
		m.logOffset = m.logIndex - visibleH + 1
	}
}

func (m Model) logPanelHeight() int {
	h := m.height - m.statsHeight() - 5 // header + title + borders + status bar
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) errorCount() int {
	if m.errors == nil {
		return 0
	}
	return len(m.errors.ErrorGroups)
}

func (m Model) watchCount() int {
	if m.watches == nil {
		return 0
	}
	return len(m.watches.Watches)
}

// ──────────────────────────────────────────────────────────
// View
// ──────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}
	if !m.ready {
		v := tea.NewView("\n  " + styleTitle.Render("OpenTrace") + styleLabel.Render(" connecting..."))
		v.AltScreen = true
		return v
	}

	var content string
	switch m.view {
	case viewDashboard:
		content = m.dashboardView()
	case viewErrors:
		content = m.errorsView()
	case viewWatches:
		content = m.watchesView()
	case viewHelp:
		content = m.helpView()
	case viewLogDetail:
		content = m.logDetailView()
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// ──────────────────────────────────────────────────────────
// Dashboard
// ──────────────────────────────────────────────────────────

func (m Model) dashboardView() string {
	var b strings.Builder
	b.WriteString(m.headerBar())
	b.WriteString(m.statsPanel())
	b.WriteString(m.logPanel())
	b.WriteString(m.statusBar())
	return b.String()
}

func (m Model) headerBar() string {
	ver := "dev"
	if m.status != nil {
		ver = m.status.Version
	}

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#C084FC")).
		Render(fmt.Sprintf(" ◆ OpenTrace v%s", ver))

	// Right side: filters + connection info
	var filters []string
	if m.levelFilter != "" {
		filters = append(filters, lipgloss.NewStyle().
			Foreground(colorWarning).
			Render(strings.ToUpper(m.levelFilter)))
	}
	if m.serviceFilter != "" {
		filters = append(filters, lipgloss.NewStyle().
			Foreground(colorSecondary).
			Render(m.serviceFilter))
	}
	right := ""
	if len(filters) > 0 {
		right = styleLabel.Render("filters: ") + strings.Join(filters, " ")
	}

	gap := m.width - lipgloss.Width(title) - lipgloss.Width(right) - 1
	if gap < 0 {
		gap = 0
	}

	bar := lipgloss.NewStyle().
		Background(lipgloss.Color("#1E1B2E")).
		Width(m.width).
		Render(title + strings.Repeat(" ", gap) + right + " ")

	return bar + "\n"
}

func (m Model) statsPanel() string {
	w := (m.width - 6) / 3
	if w < 12 {
		w = 12
	}

	col1 := m.ingestionCol(w)
	col2 := m.errorsCol(w)
	col3 := m.watchesCol(w)

	inner := lipgloss.JoinHorizontal(lipgloss.Top, col1, col2, col3)

	border := styleBorder.Padding(0, 1)
	if m.focusedPanel == panelStats {
		border = styleFocusedBorder.Padding(0, 1)
	}

	return border.Width(m.width - 2).Render(inner) + "\n"
}

func (m Model) ingestionCol(w int) string {
	var s strings.Builder
	s.WriteString(styleTitle.Render("INGESTION") + "\n")
	if m.status != nil && m.status.Logs != nil {
		count := m.status.Logs.LastHour
		s.WriteString(styleValue.Render(fmt.Sprintf("%s", formatNum(count))))
		s.WriteString(styleLabel.Render(" logs/hr") + "\n")
	} else {
		s.WriteString(styleLabel.Render("  —") + "\n")
	}
	if m.stats != nil && len(m.stats.Buckets) > 0 {
		s.WriteString(m.renderSparkline(m.stats.Buckets) + "\n")
	}
	if m.status != nil && m.status.Connectors != nil && m.status.Connectors.Total > 0 {
		s.WriteString(styleLabel.Render(fmt.Sprintf("%d connectors", m.status.Connectors.Connected)) + "\n")
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(s.String())
}

func (m Model) errorsCol(w int) string {
	var s strings.Builder
	s.WriteString(styleTitle.Render("ERRORS") + "\n")
	if m.status != nil && m.status.Logs != nil && m.status.Logs.ErrorsLastHour > 0 {
		s.WriteString(styleLevelError.Bold(true).Render(fmt.Sprintf("%d", m.status.Logs.ErrorsLastHour)))
		s.WriteString(styleLabel.Render(" in last 1h") + "\n")
	} else {
		s.WriteString(styleLevelInfo.Render("0") + styleLabel.Render(" errors") + "\n")
	}
	if m.status != nil && m.status.ErrorGroups != nil && m.status.ErrorGroups.Unresolved > 0 {
		s.WriteString(styleLevelWarn.Render(fmt.Sprintf("%d", m.status.ErrorGroups.Unresolved)))
		s.WriteString(styleLabel.Render(" unresolved") + "\n")
	}
	// Top error classes
	if m.errors != nil {
		for i, eg := range m.errors.ErrorGroups {
			if i >= 3 {
				break
			}
			s.WriteString(fmt.Sprintf(" %s %s\n",
				styleLevelError.Render(fmt.Sprintf("%3d", eg.OccurrenceCount)),
				styleLabel.Render(truncate(eg.ExceptionClass, w-7))))
		}
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(s.String())
}

func (m Model) watchesCol(w int) string {
	var s strings.Builder
	s.WriteString(styleTitle.Render("WATCHES") + "\n")
	if m.status != nil && m.status.Watches != nil {
		ws := m.status.Watches
		s.WriteString(styleValue.Render(fmt.Sprintf("%d", ws.Active)))
		s.WriteString(styleLabel.Render(" active"))
		if ws.Triggered > 0 {
			s.WriteString(styleLevelError.Render(fmt.Sprintf("  %d triggered", ws.Triggered)))
		}
		s.WriteString("\n")
	} else {
		s.WriteString(styleLabel.Render("  —") + "\n")
	}
	if m.status != nil && m.status.WatchAlerts != nil && m.status.WatchAlerts.Pending > 0 {
		s.WriteString(styleLevelWarn.Render(fmt.Sprintf("⚠ %d", m.status.WatchAlerts.Pending)))
		s.WriteString(styleLabel.Render(" pending alerts") + "\n")
	}
	if m.status != nil && m.status.Servers != nil && m.status.Servers.Total > 0 {
		srv := m.status.Servers
		s.WriteString(styleLabel.Render(fmt.Sprintf("%d/%d servers up", srv.Online, srv.Total)) + "\n")
	}
	if m.status != nil && m.status.HealthChecks != nil && m.status.HealthChecks.Total > 0 {
		hc := m.status.HealthChecks
		if hc.Down > 0 {
			s.WriteString(styleLevelError.Render(fmt.Sprintf("%d/%d checks down", hc.Down, hc.Total)) + "\n")
		} else {
			s.WriteString(styleLevelInfo.Render(fmt.Sprintf("%d checks OK", hc.Total)) + "\n")
		}
	}
	return lipgloss.NewStyle().Width(w).Padding(0, 1).Render(s.String())
}

func (m Model) logPanel() string {
	visibleH := m.logPanelHeight()

	// Title line
	titleParts := []string{styleTitle.Render("LOGS")}
	if m.searchActive {
		titleParts = append(titleParts, styleLevelWarn.Render(fmt.Sprintf(" /: %s▌", m.searchFilter)))
	} else if m.searchFilter != "" {
		titleParts = append(titleParts, styleLabel.Render(fmt.Sprintf(" search: \"%s\"", m.searchFilter)))
	}
	if len(m.logs) > 0 {
		titleParts = append(titleParts, styleLabel.Render(fmt.Sprintf("  (%d entries)", len(m.logs))))
	}
	title := strings.Join(titleParts, "")

	// Build visible log lines with selection cursor
	var lines []string
	if len(m.logs) == 0 {
		lines = append(lines, styleLabel.Render("  waiting for logs..."))
	} else {
		end := m.logOffset + visibleH
		if end > len(m.logs) {
			end = len(m.logs)
		}
		for i := m.logOffset; i < end; i++ {
			entry := m.logs[i]
			selected := i == m.logIndex && m.focusedPanel == panelLogs

			ts := entry.Timestamp.Local().Format("15:04:05")
			level := padRight(strings.ToUpper(entry.Level), 5)
			svc := padRight(entry.Service, 14)

			var line string
			if selected {
				pointer := lipgloss.NewStyle().Foreground(colorHighlight).Bold(true).Render("▸ ")
				line = pointer +
					lipgloss.NewStyle().Foreground(colorMuted).Render(ts) + " " +
					levelStyle(strings.TrimSpace(level)).Bold(true).Render(level) + " " +
					lipgloss.NewStyle().Foreground(colorSecondary).Render(svc) + " " +
					lipgloss.NewStyle().Bold(true).Render(truncate(entry.Message, m.width-30))
			} else {
				line = "  " +
					lipgloss.NewStyle().Foreground(colorDim).Render(ts) + " " +
					levelStyle(strings.TrimSpace(level)).Render(level) + " " +
					styleService.Render(svc) + " " +
					truncate(entry.Message, m.width-30)
			}
			lines = append(lines, line)
		}
	}

	// Pad with empty lines to fill height
	for len(lines) < visibleH {
		lines = append(lines, "")
	}

	content := title + "\n" + strings.Join(lines, "\n")

	border := styleBorder.Padding(0, 1)
	if m.focusedPanel == panelLogs {
		border = styleFocusedBorder.Padding(0, 1)
	}

	return border.Width(m.width - 2).Render(content) + "\n"
}

func (m Model) statusBar() string {
	var left []string
	if m.focusedPanel == panelLogs {
		left = append(left,
			hintKey("↑↓")+hintDesc("scroll"),
			hintKey("enter")+hintDesc("detail"),
			hintKey("/")+hintDesc("search"),
			hintKey("l")+hintDesc("level"),
			hintKey("s")+hintDesc("service"),
		)
	}
	left = append(left,
		hintKey("e")+hintDesc("errors"),
		hintKey("w")+hintDesc("watches"),
		hintKey("?")+hintDesc("help"),
		hintKey("q")+hintDesc("quit"),
	)

	return lipgloss.NewStyle().
		Foreground(colorMuted).
		Background(lipgloss.Color("#1E1B2E")).
		Width(m.width).
		Padding(0, 1).
		Render(strings.Join(left, "  "))
}

// ──────────────────────────────────────────────────────────
// Errors View
// ──────────────────────────────────────────────────────────

func (m Model) errorsView() string {
	var b strings.Builder
	b.WriteString(m.headerBar())
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("  ERROR GROUPS") + styleLabel.Render(" (unresolved)") + "\n\n")

	if m.errors == nil || len(m.errors.ErrorGroups) == 0 {
		b.WriteString(styleLabel.Render("  No error groups found.") + "\n")
	} else {
		for i, eg := range m.errors.ErrorGroups {
			selected := i == m.errorIndex

			pointer := "  "
			if selected {
				pointer = lipgloss.NewStyle().Foreground(colorHighlight).Bold(true).Render("▸ ")
			}

			count := styleLevelError.Render(fmt.Sprintf("%4d×", eg.OccurrenceCount))
			class := truncate(eg.ExceptionClass, 28)
			msgW := m.width - 40
			if msgW < 10 {
				msgW = 10
			}

			firstLine := fmt.Sprintf("%s%s  %-28s  %s",
				pointer, count, class, styleLabel.Render(truncate(eg.Message, msgW)))
			if selected {
				firstLine = lipgloss.NewStyle().Bold(true).Render(firstLine)
			}
			b.WriteString(firstLine + "\n")

			b.WriteString(fmt.Sprintf("        %s  last %s  impact %.1f  users %d\n",
				lipgloss.NewStyle().Foreground(colorSecondary).Render(padRight(eg.Service, 18)),
				eg.LastSeenAt.Local().Format("15:04"),
				eg.ImpactScore,
				eg.UniqueUsers))

			if i < len(m.errors.ErrorGroups)-1 {
				b.WriteString("\n")
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(m.secondaryStatusBar("esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Watches View
// ──────────────────────────────────────────────────────────

func (m Model) watchesView() string {
	var b strings.Builder
	b.WriteString(m.headerBar())
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("  WATCHES") + "\n\n")

	if m.watches == nil || len(m.watches.Watches) == 0 {
		b.WriteString(styleLabel.Render("  No watches configured.") + "\n")
	} else {
		for i, w := range m.watches.Watches {
			selected := i == m.watchIndex

			pointer := "  "
			if selected {
				pointer = lipgloss.NewStyle().Foreground(colorHighlight).Bold(true).Render("▸ ")
			}

			statusStyle := styleLabel
			switch w.Status {
			case "triggered":
				statusStyle = styleLevelError
			case "active":
				statusStyle = styleLevelInfo
			case "expired":
				statusStyle = lipgloss.NewStyle().Foreground(colorDim)
			}

			line := fmt.Sprintf("%s%s  %-14s  %-12s  %s",
				pointer,
				statusStyle.Render(padRight(w.Status, 10)),
				w.Conditions,
				w.Service,
				styleLabel.Render(string(w.Urgency)))
			if selected {
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
	b.WriteString(m.secondaryStatusBar("esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Help View
// ──────────────────────────────────────────────────────────

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(m.headerBar())
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("  KEYBOARD SHORTCUTS") + "\n\n")

	sections := []struct {
		title   string
		entries []struct{ key, desc string }
	}{
		{"Navigation", []struct{ key, desc string }{
			{"tab", "Switch focus between panels"},
			{"↑/k  ↓/j", "Move selection up/down"},
			{"g / G", "Jump to top / bottom"},
			{"enter", "Open detail view"},
			{"esc", "Back to dashboard"},
		}},
		{"Views", []struct{ key, desc string }{
			{"d", "Dashboard"},
			{"e", "Errors"},
			{"w", "Watches"},
			{"?/h", "Help"},
		}},
		{"Filters", []struct{ key, desc string }{
			{"/", "Search logs (type then Enter)"},
			{"l", "Cycle log level filter"},
			{"s", "Cycle service filter"},
		}},
		{"General", []struct{ key, desc string }{
			{"q / ctrl+c", "Quit"},
		}},
	}

	for _, sec := range sections {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(colorSecondary).Bold(true).Render(sec.title) + "\n")
		for _, e := range sec.entries {
			b.WriteString(fmt.Sprintf("    %s  %s\n",
				lipgloss.NewStyle().Foreground(colorHighlight).Render(padRight(e.key, 12)),
				e.desc))
		}
		b.WriteString("\n")
	}

	b.WriteString(m.secondaryStatusBar("esc back", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Log Detail View
// ──────────────────────────────────────────────────────────

func (m Model) logDetailView() string {
	var b strings.Builder
	b.WriteString(m.headerBar())
	b.WriteString("\n")
	b.WriteString(styleTitle.Render("  LOG DETAIL") + "\n\n")

	if m.selectedLog == nil {
		b.WriteString(styleLabel.Render("  No log selected.") + "\n")
	} else {
		entry := m.selectedLog

		fields := []struct{ label, value string }{
			{"ID", fmt.Sprintf("%d", entry.ID)},
			{"Timestamp", entry.Timestamp.Local().Format("2006-01-02 15:04:05.000")},
			{"Level", strings.ToUpper(entry.Level)},
			{"Service", entry.Service},
			{"Message", entry.Message},
		}
		if entry.RequestID != "" {
			fields = append(fields, struct{ label, value string }{"Request ID", entry.RequestID})
		}
		if entry.ExceptionClass != "" {
			fields = append(fields, struct{ label, value string }{"Exception", entry.ExceptionClass})
		}

		for _, f := range fields {
			label := lipgloss.NewStyle().Foreground(colorMuted).Width(14).Align(lipgloss.Right).Render(f.label)
			value := f.value
			if f.label == "Level" {
				value = levelStyle(f.value).Render(f.value)
			} else if f.label == "Exception" {
				value = styleLevelError.Render(f.value)
			}
			b.WriteString(fmt.Sprintf("  %s  %s\n", label, value))
		}

		if len(entry.Metadata) > 0 {
			b.WriteString("\n")
			b.WriteString("  " + lipgloss.NewStyle().Foreground(colorSecondary).Bold(true).Render("Metadata") + "\n")
			for k, v := range entry.Metadata {
				label := lipgloss.NewStyle().Foreground(colorMuted).Width(14).Align(lipgloss.Right).Render(k)
				b.WriteString(fmt.Sprintf("  %s  %v\n", label, v))
			}
		}
	}

	b.WriteString("\n")
	b.WriteString(m.secondaryStatusBar("esc back", "d dashboard", "q quit"))
	return b.String()
}

// ──────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────

func (m Model) statsHeight() int {
	return 9
}

func (m *Model) cycleLevelFilter() {
	levels := []string{"", "error", "warn", "info", "debug"}
	for i, l := range levels {
		if l == m.levelFilter {
			m.levelFilter = levels[(i+1)%len(levels)]
			m.logs = nil
			m.logCursor = 0
			m.logIndex = 0
			m.logOffset = 0
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
			m.logs = nil
			m.logCursor = 0
			m.logIndex = 0
			m.logOffset = 0
			return
		}
	}
	m.serviceFilter = ""
}

func (m Model) renderSparkline(buckets []apiclient.HistogramBucket) string {
	if len(buckets) == 0 {
		return ""
	}
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	maxVal := 0
	for _, b := range buckets {
		if b.Total > maxVal {
			maxVal = b.Total
		}
	}
	if maxVal == 0 {
		return styleSparkline.Render(strings.Repeat(string(blocks[0]), len(buckets)))
	}
	var sb strings.Builder
	for _, b := range buckets {
		idx := b.Total * (len(blocks) - 1) / maxVal
		sb.WriteRune(blocks[idx])
	}
	return styleSparkline.Render(sb.String())
}

func (m Model) secondaryStatusBar(hints ...string) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, styleLabel.Render(h))
	}
	return lipgloss.NewStyle().
		Foreground(colorMuted).
		Background(lipgloss.Color("#1E1B2E")).
		Width(m.width).
		Padding(0, 1).
		Render(strings.Join(parts, "    "))
}

func hintKey(k string) string {
	return lipgloss.NewStyle().Foreground(colorHighlight).Render(k) + " "
}

func hintDesc(d string) string {
	return lipgloss.NewStyle().Foreground(colorMuted).Render(d)
}

func formatNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

// --- Commands ---

func (m Model) tick() tea.Cmd {
	return tea.Tick(m.config.RefreshRate, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) fetchStatus() tea.Msg {
	resp, err := m.config.Client.Status()
	if err != nil {
		return errMsg(err)
	}
	return statusMsg(resp)
}

func (m Model) fetchLogTail() tea.Msg {
	resp, err := m.config.Client.LogTail(m.logCursor, 50, m.levelFilter, m.serviceFilter, m.searchFilter)
	if err != nil {
		return errMsg(err)
	}
	return logTailMsg(resp)
}

func (m Model) fetchErrors() tea.Msg {
	resp, err := m.config.Client.ErrorsTop(10, "1h", m.serviceFilter)
	if err != nil {
		return errMsg(err)
	}
	return errorsMsg(resp)
}

func (m Model) fetchWatches() tea.Msg {
	resp, err := m.config.Client.Watches()
	if err != nil {
		return errMsg(err)
	}
	return watchesMsg(resp)
}

func (m Model) fetchStats() tea.Msg {
	resp, err := m.config.Client.IngestionStats("1h", "1m", m.serviceFilter)
	if err != nil {
		return errMsg(err)
	}
	return statsMsg(resp)
}

func truncate(s string, max int) string {
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
