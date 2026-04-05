package tui

import "charm.land/lipgloss/v2"

// Color palette
var (
	colorPrimary   = lipgloss.Color("#7B2FBE")
	colorSecondary = lipgloss.Color("#3B82F6")
	colorSuccess   = lipgloss.Color("#22C55E")
	colorWarning   = lipgloss.Color("#EAB308")
	colorDanger    = lipgloss.Color("#EF4444")
	colorMuted     = lipgloss.Color("#6B7280")
	colorDim       = lipgloss.Color("#4B5563")
	colorBorder    = lipgloss.Color("#374151")
	colorHighlight = lipgloss.Color("#F59E0B")
)

// Styles
var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Padding(0, 1)

	styleStatusBar = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder)

	styleFocusedBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colorPrimary)

	styleTitle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorSecondary)

	styleValue = lipgloss.NewStyle().
			Bold(true)

	styleLabel = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleLevelError = lipgloss.NewStyle().Foreground(colorDanger).Bold(true)
	styleLevelWarn  = lipgloss.NewStyle().Foreground(colorWarning)
	styleLevelInfo  = lipgloss.NewStyle().Foreground(colorSuccess)
	styleLevelDebug = lipgloss.NewStyle().Foreground(colorMuted)

	styleService = lipgloss.NewStyle().Foreground(colorDim)

	styleSparkline = lipgloss.NewStyle().Foreground(colorSecondary)
)

// levelStyle returns the appropriate style for a log level.
func levelStyle(level string) lipgloss.Style {
	switch level {
	case "ERROR", "FATAL":
		return styleLevelError
	case "WARN":
		return styleLevelWarn
	case "INFO":
		return styleLevelInfo
	case "DEBUG":
		return styleLevelDebug
	default:
		return lipgloss.NewStyle()
	}
}
