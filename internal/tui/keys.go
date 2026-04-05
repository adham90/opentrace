package tui

import "charm.land/bubbles/v2/key"

type keyMap struct {
	Quit      key.Binding
	Tab       key.Binding
	Filter    key.Binding
	Level     key.Binding
	Service   key.Binding
	Errors    key.Binding
	Watches   key.Binding
	Help      key.Binding
	Up        key.Binding
	Down      key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Escape    key.Binding
	Dashboard key.Binding
}

var keys = keyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("tab", "switch panel"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Level: key.NewBinding(
		key.WithKeys("l"),
		key.WithHelp("l", "level"),
	),
	Service: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "service"),
	),
	Errors: key.NewBinding(
		key.WithKeys("e"),
		key.WithHelp("e", "errors"),
	),
	Watches: key.NewBinding(
		key.WithKeys("w"),
		key.WithHelp("w", "watches"),
	),
	Help: key.NewBinding(
		key.WithKeys("?", "h"),
		key.WithHelp("?", "help"),
	),
	Up: key.NewBinding(
		key.WithKeys("k", "up"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("j", "down"),
		key.WithHelp("↓/j", "down"),
	),
	Top: key.NewBinding(
		key.WithKeys("g"),
		key.WithHelp("g", "top"),
	),
	Bottom: key.NewBinding(
		key.WithKeys("G"),
		key.WithHelp("G", "bottom"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "back"),
	),
	Dashboard: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "dashboard"),
	),
}
