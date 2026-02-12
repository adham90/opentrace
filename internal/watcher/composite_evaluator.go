package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/adham90/opentrace/internal/store"
)

// CompositeEvaluator correlates multiple signals with boolean logic.
type CompositeEvaluator struct {
	watcherStore  store.WatcherStore
	runStore      store.WatcherRunStore
	ruleEvaluator *RuleEvaluator
}

// NewCompositeEvaluator creates a new composite evaluator.
func NewCompositeEvaluator(
	watcherStore store.WatcherStore,
	runStore store.WatcherRunStore,
	ruleEvaluator *RuleEvaluator,
) *CompositeEvaluator {
	return &CompositeEvaluator{
		watcherStore:  watcherStore,
		runStore:      runStore,
		ruleEvaluator: ruleEvaluator,
	}
}

// conditionResult tracks the outcome of a single condition.
type conditionResult struct {
	Label     string `json:"label"`
	Triggered bool   `json:"triggered"`
	Summary   string `json:"summary"`
}

// Evaluate checks conditions and applies boolean logic.
func (ce *CompositeEvaluator) Evaluate(ctx context.Context, w store.Watcher) (*EvalResult, error) {
	if len(w.TypeConfig) == 0 {
		return nil, fmt.Errorf("composite watcher %s has no type_config", w.ID)
	}

	var cfg CompositeConfig
	if err := json.Unmarshal(w.TypeConfig, &cfg); err != nil {
		return nil, fmt.Errorf("parsing composite config: %w", err)
	}

	if len(cfg.Conditions) == 0 {
		return nil, fmt.Errorf("composite watcher %s has no conditions", w.ID)
	}

	// Track visited watcher IDs for cycle detection
	visited := map[string]bool{w.ID.String(): true}
	var results []conditionResult

	for i, cond := range cfg.Conditions {
		label := cond.Label
		if label == "" {
			label = fmt.Sprintf("condition_%d", i+1)
		}

		triggered, summary, err := ce.evaluateCondition(ctx, cond, visited)
		if err != nil {
			return nil, fmt.Errorf("condition %q: %w", label, err)
		}

		results = append(results, conditionResult{
			Label:     label,
			Triggered: triggered,
			Summary:   summary,
		})
	}

	// Apply logic
	hasAlert, logicSummary := applyLogic(cfg.Logic, results)

	return &EvalResult{
		HasAlert: hasAlert,
		Summary:  logicSummary,
		Details: map[string]any{
			"logic":      cfg.Logic,
			"conditions": results,
		},
	}, nil
}

func (ce *CompositeEvaluator) evaluateCondition(ctx context.Context, cond CompositeCondition, visited map[string]bool) (bool, string, error) {
	if cond.WatcherID != "" {
		return ce.evaluateWatcherRef(ctx, cond.WatcherID, visited)
	}
	if len(cond.RuleConfig) > 0 {
		return ce.evaluateInlineRule(ctx, cond.RuleConfig)
	}
	return false, "", fmt.Errorf("condition has neither watcher_id nor rule_config")
}

func (ce *CompositeEvaluator) evaluateWatcherRef(ctx context.Context, watcherIDStr string, visited map[string]bool) (bool, string, error) {
	if visited[watcherIDStr] {
		return false, "", fmt.Errorf("circular reference detected: watcher %s", watcherIDStr)
	}
	visited[watcherIDStr] = true

	watcherID, err := uuid.Parse(watcherIDStr)
	if err != nil {
		return false, "", fmt.Errorf("invalid watcher_id: %w", err)
	}

	// Get the latest completed run for the referenced watcher
	runs, err := ce.runStore.List(ctx, watcherID, 5)
	if err != nil {
		return false, "", fmt.Errorf("listing runs for watcher %s: %w", watcherIDStr, err)
	}

	for _, r := range runs {
		if r.Status == "completed" {
			summary := ""
			if r.Summary != nil {
				summary = *r.Summary
			}
			return r.HasAlert, summary, nil
		}
	}

	return false, "no completed runs", nil
}

func (ce *CompositeEvaluator) evaluateInlineRule(ctx context.Context, ruleConfigRaw json.RawMessage) (bool, string, error) {
	if ce.ruleEvaluator == nil {
		return false, "", fmt.Errorf("rule evaluator not configured for inline rule")
	}

	var rc store.RuleConfig
	if err := json.Unmarshal(ruleConfigRaw, &rc); err != nil {
		return false, "", fmt.Errorf("parsing inline rule_config: %w", err)
	}

	// Create a temporary watcher for the rule evaluator
	tempWatcher := store.Watcher{
		ID:          uuid.New(),
		WatcherType: store.WatcherTypeRule,
		RuleConfig:  &rc,
	}

	result, err := ce.ruleEvaluator.Evaluate(ctx, tempWatcher)
	if err != nil {
		return false, "", fmt.Errorf("inline rule evaluation: %w", err)
	}

	return result.HasAlert, result.Summary, nil
}

func applyLogic(logic string, results []conditionResult) (bool, string) {
	triggeredCount := 0
	var triggeredLabels []string
	for _, r := range results {
		if r.Triggered {
			triggeredCount++
			triggeredLabels = append(triggeredLabels, r.Label)
		}
	}

	total := len(results)

	switch logic {
	case "all_of":
		if triggeredCount == total {
			return true, fmt.Sprintf("COMPOSITE ALERT: All %d conditions triggered (%s)",
				total, strings.Join(triggeredLabels, ", "))
		}
		return false, fmt.Sprintf("OK: %d/%d conditions triggered", triggeredCount, total)

	case "any_of":
		if triggeredCount > 0 {
			return true, fmt.Sprintf("COMPOSITE ALERT: %d/%d conditions triggered (%s)",
				triggeredCount, total, strings.Join(triggeredLabels, ", "))
		}
		return false, fmt.Sprintf("OK: 0/%d conditions triggered", total)

	case "none_of":
		if triggeredCount == 0 {
			return false, fmt.Sprintf("OK: 0/%d conditions triggered (none_of satisfied)", total)
		}
		return true, fmt.Sprintf("COMPOSITE ALERT: %d/%d conditions triggered, expected none (%s)",
			triggeredCount, total, strings.Join(triggeredLabels, ", "))

	default:
		return false, fmt.Sprintf("unknown logic: %s", logic)
	}
}
