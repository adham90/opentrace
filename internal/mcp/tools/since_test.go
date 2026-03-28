package tools

import (
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name      string
		input     string
		wantErr   bool
		checkTime func(t *testing.T, got time.Time)
	}{
		{
			name:    "empty string returns error",
			input:   "",
			wantErr: true,
		},
		{
			name:    "15m returns ~15 minutes ago",
			input:   "15m",
			wantErr: false,
			checkTime: func(t *testing.T, got time.Time) {
				t.Helper()
				expected := now.Add(-15 * time.Minute)
				if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
					t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
				}
			},
		},
		{
			name:    "1h returns ~1 hour ago",
			input:   "1h",
			wantErr: false,
			checkTime: func(t *testing.T, got time.Time) {
				t.Helper()
				expected := now.Add(-1 * time.Hour)
				if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
					t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
				}
			},
		},
		{
			name:    "7d returns ~7 days ago",
			input:   "7d",
			wantErr: false,
			checkTime: func(t *testing.T, got time.Time) {
				t.Helper()
				expected := now.Add(-7 * 24 * time.Hour)
				if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
					t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
				}
			},
		},
		{
			name:    "RFC3339 timestamp returns exact time",
			input:   "2024-01-15T10:30:00Z",
			wantErr: false,
			checkTime: func(t *testing.T, got time.Time) {
				t.Helper()
				expected := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
				if !got.Equal(expected) {
					t.Errorf("expected %v, got %v", expected, got)
				}
			},
		},
		{
			name:    "single char x returns error",
			input:   "x",
			wantErr: true,
		},
		{
			name:    "abc returns error",
			input:   "abc",
			wantErr: true,
		},
		{
			name:    "unknown unit 5w returns error",
			input:   "5w",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSince(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseSince(%q) expected error, got %v", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSince(%q) unexpected error: %v", tt.input, err)
			}
			if tt.checkTime != nil {
				tt.checkTime(t, got)
			}
		})
	}
}

func TestParseSinceOr(t *testing.T) {
	now := time.Now().UTC()
	fallback := 2 * time.Hour

	t.Run("empty string falls back to default duration", func(t *testing.T) {
		got := ParseSinceOr("", fallback)
		expected := now.Add(-fallback)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("valid 1h returns ~1 hour ago", func(t *testing.T) {
		got := ParseSinceOr("1h", fallback)
		expected := now.Add(-1 * time.Hour)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("invalid xxx falls back to default duration", func(t *testing.T) {
		got := ParseSinceOr("xxx", fallback)
		expected := now.Add(-fallback)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})
}

func TestGetSinceParam(t *testing.T) {
	now := time.Now().UTC()
	defaultDur := 1 * time.Hour

	t.Run("has since key uses it", func(t *testing.T) {
		args := map[string]any{"since": "30m"}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-30 * time.Minute)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("has time_range key for backward compat", func(t *testing.T) {
		args := map[string]any{"time_range": "2h"}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-2 * time.Hour)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("has timeframe key", func(t *testing.T) {
		args := map[string]any{"timeframe": "3h"}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-3 * time.Hour)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("no key uses default duration", func(t *testing.T) {
		args := map[string]any{"other": "value"}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-defaultDur)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("empty string values use default duration", func(t *testing.T) {
		args := map[string]any{"since": ""}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-defaultDur)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})

	t.Run("since takes priority over time_range", func(t *testing.T) {
		args := map[string]any{"since": "15m", "time_range": "2h"}
		got := GetSinceParam(args, defaultDur)
		expected := now.Add(-15 * time.Minute)
		if diff := expected.Sub(got).Abs(); diff > 2*time.Second {
			t.Errorf("expected ~%v, got %v (diff %v)", expected, got, diff)
		}
	})
}
