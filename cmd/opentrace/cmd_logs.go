package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
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
	env := flagValue("--env")
	since := flagValue("--since")
	jsonOutput := hasFlag("--json")
	noColor := hasFlag("--no-color")
	follow := !hasFlag("--no-follow")

	// If piped (not a TTY), disable follow and color by default
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		follow = false
		noColor = true
	}

	// Reject a bad --since up front, on both paths. Silently ignoring it prints
	// "nothing happened" for a window the user explicitly asked about.
	if since != "" && !validSince(since) {
		return fmt.Errorf("invalid --since value %q (use 15m, 6h, 7d or an RFC3339 timestamp)", since)
	}

	client := apiclient.New(cfg.Endpoint, cfg.APIKey)
	filters := logFilters{
		endpoint: cfg.Endpoint,
		apiKey:   cfg.APIKey,
		level:    level,
		service:  service,
		env:      env,
		since:    since,
	}

	if follow {
		// With --since, print that history first and then stream. The flag used
		// to be dropped entirely in follow mode, so `opentrace logs --since 1h`
		// printed nothing until a new log happened to arrive. Without --since,
		// follow keeps its "only new logs" behaviour.
		var lastID int64
		if since != "" {
			lastID, err = dumpLogs(filters, jsonOutput, noColor)
			if err != nil {
				return err
			}
		}
		return streamLogs(client, level, service, "", env, jsonOutput, noColor, lastID)
	}

	// One-shot: dump logs and exit
	_, err = dumpLogs(filters, jsonOutput, noColor)
	return err
}

const (
	// logPageSize is the server's maximum tail page size.
	logPageSize = 200
	// maxLogHistoryLines caps how many historical lines one command prints, so
	// a wide --since window cannot page forever.
	maxLogHistoryLines = 5000
)

// logFilters carries everything needed to query the tail endpoint.
type logFilters struct {
	endpoint string
	apiKey   string
	level    string
	service  string
	env      string
	since    string
}

// dumpLogs prints the logs in the requested window and returns the highest log
// ID printed (0 if none), so a following stream can skip what was already shown.
//
// With --since it pages forward through the whole window server-side; the old
// code fetched a fixed newest-100 page and filtered client-side, silently
// dropping everything else in the window.
func dumpLogs(f logFilters, jsonOutput, noColor bool) (int64, error) {
	var (
		lastID  int64
		printed int
	)

	// after=0 makes the server page ascending from the start of the window;
	// without a cursor it returns only the newest page.
	cursor := int64(0)
	for {
		params := url.Values{}
		params.Set("limit", strconv.Itoa(logPageSize))
		params.Set("after", strconv.FormatInt(cursor, 10))
		if f.level != "" {
			params.Set("level", f.level)
		}
		if f.service != "" {
			params.Set("service", f.service)
		}
		if f.env != "" {
			params.Set("env", f.env)
		}
		if f.since != "" {
			params.Set("since", f.since)
		}

		resp, err := fetchLogTail(f.endpoint, f.apiKey, params)
		if err != nil {
			return lastID, fmt.Errorf("fetching logs: %w", err)
		}

		for _, entry := range resp.Logs {
			printLogEntry(entry, jsonOutput, noColor)
			printed++
			if entry.ID > lastID {
				lastID = entry.ID
			}
			if printed >= maxLogHistoryLines {
				fmt.Fprintf(os.Stderr, "(truncated at %d lines — narrow --since or add filters)\n", maxLogHistoryLines)
				return lastID, nil
			}
		}

		// No since window means "the most recent page"; don't walk all history.
		if f.since == "" || !resp.HasMore || resp.Cursor <= cursor {
			if f.since == "" && resp.HasMore {
				fmt.Fprintf(os.Stderr, "(showing the newest %d lines — use --since to widen the window)\n", logPageSize)
			}
			return lastID, nil
		}
		cursor = resp.Cursor
	}
}

// fetchLogTail performs GET /api/cli/logs/tail with arbitrary query params.
// It exists because apiclient.LogTail cannot express `since` or a zero `after`
// cursor, both of which are needed to page a time window server-side.
func fetchLogTail(endpoint, apiKey string, params url.Values) (*apiclient.LogTailResponse, error) {
	u, err := url.Parse(endpoint + "/api/cli/logs/tail")
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", endpoint, err)
	}
	u.RawQuery = params.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	httpClient := &http.Client{Timeout: logTailTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var out apiclient.LogTailResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &out, nil
}

const (
	// logTailTimeout bounds a single tail page request.
	logTailTimeout = 30 * time.Second
	// maxErrorBodyBytes caps how much of an error response body is echoed.
	maxErrorBodyBytes = 1024
)

// printLogEntry writes one entry in the selected output format.
func printLogEntry(entry apiclient.LogEntry, jsonOutput, noColor bool) {
	if jsonOutput {
		data, err := json.Marshal(entry)
		if err != nil {
			return
		}
		fmt.Println(string(data))
		return
	}
	fmt.Println(formatLogLine(entry, noColor))
}

// streamLogs connects to the SSE endpoint and streams logs in real time.
// Entries with an ID at or below afterID were already printed as history and
// are skipped.
func streamLogs(client *apiclient.Client, level, service, search, env string, jsonOutput, noColor bool, afterID int64) error {
	url, err := client.LogStreamURL(level, service, search, env)
	if err != nil {
		return err
	}
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
	// One SSE "data:" line carries a whole marshaled entry, and ingest accepts
	// bodies far larger than bufio's 64KB default token. Without this a single
	// big stack trace killed the follow session with "token too long" — and
	// killed it again on every reconnect.
	scanner.Buffer(make([]byte, sseInitialBufBytes), maxSSELineBytes)

	const dataPrefix = "data: "
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, dataPrefix) {
			continue
		}

		data := line[len(dataPrefix):]

		var entry apiclient.LogEntry
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue
		}
		if afterID > 0 && entry.ID != 0 && entry.ID <= afterID {
			continue // already printed as history
		}
		if jsonOutput {
			fmt.Println(data)
			continue
		}
		fmt.Println(formatLogLine(entry, noColor))
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading stream: %w", err)
	}
	return nil
}

const (
	// sseInitialBufBytes is the starting scan buffer; it grows on demand.
	sseInitialBufBytes = 64 << 10
	// maxSSELineBytes is the largest single SSE line accepted. It is above the
	// server's 10MB ingest body cap so no ingestible entry can break the stream.
	maxSSELineBytes = 16 << 20
)

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

// validSince reports whether s is a --since value the server will accept:
// a relative duration ("15m", "6h", "7d") or an RFC3339 timestamp.
func validSince(s string) bool {
	if _, err := parseDuration(s); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
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
