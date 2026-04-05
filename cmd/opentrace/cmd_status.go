package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/adham90/opentrace/internal/apiclient"
	"github.com/adham90/opentrace/internal/cliconfig"
)

// runStatus implements `opentrace status` — a one-shot health check that prints
// server status and exits. Exit code 0 = healthy, 1 = unhealthy/unreachable.
func runStatus() error {
	cfg, err := cliconfig.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply flag overrides
	applyFlagOverrides(cfg)

	jsonOutput := hasFlag("--json")

	client := apiclient.New(cfg.Endpoint, cfg.APIKey)
	status, err := client.Status()
	if err != nil {
		if jsonOutput {
			out, _ := json.Marshal(map[string]string{"error": err.Error()})
			fmt.Fprintln(os.Stderr, string(out))
		} else {
			fmt.Fprintf(os.Stderr, "error: cannot connect to %s: %v\n", cfg.Endpoint, err)
		}
		os.Exit(1)
	}

	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(status)
	}

	printStatusText(cfg.Endpoint, status)
	return nil
}

func printStatusText(endpoint string, s *apiclient.StatusResponse) {
	fmt.Printf("\nOpenTrace Status (%s)\n", endpoint)
	fmt.Printf("  Version:     %s\n", s.Version)
	fmt.Printf("  Uptime:      %s\n", formatUptime(s.UptimeSeconds))

	if s.Database != nil {
		health := "healthy"
		if !s.Database.Healthy {
			health = "unhealthy"
		}
		fmt.Printf("  Database:    %s (%s)\n", health, formatBytes(s.Database.SizeBytes))
	}

	fmt.Println()

	if s.Logs != nil {
		fmt.Printf("  Ingestion:   %s logs (last 1h)\n", formatNumber(s.Logs.LastHour))
		fmt.Printf("  Errors:      %d (last 1h)", s.Logs.ErrorsLastHour)
		if s.ErrorGroups != nil && s.ErrorGroups.Unresolved > 0 {
			fmt.Printf(", %d unresolved groups", s.ErrorGroups.Unresolved)
		}
		fmt.Println()
	}

	if s.Servers != nil && s.Servers.Total > 0 {
		fmt.Printf("  Servers:     %d online, %d offline\n", s.Servers.Online, s.Servers.Offline)
	}

	if len(s.Services) > 0 {
		names := make([]string, 0, len(s.Services))
		for _, svc := range s.Services {
			names = append(names, svc.Name)
		}
		fmt.Printf("  Services:    %d (%s)\n", len(s.Services), strings.Join(names, ", "))
	}

	fmt.Println()

	if s.Watches != nil {
		parts := []string{fmt.Sprintf("%d active", s.Watches.Active)}
		if s.Watches.Triggered > 0 {
			parts = append(parts, fmt.Sprintf("%d triggered", s.Watches.Triggered))
		}
		fmt.Printf("  Watches:     %s\n", strings.Join(parts, ", "))
	}

	if s.WatchAlerts != nil && s.WatchAlerts.Pending > 0 {
		fmt.Printf("  Alerts:      %d pending\n", s.WatchAlerts.Pending)
	}

	if s.Connectors != nil && s.Connectors.Total > 0 {
		fmt.Printf("  Connectors:  %d connected, %d errored\n", s.Connectors.Connected, s.Connectors.Error)
	}

	if s.HealthChecks != nil && s.HealthChecks.Total > 0 {
		if s.HealthChecks.Down == 0 && s.HealthChecks.Degraded == 0 {
			fmt.Printf("  Health:      %d checks, all up\n", s.HealthChecks.Total)
		} else {
			fmt.Printf("  Health:      %d checks, %d down, %d degraded\n",
				s.HealthChecks.Total, s.HealthChecks.Down, s.HealthChecks.Degraded)
		}
	}

	fmt.Println()
}

func formatUptime(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	days := seconds / 86400
	hours := (seconds % 86400) / 3600
	minutes := (seconds % 3600) / 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func formatBytes(b int64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case b >= gb:
		return fmt.Sprintf("%.1fGB", float64(b)/float64(gb))
	case b >= mb:
		return fmt.Sprintf("%.0fMB", float64(b)/float64(mb))
	case b >= kb:
		return fmt.Sprintf("%.0fKB", float64(b)/float64(kb))
	default:
		return fmt.Sprintf("%dB", b)
	}
}

func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%s,%03d", formatNumber(n/1000), n%1000)
	}
	return fmt.Sprintf("%s,%03d,%03d", formatNumber(n/1_000_000), (n/1000)%1000, n%1000)
}

// applyFlagOverrides applies --endpoint and --api-key flags to the config.
func applyFlagOverrides(cfg *cliconfig.ProjectConfig) {
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--endpoint":
			if i+1 < len(os.Args) {
				cfg.Endpoint = os.Args[i+1]
				i++
			}
		case "--api-key":
			if i+1 < len(os.Args) {
				cfg.APIKey = os.Args[i+1]
				i++
			}
		}
	}
}

// hasFlag checks if a flag is present in os.Args.
func hasFlag(flag string) bool {
	for _, arg := range os.Args[2:] {
		if arg == flag {
			return true
		}
	}
	return false
}

// flagValue returns the value of a flag, or empty string if not present.
func flagValue(flag string) string {
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == flag && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}
