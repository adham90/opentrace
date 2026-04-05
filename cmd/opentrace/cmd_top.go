package main

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/adham90/opentrace/internal/apiclient"
	"github.com/adham90/opentrace/internal/cliconfig"
	"github.com/adham90/opentrace/internal/tui"
)

// runTop implements `opentrace top` — interactive TUI dashboard.
func runTop() error {
	cfg, err := cliconfig.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	applyFlagOverrides(cfg)

	client := apiclient.New(cfg.Endpoint, cfg.APIKey)

	refreshRate := 5 * time.Second
	if v := flagValue("--refresh"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 1*time.Second {
			refreshRate = d
		}
	}

	model := tui.New(tui.Config{
		Client:      client,
		Service:     flagValue("--service"),
		Level:       flagValue("--level"),
		RefreshRate: refreshRate,
	})

	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	return nil
}
