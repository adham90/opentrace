package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// errAnalyticsStore returns a raw driver-style error from TrafficSummary, as the
// real SQLite store did when the request_summaries/logs tables were absent.
type errAnalyticsStore struct {
	*mocks.AnalyticsStore
}

func (e *errAnalyticsStore) TrafficSummary(_ context.Context, _ store.AnalyticsParams) (*store.TrafficSummary, error) {
	return nil, errors.New("no such table: request_summaries")
}

func TestAnalyticsTraffic_DoesNotLeakRawSQLError(t *testing.T) {
	deps := AnalyticsDeps{AnalyticsStore: &errAnalyticsStore{mocks.NewAnalyticsStore()}}

	result, err := HandleTraffic(context.Background(), deps, map[string]any{"action": "traffic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	txt := extractText(t, result)

	if strings.Contains(txt, "no such table") || strings.Contains(txt, "request_summaries") {
		t.Errorf("raw SQL error leaked to client: %s", txt)
	}
	if result.IsError {
		t.Errorf("expected graceful degradation (non-error), got error result: %s", txt)
	}
	if !strings.Contains(txt, "analytics_available") {
		t.Errorf("expected an analytics-unavailable indication, got: %s", txt)
	}
}
