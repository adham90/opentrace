package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/mcp/envscope"
	"github.com/adham90/opentrace/internal/testutil/mocks"
	"github.com/adham90/opentrace/pkg/store"
)

// envLogStore is a store.LogStore that actually honours params.Environment (the
// real segmented store does; the shared mock does not). It lets these tests
// prove a scoped token only sees its own environment's data.
type envLogStore struct {
	*mocks.LogStore
}

func newEnvLogStore(entries ...store.LogEntry) *envLogStore {
	m := mocks.NewLogStore()
	m.Entries = entries
	return &envLogStore{LogStore: m}
}

func (s *envLogStore) match(e store.LogEntry, env, service, level, traceID, fingerprint string) bool {
	if env != "" && e.Environment != env {
		return false
	}
	if service != "" && e.Service != service {
		return false
	}
	if level != "" && e.Level != level {
		return false
	}
	if traceID != "" && e.TraceID != traceID {
		return false
	}
	if fingerprint != "" && e.ErrorFingerprint != fingerprint {
		return false
	}
	return true
}

func (s *envLogStore) Search(_ context.Context, p store.LogSearchParams) ([]store.LogEntry, error) {
	s.LastSearchParams = p
	var out []store.LogEntry
	for _, e := range s.Entries {
		if s.match(e, p.Environment, p.Service, p.Level, p.TraceID, p.ErrorFingerprint) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *envLogStore) CountByLevel(_ context.Context, p store.LogCountParams) (map[string]int, error) {
	m := map[string]int{}
	for _, e := range s.Entries {
		if s.match(e, p.Environment, p.Service, p.Level, "", "") {
			m[e.Level]++
		}
	}
	return m, nil
}

func (s *envLogStore) CountByService(_ context.Context, p store.LogCountParams) ([]store.ServiceLogCount, error) {
	byService := map[string]*store.ServiceLogCount{}
	var order []string
	for _, e := range s.Entries {
		if !s.match(e, p.Environment, p.Service, p.Level, "", "") {
			continue
		}
		c, ok := byService[e.Service]
		if !ok {
			c = &store.ServiceLogCount{Service: e.Service}
			byService[e.Service] = c
			order = append(order, e.Service)
		}
		c.Total++
		if e.Level == "error" || e.Level == "fatal" {
			c.ErrorCount++
		}
	}
	out := make([]store.ServiceLogCount, 0, len(order))
	for _, svc := range order {
		out = append(out, *byService[svc])
	}
	return out, nil
}

func stagingScope(ctx context.Context) context.Context {
	return envscope.With(ctx, envscope.EnvScope{Allowed: []string{"staging"}})
}

func twoEnvEntries() []store.LogEntry {
	now := time.Now().UTC()
	return []store.LogEntry{
		{ID: 1, Timestamp: now, Level: "error", Service: "api", Environment: "staging", Message: "staging boom", TraceID: "t-1"},
		{ID: 2, Timestamp: now, Level: "error", Service: "api", Environment: "production", Message: "prod boom", TraceID: "t-1"},
		{ID: 3, Timestamp: now, Level: "info", Service: "api", Environment: "production", Message: "prod ok", TraceID: "t-1"},
	}
}

func parseJSON(t *testing.T, result *CallToolResult) map[string]any {
	t.Helper()
	if result.IsError {
		t.Fatalf("unexpected error result: %s", extractText(t, result))
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(extractText(t, result)), &resp); err != nil {
		t.Fatalf("failed to parse response JSON: %v", err)
	}
	return resp
}

func TestLogsStats_EnvScope(t *testing.T) {
	deps := LogsDeps{LogStore: newEnvLogStore(twoEnvEntries()...), ErrorGroupStore: mocks.NewErrorGroupStore()}
	ctx := stagingScope(context.Background())

	result, err := LogsStats(ctx, map[string]any{"action": "stats", "group_by": "level"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseJSON(t, result)
	// Only the single staging entry must be counted, not the 2 production ones.
	if total, _ := resp["total_logs"].(float64); int(total) != 1 {
		t.Errorf("total_logs = %v, want 1 (staging only)", resp["total_logs"])
	}
}

func TestLogsTrace_EnvScope(t *testing.T) {
	deps := LogsDeps{LogStore: newEnvLogStore(twoEnvEntries()...), ErrorGroupStore: mocks.NewErrorGroupStore()}
	ctx := stagingScope(context.Background())

	result, err := LogsTrace(ctx, map[string]any{"action": "trace", "trace_id": "t-1"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseJSON(t, result)
	// trace t-1 spans staging + production; a staging token sees only its entry.
	if n, _ := resp["total_entries"].(float64); int(n) != 1 {
		t.Errorf("total_entries = %v, want 1 (staging only)", resp["total_entries"])
	}
}

func TestLogsSummary_EnvScope(t *testing.T) {
	deps := LogsDeps{LogStore: newEnvLogStore(twoEnvEntries()...), ErrorGroupStore: mocks.NewErrorGroupStore()}
	ctx := stagingScope(context.Background())

	result, err := LogsSummary(ctx, map[string]any{"action": "summary"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp := parseJSON(t, result)
	if total, _ := resp["total_logs"].(float64); int(total) != 1 {
		t.Errorf("total_logs = %v, want 1 (staging only)", resp["total_logs"])
	}
}

func TestLogsStats_RejectsOutOfScopeEnvArg(t *testing.T) {
	deps := LogsDeps{LogStore: newEnvLogStore(twoEnvEntries()...), ErrorGroupStore: mocks.NewErrorGroupStore()}
	ctx := stagingScope(context.Background())

	// A staging-scoped token explicitly asking for production must be rejected.
	result, err := LogsStats(ctx, map[string]any{"action": "stats", "environment": "production"}, deps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected an error result when requesting an out-of-scope environment")
	}
}

func TestErrorsDetail_EnvScope(t *testing.T) {
	egs := mocks.NewErrorGroupStore()
	now := time.Now().UTC()
	egs.Groups["fp-prod"] = &store.ErrorGroup{
		Fingerprint:     "fp-prod",
		Service:         "api",
		Environment:     "production",
		Message:         "prod only error",
		Status:          store.ErrorGroupUnresolved,
		OccurrenceCount: 3,
		FirstSeenAt:     now.Add(-time.Hour),
		LastSeenAt:      now,
	}
	deps := ErrorsDeps{ErrorGroupStore: egs, LogStore: mocks.NewLogStore(), ErrorImpactStore: mocks.NewErrorImpactStore()}

	// staging-scoped token must NOT be able to read the production group.
	staging := stagingScope(context.Background())
	res, err := ErrorsDetail(staging, deps, map[string]any{"action": "detail", "fingerprint": "fp-prod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Error("staging token should not read a production error group (expected not-found)")
	}

	// production-scoped token can read it.
	prod := envscope.With(context.Background(), envscope.EnvScope{Allowed: []string{"production"}})
	res2, err := ErrorsDetail(prod, deps, map[string]any{"action": "detail", "fingerprint": "fp-prod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res2.IsError {
		t.Errorf("production token should read the production group, got: %s", extractText(t, res2))
	}
}
