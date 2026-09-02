package tools

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	sqliteadapter "github.com/adham90/opentrace/internal/adapter/sqlite"
	"github.com/adham90/opentrace/internal/logstore/adapter"
	"github.com/adham90/opentrace/internal/logstore/engine"
	"github.com/adham90/opentrace/internal/logstore/ingest"
	"github.com/adham90/opentrace/pkg/store"
)

// These benchmarks measure what an agent actually waits on: a full MCP tool
// action against the real columnar log store and a real SQLite file, not
// against mocks. Everything else in this package tests behaviour with in-memory
// doubles, which says nothing about how long a call takes on real data.

// benchLogRows is the sealed population each benchmark queries — roughly a busy
// hour for a small service.
const benchLogRows = 20000

// benchLiveRows stay in the unsealed WAL, so every query exercises both halves
// of the store the way a real one does.
const benchLiveRows = 2000

// benchStores builds a store set backed by a real segmented log store and a
// real SQLite database, seeded with a sealed hour plus a live tail.
func benchStores(tb testing.TB) store.Stores {
	tb.Helper()
	dir := tb.TempDir()

	eng, err := engine.NewStore(filepath.Join(dir, "logs"), nil, ingest.PIIConfig{})
	if err != nil {
		tb.Fatalf("log engine: %v", err)
	}
	tb.Cleanup(func() { eng.Close() })
	logStore := adapter.New(eng)

	db, err := sqliteadapter.OpenSQLite(filepath.Join(dir, "opentrace.db"))
	if err != nil {
		tb.Fatalf("open sqlite: %v", err)
	}
	if err := sqliteadapter.RunSQLiteMigrations(db); err != nil {
		tb.Fatalf("migrate: %v", err)
	}
	tb.Cleanup(func() { db.Close() })

	stores := sqliteadapter.NewStores(db, logStore)

	ctx := context.Background()
	base := time.Now().UTC().Add(-30 * time.Minute)
	sealed := benchLogEntries(base, benchLogRows)
	if _, err := logStore.BatchInsert(ctx, sealed); err != nil {
		tb.Fatalf("insert sealed: %v", err)
	}
	if err := eng.SealCurrentHour(); err != nil {
		tb.Fatalf("seal: %v", err)
	}
	if _, err := logStore.BatchInsert(ctx, benchLogEntries(base.Add(20*time.Minute), benchLiveRows)); err != nil {
		tb.Fatalf("insert live: %v", err)
	}

	// Error groups: the enrichment the errors and search tools read back.
	for _, e := range sealed {
		if e.ErrorFingerprint == "" {
			continue
		}
		if err := stores.ErrorGroupStore.Upsert(ctx, e); err != nil {
			tb.Fatalf("upsert error group: %v", err)
		}
	}
	return stores
}

// benchLogEntries generates a realistic mix: mostly HTTP requests, some app
// logs, some errors sharing a small set of fingerprints.
func benchLogEntries(base time.Time, n int) []store.LogEntry {
	out := make([]store.LogEntry, n)
	services := []string{"api", "web", "worker"}
	for i := range out {
		e := store.LogEntry{
			Timestamp:   base.Add(time.Duration(i) * time.Millisecond),
			Level:       "info",
			Service:     services[i%len(services)],
			Environment: "production",
			Message:     fmt.Sprintf("request %d completed for account %d", i, i%500),
			TraceID:     fmt.Sprintf("trace-%d", i%1000),
			RequestID:   fmt.Sprintf("req-%d", i),
			UserID:      fmt.Sprintf("user-%d", i%200),
		}
		switch i % 6 {
		case 0, 1, 2, 3:
			e.Kind = "request"
			e.EventType = "http.request"
			e.RequestSummary = &store.RequestSummary{
				Controller: "OrdersController",
				Action:     "show",
				Method:     "GET",
				Path:       fmt.Sprintf("/orders/%d", i%50),
				Status:     200,
				DurationMs: float64(10 + i%500),
				SQLCount:   i % 20,
				DBTimeMs:   float64((i % 20) * 2),
				CacheReads: 10,
				CacheHits:  7,
			}
		case 4:
			e.Kind = "log"
			e.EventType = "app.log"
			e.Message = fmt.Sprintf("cache warm for shard %d", i%16)
		case 5:
			e.Kind = "error"
			e.Level = "error"
			e.EventType = "app.error"
			e.ExceptionClass = "NoMethodError"
			e.ErrorMessage = "undefined method `total' for nil"
			e.ErrorFingerprint = fmt.Sprintf("fp-%d", i%25)
			e.SourceFile = "app/models/order.rb"
			e.SourceLine = 87
		}
		out[i] = e
	}
	return out
}

func benchLogsDeps(s store.Stores) LogsDeps {
	d := LogsDeps{LogStore: s.LogStore, ErrorGroupStore: s.ErrorGroupStore}
	InitLogsDeps(&d)
	return d
}

func runTool(b *testing.B, call func() (*CallToolResult, error)) {
	b.Helper()
	result, err := call()
	if err != nil {
		b.Fatalf("tool error: %v", err)
	}
	if result == nil || result.IsError {
		b.Fatalf("tool returned an error result: %+v", result)
	}
}

// BenchmarkToolLogsSearch is the single most-called action.
func BenchmarkToolLogsSearch(b *testing.B) {
	deps := benchLogsDeps(benchStores(b))
	ctx := context.Background()
	args := map[string]any{"action": "search", "since": "2h", "limit": float64(50)}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return LogsSearch(ctx, args, deps) })
	}
}

// BenchmarkToolLogsSearchFiltered narrows to errors from one service — the
// shape a triage session uses.
func BenchmarkToolLogsSearchFiltered(b *testing.B) {
	deps := benchLogsDeps(benchStores(b))
	ctx := context.Background()
	args := map[string]any{
		"action": "search", "since": "2h", "limit": float64(50),
		"level": "error", "service": "api",
	}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return LogsSearch(ctx, args, deps) })
	}
}

// BenchmarkToolLogsStats backs the log-volume view.
func BenchmarkToolLogsStats(b *testing.B) {
	deps := benchLogsDeps(benchStores(b))
	ctx := context.Background()
	args := map[string]any{"action": "stats", "since": "2h", "group_by": "level"}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return LogsStats(ctx, args, deps) })
	}
}

// BenchmarkToolLogsPerformance is the whole-range request scan.
func BenchmarkToolLogsPerformance(b *testing.B) {
	deps := benchLogsDeps(benchStores(b))
	ctx := context.Background()
	args := map[string]any{"action": "performance", "since": "2h"}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return LogsPerformance(ctx, args, deps) })
	}
}

// BenchmarkToolErrorsList is the errors landing action.
func BenchmarkToolErrorsList(b *testing.B) {
	s := benchStores(b)
	deps := ErrorsDeps{
		ErrorGroupStore:  s.ErrorGroupStore,
		LogStore:         s.LogStore,
		ErrorImpactStore: s.ErrorImpactStore,
	}
	ctx := context.Background()
	args := map[string]any{"action": "list", "since": "2h", "limit": float64(25)}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return ErrorsList(ctx, deps, args) })
	}
}

// BenchmarkToolOverviewStatus fans out across every store — it is the first
// call most sessions make, so its latency is the first thing an agent feels.
func BenchmarkToolOverviewStatus(b *testing.B) {
	s := benchStores(b)
	deps := OverviewDeps{
		LogStore:         s.LogStore,
		DSStore:          s.DSStore,
		ServerStore:      s.ServerStore,
		ErrorGroupStore:  s.ErrorGroupStore,
		WatchStore:       s.WatchStore,
		HealthCheckStore: s.HealthCheckStore,
		SettingsStore:    s.SettingsStore,
		AgentNoteStore:   s.AgentNoteStore,
		DeployStore:      s.DeployStore,
		UserStore:        s.UserStore,
	}
	ctx := context.Background()
	args := map[string]any{"action": "status"}
	b.ResetTimer()
	for range b.N {
		runTool(b, func() (*CallToolResult, error) { return HandleOverviewStatus(ctx, deps, args) })
	}
}

// BenchmarkIngestBatch measures the SDK-facing write: a 200-entry batch through
// the store adapter, including the fsync the durability model requires.
func BenchmarkIngestBatch(b *testing.B) {
	s := benchStores(b)
	ctx := context.Background()
	base := time.Now().UTC()
	batch := benchLogEntries(base, 200)
	b.ResetTimer()
	for range b.N {
		if _, err := s.LogStore.BatchInsert(ctx, batch); err != nil {
			b.Fatalf("insert: %v", err)
		}
	}
}

func init() {
	// Store and migration INFO lines interleave with benchmark output and the
	// write itself shows up in the timings.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}
