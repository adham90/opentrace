package adapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

func reqEntry(ts time.Time, env, controller, action string, duration float64, sqlCount int, nPlusOne bool) store.LogEntry {
	return store.LogEntry{
		Timestamp:   ts,
		Level:       "info",
		Service:     "api",
		Environment: env,
		Kind:        "request",
		Message:     "request",
		RequestSummary: &store.RequestSummary{
			Controller: controller,
			Action:     action,
			Method:     "GET",
			Path:       "/orders",
			Status:     200,
			DurationMs: duration,
			SQLCount:   sqlCount,
			NPlusOne:   nPlusOne,
			CacheReads: 10,
			CacheHits:  7,
		},
	}
}

func window(now time.Time) (*time.Time, *time.Time) {
	start := now.Add(-2 * time.Hour)
	end := now.Add(time.Minute)
	return &start, &end
}

// TestRequestSummariesEnvironmentScope: Environment is an authorization
// boundary here; it used to be dropped, handing a staging-scoped caller
// production paths, controllers and latencies.
func TestRequestSummariesEnvironmentScope(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		reqEntry(now, "production", "OrdersController", "create", 120, 3, false),
		reqEntry(now, "staging", "OrdersController", "create", 90, 2, false),
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, Environment: "staging", Limit: 50,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("env scope: want 1 staging row, got %d", len(got))
	}
	if got[0].DurationMs != 90 {
		t.Fatalf("returned the wrong environment's row: %+v", got[0])
	}
}

// TestRequestSummariesSlowestNotNewest: the engine limit was applied before the
// sort, so "slowest" really meant "most recent, reordered".
func TestRequestSummariesSlowestNotNewest(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	entries := []store.LogEntry{reqEntry(now.Add(-90*time.Minute), "production", "SlowController", "show", 30000, 1, false)}
	for i := 0; i < 60; i++ {
		entries = append(entries, reqEntry(now.Add(-time.Duration(i)*time.Second), "production", "FastController", "index", 20, 1, false))
	}
	if _, err := a.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, SortBy: "duration_ms", Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("want 5 rows, got %d", len(got))
	}
	if got[0].DurationMs != 30000 {
		t.Fatalf("slowest row missing: top row is %v ms", got[0].DurationMs)
	}
}

// TestRequestSummariesControllerFilter: handlers are stored as
// "Controller#action", so an equality test against the bare controller name
// could never match and the tool reported "no data".
func TestRequestSummariesControllerFilter(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		reqEntry(now, "production", "OrdersController", "create", 120, 3, false),
		reqEntry(now, "production", "UsersController", "show", 15, 1, false),
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, Controller: "OrdersController", Limit: 50,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 1 || got[0].Controller != "OrdersController#create" {
		t.Fatalf("controller filter: got %+v", got)
	}
}

// TestRequestSummariesNPlusOneOverWholeWindow: n_plus_one_only used to examine
// only the most recent page and reported "none detected" while many existed.
func TestRequestSummariesNPlusOneOverWholeWindow(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	entries := []store.LogEntry{reqEntry(now.Add(-90*time.Minute), "production", "OrdersController", "index", 900, 200, true)}
	for i := 0; i < 60; i++ {
		entries = append(entries, reqEntry(now.Add(-time.Duration(i)*time.Second), "production", "FastController", "index", 20, 1, false))
	}
	if _, err := a.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, NPlusOneOnly: true, Limit: 20,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 1 || !got[0].NPlusOne {
		t.Fatalf("n+1 filter over the window: got %d rows", len(got))
	}

	// MinSQLCount / MinDurationMs are pushed down too.
	got, err = a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, MinSQLCount: 100, MinDurationMs: 500, Limit: 20,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("min filters: want 1 row, got %d", len(got))
	}
}

// TestAggregateRequestSummariesEnvAndCache: aggregates were computed over the
// newest 500 rows, ignored Environment, and never populated the cache figures.
func TestAggregateRequestSummariesEnvAndCache(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		reqEntry(now, "production", "OrdersController", "create", 100, 2, false),
		reqEntry(now, "production", "OrdersController", "create", 300, 4, false),
		reqEntry(now, "staging", "OrdersController", "create", 999, 9, false),
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	agg, err := a.AggregateRequestSummaries(ctx, store.RequestSummaryAggregateParams{
		Start: start, End: end, Environment: "production",
	})
	if err != nil {
		t.Fatalf("AggregateRequestSummaries: %v", err)
	}
	if agg.Count != 2 {
		t.Fatalf("env scope: want 2 rows, got %d", agg.Count)
	}
	if agg.AvgDuration != 200 {
		t.Fatalf("avg duration: want 200, got %v", agg.AvgDuration)
	}
	if agg.TotalReads != 20 || agg.TotalHits != 14 {
		t.Fatalf("cache aggregates: reads=%d hits=%d", agg.TotalReads, agg.TotalHits)
	}
	if agg.CacheHitRate != 0.7 {
		t.Fatalf("cache hit rate: want 0.7, got %v", agg.CacheHitRate)
	}
}

// TestSearchMetadataFilter pins the MCP metadata_filter argument end to end.
func TestSearchMetadataFilter(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "one", Metadata: map[string]any{"host": "server-01"}},
		{Timestamp: now, Level: "info", Service: "api", Message: "two", Metadata: map[string]any{"host": "server-02"}},
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.Search(ctx, store.LogSearchParams{
		Start: start, End: end, Limit: 50,
		MetadataFilter: map[string]string{"host": "server-01"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 || got[0].Message != "one" {
		t.Fatalf("metadata filter: got %d rows", len(got))
	}
}

// TestCountByServiceErrorCount pins the ErrorCount that was always zero.
func TestCountByServiceErrorCount(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		{Timestamp: now, Level: "error", Service: "api", Message: "boom"},
		{Timestamp: now, Level: "info", Service: "api", Message: "ok"},
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	counts, err := a.CountByService(ctx, store.LogCountParams{Since: now.Add(-time.Hour), Until: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("CountByService: %v", err)
	}
	if len(counts) != 1 || counts[0].ErrorCount != 1 || counts[0].Total != 2 {
		t.Fatalf("service counts: %+v", counts)
	}
}

// TestHistogramServiceFilter pins the dropped Service filter.
func TestHistogramServiceFilter(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "checkout", Message: "a"},
		{Timestamp: now, Level: "info", Service: "other", Message: "b"},
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	buckets, err := a.Histogram(ctx, store.LogHistogramParams{
		Since: now.Add(-time.Hour), Until: now.Add(time.Minute), Interval: time.Minute, Service: "checkout",
	})
	if err != nil {
		t.Fatalf("Histogram: %v", err)
	}
	total := 0
	for _, b := range buckets {
		total += b.Total
	}
	if total != 1 {
		t.Fatalf("service-filtered histogram: want 1, got %d", total)
	}
}

// TestDistinctValuesScoped pins the Service/Level/Environment scoping.
func TestDistinctValuesScoped(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	if _, err := a.BatchInsert(ctx, []store.LogEntry{
		{Timestamp: now, Level: "error", Service: "api", Environment: "production", Message: "a", ErrorFingerprint: "fp1"},
		{Timestamp: now, Level: "info", Service: "api", Environment: "production", Message: "b", ErrorFingerprint: "fp2"},
		{Timestamp: now, Level: "error", Service: "worker", Environment: "staging", Message: "c", ErrorFingerprint: "fp3"},
	}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	params := store.LogCountParams{
		Since: now.Add(-time.Hour), Until: now.Add(time.Minute),
		Service: "api", Level: "error", Environment: "production",
	}
	vals, err := a.DistinctValues(ctx, "error_fingerprint", params)
	if err != nil {
		t.Fatalf("DistinctValues: %v", err)
	}
	if len(vals) != 1 || vals[0] != "fp1" {
		t.Fatalf("scoped distinct: got %v", vals)
	}
}

// TestGetByIDNotFound: 404s must use the store.ErrNotFound sentinel and never
// surface an on-disk path.
func TestGetByIDNotFound(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)

	_, err := a.GetByID(ctx, 999999999)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want store.ErrNotFound, got %v", err)
	}
}

// TestBatchInsertAssignsIDs: post-insert processing runs on the caller's slice
// and needs the engine-assigned IDs.
func TestBatchInsertAssignsIDs(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	entries := []store.LogEntry{
		{Timestamp: now, Level: "info", Service: "api", Message: "one"},
		{Timestamp: now, Level: "info", Service: "api", Message: "two"},
	}
	if _, err := a.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
	for i, e := range entries {
		if e.ID == 0 {
			t.Fatalf("entry %d has no assigned ID", i)
		}
	}
	if entries[0].ID == entries[1].ID {
		t.Fatal("assigned IDs must be distinct")
	}

	got, err := a.GetByID(ctx, entries[1].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Message != "two" {
		t.Fatalf("assigned ID points at the wrong row: %q", got.Message)
	}
}

// TestFlatFieldsRoundTrip pins the ~19 flat SDK fields through storage.
func TestFlatFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	in := store.LogEntry{
		Timestamp: now, Level: "info", Service: "api", Message: "flat",
		Host: "server-01", Kind: "job", TenantID: "t1", SessionID: "s1", Route: "/orders/:id",
		CacheMs: 5, CacheHits: 7, CacheMisses: 3, ExtMs: 11, ExtCount: 2,
		RenderMs: 13, AllocCount: 17, MemDeltaMb: 170, SlowQueries: 4,
		ErrorMessage: "nope", JobClass: "MailJob", JobQueue: "default", JobID: "j1", QueueMs: 21,
	}
	if _, err := a.BatchInsert(ctx, []store.LogEntry{in}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	// Seal so the values are read back out of the columnar chunk, which is
	// where the kind column was being decoded with the wrong reader.
	if err := a.Engine().SealCurrentHour(); err != nil {
		t.Fatalf("SealCurrentHour: %v", err)
	}

	start, end := window(now)
	got, err := a.Search(ctx, store.LogSearchParams{Start: start, End: end, Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	g := got[0]
	checks := []struct {
		name       string
		want, have any
	}{
		{"Host", in.Host, g.Host},
		{"Kind", in.Kind, g.Kind},
		{"TenantID", in.TenantID, g.TenantID},
		{"SessionID", in.SessionID, g.SessionID},
		{"Route", in.Route, g.Route},
		{"CacheMs", in.CacheMs, g.CacheMs},
		{"CacheHits", in.CacheHits, g.CacheHits},
		{"CacheMisses", in.CacheMisses, g.CacheMisses},
		{"ExtMs", in.ExtMs, g.ExtMs},
		{"ExtCount", in.ExtCount, g.ExtCount},
		{"RenderMs", in.RenderMs, g.RenderMs},
		{"AllocCount", in.AllocCount, g.AllocCount},
		{"MemDeltaMb", in.MemDeltaMb, g.MemDeltaMb},
		{"SlowQueries", in.SlowQueries, g.SlowQueries},
		{"ErrorMessage", in.ErrorMessage, g.ErrorMessage},
		{"JobClass", in.JobClass, g.JobClass},
		{"JobQueue", in.JobQueue, g.JobQueue},
		{"JobID", in.JobID, g.JobID},
		{"QueueMs", in.QueueMs, g.QueueMs},
	}
	for _, c := range checks {
		if c.want != c.have {
			t.Errorf("%s: want %v, got %v", c.name, c.want, c.have)
		}
	}
}

// TestRequestSummariesFullPageWithZeroDurationRows: rows without a duration
// can't be rendered as a summary, and the adapter used to drop them *after* the
// engine had applied the limit — so a page came back short (here: empty) while
// plenty of qualifying rows existed. The predicate belongs in the engine, ahead
// of the limit.
func TestRequestSummariesFullPageWithZeroDurationRows(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter(t)
	now := time.Now().UTC()

	var entries []store.LogEntry
	// Rows with no duration but the highest SQL counts: they sort first and
	// would fill the whole page before being discarded.
	for i := 0; i < 10; i++ {
		entries = append(entries, reqEntry(now.Add(-time.Duration(i)*time.Second), "production", "GhostController", "index", 0, 100, false))
	}
	for i := 0; i < 10; i++ {
		entries = append(entries, reqEntry(now.Add(-time.Duration(i)*time.Minute), "production", "OrdersController", "index", 25, 5, false))
	}
	if _, err := a.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}

	start, end := window(now)
	got, err := a.SearchRequestSummaries(ctx, store.RequestSummarySearchParams{
		Start: start, End: end, SortBy: "sql_count", Limit: 5,
	})
	if err != nil {
		t.Fatalf("SearchRequestSummaries: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("short page: want 5 rows, got %d", len(got))
	}
	for _, r := range got {
		if r.DurationMs <= 0 {
			t.Fatalf("zero-duration row leaked into the page: %+v", r)
		}
	}
}
