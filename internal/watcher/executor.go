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
	watcherStore  store.WatcherStore
	runStore      store.WatcherRunStore
	alertStore    store.AlertStore
	registry      *connector.Registry
	providerCache *llm.ProviderCache
	agentCfg      agent.RunConfig
	eventHub      *EventHub
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
		watcherStore:  watcherStore,
		runStore:      runStore,
		alertStore:    alertStore,
		registry:      registry,
		providerCache: providerCache,
		agentCfg:      agentCfg,
		eventHub:      eventHub,
	}
}

// Execute runs a single watcher: creates a run record, executes the agent,
// evaluates findings, creates alerts if needed, and sends notifications.
func (e *Executor) Execute(ctx context.Context, w store.Watcher) {
	// 1. Create a run record
	run, err := e.runStore.Create(ctx, w.ID)
	if err != nil {
		log.Printf("watcher %s: failed to create run: %v", w.ID, err)
		return
	}

	// Register with EventHub for live streaming
	if e.eventHub != nil {
		e.eventHub.Register(run.ID)
		defer e.eventHub.MarkDone(run.ID)
	}

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
		e.updateWatcherTiming(ctx, w)
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

	ag := agent.New(provider, runCfg)
	answer, err := ag.RunWithCallback(ctx, query, tools, callback, nil)
	if err != nil {
		errMsg := fmt.Sprintf("agent error: %v", err)
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.updateWatcherTiming(ctx, w)
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
			WatcherID: &w.ID,
			RunID:     &run.ID,
			Title:     w.Title,
			Summary:   answer,
			Severity:  w.Severity,
		})
		if err != nil {
			log.Printf("watcher %s: failed to create alert: %v", w.ID, err)
		} else {
			notifiers := ParseNotifiers(w.Notify)
			SendAll(ctx, notifiers, *alert)
		}
	}

	// 10. Update watcher timing
	e.updateWatcherTiming(ctx, w)
}

func (e *Executor) updateWatcherTiming(ctx context.Context, w store.Watcher) {
	now := time.Now()
	next := now.Add(ParseTimeRange(w.TimeRange))
	if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
		log.Printf("watcher %s: failed to update run time: %v", w.ID, err)
	}
}
