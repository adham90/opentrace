package watcher

import (
	"context"

	"github.com/adham90/opentrace/internal/store"
)

// EvalResult holds the output of an evaluation.
type EvalResult struct {
	HasAlert bool     `json:"has_alert"`
	Summary  string   `json:"summary"`
	Details  any      `json:"details,omitempty"`
	Value    *float64 `json:"value,omitempty"`
}

// Evaluator evaluates a watcher and returns whether an alert should fire.
type Evaluator interface {
	Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error)
}
