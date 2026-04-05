// Package tui implements the interactive terminal dashboard for OpenTrace.
// Built with bubbletea v2 and the Charm ecosystem.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
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
	view        viewMode
	focusedPanel panel

	// Data
	status      *apiclient.StatusResponse
	logs        []apiclient.LogEntry
	logCursor   int64
	errors      *apiclient.ErrorGroupsResponse
	watches     *apiclient.WatchesResponse
	stats       *apiclient.IngestionStatsResponse

	// Components
	logViewport viewport.Model

	// Filters
	levelFilter   string
	serviceFilter string

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
		config:        cfg,
		view:          viewDashboard,
		focusedPanel:  panelLogs,
		levelFilter:   cfg.Level,
		serviceFilter: cfg.Service,
		logViewport:   viewport.New(),
	}
}

// Messages
type (
	statusMsg      *apiclient.StatusResponse
	logTailMsg     *apiclient.LogTailResponse
	errorsMsg      *apiclient.ErrorGroupsResponse
	watchesMsg     *apiclient.WatchesResponse
	statsMsg       *apiclient.IngestionStatsResponse
	tickMsg        time.Time
	errMsg         error
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
		m.logViewport.SetWidth(m.width - 2)
		logHeight := m.height - m.statsHeight() - 4 // header + status bar + borders
		if logHeight < 3 {
			logHeight = 3
		}
		m.logViewport.SetHeight(logHeight)
		m.updateLogViewport()
		return m, nil

	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, keys.Tab):
			if m.focusedPanel == panelStats {
				m.focusedPanel = panelLogs
			} else {
				m.focusedPanel = panelStats
			}
			return m, nil
		case key.Matches(msg, keys.Errors):
			m.view = viewErrors
			return m, nil
		case key.Matches(msg, keys.Watches):
			m.view = viewWatches
			return m, nil
		case key.Matches(msg, keys.Dashboard):
			m.view = viewDashboard
			return m, nil
		case key.Matches(msg, keys.Help):
			if m.view == viewHelp {
				m.view = viewDashboard
			} else {
				m.view = viewHelp
			}
			return m, nil
		case key.Matches(msg, keys.Escape):
			m.view = viewDashboard
			return m, nil
		case key.Matches(msg, keys.Level):
			m.cycleLevelFilter()
			return m, m.fetchLogTail
		case key.Matches(msg, keys.Service):
			m.cycleServiceFilter()
			return m, m.fetchLogTail
		}

		// Scroll viewport in dashboard view
		if m.view == viewDashboard && m.focusedPanel == panelLogs {
			var cmd tea.Cmd
			m.logViewport, cmd = m.logViewport.Update(msg)
			return m, cmd
		}

		return m, nil

	case statusMsg:
		m.status = msg
		return m, nil

	case logTailMsg:
		if msg != nil {
			m.logs = append(m.logs, msg.Logs...)
			// Cap log buffer at 1000 entries
			if len(m.logs) > 1000 {
				m.logs = m.logs[len(m.logs)-1000:]
			}
			if msg.Cursor > 0 {
				m.logCursor = msg.Cursor
			}
			m.updateLogViewport()
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
			m.fetchStatus,
			m.fetchLogTail,
			m.fetchErrors,
			m.fetchWatches,
			m.fetchStats,
			m.tick(),
		)

	case errMsg:
		m.err = msg
		return m, nil
	}

	return m, nil
}

func (m Model) View() tea.View {
	if m.quitting {
		return tea.NewView("")
	}

	if !m.ready {
		v := tea.NewView(styleHeader.Render("  OpenTrace — loading..."))
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
	}

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// --- Views ---

func (m Model) dashboardView() string {
	var b strings.Builder

	// Header
	b.WriteString(m.headerView())
	b.WriteString("\n")

	// Stats panel
	b.WriteString(m.statsView())
	b.WriteString("\n")

	// Log tail
	logBorder := styleBorder
	if m.focusedPanel == panelLogs {
		logBorder = styleFocusedBorder
	}
	logTitle := " LOGS "
	if m.levelFilter != "" {
		logTitle += fmt.Sprintf("[%s] ", strings.ToUpper(m.levelFilter))
	}
	if m.serviceFilter != "" {
		logTitle += fmt.Sprintf("[%s] ", m.serviceFilter)
	}
	logPanel := logBorder.Width(m.width - 2).Render(
		styleTitle.Render(logTitle) + "\n" + m.logViewport.View(),
	)
	b.WriteString(logPanel)
	b.WriteString("\n")

	// Status bar
	b.WriteString(m.statusBarView())

	return b.String()
}

func (m Model) headerView() string {
	ver := "dev"
	endpoint := ""
	if m.status != nil {
		ver = m.status.Version
	}
	if m.config.Client != nil {
		endpoint = ""
	}

	left := styleHeader.Render(fmt.Sprintf(" OpenTrace v%s", ver))
	right := styleLabel.Render(endpoint)
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if gap < 0 {
		gap = 0
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m Model) statsView() string {
	w := (m.width - 4) / 3
	if w < 10 {
		w = 10
	}

	// Ingestion column
	var ingestion strings.Builder
	ingestion.WriteString(styleTitle.Render("INGESTION") + "\n")
	if m.status != nil && m.status.Logs != nil {
		ingestion.WriteString(styleValue.Render(fmt.Sprintf("%d logs/hr", m.status.Logs.LastHour)))
		ingestion.WriteString("\n")
	} else {
		ingestion.WriteString(styleLabel.Render("—") + "\n")
	}
	if m.stats != nil && len(m.stats.Buckets) > 0 {
		ingestion.WriteString(m.renderSparkline(m.stats.Buckets))
	}

	// Errors column
	var errors strings.Builder
	errors.WriteString(styleTitle.Render("ERRORS (last 1h)") + "\n")
	if m.status != nil && m.status.Logs != nil {
		errors.WriteString(styleLevelError.Render(fmt.Sprintf("%d errors", m.status.Logs.ErrorsLastHour)))
		errors.WriteString("\n")
	} else {
		errors.WriteString(styleLabel.Render("—") + "\n")
	}
	if m.errors != nil && len(m.errors.ErrorGroups) > 0 {
		for i, eg := range m.errors.ErrorGroups {
			if i >= 3 {
				break
			}
			errors.WriteString(fmt.Sprintf("  %s %d\n",
				truncate(eg.ExceptionClass, w-8),
				eg.OccurrenceCount))
		}
	}

	// Watches column
	var watches strings.Builder
	watches.WriteString(styleTitle.Render("WATCHES") + "\n")
	if m.status != nil && m.status.Watches != nil {
		wst := m.status.Watches
		watches.WriteString(fmt.Sprintf("%d active", wst.Active))
		if wst.Triggered > 0 {
			watches.WriteString(styleLevelError.Render(fmt.Sprintf(", %d triggered", wst.Triggered)))
		}
		watches.WriteString("\n")
	} else {
		watches.WriteString(styleLabel.Render("—") + "\n")
	}
	if m.status != nil && m.status.WatchAlerts != nil && m.status.WatchAlerts.Pending > 0 {
		watches.WriteString(styleLevelWarn.Render(fmt.Sprintf("%d pending alerts", m.status.WatchAlerts.Pending)))
		watches.WriteString("\n")
	}

	col1 := lipgloss.NewStyle().Width(w).Render(ingestion.String())
	col2 := lipgloss.NewStyle().Width(w).Render(errors.String())
	col3 := lipgloss.NewStyle().Width(w).Render(watches.String())

	statsBorder := styleBorder
	if m.focusedPanel == panelStats {
		statsBorder = styleFocusedBorder
	}

	return statsBorder.Width(m.width - 2).Render(
		lipgloss.JoinHorizontal(lipgloss.Top, col1, col2, col3),
	)
}

func (m Model) statusBarView() string {
	var hints []string
	hints = append(hints, "q quit")
	hints = append(hints, "tab switch")
	hints = append(hints, "l level")
	hints = append(hints, "s service")
	hints = append(hints, "e errors")
	hints = append(hints, "w watches")
	hints = append(hints, "? help")

	return styleStatusBar.Width(m.width).Render(strings.Join(hints, "  "))
}

func (m Model) errorsView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n\n")
	b.WriteString(styleTitle.Render("  ERROR GROUPS (unresolved)") + "\n\n")

	if m.errors == nil || len(m.errors.ErrorGroups) == 0 {
		b.WriteString(styleLabel.Render("  No error groups found.") + "\n")
	} else {
		for _, eg := range m.errors.ErrorGroups {
			status := styleLevelError.Render(fmt.Sprintf("%4d×", eg.OccurrenceCount))
			b.WriteString(fmt.Sprintf("  %s  %-20s %s\n",
				status,
				truncate(eg.ExceptionClass, 20),
				truncate(eg.Message, m.width-34)))
			b.WriteString(fmt.Sprintf("         %s  last: %s  impact: %.1f\n",
				styleService.Render(eg.Service),
				eg.LastSeenAt.Format("15:04:05"),
				eg.ImpactScore))
		}
	}

	b.WriteString("\n")
	b.WriteString(styleStatusBar.Width(m.width).Render("esc back  d dashboard  q quit"))
	return b.String()
}

func (m Model) watchesView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n\n")
	b.WriteString(styleTitle.Render("  WATCHES") + "\n\n")

	if m.watches == nil || len(m.watches.Watches) == 0 {
		b.WriteString(styleLabel.Render("  No watches configured.") + "\n")
	} else {
		for _, w := range m.watches.Watches {
			statusStyle := styleLabel
			switch w.Status {
			case "triggered":
				statusStyle = styleLevelError
			case "active":
				statusStyle = styleLevelInfo
			}
			b.WriteString(fmt.Sprintf("  %s  %-14s %-10s %s\n",
				statusStyle.Render(padRight(w.Status, 10)),
				w.Conditions,
				w.Service,
				w.Urgency))
		}

		if m.watches.Alerts.Pending > 0 {
			b.WriteString(fmt.Sprintf("\n  %s\n",
				styleLevelWarn.Render(fmt.Sprintf("%d pending alerts", m.watches.Alerts.Pending))))
		}
	}

	b.WriteString("\n")
	b.WriteString(styleStatusBar.Width(m.width).Render("esc back  d dashboard  q quit"))
	return b.String()
}

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n\n")
	b.WriteString(styleTitle.Render("  KEYBOARD SHORTCUTS") + "\n\n")

	helpEntries := []struct{ key, desc string }{
		{"q / ctrl+c", "Quit"},
		{"tab", "Cycle focus between panels"},
		{"l", "Cycle log level filter"},
		{"s", "Cycle service filter"},
		{"e", "Switch to errors view"},
		{"w", "Switch to watches view"},
		{"d", "Switch to dashboard view"},
		{"j/k / ↑/↓", "Scroll log tail"},
		{"esc", "Close overlay / back to dashboard"},
		{"?/h", "Toggle this help"},
	}

	for _, h := range helpEntries {
		b.WriteString(fmt.Sprintf("  %s  %s\n",
			styleValue.Render(padRight(h.key, 14)),
			h.desc))
	}

	b.WriteString("\n")
	b.WriteString(styleStatusBar.Width(m.width).Render("esc back  q quit"))
	return b.String()
}

// --- Helpers ---

func (m Model) statsHeight() int {
	return 8 // approximate height of stats panel + borders
}

func (m *Model) updateLogViewport() {
	var lines []string
	for _, entry := range m.logs {
		ts := entry.Timestamp.Local().Format("15:04:05")
		level := padRight(strings.ToUpper(entry.Level), 5)
		svc := padRight(entry.Service, 12)
		line := fmt.Sprintf("%s %s %s %s",
			ts,
			levelStyle(strings.TrimSpace(level)).Render(level),
			styleService.Render(svc),
			entry.Message)
		lines = append(lines, line)
	}
	m.logViewport.SetContent(strings.Join(lines, "\n"))
	m.logViewport.GotoBottom()
}

func (m *Model) cycleLevelFilter() {
	levels := []string{"", "error", "warn", "info", "debug"}
	for i, l := range levels {
		if l == m.levelFilter {
			m.levelFilter = levels[(i+1)%len(levels)]
			m.logs = nil // clear logs to re-fetch with new filter
			m.logCursor = 0
			return
		}
	}
	m.levelFilter = ""
}

func (m *Model) cycleServiceFilter() {
	// Cycle through known services from status
	if m.status == nil || len(m.status.Services) == 0 {
		m.serviceFilter = ""
		return
	}

	services := make([]string, 0, len(m.status.Services)+1)
	services = append(services, "") // "all" option
	for _, s := range m.status.Services {
		services = append(services, s.Name)
	}

	for i, s := range services {
		if s == m.serviceFilter {
			m.serviceFilter = services[(i+1)%len(services)]
			m.logs = nil
			m.logCursor = 0
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
	max := 0
	for _, b := range buckets {
		if b.Total > max {
			max = b.Total
		}
	}
	if max == 0 {
		return styleSparkline.Render(strings.Repeat(string(blocks[0]), len(buckets)))
	}

	var sb strings.Builder
	for _, b := range buckets {
		idx := b.Total * (len(blocks) - 1) / max
		sb.WriteRune(blocks[idx])
	}
	return styleSparkline.Render(sb.String())
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
	resp, err := m.config.Client.LogTail(m.logCursor, 50, m.levelFilter, m.serviceFilter, "")
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
