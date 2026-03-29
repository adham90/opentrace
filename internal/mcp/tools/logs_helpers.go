package tools

import (
	"fmt"
	"regexp"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Shared helpers (used across logs_*.go files and errors.go)
// ---------------------------------------------------------------------------

// logsBuildTimeHistogram creates a compact time distribution of log entries.
// Auto-selects bucket size based on the time span of the results.
func logsBuildTimeHistogram(entries []store.LogEntry) map[string]any {
	if len(entries) == 0 {
		return nil
	}

	// Find time range.
	earliest := entries[0].Timestamp
	latest := entries[0].Timestamp
	for _, e := range entries[1:] {
		if e.Timestamp.Before(earliest) {
			earliest = e.Timestamp
		}
		if e.Timestamp.After(latest) {
			latest = e.Timestamp
		}
	}

	span := latest.Sub(earliest)
	if span <= 0 {
		return nil
	}

	// Auto-select bucket size.
	var bucketSize time.Duration
	var bucketLabel string
	switch {
	case span <= 5*time.Minute:
		bucketSize = 30 * time.Second
		bucketLabel = "30s"
	case span <= 30*time.Minute:
		bucketSize = time.Minute
		bucketLabel = "1m"
	case span <= 2*time.Hour:
		bucketSize = 5 * time.Minute
		bucketLabel = "5m"
	case span <= 12*time.Hour:
		bucketSize = 30 * time.Minute
		bucketLabel = "30m"
	default:
		bucketSize = time.Hour
		bucketLabel = "1h"
	}

	// Build buckets.
	buckets := make(map[string]int)
	for _, e := range entries {
		bucketStart := e.Timestamp.Truncate(bucketSize)
		key := bucketStart.Format(time.RFC3339)
		buckets[key]++
	}

	return map[string]any{
		"bucket_size": bucketLabel,
		"buckets":     buckets,
	}
}

var (
	logsReUUID      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	logsReIP        = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)
	logsReNumber    = regexp.MustCompile(`\b\d+\b`)
	logsReQuoted    = regexp.MustCompile(`"[^"]*"`)
	logsReTimestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}[^\s]*`)
)

// logsNormalizeMessage strips variable parts (UUIDs, IPs, numbers, timestamps, quoted strings)
// to produce a pattern key for clustering similar error messages.
func logsNormalizeMessage(msg string) string {
	msg = logsReTimestamp.ReplaceAllString(msg, "*")
	msg = logsReUUID.ReplaceAllString(msg, "*")
	msg = logsReIP.ReplaceAllString(msg, "*")
	msg = logsReQuoted.ReplaceAllString(msg, `"*"`)
	msg = logsReNumber.ReplaceAllString(msg, "*")
	return msg
}

func logsToInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	default:
		return 0
	}
}

func logsRound2(f float64) float64 {
	return float64(int(f*100)) / 100
}

func logsResolvePeriod(period string, now time.Time) (time.Time, time.Time, error) {
	switch period {
	case "last_1h":
		return now.Add(-time.Hour), now, nil
	case "last_6h":
		return now.Add(-6 * time.Hour), now, nil
	case "last_24h":
		return now.Add(-24 * time.Hour), now, nil
	case "today":
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		return start, now, nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown period %q (use last_1h, last_6h, last_24h, today)", period)
	}
}

func logsResolveBaseline(baseline string, curStart, curEnd time.Time) (time.Time, time.Time, error) {
	duration := curEnd.Sub(curStart)
	switch baseline {
	case "previous":
		return curStart.Add(-duration), curStart, nil
	case "yesterday_same_time":
		return curStart.Add(-24 * time.Hour), curEnd.Add(-24 * time.Hour), nil
	case "last_week_same_time":
		return curStart.Add(-168 * time.Hour), curEnd.Add(-168 * time.Hour), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("unknown baseline %q (use previous, yesterday_same_time, last_week_same_time)", baseline)
	}
}

func logsCalcChange(baseline, current int) (float64, string) {
	if baseline == 0 {
		if current == 0 {
			return 0, "unchanged"
		}
		return 100, "new"
	}
	pct := float64(current-baseline) / float64(baseline) * 100
	pct = logsRound2(pct)
	if pct > 0 {
		return pct, "increase"
	} else if pct < 0 {
		return pct, "decrease"
	}
	return 0, "unchanged"
}

func logsPeriodInfo(start, end time.Time) map[string]string {
	return map[string]string{
		"start": start.Format(time.RFC3339),
		"end":   end.Format(time.RFC3339),
	}
}
