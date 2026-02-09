package watcher

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/opentrace/opentrace/internal/agent"
	"github.com/opentrace/opentrace/internal/connector"
	"github.com/opentrace/opentrace/internal/llm"
	"github.com/opentrace/opentrace/internal/store"
)

// Executor runs a single watcher evaluation.
type Executor struct {
	watcherStore store.WatcherStore
	runStore     store.WatcherRunStore
	alertStore   store.AlertStore
	registry     *connector.Registry
	llmProvider  llm.LLMProvider
	agentCfg     agent.RunConfig
}

// NewExecutor creates a new watcher executor.
func NewExecutor(
	watcherStore store.WatcherStore,
	runStore store.WatcherRunStore,
	alertStore store.AlertStore,
	registry *connector.Registry,
	llmProvider llm.LLMProvider,
	agentCfg agent.RunConfig,
) *Executor {
	return &Executor{
		watcherStore: watcherStore,
		runStore:     runStore,
		alertStore:   alertStore,
		registry:     registry,
		llmProvider:  llmProvider,
		agentCfg:     agentCfg,
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
	query := BuildQuery(w, lastRunSummary)

	// 5. Run the agent loop
	ag := agent.New(e.llmProvider, e.agentCfg)
	answer, err := ag.Run(ctx, query, tools)
	if err != nil {
		errMsg := fmt.Sprintf("agent error: %v", err)
		log.Printf("watcher %s (%s): %s", w.ID, w.Title, errMsg)
		e.runStore.Fail(ctx, run.ID, errMsg)
		e.updateWatcherTiming(ctx, w)
		return
	}

	// 6. Evaluate findings
	hasAlert := EvaluateFindings(answer)

	// 7. Complete the run
	if err := e.runStore.Complete(ctx, run.ID, answer, nil, hasAlert); err != nil {
		log.Printf("watcher %s: failed to complete run: %v", w.ID, err)
	}

	// 8. Create alert and notify if needed
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

	// 9. Update watcher timing
	e.updateWatcherTiming(ctx, w)
}

func (e *Executor) updateWatcherTiming(ctx context.Context, w store.Watcher) {
	now := time.Now()
	next := now.Add(ParseTimeRange(w.TimeRange))
	if err := e.watcherStore.UpdateRunTime(ctx, w.ID, now, next); err != nil {
		log.Printf("watcher %s: failed to update run time: %v", w.ID, err)
	}
}
