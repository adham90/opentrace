package healthcheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// fakeHealthCheckStore is a minimal in-memory HealthCheckStore that mimics the
// SQLite adapter's pagination behaviour (a zero limit is capped at 100).
type fakeHealthCheckStore struct {
	mu       sync.Mutex
	checks   []store.HealthCheck
	results  []store.HealthCheckResult
	listCall int
}

const fakeDefaultLimit = 100

func (f *fakeHealthCheckStore) List(_ context.Context, params store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCall++

	limit := params.Limit
	if limit <= 0 || limit > fakeDefaultLimit {
		limit = fakeDefaultLimit
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}
	if offset >= len(f.checks) {
		return nil, nil
	}
	end := offset + limit
	if end > len(f.checks) {
		end = len(f.checks)
	}
	page := make([]store.HealthCheck, end-offset)
	copy(page, f.checks[offset:end])
	return page, nil
}

func (f *fakeHealthCheckStore) RecordResult(_ context.Context, result store.HealthCheckResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, result)
	return nil
}

func (f *fakeHealthCheckStore) recordedIDs() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int, len(f.results))
	for _, r := range f.results {
		out[r.HealthCheckID]++
	}
	return out
}

func (f *fakeHealthCheckStore) Create(context.Context, store.CreateHealthCheckParams) (*store.HealthCheck, error) {
	return nil, nil
}
func (f *fakeHealthCheckStore) Get(context.Context, string) (*store.HealthCheck, error) {
	return nil, store.ErrNotFound
}
func (f *fakeHealthCheckStore) Delete(context.Context, string) error           { return nil }
func (f *fakeHealthCheckStore) SetEnabled(context.Context, string, bool) error { return nil }
func (f *fakeHealthCheckStore) LatestResults(context.Context, string, int) ([]store.HealthCheckResult, error) {
	return nil, nil
}
func (f *fakeHealthCheckStore) UptimeSummaries(context.Context, time.Time) ([]store.UptimeSummary, error) {
	return nil, nil
}
func (f *fakeHealthCheckStore) PruneResults(context.Context, time.Duration) (int64, error) {
	return 0, nil
}

func newTestCheck(id, url string) store.HealthCheck {
	return store.HealthCheck{
		ID:             id,
		Name:           id,
		URL:            url,
		Method:         http.MethodGet,
		IntervalSecs:   3600,
		TimeoutSecs:    2,
		ExpectedStatus: http.StatusOK,
		Enabled:        true,
	}
}

// TestSchedulerProbesChecksBeyondFirstPage proves the scheduler paginates. It
// drives tick() — the real poll path — rather than the listAllChecks helper, so
// reverting tick() to a single unpaginated store.List call fails the test:
// only the first page of checks would ever be probed.
func TestSchedulerProbesChecksBeyondFirstPage(t *testing.T) {
	const total = 250
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := &fakeHealthCheckStore{}
	for i := 0; i < total; i++ {
		st.checks = append(st.checks, newTestCheck(fmt.Sprintf("hc-%03d", i), srv.URL))
	}

	// A long interval means each check runs at most once, so repeated ticks
	// only pick up the ones the concurrency semaphore deferred last round.
	s := NewScheduler(st, time.Hour)
	ctx := context.Background()
	maxRounds := total/cap(s.sem) + 4
	var recorded map[string]int
	for round := 0; round < maxRounds; round++ {
		s.tick(ctx)
		s.wg.Wait()
		recorded = st.recordedIDs()
		if len(recorded) >= total {
			break
		}
	}

	if len(recorded) != total {
		t.Fatalf("tick() probed %d distinct checks, want %d (the store caps a page at %d, so an unpaginated List sees only the first page)",
			len(recorded), total, fakeDefaultLimit)
	}
	if _, ok := recorded["hc-249"]; !ok {
		t.Error("the last check was never probed")
	}
	if st.listCall < 3 {
		t.Errorf("List called %d times, want at least 3 pages", st.listCall)
	}
}

// TestSchedulerDefersSkippedCheckToNextTick proves that a check skipped because
// the concurrency semaphore was full is retried on the next poll rather than
// losing its whole interval (lastRun must not be stamped on the skip path).
func TestSchedulerDefersSkippedCheckToNextTick(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	st := &fakeHealthCheckStore{checks: []store.HealthCheck{newTestCheck("hc-1", srv.URL)}}
	s := NewScheduler(st, time.Hour)

	// Saturate the semaphore so the first tick has to take the skip path.
	for i := 0; i < cap(s.sem); i++ {
		s.sem <- struct{}{}
	}
	s.tick(context.Background())

	if n := len(st.recordedIDs()); n != 0 {
		t.Fatalf("probe ran despite a full semaphore (%d results)", n)
	}
	s.mu.Lock()
	_, stamped := s.lastRun["hc-1"]
	s.mu.Unlock()
	if stamped {
		t.Fatal("lastRun was stamped for a skipped check; it loses a full interval")
	}

	// Drain the semaphore and tick again: the check has a 1h interval, so it can
	// only run now if the skip did not consume it.
	for i := 0; i < cap(s.sem); i++ {
		<-s.sem
	}
	s.tick(context.Background())
	s.Stop()

	if st.recordedIDs()["hc-1"] != 1 {
		t.Errorf("deferred check was not probed on the next tick: %v", st.recordedIDs())
	}
}

// TestSchedulerBackoffStateNoRace exercises isDue on the tick goroutine
// concurrently with runCheck's backoff mutation on probe goroutines. Reading
// consecutiveFailures off the *backoffState outside backoffMu makes this fail
// under -race.
func TestSchedulerBackoffStateNoRace(t *testing.T) {
	// Always fail so runCheck keeps incrementing consecutiveFailures.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	hc := newTestCheck("hc-race", srv.URL)
	hc.IntervalSecs = 0 // always due
	st := &fakeHealthCheckStore{checks: []store.HealthCheck{hc}}

	s := NewScheduler(st, time.Hour)
	ctx := context.Background()

	// Seed the backoff entry so readers and writers touch the same struct from
	// the very first iteration.
	s.backoffMu.Lock()
	s.backoff[hc.ID] = &backoffState{}
	s.backoffMu.Unlock()

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s.EffectiveInterval(hc)
				s.isDue(hc, time.Now())
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				s.runCheck(ctx, hc)
			}
		}()
	}
	wg.Wait()
	s.Stop()

	if s.consecutiveFailures("hc-race") == 0 {
		t.Error("expected consecutive failures to accumulate")
	}
}

// TestSchedulerEffectiveIntervalBackoff pins the backoff curve, including the
// clamp that keeps a long-failing check from overflowing the multiplier.
func TestSchedulerEffectiveIntervalBackoff(t *testing.T) {
	st := &fakeHealthCheckStore{}
	s := NewScheduler(st, time.Hour)
	hc := newTestCheck("hc-1", "http://example.invalid")
	hc.IntervalSecs = 60

	tests := []struct {
		failures int
		want     time.Duration
	}{
		{0, 60 * time.Second},
		{2, 60 * time.Second},
		{3, 120 * time.Second},
		{4, 240 * time.Second},
		{5, 240 * time.Second},
		{1000, 240 * time.Second},
	}
	for _, tt := range tests {
		s.backoffMu.Lock()
		s.backoff[hc.ID] = &backoffState{consecutiveFailures: tt.failures}
		s.backoffMu.Unlock()

		if got := s.EffectiveInterval(hc); got != tt.want {
			t.Errorf("failures=%d: EffectiveInterval = %v, want %v", tt.failures, got, tt.want)
		}
	}
}
