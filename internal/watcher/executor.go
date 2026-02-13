package watcher

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// Executor runs a single watcher evaluation.
type Executor struct {
	watcherStore      store.WatcherStore
	runStore          store.WatcherRunStore
	alertStore        store.AlertStore
	registry          *connector.Registry
	providerCache     *llm.ProviderCache
	agentCfg          agent.RunConfig
	eventHub          *EventHub
	ruleEvaluator     *RuleEvaluator
	deadmanEvaluator  Evaluator
	diffEvaluator     Evaluator
	compositeEvaluator Evaluator
	trendEvaluator    Evaluator
	sequenceEvaluator Evaluator
	adaptiveEngine    *AdaptiveEngine
}

// NewExecutor creates a new watcher executor.
func NewExecutor(
	watcherStore store.WatcherStore,
	runStore store.WatcherRunStore,
	alertStore store.AlertStore,
	registry *connector.Registry,
	providerCache *llm.ProviderCache,
	agentCfg agent.RunConfig,
	eventHub *EventHub,
) *Executor {
	return &Executor{
		watcherStore:   watcherStore,
		runStore:       runStore,
		alertStore:     alertStore,
		registry:       registry,
		providerCache:  providerCache,
		agentCfg:       agentCfg,
		eventHub:       eventHub,
		adaptiveEngine: &AdaptiveEngine{},
	}
}

// SetRuleEvaluator sets the rule evaluator for handling rule-based watchers.
func (e *Executor) SetRuleEvaluator(re *RuleEvaluator) {
	e.ruleEvaluator = re
}

// SetDeadmanEvaluator sets the evaluator for deadman watchers.
func (e *Executor) SetDeadmanEvaluator(ev Evaluator) { e.deadmanEvaluator = ev }

// SetDiffEvaluator sets the evaluator for diff watchers.
func (e *Executor) SetDiffEvaluator(ev Evaluator) { e.diffEvaluator = ev }

// SetCompositeEvaluator sets the evaluator for composite watchers.
func (e *Executor) SetCompositeEvaluator(ev Evaluator) { e.compositeEvaluator = ev }

// SetTrendEvaluator sets the evaluator for trend watchers.
func (e *Executor) SetTrendEvaluator(ev Evaluator) { e.trendEvaluator = ev }

// SetSequenceEvaluator sets the evaluator for sequence watchers.
func (e *Executor) SetSequenceEvaluator(ev Evaluator) { e.sequenceEvaluator = ev }

// Execute runs a single watcher: creates a run record, evaluates (AI or rule),
// creates alerts if needed, and sends notifications.
func (e *Executor) Execute(ctx context.Context, w store.Watcher) {
	// 1. Create a run record
	run, err := e.runStore.Create(ctx, w.ID)
	if err != nil {
		slog.Error("failed to create run", "watcher_id", w.ID, "error", err)
		return
	}

	// Register with EventHub for live streaming and cancellation
	if e.eventHub != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		e.eventHub.Register(run.ID, cancel)
		defer e.eventHub.MarkDone(run.ID)
	}

	// Dispatch by watcher type
	switch w.WatcherType {
	case store.WatcherTypeRule:
		e.executeRule(ctx, w, run)
	case store.WatcherTypeDeadman:
		e.executeTyped(ctx, w, run, e.deadmanEvaluator, "deadman")
	case store.WatcherTypeDiff:
		e.executeTyped(ctx, w, run, e.diffEvaluator, "diff")
	case store.WatcherTypeComposite:
		e.executeTyped(ctx, w, run, e.compositeEvaluator, "composite")
	case store.WatcherTypeTrend:
		e.executeTyped(ctx, w, run, e.trendEvaluator, "trend")
	case store.WatcherTypeSequence:
		e.executeTyped(ctx, w, run, e.sequenceEvaluator, "sequence")
	default: // "ai" or empty (backward compat)
		e.executeAI(ctx, w, run)
	}
}

// executeRule runs a rule-based evaluation (query, logs, or health).
func (e *Executor) executeRule(ctx context.Context, w store.Watcher, run *store.WatcherRun) {
	if e.ruleEvaluator == nil {
		errMsg := "rule evaluator not configured"
		slog.Error(errMsg, "watcher_id", w.ID, "watcher_title", w.Title)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	result, err := e.ruleEvaluator.Evaluate(ctx, w)
	if err != nil {
		errMsg := fmt.Sprintf("rule evaluation error: %v", err)
		slog.Error("rule evaluation error", "watcher_id", w.ID, "watcher_title", w.Title, "error", err)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	// Publish evaluation result to EventHub
	if e.eventHub != nil {
		e.eventHub.Publish(run.ID, RunEvent{
			Type:    "rule_result",
			Content: result.Summary,
			Time:    time.Now(),
		})
	}

	// Complete the run
	if err := e.runStore.Complete(ctx, run.ID, result.Summary, result.Details, result.HasAlert); err != nil {
		slog.Error("failed to complete run", "watcher_id", w.ID, "error", err)
	}

	// Create alert and notify if needed
	if result.HasAlert {
		alert, err := e.alertStore.Create(ctx, store.CreateAlertParams{
			WatcherID: &w.ID,
			RunID:     &run.ID,
			Title:     w.Title,
			Summary:   result.Summary,
			Severity:  w.Severity,
		})
		if err != nil {
			slog.Error("failed to create alert", "watcher_id", w.ID, "error", err)
		} else {
			notifiers := ParseNotifiers(w.Notify)
			SendAll(ctx, notifiers, *alert)
		}
	}

	e.finalizeRun(ctx, &w, RunOutcome{Alerted: result.HasAlert})
}

// executeTyped runs a typed evaluator (deadman, diff, composite, trend, sequence).
func (e *Executor) executeTyped(ctx context.Context, w store.Watcher, run *store.WatcherRun, eval Evaluator, typeName string) {
	if eval == nil {
		errMsg := fmt.Sprintf("%s evaluator not configured", typeName)
		slog.Error(errMsg, "watcher_id", w.ID, "watcher_title", w.Title)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	result, err := eval.Evaluate(ctx, w)
	if err != nil {
		errMsg := fmt.Sprintf("%s evaluation error: %v", typeName, err)
		slog.Error(typeName+" evaluation error", "watcher_id", w.ID, "watcher_title", w.Title, "error", err)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	if e.eventHub != nil {
		e.eventHub.Publish(run.ID, RunEvent{
			Type:    typeName + "_result",
			Content: result.Summary,
			Time:    time.Now(),
		})
	}

	if err := e.runStore.Complete(ctx, run.ID, result.Summary, result.Details, result.HasAlert); err != nil {
		slog.Error("failed to complete run", "watcher_id", w.ID, "error", err)
	}

	if result.HasAlert {
		alert, err := e.alertStore.Create(ctx, store.CreateAlertParams{
			WatcherID: &w.ID,
			RunID:     &run.ID,
			Title:     w.Title,
			Summary:   result.Summary,
			Severity:  w.Severity,
		})
		if err != nil {
			slog.Error("failed to create alert", "watcher_id", w.ID, "error", err)
		} else {
			notifiers := ParseNotifiers(w.Notify)
			SendAll(ctx, notifiers, *alert)
		}
	}

	e.finalizeRun(ctx, &w, RunOutcome{Alerted: result.HasAlert})
}

// executeAI runs the existing AI agent-based evaluation.
func (e *Executor) executeAI(ctx context.Context, w store.Watcher, run *store.WatcherRun) {
	// 2. Get tools from registry
	tools := e.registry.AllTools()

	// 3. Fetch previous run summary for context
	var lastRunSummary string
	runs, err := e.runStore.List(ctx, w.ID, 2) // get latest 2 (current one + previous)
	if err == nil {
		for _, r := range runs {
			if r.ID != run.ID && r.Summary != nil {
				lastRunSummary = *r.Summary
				break
			}
		}
	}

	// 4. Build the query from watcher config
	hasServers := e.registry.Get(connector.ConnectorServerMetrics) != nil
	query := BuildQuery(w, lastRunSummary, hasServers)

	// 5. Resolve per-watcher LLM provider
	provider, err := e.providerCache.Get(w.Model)
	if err != nil {
		errMsg := fmt.Sprintf("model resolution error: %v", err)
		slog.Error("model resolution error", "watcher_id", w.ID, "watcher_title", w.Title, "error", err)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	// 6. Run the agent loop with effort-adjusted config and event callback
	runCfg := e.agentCfg
	effortCfg := EffortSettings(w.Effort)
	runCfg.MaxSteps = effortCfg.MaxSteps
	runCfg.MaxToolCalls = effortCfg.MaxToolCalls

	var mu sync.Mutex
	var traceEvents []RunEvent
	var callback agent.EventCallback
	if e.eventHub != nil {
		callback = func(ev agent.Event) {
			re := RunEvent{
				Type:     ev.Type,
				Content:  ev.Content,
				ToolName: ev.ToolName,
				Args:     ev.Args,
				Time:     time.Now(),
			}
			mu.Lock()
			traceEvents = append(traceEvents, re)
			mu.Unlock()
			e.eventHub.Publish(run.ID, re)
		}
	}

	// Apply a 5-minute timeout to the AI agent execution to prevent runaway LLM calls
	execCtx, execCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer execCancel()

	ag := agent.New(provider, runCfg)
	answer, err := ag.RunWithCallback(execCtx, query, tools, callback, nil)
	if err != nil {
		errMsg := fmt.Sprintf("agent error: %v", err)
		slog.Error("agent error", "watcher_id", w.ID, "watcher_title", w.Title, "error", err)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	// 7. Evaluate findings
	hasAlert := EvaluateFindings(answer)

	// 8. Complete the run (store trace events as details)
	mu.Lock()
	var details any
	if len(traceEvents) > 0 {
		details = traceEvents
	}
	mu.Unlock()
	if err := e.runStore.Complete(ctx, run.ID, answer, details, hasAlert); err != nil {
		slog.Error("failed to complete run", "watcher_id", w.ID, "error", err)
	}

	// 9. Create alert and notify if needed
	if hasAlert {
		alert, err := e.alertStore.Create(ctx, store.CreateAlertParams{
			WatcherID: &w.ID,
			RunID:     &run.ID,
			Title:     w.Title,
			Summary:   answer,
			Severity:  w.Severity,
		})
		if err != nil {
			slog.Error("failed to create alert", "watcher_id", w.ID, "error", err)
		} else {
			notifiers := ParseNotifiers(w.Notify)
			SendAll(ctx, notifiers, *alert)
		}
	}

	// 10. Finalize: apply adaptive scheduling and update timing
	e.finalizeRun(ctx, &w, RunOutcome{Alerted: hasAlert})
}

// finalizeRun applies adaptive scheduling transitions and updates watcher timing.
func (e *Executor) finalizeRun(ctx context.Context, w *store.Watcher, outcome RunOutcome) {
	now := time.Now()

	// Apply adaptive scheduling if enabled
	if w.AdaptiveConfig != nil && w.AdaptiveConfig.Enabled {
		tr := e.adaptiveEngine.Transition(w, outcome, now)

		if err := e.watcherStore.UpdateAdaptiveState(ctx, w.ID, store.UpdateAdaptiveParams{
			AdaptiveState:        tr.NewState,
			ConsecutiveCleanRuns: tr.CleanRuns,
			ConsecutiveErrors:    tr.ErrorCount,
			EscalatedAt:          tr.EscalatedAt,
			TimeRange:            tr.NewTimeRange,
			BaseTimeRange:        tr.BaseTimeRange,
		}); err != nil {
			slog.Error("failed to update adaptive state", "watcher_id", w.ID, "error", err)
		}

		if tr.ShouldPause {
			if _, err := e.watcherStore.UpdateStatus(ctx, w.ID, store.WatcherError); err != nil {
				slog.Error("failed to pause watcher", "watcher_id", w.ID, "error", err)
			}
		}

		if tr.LogMessage != "" {
			slog.Info("adaptive scheduling", "watcher_id", w.ID, "watcher_title", w.Title, "message", tr.LogMessage)
		}

		// Use adaptive interval for next_run_at if provided
		if tr.NewInterval != "" {
			sched, err := ParseSchedule(tr.NewInterval)
			if err == nil {
				next := sched.Next(now)
				if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
					slog.Error("failed to update run time", "watcher_id", w.ID, "error", err)
				}
				return
			}
		}
	}

	// Default timing: prefer explicit schedule, fall back to time_range
	schedExpr := w.Schedule
	if schedExpr == "" {
		schedExpr = w.TimeRange
	}

	sched, err := ParseSchedule(schedExpr)
	if err != nil {
		slog.Warn("invalid schedule, using 15m fallback", "watcher_id", w.ID, "schedule", schedExpr, "error", err)
		if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, now.Add(15*time.Minute)); err != nil {
			slog.Error("failed to update run time", "watcher_id", w.ID, "error", err)
		}
		return
	}

	next := sched.Next(now)
	if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
		slog.Error("failed to update run time", "watcher_id", w.ID, "error", err)
	}
}
