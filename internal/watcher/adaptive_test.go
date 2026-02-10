package watcher

import (
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

func TestAdaptive_Disabled(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveState: store.AdaptiveNormal,
		TimeRange:     "15m",
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("disabled: state = %s, want normal", tr.NewState)
	}
	if tr.NewInterval != "" {
		t.Errorf("disabled: interval = %q, want empty", tr.NewInterval)
	}
}

func TestAdaptive_NilConfig(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: nil,
		AdaptiveState:  store.AdaptiveNormal,
		TimeRange:      "15m",
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("nil config: state = %s, want normal", tr.NewState)
	}
}

func TestAdaptive_NormalToEscalated(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true, EscalatedInterval: "1m"},
		AdaptiveState:  store.AdaptiveNormal,
		TimeRange:      "15m",
	}
	now := time.Now()
	tr := engine.Transition(w, RunOutcome{Alerted: true}, now)
	if tr.NewState != store.AdaptiveEscalated {
		t.Errorf("state = %s, want escalated", tr.NewState)
	}
	if tr.NewInterval != "1m" {
		t.Errorf("interval = %q, want 1m", tr.NewInterval)
	}
	if tr.NewTimeRange != "1m" {
		t.Errorf("time_range = %q, want 1m", tr.NewTimeRange)
	}
	if tr.EscalatedAt == nil {
		t.Error("escalated_at should be set")
	}
	if tr.BaseTimeRange != "15m" {
		t.Errorf("base_time_range = %q, want 15m", tr.BaseTimeRange)
	}
	if tr.CleanRuns != 0 {
		t.Errorf("clean_runs = %d, want 0", tr.CleanRuns)
	}
}

func TestAdaptive_EscalatedInterval_Default(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true},
		AdaptiveState:  store.AdaptiveNormal,
		TimeRange:      "20m",
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveEscalated {
		t.Errorf("state = %s, want escalated", tr.NewState)
	}
	// 20m / 4 = 5m
	if tr.NewInterval != "5m" {
		t.Errorf("default escalated interval = %q, want 5m", tr.NewInterval)
	}
}

func TestAdaptive_EscalatedInterval_MinCap(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true},
		AdaptiveState:  store.AdaptiveNormal,
		TimeRange:      "2m", // 2m / 4 = 30s, below minimum
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	// Should be capped at 1m minimum
	if tr.NewInterval != "1m" {
		t.Errorf("min-capped escalated interval = %q, want 1m", tr.NewInterval)
	}
}

func TestAdaptive_EscalatedToNormal_AfterCooldown(t *testing.T) {
	engine := &AdaptiveEngine{}
	escAt := time.Now().Add(-5 * time.Minute)
	w := &store.Watcher{
		AdaptiveConfig:       &store.AdaptiveConfig{Enabled: true, CooldownRuns: 3},
		AdaptiveState:        store.AdaptiveEscalated,
		ConsecutiveCleanRuns: 2, // This will become 3 after increment
		TimeRange:            "1m",
		BaseTimeRange:        "15m",
		EscalatedAt:          &escAt,
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("state = %s, want normal", tr.NewState)
	}
	if tr.NewInterval != "15m" {
		t.Errorf("interval = %q, want 15m (restored)", tr.NewInterval)
	}
	if tr.NewTimeRange != "15m" {
		t.Errorf("time_range = %q, want 15m (restored)", tr.NewTimeRange)
	}
	if tr.CleanRuns != 3 {
		t.Errorf("clean_runs = %d, want 3", tr.CleanRuns)
	}
}

func TestAdaptive_EscalatedStays_BeforeCooldown(t *testing.T) {
	engine := &AdaptiveEngine{}
	escAt := time.Now().Add(-5 * time.Minute)
	w := &store.Watcher{
		AdaptiveConfig:       &store.AdaptiveConfig{Enabled: true, CooldownRuns: 3},
		AdaptiveState:        store.AdaptiveEscalated,
		ConsecutiveCleanRuns: 0, // Will become 1
		TimeRange:            "1m",
		BaseTimeRange:        "15m",
		EscalatedAt:          &escAt,
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveEscalated {
		t.Errorf("state = %s, want escalated (only 1 clean run)", tr.NewState)
	}
}

func TestAdaptive_EscalatedToSustained_DurationExpired(t *testing.T) {
	engine := &AdaptiveEngine{}
	escAt := time.Now().Add(-35 * time.Minute)
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true, EscalationDuration: "30m"},
		AdaptiveState:  store.AdaptiveEscalated,
		TimeRange:      "1m",
		BaseTimeRange:  "15m",
		EscalatedAt:    &escAt,
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveSustained {
		t.Errorf("state = %s, want sustained", tr.NewState)
	}
	if tr.NewInterval != "15m" {
		t.Errorf("interval = %q, want 15m (back to normal interval)", tr.NewInterval)
	}
	if tr.NewTimeRange != "15m" {
		t.Errorf("time_range = %q, want 15m", tr.NewTimeRange)
	}
}

func TestAdaptive_SustainedStays_WhileAlerting(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true},
		AdaptiveState:  store.AdaptiveSustained,
		TimeRange:      "15m",
		BaseTimeRange:  "15m",
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveSustained {
		t.Errorf("state = %s, want sustained", tr.NewState)
	}
}

func TestAdaptive_SustainedToNormal_AfterCooldown(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig:       &store.AdaptiveConfig{Enabled: true, CooldownRuns: 3},
		AdaptiveState:        store.AdaptiveSustained,
		ConsecutiveCleanRuns: 2, // Will become 3
		TimeRange:            "15m",
		BaseTimeRange:        "15m",
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("state = %s, want normal", tr.NewState)
	}
}

func TestAdaptive_NormalToRelaxed(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{
			Enabled:         true,
			RelaxEnabled:    true,
			RelaxAfterRuns:  20,
			RelaxedInterval: "2h",
		},
		AdaptiveState:        store.AdaptiveNormal,
		ConsecutiveCleanRuns: 19, // Will become 20
		TimeRange:            "15m",
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveRelaxed {
		t.Errorf("state = %s, want relaxed", tr.NewState)
	}
	if tr.NewInterval != "2h" {
		t.Errorf("interval = %q, want 2h", tr.NewInterval)
	}
	if tr.NewTimeRange != "2h" {
		t.Errorf("time_range = %q, want 2h", tr.NewTimeRange)
	}
}

func TestAdaptive_RelaxedToEscalated(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{Enabled: true, EscalatedInterval: "1m"},
		AdaptiveState:  store.AdaptiveRelaxed,
		TimeRange:      "2h",
		BaseTimeRange:  "15m",
	}
	tr := engine.Transition(w, RunOutcome{Alerted: true}, time.Now())
	if tr.NewState != store.AdaptiveEscalated {
		t.Errorf("state = %s, want escalated", tr.NewState)
	}
	if tr.NewInterval != "1m" {
		t.Errorf("interval = %q, want 1m", tr.NewInterval)
	}
	if tr.EscalatedAt == nil {
		t.Error("escalated_at should be set")
	}
}

func TestAdaptive_BackoffOnError(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig:    &store.AdaptiveConfig{Enabled: true, BackoffMultiplier: 2.0, MaxBackoffInterval: "1h"},
		AdaptiveState:     store.AdaptiveNormal,
		ConsecutiveErrors: 0, // Will become 1
		TimeRange:         "15m",
	}
	tr := engine.Transition(w, RunOutcome{Errored: true}, time.Now())
	if tr.NewState != store.AdaptiveBackoff {
		t.Errorf("state = %s, want backing_off", tr.NewState)
	}
	if tr.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", tr.ErrorCount)
	}
	// 15m * 2^1 = 30m
	if tr.NewInterval != "30m" {
		t.Errorf("backoff interval = %q, want 30m", tr.NewInterval)
	}
}

func TestAdaptive_BackoffCapped(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig:    &store.AdaptiveConfig{Enabled: true, BackoffMultiplier: 2.0, MaxBackoffInterval: "1h"},
		AdaptiveState:     store.AdaptiveBackoff,
		ConsecutiveErrors: 3, // Will become 4, 15m * 2^4 = 240m, capped at 60m
		TimeRange:         "15m",
	}
	tr := engine.Transition(w, RunOutcome{Errored: true}, time.Now())
	if tr.NewState != store.AdaptiveBackoff {
		t.Errorf("state = %s, want backing_off", tr.NewState)
	}
	if tr.NewInterval != "1h" {
		t.Errorf("capped backoff interval = %q, want 1h", tr.NewInterval)
	}
}

func TestAdaptive_BackoffToNormal_OnSuccess(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig:    &store.AdaptiveConfig{Enabled: true},
		AdaptiveState:     store.AdaptiveBackoff,
		ConsecutiveErrors: 2,
		TimeRange:         "15m",
		BaseTimeRange:     "15m",
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("state = %s, want normal", tr.NewState)
	}
	if tr.NewInterval != "15m" {
		t.Errorf("interval = %q, want 15m (restored)", tr.NewInterval)
	}
	if tr.ErrorCount != 0 {
		t.Errorf("error_count = %d, want 0", tr.ErrorCount)
	}
}

func TestAdaptive_ErrorPause_AfterMaxErrors(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig:    &store.AdaptiveConfig{Enabled: true, MaxConsecErrors: 5},
		AdaptiveState:     store.AdaptiveBackoff,
		ConsecutiveErrors: 4, // Will become 5
		TimeRange:         "15m",
	}
	tr := engine.Transition(w, RunOutcome{Errored: true}, time.Now())
	if tr.NewState != store.AdaptiveError {
		t.Errorf("state = %s, want error", tr.NewState)
	}
	if !tr.ShouldPause {
		t.Error("should_pause = false, want true")
	}
	if tr.ErrorCount != 5 {
		t.Errorf("error_count = %d, want 5", tr.ErrorCount)
	}
}

func TestAdaptive_RelaxDisabled_StaysNormal(t *testing.T) {
	engine := &AdaptiveEngine{}
	w := &store.Watcher{
		AdaptiveConfig: &store.AdaptiveConfig{
			Enabled:      true,
			RelaxEnabled: false,
		},
		AdaptiveState:        store.AdaptiveNormal,
		ConsecutiveCleanRuns: 100,
		TimeRange:            "15m",
	}
	tr := engine.Transition(w, RunOutcome{}, time.Now())
	if tr.NewState != store.AdaptiveNormal {
		t.Errorf("state = %s, want normal (relax disabled)", tr.NewState)
	}
}

func TestAdaptive_TimeRangeAdapts(t *testing.T) {
	engine := &AdaptiveEngine{}

	tests := []struct {
		name          string
		state         store.AdaptiveState
		outcome       RunOutcome
		wantTimeRange string
	}{
		{
			name:          "escalation sets time_range to escalated interval",
			state:         store.AdaptiveNormal,
			outcome:       RunOutcome{Alerted: true},
			wantTimeRange: "1m",
		},
		{
			name:          "relaxation sets time_range to relaxed interval",
			state:         store.AdaptiveNormal,
			outcome:       RunOutcome{},
			wantTimeRange: "2h",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &store.Watcher{
				AdaptiveConfig: &store.AdaptiveConfig{
					Enabled:           true,
					EscalatedInterval: "1m",
					RelaxEnabled:      true,
					RelaxAfterRuns:    1,
					RelaxedInterval:   "2h",
				},
				AdaptiveState:        tt.state,
				ConsecutiveCleanRuns: 0,
				TimeRange:            "15m",
			}
			tr := engine.Transition(w, tt.outcome, time.Now())
			if tr.NewTimeRange != tt.wantTimeRange {
				t.Errorf("time_range = %q, want %q", tr.NewTimeRange, tt.wantTimeRange)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{1 * time.Minute, "1m"},
		{15 * time.Minute, "15m"},
		{1 * time.Hour, "1h"},
		{2 * time.Hour, "2h"},
		{90 * time.Second, "90s"},
		{30 * time.Minute, "30m"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestResolveBaseInterval(t *testing.T) {
	tests := []struct {
		name     string
		w        *store.Watcher
		wantDur  time.Duration
	}{
		{
			name:    "from schedule",
			w:       &store.Watcher{Schedule: "5m", TimeRange: "15m"},
			wantDur: 5 * time.Minute,
		},
		{
			name:    "from base_time_range",
			w:       &store.Watcher{BaseTimeRange: "10m", TimeRange: "1m"},
			wantDur: 10 * time.Minute,
		},
		{
			name:    "from time_range fallback",
			w:       &store.Watcher{TimeRange: "20m"},
			wantDur: 20 * time.Minute,
		},
		{
			name:    "cron schedule falls back to base_time_range",
			w:       &store.Watcher{Schedule: "0 9 * * *", BaseTimeRange: "15m"},
			wantDur: 15 * time.Minute,
		},
		{
			name:    "empty falls back to 15m",
			w:       &store.Watcher{},
			wantDur: 15 * time.Minute,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveBaseInterval(tt.w)
			if got != tt.wantDur {
				t.Errorf("resolveBaseInterval = %v, want %v", got, tt.wantDur)
			}
		})
	}
}
