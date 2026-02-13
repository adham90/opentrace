package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

// SequenceEvaluator detects ordered patterns of events within a time window.
type SequenceEvaluator struct {
	logStore   store.LogStore
	alertStore store.AlertStore
}

// NewSequenceEvaluator creates a new sequence evaluator.
func NewSequenceEvaluator(logStore store.LogStore, alertStore store.AlertStore) *SequenceEvaluator {
	return &SequenceEvaluator{
		logStore:   logStore,
		alertStore: alertStore,
	}
}

// stepResult tracks the outcome of a single step in the sequence.
type stepResult struct {
	Label     string     `json:"label"`
	Matched   bool       `json:"matched"`
	MatchTime *time.Time `json:"match_time,omitempty"`
}

// Evaluate checks that all steps in the sequence match in chronological order.
func (se *SequenceEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if len(w.TypeConfig) == 0 {
		return nil, fmt.Errorf("sequence watcher %s has no type_config", w.ID)
	}

	var cfg SequenceConfig
	if err := json.Unmarshal(w.TypeConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing sequence config: %w", err)
	}

	if len(cfg.Steps) == 0 {
		return &EvalResult{
			HasAlert: false,
			Summary:  "OK: No steps defined",
		}, nil
	}

	window := ParseTimeRange(cfg.Window)
	windowStart := time.Now().Add(-window)

	var results []stepResult
	var lastMatchTime *time.Time
	allMatched := true

	for i, step := range cfg.Steps {
		label := step.Label
		if label == "" {
			label = fmt.Sprintf("step_%d", i+1)
		}

		// The step must find events after the previous step's match time
		afterTime := windowStart
		if lastMatchTime != nil {
			afterTime = *lastMatchTime
		}

		matched, matchTime, err := se.evaluateStep(ctx, step, afterTime, window)
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", label, err)
		}

		sr := stepResult{
			Label:     label,
			Matched:   matched,
			MatchTime: matchTime,
		}
		results = append(results, sr)

		if !matched {
			allMatched = false
			break // short-circuit
		}

		lastMatchTime = matchTime
	}

	var summary string
	if allMatched {
		var labels []string
		for _, r := range results {
			labels = append(labels, r.Label)
		}
		summary = fmt.Sprintf("SEQUENCE ALERT: All %d steps matched in order within %s (%s)",
			len(cfg.Steps), window, strings.Join(labels, " → "))
	} else {
		matchedCount := 0
		for _, r := range results {
			if r.Matched {
				matchedCount++
			}
		}
		summary = fmt.Sprintf("OK: %d/%d steps matched (sequence incomplete)", matchedCount, len(cfg.Steps))
	}

	return &EvalResult{
		HasAlert: allMatched,
		Summary:  summary,
		Details: map[string]any{
			"window": cfg.Window,
			"steps":  results,
		},
	}, nil
}

func (se *SequenceEvaluator) evaluateStep(ctx context.Context, step SequenceStep, afterTime time.Time, window time.Duration) (bool, *time.Time, error) {
	switch step.Source {
	case "logs":
		return se.evaluateLogStep(ctx, step, afterTime)
	case "alert":
		return se.evaluateAlertStep(ctx, step, afterTime, window)
	default:
		return false, nil, fmt.Errorf("unknown step source: %q", step.Source)
	}
}

func (se *SequenceEvaluator) evaluateLogStep(ctx context.Context, step SequenceStep, afterTime time.Time) (bool, *time.Time, error) {
	params := store.LogSearchParams{
		Start:   &afterTime,
		Limit:   1,
		SortAsc: true, // oldest first, to get the earliest match after afterTime
	}
	if step.Filter != nil {
		params.Service = step.Filter.Service
		params.Level = step.Filter.Level
		params.Query = step.Filter.Query
	}

	logs, err := se.logStore.Search(ctx, params)
	if err != nil {
		return false, nil, fmt.Errorf("searching logs: %w", err)
	}

	if len(logs) == 0 {
		return false, nil, nil
	}

	return true, &logs[0].Timestamp, nil
}

func (se *SequenceEvaluator) evaluateAlertStep(ctx context.Context, step SequenceStep, afterTime time.Time, window time.Duration) (bool, *time.Time, error) {
	if step.WatcherID == "" {
		return false, nil, fmt.Errorf("alert step requires watcher_id")
	}

	watcherID, err := uuid.Parse(step.WatcherID)
	if err != nil {
		return false, nil, fmt.Errorf("invalid watcher_id: %w", err)
	}

	alerts, err := se.alertStore.List(ctx, store.ListAlertParams{
		WatcherID: &watcherID,
		Limit:     10,
	})
	if err != nil {
		return false, nil, fmt.Errorf("listing alerts: %w", err)
	}

	// Find an alert created after afterTime and within window
	for _, a := range alerts {
		if a.CreatedAt.After(afterTime) {
			t := a.CreatedAt
			return true, &t, nil
		}
	}

	return false, nil, nil
}
