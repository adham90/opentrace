package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
)

// TestScalarFiltersMatchAcrossSeal is the guard on the column-level filters.
//
// A search answers from two code paths at once: sealed rows are narrowed by
// applyScalarColumnFilters reading columns, unsealed rows by matchesParams
// reading a materialized entry. If the two ever disagree, a query returns
// different rows for the same data depending on whether the hour has sealed
// yet — the worst kind of bug here, because the result still looks like an
// answer. This ingests the same rows twice, seals one copy, and requires every
// filter to select the same rows from both halves.
func TestScalarFiltersMatchAcrossSeal(t *testing.T) {
	s, base := newClockedStoreTB(t)

	rows := scalarFilterRows(base)
	if _, err := s.Ingest(cloneEntries(rows)); err != nil {
		t.Fatalf("ingest sealed: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}
	// The same rows again, this time left in the live WAL.
	if _, err := s.Ingest(cloneEntries(rows)); err != nil {
		t.Fatalf("ingest live: %v", err)
	}

	start := base.Add(-time.Minute)
	end := base.Add(2 * time.Hour)

	cases := []struct {
		name  string
		apply func(*SearchParams)
	}{
		{"none", func(p *SearchParams) {}},
		{"method", func(p *SearchParams) { p.Method = "get" }},
		{"path substring", func(p *SearchParams) { p.Path = "ORD" }},
		{"handler bare controller", func(p *SearchParams) { p.Handler = "OrdersController" }},
		{"handler full", func(p *SearchParams) { p.Handler = "OrdersController#show" }},
		{"tenant", func(p *SearchParams) { p.TenantID = "acme" }},
		{"source file", func(p *SearchParams) { p.SourceFile = "app/models/order.rb" }},
		{"commit", func(p *SearchParams) { p.CommitHash = "deadbeef" }},
		{"min duration", func(p *SearchParams) { p.MinDurationMs = 200 }},
		{"positive duration only", func(p *SearchParams) { p.PositiveDurationOnly = true }},
		{"min sql count", func(p *SearchParams) { p.MinSQLCount = 5 }},
		{"n plus one", func(p *SearchParams) { p.NPlusOneOnly = true }},
		{"requests only", func(p *SearchParams) { p.RequestsOnly = true }},
		{"level and requests", func(p *SearchParams) { p.Level = "info"; p.RequestsOnly = true }},
		{"service and min duration", func(p *SearchParams) { p.Service = "api"; p.MinDurationMs = 100 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := SearchParams{Start: &start, End: &end, Limit: 500}
			tc.apply(&p)

			res, err := s.Search(p)
			if err != nil {
				t.Fatalf("search: %v", err)
			}

			// Every row was ingested twice — once sealed, once live — so a
			// filter that agrees across both halves returns an even count with
			// each message appearing exactly twice.
			perMessage := make(map[string]int)
			for _, e := range res.Entries {
				perMessage[e.Message]++
			}
			if len(perMessage) == 0 {
				t.Fatalf("filter matched nothing in either half; the case proves nothing")
			}
			for msg, n := range perMessage {
				if n != 2 {
					t.Errorf("row %q matched %d times, want 2: the sealed and unsealed filters disagree", msg, n)
				}
			}
		})
	}
}

// TestSinceIDCursorAcrossSeal checks the cursor filter separately: SinceID is
// row identity rather than row content, so the duplicate-count check above
// cannot express it.
func TestSinceIDCursorAcrossSeal(t *testing.T) {
	s, base := newClockedStoreTB(t)
	if _, err := s.Ingest(cloneEntries(scalarFilterRows(base))); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start := base.Add(-time.Minute)
	end := base.Add(2 * time.Hour)
	all, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 500, SortAsc: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all.Entries) < 3 {
		t.Fatalf("want at least 3 rows, got %d", len(all.Entries))
	}

	cursor := all.Entries[0].ID
	after, err := s.Search(SearchParams{Start: &start, End: &end, Limit: 500, SortAsc: true, SinceID: cursor})
	if err != nil {
		t.Fatalf("search after cursor: %v", err)
	}
	if len(after.Entries) != len(all.Entries)-1 {
		t.Fatalf("cursor returned %d rows, want %d", len(after.Entries), len(all.Entries)-1)
	}
	for _, e := range after.Entries {
		if e.ID <= cursor {
			t.Fatalf("cursor is not exclusive: got id %d, cursor %d", e.ID, cursor)
		}
	}
}

// TestAggregateRequestsMatchesSearch checks that the column-level RequestsOnly
// filter selects the same rows the materialized aggregate would have.
func TestAggregateRequestsMatchesSearch(t *testing.T) {
	s, base := newClockedStoreTB(t)
	if _, err := s.Ingest(cloneEntries(scalarFilterRows(base))); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start := base.Add(-time.Minute)
	end := base.Add(2 * time.Hour)
	p := SearchParams{Start: &start, End: &end, RequestsOnly: true}

	agg, err := s.AggregateRequests(p)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}

	// Recompute from the materialized rows the search path returns.
	p.Limit = 500
	res, err := s.Search(p)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	var wantCount, wantDuration, wantSQL int
	for _, e := range res.Entries {
		if e.DurationMs <= 0 {
			continue
		}
		wantCount++
		wantDuration += e.DurationMs
		wantSQL += e.DbCount
	}
	if agg.Count != wantCount {
		t.Errorf("aggregate count = %d, search says %d", agg.Count, wantCount)
	}
	if int(agg.TotalDurationMs) != wantDuration {
		t.Errorf("aggregate duration = %d, search says %d", int(agg.TotalDurationMs), wantDuration)
	}
	if int(agg.TotalSQLCount) != wantSQL {
		t.Errorf("aggregate sql count = %d, search says %d", int(agg.TotalSQLCount), wantSQL)
	}
}

// scalarFilterRows returns rows chosen so every scalar filter both selects and
// rejects at least one of them.
func scalarFilterRows(base time.Time) []chunk.Entry {
	yes, no := true, false
	at := func(i int) int64 { return base.Add(time.Duration(i) * time.Second).UnixMilli() }
	return []chunk.Entry{
		{
			Ts: at(0), Level: "info", Service: "api", Env: "production",
			Kind: "request", EventType: "http.request", Message: "fast order request",
			Method: "GET", Path: "/orders/1", Handler: "OrdersController#show",
			TenantID: "acme", Version: "deadbeef", SourceFile: "app/models/order.rb",
			DurationMs: 120, DbCount: 3, NPlusOne: &no,
		},
		{
			Ts: at(1), Level: "info", Service: "api", Env: "production",
			Kind: "request", EventType: "http.request", Message: "slow order request",
			Method: "POST", Path: "/orders", Handler: "OrdersController#create",
			TenantID: "acme", Version: "deadbeef", SourceFile: "app/models/order.rb",
			DurationMs: 900, DbCount: 40, NPlusOne: &yes,
		},
		{
			Ts: at(2), Level: "info", Service: "web", Env: "production",
			Kind: "request", EventType: "http.request", Message: "asset request",
			Method: "GET", Path: "/assets/app.js", Handler: "AssetsController#show",
			TenantID: "globex", Version: "cafebabe",
			DurationMs: 5, DbCount: 0,
		},
		{
			// A timed row with request identity but no request kind/event_type:
			// isRequestEntry must still classify it as a request.
			Ts: at(3), Level: "info", Service: "api", Env: "production",
			Kind: "log", EventType: "rack.timing", Message: "legacy timed row",
			Method: "GET", Path: "/orders/legacy", Handler: "OrdersController#legacy",
			DurationMs: 250, DbCount: 6,
		},
		{
			// Not a request at any level: no kind, no timing, no identity.
			Ts: at(4), Level: "warn", Service: "worker", Env: "production",
			Kind: "log", EventType: "app.log", Message: "cache miss storm",
		},
		{
			Ts: at(5), Level: "error", Service: "api", Env: "production",
			Kind: "error", EventType: "app.error", Message: "order total exploded",
			ErrorClass: "NoMethodError", SourceFile: "app/models/order.rb",
			Version: "deadbeef",
		},
	}
}

// cloneEntries copies rows before ingest: Ingest stamps IDs into the slice it
// is given, so the same backing array cannot be ingested twice.
func cloneEntries(in []chunk.Entry) []chunk.Entry {
	out := make([]chunk.Entry, len(in))
	copy(out, in)
	return out
}

// TestBodyFilteredSearchReturnsCorrectPage guards the early-exit path.
//
// MetadataFilter and Exclude can only be judged after a row is materialized, so
// that scan walks candidates in timestamp order and stops at the first full
// page instead of materializing the whole range. Stopping early is only correct
// if the rows it stopped on really are the page the caller asked for — a bug
// here returns a plausible-looking page made of the wrong rows.
func TestBodyFilteredSearchReturnsCorrectPage(t *testing.T) {
	s, base := newClockedStoreTB(t)

	// Alternate the metadata value so the filter rejects half the rows and the
	// scan has to look past its page size to fill a page.
	const total = 400
	rows := make([]chunk.Entry, total)
	for i := range rows {
		region := "eu-west-1"
		if i%2 == 1 {
			region = "us-east-1"
		}
		rows[i] = chunk.Entry{
			Ts:      base.Add(time.Duration(i) * time.Second).UnixMilli(),
			Level:   "info",
			Service: "api",
			Env:     "production",
			Message: fmt.Sprintf("row-%d", i),
			Body:    []byte(fmt.Sprintf(`{"metadata":{"region":%q}}`, region)),
		}
	}
	if _, err := s.Ingest(cloneEntries(rows)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start, end := base.Add(-time.Minute), base.Add(2*time.Hour)
	filter := map[string]string{"region": "eu-west-1"}

	for _, asc := range []bool{false, true} {
		name := "newest first"
		if asc {
			name = "oldest first"
		}
		t.Run(name, func(t *testing.T) {
			// The whole matching set, unpaged, is the reference answer.
			all, err := s.Search(SearchParams{
				Start: &start, End: &end, Limit: 500, SortAsc: asc,
				MetadataFilter: filter,
			})
			if err != nil {
				t.Fatalf("reference search: %v", err)
			}
			if len(all.Entries) != total/2 {
				t.Fatalf("reference search matched %d rows, want %d", len(all.Entries), total/2)
			}

			// A page must be the matching prefix of that reference answer.
			const pageSize = 20
			for _, offset := range []int{0, 20, 100} {
				page, err := s.Search(SearchParams{
					Start: &start, End: &end, Limit: pageSize, Offset: offset,
					SortAsc: asc, MetadataFilter: filter,
				})
				if err != nil {
					t.Fatalf("page at offset %d: %v", offset, err)
				}
				if len(page.Entries) != pageSize {
					t.Fatalf("page at offset %d returned %d rows, want %d",
						offset, len(page.Entries), pageSize)
				}
				for i, got := range page.Entries {
					want := all.Entries[offset+i]
					if got.ID != want.ID {
						t.Fatalf("page at offset %d, row %d: got %q, want %q — the early exit stopped on the wrong rows",
							offset, i, got.Message, want.Message)
					}
					if got.Message == "" || got.Body == nil {
						t.Fatalf("page at offset %d, row %d came back only partly materialized", offset, i)
					}
				}
			}
		})
	}
}

// TestExcludeFilterPageIsBounded checks the same early exit for the Exclude
// filter, which shares the path.
func TestExcludeFilterPageIsBounded(t *testing.T) {
	s, base := newClockedStoreTB(t)

	rows := make([]chunk.Entry, 200)
	for i := range rows {
		path := "/orders"
		if i%3 == 0 {
			path = "/health"
		}
		rows[i] = chunk.Entry{
			Ts:    base.Add(time.Duration(i) * time.Second).UnixMilli(),
			Level: "info", Service: "api", Env: "production",
			Kind: "request", EventType: "http.request",
			Message: fmt.Sprintf("row-%d", i), Method: "GET", Path: path,
			DurationMs: 10,
		}
	}
	if _, err := s.Ingest(cloneEntries(rows)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	s.writer.now = func() time.Time { return base.Add(time.Hour) }
	if err := s.SealCurrentHour(); err != nil {
		t.Fatalf("seal: %v", err)
	}

	start, end := base.Add(-time.Minute), base.Add(2*time.Hour)
	res, err := s.Search(SearchParams{
		Start: &start, End: &end, Limit: 25,
		Exclude: map[string]string{"path": "/health"},
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res.Entries) != 25 {
		t.Fatalf("got %d rows, want a full page of 25", len(res.Entries))
	}
	for _, e := range res.Entries {
		if e.Path == "/health" {
			t.Fatalf("excluded path leaked into the page: %q", e.Message)
		}
	}
}
