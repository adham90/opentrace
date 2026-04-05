package tui

import "charm.land/lipgloss/v2"

// btop-inspired color palette — dark background, vibrant accents
var (
	// Primary accents
	colorAccent    = lipgloss.Color("#a78bfa") // purple
	colorAccentDim = lipgloss.Color("#6d28d9")
	colorBlue      = lipgloss.Color("#60a5fa")
	colorCyan      = lipgloss.Color("#22d3ee")
	colorGreen     = lipgloss.Color("#34d399")
	colorYellow    = lipgloss.Color("#fbbf24")
	colorOrange    = lipgloss.Color("#fb923c")
	colorRed       = lipgloss.Color("#f87171")
	colorPink      = lipgloss.Color("#f472b6")

	// Neutrals
	colorFg      = lipgloss.Color("#e5e7eb")
	colorFgDim   = lipgloss.Color("#9ca3af")
	colorFgMuted = lipgloss.Color("#6b7280")
	colorFgDark  = lipgloss.Color("#4b5563")
	colorBg      = lipgloss.Color("#0f0f1a")
	colorBgPanel = lipgloss.Color("#161625")
	colorBorder  = lipgloss.Color("#2d2640")
	colorBorderFocus = lipgloss.Color("#7c3aed")

	// Semantic
	colorSuccess = colorGreen
	colorWarning = colorYellow
	colorDanger  = colorRed
	colorInfo    = colorBlue
)

// Shared styles
var (
	styleTitle = lipgloss.NewStyle().Bold(true).Foreground(colorBlue)
	styleValue = lipgloss.NewStyle().Bold(true).Foreground(colorFg)
	styleLabel = lipgloss.NewStyle().Foreground(colorFgMuted)
	styleDim   = lipgloss.NewStyle().Foreground(colorFgDark)

	styleLevelError = lipgloss.NewStyle().Foreground(colorRed)
	styleLevelWarn  = lipgloss.NewStyle().Foreground(colorYellow)
	styleLevelInfo  = lipgloss.NewStyle().Foreground(colorGreen)
	styleLevelDebug = lipgloss.NewStyle().Foreground(colorFgMuted)
)

// panelStyle returns a bordered panel with optional focus highlight.
func panelStyle(focused bool) lipgloss.Style {
	bc := colorBorder
	if focused {
		bc = colorBorderFocus
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(bc).
		Padding(0, 1)
}

// levelStyle returns the style for a log level.
func levelStyle(level string) lipgloss.Style {
	switch level {
	case "FATAL":
		return lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	case "ERROR":
		return styleLevelError
	case "WARN":
		return styleLevelWarn
	case "INFO":
		return styleLevelInfo
	case "DEBUG":
		return styleLevelDebug
	default:
		return lipgloss.NewStyle().Foreground(colorFgDim)
	}
}

// miniBar renders a small bar chart using block characters.
// value is 0.0-1.0, width is character width.
func miniBar(value float64, width int, c string) string {
	if width <= 0 {
		return ""
	}
	filled := int(value * float64(width))
	if filled > width {
		filled = width
	}
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render(repeatStr("━", filled))
	empty := lipgloss.NewStyle().Foreground(colorBorder).Render(repeatStr("─", width-filled))
	return bar + empty
}

func repeatStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
