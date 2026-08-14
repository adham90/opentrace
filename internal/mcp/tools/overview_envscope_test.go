package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// ---------------------------------------------------------------------------
// Env-aware fakes. The shared mocks return empty slices, which would let an
// unfiltered handler pass an env-scoping test by accident. These honour the
// Environment field the way the real stores do.
// ---------------------------------------------------------------------------

type ovLogStore struct {
	*mocks.LogStore
	entries  []store.LogEntry
	requests []store.RequestSummaryResult

	mu         sync.Mutex
	lastReqLim int
}

func newOvLogStore(entries ...store.LogEntry) *ovLogStore {
	return &ovLogStore{LogStore: mocks.NewLogStore(), entries: entries}
}

func (s *ovLogStore) matches(e store.LogEntry, p store.LogSearchParams) bool {
	if p.Environment != "" && e.Environment != p.Environment {
		return false
	}
	if p.Service != "" && e.Service != p.Service {
		return false
	}
	if p.Level != "" && e.Level != p.Level {
		return false
	}
	if p.Start != nil && e.Timestamp.Before(*p.Start) {
		return false
	}
	if p.End != nil && e.Timestamp.After(*p.End) {
		return false
	}
	return true
}

func (s *ovLogStore) Search(_ context.Context, p store.LogSearchParams) ([]store.LogEntry, error) {
	var out []store.LogEntry
	for _, e := range s.entries {
		if s.matches(e, p) {
			out = append(out, e)
		}
		if p.Limit > 0 && len(out) >= p.Limit {
			break
		}
	}
	return out, nil
}

func (s *ovLogStore) CountByLevel(_ context.Context, p store.LogCountParams) (map[string]int, error) {
	out := map[string]int{}
	for _, e := range s.entries {
		if p.Environment != "" && e.Environment != p.Environment {
			continue
		}
		if p.Service != "" && e.Service != p.Service {
			continue
		}
		out[e.Level]++
	}
	return out, nil
}

// SearchRequestSummaries honours Limit (and Environment) but, like the real
// store, has no service filter — the whole point of issue 5.
func (s *ovLogStore) SearchRequestSummaries(_ context.Context, p store.RequestSummarySearchParams) ([]store.RequestSummaryResult, error) {
	s.mu.Lock()
	s.lastReqLim = p.Limit
	s.mu.Unlock()

	out := make([]store.RequestSummaryResult, 0, len(s.requests))
	for _, r := range s.requests {
		if p.Limit > 0 && len(out) >= p.Limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

type ovErrorGroups struct {
	*mocks.ErrorGroupStore
	groups []store.ErrorGroup
	events map[string][]store.ErrorGroupEvent // keyed fingerprint|env

	mu             sync.Mutex
	lastListLimit  int
	listEventCalls int
}

func newOvErrorGroups(groups ...store.ErrorGroup) *ovErrorGroups {
	return &ovErrorGroups{
		ErrorGroupStore: mocks.NewErrorGroupStore(),
		groups:          groups,
		events:          map[string][]store.ErrorGroupEvent{},
	}
}

func (s *ovErrorGroups) List(_ context.Context, p store.ListErrorGroupParams) ([]store.ErrorGroup, error) {
	s.mu.Lock()
	s.lastListLimit = p.Limit
	s.mu.Unlock()

	var out []store.ErrorGroup
	for _, g := range s.groups {
		if p.Environment != "" && g.Environment != p.Environment {
			continue
		}
		if p.Service != "" && g.Service != p.Service {
			continue
		}
		if p.Limit > 0 && len(out) >= p.Limit {
			break
		}
		out = append(out, g)
	}
	return out, nil
}

func (s *ovErrorGroups) Count(_ context.Context, _ store.ErrorGroupStatus, env string) (int, error) {
	n := 0
	for _, g := range s.groups {
		if env == "" || g.Environment == env {
			n++
		}
	}
	return n, nil
}

func (s *ovErrorGroups) ListEvents(_ context.Context, fingerprint, env string, _ int) ([]store.ErrorGroupEvent, error) {
	s.mu.Lock()
	s.listEventCalls++
	s.mu.Unlock()
	return s.events[fingerprint+"|"+env], nil
}

func (s *ovErrorGroups) eventCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listEventCalls
}

type ovWatchStore struct {
	*mocks.WatchStore
	alerts  []store.WatchAlert
	watches []store.Watch
}

func newOvWatchStore() *ovWatchStore {
	return &ovWatchStore{WatchStore: mocks.NewWatchStore()}
}

func (s *ovWatchStore) ListAlerts(_ context.Context, _ string, status string, limit int) ([]store.WatchAlert, error) {
	out := make([]store.WatchAlert, 0, len(s.alerts))
	for _, a := range s.alerts {
		if status != "" && a.Status != status {
			continue
		}
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *ovWatchStore) List(_ context.Context, p store.ListWatchParams) ([]store.Watch, error) {
	var out []store.Watch
	for _, w := range s.watches {
		if p.Service != "" && w.Service != p.Service {
			continue
		}
		if p.Environment != "" && w.Environment != p.Environment {
			continue
		}
		if p.Status != "" && w.Status != p.Status {
			continue
		}
		out = append(out, w)
	}
	return out, nil
}

type ovHealthChecks struct {
	*mocks.HealthCheckStore
	checks  []store.HealthCheck
	results map[string][]store.HealthCheckResult

	mu              sync.Mutex
	lastListLimit   int
	latestCallCount int
}

func newOvHealthChecks() *ovHealthChecks {
	return &ovHealthChecks{
		HealthCheckStore: mocks.NewHealthCheckStore(),
		results:          map[string][]store.HealthCheckResult{},
	}
}

func (s *ovHealthChecks) List(_ context.Context, p store.ListHealthCheckParams) ([]store.HealthCheck, error) {
	s.mu.Lock()
	s.lastListLimit = p.Limit
	s.mu.Unlock()

	var out []store.HealthCheck
	for _, c := range s.checks {
		if p.Environment != "" && c.Environment != p.Environment {
			continue
		}
		if p.Limit > 0 && len(out) >= p.Limit {
			break
		}
		out = append(out, c)
	}
	return out, nil
}

func (s *ovHealthChecks) LatestResults(_ context.Context, id string, _ int) ([]store.HealthCheckResult, error) {
	s.mu.Lock()
	s.latestCallCount++
	s.mu.Unlock()
	return s.results[id], nil
}

func (s *ovHealthChecks) latestCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latestCallCount
}

// ---------------------------------------------------------------------------
// Shared two-environment fixture
// ---------------------------------------------------------------------------

const (
	prodSecret    = "PRODUCTION-ONLY-MARKER"
	stagingMarker = "STAGING-MARKER"
)

var fixtureNow = time.Now().UTC()

func ovStagingScope(ctx context.Context) context.Context {
	return envscope.With(ctx, envscope.EnvScope{Allowed: []string{"staging"}})
}

func twoEnvDeps() (OverviewDeps, *ovLogStore, *ovErrorGroups, *ovWatchStore, *ovHealthChecks) {
	logs := newOvLogStore(
		store.LogEntry{ID: 1, Timestamp: fixtureNow.Add(-10 * time.Minute), Level: "error", Service: "api", Environment: "production", Message: prodSecret},
		store.LogEntry{ID: 2, Timestamp: fixtureNow.Add(-10 * time.Minute), Level: "error", Service: "api", Environment: "staging", Message: stagingMarker},
	)
	groups := newOvErrorGroups(
		store.ErrorGroup{Fingerprint: "fp-prod", Environment: "production", Service: "api", ExceptionClass: "Boom", Message: prodSecret, FirstSeenAt: fixtureNow.Add(-5 * time.Minute), LastSeenAt: fixtureNow},
		store.ErrorGroup{Fingerprint: "fp-stg", Environment: "staging", Service: "api", ExceptionClass: "Boom", Message: stagingMarker, FirstSeenAt: fixtureNow.Add(-5 * time.Minute), LastSeenAt: fixtureNow},
	)
	groups.events["fp-prod|production"] = []store.ErrorGroupEvent{
		{Fingerprint: "fp-prod", Environment: "production", Action: "resolved", Reason: prodSecret, CreatedAt: fixtureNow.Add(-time.Minute)},
	}
	groups.events["fp-stg|staging"] = []store.ErrorGroupEvent{
		{Fingerprint: "fp-stg", Environment: "staging", Action: "resolved", Reason: stagingMarker, CreatedAt: fixtureNow.Add(-time.Minute)},
	}

	watches := newOvWatchStore()
	watches.watches = []store.Watch{
		{ID: "w-prod", Service: "api", Environment: "production"},
		{ID: "w-stg", Service: "api", Environment: "staging"},
		{ID: "w-other", Service: "billing", Environment: "staging"},
	}
	watches.alerts = []store.WatchAlert{
		{ID: "a-prod", WatchID: "w-prod", Environment: "production", Status: "pending", Summary: prodSecret, CreatedAt: fixtureNow.Add(-2 * time.Minute)},
		{ID: "a-stg", WatchID: "w-stg", Environment: "staging", Status: "pending", Summary: stagingMarker, CreatedAt: fixtureNow.Add(-2 * time.Minute)},
		{ID: "a-other", WatchID: "w-other", Environment: "staging", Status: "pending", Summary: "billing alert", CreatedAt: fixtureNow.Add(-2 * time.Minute)},
	}

	checks := newOvHealthChecks()
	checks.checks = []store.HealthCheck{
		{ID: "hc-prod", Name: prodSecret, URL: "https://prod", Environment: "production"},
		{ID: "hc-stg", Name: stagingMarker, URL: "https://stg", Environment: "staging"},
	}
	for _, id := range []string{"hc-prod", "hc-stg"} {
		checks.results[id] = []store.HealthCheckResult{
			{HealthCheckID: id, Status: store.HealthCheckDown, CheckedAt: fixtureNow.Add(-3 * time.Minute)},
			{HealthCheckID: id, Status: store.HealthCheckUp, CheckedAt: fixtureNow.Add(-30 * time.Minute)},
		}
	}

	d := OverviewDeps{
		LogStore:         logs,
		ErrorGroupStore:  groups,
		WatchStore:       watches,
		HealthCheckStore: checks,
		SettingsStore:    mocks.NewSettingsStore(),
		AgentNoteStore:   mocks.NewAgentNoteStore(),
		DSStore:          mocks.NewDataSourceStore(),
		ServerStore:      mocks.NewServerStore(),
	}
	return d, logs, groups, watches, checks
}

func mustText(t *testing.T, call func() (*CallToolResult, error)) string {
	t.Helper()
	result, err := call()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", extractText(t, result))
	}
	return extractText(t, result)
}

func assertScoped(t *testing.T, action, text string) {
	t.Helper()
	if strings.Contains(text, prodSecret) {
		t.Errorf("%s leaked production data to a staging-scoped caller:\n%s", action, text)
	}
	if !strings.Contains(text, stagingMarker) {
		t.Errorf("%s returned no staging data — the test fixture would pass vacuously:\n%s", action, text)
	}
}

// ---------------------------------------------------------------------------
// Issue 2 / 4 / 7: env scope on timeline, changes, investigate
// ---------------------------------------------------------------------------

func TestTimeline_StagingScopeSeesNoProduction(t *testing.T) {
	d, _, _, _, _ := twoEnvDeps()
	args := map[string]any{
		"start": fixtureNow.Add(-time.Hour).Format(time.RFC3339),
		"end":   fixtureNow.Add(time.Minute).Format(time.RFC3339),
	}
	ctx := ovStagingScope(context.Background())
	text := mustText(t, func() (*CallToolResult, error) { return HandleTimeline(ctx, d, args) })
	assertScoped(t, "timeline", text)
}

func TestChanges_StagingScopeSeesNoProduction(t *testing.T) {
	d, _, _, _, _ := twoEnvDeps()
	ctx := ovStagingScope(context.Background())
	text := mustText(t, func() (*CallToolResult, error) { return HandleChanges(ctx, d, map[string]any{"since": "1h"}) })
	assertScoped(t, "changes", text)
}

func TestInvestigate_StagingScopeSeesNoProduction(t *testing.T) {
	d, _, _, _, _ := twoEnvDeps()
	args := map[string]any{"service": "api", "since": "1h"}
	ctx := ovStagingScope(context.Background())
	text := mustText(t, func() (*CallToolResult, error) { return HandleOverviewInvestigate(ctx, d, args) })
	assertScoped(t, "investigate", text)
}

func TestTimeline_RejectsOutOfScopeEnvArg(t *testing.T) {
	d, _, _, _, _ := twoEnvDeps()
	args := map[string]any{
		"start":       fixtureNow.Add(-time.Hour).Format(time.RFC3339),
		"end":         fixtureNow.Format(time.RFC3339),
		"environment": "production",
	}
	result, err := HandleTimeline(ovStagingScope(context.Background()), d, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an authorization error, got: %s", extractText(t, result))
	}
}

// Issue 7 specifically: the per-service report must not count another
// service's alerts.
func TestInvestigate_AlertsAreScopedToService(t *testing.T) {
	d, _, _, _, _ := twoEnvDeps()
	args := map[string]any{"service": "api", "since": "1h"}
	ctx := ovStagingScope(context.Background())
	text := mustText(t, func() (*CallToolResult, error) { return HandleOverviewInvestigate(ctx, d, args) })
	if strings.Contains(text, "billing alert") {
		t.Errorf("investigate(service=api) included another service's alert:\n%s", text)
	}
	if !strings.Contains(text, "1 active alerts") {
		t.Errorf("expected the summary to count only api's single staging alert:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// Issue 1: the ingest API key must never reach a tool response
// ---------------------------------------------------------------------------

func TestOverview_NeverReturnsAPIKey(t *testing.T) {
	const key = "ot_live_supersecret_ingest_key"

	d, _, _, _, _ := twoEnvDeps()
	settings := mocks.NewSettingsStore()
	settings.APIKey = key
	d.SettingsStore = settings

	handler := OverviewHandler(d)
	actions := []map[string]any{
		{"action": "settings"},
		{"action": "status"},
		{"action": "triage"},
		{"action": "diagnose"},
		{"action": "changes"},
		{"action": "notes"},
		{"action": "investigate", "service": "api"},
		{"action": "timeline",
			"start": fixtureNow.Add(-time.Hour).Format(time.RFC3339),
			"end":   fixtureNow.Format(time.RFC3339)},
	}

	for _, args := range actions {
		action := args["action"].(string)
		result, err := handler(ovStagingScope(context.Background()), MakeCallToolRequest("overview", args))
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", action, err)
		}
		text := extractText(t, result)
		if strings.Contains(text, key) {
			t.Errorf("overview action %q leaked the ingest API key:\n%s", action, text)
		}
		if strings.Contains(text, "api_key") {
			t.Errorf("overview action %q returned an api_key field:\n%s", action, text)
		}
	}
}

// ---------------------------------------------------------------------------
// Issue 6: bounded, batched fan-out
// ---------------------------------------------------------------------------

func TestTimeline_BoundsFanOut(t *testing.T) {
	groups := newOvErrorGroups()
	for i := range 100 {
		groups.groups = append(groups.groups, store.ErrorGroup{
			Fingerprint: fmt.Sprintf("fp-%d", i),
			Environment: "staging",
			Service:     "api",
			LastSeenAt:  fixtureNow,
		})
	}
	checks := newOvHealthChecks()
	for i := range 100 {
		id := fmt.Sprintf("hc-%d", i)
		checks.checks = append(checks.checks, store.HealthCheck{ID: id, Name: id, Environment: "staging"})
	}

	d := OverviewDeps{
		LogStore:         newOvLogStore(),
		ErrorGroupStore:  groups,
		WatchStore:       newOvWatchStore(),
		HealthCheckStore: checks,
	}
	args := map[string]any{
		"start": fixtureNow.Add(-time.Hour).Format(time.RFC3339),
		"end":   fixtureNow.Format(time.RFC3339),
	}
	ctx := ovStagingScope(context.Background())
	text := mustText(t, func() (*CallToolResult, error) { return HandleTimeline(ctx, d, args) })

	if got := groups.eventCalls(); got > maxTimelineErrorGroups {
		t.Errorf("ListEvents called %d times, want at most %d", got, maxTimelineErrorGroups)
	}
	if got := checks.latestCalls(); got > maxTimelineHealthChecks {
		t.Errorf("LatestResults called %d times, want at most %d", got, maxTimelineHealthChecks)
	}
	if checks.lastListLimit != maxTimelineHealthChecks {
		t.Errorf("healthcheck List limit = %d, want %d (it used to be unbounded)", checks.lastListLimit, maxTimelineHealthChecks)
	}
	// A capped scan must say so rather than reading as exhaustive.
	if !strings.Contains(text, "coverage_notes") {
		t.Errorf("expected coverage_notes when the scan was capped:\n%s", text)
	}
}

// ---------------------------------------------------------------------------
// Issue 5: service filter must be applied before the row limit
// ---------------------------------------------------------------------------

func TestCollectPerformance_FindsServiceRowsBehindSlowerOnes(t *testing.T) {
	logs := newOvLogStore()
	// The ten slowest requests all belong to other services; checkout's rows
	// sit behind them. A Limit:5 query followed by a Go-side filter returned
	// nothing here.
	for i := range 10 {
		logs.requests = append(logs.requests, store.RequestSummaryResult{Service: "other", Timestamp: fixtureNow.Add(-time.Duration(i) * time.Minute)})
	}
	for range 3 {
		logs.requests = append(logs.requests, store.RequestSummaryResult{Service: "checkout", Timestamp: fixtureNow})
	}

	d := OverviewDeps{LogStore: logs}
	out := collectPerformance(context.Background(), d, "checkout", "staging", fixtureNow.Add(-time.Hour), fixtureNow)

	if got := out["total_requests"]; got != 3 {
		t.Errorf("total_requests = %v, want 3", got)
	}
	if logs.lastReqLim != perfScanLimit {
		t.Errorf("scan limit = %d, want %d for a service-scoped call", logs.lastReqLim, perfScanLimit)
	}
}

func TestCollectPerformance_UnscopedStillLimitsToFive(t *testing.T) {
	logs := newOvLogStore()
	for range 10 {
		logs.requests = append(logs.requests, store.RequestSummaryResult{Service: "other"})
	}
	d := OverviewDeps{LogStore: logs}
	out := collectPerformance(context.Background(), d, "", "", fixtureNow.Add(-time.Hour), fixtureNow)
	if got := out["total_requests"]; got != perfSlowestReported {
		t.Errorf("total_requests = %v, want %d", got, perfSlowestReported)
	}
}
