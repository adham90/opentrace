// Package adapter provides a bridge between the new segmented log store engine
// and the existing pkg/store.LogStore interface. This allows all existing MCP tools,
// watchers, and domain services to work unchanged with the new storage engine.
package adapter

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/adham90/opentrace/internal/logstore/chunk"
	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/pkg/store"
)

// LogStore wraps engine.Store and implements pkg/store.LogStore.
type LogStore struct {
	engine *engine.Store
}

// New creates a LogStore adapter around an engine.Store.
func New(e *engine.Store) *LogStore {
	return &LogStore{engine: e}
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
		EventType:        params.EventType,
		ErrorClass:   params.ExceptionClass,
		ErrorFingerprint: params.ErrorFingerprint,
		Start:            params.Start,
		End:              params.End,
		Limit:            params.Limit,
		Offset:           params.Offset,
		SortAsc:          params.SortAsc,
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
		return nil, err
	}
	old := newToOld(*entry)
	return &old, nil
}

func (a *LogStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	deleted, err := a.engine.Prune(olderThan)
	return int64(deleted), err
}

func (a *LogStore) CountByLevel(ctx context.Context, params store.LogCountParams) (map[string]int, error) {
	counts, err := a.engine.CountByLevel(params.Since, params.Until, params.Service)
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
	counts, err := a.engine.CountByService(params.Since, params.Until)
	if err != nil {
		return nil, err
	}

	result := make([]store.ServiceLogCount, 0, len(counts))
	for svc, total := range counts {
		result = append(result, store.ServiceLogCount{
			Service: svc,
			Total:   total,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Total > result[j].Total })
	return result, nil
}

func (a *LogStore) Histogram(ctx context.Context, params store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	buckets, err := a.engine.Histogram(params.Since, params.Until, params.Interval)
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
	return a.engine.DistinctValues(field, params.Since, params.Until)
}

func (a *LogStore) MetadataKeys(ctx context.Context, params store.LogCountParams) ([]string, error) {
	// Metadata is now inside the opaque body. Return empty list.
	return nil, nil
}

// --- Request performance (adapted from sparse column queries) ---

func (a *LogStore) SearchRequestSummaries(ctx context.Context, params store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	// Search for entries with duration_ms > 0 (i.e., HTTP request events)
	sp := engine.SearchParams{
		EventType: "http.request",
		Start:     params.Start,
		End:       params.End,
		Limit:     params.Limit,
	}
	if sp.Limit <= 0 {
		sp.Limit = 20
	}
	if params.Path != "" {
		sp.Path = params.Path
	}

	result, err := a.engine.Search(sp)
	if err != nil {
		return nil, err
	}

	var summaries []store.RequestSummaryResult
	for _, e := range result.Entries {
		if e.DurationMs <= 0 {
			continue
		}

		if params.Controller != "" && e.Handler != params.Controller {
			continue
		}
		if params.NPlusOneOnly && (e.NPlusOne == nil || !*e.NPlusOne) {
			continue
		}
		if params.MinDurationMs > 0 && float64(e.DurationMs) < params.MinDurationMs {
			continue
		}
		if params.MinSQLCount > 0 && e.DbCount < params.MinSQLCount {
			continue
		}

		npo := false
		if e.NPlusOne != nil {
			npo = *e.NPlusOne
		}

		summaries = append(summaries, store.RequestSummaryResult{
			RequestSummary: store.RequestSummary{
				LogID:            e.ID,
				Controller:       e.Handler,
				Method:           e.Method,
				Path:             e.Path,
				Status:           e.Status,
				DurationMs:       float64(e.DurationMs),
				DBTimeMs:         float64(e.DbMs),
				SQLCount:         e.DbCount,
				NPlusOne:         npo,
				DuplicateQueries: e.DupQueries,
			},
			Timestamp: time.UnixMilli(e.Ts),
			Service:   e.Service,
			TraceID:   e.TraceID,
		})
	}

	// Sort by the requested field
	switch params.SortBy {
	case "sql_count":
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].SQLCount > summaries[j].SQLCount })
	case "db_time_ms":
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].DBTimeMs > summaries[j].DBTimeMs })
	case "duplicate_queries":
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].DuplicateQueries > summaries[j].DuplicateQueries })
	default: // duration_ms
		sort.Slice(summaries, func(i, j int) bool { return summaries[i].DurationMs > summaries[j].DurationMs })
	}

	if params.Limit > 0 && len(summaries) > params.Limit {
		summaries = summaries[:params.Limit]
	}

	return summaries, nil
}

func (a *LogStore) AggregateRequestSummaries(ctx context.Context, params store.RequestSummaryAggregateParams) (*store.RequestSummaryAggregates, error) {
	sp := engine.SearchParams{
		EventType: "http.request",
		Start:     params.Start,
		End:       params.End,
		Service:   params.Service,
		Limit:     500,
	}
	if params.Endpoint != "" {
		sp.Path = params.Endpoint
	}

	result, err := a.engine.Search(sp)
	if err != nil {
		return nil, err
	}

	agg := &store.RequestSummaryAggregates{}
	var totalDuration float64
	var totalSQLCount float64
	for _, e := range result.Entries {
		if e.DurationMs <= 0 {
			continue
		}
		agg.Count++
		totalDuration += float64(e.DurationMs)
		totalSQLCount += float64(e.DbCount)
	}

	if agg.Count > 0 {
		agg.AvgDuration = totalDuration / float64(agg.Count)
		agg.AvgSQLCount = totalSQLCount / float64(agg.Count)
	}

	return agg, nil
}

// --- Batch deduplication (no-ops — WAL is crash-safe) ---

func (a *LogStore) RecordBatch(ctx context.Context, batchID string, logCount int) error {
	return nil // WAL is crash-safe, no batch tracking needed
}

func (a *LogStore) GetBatch(ctx context.Context, batchID string) (*store.BatchRecord, error) {
	return nil, nil // always "not found" — no dedup tracking
}

func (a *LogStore) PruneBatches(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil // no-op
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
	}

	// Convert metadata to body if present
	if len(e.Metadata) > 0 {
		if body, err := json.Marshal(map[string]any{"metadata": e.Metadata}); err == nil {
			ne.Body = body
		}
	}

	// Convert request summary to flat fields if present
	if e.RequestSummary != nil {
		rs := e.RequestSummary
		ne.EventType = "http.request"
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
		ne.SlowQueries = 0 // not tracked in old schema
		ne.DupQueries = rs.DuplicateQueries
	}

	// Handle MetadataJSON carrier field (pre-marshaled metadata)
	if ne.Body == nil && e.MetadataJSON != "" {
		ne.Body = json.RawMessage(e.MetadataJSON)
	}

	return ne
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
		npo := false
		if e.NPlusOne != nil {
			npo = *e.NPlusOne
		}
		old.RequestSummary = &store.RequestSummary{
			LogID:            e.ID,
			Controller:       e.Handler,
			Method:           e.Method,
			Path:             e.Path,
			Status:           e.Status,
			DurationMs:       float64(e.DurationMs),
			DBTimeMs:         float64(e.DbMs),
			SQLCount:         e.DbCount,
			NPlusOne:         npo,
			DuplicateQueries: e.DupQueries,
		}
	}

	return old
}

// Compile-time interface check
var _ store.LogStore = (*LogStore)(nil)
