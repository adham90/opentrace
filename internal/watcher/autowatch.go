package watcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// Default watch tuning. These are the numbers a founder would pick on their
// first day and never revisit: "tell me when errors spike" and "tell me when
// the service stops talking".
const (
	// defaultWatchDuration is effectively "until deleted". Watches expire by
	// design; a default that expires is a default that silently stops working.
	defaultWatchDuration = "8760h"

	// defaultErrorRate is the fraction of error/fatal logs that counts as a
	// spike. 5% of requests failing is not a normal Tuesday.
	defaultErrorRate = 0.05

	// defaultErrorRateInterval is both the check cadence and the measurement
	// window (the evaluator measures over check_interval).
	defaultErrorRateInterval = "5m"

	// defaultHeartbeatSilence is how long a service may say nothing before it
	// is treated as down, in seconds — the unit measureHeartbeat reports.
	defaultHeartbeatSilence = 900

	// defaultHeartbeatInterval must exceed defaultHeartbeatSilence: the
	// heartbeat metric is capped at the width of its measurement window, so a
	// window narrower than the threshold can never breach it.
	defaultHeartbeatInterval = "20m"

	// deployWatchDuration is how long a deploy stays under observation.
	deployWatchDuration = "24h"

	// deployWatchInterval is the post-deploy check cadence.
	deployWatchInterval = "5m"
)

// CreatedBy markers for watches this file creates. They distinguish "nobody
// asked for this" watches from the ones an agent or a human made, and they are
// how re-seeding stays idempotent across restarts.
const (
	CreatedByDefaultWatch = "auto:default"
	CreatedByDeployWatch  = "auto:deploy"
)

// deployReportDelays are when a deploy reports back on itself. One hour catches
// the fast regression while the change is still fresh in the agent's context;
// one day catches the slow leak that only shows up under a full traffic cycle.
var deployReportDelays = []time.Duration{time.Hour, 24 * time.Hour}

// DeployReport identifies one scheduled post-deploy check-in.
type DeployReport struct {
	WatchID     string `json:"watch_id"`
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Commit      string `json:"commit"`
	After       string `json:"after"` // "1h" / "24h" — also the comparison window
}

// AutoWatcher creates the watches nobody asked for.
//
// Two triggers, both on the ingest path: a (service, environment) reporting for
// the first time gets baseline coverage (error rate + heartbeat), and a commit
// hash never seen before gets a 24h post-deploy watch plus scheduled check-ins.
// Empty watches means silent production, and a founder who has to remember to
// arm monitoring is a founder who finds out from a customer.
type AutoWatcher struct {
	Watches store.WatchStore
	Logs    store.LogStore
	Groups  store.ErrorGroupStore
	Metrics *WatchMetrics

	// ScheduleReport queues a post-deploy check-in for a future time. Nil
	// disables the 1h/24h reports; the watch itself still runs.
	ScheduleReport func(ctx context.Context, at time.Time, r DeployReport) error

	// seen suppresses the store lookup after the first sighting of a key.
	//
	// ponytail: unbounded, but keyed by (service, env) and (service, env,
	//   commit) — bounded by deploy count, not traffic. Swap for an LRU if a
	//   process ever runs through enough deploys to notice.
	seen sync.Map
}

// Observe is called once per distinct (service, environment, commit) seen in an
// ingest batch. It is safe to call on every batch: the store checks behind it
// are idempotent and the process-local cache keeps them off the hot path.
func (a *AutoWatcher) Observe(ctx context.Context, service, environment, commit string) {
	if a == nil || a.Watches == nil || service == "" || environment == "" {
		return
	}
	a.ensureDefaults(ctx, service, environment)
	if commit != "" {
		a.ensureDeployWatch(ctx, service, environment, commit)
	}
}

// once runs fn the first time this process sees key.
func (a *AutoWatcher) once(key string, fn func()) {
	if _, loaded := a.seen.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	fn()
}

// ensureDefaults arms a service that has no watches at all. Any existing watch
// — even a triggered or expired one — means somebody has already decided what
// this service should be monitored for, and defaults stay out of the way.
func (a *AutoWatcher) ensureDefaults(ctx context.Context, service, env string) {
	a.once("default|"+service+"|"+env, func() {
		existing, err := a.Watches.List(ctx, store.ListWatchParams{
			Service:     service,
			Environment: env,
			Limit:       1,
		})
		if err != nil {
			slog.Warn("listing watches for defaults failed", "error", err, "service", service, "environment", env)
			return
		}
		if len(existing) > 0 {
			return
		}

		a.create(ctx, store.CreateWatchParams{
			ConditionsJSON: conditionJSON(&Condition{
				Type:    "threshold",
				Metric:  store.WatchMetricErrorRate,
				Op:      store.WatchOpGreaterThan,
				Value:   defaultErrorRate,
				Service: service,
			}),
			Service:       service,
			Environment:   env,
			Duration:      defaultWatchDuration,
			Urgency:       store.WatchUrgencyHigh,
			CheckInterval: defaultErrorRateInterval,
			// Two consecutive breaches: a single bad five-minute window on a
			// low-traffic service is one unlucky request, not an incident.
			MinConsecutive: 2,
			CreatedBy:      CreatedByDefaultWatch,
		})

		a.create(ctx, store.CreateWatchParams{
			ConditionsJSON: conditionJSON(&Condition{
				Type:    "threshold",
				Metric:  store.WatchMetricHeartbeat,
				Op:      store.WatchOpGreaterThan,
				Value:   defaultHeartbeatSilence,
				Service: service,
			}),
			Service:        service,
			Environment:    env,
			Duration:       defaultWatchDuration,
			Urgency:        store.WatchUrgencyCritical,
			CheckInterval:  defaultHeartbeatInterval,
			MinConsecutive: 1,
			CreatedBy:      CreatedByDefaultWatch,
		})
	})
}

// ensureDeployWatch puts a newly seen commit under observation for a day and
// schedules the check-ins that report back on it.
func (a *AutoWatcher) ensureDeployWatch(ctx context.Context, service, env, commit string) {
	a.once("deploy|"+service+"|"+env+"|"+commit, func() {
		existing, err := a.Watches.List(ctx, store.ListWatchParams{
			Service:     service,
			Environment: env,
			Limit:       maxDeployWatchScan,
		})
		if err != nil {
			slog.Warn("listing watches for deploy failed", "error", err, "service", service, "environment", env)
			return
		}
		for _, w := range existing {
			if w.CommitHash == commit && w.CreatedBy == CreatedByDeployWatch {
				return
			}
		}

		// Either an outright spike or a doubling of whatever this service
		// normally does. The absolute leg matters because a service whose
		// baseline error rate is 0 can never trip the relative one.
		w := a.create(ctx, store.CreateWatchParams{
			ConditionsJSON: conditionJSON(&Condition{Any: []*Condition{
				{
					Type:    "threshold",
					Metric:  store.WatchMetricErrorRate,
					Op:      store.WatchOpGreaterThan,
					Value:   defaultErrorRate,
					Service: service,
				},
				{
					Type:             "relative",
					Metric:           store.WatchMetricErrorRate,
					Op:               store.WatchOpGreaterThan,
					BaselineMultiple: 2,
					Service:          service,
				},
			}}),
			Service:        service,
			Environment:    env,
			CommitHash:     commit,
			Duration:       deployWatchDuration,
			Urgency:        store.WatchUrgencyHigh,
			CheckInterval:  deployWatchInterval,
			MinConsecutive: 2,
			CreatedBy:      CreatedByDeployWatch,
		})
		if w == nil || a.ScheduleReport == nil {
			return
		}

		now := time.Now().UTC()
		for _, d := range deployReportDelays {
			r := DeployReport{
				WatchID:     w.ID,
				Service:     service,
				Environment: env,
				Commit:      commit,
				After:       d.String(),
			}
			if err := a.ScheduleReport(ctx, now.Add(d), r); err != nil {
				slog.Warn("scheduling deploy report failed", "error", err, "commit", commit, "after", r.After)
			}
		}
	})
}

// maxDeployWatchScan caps the watch listing used to check whether a commit is
// already watched. A service with more live watches than this has plenty of
// coverage already; the worst case is one redundant deploy watch.
const maxDeployWatchScan = 200

func (a *AutoWatcher) create(ctx context.Context, p store.CreateWatchParams) *store.Watch {
	w, err := a.Watches.Create(ctx, p)
	if err != nil {
		slog.Warn("creating automatic watch failed", "error", err, "service", p.Service, "environment", p.Environment, "created_by", p.CreatedBy)
		return nil
	}

	// The baseline is what "relative" conditions and the deploy report compare
	// against, so capture it at creation — after the deploy has landed it is no
	// longer a baseline.
	if a.Logs != nil && a.Metrics != nil {
		if b, err := CaptureBaseline(ctx, a.Logs, a.Metrics, w); err == nil {
			if err := a.Watches.UpdateBaseline(ctx, w.ID, b); err == nil {
				w.BaselineJSON = b
			}
		}
	}

	slog.Info("created automatic watch",
		"watch_id", w.ID, "created_by", p.CreatedBy,
		"service", p.Service, "environment", p.Environment, "commit", p.CommitHash)
	return w
}

// conditionJSON marshals a condition tree. The trees here are literals built in
// this file, so a marshal error is a programming bug, not a runtime condition.
func conditionJSON(c *Condition) json.RawMessage {
	raw, err := json.Marshal(c)
	if err != nil {
		panic("watcher: marshalling built-in condition: " + err.Error())
	}
	return raw
}

// maxReportedNewErrors caps how many new error groups a deploy report names.
const maxReportedNewErrors = 5

// Report renders the post-deploy check-in for one scheduled report: how the
// deploy's own metrics moved against the baseline captured when it landed, and
// what broke that was not broken before.
func (a *AutoWatcher) Report(ctx context.Context, r DeployReport) (string, error) {
	if a == nil || a.Watches == nil || a.Metrics == nil {
		return "", fmt.Errorf("auto-watcher not configured")
	}
	w, err := a.Watches.GetByID(ctx, r.WatchID)
	if err != nil {
		// The watch is gone — deleted, or pruned with its data. There is
		// nothing to report and nothing to retry.
		return "", nil
	}

	window, err := time.ParseDuration(r.After)
	if err != nil || window <= 0 {
		return "", fmt.Errorf("invalid report window %q", r.After)
	}

	rate, rateErr := a.Metrics.Measure(ctx, store.WatchMetricErrorRate, r.Service, "", r.Environment, window)
	resp, respErr := a.Metrics.Measure(ctx, store.WatchMetricResponseTime, r.Service, "", r.Environment, window)

	short := r.Commit
	if len(short) > 7 {
		short = short[:7]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Deploy %s — %s after (%s / %s)\n", short, r.After, r.Service, r.Environment)

	if rateErr == nil {
		fmt.Fprintf(&b, "Error rate: %.2f%%%s\n", rate*100, deltaNote(rate, baselineErrorRate(w)))
	}
	if respErr == nil {
		fmt.Fprintf(&b, "Avg response: %.0fms%s\n", resp, deltaNote(resp, baselineResponse(w)))
	}

	newErrors := a.newErrorsSince(ctx, r.Environment, r.Service, w.CreatedAt)
	if len(newErrors) == 0 {
		b.WriteString("No new errors since the deploy.")
	} else {
		fmt.Fprintf(&b, "New errors since the deploy (%d):", len(newErrors))
		for _, line := range newErrors {
			b.WriteString("\n  • " + line)
		}
	}
	return b.String(), nil
}

// newErrorsSince lists error groups first seen after the deploy landed.
func (a *AutoWatcher) newErrorsSince(ctx context.Context, env, service string, since time.Time) []string {
	if a.Groups == nil {
		return nil
	}
	groups, err := a.Groups.List(ctx, store.ListErrorGroupParams{
		Status:      store.ErrorGroupUnresolved,
		Environment: env,
		Service:     service,
		Since:       &since,
		SortBy:      "occurrence_count",
		Limit:       maxReportedNewErrors,
	})
	if err != nil {
		return nil
	}
	lines := make([]string, 0, len(groups))
	for _, g := range groups {
		name := g.ExceptionClass
		if name == "" {
			name = "error"
		}
		lines = append(lines, fmt.Sprintf("%s: %s (%d)", name, truncate(g.Message, 80), g.OccurrenceCount))
	}
	return lines
}

func baselineErrorRate(w *store.Watch) float64 {
	if w.BaselineJSON == nil {
		return 0
	}
	return w.BaselineJSON.ErrorRate
}

func baselineResponse(w *store.Watch) float64 {
	if w.BaselineJSON == nil {
		return 0
	}
	return w.BaselineJSON.AvgResponseMs
}

// deltaNote renders the change against a baseline, or nothing when there is no
// baseline to compare against. A percentage off a zero baseline is infinity
// dressed up as a number.
func deltaNote(current, baseline float64) string {
	if baseline == 0 {
		return ""
	}
	return fmt.Sprintf(" (%+.0f%% vs baseline)", (current-baseline)/baseline*100)
}
