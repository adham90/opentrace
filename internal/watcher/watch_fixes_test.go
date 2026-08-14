package watcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Test doubles
// ---------------------------------------------------------------------------

// rangeLogStore answers aggregation queries from a caller-supplied function of
// the queried time range, and records every params struct it was handed. It is
// the only way to prove which window / environment / level filter the watcher
// actually asked for.
type rangeLogStore struct {
	store.LogStore

	mu sync.Mutex

	countsFor   func(p store.LogCountParams) map[string]int
	distinctFor func(field string, p store.LogCountParams) []string
	err         error

	countCalls    []store.LogCountParams
	distinctCalls []store.LogCountParams
	distinctField []string
	searchCalls   []store.LogSearchParams
	summaryCalls  []store.RequestSummarySearchParams
}

func (m *rangeLogStore) CountByLevel(_ context.Context, p store.LogCountParams) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.countCalls = append(m.countCalls, p)
	if m.err != nil {
		return nil, m.err
	}
	counts := map[string]int{}
	if m.countsFor != nil {
		counts = m.countsFor(p)
	}
	// Mirror the adapter's level filter so tests exercise the real contract.
	if p.Level != "" {
		filtered := map[string]int{}
		for level, n := range counts {
			if level == p.Level {
				filtered[level] = n
			}
		}
		return filtered, nil
	}
	return counts, nil
}

func (m *rangeLogStore) DistinctValues(_ context.Context, field string, p store.LogCountParams) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.distinctCalls = append(m.distinctCalls, p)
	m.distinctField = append(m.distinctField, field)
	if m.err != nil {
		return nil, m.err
	}
	if m.distinctFor != nil {
		return m.distinctFor(field, p), nil
	}
	return nil, nil
}

func (m *rangeLogStore) Search(_ context.Context, p store.LogSearchParams) ([]store.LogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.searchCalls = append(m.searchCalls, p)
	return nil, m.err
}

func (m *rangeLogStore) SearchRequestSummaries(_ context.Context, p store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summaryCalls = append(m.summaryCalls, p)
	return nil, m.err
}

func (m *rangeLogStore) AggregateRequestSummaries(_ context.Context, _ store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, m.err
}

func (m *rangeLogStore) calls() []store.LogCountParams {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.LogCountParams, len(m.countCalls))
	copy(out, m.countCalls)
	return out
}

// isCurrentWindow reports whether a queried range ends at (approximately) now,
// i.e. it is the current measurement window rather than a shifted-back one.
func isCurrentWindow(p store.LogCountParams) bool {
	return time.Since(p.Until) < 5*time.Second
}

// ---------------------------------------------------------------------------
// Issue 1 — delta math
// ---------------------------------------------------------------------------

// TestEvalDelta_Math pins the corrected delta arithmetic. The old code
// approximated "previous" with a wide window that contained the current one,
// which bounded rate deltas inside ±100% (making change_pct >= 100
// unsatisfiable) and compared counts across windows of unequal length.
func TestEvalDelta_Math(t *testing.T) {
	const checkWindow = time.Minute

	tests := []struct {
		name          string
		metric        store.WatchMetric
		compareWindow string
		changePct     float64
		current       map[string]int
		previous      map[string]int
		wantBreached  bool
		wantDeltaPct  float64
		deltaTol      float64
	}{
		{
			// The headline case: error_rate 0.001 -> 1.0 is a 99,900% jump and
			// must clear a 100% threshold. The old math could never reach 100.
			name:      "error_rate 0.001 to 1.0 exceeds a 100% threshold",
			metric:    store.WatchMetricErrorRate,
			changePct: 100,
			// Equal traffic in both windows: under the old wide-window
			// approximation this reads as 99.8% and misses the 100% threshold.
			previous:     map[string]int{"error": 1, "info": 999},
			current:      map[string]int{"error": 1000},
			wantBreached: true,
			wantDeltaPct: 99900,
			deltaTol:     1,
		},
		{
			name:         "error_rate quadrupling reads as +300%, not ~66%",
			metric:       store.WatchMetricErrorRate,
			changePct:    100,
			previous:     map[string]int{"error": 10, "info": 90}, // 0.10
			current:      map[string]int{"error": 40, "info": 60}, // 0.40
			wantBreached: true,
			wantDeltaPct: 300,
			deltaTol:     0.001,
		},
		{
			// Steady traffic with a 24h compare window used to read as a
			// constant -95.8% change and breach forever.
			name:          "steady log_count with a 24h compare window is 0%",
			metric:        store.WatchMetricLogCount,
			compareWindow: "24h",
			changePct:     50,
			previous:      map[string]int{"info": 86400}, // 24h at 1/s
			current:       map[string]int{"info": 60},    // 1m at 1/s
			wantBreached:  false,
			wantDeltaPct:  0,
			deltaTol:      0.001,
		},
		{
			name:         "log_count doubling breaches a 50% threshold",
			metric:       store.WatchMetricLogCount,
			changePct:    50,
			previous:     map[string]int{"info": 60},
			current:      map[string]int{"info": 120},
			wantBreached: true,
			wantDeltaPct: 100,
			deltaTol:     0.001,
		},
		{
			name:         "log_count halving breaches on absolute change",
			metric:       store.WatchMetricLogCount,
			changePct:    40,
			previous:     map[string]int{"info": 100},
			current:      map[string]int{"info": 40},
			wantBreached: true,
			wantDeltaPct: -60,
			deltaTol:     0.001,
		},
		{
			name:         "zero previous cannot compute a delta",
			metric:       store.WatchMetricLogCount,
			changePct:    10,
			previous:     map[string]int{},
			current:      map[string]int{"info": 5},
			wantBreached: false,
			wantDeltaPct: 5, // reports the current value
			deltaTol:     0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &rangeLogStore{countsFor: func(p store.LogCountParams) map[string]int {
				if isCurrentWindow(p) {
					return tt.current
				}
				return tt.previous
			}}
			c := &Condition{
				Type:          "delta",
				Metric:        tt.metric,
				Op:            store.WatchOpGreaterThan,
				ChangePct:     tt.changePct,
				CompareWindow: tt.compareWindow,
			}

			got, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", checkWindow)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got.Breached != tt.wantBreached {
				t.Errorf("breached = %v, want %v (%s)", got.Breached, tt.wantBreached, got.Summary)
			}
			if math.Abs(got.Value-tt.wantDeltaPct) > tt.deltaTol {
				t.Errorf("delta = %v, want %v (±%v)", got.Value, tt.wantDeltaPct, tt.deltaTol)
			}
		})
	}
}

// TestEvalDelta_PreviousWindowDoesNotOverlapCurrent proves the previous window
// is measured on its own range instead of a wide window containing the current
// one.
func TestEvalDelta_PreviousWindowDoesNotOverlapCurrent(t *testing.T) {
	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return map[string]int{"info": 1} }}
	c := &Condition{Type: "delta", Metric: store.WatchMetricLogCount, Op: store.WatchOpGreaterThan, ChangePct: 10}

	if _, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", time.Minute); err != nil {
		t.Fatalf("eval: %v", err)
	}
	calls := ls.calls()
	if len(calls) != 2 {
		t.Fatalf("got %d count queries, want 2", len(calls))
	}
	cur, prev := calls[0], calls[1]
	if prev.Until.After(cur.Since) {
		t.Errorf("previous window [%s,%s) overlaps current [%s,%s)", prev.Since, prev.Until, cur.Since, cur.Until)
	}
	if d := prev.Until.Sub(prev.Since); math.Abs(d.Seconds()-60) > 1 {
		t.Errorf("previous window length = %s, want ~1m", d)
	}
}

// ---------------------------------------------------------------------------
// Issue 2 — count condition query filter
// ---------------------------------------------------------------------------

// TestEvalCount_HonoursQueryFilter pins the documented `query` filter. A
// healthy service with 5000 info logs and 3 errors must NOT breach
// "count(level:error) > 100" — the old code counted every level.
func TestEvalCount_HonoursQueryFilter(t *testing.T) {
	levels := map[string]int{"info": 5000, "error": 3}

	tests := []struct {
		name         string
		query        string
		want         float64
		wantBreached bool
		wantLevel    string
	}{
		{"level:error counts only errors", "level:error", 3, false, "error"},
		{"no query counts everything", "", 5003, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return levels }}
			c := &Condition{
				Type:   "count",
				Query:  tt.query,
				Op:     store.WatchOpGreaterThan,
				Value:  100,
				Window: "1h",
			}
			got, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", time.Minute)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got.Value != tt.want {
				t.Errorf("count = %v, want %v", got.Value, tt.want)
			}
			if got.Breached != tt.wantBreached {
				t.Errorf("breached = %v, want %v", got.Breached, tt.wantBreached)
			}
			calls := ls.calls()
			if len(calls) != 1 {
				t.Fatalf("got %d count queries, want 1", len(calls))
			}
			if calls[0].Level != tt.wantLevel {
				t.Errorf("level filter = %q, want %q", calls[0].Level, tt.wantLevel)
			}
			if calls[0].Environment != "prod" {
				t.Errorf("environment = %q, want prod", calls[0].Environment)
			}
		})
	}
}

// TestEvalCount_DistinctPassesFilters proves the distinct branch scopes its
// query too (service, environment and the parsed level filter).
func TestEvalCount_DistinctPassesFilters(t *testing.T) {
	ls := &rangeLogStore{distinctFor: func(_ string, _ store.LogCountParams) []string {
		return []string{"a", "b", "c"}
	}}
	c := &Condition{
		Type:     "count",
		Query:    "level:error",
		Field:    "error_fingerprint",
		Distinct: true,
		Service:  "api",
		Op:       store.WatchOpGreaterThan,
		Value:    2,
	}
	got, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), nil, "prod", time.Minute)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if !got.Breached || got.Value != 3 {
		t.Errorf("got (%v, %v), want (true, 3)", got.Breached, got.Value)
	}
	if len(ls.distinctCalls) != 1 {
		t.Fatalf("got %d distinct queries, want 1", len(ls.distinctCalls))
	}
	p := ls.distinctCalls[0]
	if p.Environment != "prod" || p.Service != "api" || p.Level != "error" {
		t.Errorf("distinct params = %+v, want env=prod service=api level=error", p)
	}
	if ls.distinctField[0] != "error_fingerprint" {
		t.Errorf("field = %q, want error_fingerprint", ls.distinctField[0])
	}
}

func TestParseCountQuery_RejectsInexpressibleFilters(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		condService string
		wantErr     bool
		want        countQueryFilter
	}{
		{name: "empty", query: "", want: countQueryFilter{}},
		{name: "level", query: "level:error", want: countQueryFilter{Level: "error"}},
		{name: "level and service", query: "level:error service:api", want: countQueryFilter{Level: "error", Service: "api"}},
		{name: "free text", query: "timeout", wantErr: true},
		{name: "unknown field", query: "user_id:42", wantErr: true},
		{name: "environment override", query: "env:staging", wantErr: true},
		{name: "service conflict", query: "service:api", condService: "web", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCountQuery(tt.query, tt.condService)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseCountQuery(%q) = %+v, want an error", tt.query, got)
				}
				if !errors.Is(err, ErrInvalidWatchConfig) {
					t.Errorf("error = %v, want it to wrap ErrInvalidWatchConfig", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCountQuery(%q): %v", tt.query, err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Issue 3 — failed evaluations must advance the schedule
// ---------------------------------------------------------------------------

// TestEvaluate_InvalidConditionDisablesWatch proves a permanently malformed
// condition stops being retried on every tick and every ingest batch.
func TestEvaluate_InvalidConditionDisablesWatch(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: json.RawMessage(`{"type":"treshold","metric":"error_rate","op":"gt","value":0.05}`),
		Service:        "web",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	evaluator := NewWatchEvaluator(NewWatchMetrics(logStore), watchStore)
	if _, err := evaluator.Evaluate(ctx, w); err == nil {
		t.Fatal("expected an evaluation error for a malformed condition type")
	} else if !errors.Is(err, ErrInvalidWatchConfig) {
		t.Fatalf("error = %v, want it to wrap ErrInvalidWatchConfig", err)
	}

	after, err := watchStore.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != store.WatchStatusExpired {
		t.Errorf("status = %q, want expired (a watch that can never evaluate must not be retried)", after.Status)
	}

	due, err := watchStore.GetDueWatches(ctx)
	if err != nil {
		t.Fatalf("GetDueWatches: %v", err)
	}
	for _, d := range due {
		if d.ID == w.ID {
			t.Error("the disabled watch is still returned as due")
		}
	}
}

// TestEvaluate_TransientFailureAdvancesSchedule proves a store-level failure
// still moves next_check_at/last_checked_at forward, so the scheduler and the
// stream evaluator stop hammering the watch every tick and every batch.
func TestEvaluate_TransientFailureAdvancesSchedule(t *testing.T) {
	watchStore, _ := setupWatchTestDB(t)
	ctx := context.Background()

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.05, "web"),
		Service:        "web",
		CheckInterval:  "30s",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	failing := &rangeLogStore{err: fmt.Errorf("log store unavailable")}
	evaluator := NewWatchEvaluator(NewWatchMetrics(failing), watchStore)

	before := time.Now().UTC()
	if _, err := evaluator.Evaluate(ctx, w); err == nil {
		t.Fatal("expected an evaluation error")
	} else if errors.Is(err, ErrInvalidWatchConfig) {
		t.Fatalf("a store outage must not be treated as a config error: %v", err)
	}

	after, err := watchStore.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.Status != store.WatchStatusActive {
		t.Errorf("status = %q, want active (a transient failure must not disable the watch)", after.Status)
	}
	if after.LastCheckedAt == nil {
		t.Fatal("last_checked_at is still nil — the stream evaluator's coordination gate never engages")
	}
	if after.NextCheckAt == nil || !after.NextCheckAt.After(before) {
		t.Fatalf("next_check_at = %v, want it advanced past %v", after.NextCheckAt, before)
	}
}

// ---------------------------------------------------------------------------
// Issues 4 & 5 — baseline environment scoping and the error_class column
// ---------------------------------------------------------------------------

func TestCaptureBaseline_ScopedToEnvironment(t *testing.T) {
	ls := &rangeLogStore{
		countsFor:   func(store.LogCountParams) map[string]int { return map[string]int{"info": 9, "error": 1} },
		distinctFor: func(string, store.LogCountParams) []string { return []string{"TimeoutError"} },
	}
	w := &store.Watch{ID: "w1", Service: "api", Environment: "production", BaselineWindow: "1h"}

	baseline, err := CaptureBaseline(context.Background(), ls, NewWatchMetrics(ls), w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}

	for i, p := range ls.calls() {
		if p.Environment != "production" {
			t.Errorf("CountByLevel call %d environment = %q, want production", i, p.Environment)
		}
	}
	if len(ls.distinctCalls) != 1 || ls.distinctCalls[0].Environment != "production" {
		t.Errorf("DistinctValues env = %+v, want production", ls.distinctCalls)
	}
	if len(ls.summaryCalls) == 0 || ls.summaryCalls[0].Environment != "production" {
		t.Errorf("SearchRequestSummaries env = %+v, want production", ls.summaryCalls)
	}
	if len(ls.distinctField) != 1 || ls.distinctField[0] != "error_class" {
		t.Errorf("distinct column = %v, want [error_class] (the store has no exception_class column)", ls.distinctField)
	}
	if len(baseline.ExceptionClasses) != 1 || baseline.ExceptionClasses[0] != "TimeoutError" {
		t.Errorf("exception classes = %v, want [TimeoutError]", baseline.ExceptionClasses)
	}
}

// TestCaptureBaseline_RecordsErrorClasses is the end-to-end proof against the
// real log store: a long-standing exception class must land in the baseline so
// alert evidence does not report it as new.
func TestCaptureBaseline_RecordsErrorClasses(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	if _, err := logStore.BatchInsert(ctx, []store.LogEntry{{
		Timestamp:      time.Now().UTC().Add(-time.Minute),
		Level:          "error",
		Service:        "api",
		Message:        "boom",
		ExceptionClass: "TimeoutError",
	}}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "api"),
		Service:        "api",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	baseline, err := CaptureBaseline(ctx, logStore, NewWatchMetrics(logStore), w)
	if err != nil {
		t.Fatalf("CaptureBaseline: %v", err)
	}
	found := false
	for _, cls := range baseline.ExceptionClasses {
		if cls == "TimeoutError" {
			found = true
		}
	}
	if !found {
		t.Errorf("exception classes = %v, want it to contain TimeoutError", baseline.ExceptionClasses)
	}
}

// ---------------------------------------------------------------------------
// Issue 6 — measurement window is the check interval, not the baseline window
// ---------------------------------------------------------------------------

func TestEvaluate_MeasuresOverCheckInterval(t *testing.T) {
	watchStore, _ := setupWatchTestDB(t)
	ctx := context.Background()

	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return map[string]int{"info": 10} }}

	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("log_count", "gt", 100, "web"),
		Service:        "web",
		CheckInterval:  "30s",
		BaselineWindow: "1h",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	evaluator := NewWatchEvaluator(NewWatchMetrics(ls), watchStore)
	if _, err := evaluator.Evaluate(ctx, w); err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	calls := ls.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d count queries, want 1", len(calls))
	}
	got := calls[0].Until.Sub(calls[0].Since)
	if math.Abs(got.Seconds()-30) > 1 {
		t.Errorf("measurement window = %s, want ~30s (the check interval, not the 1h baseline window)", got)
	}
}

// TestEvalRelative_MeasuresOverBaselineWindow is the flip side: a relative
// condition must measure over the window the baseline was captured over, or it
// compares a 30s count against an hour's worth of baseline.
func TestEvalRelative_MeasuresOverBaselineWindow(t *testing.T) {
	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return map[string]int{"info": 10} }}
	baseline := &store.WatchBaseline{LogCount: 100, WindowDuration: "1h"}
	c := &Condition{
		Type:             "relative",
		Metric:           store.WatchMetricLogCount,
		Op:               store.WatchOpGreaterThan,
		BaselineMultiple: 2,
	}

	if _, err := EvaluateCondition(context.Background(), c, NewWatchMetrics(ls), baseline, "prod", 30*time.Second); err != nil {
		t.Fatalf("eval: %v", err)
	}
	calls := ls.calls()
	if len(calls) != 1 {
		t.Fatalf("got %d count queries, want 1", len(calls))
	}
	if got := calls[0].Until.Sub(calls[0].Since); math.Abs(got.Hours()-1) > 0.01 {
		t.Errorf("measurement window = %s, want ~1h (the baseline's own window)", got)
	}
}

// ---------------------------------------------------------------------------
// Issue 7 — consecutive breaches must not lose increments under concurrency
// ---------------------------------------------------------------------------

// TestEvaluate_ConcurrentBreachesDoNotLoseIncrements runs several evaluations
// from the same stale snapshot, exactly as the scheduler tick and the stream
// evaluator's goroutines do. Every observed breach must advance the counter;
// the old read-modify-write from the caller's snapshot collapsed them.
func TestEvaluate_ConcurrentBreachesDoNotLoseIncrements(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	insertTestLogs(t, logStore, "web", 5, "info")
	insertTestLogs(t, logStore, "web", 5, "error")

	const evaluations = 8
	w, err := watchStore.Create(ctx, store.CreateWatchParams{
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
		Service:        "web",
		MinConsecutive: evaluations + 1, // never alert, just count
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	evaluator := NewWatchEvaluator(NewWatchMetrics(logStore), watchStore)

	// Every goroutine starts from its own copy of the same pre-check snapshot.
	var wg sync.WaitGroup
	errs := make([]error, evaluations)
	for i := 0; i < evaluations; i++ {
		snap := *w
		wg.Add(1)
		go func(i int, snap store.Watch) {
			defer wg.Done()
			_, errs[i] = evaluator.Evaluate(ctx, &snap)
		}(i, snap)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("Evaluate[%d]: %v", i, err)
		}
	}

	after, err := watchStore.GetByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.ConsecutiveBreaches != evaluations {
		t.Errorf("consecutive_breaches = %d, want %d — increments were lost", after.ConsecutiveBreaches, evaluations)
	}
}

// ---------------------------------------------------------------------------
// Issue 10 — notification dispatch must not block the caller
// ---------------------------------------------------------------------------

type blockingNotifier struct {
	release chan struct{}
	done    chan struct{}
}

func (n *blockingNotifier) NotifyWatchAlert(context.Context, *store.WatchAlert, *store.Watch) error {
	<-n.release
	close(n.done)
	return nil
}

func TestNotifyDispatcher_DoesNotBlockCaller(t *testing.T) {
	n := &blockingNotifier{release: make(chan struct{}), done: make(chan struct{})}
	d := newNotifyDispatcher()

	start := time.Now()
	d.dispatch(context.Background(), []WatchAlertNotifier{n}, testAlert(), testWatch())
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dispatch blocked for %s — the scheduler tick would stall behind a wedged webhook", elapsed)
	}

	close(n.release)
	d.wait()
	select {
	case <-n.done:
	default:
		t.Error("notifier never ran")
	}
}

// ---------------------------------------------------------------------------
// Issue 11 — triggered watches past their expiry must stop being re-measured
// ---------------------------------------------------------------------------

// expiringWatchStore serves one triggered, past-expiry watch and records status
// updates.
type expiringWatchStore struct {
	store.WatchStore
	watch      store.Watch
	lastParams store.ListWatchParams
	statuses   []store.WatchStatus
}

func (s *expiringWatchStore) List(_ context.Context, p store.ListWatchParams) ([]store.Watch, error) {
	s.lastParams = p
	return []store.Watch{s.watch}, nil
}

func (s *expiringWatchStore) UpdateStatus(_ context.Context, _ string, status store.WatchStatus) error {
	s.statuses = append(s.statuses, status)
	return nil
}

func TestCheckTriggeredWatches_ExpiresPastExpiryWatches(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	ws := &expiringWatchStore{watch: store.Watch{
		ID:             "w1",
		Status:         store.WatchStatusTriggered,
		ExpiresAt:      &past,
		BaselineWindow: "1h",
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "web"),
	}}
	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int { return map[string]int{"info": 1} }}
	metrics := NewWatchMetrics(ls)

	s := NewWatchScheduler(WatchSchedulerOpts{
		WatchStore:     ws,
		SessionManager: NewWatchSessionManager(ws, metrics),
	})
	s.checkTriggeredWatches(context.Background())

	if len(ws.statuses) != 1 || ws.statuses[0] != store.WatchStatusExpired {
		t.Errorf("status updates = %v, want [expired]", ws.statuses)
	}
	if len(ls.calls()) != 0 {
		t.Errorf("expired watch was still measured (%d log-store queries)", len(ls.calls()))
	}
	if ws.lastParams.Limit != maxTriggeredWatchesPerPoll {
		t.Errorf("list limit = %d, want %d", ws.lastParams.Limit, maxTriggeredWatchesPerPoll)
	}
}

// ---------------------------------------------------------------------------
// Issue 12 — keyedMutex must not leak entries
// ---------------------------------------------------------------------------

func TestKeyedMutex_ReleasesEntries(t *testing.T) {
	var k keyedMutex

	for i := 0; i < 100; i++ {
		unlock := k.lock(fmt.Sprintf("watch-%d", i))
		unlock()
	}
	if got := k.size(); got != 0 {
		t.Errorf("tracked mutexes = %d, want 0 after every holder released", got)
	}

	// Contended keys must still be mutually exclusive and still be cleaned up.
	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := k.lock("hot")
			defer unlock()
			counter++
		}()
	}
	wg.Wait()
	if counter != 50 {
		t.Errorf("counter = %d, want 50", counter)
	}
	if got := k.size(); got != 0 {
		t.Errorf("tracked mutexes = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// Issue 13 — alert evidence must be environment-scoped
// ---------------------------------------------------------------------------

func TestEvidenceBuilder_ScopedToEnvironment(t *testing.T) {
	ls := &rangeLogStore{}
	b := NewWatchEvidenceBuilder(ls, NewWatchMetrics(ls))
	w := &store.Watch{
		ID:             "w1",
		Service:        "api",
		Environment:    "production",
		BaselineWindow: "1h",
		ConditionsJSON: condJSONWithService("error_rate", "gt", 0.1, "api"),
	}

	if _, err := b.Build(context.Background(), w, &WatchEvalResult{Value: 0.5}); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(ls.searchCalls) != 2 {
		t.Fatalf("got %d log searches, want 2", len(ls.searchCalls))
	}
	for i, p := range ls.searchCalls {
		if p.Environment != "production" {
			t.Errorf("search %d environment = %q, want production", i, p.Environment)
		}
	}
	if len(ls.summaryCalls) != 1 || ls.summaryCalls[0].Environment != "production" {
		t.Errorf("summary search env = %+v, want production", ls.summaryCalls)
	}
}

// ---------------------------------------------------------------------------
// Issue 14 — error_count must include fatal, like error_rate and the baseline
// ---------------------------------------------------------------------------

func TestMeasure_ErrorCount_IncludesFatal(t *testing.T) {
	ls := &rangeLogStore{countsFor: func(store.LogCountParams) map[string]int {
		return map[string]int{"info": 90, "error": 3, "fatal": 7}
	}}
	m := NewWatchMetrics(ls)

	got, err := m.Measure(context.Background(), store.WatchMetricErrorCount, "api", "", "prod", time.Hour)
	if err != nil {
		t.Fatalf("Measure: %v", err)
	}
	if got != 10 {
		t.Errorf("error_count = %v, want 10 (3 error + 7 fatal)", got)
	}
}

// ---------------------------------------------------------------------------
// Issue 15 — compound watches auto-resolve without a baseline
// ---------------------------------------------------------------------------

func TestCheckAutoResolve_CompoundWithNilBaseline(t *testing.T) {
	watchStore, logStore := setupWatchTestDB(t)
	ctx := context.Background()

	// All info logs: error_rate is 0, so neither leaf breaches.
	insertTestLogs(t, logStore, "web", 10, "info")

	compound, _ := json.Marshal(map[string]any{
		"all": []any{
			map[string]any{"type": "threshold", "metric": "error_rate", "op": "gt", "value": 0.1, "service": "web"},
			map[string]any{"type": "threshold", "metric": "log_count", "op": "gt", "value": 1e6, "service": "web"},
		},
	})
	w, err := watchStore.Create(ctx, store.CreateWatchParams{ConditionsJSON: compound, Service: "web"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := watchStore.UpdateStatus(ctx, w.ID, store.WatchStatusTriggered); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	w, _ = watchStore.GetByID(ctx, w.ID)
	if w.BaselineJSON != nil {
		t.Fatal("precondition: this watch must have no baseline")
	}

	mgr := NewWatchSessionManager(watchStore, NewWatchMetrics(logStore))
	if err := mgr.CheckAutoResolve(ctx, w); err != nil {
		t.Fatalf("CheckAutoResolve: %v", err)
	}

	after, _ := watchStore.GetByID(ctx, w.ID)
	if after.Status != store.WatchStatusResolved {
		t.Errorf("status = %q, want resolved (a compound watch needs no baseline to stop breaching)", after.Status)
	}
}
