package digest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/adham90/opentrace/internal/store"
)

// DigestOpts configures digest generation.
type DigestOpts struct {
	PeriodStart time.Time
	PeriodEnd   time.Time
	Environment string // optional filter
	TopN        int    // max alerts to include (default 10)
}

// Builder generates digests from store data.
type Builder struct {
	alertStore   store.AlertStore
	watcherStore store.WatcherStore
	runStore     store.WatcherRunStore
}

// NewBuilder creates a new digest Builder.
func NewBuilder(as store.AlertStore, ws store.WatcherStore, rs store.WatcherRunStore) *Builder {
	return &Builder{
		alertStore:   as,
		watcherStore: ws,
		runStore:     rs,
	}
}

// Generate produces a Digest for the given time period.
func (b *Builder) Generate(ctx context.Context, opts DigestOpts) (*Digest, error) {
	if opts.TopN <= 0 {
		opts.TopN = 10
	}

	// 1. Build alert summary
	alertSummary, topAlerts, err := b.buildAlertData(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("building alert data: %w", err)
	}

	// 2. Build monitor summary and per-monitor health
	monitorSummary, monitorHealth, err := b.buildMonitorData(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("building monitor data: %w", err)
	}

	// 3. Build trends (compare with previous period of same duration)
	trends, err := b.buildTrends(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("building trends: %w", err)
	}

	status := DeriveStatus(alertSummary, monitorSummary)

	d := &Digest{
		ID:             uuid.New().String(),
		GeneratedAt:    time.Now().UTC(),
		PeriodStart:    opts.PeriodStart,
		PeriodEnd:      opts.PeriodEnd,
		Environment:    opts.Environment,
		Status:         status,
		AlertSummary:   alertSummary,
		MonitorSummary: monitorSummary,
		TopAlerts:      topAlerts,
		MonitorHealth:  monitorHealth,
		Trends:         trends,
	}

	return d, nil
}

func (b *Builder) buildAlertData(ctx context.Context, opts DigestOpts) (AlertSummary, []AlertItem, error) {
	// Count by severity in period
	severityCounts, err := b.alertStore.CountBySeverity(ctx, opts.PeriodStart, opts.PeriodEnd, opts.Environment)
	if err != nil {
		return AlertSummary{}, nil, err
	}

	total := 0
	for _, c := range severityCounts {
		total += c
	}

	unread, err := b.alertStore.CountUnread(ctx, opts.Environment)
	if err != nil {
		return AlertSummary{}, nil, err
	}

	summary := AlertSummary{
		Total:       total,
		Critical:    severityCounts["critical"],
		Warning:     severityCounts["warning"],
		Info:        severityCounts["info"],
		Unread:      unread,
		NewInPeriod: total,
	}

	// Get top alerts in period (sorted by severity then time)
	alerts, err := b.alertStore.List(ctx, store.ListAlertParams{
		Environment: opts.Environment,
		Limit:       opts.TopN,
	})
	if err != nil {
		return AlertSummary{}, nil, err
	}

	// Filter to period and sort by severity priority then time
	var inPeriod []store.Alert
	for _, a := range alerts {
		if !a.CreatedAt.Before(opts.PeriodStart) && a.CreatedAt.Before(opts.PeriodEnd) {
			inPeriod = append(inPeriod, a)
		}
	}

	sort.Slice(inPeriod, func(i, j int) bool {
		pi := severityPriority(inPeriod[i].Severity)
		pj := severityPriority(inPeriod[j].Severity)
		if pi != pj {
			return pi > pj
		}
		return inPeriod[i].CreatedAt.After(inPeriod[j].CreatedAt)
	})

	if len(inPeriod) > opts.TopN {
		inPeriod = inPeriod[:opts.TopN]
	}

	topAlerts := make([]AlertItem, 0, len(inPeriod))
	for _, a := range inPeriod {
		topAlerts = append(topAlerts, AlertItem{
			ID:           a.ID.String(),
			MonitorTitle: a.WatcherTitle,
			Severity:     string(a.Severity),
			Summary:      a.Summary,
			CreatedAt:    a.CreatedAt,
			Read:         a.Read,
		})
	}

	return summary, topAlerts, nil
}

func (b *Builder) buildMonitorData(ctx context.Context, opts DigestOpts) (MonitorSummary, []MonitorHealth, error) {
	watchers, err := b.watcherStore.List(ctx, store.ListWatcherParams{
		Environment: opts.Environment,
	})
	if err != nil {
		return MonitorSummary{}, nil, err
	}

	summary := MonitorSummary{Total: len(watchers)}

	health := make([]MonitorHealth, 0, len(watchers))
	for _, w := range watchers {
		switch w.Status {
		case store.WatcherActive:
			summary.Active++
		case store.WatcherPaused:
			summary.Paused++
		case store.WatcherError:
			summary.InError++
		}

		// Count runs for this watcher in period
		runCount, err := b.runStore.CountRuns(ctx, store.CountRunParams{
			Since:     opts.PeriodStart,
			Until:     opts.PeriodEnd,
			WatcherID: &w.ID,
		})
		if err != nil {
			return MonitorSummary{}, nil, err
		}

		failedCount, err := b.runStore.CountRuns(ctx, store.CountRunParams{
			Since:     opts.PeriodStart,
			Until:     opts.PeriodEnd,
			Status:    "error",
			WatcherID: &w.ID,
		})
		if err != nil {
			return MonitorSummary{}, nil, err
		}

		summary.RunsInPeriod += runCount
		summary.FailedRuns += failedCount

		// Count alerts for this watcher in period
		watcherAlerts, err := b.alertStore.List(ctx, store.ListAlertParams{
			WatcherID:   &w.ID,
			Environment: opts.Environment,
			Limit:       1000,
		})
		if err != nil {
			return MonitorSummary{}, nil, err
		}
		alertCount := 0
		for _, a := range watcherAlerts {
			if !a.CreatedAt.Before(opts.PeriodStart) && a.CreatedAt.Before(opts.PeriodEnd) {
				alertCount++
			}
		}

		// Determine last run status
		lastRunOK := true
		runs, err := b.runStore.List(ctx, w.ID, 1)
		if err != nil {
			return MonitorSummary{}, nil, err
		}
		if len(runs) > 0 && runs[0].Status == "error" {
			lastRunOK = false
		}

		h := MonitorHealth{
			ID:         w.ID.String(),
			Title:      w.Title,
			Status:     string(w.Status),
			LastRunAt:  w.LastRunAt,
			LastRunOK:  lastRunOK,
			RunCount:   runCount,
			AlertCount: alertCount,
		}
		health = append(health, h)
	}

	return summary, health, nil
}

func (b *Builder) buildTrends(ctx context.Context, opts DigestOpts) (*Trends, error) {
	duration := opts.PeriodEnd.Sub(opts.PeriodStart)
	prevStart := opts.PeriodStart.Add(-duration)
	prevEnd := opts.PeriodStart

	// Current period alert count (already computed but we count again for trends comparison)
	currentAlerts, err := b.alertStore.CountBySeverity(ctx, opts.PeriodStart, opts.PeriodEnd, opts.Environment)
	if err != nil {
		return nil, err
	}
	currentTotal := 0
	for _, c := range currentAlerts {
		currentTotal += c
	}

	// Previous period alert count
	prevAlerts, err := b.alertStore.CountBySeverity(ctx, prevStart, prevEnd, opts.Environment)
	if err != nil {
		return nil, err
	}
	prevTotal := 0
	for _, c := range prevAlerts {
		prevTotal += c
	}

	// Current period failed runs
	currentFailed, err := b.runStore.CountRuns(ctx, store.CountRunParams{
		Since:  opts.PeriodStart,
		Until:  opts.PeriodEnd,
		Status: "error",
	})
	if err != nil {
		return nil, err
	}

	// Previous period failed runs
	prevFailed, err := b.runStore.CountRuns(ctx, store.CountRunParams{
		Since:  prevStart,
		Until:  prevEnd,
		Status: "error",
	})
	if err != nil {
		return nil, err
	}

	return &Trends{
		AlertsPrevCount:    prevTotal,
		AlertsCurrentCount: currentTotal,
		FailedRunsPrev:     prevFailed,
		FailedRunsCurrent:  currentFailed,
	}, nil
}

func severityPriority(s store.WatcherSeverity) int {
	switch s {
	case store.SeverityCritical:
		return 3
	case store.SeverityWarning:
		return 2
	case store.SeverityInfo:
		return 1
	default:
		return 0
	}
}
