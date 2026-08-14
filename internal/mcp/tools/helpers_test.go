package tools

import (
	"math"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// database_helpers.go tests
// ---------------------------------------------------------------------------

func TestToInt64(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int64
	}{
		{"int64 value", int64(42), 42},
		{"int value", int(10), 10},
		{"float64 truncates", float64(3.7), 3},
		{"nil returns 0", nil, 0},
		{"string returns 0", "x", 0},
		{"bool returns 0", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toInt64(tt.input)
			if got != tt.want {
				t.Errorf("toInt64(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   float64
		wantOK bool
	}{
		{"float64", float64(3.14), 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int64", int64(5), 5.0, true},
		{"int", int(3), 3.0, true},
		{"string numeric", "1.5", 1.5, true},
		{"string non-numeric", "abc", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.input)
			if ok != tt.wantOK {
				t.Errorf("toFloat64(%v) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if tt.wantOK && math.Abs(got-tt.want) > 0.01 {
				t.Errorf("toFloat64(%v) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"nil returns empty", nil, ""},
		{"string passthrough", "hello", "hello"},
		{"int formatted", 42, "42"},
		{"float formatted", 3.14, "3.14"},
		{"bool formatted", true, "true"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toString(tt.input)
			if got != tt.want {
				t.Errorf("toString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"valid name unchanged", "valid_name", "valid_name"},
		{"SQL injection stripped", "DROP TABLE;--", "DROPTABLE"},
		{"empty string", "", ""},
		{"mixed with punctuation", "hello world!", "helloworld"},
		{"numbers preserved", "table123", "table123"},
		{"underscores preserved", "my_table_name", "my_table_name"},
		{"special chars removed", "col@#$%name", "colname"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeIdentifier(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		name  string
		input int64
		want  string
	}{
		{"bytes", 500, "500 B"},
		{"kilobytes", 1500, "1.5 KB"},
		{"megabytes", 1_500_000, "1.4 MB"},
		{"gigabytes", 1_500_000_000, "1.4 GB"},
		{"zero", 0, "0 B"},
		{"exact 1KB", 1024, "1.0 KB"},
		{"exact 1MB", 1 << 20, "1.0 MB"},
		{"exact 1GB", 1 << 30, "1.0 GB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := humanSize(tt.input)
			if got != tt.want {
				t.Errorf("humanSize(%d) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParsePostgresArray(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"three elements", "{a,b,c}", []string{"a", "b", "c"}},
		{"empty braces", "{}", nil},
		{"empty string", "", nil},
		{"single element", "{single}", []string{"single"}},
		{"numbers", "{1,2,3}", []string{"1", "2", "3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePostgresArray(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("parsePostgresArray(%q) = %v, want nil", tt.input, got)
				}
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parsePostgresArray(%q) len = %d, want %d", tt.input, len(got), len(tt.want))
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("parsePostgresArray(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// logs_helpers.go tests
// ---------------------------------------------------------------------------

func TestParseTimeRange(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"empty returns 1 hour default", "", time.Hour, false},
		{"30m", "30m", 30 * time.Minute, false},
		{"2h", "2h", 2 * time.Hour, false},
		{"7d", "7d", 168 * time.Hour, false},
		{"1d", "1d", 24 * time.Hour, false},
		{"15m", "15m", 15 * time.Minute, false},
		{"invalid", "invalid", 0, true},
		{"no number with d", "d", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTimeRange(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseTimeRange(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTimeRange(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseTimeRange(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLogsNormalizeMessage(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, got string)
	}{
		{
			name:  "UUID replaced with star",
			input: "User 550e8400-e29b-41d4-a716-446655440000 not found",
			check: func(t *testing.T, got string) {
				t.Helper()
				if got == "User 550e8400-e29b-41d4-a716-446655440000 not found" {
					t.Error("UUID was not replaced")
				}
				if got != "User * not found" {
					t.Errorf("expected 'User * not found', got %q", got)
				}
			},
		},
		{
			name:  "IP address replaced with star",
			input: "Connection from 192.168.1.100 failed",
			check: func(t *testing.T, got string) {
				t.Helper()
				if got == "Connection from 192.168.1.100 failed" {
					t.Error("IP address was not replaced")
				}
				if got != "Connection from * failed" {
					t.Errorf("expected 'Connection from * failed', got %q", got)
				}
			},
		},
		{
			name:  "numbers replaced with star",
			input: "Processed 42 records in 150 ms",
			check: func(t *testing.T, got string) {
				t.Helper()
				if got == "Processed 42 records in 150 ms" {
					t.Error("numbers were not replaced")
				}
				if got != "Processed * records in * ms" {
					t.Errorf("expected 'Processed * records in * ms', got %q", got)
				}
			},
		},
		{
			name:  "quoted strings replaced",
			input: `Error: "file not found" in module "core"`,
			check: func(t *testing.T, got string) {
				t.Helper()
				if got != `Error: "*" in module "*"` {
					t.Errorf("expected 'Error: \"*\" in module \"*\"', got %q", got)
				}
			},
		},
		{
			name:  "timestamps replaced",
			input: "Event at 2024-01-15T10:30:00Z was logged",
			check: func(t *testing.T, got string) {
				t.Helper()
				if got == "Event at 2024-01-15T10:30:00Z was logged" {
					t.Error("timestamp was not replaced")
				}
				if got != "Event at * was logged" {
					t.Errorf("expected 'Event at * was logged', got %q", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logsNormalizeMessage(tt.input)
			tt.check(t, got)
		})
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  int
	}{
		{"int value", int(5), 5},
		{"int64 value", int64(42), 42},
		{"float64 truncates", float64(3.7), 3},
		{"string returns 0", "x", 0},
		{"nil returns 0", nil, 0},
		{"bool returns 0", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toInt(tt.input)
			if got != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestRound2(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{"pi rounds to 2 decimals", 3.14159, 3.14},
		{"zero", 0.0, 0.0},
		{"negative rounds to 2 decimals", -1.567, -1.56},
		{"already 2 decimals", 2.75, 2.75},
		{"whole number", 5.0, 5.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := round2(tt.input)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("round2(%f) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}

func TestLogsResolvePeriod(t *testing.T) {
	now := time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name      string
		period    string
		wantStart time.Time
		wantEnd   time.Time
		wantErr   bool
	}{
		{
			name:      "last_1h",
			period:    "last_1h",
			wantStart: now.Add(-time.Hour),
			wantEnd:   now,
		},
		{
			name:      "last_6h",
			period:    "last_6h",
			wantStart: now.Add(-6 * time.Hour),
			wantEnd:   now,
		},
		{
			name:      "last_24h",
			period:    "last_24h",
			wantStart: now.Add(-24 * time.Hour),
			wantEnd:   now,
		},
		{
			name:      "today",
			period:    "today",
			wantStart: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			wantEnd:   now,
		},
		{
			name:    "unknown returns error",
			period:  "unknown",
			wantErr: true,
		},
		{
			name:    "empty returns error",
			period:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := logsResolvePeriod(tt.period, now)
			if tt.wantErr {
				if err == nil {
					t.Errorf("logsResolvePeriod(%q) expected error", tt.period)
				}
				return
			}
			if err != nil {
				t.Fatalf("logsResolvePeriod(%q) unexpected error: %v", tt.period, err)
			}
			if !start.Equal(tt.wantStart) {
				t.Errorf("start = %v, want %v", start, tt.wantStart)
			}
			if !end.Equal(tt.wantEnd) {
				t.Errorf("end = %v, want %v", end, tt.wantEnd)
			}
		})
	}
}

func TestLogsResolveBaseline(t *testing.T) {
	curStart := time.Date(2024, 6, 15, 13, 0, 0, 0, time.UTC)
	curEnd := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
	// duration = 1 hour

	tests := []struct {
		name      string
		baseline  string
		wantStart time.Time
		wantEnd   time.Time
		wantErr   bool
	}{
		{
			name:      "previous returns same duration before start",
			baseline:  "previous",
			wantStart: curStart.Add(-time.Hour), // 12:00
			wantEnd:   curStart,                 // 13:00
		},
		{
			name:      "yesterday_same_time returns 24h before",
			baseline:  "yesterday_same_time",
			wantStart: curStart.Add(-24 * time.Hour),
			wantEnd:   curEnd.Add(-24 * time.Hour),
		},
		{
			name:      "last_week_same_time returns 168h before",
			baseline:  "last_week_same_time",
			wantStart: curStart.Add(-168 * time.Hour),
			wantEnd:   curEnd.Add(-168 * time.Hour),
		},
		{
			name:     "unknown returns error",
			baseline: "unknown",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, err := logsResolveBaseline(tt.baseline, curStart, curEnd)
			if tt.wantErr {
				if err == nil {
					t.Errorf("logsResolveBaseline(%q) expected error", tt.baseline)
				}
				return
			}
			if err != nil {
				t.Fatalf("logsResolveBaseline(%q) unexpected error: %v", tt.baseline, err)
			}
			if !start.Equal(tt.wantStart) {
				t.Errorf("start = %v, want %v", start, tt.wantStart)
			}
			if !end.Equal(tt.wantEnd) {
				t.Errorf("end = %v, want %v", end, tt.wantEnd)
			}
		})
	}
}

func TestLogsCalcChange(t *testing.T) {
	tests := []struct {
		name         string
		baseline     int
		current      int
		wantPct      float64
		wantCategory string
	}{
		{"both zero", 0, 0, 0, "unchanged"},
		// A jump from zero is scored as growth from a baseline of one so that
		// "biggest movers" ranking keeps its magnitude; the old flat 100 sorted
		// a brand-new 0→5000 incident below routine 10→50 drift.
		{"zero baseline with current", 0, 5, 500, "new"},
		{"zero baseline with large current outranks drift", 0, 5000, 500000, "new"},
		{"increase", 10, 15, 50, "increase"},
		{"decrease", 10, 5, -50, "decrease"},
		{"unchanged", 10, 10, 0, "unchanged"},
		{"large increase", 100, 300, 200, "increase"},
		{"small decrease", 100, 90, -10, "decrease"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pct, category := logsCalcChange(tt.baseline, tt.current)
			if math.Abs(pct-tt.wantPct) > 0.01 {
				t.Errorf("logsCalcChange(%d, %d) pct = %f, want %f", tt.baseline, tt.current, pct, tt.wantPct)
			}
			if category != tt.wantCategory {
				t.Errorf("logsCalcChange(%d, %d) category = %q, want %q", tt.baseline, tt.current, category, tt.wantCategory)
			}
		})
	}
}

func TestLogsBuildTimeHistogram(t *testing.T) {
	t.Run("empty entries returns nil", func(t *testing.T) {
		got := logsBuildTimeHistogram(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		got := logsBuildTimeHistogram([]store.LogEntry{})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("single entry returns nil due to zero span", func(t *testing.T) {
		now := time.Now().UTC()
		entries := []store.LogEntry{
			{Timestamp: now},
		}
		got := logsBuildTimeHistogram(entries)
		if got != nil {
			t.Errorf("expected nil for single entry, got %v", got)
		}
	})

	t.Run("multiple entries across 10 minutes produces buckets with 1m size", func(t *testing.T) {
		base := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
		entries := []store.LogEntry{
			{Timestamp: base},
			{Timestamp: base.Add(2 * time.Minute)},
			{Timestamp: base.Add(5 * time.Minute)},
			{Timestamp: base.Add(7 * time.Minute)},
			{Timestamp: base.Add(10 * time.Minute)},
		}
		got := logsBuildTimeHistogram(entries)
		if got == nil {
			t.Fatal("expected non-nil result")
		}

		bucketSize, ok := got["bucket_size"].(string)
		if !ok {
			t.Fatal("expected bucket_size to be string")
		}
		// 10 minute span falls in the 5m < span <= 30m range, so bucket_size = "1m"
		if bucketSize != "1m" {
			t.Errorf("bucket_size = %q, want %q", bucketSize, "1m")
		}

		buckets, ok := got["buckets"].(map[string]int)
		if !ok {
			t.Fatal("expected buckets to be map[string]int")
		}
		if len(buckets) == 0 {
			t.Error("expected non-empty buckets")
		}

		// Verify total count across all buckets equals number of entries.
		total := 0
		for _, count := range buckets {
			total += count
		}
		if total != len(entries) {
			t.Errorf("total bucket count = %d, want %d", total, len(entries))
		}
	})

	t.Run("short span uses 30s buckets", func(t *testing.T) {
		base := time.Date(2024, 6, 15, 14, 0, 0, 0, time.UTC)
		entries := []store.LogEntry{
			{Timestamp: base},
			{Timestamp: base.Add(1 * time.Minute)},
			{Timestamp: base.Add(3 * time.Minute)},
		}
		got := logsBuildTimeHistogram(entries)
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		bucketSize := got["bucket_size"].(string)
		// 3 minute span falls in span <= 5m range, so bucket_size = "30s"
		if bucketSize != "30s" {
			t.Errorf("bucket_size = %q, want %q", bucketSize, "30s")
		}
	})

	t.Run("long span uses 1h buckets", func(t *testing.T) {
		base := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
		entries := []store.LogEntry{
			{Timestamp: base},
			{Timestamp: base.Add(6 * time.Hour)},
			{Timestamp: base.Add(13 * time.Hour)},
		}
		got := logsBuildTimeHistogram(entries)
		if got == nil {
			t.Fatal("expected non-nil result")
		}
		bucketSize := got["bucket_size"].(string)
		// 13 hour span > 12h, so bucket_size = "1h"
		if bucketSize != "1h" {
			t.Errorf("bucket_size = %q, want %q", bucketSize, "1h")
		}
	})
}
