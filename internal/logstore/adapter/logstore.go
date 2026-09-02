// Package adapter provides a bridge between the new segmented log store engine
// and the existing pkg/store.LogStore interface. This allows all existing MCP tools,
// watchers, and domain services to work unchanged with the new storage engine.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/pkg/store"
)

// maxTrackedBatches bounds the in-memory idempotency cache.
const maxTrackedBatches = 20000

// errLookupFailed is the user-facing error for an internal read failure. The
// underlying error (which can embed on-disk paths) is logged, never returned.
var errLookupFailed = errors.New("log entry lookup failed")

// LogStore wraps engine.Store and implements pkg/store.LogStore.
type LogStore struct {
	engine *engine.Store

	// Batch idempotency cache. ponytail: in-memory recent-batch set, per-process
	// FIFO-evicted at maxTrackedBatches — dedups SDK retries within the window
	// (the realistic "retry after timeout" case); does not persist across
	// restarts. A persistent table would be needed for cross-restart dedup.
	batchMu    sync.Mutex
	batchSeen  map[string]store.BatchRecord
	batchOrder []string
}

// New creates a LogStore adapter around an engine.Store.
func New(e *engine.Store) *LogStore {
	return &LogStore{
		engine:    e,
		batchSeen: make(map[string]store.BatchRecord),
	}
}

// Engine returns the underlying engine (for direct access when needed).
func (a *LogStore) Engine() *engine.Store {
	return a.engine
}

// --- Write operations ---

func (a *LogStore) BatchInsert(ctx context.Context, entries []store.LogEntry) (int, error) {
	if len(entries) == 0 {
		return 0, nil
	}

	converted := make([]chunk.Entry, len(entries))
	for i, e := range entries {
		converted[i] = oldToNew(e)
	}

	result, err := a.engine.Ingest(converted)
	if err != nil {
		return 0, err
	}

	// Write the engine-assigned IDs back into the caller's slice. Post-insert
	// processing runs on this same slice, and without this it saw ID 0 on every
	// entry, so last_log_id linkage was never populated. The pipeline can drop
	// or expand entries, so only a 1:1 result is safe to map positionally.
	if len(result) == len(entries) {
		for i := range entries {
			entries[i].ID = result[i].ID
		}
	}
	return len(result), nil
}

// --- Read operations ---

func (a *LogStore) Search(ctx context.Context, params store.LogSearchParams) ([]store.LogEntry, error) {
	sp := engine.SearchParams{
		Query:            params.Query,
		Service:          params.Service,
		Level:            params.Level,
		Env:              params.Environment,
		TraceID:          params.TraceID,
		RequestID:        params.RequestID,
		UserID:           params.UserID,
		TenantID:         params.TenantID,
		EventType:        params.EventType,
		ErrorClass:       params.ExceptionClass,
		ErrorFingerprint: params.ErrorFingerprint,
		SourceFile:       params.SourceFile,
		CommitHash:       params.CommitHash,
		Start:            params.Start,
		End:              params.End,
		SinceID:          params.SinceID,
		Limit:            params.Limit,
		Offset:           params.Offset,
		SortAsc:          params.SortAsc,
		// Both of these used to be dropped between the tool boundary and the
		// engine: the MCP metadata_filter argument was parsed and ignored, so a
		// narrowed search returned every row and read as the answer.
		MetadataFilter: params.MetadataFilter,
		Exclude:        params.Exclude,
		OmitBody:       params.OmitBody,
	}

	result, err := a.engine.Search(sp)
	if err != nil {
		return nil, err
	}

	entries := make([]store.LogEntry, len(result.Entries))
	for i, e := range result.Entries {
		entries[i] = newToOld(e)
	}
	return entries, nil
}

func (a *LogStore) GetByID(ctx context.Context, id int64) (*store.LogEntry, error) {
	entry, err := a.engine.GetByID(id)
	if err != nil {
		// Callers must be able to tell a 404 from an I/O failure, and engine
		// errors wrap *os.PathError — leaking data-directory paths into MCP
		// tool output. Map the sentinel, and keep the detail in the log.
		if errors.Is(err, engine.ErrEntryNotFound) {
			return nil, store.ErrNotFound
		}
		slog.Error("logstore: get by id failed", "error", err, "id", id)
		return nil, errLookupFailed
	}
	old := newToOld(*entry)
	return &old, nil
}

func (a *LogStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	deleted, err := a.engine.Prune(olderThan)
	return int64(deleted), err
}

func (a *LogStore) CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error) {
	counts, err := a.engine.CountByLevel(params.Since, params.Until, params.Service, params.Environment)
	if err != nil {
		return nil, err
	}
	// If a level filter is specified, return only that level's count
	if params.Level != "" {
		filtered := make(map[string]int)
		for level, count := range counts {
			if strings.EqualFold(level, params.Level) {
				filtered[level] = count
			}
		}
		return filtered, nil
	}
	return counts, nil
}

func (a *LogStore) CountByService(ctx context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error) {
	counts, err := a.engine.CountByServiceDetailed(params.Since, params.Until, params.Environment)
	if err != nil {
		return nil, err
	}

	// ErrorCount was hardcoded to zero, which made the "compare errors" tooling
	// report "0 errors, unchanged" in the middle of an incident.
	result := make([]store.ServiceLogCount, 0, len(counts))
	for svc, c := range counts {
		result = append(result, store.ServiceLogCount{
			Service:    svc,
			Total:      c.Total,
			ErrorCount: c.Errors,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Total > result[j].Total })
	return result, nil
}

func (a *LogStore) Histogram(ctx context.Context, params store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	// Service/Level/Environment are real filters here (the CLI stats endpoint
	// passes ?service=): dropping them returned every service's volume under
	// the caller's label.
	buckets, err := a.engine.HistogramFiltered(params.Since, params.Until, params.Interval, engine.HistogramFilter{
		Service:     params.Service,
		Level:       params.Level,
		Environment: params.Environment,
	})
	if err != nil {
		return nil, err
	}

	result := make([]store.LogHistogramBucket, len(buckets))
	for i, b := range buckets {
		result[i] = store.LogHistogramBucket{
			Timestamp:  b.Timestamp,
			Total:      b.Total,
			ErrorCount: b.Errors,
		}
	}
	return result, nil
}

func (a *LogStore) DistinctValues(ctx context.Context, field string, params store.LogCountParams) ([]string, error) {
	// Service and Environment scope the distinct set; without them a watcher's
	// COUNT DISTINCT was computed across every service and environment.
	return a.engine.DistinctValues(field, params.Since, params.Until, params.Service, params.Level, params.Environment)
}

func (a *LogStore) MetadataKeys(ctx context.Context, params store.LogCountParams) ([]string, error) {
	// Metadata is now inside the opaque body. Return empty list.
	return nil, nil
}

// --- Request performance (adapted from sparse column queries) ---

func (a *LogStore) SearchRequestSummaries(ctx context.Context, params store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	// Every filter is pushed into the engine and the whole window is ordered
	// before the limit is applied. Previously the engine truncated to the newest
	// Limit rows first, so "slowest endpoints" meant "most recent requests,
	// reordered" and n_plus_one_only reported nothing while hundreds existed
	// earlier in the window. Environment is an authorization scope here — a
	// staging-scoped token was receiving production paths, controllers and
	// latencies.
	sp := engine.SearchParams{
		RequestsOnly:  true,
		Start:         params.Start,
		End:           params.End,
		Env:           params.Environment,
		Path:          params.Path,
		Handler:       params.Controller,
		NPlusOneOnly:  params.NPlusOneOnly,
		MinDurationMs: int(params.MinDurationMs),
		MinSQLCount:   params.MinSQLCount,
		// Rows without a duration can't be rendered as a request summary. The
		// predicate belongs in the engine, before the limit: filtering them out
		// here returned short pages while more qualifying rows existed.
		PositiveDurationOnly: true,
	}

	limit := params.Limit
	if limit <= 0 {
		limit = defaultRequestLimit
	}

	entries, err := a.engine.SearchRequests(sp, params.SortBy, limit, params.Offset)
	if err != nil {
		return nil, err
	}

	summaries := make([]store.RequestSummaryResult, 0, len(entries))
	for _, e := range entries {
		summaries = append(summaries, store.RequestSummaryResult{
			RequestSummary: *requestSummaryFromEntry(e),
			Timestamp:      time.UnixMilli(e.Ts),
			Service:        e.Service,
			TraceID:        e.TraceID,
		})
	}

	return summaries, nil
}

// defaultRequestLimit is the page size used when a caller doesn't set one.
const defaultRequestLimit = 20

func (a *LogStore) AggregateRequestSummaries(ctx context.Context, params store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	// Aggregate over the whole range, not the newest 500 rows presented as if
	// they were the range, and honour the environment scope.
	sp := engine.SearchParams{
		RequestsOnly: true,
		Start:        params.Start,
		End:          params.End,
		Service:      params.Service,
		Env:          params.Environment,
		Path:         params.Endpoint,
	}

	res, err := a.engine.AggregateRequests(sp)
	if err != nil {
		return nil, err
	}

	agg := &store.RequestSummaryAggregates{
		Count:      res.Count,
		TotalReads: res.CacheReads,
		TotalHits:  res.CacheHits,
	}
	if res.Count > 0 {
		agg.AvgDuration = res.TotalDurationMs / float64(res.Count)
		agg.AvgSQLCount = res.TotalSQLCount / float64(res.Count)
	}
	// Cache aggregates were never populated, so every cache-hit-rate baseline
	// and watch computed 0 forever.
	if res.CacheReads > 0 {
		agg.CacheHitRate = float64(res.CacheHits) / float64(res.CacheReads)
	}

	return agg, nil
}

// --- Batch deduplication (in-memory, bounded) ---

func (a *LogStore) RecordBatch(ctx context.Context, batchID string, logCount int) error {
	if batchID == "" {
		return nil
	}
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	if _, exists := a.batchSeen[batchID]; exists {
		return nil
	}
	a.batchSeen[batchID] = store.BatchRecord{BatchID: batchID, LogCount: logCount, ReceivedAt: time.Now()}
	a.batchOrder = append(a.batchOrder, batchID)
	// FIFO-evict oldest entries once over the cap.
	for len(a.batchOrder) > maxTrackedBatches {
		oldest := a.batchOrder[0]
		a.batchOrder = a.batchOrder[1:]
		delete(a.batchSeen, oldest)
	}
	return nil
}

func (a *LogStore) GetBatch(ctx context.Context, batchID string) (*store.BatchRecord, error) {
	if batchID == "" {
		return nil, nil
	}
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	if rec, ok := a.batchSeen[batchID]; ok {
		r := rec
		return &r, nil
	}
	return nil, nil
}

func (a *LogStore) PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error) {
	a.batchMu.Lock()
	defer a.batchMu.Unlock()
	cutoff := time.Now().Add(-olderThan)
	var pruned int64
	kept := a.batchOrder[:0:0]
	for _, id := range a.batchOrder {
		if rec, ok := a.batchSeen[id]; ok {
			if rec.ReceivedAt.Before(cutoff) {
				delete(a.batchSeen, id)
				pruned++
				continue
			}
			kept = append(kept, id)
		}
	}
	a.batchOrder = kept
	return pruned, nil
}

// --- Type conversion helpers ---

func oldToNew(e store.LogEntry) chunk.Entry {
	ne := chunk.Entry{
		Ts:               e.Timestamp.UnixMilli(),
		Level:            e.Level,
		Service:          e.Service,
		Env:              e.Environment,
		Version:          e.CommitHash,
		Message:          e.Message,
		EventType:        e.EventType,
		TraceID:          e.TraceID,
		SpanID:           e.SpanID,
		ParentSpanID:     e.ParentSpanID,
		RequestID:        e.RequestID,
		UserID:           e.UserID,
		ErrorClass:       e.ExceptionClass,
		SourceFile:       e.SourceFile,
		SourceLine:       e.SourceLine,
		ErrorFingerprint: e.ErrorFingerprint,

		// Flat-SDK fields. The columnar schema has had a column for each of
		// these all along; leaving them unmapped wrote zeros/empties to disk
		// for values the ingest path had already parsed off the wire.
		Host:         e.Host,
		Kind:         e.Kind,
		TenantID:     e.TenantID,
		SessionID:    e.SessionID,
		Route:        e.Route,
		CacheMs:      e.CacheMs,
		CacheHits:    e.CacheHits,
		CacheMisses:  e.CacheMisses,
		ExtMs:        e.ExtMs,
		ExtCount:     e.ExtCount,
		RenderMs:     e.RenderMs,
		AllocCount:   e.AllocCount,
		MemDeltaMb:   e.MemDeltaMb,
		SlowQueries:  e.SlowQueries,
		ErrorMessage: e.ErrorMessage,
		// Timing for non-request rows (jobs, events). A RequestSummary, when
		// present, overwrites these below — request rows stay authoritative
		// through that path.
		DurationMs: int(e.DurationMs),
		DbMs:       int(e.DbMs),
		DbCount:    e.DbCount,
		Status:     e.Status,
		JobClass:   e.JobClass,
		JobQueue:   e.JobQueue,
		JobID:      e.JobID,
		QueueMs:    e.QueueMs,
	}

	// Prefer the original wire JSON. Flat ingest carries both a parsed map for
	// post-processing and this raw form for storage; marshaling the map first
	// both wasted work and changed {"key":...} into
	// {"metadata":{"key":...}}.
	if e.MetadataJSON != "" {
		ne.Body = json.RawMessage(e.MetadataJSON)
	} else if len(e.Metadata) > 0 {
		if body, err := json.Marshal(map[string]any{"metadata": e.Metadata}); err == nil {
			ne.Body = body
		}
	}

	// Convert request summary to flat fields if present
	if e.RequestSummary != nil {
		rs := e.RequestSummary
		// Keep an SDK-supplied event_type on a request row rather than
		// overwriting it.
		if ne.EventType == "" {
			ne.EventType = "http.request"
		}
		if rs.Action != "" {
			ne.Handler = rs.Controller + "#" + rs.Action
		} else {
			ne.Handler = rs.Controller
		}
		ne.Method = rs.Method
		ne.Path = rs.Path
		ne.Status = rs.Status
		ne.DurationMs = int(rs.DurationMs)
		ne.DbMs = int(rs.DBTimeMs)
		ne.DbCount = rs.SQLCount
		ne.NPlusOne = &rs.NPlusOne
		ne.DupQueries = rs.DuplicateQueries

		// Cache/view/memory metrics have dedicated columns; discarding them
		// meant every cache-hit-rate baseline was computed from zeros.
		setIfZero(&ne.CacheHits, rs.CacheHits)
		setIfZero(&ne.CacheMisses, rs.CacheReads-rs.CacheHits)
		setIfZero(&ne.RenderMs, int(rs.ViewTimeMs))
		setIfZero(&ne.ExtMs, int(rs.HTTPExternalTotalMs))
		setIfZero(&ne.ExtCount, rs.HTTPExternalCount)
		setIfZero(&ne.MemDeltaMb, int(rs.MemoryDeltaMb*memDeltaScale))
	}

	return ne
}

// memDeltaScale is the fixed-point factor the mem_delta_mb column uses
// (stored as value * 100, i.e. two decimal places).
const memDeltaScale = 100

// setIfZero fills a destination that the flat-field mapping left at zero,
// so an explicit flat value always wins over a RequestSummary-derived one.
func setIfZero(dst *int, v int) {
	if *dst == 0 && v > 0 {
		*dst = v
	}
}

// requestSummaryFromEntry rebuilds a RequestSummary from the columnar entry,
// including the cache/view/external/memory metrics that used to be dropped in
// both directions.
func requestSummaryFromEntry(e chunk.Entry) *store.RequestSummary {
	npo := false
	if e.NPlusOne != nil {
		npo = *e.NPlusOne
	}
	return &store.RequestSummary{
		LogID:               e.ID,
		Controller:          e.Handler,
		Method:              e.Method,
		Path:                e.Path,
		Status:              e.Status,
		DurationMs:          float64(e.DurationMs),
		DBTimeMs:            float64(e.DbMs),
		SQLCount:            e.DbCount,
		NPlusOne:            npo,
		DuplicateQueries:    e.DupQueries,
		ViewTimeMs:          float64(e.RenderMs),
		CacheHits:           e.CacheHits,
		CacheReads:          e.CacheHits + e.CacheMisses,
		HTTPExternalCount:   e.ExtCount,
		HTTPExternalTotalMs: float64(e.ExtMs),
		MemoryDeltaMb:       float64(e.MemDeltaMb) / memDeltaScale,
	}
}

func newToOld(e chunk.Entry) store.LogEntry {
	old := store.LogEntry{
		ID:               e.ID,
		Timestamp:        time.UnixMilli(e.Ts).UTC(),
		Level:            e.Level,
		Service:          e.Service,
		Environment:      e.Env,
		CommitHash:       e.Version,
		Message:          e.Message,
		EventType:        e.EventType,
		TraceID:          e.TraceID,
		SpanID:           e.SpanID,
		ParentSpanID:     e.ParentSpanID,
		RequestID:        e.RequestID,
		UserID:           e.UserID,
		ExceptionClass:   e.ErrorClass,
		SourceFile:       e.SourceFile,
		SourceLine:       e.SourceLine,
		ErrorFingerprint: e.ErrorFingerprint,
		CreatedAt:        time.UnixMilli(e.ReceivedAt).UTC(),

		Host:         e.Host,
		Kind:         e.Kind,
		TenantID:     e.TenantID,
		SessionID:    e.SessionID,
		Route:        e.Route,
		CacheMs:      e.CacheMs,
		CacheHits:    e.CacheHits,
		CacheMisses:  e.CacheMisses,
		ExtMs:        e.ExtMs,
		ExtCount:     e.ExtCount,
		RenderMs:     e.RenderMs,
		AllocCount:   e.AllocCount,
		MemDeltaMb:   e.MemDeltaMb,
		SlowQueries:  e.SlowQueries,
		ErrorMessage: e.ErrorMessage,
		JobClass:     e.JobClass,
		JobQueue:     e.JobQueue,
		JobID:        e.JobID,
		QueueMs:      e.QueueMs,
		// Restore non-request timing. Request rows also get a RequestSummary
		// built below from the same columns, so both views agree.
		DurationMs: float64(e.DurationMs),
		DbMs:       float64(e.DbMs),
		DbCount:    e.DbCount,
		Status:     e.Status,
	}

	// Convert body to metadata for backward compat
	if len(e.Body) > 0 {
		var body map[string]any
		if err := json.Unmarshal(e.Body, &body); err == nil {
			if meta, ok := body["metadata"].(map[string]any); ok {
				old.Metadata = meta
			}
		}
	}

	// Convert flat performance fields to RequestSummary
	if e.DurationMs > 0 {
		old.RequestSummary = requestSummaryFromEntry(e)
	}

	return old
}

// Compile-time interface check
var _ store.LogStore = (*LogStore)(nil)
