// Package tui implements the interactive terminal dashboard for OpenTrace.
package tui

import (
	"encoding/json"
	"fmt"
	"image/color"
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

type Config struct {
	Client      *apiclient.Client
	Service     string
	Level       string
	RefreshRate time.Duration
}

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

	logIndex       int
	logOffset      int
	logExpanded    bool
	expandedDetail *apiclient.LogEntry

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
		config: cfg, view: viewDashboard, focusedPanel: panelLogs,
		levelFilter: cfg.Level, serviceFilter: cfg.Service,
	}
}

type (
	statusMsg    *apiclient.StatusResponse
	logTailMsg   *apiclient.LogTailResponse
	errorsMsg    *apiclient.ErrorGroupsResponse
	watchesMsg   *apiclient.WatchesResponse
	statsMsg     *apiclient.IngestionStatsResponse
	logDetailMsg *apiclient.LogEntry
	tickMsg      time.Time
	errMsg       error
)

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchStatus, m.fetchLogTail, m.fetchErrors, m.fetchWatches, m.fetchStats, m.tick())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height, m.ready = msg.Width, msg.Height, true
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
		switch m.view {
		case viewDashboard:
			if m.focusedPanel == panelLogs {
				switch msg.String() {
				case "j", "down":
					if m.logIndex < len(m.logs)-1 {
						m.logExpanded = false
						m.logIndex++
						m.ensureVis()
					}
					return m, nil
				case "k", "up":
					if m.logIndex > 0 {
						m.logExpanded = false
						m.logIndex--
						m.ensureVis()
					}
					return m, nil
				case "g":
					m.logExpanded = false
					m.logIndex, m.logOffset = 0, 0
					return m, nil
				case "G":
					if len(m.logs) > 0 {
						m.logExpanded = false
						m.logIndex = len(m.logs) - 1
						m.ensureVis()
					}
					return m, nil
				case "enter", " ":
					m.logExpanded = !m.logExpanded
					if m.logExpanded && m.logIndex < len(m.logs) {
						m.expandedDetail = nil
						return m, m.fetchLogDetail(m.logs[m.logIndex].ID)
					}
					m.expandedDetail = nil
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
			m.view, m.errorIndex = viewErrors, 0
			return m, nil
		case "w":
			m.view, m.watchIndex = viewWatches, 0
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
			m.view, m.logExpanded = viewDashboard, false
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
	case logTailMsg:
		if msg != nil && len(msg.Logs) > 0 {
			atBot := len(m.logs) == 0 || m.logIndex >= len(m.logs)-1
			m.logs = append(m.logs, msg.Logs...)
			if len(m.logs) > 1000 {
				m.logs = m.logs[len(m.logs)-1000:]
			}
			if msg.Cursor > 0 {
				m.logCursor = msg.Cursor
			}
			if atBot {
				m.logIndex = len(m.logs) - 1
				m.ensureVis()
			}
		}
	case logDetailMsg:
		m.expandedDetail = msg
	case errorsMsg:
		m.errors = msg
	case watchesMsg:
		m.watches = msg
	case statsMsg:
		m.stats = msg
	case tickMsg:
		return m, tea.Batch(m.fetchStatus, m.fetchLogTail, m.fetchErrors, m.fetchWatches, m.fetchStats, m.tick())
	case errMsg:
		m.err = msg
	}
	return m, nil
}

func (m *Model) resetLogs()        { m.logs = nil; m.logCursor = 0; m.logIndex = 0; m.logOffset = 0; m.logExpanded = false }
func (m *Model) ensureVis() {
	h := m.logH()
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
func (m Model) logH() int { h := m.height - 12; if h < 3 { return 3 }; return h }

func (m *Model) cycleLevelFilter() {
	lvls := []string{"", "error", "warn", "info", "debug"}
	for i, l := range lvls {
		if l == m.levelFilter { m.levelFilter = lvls[(i+1)%len(lvls)]; m.resetLogs(); return }
	}
	m.levelFilter = ""
}
func (m *Model) cycleServiceFilter() {
	if m.status == nil || len(m.status.Services) == 0 { m.serviceFilter = ""; return }
	svcs := []string{""}
	for _, s := range m.status.Services { svcs = append(svcs, s.Name) }
	for i, s := range svcs {
		if s == m.serviceFilter { m.serviceFilter = svcs[(i+1)%len(svcs)]; m.resetLogs(); return }
	}
	m.serviceFilter = ""
}

// ═══════════════════════════════════════════════════════════
// VIEW
// ═══════════════════════════════════════════════════════════

func (m Model) View() tea.View {
	if m.quitting { return tea.NewView("") }
	if !m.ready {
		v := tea.NewView("\n " + lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("◆ OpenTrace") + styleDim.Render(" connecting..."))
		v.AltScreen = true
		return v
	}
	var s string
	switch m.view {
	case viewDashboard: s = m.dashView()
	case viewErrors:    s = m.errView()
	case viewWatches:   s = m.watchView()
	case viewHelp:      s = m.helpView()
	}
	v := tea.NewView(s)
	v.AltScreen = true
	return v
}

// ═══════════════════════════════════════════════════════════
// DASHBOARD — htop style: no borders, full width, compact
// ═══════════════════════════════════════════════════════════

func (m Model) dashView() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString(m.renderMeters())
	b.WriteString(m.renderLogHeader())
	b.WriteString(m.renderLogs())
	b.WriteString(m.renderHotkeys())
	return b.String()
}

// Header: htop-style top bar
func (m Model) renderHeader() string {
	ver := "dev"
	up := ""
	if m.status != nil {
		if m.status.Version != "" { ver = m.status.Version }
		up = fmtUptime(m.status.UptimeSeconds)
	}

	left := lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Render("◆ OpenTrace") +
		styleDim.Render(" v"+ver)

	var pills []string
	if m.status != nil && m.status.Database != nil {
		if m.status.Database.Healthy {
			pills = append(pills, pill("DB", "OK", colorGreen))
		} else {
			pills = append(pills, pill("DB", "ERR", colorRed))
		}
	}
	if m.status != nil && m.status.Servers != nil && m.status.Servers.Total > 0 {
		sv := m.status.Servers
		c := colorGreen; if sv.Offline > 0 { c = colorYellow }; if sv.Online == 0 { c = colorRed }
		pills = append(pills, pill("SRV", fmt.Sprintf("%d/%d", sv.Online, sv.Total), c))
	}
	if m.status != nil && m.status.Connectors != nil && m.status.Connectors.Total > 0 {
		pills = append(pills, pill("CONN", fmt.Sprintf("%d", m.status.Connectors.Connected), colorCyan))
	}

	var rp []string
	if up != "" { rp = append(rp, styleDim.Render("up "+up)) }
	if m.levelFilter != "" { rp = append(rp, lipgloss.NewStyle().Foreground(colorYellow).Render("◉ "+strings.ToUpper(m.levelFilter))) }
	if m.serviceFilter != "" { rp = append(rp, lipgloss.NewStyle().Foreground(colorCyan).Render("◉ "+m.serviceFilter)) }

	center := strings.Join(pills, " ")
	right := strings.Join(rp, "  ")

	g1 := m.width/2 - lipgloss.Width(left) - lipgloss.Width(center)/2
	if g1 < 1 { g1 = 1 }
	g2 := m.width - lipgloss.Width(left) - g1 - lipgloss.Width(center) - lipgloss.Width(right) - 1
	if g2 < 1 { g2 = 1 }

	return lipgloss.NewStyle().Width(m.width).Background(colorBg).
		Render(left+strings.Repeat(" ", g1)+center+strings.Repeat(" ", g2)+right) + "\n"
}

// Meters: htop-style horizontal bars, no border, full width
func (m Model) renderMeters() string {
	W := m.width
	colW := (W - 2) / 3

	var c1, c2, c3 strings.Builder

	// ── Ingestion ──
	c1.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("INGESTION") + "\n")
	if m.status != nil && m.status.Logs != nil {
		n := m.status.Logs.LastHour
		c1.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorFg).Render(formatNum(n)) + styleDim.Render(" logs/hr") + "\n")
		rate := float64(n) / 2000.0; if rate > 1 { rate = 1 }
		c1.WriteString(miniBar(rate, colW-4, "#22d3ee") + "\n")
	}
	if m.stats != nil && len(m.stats.Buckets) > 0 {
		c1.WriteString(m.sparkline(m.stats.Buckets, colW-2) + "\n")
	}

	// ── Errors ──
	c2.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("ERRORS") + "\n")
	if m.status != nil && m.status.Logs != nil {
		n := m.status.Logs.ErrorsLastHour
		if n > 0 {
			c2.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorRed).Render(fmt.Sprintf("%d", n)) + styleDim.Render(" last 1h"))
		} else {
			c2.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Render("✓") + styleDim.Render(" clean"))
		}
		c2.WriteString("\n")
	}
	if m.status != nil && m.status.ErrorGroups != nil && m.status.ErrorGroups.Unresolved > 0 {
		c2.WriteString(lipgloss.NewStyle().Foreground(colorOrange).Render(fmt.Sprintf("%d", m.status.ErrorGroups.Unresolved)) + styleDim.Render(" unresolved\n"))
	}
	if m.errors != nil {
		for i, eg := range m.errors.ErrorGroups {
			if i >= 3 { break }
			c2.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render(fmt.Sprintf("%3d", eg.OccurrenceCount)) +
				styleDim.Render(" "+truncate(eg.ExceptionClass, colW-6)) + "\n")
		}
	}

	// ── Watches ──
	c3.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("WATCHES") + "\n")
	if m.status != nil && m.status.Watches != nil {
		ws := m.status.Watches
		c3.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colorFg).Render(fmt.Sprintf("%d", ws.Active)) + styleDim.Render(" active"))
		if ws.Triggered > 0 {
			c3.WriteString(" " + lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(fmt.Sprintf("▲%d", ws.Triggered)))
		}
		c3.WriteString("\n")
	}
	if m.status != nil && m.status.WatchAlerts != nil && m.status.WatchAlerts.Pending > 0 {
		c3.WriteString(lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("⚠ %d", m.status.WatchAlerts.Pending)) + styleDim.Render(" pending\n"))
	}
	if m.status != nil && m.status.HealthChecks != nil && m.status.HealthChecks.Total > 0 {
		hc := m.status.HealthChecks
		if hc.Down > 0 {
			c3.WriteString(lipgloss.NewStyle().Foreground(colorRed).Render(fmt.Sprintf("✗ %d/%d", hc.Down, hc.Total)) + styleDim.Render(" checks\n"))
		} else {
			c3.WriteString(lipgloss.NewStyle().Foreground(colorGreen).Render(fmt.Sprintf("✓ %d", hc.Total)) + styleDim.Render(" checks\n"))
		}
	}

	r1 := lipgloss.NewStyle().Width(colW).Padding(0, 1).Render(c1.String())
	r2 := lipgloss.NewStyle().Width(colW).Padding(0, 1).Render(c2.String())
	r3 := lipgloss.NewStyle().Width(colW).Padding(0, 1).Render(c3.String())

	sep := styleDim.Render(strings.Repeat("─", W))
	return lipgloss.JoinHorizontal(lipgloss.Top, r1, r2, r3) + "\n" + sep + "\n"
}

// Log column header — htop style
func (m Model) renderLogHeader() string {
	W := m.width

	// Title row
	title := lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("LOGS")
	if m.searchActive {
		title += " " + lipgloss.NewStyle().Foreground(colorYellow).Render("/" + m.searchFilter + "▌")
	} else if m.searchFilter != "" {
		title += " " + styleDim.Render("\""+m.searchFilter+"\"")
	}
	if len(m.logs) > 0 {
		title += " " + styleDim.Render(fmt.Sprintf("(%d)", len(m.logs)))
	}

	// Column header like htop's PID USER PRI...
	colHdr := lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Foreground(colorFg).Bold(true).Width(W).
		Render(fmt.Sprintf("  %-8s %-5s %-14s %s", "TIME", "LEVEL", "SERVICE", "MESSAGE"))

	return title + "\n" + colHdr + "\n"
}

// Log rows — full width, no border, htop-style selected row highlight
func (m Model) renderLogs() string {
	visH := m.logH()
	W := m.width

	var lines []string
	if len(m.logs) == 0 {
		lines = append(lines, styleDim.Render("  Waiting for logs..."))
	} else {
		end := m.logOffset + visH
		if end > len(m.logs) { end = len(m.logs) }
		for i := m.logOffset; i < end; i++ {
			lines = append(lines, m.fmtLog(i, W))
			if i == m.logIndex && m.logExpanded {
				lines = append(lines, m.fmtLogDetail(m.logs[i], W)...)
			}
		}
	}

	for len(lines) < visH { lines = append(lines, "") }
	if len(lines) > visH { lines = lines[:visH] }

	return strings.Join(lines, "\n") + "\n"
}

func (m Model) fmtLog(idx, W int) string {
	e := m.logs[idx]
	sel := idx == m.logIndex && m.focusedPanel == panelLogs

	ts := e.Timestamp.Local().Format("15:04:05")
	lvl := padRight(strings.ToUpper(e.Level), 5)
	svc := padRight(e.Service, 14)

	msgW := W - 32
	if msgW < 10 { msgW = 10 }
	msg := truncate(oneLine(e.Message), msgW)

	if sel {
		// htop-style: full-width highlight background on selected row
		row := fmt.Sprintf("▸ %s %s %s %s", ts, lvl, svc, msg)
		// Pad to full width
		row = row + strings.Repeat(" ", max(0, W-lipgloss.Width(row)))
		return lipgloss.NewStyle().
			Background(lipgloss.Color("#1e293b")).
			Foreground(colorFg).
			Bold(true).
			Width(W).
			Render(fmt.Sprintf("▸ %s %s %s %s",
				lipgloss.NewStyle().Foreground(colorFgDim).Render(ts),
				levelStyle(strings.TrimSpace(lvl)).Bold(true).Render(lvl),
				lipgloss.NewStyle().Foreground(colorCyan).Render(svc),
				lipgloss.NewStyle().Foreground(colorFg).Bold(true).Render(msg),
			))
	}

	return fmt.Sprintf("  %s %s %s %s",
		styleDim.Render(ts),
		levelStyle(strings.TrimSpace(lvl)).Render(lvl),
		styleDim.Render(svc),
		lipgloss.NewStyle().Foreground(colorFgDim).Render(msg))
}

func (m Model) fmtLogDetail(e apiclient.LogEntry, W int) []string {
	d := styleDim
	v := lipgloss.NewStyle().Foreground(colorFgDim)
	a := lipgloss.NewStyle().Foreground(colorCyan)
	indent := "     "
	sepW := min(60, W-8)
	sep := indent + styleDim.Render(strings.Repeat("─", sepW))

	var lines []string
	lines = append(lines, sep)

	lines = append(lines, indent+d.Render("id ")+v.Render(fmt.Sprintf("%d", e.ID))+
		"  "+d.Render("time ")+v.Render(e.Timestamp.Local().Format("2006-01-02 15:04:05.000")))

	if e.RequestID != "" {
		lines = append(lines, indent+d.Render("req ")+a.Render(e.RequestID))
	}
	if e.ExceptionClass != "" {
		lines = append(lines, indent+d.Render("err ")+styleLevelError.Render(e.ExceptionClass))
	}

	cleanMsg := oneLine(e.Message)
	if len(cleanMsg) > W-38 {
		lines = append(lines, indent+d.Render("msg ")+v.Render(truncate(cleanMsg, W-12)))
	}

	// Request waterfall
	det := m.expandedDetail
	if det != nil && det.ID == e.ID && det.RequestSummary != nil {
		rs := det.RequestSummary
		lines = append(lines, "")
		lines = append(lines, indent+lipgloss.NewStyle().Bold(true).Foreground(colorBlue).
			Render(fmt.Sprintf("▼ %s %s → %d  (%.0fms)", rs.Method, truncate(rs.Path, 30), rs.Status, rs.DurationMs)))

		barW := min(40, W-30)
		if barW < 10 { barW = 10 }
		total := rs.DurationMs
		if total <= 0 { total = 1 }

		type seg struct{ label string; ms float64; clr string; extra string }
		var segs []seg

		if rs.SQLTotalMs > 0 {
			ex := fmt.Sprintf("%d queries", rs.SQLCount)
			if rs.NPlusOne { ex += " ⚠N+1" }
			if rs.DuplicateQueries > 0 { ex += fmt.Sprintf(" %ddups", rs.DuplicateQueries) }
			segs = append(segs, seg{"SQL", rs.SQLTotalMs, "#f87171", ex})
		}
		if rs.ViewTotalMs > 0 {
			ex := fmt.Sprintf("%d views", rs.ViewCount)
			segs = append(segs, seg{"View", rs.ViewTotalMs, "#34d399", ex})
		}
		if rs.HTTPExternalTotalMs > 0 {
			segs = append(segs, seg{"HTTP", rs.HTTPExternalTotalMs, "#fbbf24", fmt.Sprintf("%d calls", rs.HTTPExternalCount)})
		}
		if rs.CacheReads > 0 {
			segs = append(segs, seg{"Cache", 0, "#22d3ee", fmt.Sprintf("r:%d h:%d w:%d %.0f%%", rs.CacheReads, rs.CacheHits, rs.CacheWrites, rs.CacheHitRatio*100)})
		}
		other := total - rs.SQLTotalMs - rs.ViewTotalMs - rs.HTTPExternalTotalMs
		if other > 0.5 { segs = append(segs, seg{"App", other, "#a78bfa", ""}) }

		for _, s := range segs {
			ratio := s.ms / total
			filled := int(ratio * float64(barW))
			if filled < 1 && s.ms > 0 { filled = 1 }
			bar := lipgloss.NewStyle().Foreground(lipgloss.Color(s.clr)).Render(repeatStr("█", filled))
			empty := styleDim.Render(repeatStr("░", barW-filled))
			timing := ""
			if s.ms > 0 {
				timing = lipgloss.NewStyle().Foreground(lipgloss.Color(s.clr)).Bold(true).Render(fmt.Sprintf("%6.1fms", s.ms))
			} else {
				timing = styleDim.Render("      —")
			}
			line := indent + d.Render(padRight(s.label, 5)) + " " + bar + empty + " " + timing
			if s.extra != "" { line += "  " + d.Render(s.extra) }
			lines = append(lines, line)
		}

		if rs.MemoryDeltaMb != 0 {
			mc := colorGreen; if rs.MemoryDeltaMb > 5 { mc = colorOrange }; if rs.MemoryDeltaMb > 20 { mc = colorRed }
			lines = append(lines, indent+d.Render("mem  ")+lipgloss.NewStyle().Foreground(mc).Render(fmt.Sprintf("%+.1fMB", rs.MemoryDeltaMb)))
		}
		if rs.SQLSlowestName != "" {
			lines = append(lines, indent+d.Render("slow ")+lipgloss.NewStyle().Foreground(colorOrange).Render(fmt.Sprintf("%.1fms %s", rs.SQLSlowestMs, truncate(rs.SQLSlowestName, W-25))))
		}
	} else if det == nil && m.logExpanded {
		lines = append(lines, indent+styleDim.Render("loading..."))
	}

	if len(e.Metadata) > 0 {
		for k, val := range e.Metadata {
			if k == "logs" || k == "timeline" || k == "request_summary" { continue }
			s := fmtMetaValue(val)
			if s != "" { lines = append(lines, indent+d.Render(k+" ")+v.Render(truncate(s, W-len(k)-10))) }
		}
	}

	lines = append(lines, sep)
	return lines
}

// Hotkeys bar — htop-style F-key bar at bottom
func (m Model) renderHotkeys() string {
	var parts []string
	if m.focusedPanel == panelLogs {
		parts = append(parts, hk("↑↓")+"scroll", hk("⏎")+"expand", hk("/")+"search", hk("l")+"level", hk("s")+"svc")
	} else {
		parts = append(parts, hk("tab")+"logs")
	}
	parts = append(parts, hk("e")+"errors", hk("w")+"watches", hk("?")+"help", hk("q")+"quit")
	return lipgloss.NewStyle().Width(m.width).Background(colorBg).Foreground(colorFgMuted).Padding(0, 1).Render(strings.Join(parts, "  "))
}

// ═══════════════════════════════════════════════════════════
// ERRORS / WATCHES / HELP VIEWS
// ═══════════════════════════════════════════════════════════

func (m Model) errView() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n " + lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("ERROR GROUPS") + " " + styleDim.Render("unresolved") + "\n\n")
	if m.errors == nil || len(m.errors.ErrorGroups) == 0 {
		b.WriteString(styleDim.Render("  No errors ✓") + "\n")
	} else {
		for i, eg := range m.errors.ErrorGroups {
			sel := i == m.errorIndex
			ptr := "  "; if sel { ptr = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("▸ ") }
			cnt := lipgloss.NewStyle().Foreground(colorRed).Bold(true).Render(fmt.Sprintf("%4d×", eg.OccurrenceCount))
			mW := m.width - 42; if mW < 10 { mW = 10 }
			line := fmt.Sprintf("%s%s  %-28s  %s", ptr, cnt, truncate(eg.ExceptionClass, 28), styleDim.Render(truncate(oneLine(eg.Message), mW)))
			if sel { line = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Width(m.width).Render(line) }
			b.WriteString(line + "\n")
			b.WriteString(fmt.Sprintf("        %s  %s  impact %s  users %s\n",
				lipgloss.NewStyle().Foreground(colorCyan).Render(padRight(eg.Service, 16)),
				styleDim.Render("last "+eg.LastSeenAt.Local().Format("15:04")),
				lipgloss.NewStyle().Foreground(colorOrange).Render(fmt.Sprintf("%.1f", eg.ImpactScore)),
				lipgloss.NewStyle().Foreground(colorBlue).Render(fmt.Sprintf("%d", eg.UniqueUsers))))
			if i < len(m.errors.ErrorGroups)-1 { b.WriteString("\n") }
		}
	}
	b.WriteString("\n" + footer(m.width, "esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

func (m Model) watchView() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n " + lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("WATCHES") + "\n\n")
	if m.watches == nil || len(m.watches.Watches) == 0 {
		b.WriteString(styleDim.Render("  No watches.") + "\n")
	} else {
		for i, w := range m.watches.Watches {
			sel := i == m.watchIndex
			ptr := "  "; if sel { ptr = lipgloss.NewStyle().Foreground(colorYellow).Bold(true).Render("▸ ") }
			st := styleDim
			switch w.Status {
			case "triggered": st = lipgloss.NewStyle().Foreground(colorRed).Bold(true)
			case "active": st = lipgloss.NewStyle().Foreground(colorGreen)
			}
			line := fmt.Sprintf("%s%s  %-14s  %-12s  %s", ptr, st.Render(padRight(w.Status, 10)), w.Conditions, w.Service, styleDim.Render(w.Urgency))
			if sel { line = lipgloss.NewStyle().Background(lipgloss.Color("#1e293b")).Width(m.width).Render(line) }
			b.WriteString(line + "\n")
		}
		if m.watches.Alerts.Pending > 0 {
			b.WriteString(fmt.Sprintf("\n  %s\n", lipgloss.NewStyle().Foreground(colorYellow).Render(fmt.Sprintf("⚠ %d pending alerts", m.watches.Alerts.Pending))))
		}
	}
	b.WriteString("\n" + footer(m.width, "esc back", "↑↓ navigate", "d dashboard", "q quit"))
	return b.String()
}

func (m Model) helpView() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n " + lipgloss.NewStyle().Bold(true).Foreground(colorBlue).Render("KEYBOARD SHORTCUTS") + "\n\n")
	secs := []struct{ n string; items [][2]string }{
		{"Navigation", [][2]string{{"↑/k ↓/j", "Move selection"}, {"enter/space", "Expand log detail"}, {"g/G", "Top/bottom"}, {"tab", "Switch panel"}, {"esc", "Back"}}},
		{"Views", [][2]string{{"d", "Dashboard"}, {"e", "Errors"}, {"w", "Watches"}, {"?/h", "Help"}}},
		{"Filters", [][2]string{{"/", "Search"}, {"l", "Level"}, {"s", "Service"}}},
	}
	for _, sec := range secs {
		b.WriteString("  " + lipgloss.NewStyle().Foreground(colorBlue).Bold(true).Render(sec.n) + "\n")
		for _, e := range sec.items {
			b.WriteString(fmt.Sprintf("    %s  %s\n", lipgloss.NewStyle().Foreground(colorYellow).Render(padRight(e[0], 14)), e[1]))
		}
		b.WriteString("\n")
	}
	b.WriteString(footer(m.width, "esc back", "q quit"))
	return b.String()
}

// ═══════════════════════════════════════════════════════════
// SHARED
// ═══════════════════════════════════════════════════════════

func pill(label, value string, c color.Color) string {
	return lipgloss.NewStyle().Foreground(c).Render(label+":"+value)
}
func hk(k string) string { return lipgloss.NewStyle().Foreground(colorYellow).Render(k) + " " }
func footer(w int, hints ...string) string {
	p := make([]string, len(hints)); for i, h := range hints { p[i] = styleDim.Render(h) }
	return lipgloss.NewStyle().Width(w).Background(colorBg).Foreground(colorFgMuted).Padding(0, 1).Render(strings.Join(p, "    "))
}
func (m Model) sparkline(buckets []apiclient.HistogramBucket, w int) string {
	blocks := []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
	maxV := 0; for _, b := range buckets { if b.Total > maxV { maxV = b.Total } }
	if maxV == 0 { return styleDim.Render(strings.Repeat(string(blocks[0]), min(len(buckets), w))) }
	n := min(len(buckets), w)
	var sb strings.Builder
	for i := len(buckets) - n; i < len(buckets); i++ {
		idx := buckets[i].Total * (len(blocks) - 1) / maxV; sb.WriteRune(blocks[idx])
	}
	return lipgloss.NewStyle().Foreground(colorCyan).Render(sb.String())
}
func fmtUptime(sec int64) string {
	d, h, m := sec/86400, (sec%86400)/3600, (sec%3600)/60
	if d > 0 { return fmt.Sprintf("%dd%dh", d, h) }
	if h > 0 { return fmt.Sprintf("%dh%dm", h, m) }
	return fmt.Sprintf("%dm", m)
}
func fmtMetaValue(val any) string {
	switch v := val.(type) {
	case string: return oneLine(v)
	case float64: if v == float64(int64(v)) { return fmt.Sprintf("%d", int64(v)) }; return fmt.Sprintf("%.2f", v)
	case bool: if v { return "true" }; return "false"
	case nil: return ""
	default: b, err := json.Marshal(v); if err != nil { return fmt.Sprintf("%v", v) }; return oneLine(string(b))
	}
}
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " "); s = strings.ReplaceAll(s, "\r", ""); s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") { s = strings.ReplaceAll(s, "  ", " ") }
	return strings.TrimSpace(s)
}

// ═══════════════════════════════════════════════════════════
// COMMANDS
// ═══════════════════════════════════════════════════════════

func (m Model) tick() tea.Cmd { return tea.Tick(m.config.RefreshRate, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func (m Model) fetchStatus() tea.Msg { r, e := m.config.Client.Status(); if e != nil { return errMsg(e) }; return statusMsg(r) }
func (m Model) fetchLogTail() tea.Msg { r, e := m.config.Client.LogTail(m.logCursor, 50, m.levelFilter, m.serviceFilter, m.searchFilter); if e != nil { return errMsg(e) }; return logTailMsg(r) }
func (m Model) fetchErrors() tea.Msg { r, e := m.config.Client.ErrorsTop(10, "1h", m.serviceFilter); if e != nil { return errMsg(e) }; return errorsMsg(r) }
func (m Model) fetchWatches() tea.Msg { r, e := m.config.Client.Watches(); if e != nil { return errMsg(e) }; return watchesMsg(r) }
func (m Model) fetchStats() tea.Msg { r, e := m.config.Client.IngestionStats("1h", "1m", m.serviceFilter); if e != nil { return errMsg(e) }; return statsMsg(r) }
func (m Model) fetchLogDetail(id int64) tea.Cmd {
	return func() tea.Msg {
		r, e := m.config.Client.GetLog(id); if e != nil { return errMsg(e) }; return logDetailMsg(r)
	}
}

// ═══════════════════════════════════════════════════════════
// UTILS
// ═══════════════════════════════════════════════════════════

func truncate(s string, mx int) string {
	if mx <= 0 { return "" }; if len(s) <= mx { return s }; if mx <= 3 { return s[:mx] }; return s[:mx-1] + "…"
}
func padRight(s string, n int) string { if len(s) >= n { return s[:n] }; return s + strings.Repeat(" ", n-len(s)) }
func formatNum(n int) string { if n < 1000 { return fmt.Sprintf("%d", n) }; return fmt.Sprintf("%d,%03d", n/1000, n%1000) }
func max(a, b int) int { if a > b { return a }; return b }
func min(a, b int) int { if a < b { return a }; return b }
