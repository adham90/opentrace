package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"

	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/store"
)

// DiffEvaluator detects changes in query results between runs.
type DiffEvaluator struct {
	registry *connector.Registry
	runStore store.WatcherRunStore
}

// NewDiffEvaluator creates a new diff evaluator.
func NewDiffEvaluator(registry *connector.Registry, runStore store.WatcherRunStore) *DiffEvaluator {
	return &DiffEvaluator{
		registry: registry,
		runStore: runStore,
	}
}

// diffSnapshot stores the state of a diff run for future comparison.
type diffSnapshot struct {
	Hash     string `json:"hash"`
	RowCount int    `json:"row_count"`
	Snapshot any    `json:"snapshot,omitempty"`
}

// Evaluate compares current query results against the previous run's snapshot.
func (de *DiffEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if len(w.TypeConfig) == 0 {
		return nil, fmt.Errorf("diff watcher %s has no type_config", w.ID)
	}

	var cfg DiffConfig
	if err := json.Unmarshal(w.TypeConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing diff config: %w", err)
	}

	// Execute query
	ds := de.registry.Get(connector.ConnectorDatabase)
	if ds == nil {
		return nil, fmt.Errorf("no database connector available")
	}

	qe, ok := ds.(connector.QueryExecutor)
	if !ok {
		return nil, fmt.Errorf("database connector does not support direct queries")
	}

	result, err := qe.ExecuteReadQuery(ctx, cfg.Query)
	if err != nil {
		return nil, fmt.Errorf("executing query: %w", err)
	}

	// Build current snapshot
	currentData, _ := json.Marshal(result.Rows)
	currentHash := fmt.Sprintf("%x", sha256.Sum256(currentData))
	currentRowCount := result.RowCount

	current := diffSnapshot{
		Hash:     currentHash,
		RowCount: currentRowCount,
		Snapshot: result.Rows,
	}

	// Get previous run for comparison
	prev, err := de.getPreviousSnapshot(ctx, w)
	if err != nil || prev == nil {
		// First run — establish baseline, no alert
		return &EvalResult{
			HasAlert: false,
			Summary:  fmt.Sprintf("Baseline established: %d rows (hash: %s)", currentRowCount, currentHash[:12]),
			Details:  current,
		}, nil
	}

	// Compare
	triggered := false
	var summary string

	switch cfg.CompareMode {
	case "hash":
		if currentHash != prev.Hash {
			triggered = true
			summary = fmt.Sprintf("DIFF ALERT: Query results changed (hash %s → %s, rows %d → %d)",
				prev.Hash[:12], currentHash[:12], prev.RowCount, currentRowCount)
		} else {
			summary = fmt.Sprintf("OK: Query results unchanged (%d rows, hash %s)", currentRowCount, currentHash[:12])
		}

	case "row_count":
		if prev.RowCount == 0 {
			if currentRowCount != 0 {
				triggered = true
				summary = fmt.Sprintf("DIFF ALERT: Row count changed from 0 to %d", currentRowCount)
			} else {
				summary = "OK: Row count unchanged (0 rows)"
			}
		} else {
			changePct := math.Abs(float64(currentRowCount-prev.RowCount)) / float64(prev.RowCount) * 100
			threshold := cfg.ChangeThreshold
			if threshold == 0 {
				threshold = 10 // default 10%
			}
			if changePct > threshold {
				triggered = true
				summary = fmt.Sprintf("DIFF ALERT: Row count changed by %.1f%% (%d → %d, threshold: %.0f%%)",
					changePct, prev.RowCount, currentRowCount, threshold)
			} else {
				summary = fmt.Sprintf("OK: Row count change %.1f%% within threshold (%.0f%%, %d → %d)",
					changePct, threshold, prev.RowCount, currentRowCount)
			}
		}

	case "exact":
		prevData, _ := json.Marshal(prev.Snapshot)
		if string(currentData) != string(prevData) {
			triggered = true
			summary = fmt.Sprintf("DIFF ALERT: Exact content changed (%d → %d rows)", prev.RowCount, currentRowCount)
		} else {
			summary = fmt.Sprintf("OK: Content identical (%d rows)", currentRowCount)
		}

	default:
		return nil, fmt.Errorf("unknown compare_mode: %q", cfg.CompareMode)
	}

	return &EvalResult{
		HasAlert: triggered,
		Summary:  summary,
		Details:  current,
	}, nil
}

// getPreviousSnapshot retrieves the most recent completed run's snapshot.
func (de *DiffEvaluator) getPreviousSnapshot(ctx context.Context, w store.Watcher) (*diffSnapshot, error) {
	runs, err := de.runStore.List(ctx, w.ID, 5)
	if err != nil {
		return nil, err
	}

	for _, r := range runs {
		if r.Status != "completed" || len(r.Details) == 0 {
			continue
		}
		var snap diffSnapshot
		if err := json.Unmarshal(r.Details, &snap); err != nil {
			continue
		}
		if snap.Hash != "" {
			return &snap, nil
		}
	}

	return nil, nil
}
