package tools

import (
	"testing"
	"time"
)

// Several log tools read only "time_range" and silently ignored "since", so a
// caller asking for 24h got a 1h answer with no sign of the substitution.
// ResolveWindow is the one place that decision is made now.
func TestResolveWindow_AcceptsEveryName(t *testing.T) {
	for _, key := range []string{"since", "time_range", "timeframe"} {
		got, label, err := ResolveWindow(map[string]any{key: "6h"}, "1h")
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if d := time.Since(got); d < 5*time.Hour+50*time.Minute || d > 6*time.Hour+10*time.Minute {
			t.Errorf("%s=6h resolved to %v ago", key, d)
		}
		if label == "" {
			t.Errorf("%s: window label is empty", key)
		}
	}
}

func TestResolveWindow_DefaultsWhenAbsent(t *testing.T) {
	got, label, err := ResolveWindow(map[string]any{}, "24h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d := time.Since(got); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("default window resolved to %v ago, want ~24h", d)
	}
	if label != "24h" {
		t.Errorf("label = %q, want the default it applied", label)
	}
}

// A typo must not fall back to the default. Silently answering about a
// different window than the one asked for is the whole bug being fixed here.
func TestResolveWindow_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{"yesterday", "6", "6 hours", "-1h"} {
		if _, _, err := ResolveWindow(map[string]any{"since": bad}, "1h"); err == nil {
			t.Errorf("since=%q was accepted; it should be rejected, not defaulted", bad)
		}
	}
}

// "since" wins over the legacy names when more than one is supplied, matching
// GetSinceParam's documented priority.
func TestResolveWindow_SincePriority(t *testing.T) {
	got, _, err := ResolveWindow(map[string]any{"since": "2h", "time_range": "48h"}, "1h")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d := time.Since(got); d > 3*time.Hour {
		t.Errorf("resolved to %v ago; since=2h should win over time_range=48h", d)
	}
}
