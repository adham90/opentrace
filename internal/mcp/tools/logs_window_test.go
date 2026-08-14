package tools

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// windowLogStore — a LogStore that actually honours the time window, the
// filters and the limit. The shared mock returns every entry for every query,
// so a handler that forgets Start/End (the bug this file guards) would look
// perfectly healthy against it.
// ---------------------------------------------------------------------------

type windowLogStore struct {
	entries []store.LogEntry

	// levelCounts is returned by CountByLevel, keyed by service ("" = all).
	levelCounts map[string]map[string]int
	// services is returned by CountByService. ErrorCount is deliberately left
	// zero, exactly as the production adapter leaves it.
	services []store.ServiceLogCount

	searchCalls  atomic.Int64
	countCalls   atomic.Int64
	requestSums  []store.RequestSummaryResult
	distinctVals []string
}

var _ store.LogStore = (*windowLogStore)(nil)

func (s *windowLogStore) BatchInsert(context.Context, []store.LogEntry) (int, error) { return 0, nil }

func (s *windowLogStore) Search(_ context.Context, p store.LogSearchParams) ([]store.LogEntry, error) {
	s.searchCalls.Add(1)

	// Mirror the engine's defaults exactly: a nil Start means "the last hour",
	// a nil End means "now". This is what makes these tests meaningful — a
	// handler that forgets to derive its window from the anchor gets an
	// inverted or now-relative range here, just like in production.
	now := time.Now().UTC()
	start := now.Add(-time.Hour)
	if p.Start != nil {
		start = *p.Start
	}
	end := now
	if p.End != nil {
		end = *p.End
	}

	var out []store.LogEntry
	for _, e := range s.entries {
		if e.Timestamp.Before(start) || e.Timestamp.After(end) {
			continue
		}
		if p.TraceID != "" && e.TraceID != p.TraceID {
			continue
		}
		if p.Service != "" && e.Service != p.Service {
			continue
		}
		if p.Environment != "" && e.Environment != p.Environment {
			continue
		}
		if p.Level != "" && e.Level != p.Level {
			continue
		}
		if p.ErrorFingerprint != "" && e.ErrorFingerprint != p.ErrorFingerprint {
			continue
		}
		if p.EventType != "" && e.EventType != p.EventType {
			continue
		}
		// MetadataFilter is deliberately IGNORED — this mirrors the production
		// adapter, which drops it. The tool layer must not return unmatched rows.
		out = append(out, e)
	}
	// Newest first unless asked otherwise.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		if !p.SortAsc {
			break
		}
		_ = i
		break
	}
	if p.SortAsc {
		sortEntriesAsc(out)
	} else {
		sortEntriesDesc(out)
	}
	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

func sortEntriesAsc(e []store.LogEntry) {
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && e[j].Timestamp.Before(e[j-1].Timestamp); j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
}

func sortEntriesDesc(e []store.LogEntry) {
	sortEntriesAsc(e)
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
}

func (s *windowLogStore) GetByID(_ context.Context, id int64) (*store.LogEntry, error) {
	for i := range s.entries {
		if s.entries[i].ID == id {
			clone := s.entries[i]
			return &clone, nil
		}
	}
	return nil, store.ErrNotFound
}

func (s *windowLogStore) Prune(context.Context, time.Duration) (int64, error) { return 0, nil }

func (s *windowLogStore) CountByLevel(_ context.Context, p store.LogCountParams) (map[string]int, error) {
	s.countCalls.Add(1)
	counts := s.levelCounts[p.Service]
	if counts == nil {
		// Derive from entries when no canned answer was supplied.
		counts = map[string]int{}
		for _, e := range s.entries {
			if e.Timestamp.Before(p.Since) || e.Timestamp.After(p.Until) {
				continue
			}
			if p.Service != "" && e.Service != p.Service {
				continue
			}
			if p.Environment != "" && e.Environment != p.Environment {
				continue
			}
			counts[e.Level]++
		}
	}
	if p.Level != "" {
		filtered := map[string]int{}
		if n, ok := counts[p.Level]; ok {
			filtered[p.Level] = n
		}
		return filtered, nil
	}
	return counts, nil
}

func (s *windowLogStore) CountByService(_ context.Context, _ store.LogCountParams) ([]store.ServiceLogCount, error) {
	return s.services, nil
}

func (s *windowLogStore) Histogram(context.Context, store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	return nil, nil
}

func (s *windowLogStore) DistinctValues(context.Context, string, store.LogCountParams) ([]string, error) {
	return s.distinctVals, nil
}

func (s *windowLogStore) MetadataKeys(context.Context, store.LogCountParams) ([]string, error) {
	return nil, nil
}

func (s *windowLogStore) SearchRequestSummaries(_ context.Context, _ store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	return s.requestSums, nil
}

func (s *windowLogStore) AggregateRequestSummaries(context.Context, store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	return &store.RequestSummaryAggregates{}, nil
}

func (s *windowLogStore) RecordBatch(context.Context, string, int) error { return nil }

func (s *windowLogStore) GetBatch(context.Context, string) (*store.BatchRecord, error) {
	return nil, nil
}

func (s *windowLogStore) PruneBatches(context.Context, time.Duration) (int64, error) { return 0, nil }

func parseResult(t *testing.T, result *CallToolResult) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(extractText(t, result)), &m); err != nil {
		t.Fatalf("failed to parse response JSON: %v\n%s", err, extractText(t, result))
	}
	return m
}

// ---------------------------------------------------------------------------
// Issue 1: bucket_interval validation (CRITICAL — used to spin forever)
// ---------------------------------------------------------------------------

func TestLogsStats_NonPositiveBucketIntervalIsRejected(t *testing.T) {
	for _, interval := range []string{"0s", "-5m", "0m"} {
		t.Run(interval, func(t *testing.T) {
			ls := &windowLogStore{}
			deps := LogsDeps{LogStore: ls}
			args := map[string]any{
				"action":          "stats",
				"group_by":        "level",
				"since":           "1h",
				"bucket_interval": interval,
			}

			// Before the fix the handler never returns: bucketDur <= 0 means the
			// loop variable never advances past `until`. Bound the wait so the
			// suite fails instead of hanging forever.
			type outcome struct {
				res *CallToolResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := LogsStats(context.Background(), args, deps)
				done <- outcome{res, err}
			}()

			select {
			case got := <-done:
				if got.err != nil {
					t.Fatalf("unexpected error: %v", got.err)
				}
				if !got.res.IsError {
					t.Fatalf("bucket_interval %q must be rejected, got: %s", interval, extractText(t, got.res))
				}
				if !strings.Contains(extractText(t, got.res), "positive") {
					t.Errorf("error should explain the interval must be positive, got: %s", extractText(t, got.res))
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("LogsStats did not return for bucket_interval %q — the trend loop is unbounded", interval)
			}
		})
	}
}

func TestLogsStats_BucketCountIsCapped(t *testing.T) {
	ls := &windowLogStore{}
	deps := LogsDeps{LogStore: ls}
	args := map[string]any{
		"action":          "stats",
		"group_by":        "level",
		"since":           "30d",
		"bucket_interval": "30s",
	}
	result, err := LogsStats(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("30d/30s = 86400 buckets must be refused, got: %s", extractText(t, result))
	}
	if !strings.Contains(extractText(t, result), "bucket_interval too small") {
		t.Errorf("unexpected message: %s", extractText(t, result))
	}
	if n := ls.countCalls.Load(); n != 0 {
		t.Errorf("no store call should be issued when the request is refused, got %d", n)
	}
}

func TestLogsStats_ValidBucketIntervalStillWorks(t *testing.T) {
	ls := &windowLogStore{}
	deps := LogsDeps{LogStore: ls}
	args := map[string]any{"action": "stats", "group_by": "level", "since": "1h", "bucket_interval": "15m"}
	result, err := LogsStats(context.Background(), args, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}
}

// ---------------------------------------------------------------------------
// Issue 13: group_by=service error counts + level filter
// ---------------------------------------------------------------------------

func TestLogsStatsByService_ErrorCountsAndLevelFilter(t *testing.T) {
	ls := &windowLogStore{
		// ErrorCount deliberately zero, like the production adapter.
		services: []store.ServiceLogCount{{Service: "api", Total: 100}, {Service: "web", Total: 40}},
		levelCounts: map[string]map[string]int{
			"api": {"info": 60, "error": 40},
			"web": {"info": 39, "error": 1},
		},
	}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsStats(context.Background(), map[string]any{
		"action": "stats", "group_by": "service", "since": "1h",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	rows, _ := resp["by_service"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 services, got %v", resp["by_service"])
	}
	first, _ := rows[0].(map[string]any)
	if first["errors"].(float64) == 0 {
		t.Errorf("per-service error counts must not be zero: %v", first)
	}
	if first["error_rate_pct"].(float64) == 0 {
		t.Errorf("error_rate_pct must not be zero: %v", first)
	}

	// With level=error the totals must be the filtered ones, not the raw
	// unfiltered service totals.
	result, err = LogsStats(context.Background(), map[string]any{
		"action": "stats", "group_by": "service", "since": "1h", "level": "error",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp = parseResult(t, result)
	rows, _ = resp["by_service"].([]any)
	api, _ := rows[0].(map[string]any)
	if api["total"].(float64) != 40 {
		t.Errorf("level filter ignored: api total = %v, want 40", api["total"])
	}
	if resp["level_filter"] != "error" {
		t.Errorf("level_filter should be echoed, got %v", resp["level_filter"])
	}
}

// ---------------------------------------------------------------------------
// Issue 14: pattern stats must not present a 500-row sample as the window
// ---------------------------------------------------------------------------

func TestLogsStatsByPattern_ReportsSampleTruncation(t *testing.T) {
	now := time.Now().UTC()
	ls := &windowLogStore{levelCounts: map[string]map[string]int{"": {"error": 50000}}}
	for i := 0; i < maxPatternSample+10; i++ {
		ls.entries = append(ls.entries, store.LogEntry{
			ID: int64(i + 1), Timestamp: now.Add(-time.Duration(i) * time.Second),
			Level: "error", Service: "api", Message: "boom 1",
		})
	}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsStats(context.Background(), map[string]any{
		"action": "stats", "group_by": "pattern", "since": "1h",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if resp["sample_truncated"] != true {
		t.Errorf("sample_truncated must be true, got %v", resp["sample_truncated"])
	}
	if got := resp["analyzed_sample"].(float64); int(got) != maxPatternSample {
		t.Errorf("analyzed_sample = %v, want %d", got, maxPatternSample)
	}
	if got := resp["total_errors"].(float64); int(got) != 50000 {
		t.Errorf("total_errors must be the window-wide count (50000), got %v", got)
	}
	warnings, _ := resp["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if strings.Contains(w.(string), "Pattern analysis covers") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a truncation warning, got %v", warnings)
	}
}

// ---------------------------------------------------------------------------
// Issue 7: context around an anchor older than one hour
// ---------------------------------------------------------------------------

func oldAnchorStore() *windowLogStore {
	base := time.Now().UTC().Add(-3 * time.Hour)
	s := &windowLogStore{}
	for i := 0; i < 5; i++ {
		s.entries = append(s.entries, store.LogEntry{
			ID: int64(i + 1), Timestamp: base.Add(time.Duration(i) * time.Second),
			Level: "info", Service: "api", Environment: "production", Message: "before",
		})
	}
	s.entries = append(s.entries, store.LogEntry{
		ID: 100, Timestamp: base.Add(10 * time.Second), Level: "error", Service: "api",
		Environment: "production", Message: "the failure", TraceID: "trace-old",
		ErrorFingerprint: "fp-old",
	})
	for i := 0; i < 5; i++ {
		s.entries = append(s.entries, store.LogEntry{
			ID: int64(200 + i), Timestamp: base.Add(20*time.Second + time.Duration(i)*time.Second),
			Level: "info", Service: "api", Environment: "production", Message: "after",
			TraceID: "trace-old",
		})
	}
	return s
}

func TestLogsContext_AnchorOlderThanOneHourStillHasBeforeContext(t *testing.T) {
	ls := oldAnchorStore()
	deps := LogsDeps{LogStore: ls}

	result, err := LogsContext(context.Background(), map[string]any{
		"action": "context", "log_id": float64(100), "before": float64(3), "after": float64(2),
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if got := int(resp["before_count"].(float64)); got != 3 {
		t.Errorf("before_count = %d, want 3 (an anchor older than 1h used to return 0, and the anchor used to eat a slot)", got)
	}
	if got := int(resp["after_count"].(float64)); got != 2 {
		t.Errorf("after_count = %d, want 2", got)
	}
}

func TestLogsContext_BeforeZeroReturnsNothingBefore(t *testing.T) {
	ls := oldAnchorStore()
	deps := LogsDeps{LogStore: ls}

	result, err := LogsContext(context.Background(), map[string]any{
		"action": "context", "log_id": float64(100), "before": float64(0), "after": float64(1),
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if got := int(resp["before_count"].(float64)); got != 0 {
		t.Errorf("before=0 must return no before-entries, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Issue 9: trace assembly for a trace older than an hour
// ---------------------------------------------------------------------------

func TestLogsTrace_OlderThanOneHour(t *testing.T) {
	ls := oldAnchorStore()
	deps := LogsDeps{LogStore: ls}

	result, err := LogsTrace(context.Background(), map[string]any{
		"action": "trace", "trace_id": "trace-old",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if got := int(resp["total_entries"].(float64)); got != 6 {
		t.Fatalf("total_entries = %d, want 6 — a 3h-old trace used to report zero", got)
	}
	if resp["complete"] != true {
		t.Errorf("complete should be true for a small trace, got %v", resp["complete"])
	}
}

// ---------------------------------------------------------------------------
// Issue 19: a malformed since must be an error, not a silent 1h window
// ---------------------------------------------------------------------------

func TestLogsSearch_MalformedSinceIsRejected(t *testing.T) {
	deps := LogsDeps{LogStore: &windowLogStore{}}
	result, err := LogsSearch(context.Background(), map[string]any{
		"action": "search", "query": "OOM", "since": "1w",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("since=\"1w\" must be rejected instead of silently searching the last hour: %s", extractText(t, result))
	}
	if !strings.Contains(extractText(t, result), "invalid since") {
		t.Errorf("unexpected message: %s", extractText(t, result))
	}
}

// ---------------------------------------------------------------------------
// Issue 8: metadata_filter must never return unmatched rows
// ---------------------------------------------------------------------------

func TestLogsSearch_MetadataFilterEnforcedInToolLayer(t *testing.T) {
	now := time.Now().UTC()
	ls := &windowLogStore{entries: []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "info", Service: "api", Message: "on server-01",
			Metadata: map[string]any{"host": "server-01"}},
		{ID: 2, Timestamp: now, Level: "info", Service: "api", Message: "on server-02",
			Metadata: map[string]any{"host": "server-02"}},
		{ID: 3, Timestamp: now, Level: "info", Service: "api", Message: "no metadata"},
	}}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsSearch(context.Background(), map[string]any{
		"action":          "search",
		"metadata_filter": map[string]any{"host": "server-01"},
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	if got := int(resp["total_returned"].(float64)); got != 1 {
		t.Fatalf("metadata_filter dropped by the store must be enforced here: total_returned = %d, want 1", got)
	}
	if resp["metadata_filter_dropped"] == nil {
		t.Error("expected metadata_filter_dropped to report the rows filtered in this layer")
	}
}

// ---------------------------------------------------------------------------
// Issue 6 + 18: compare metric=errors
// ---------------------------------------------------------------------------

func TestLogsCompareErrors_CountsAndNewSourceRanking(t *testing.T) {
	ls := &windowLogStore{
		services: []store.ServiceLogCount{{Service: "api", Total: 10}, {Service: "web", Total: 10}},
		levelCounts: map[string]map[string]int{
			"api": {"error": 5000},
			"web": {"error": 50},
		},
	}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsCompare(context.Background(), map[string]any{
		"action": "compare", "metric": "errors",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	cur := resp["current"].(map[string]any)
	if got := int(cur["total_errors"].(float64)); got != 5050 {
		t.Fatalf("total_errors = %d, want 5050 — ErrorCount is never populated by the adapter, so counts must come from CountByLevel", got)
	}
}

func TestLogsCalcChange_NewSourceOutranksDrift(t *testing.T) {
	newSource, dir := logsCalcChange(0, 5000)
	drift, _ := logsCalcChange(10, 50)
	if dir != "new" {
		t.Errorf("direction = %q, want new", dir)
	}
	if newSource <= drift {
		t.Errorf("0→5000 (%v) must outrank 10→50 (%v) in biggest_movers", newSource, drift)
	}
}

// ---------------------------------------------------------------------------
// Issue 15: summary must label its sample
// ---------------------------------------------------------------------------

func TestLogsSummary_LabelsAggregationSample(t *testing.T) {
	now := time.Now().UTC()
	ls := &windowLogStore{levelCounts: map[string]map[string]int{"": {"info": 19000, "error": 1000}}}
	for i := 0; i < maxSummarySample+50; i++ {
		ls.entries = append(ls.entries, store.LogEntry{
			ID: int64(i + 1), Timestamp: now.Add(-time.Duration(i) * time.Second),
			Level: "error", Service: "api", Message: "boom", CommitHash: "abc1234",
			ErrorFingerprint: "fp-1",
		})
	}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsSummary(context.Background(), map[string]any{"action": "summary", "since": "1h"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	sample, ok := resp["aggregation_sample"].(map[string]any)
	if !ok {
		t.Fatalf("expected aggregation_sample alongside active_commits/unique_errors, got %v", keysOf(resp))
	}
	if sample["truncated"] != true {
		t.Errorf("sample should be reported as truncated, got %v", sample)
	}
	if int(sample["entries_scanned"].(float64)) != maxSummarySample {
		t.Errorf("entries_scanned = %v, want %d", sample["entries_scanned"], maxSummarySample)
	}
}

// ---------------------------------------------------------------------------
// Issue 16: slowest_endpoints must respect the service filter
// ---------------------------------------------------------------------------

func TestLogsSummary_SlowestEndpointsRespectServiceFilter(t *testing.T) {
	ls := &windowLogStore{
		requestSums: []store.RequestSummaryResult{
			{Service: "admin", RequestSummary: store.RequestSummary{Path: "/admin/slow", DurationMs: 9000}},
			{Service: "payments", RequestSummary: store.RequestSummary{Path: "/pay", DurationMs: 300}},
		},
	}
	deps := LogsDeps{LogStore: ls}

	result, err := LogsSummary(context.Background(), map[string]any{
		"action": "summary", "since": "1h", "service": "payments",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseResult(t, result)
	eps, _ := resp["slowest_endpoints"].([]any)
	for _, ep := range eps {
		m := ep.(map[string]any)
		if m["path"] == "/admin/slow" {
			t.Fatalf("another service's endpoint leaked into a service-filtered summary: %v", eps)
		}
	}
	if len(eps) != 1 {
		t.Errorf("expected only the payments endpoint, got %v", eps)
	}
}

// ---------------------------------------------------------------------------
// Issue 12: attributes field handling
// ---------------------------------------------------------------------------

func TestLogsAttributes_UnsupportedFieldRejectedAtBoundary(t *testing.T) {
	deps := LogsDeps{LogStore: &windowLogStore{}}
	for _, field := range []string{"commit_hash", "source_file", "nonsense"} {
		result, err := LogsAttributes(context.Background(), map[string]any{
			"action": "attributes", "field": field,
		}, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Errorf("field %q should be rejected with the supported list, got: %s", field, extractText(t, result))
		}
	}
}

func TestLogsAttributes_ServiceAndLevelComeFromScopedCounts(t *testing.T) {
	ls := &windowLogStore{
		services:    []store.ServiceLogCount{{Service: "api", Total: 5}},
		levelCounts: map[string]map[string]int{"": {"info": 3, "error": 2}},
		// The store's env-blind dictionary would leak this; it must not be used
		// for service/level.
		distinctVals: []string{"leaked-from-another-env"},
	}
	deps := LogsDeps{LogStore: ls}

	for field, want := range map[string]string{"service": "api", "level": "error"} {
		result, err := LogsAttributes(context.Background(), map[string]any{
			"action": "attributes", "field": field, "environment": "production",
		}, deps)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		resp := parseResult(t, result)
		values, _ := resp["values"].([]any)
		found := false
		for _, v := range values {
			if v == want {
				found = true
			}
			if v == "leaked-from-another-env" {
				t.Fatalf("field %q used the env-blind dictionary scan: %v", field, values)
			}
		}
		if !found {
			t.Errorf("field %q values = %v, want to contain %q", field, values, want)
		}
	}
}

func TestLogsAttributes_EnvironmentFieldAnswersFromScope(t *testing.T) {
	deps := LogsDeps{LogStore: &windowLogStore{}}
	result, err := LogsAttributes(context.Background(), map[string]any{
		"action": "attributes", "field": "environment", "environment": "production",
	}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("field=environment must not error (it used to hit \"not supported for column\"): %s", extractText(t, result))
	}
	resp := parseResult(t, result)
	values, _ := resp["values"].([]any)
	if len(values) != 1 || values[0] != "production" {
		t.Errorf("values = %v, want [production]", values)
	}
}
