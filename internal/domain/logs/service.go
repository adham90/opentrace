package logs

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/adham90/opentrace/internal/domain"
	"github.com/adham90/opentrace/pkg/store"
)

// Service provides log search, analysis, and trace correlation.
type Service struct {
	repo Repository
}

// NewService creates a log service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// SearchResult is the result of a log search.
type SearchResult struct {
	Entries []store.LogEntry `json:"entries"`
	Total   int              `json:"total"`
}

// Search finds log entries matching the given params.
func (s *Service) Search(ctx context.Context, params store.LogSearchParams) (*SearchResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log search: %w", domain.ErrNotConfigured)
	}

	params.Limit = domain.ClampLimit(params.Limit, 50, 500)

	entries, err := s.repo.Search(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("searching logs: %w", err)
	}

	return &SearchResult{
		Entries: entries,
		Total:   len(entries),
	}, nil
}

// GetByID retrieves a single log entry.
func (s *Service) GetByID(ctx context.Context, id int64) (*store.LogEntry, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log get: %w", domain.ErrNotConfigured)
	}
	entry, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("getting log %d: %w", id, err)
	}
	return entry, nil
}

// LevelCounts returns log counts grouped by level for a time window.
type LevelCounts struct {
	ByLevel    map[string]int `json:"by_level"`
	Total      int            `json:"total"`
	ErrorCount int            `json:"error_count"`
	ErrorRate  float64        `json:"error_rate_pct"`
}

// CountByLevel aggregates log counts by level.
func (s *Service) CountByLevel(ctx context.Context, params store.LogCountParams) (*LevelCounts, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log count: %w", domain.ErrNotConfigured)
	}

	counts, err := s.repo.CountByLevel(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("counting by level: %w", err)
	}

	total := 0
	errorCount := 0
	for level, count := range counts {
		total += count
		if level == "error" || level == "fatal" {
			errorCount += count
		}
	}

	var errorRate float64
	if total > 0 {
		errorRate = float64(errorCount) / float64(total) * 100
	}

	return &LevelCounts{
		ByLevel:    counts,
		Total:      total,
		ErrorCount: errorCount,
		ErrorRate:  errorRate,
	}, nil
}

// ServiceCounts returns log counts grouped by service.
func (s *Service) CountByService(ctx context.Context, params store.LogCountParams) ([]store.ServiceLogCount, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log count by service: %w", domain.ErrNotConfigured)
	}
	return s.repo.CountByService(ctx, params)
}

// TraceResult holds a distributed trace reconstructed from log entries.
type TraceResult struct {
	TraceID        string           `json:"trace_id"`
	Entries        []store.LogEntry `json:"entries"`
	TotalEntries   int              `json:"total_entries"`
	TotalDurationMs int64           `json:"total_duration_ms"`
	Services       []string         `json:"services_touched"`
	HasErrors      bool             `json:"has_errors"`
}

// Trace reconstructs a distributed trace from log entries sharing a trace ID.
func (s *Service) Trace(ctx context.Context, traceID string) (*TraceResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log trace: %w", domain.ErrNotConfigured)
	}
	if traceID == "" {
		return nil, fmt.Errorf("trace_id: %w", domain.ErrInvalidInput)
	}

	entries, err := s.repo.Search(ctx, store.LogSearchParams{
		TraceID: traceID,
		Limit:   1000,
	})
	if err != nil {
		return nil, fmt.Errorf("searching trace %s: %w", traceID, err)
	}

	// Sort by timestamp ascending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})

	servicesSet := map[string]bool{}
	hasErrors := false
	for _, e := range entries {
		if e.Service != "" {
			servicesSet[e.Service] = true
		}
		if e.Level == "error" || e.Level == "fatal" {
			hasErrors = true
		}
	}

	var services []string
	for svc := range servicesSet {
		services = append(services, svc)
	}
	sort.Strings(services)

	var durationMs int64
	if len(entries) > 1 {
		durationMs = entries[len(entries)-1].Timestamp.Sub(entries[0].Timestamp).Milliseconds()
	}

	return &TraceResult{
		TraceID:         traceID,
		Entries:         entries,
		TotalEntries:    len(entries),
		TotalDurationMs: durationMs,
		Services:        services,
		HasErrors:       hasErrors,
	}, nil
}

// Attributes returns distinct values for a log field within a time window.
func (s *Service) Attributes(ctx context.Context, field string, params store.LogCountParams) ([]string, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log attributes: %w", domain.ErrNotConfigured)
	}
	return s.repo.DistinctValues(ctx, field, params)
}

// MetadataKeys returns all unique metadata keys within a time window.
func (s *Service) MetadataKeys(ctx context.Context, params store.LogCountParams) ([]string, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log metadata keys: %w", domain.ErrNotConfigured)
	}
	return s.repo.MetadataKeys(ctx, params)
}

// Histogram returns time-bucketed log counts.
func (s *Service) Histogram(ctx context.Context, params store.LogHistogramParams) ([]store.LogHistogramBucket, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log histogram: %w", domain.ErrNotConfigured)
	}
	return s.repo.Histogram(ctx, params)
}

// PerformanceResult holds request performance analysis.
type PerformanceResult struct {
	TotalRequests    int                        `json:"total_requests"`
	AvgDurationMs    int64                      `json:"avg_duration_ms"`
	AvgSQLCount      int64                      `json:"avg_sql_count"`
	SlowestEndpoint  string                     `json:"slowest_endpoint"`
	SlowestDuration  int64                      `json:"slowest_duration_ms"`
	TopEndpoints     []store.RequestSummaryResult `json:"top_endpoints"`
	Warnings         []string                   `json:"warnings,omitempty"`
	Period           TimeWindow                 `json:"period"`
}

// TimeWindow represents a start/end time range.
type TimeWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Performance analyzes request performance for a time window.
func (s *Service) Performance(ctx context.Context, start, end time.Time, service string) (*PerformanceResult, error) {
	if s.repo == nil {
		return nil, fmt.Errorf("log performance: %w", domain.ErrNotConfigured)
	}

	params := store.RequestSummarySearchParams{
		Start: &start,
		End:   &end,
		Limit: 100,
	}
	if service != "" {
		// RequestSummarySearchParams doesn't have a Service field — use Controller as proxy.
		// This is a limitation of the current store interface.
	}

	summaries, err := s.repo.SearchRequestSummaries(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("searching summaries: %w", err)
	}

	result := &PerformanceResult{
		TotalRequests: len(summaries),
		Period:        TimeWindow{Start: start, End: end},
		TopEndpoints:  summaries,
	}

	if len(summaries) > 10 {
		result.TopEndpoints = summaries[:10]
	}

	return result, nil
}
