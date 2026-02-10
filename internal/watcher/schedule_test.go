package watcher

import (
	"testing"
	"time"
)

func TestParseSchedule_Intervals(t *testing.T) {
	tests := []struct {
		expr     string
		wantDur  time.Duration
		wantDesc string
	}{
		{"5m", 5 * time.Minute, "Every 5 minutes"},
		{"1m", 1 * time.Minute, "Every minute"},
		{"15m", 15 * time.Minute, "Every 15 minutes"},
		{"30m", 30 * time.Minute, "Every 30 minutes"},
		{"1h", 1 * time.Hour, "Every hour"},
		{"6h", 6 * time.Hour, "Every 6 hours"},
		{"24h", 24 * time.Hour, "Every 24 hours"},
		{"30s", 30 * time.Second, "Every 30 seconds"},
	}

	for _, tt := range tests {
		s, err := ParseSchedule(tt.expr)
		if err != nil {
			t.Errorf("ParseSchedule(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if !s.IsInterval() {
			t.Errorf("ParseSchedule(%q): expected interval mode", tt.expr)
		}
		if s.interval != tt.wantDur {
			t.Errorf("ParseSchedule(%q): interval = %v, want %v", tt.expr, s.interval, tt.wantDur)
		}
		if s.Description() != tt.wantDesc {
			t.Errorf("ParseSchedule(%q): Description() = %q, want %q", tt.expr, s.Description(), tt.wantDesc)
		}
	}
}

func TestParseSchedule_IntervalNext(t *testing.T) {
	s, err := ParseSchedule("5m")
	if err != nil {
		t.Fatalf("ParseSchedule(5m): %v", err)
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := base.Add(5 * time.Minute)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}

func TestParseSchedule_CronExpressions(t *testing.T) {
	tests := []struct {
		expr     string
		wantDesc string
	}{
		{"*/5 * * * *", "Every 5 minutes"},
		{"0 */2 * * *", "Every 2 hours"},
		{"0 9 * * *", "Daily at 9:00 AM"},
		{"30 14 * * *", "Daily at 2:30 PM"},
		{"0 9 * * 1-5", "Weekdays at 9:00 AM"},
		{"0 0 * * *", "Daily at 12:00 AM"},
	}

	for _, tt := range tests {
		s, err := ParseSchedule(tt.expr)
		if err != nil {
			t.Errorf("ParseSchedule(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if s.IsInterval() {
			t.Errorf("ParseSchedule(%q): expected cron mode, got interval", tt.expr)
		}
		if s.Description() != tt.wantDesc {
			t.Errorf("ParseSchedule(%q): Description() = %q, want %q", tt.expr, s.Description(), tt.wantDesc)
		}
	}
}

func TestParseSchedule_CronNext(t *testing.T) {
	// "0 9 * * *" — daily at 9:00 AM
	s, err := ParseSchedule("0 9 * * *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	// From midnight → next is 9 AM same day
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}

	// From 10 AM → next is 9 AM next day
	base2 := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	next2 := s.Next(base2)
	want2 := time.Date(2025, 1, 2, 9, 0, 0, 0, time.UTC)
	if !next2.Equal(want2) {
		t.Errorf("Next(%v) = %v, want %v", base2, next2, want2)
	}
}

func TestParseSchedule_WeekdayCron(t *testing.T) {
	// "0 9 * * 1-5" — weekdays at 9 AM
	s, err := ParseSchedule("0 9 * * 1-5")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	// Friday at 10 AM → next is Monday 9 AM
	friday := time.Date(2025, 1, 3, 10, 0, 0, 0, time.UTC) // Jan 3, 2025 is a Friday
	next := s.Next(friday)
	monday := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC)
	if !next.Equal(monday) {
		t.Errorf("Next(Friday 10AM) = %v (%s), want %v (%s)", next, next.Weekday(), monday, monday.Weekday())
	}
}

func TestParseSchedule_Predefined(t *testing.T) {
	tests := []struct {
		expr     string
		wantDesc string
	}{
		{"@hourly", "Every hour"},
		{"@daily", "Daily at midnight"},
		{"@weekly", "Once a week"},
	}

	for _, tt := range tests {
		s, err := ParseSchedule(tt.expr)
		if err != nil {
			t.Errorf("ParseSchedule(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if s.IsInterval() {
			t.Errorf("ParseSchedule(%q): expected cron mode", tt.expr)
		}
		if s.Description() != tt.wantDesc {
			t.Errorf("ParseSchedule(%q): Description() = %q, want %q", tt.expr, s.Description(), tt.wantDesc)
		}
	}
}

func TestParseSchedule_EveryDuration(t *testing.T) {
	s, err := ParseSchedule("@every 30s")
	if err != nil {
		t.Fatalf("ParseSchedule(@every 30s): %v", err)
	}
	if s.IsInterval() {
		t.Error("expected cron mode for @every")
	}
	if s.Description() != "Every 30 seconds" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Every 30 seconds")
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := base.Add(30 * time.Second)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}

func TestParseSchedule_Invalid(t *testing.T) {
	invalids := []string{
		"",
		"invalid",
		"abc",
		"0m",
		"-5m",
		"5x",
		"* * *",           // too few fields
		"* * * * * * *",   // too many fields
		"60 * * * *",      // invalid minute
		"@never",
	}

	for _, expr := range invalids {
		_, err := ParseSchedule(expr)
		if err == nil {
			t.Errorf("ParseSchedule(%q): expected error, got nil", expr)
		}
	}
}

func TestParseSchedule_Expression(t *testing.T) {
	s, _ := ParseSchedule("0 9 * * 1-5")
	if s.Expression != "0 9 * * 1-5" {
		t.Errorf("Expression = %q, want %q", s.Expression, "0 9 * * 1-5")
	}
}

func TestParseSchedule_EveryMinuteCron(t *testing.T) {
	// "*/1 * * * *" — every minute via cron
	s, err := ParseSchedule("*/1 * * * *")
	if err != nil {
		t.Fatalf("ParseSchedule: %v", err)
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	next := s.Next(base)
	want := time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("Next(%v) = %v, want %v", base, next, want)
	}
}

func TestDescribeInterval(t *testing.T) {
	tests := []struct {
		dur  time.Duration
		want string
	}{
		{1 * time.Second, "Every second"},
		{30 * time.Second, "Every 30 seconds"},
		{1 * time.Minute, "Every minute"},
		{5 * time.Minute, "Every 5 minutes"},
		{1 * time.Hour, "Every hour"},
		{6 * time.Hour, "Every 6 hours"},
	}

	for _, tt := range tests {
		got := describeInterval(tt.dur)
		if got != tt.want {
			t.Errorf("describeInterval(%v) = %q, want %q", tt.dur, got, tt.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	tests := []struct {
		hour, min string
		want      string
	}{
		{"0", "0", "12:00 AM"},
		{"9", "0", "9:00 AM"},
		{"12", "0", "12:00 PM"},
		{"14", "30", "2:30 PM"},
		{"23", "59", "11:59 PM"},
	}

	for _, tt := range tests {
		got := formatTime(tt.hour, tt.min)
		if got != tt.want {
			t.Errorf("formatTime(%q, %q) = %q, want %q", tt.hour, tt.min, got, tt.want)
		}
	}
}
