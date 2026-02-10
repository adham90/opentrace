package watcher

import (
	"fmt"
	"math"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// Default adaptive scheduling values.
const (
	DefaultEscalationDuration = 30 * time.Minute
	DefaultCooldownRuns       = 3
	DefaultRelaxAfterRuns     = 20
	DefaultRelaxSkipRuns      = 1
	DefaultBackoffMultiplier  = 2.0
	DefaultMaxBackoffInterval = 1 * time.Hour
	DefaultMaxConsecErrors    = 5
	MinEscalatedInterval      = 1 * time.Minute
)

// RunOutcome describes the result of a single watcher execution.
type RunOutcome struct {
	Alerted bool
	Errored bool
}

// AdaptiveTransition describes the state change after evaluating a run outcome.
type AdaptiveTransition struct {
	NewState      store.AdaptiveState
	NewInterval   string // effective interval for next_run_at (empty = no change)
	NewTimeRange  string // effective time_range for log lookback (empty = no change)
	BaseTimeRange string // set once on first escalation (empty = don't update)
	EscalatedAt   *time.Time
	CleanRuns     int
	ErrorCount    int
	ShouldPause   bool
	LogMessage    string
}

// AdaptiveEngine computes state transitions for adaptive scheduling.
type AdaptiveEngine struct{}

// Transition computes the next adaptive state given the current watcher and run outcome.
func (e *AdaptiveEngine) Transition(w *store.Watcher, outcome RunOutcome, now time.Time) AdaptiveTransition {
	cfg := w.AdaptiveConfig
	if cfg == nil || !cfg.Enabled {
		return AdaptiveTransition{
			NewState:   w.AdaptiveState,
			CleanRuns:  w.ConsecutiveCleanRuns,
			ErrorCount: w.ConsecutiveErrors,
		}
	}

	state := w.AdaptiveState
	cleanRuns := w.ConsecutiveCleanRuns
	errCount := w.ConsecutiveErrors

	baseInterval := resolveBaseInterval(w)
	baseTimeRange := w.BaseTimeRange
	if baseTimeRange == "" {
		baseTimeRange = w.TimeRange
	}

	// --- Error path ---
	if outcome.Errored {
		errCount++
		maxErrors := cfgInt(cfg.MaxConsecErrors, DefaultMaxConsecErrors)
		if errCount >= maxErrors {
			return AdaptiveTransition{
				NewState:    store.AdaptiveError,
				CleanRuns:   cleanRuns,
				ErrorCount:  errCount,
				ShouldPause: true,
				LogMessage:  fmt.Sprintf("paused after %d consecutive errors", errCount),
			}
		}
		multiplier := cfgFloat(cfg.BackoffMultiplier, DefaultBackoffMultiplier)
		maxBackoff := parseDurationOr(cfg.MaxBackoffInterval, DefaultMaxBackoffInterval)
		backoffDur := time.Duration(float64(baseInterval) * math.Pow(multiplier, float64(errCount)))
		if backoffDur > maxBackoff {
			backoffDur = maxBackoff
		}
		backoffStr := formatDuration(backoffDur)
		return AdaptiveTransition{
			NewState:    store.AdaptiveBackoff,
			NewInterval: backoffStr,
			CleanRuns:   cleanRuns,
			ErrorCount:  errCount,
			LogMessage:  fmt.Sprintf("backing off → %s (error %d)", backoffStr, errCount),
		}
	}

	// --- Alert path ---
	if outcome.Alerted {
		cleanRuns = 0

		if state == store.AdaptiveSustained {
			// Already past escalation, stay sustained
			return AdaptiveTransition{
				NewState:   store.AdaptiveSustained,
				CleanRuns:  0,
				ErrorCount: 0,
				LogMessage: "sustained alert continues",
			}
		}

		if state != store.AdaptiveEscalated {
			// Enter escalation
			escInterval := resolveEscalatedInterval(cfg, baseInterval)
			escIntervalStr := formatDuration(escInterval)
			return AdaptiveTransition{
				NewState:      store.AdaptiveEscalated,
				NewInterval:   escIntervalStr,
				NewTimeRange:  escIntervalStr,
				BaseTimeRange: baseTimeRange,
				EscalatedAt:   &now,
				CleanRuns:     0,
				ErrorCount:    0,
				LogMessage:    fmt.Sprintf("escalated → checking every %s", escIntervalStr),
			}
		}

		// Already escalated — check if duration expired
		escDuration := parseDurationOr(cfg.EscalationDuration, DefaultEscalationDuration)
		if w.EscalatedAt != nil && now.Sub(*w.EscalatedAt) > escDuration {
			baseIntervalStr := formatDuration(baseInterval)
			return AdaptiveTransition{
				NewState:     store.AdaptiveSustained,
				NewInterval:  baseIntervalStr,
				NewTimeRange: baseTimeRange,
				CleanRuns:    0,
				ErrorCount:   0,
				LogMessage:   fmt.Sprintf("escalation expired after %s → sustained (normal interval)", escDuration),
			}
		}

		// Stay escalated
		return AdaptiveTransition{
			NewState:   store.AdaptiveEscalated,
			CleanRuns:  0,
			ErrorCount: 0,
			LogMessage: "escalated, still alerting",
		}
	}

	// --- Clean run path ---
	cleanRuns++
	errCount = 0

	baseIntervalStr := formatDuration(baseInterval)

	switch state {
	case store.AdaptiveEscalated:
		cooldown := cfgInt(cfg.CooldownRuns, DefaultCooldownRuns)
		if cleanRuns >= cooldown {
			return AdaptiveTransition{
				NewState:     store.AdaptiveNormal,
				NewInterval:  baseIntervalStr,
				NewTimeRange: baseTimeRange,
				CleanRuns:    cleanRuns,
				ErrorCount:   0,
				LogMessage:   fmt.Sprintf("de-escalated after %d clean runs → normal", cleanRuns),
			}
		}

	case store.AdaptiveSustained:
		cooldown := cfgInt(cfg.CooldownRuns, DefaultCooldownRuns)
		if cleanRuns >= cooldown {
			return AdaptiveTransition{
				NewState:     store.AdaptiveNormal,
				NewInterval:  baseIntervalStr,
				NewTimeRange: baseTimeRange,
				CleanRuns:    cleanRuns,
				ErrorCount:   0,
				LogMessage:   fmt.Sprintf("sustained → normal after %d clean runs", cleanRuns),
			}
		}

	case store.AdaptiveNormal:
		if cfg.RelaxEnabled {
			relaxAfter := cfgInt(cfg.RelaxAfterRuns, DefaultRelaxAfterRuns)
			if cleanRuns >= relaxAfter {
				relaxedInterval := parseDurationOr(cfg.RelaxedInterval, 2*time.Hour)
				relaxedStr := formatDuration(relaxedInterval)
				return AdaptiveTransition{
					NewState:     store.AdaptiveRelaxed,
					NewInterval:  relaxedStr,
					NewTimeRange: relaxedStr,
					CleanRuns:    cleanRuns,
					ErrorCount:   0,
					LogMessage:   fmt.Sprintf("relaxed after %d clean runs → checking every %s", cleanRuns, relaxedStr),
				}
			}
		}

	case store.AdaptiveBackoff:
		return AdaptiveTransition{
			NewState:     store.AdaptiveNormal,
			NewInterval:  baseIntervalStr,
			NewTimeRange: baseTimeRange,
			CleanRuns:    cleanRuns,
			ErrorCount:   0,
			LogMessage:   "backoff cleared → normal",
		}
	}

	// No state change
	return AdaptiveTransition{
		NewState:   state,
		CleanRuns:  cleanRuns,
		ErrorCount: errCount,
	}
}

// resolveBaseInterval returns the base polling interval for the watcher.
func resolveBaseInterval(w *store.Watcher) time.Duration {
	// Prefer explicit schedule; fall back to time_range
	expr := w.Schedule
	if expr == "" {
		expr = w.BaseTimeRange
	}
	if expr == "" {
		expr = w.TimeRange
	}
	dur := tryParseInterval(expr)
	if dur > 0 {
		return dur
	}
	return 15 * time.Minute // fallback
}

// resolveEscalatedInterval returns the escalated interval, using config or 1/4 of base.
func resolveEscalatedInterval(cfg *store.AdaptiveConfig, baseInterval time.Duration) time.Duration {
	if cfg.EscalatedInterval != "" {
		if d := tryParseInterval(cfg.EscalatedInterval); d > 0 {
			return d
		}
	}
	quarter := baseInterval / 4
	if quarter < MinEscalatedInterval {
		return MinEscalatedInterval
	}
	return quarter
}

// parseDurationOr parses a duration string like "30m", returning fallback on failure.
func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d := tryParseInterval(s)
	if d > 0 {
		return d
	}
	return fallback
}

// formatDuration converts a duration to a compact string like "1m", "15m", "2h".
func formatDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return fmt.Sprintf("%ds", int(d/time.Second))
}

func cfgInt(val, def int) int {
	if val > 0 {
		return val
	}
	return def
}

func cfgFloat(val, def float64) float64 {
	if val > 0 {
		return val
	}
	return def
}
