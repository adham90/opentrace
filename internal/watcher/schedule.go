package watcher

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// Schedule represents a parsed scheduling expression. It supports both simple
// duration intervals ("5m", "1h") and standard cron expressions ("0 9 * * *").
type Schedule struct {
	Expression string        // Raw expression as provided by the user
	cronSched  cron.Schedule // Non-nil for cron expressions
	interval   time.Duration // Non-zero for simple intervals
}

// cronParser handles standard 5-field cron expressions and predefined schedules.
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// ParseSchedule parses a schedule expression string into a Schedule.
// It accepts:
//   - Simple intervals: "5m", "1h", "30s"
//   - Standard 5-field cron: "*/5 * * * *", "0 9 * * 1-5"
//   - Predefined schedules: "@every 5m", "@hourly", "@daily", "@weekly"
//
// Returns an error for invalid expressions (no silent fallback).
func ParseSchedule(expr string) (*Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("empty schedule expression")
	}

	// 1. Try simple interval ("5m", "1h", "30s")
	dur := tryParseInterval(expr)
	if dur > 0 {
		return &Schedule{
			Expression: expr,
			interval:   dur,
		}, nil
	}

	// 2. Try cron expression (5-field or predefined like @hourly, @daily, @every)
	sched, err := cronParser.Parse(expr)
	if err == nil {
		return &Schedule{
			Expression: expr,
			cronSched:  sched,
		}, nil
	}

	return nil, fmt.Errorf("invalid schedule expression %q: not a valid interval or cron expression", expr)
}

// tryParseInterval attempts to parse a simple duration string like "5m", "1h", "30s".
// Returns 0 if the string is not a valid interval.
func tryParseInterval(s string) time.Duration {
	if len(s) < 2 {
		return 0
	}

	unit := s[len(s)-1]
	numStr := s[:len(s)-1]

	// Must be all digits
	for _, c := range numStr {
		if c < '0' || c > '9' {
			return 0
		}
	}

	num := 0
	for _, c := range numStr {
		num = num*10 + int(c-'0')
	}
	if num <= 0 {
		return 0
	}

	switch unit {
	case 's':
		return time.Duration(num) * time.Second
	case 'm':
		return time.Duration(num) * time.Minute
	case 'h':
		return time.Duration(num) * time.Hour
	default:
		return 0
	}
}

// Next returns the next run time after the given time.
func (s *Schedule) Next(from time.Time) time.Time {
	if s.cronSched != nil {
		return s.cronSched.Next(from)
	}
	return from.Add(s.interval)
}

// IsInterval returns true if this schedule is a simple interval (not a cron expression).
func (s *Schedule) IsInterval() bool {
	return s.interval > 0
}

// Description returns a human-readable description of the schedule.
func (s *Schedule) Description() string {
	if s.interval > 0 {
		return describeInterval(s.interval)
	}
	return describeCron(s.Expression)
}

func describeInterval(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		h := int(d / time.Hour)
		if h == 1 {
			return "Every hour"
		}
		return fmt.Sprintf("Every %d hours", h)
	case d%time.Minute == 0:
		m := int(d / time.Minute)
		if m == 1 {
			return "Every minute"
		}
		return fmt.Sprintf("Every %d minutes", m)
	default:
		s := int(d / time.Second)
		if s == 1 {
			return "Every second"
		}
		return fmt.Sprintf("Every %d seconds", s)
	}
}

func describeCron(expr string) string {
	// Handle predefined schedules
	switch strings.ToLower(expr) {
	case "@yearly", "@annually":
		return "Once a year"
	case "@monthly":
		return "Once a month"
	case "@weekly":
		return "Once a week"
	case "@daily", "@midnight":
		return "Daily at midnight"
	case "@hourly":
		return "Every hour"
	}

	if strings.HasPrefix(expr, "@every ") {
		durStr := strings.TrimPrefix(expr, "@every ")
		if d, err := time.ParseDuration(durStr); err == nil {
			return describeInterval(d)
		}
		return "Every " + durStr
	}

	// Parse 5-field cron for human-readable description
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return expr
	}

	min, hour, dom, _, dow := fields[0], fields[1], fields[2], fields[3], fields[4]

	// "*/N * * * *" → "Every N minutes"
	if strings.HasPrefix(min, "*/") && hour == "*" && dom == "*" && dow == "*" {
		return fmt.Sprintf("Every %s minutes", min[2:])
	}

	// "0 */N * * *" → "Every N hours"
	if min == "0" && strings.HasPrefix(hour, "*/") && dom == "*" && dow == "*" {
		return fmt.Sprintf("Every %s hours", hour[2:])
	}

	// "M H * * *" → "Daily at H:MM"
	if dom == "*" && dow == "*" && !strings.Contains(min, "*") && !strings.Contains(hour, "*") {
		return fmt.Sprintf("Daily at %s", formatTime(hour, min))
	}

	// "M H * * 1-5" → "Weekdays at H:MM"
	if dom == "*" && dow == "1-5" && !strings.Contains(min, "*") && !strings.Contains(hour, "*") {
		return fmt.Sprintf("Weekdays at %s", formatTime(hour, min))
	}

	// "M H * * 0" or "M H * * 0,6" → weekend patterns
	if dom == "*" && (dow == "0,6" || dow == "6,0") && !strings.Contains(min, "*") && !strings.Contains(hour, "*") {
		return fmt.Sprintf("Weekends at %s", formatTime(hour, min))
	}

	// "M H-H * * *" → range patterns
	if dom == "*" && dow == "*" && !strings.Contains(min, "*") && strings.Contains(hour, "-") {
		return fmt.Sprintf("Every hour %s at :%s", hour, min)
	}

	return expr
}

func formatTime(hour, min string) string {
	h := 0
	for _, c := range hour {
		h = h*10 + int(c-'0')
	}
	m := 0
	for _, c := range min {
		m = m*10 + int(c-'0')
	}

	ampm := "AM"
	displayH := h
	if h == 0 {
		displayH = 12
	} else if h == 12 {
		ampm = "PM"
	} else if h > 12 {
		displayH = h - 12
		ampm = "PM"
	}

	if m == 0 {
		return fmt.Sprintf("%d:00 %s", displayH, ampm)
	}
	return fmt.Sprintf("%d:%02d %s", displayH, m, ampm)
}
