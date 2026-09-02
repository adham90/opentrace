package watcher

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// recordingWatchStore captures what the auto-watcher creates and replays a
// fixed listing back to it.
type recordingWatchStore struct {
	store.WatchStore
	existing []store.Watch
	created  []store.CreateWatchParams
	listed   int
}

func (m *recordingWatchStore) GetByID(_ context.Context, id string) (*store.Watch, error) {
	for i := range m.existing {
		if m.existing[i].ID == id {
			return &m.existing[i], nil
		}
	}
	return nil, store.ErrNotFound
}

func (m *recordingWatchStore) List(_ context.Context, _ store.ListWatchParams) ([]store.Watch, error) {
	m.listed++
	return m.existing, nil
}

func (m *recordingWatchStore) Create(_ context.Context, p store.CreateWatchParams) (*store.Watch, error) {
	m.created = append(m.created, p)
	w := &store.Watch{
		ID:          "w" + string(rune('0'+len(m.created))),
		Service:     p.Service,
		Environment: p.Environment,
		CommitHash:  p.CommitHash,
		CreatedBy:   p.CreatedBy,
	}
	m.existing = append(m.existing, *w)
	return w, nil
}

// metricOf reports the metric named by a created watch's condition tree.
func metricOf(t *testing.T, raw json.RawMessage) store.WatchMetric {
	t.Helper()
	c, err := ParseCondition(raw)
	if err != nil {
		t.Fatalf("parsing generated condition: %v", err)
	}
	return c.Metric
}

// TestAutoWatcher_SeedsDefaultsOnce is the whole point of default watches:
// an unwatched service becomes a watched one the first time it reports, and
// stays at exactly two watches no matter how many batches arrive.
func TestAutoWatcher_SeedsDefaultsOnce(t *testing.T) {
	ws := &recordingWatchStore{}
	a := &AutoWatcher{Watches: ws}

	for range 3 {
		a.Observe(context.Background(), "api", "production", "")
	}

	if len(ws.created) != 2 {
		t.Fatalf("created %d watches, want 2", len(ws.created))
	}
	if ws.listed != 1 {
		t.Errorf("listed watches %d times, want 1 (the process cache should absorb the rest)", ws.listed)
	}

	got := map[store.WatchMetric]bool{}
	for _, p := range ws.created {
		if p.CreatedBy != CreatedByDefaultWatch {
			t.Errorf("created_by = %q, want %q", p.CreatedBy, CreatedByDefaultWatch)
		}
		if p.Service != "api" || p.Environment != "production" {
			t.Errorf("watch scoped to %q/%q, want api/production", p.Service, p.Environment)
		}
		got[metricOf(t, p.ConditionsJSON)] = true
	}
	if !got[store.WatchMetricErrorRate] || !got[store.WatchMetricHeartbeat] {
		t.Errorf("default metrics = %v, want error_rate and heartbeat", got)
	}
}

// TestAutoWatcher_HeartbeatWindowExceedsThreshold pins the one number that
// silently breaks this feature: measureHeartbeat is capped at the width of its
// measurement window (the check interval), so a window narrower than the
// silence threshold produces a watch that can never fire.
func TestAutoWatcher_HeartbeatWindowExceedsThreshold(t *testing.T) {
	interval, err := time.ParseDuration(defaultHeartbeatInterval)
	if err != nil {
		t.Fatalf("parsing default heartbeat interval: %v", err)
	}
	if interval.Seconds() <= defaultHeartbeatSilence {
		t.Errorf("heartbeat check interval %s <= threshold %ds; the watch can never breach",
			interval, defaultHeartbeatSilence)
	}
}

// TestAutoWatcher_ExistingWatchesAreLeftAlone: somebody already decided what
// this service should be monitored for. Defaults stay out of the way.
func TestAutoWatcher_ExistingWatchesAreLeftAlone(t *testing.T) {
	ws := &recordingWatchStore{existing: []store.Watch{{ID: "mine", Service: "api"}}}
	a := &AutoWatcher{Watches: ws}

	a.Observe(context.Background(), "api", "production", "")

	if len(ws.created) != 0 {
		t.Fatalf("created %d watches over an existing one, want 0", len(ws.created))
	}
}

// TestAutoWatcher_DeployWatchSchedulesReports covers the "self-monitoring its
// own changes" promise: a new commit gets a watch and the 1h/24h check-ins.
func TestAutoWatcher_DeployWatchSchedulesReports(t *testing.T) {
	ws := &recordingWatchStore{existing: []store.Watch{{ID: "mine", Service: "api"}}}

	var scheduled []DeployReport
	var at []time.Time
	a := &AutoWatcher{
		Watches: ws,
		ScheduleReport: func(_ context.Context, when time.Time, r DeployReport) error {
			scheduled = append(scheduled, r)
			at = append(at, when)
			return nil
		},
	}

	start := time.Now().UTC()
	a.Observe(context.Background(), "api", "production", "abc1234def")
	a.Observe(context.Background(), "api", "production", "abc1234def") // repeat batch

	if len(ws.created) != 1 {
		t.Fatalf("created %d deploy watches, want 1", len(ws.created))
	}
	w := ws.created[0]
	if w.CreatedBy != CreatedByDeployWatch || w.CommitHash != "abc1234def" {
		t.Errorf("deploy watch = %+v, want created_by=%s commit=abc1234def", w, CreatedByDeployWatch)
	}
	if w.Duration != deployWatchDuration {
		t.Errorf("duration = %q, want %q", w.Duration, deployWatchDuration)
	}

	if len(scheduled) != 2 {
		t.Fatalf("scheduled %d reports, want 2", len(scheduled))
	}
	for i, want := range deployReportDelays {
		if scheduled[i].After != want.String() {
			t.Errorf("report %d after = %q, want %q", i, scheduled[i].After, want)
		}
		if delta := at[i].Sub(start); delta < want || delta > want+time.Minute {
			t.Errorf("report %d scheduled at +%s, want ~+%s", i, delta, want)
		}
		if scheduled[i].WatchID == "" {
			t.Errorf("report %d has no watch to compare against", i)
		}
	}
}

// TestAutoWatcher_ReportSkipsDeletedWatch: a report whose watch is gone must
// not fail the job forever — there is nothing left to compare against.
func TestAutoWatcher_ReportSkipsDeletedWatch(t *testing.T) {
	a := &AutoWatcher{Watches: &recordingWatchStore{}, Metrics: NewWatchMetrics(nil)}

	text, err := a.Report(context.Background(), DeployReport{WatchID: "gone", After: "1h"})
	if err != nil {
		t.Fatalf("Report on a deleted watch returned %v, want nil", err)
	}
	if text != "" {
		t.Errorf("Report on a deleted watch returned %q, want empty", text)
	}
}

func TestDeltaNote(t *testing.T) {
	if got := deltaNote(0.10, 0); got != "" {
		t.Errorf("deltaNote with a zero baseline = %q, want empty", got)
	}
	if got := deltaNote(0.10, 0.05); !strings.Contains(got, "+100%") {
		t.Errorf("deltaNote(0.10, 0.05) = %q, want a +100%% delta", got)
	}
}
