package watcher

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/adham90/opentrace/internal/agent"
	"github.com/adham90/opentrace/internal/connector"
	"github.com/adham90/opentrace/internal/llm"
	"github.com/adham90/opentrace/internal/store"
)

// Executor runs a single watcher evaluation.
type Executor struct {
	watcherStore    store.WatcherStore
	runStore        store.WatcherRunStore
	alertStore      store.AlertStore
	registry        *connector.Registry
	providerCache   *llm.ProviderCache
	agentCfg        agent.RunConfig
	eventHub        *EventHub
	ruleEvaluator   *RuleEvaluator
	adaptiveEngine  *AdaptiveEngine
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

// SetRuleEvaluator sets the rule evaluator for handling rule-based monitors.
func (e *Executor) SetRuleEvaluator(re *RuleEvaluator) {
	e.ruleEvaluator = re
}

// Execute runs a single watcher: creates a run record, evaluates (AI or rule),
// creates alerts if needed, and sends notifications.
func (e *Executor) Execute(ctx context.Context, w store.Watcher) {
	// 1. Create a run record
	run, err := e.runStore.Create(ctx, w.ID)
	if err != nil {
		log.Printf("watcher %s: failed to create run: %v", w.ID, err)
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

	// Dispatch by monitor type
	switch w.MonitorType {
	case store.MonitorTypeRule:
		e.executeRule(ctx, w, run)
	default: // "ai" or empty (backward compat)
		e.executeAI(ctx, w, run)
	}
}

// executeRule runs a rule-based evaluation (query, logs, or health).
func (e *Executor) executeRule(ctx context.Context, w store.Watcher, run *store.WatcherRun) {
	if e.ruleEvaluator == nil {
		errMsg := "rule evaluator not configured"
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	result, err := e.ruleEvaluator.Evaluate(ctx, w)
	if err != nil {
		errMsg := fmt.Sprintf("rule evaluation error: %v", err)
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
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
		log.Printf("watcher %s: failed to complete run: %v", w.ID, err)
	}

	// Create alert and notify if needed
	if result.HasAlert {
		alert, err := e.alertStore.Create(ctx, store.CreateAlertParams{
			WatcherID:   &w.ID,
			RunID:       &run.ID,
			Title:       w.Title,
			Summary:     result.Summary,
			Environment: w.Environment,
			Severity:    w.Severity,
		})
		if err != nil {
			log.Printf("watcher %s: failed to create alert: %v", w.ID, err)
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
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	// 6. Run the agent loop with effort-adjusted config and event callback
	runCfg := e.agentCfg
	effortCfg := EffortSettings(w.Effort)
	runCfg.MaxSteps = effortCfg.MaxSteps
	runCfg.MaxToolCalls = effortCfg.MaxToolCalls

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
			traceEvents = append(traceEvents, re)
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
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.finalizeRun(ctx, &w, RunOutcome{Errored: true})
		return
	}

	// 7. Evaluate findings
	hasAlert := EvaluateFindings(answer)

	// 8. Complete the run (store trace events as details)
	var details any
	if len(traceEvents) > 0 {
		details = traceEvents
	}
	if err := e.runStore.Complete(ctx, run.ID, answer, details, hasAlert); err != nil {
		log.Printf("watcher %s: failed to complete run: %v", w.ID, err)
	}

	// 9. Create alert and notify if needed
	if hasAlert {
		alert, err := e.alertStore.Create(ctx, store.CreateAlertParams{
			WatcherID:   &w.ID,
			RunID:       &run.ID,
			Title:       w.Title,
			Summary:     answer,
			Environment: w.Environment,
			Severity:    w.Severity,
		})
		if err != nil {
			log.Printf("watcher %s: failed to create alert: %v", w.ID, err)
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
			log.Printf("watcher %s: failed to update adaptive state: %v", w.ID, err)
		}

		if tr.ShouldPause {
			if _, err := e.watcherStore.UpdateStatus(ctx, w.ID, store.WatcherError); err != nil {
				log.Printf("watcher %s: failed to pause monitor: %v", w.ID, err)
			}
		}

		if tr.LogMessage != "" {
			log.Printf("watcher %s (%s): adaptive: %s", w.ID, w.Title, tr.LogMessage)
		}

		// Use adaptive interval for next_run_at if provided
		if tr.NewInterval != "" {
			sched, err := ParseSchedule(tr.NewInterval)
			if err == nil {
				next := sched.Next(now)
				if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
					log.Printf("watcher %s: failed to update run time: %v", w.ID, err)
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
		log.Printf("watcher %s: invalid schedule %q, using 15m fallback: %v", w.ID, schedExpr, err)
		if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, now.Add(15*time.Minute)); err != nil {
			log.Printf("watcher %s: failed to update run time: %v", w.ID, err)
		}
		return
	}

	next := sched.Next(now)
	if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
		log.Printf("watcher %s: failed to update run time: %v", w.ID, err)
	}
}
