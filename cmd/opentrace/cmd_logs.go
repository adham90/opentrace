package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/apiclient"
	"github.com/adham90/opentrace/internal/cliconfig"
)

// runLogs implements `opentrace logs` — streaming log tail.
func runLogs() error {
	cfg, err := cliconfig.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	applyFlagOverrides(cfg)

	level := flagValue("--level")
	service := flagValue("--service")
	since := flagValue("--since")
	jsonOutput := hasFlag("--json")
	noColor := hasFlag("--no-color")
	follow := !hasFlag("--no-follow")

	// If piped (not a TTY), disable follow and color by default
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		follow = false
		noColor = true
	}

	client := apiclient.New(cfg.Endpoint, cfg.APIKey)

	if follow {
		return streamLogs(client, level, service, "", jsonOutput, noColor)
	}

	// One-shot: dump logs and exit
	return dumpLogs(client, level, service, since, jsonOutput, noColor)
}

// dumpLogs fetches a page of recent logs and prints them.
func dumpLogs(client *apiclient.Client, level, service, since string, jsonOutput, noColor bool) error {
	resp, err := client.LogTail(0, 100, level, service, "")
	if err != nil {
		return fmt.Errorf("fetching logs: %w", err)
	}

	var sinceTime time.Time
	if since != "" {
		d, err := parseDuration(since)
		if err != nil {
			return fmt.Errorf("invalid --since value: %w", err)
		}
		sinceTime = time.Now().Add(-d)
	}

	for _, entry := range resp.Logs {
		if !sinceTime.IsZero() && entry.Timestamp.Before(sinceTime) {
			continue
		}
		if jsonOutput {
			data, _ := json.Marshal(entry)
			fmt.Println(string(data))
		} else {
			fmt.Println(formatLogLine(entry, noColor))
		}
	}

	return nil
}

// streamLogs connects to the SSE endpoint and streams logs in real time.
func streamLogs(client *apiclient.Client, level, service, search string, jsonOutput, noColor bool) error {
	url := client.LogStreamURL(level, service, search)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	if key := client.APIKey(); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}

	httpClient := &http.Client{Timeout: 0} // no timeout for SSE
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := line[6:]
		if jsonOutput {
			fmt.Println(data)
			continue
		}

		var entry apiclient.LogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue
		}
		fmt.Println(formatLogLine(entry, noColor))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	return nil
}

// formatLogLine formats a log entry as a single line.
func formatLogLine(entry apiclient.LogEntry, noColor bool) string {
	ts := entry.Timestamp.Local().Format("15:04:05")
	level := padRight(strings.ToUpper(entry.Level), 5)
	service := padRight(entry.Service, 12)

	if noColor {
		return fmt.Sprintf("%s %s %s %s", ts, level, service, entry.Message)
	}

	return fmt.Sprintf("%s %s %s %s",
		ts,
		colorLevel(level),
		dim(service),
		entry.Message,
	)
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

// ANSI color helpers
func colorLevel(level string) string {
	trimmed := strings.TrimSpace(level)
	switch trimmed {
	case "FATAL", "ERROR":
		return "\033[31m" + level + "\033[0m" // red
	case "WARN":
		return "\033[33m" + level + "\033[0m" // yellow
	case "INFO":
		return "\033[32m" + level + "\033[0m" // green
	case "DEBUG":
		return "\033[36m" + level + "\033[0m" // cyan
	default:
		return level
	}
}

func dim(s string) string {
	return "\033[2m" + s + "\033[0m"
}

func parseDuration(s string) (time.Duration, error) {
	// Support "d" suffix for days
	if len(s) > 1 && s[len(s)-1] == 'd' {
		n, err := fmt.Sscanf(s[:len(s)-1], "%d", new(int))
		if err == nil && n == 1 {
			var days int
			fmt.Sscanf(s[:len(s)-1], "%d", &days)
			return time.Duration(days) * 24 * time.Hour, nil
		}
	}
	return time.ParseDuration(s)
}
