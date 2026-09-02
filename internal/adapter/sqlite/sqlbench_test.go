package sqlite

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/adham90/opentrace/pkg/store"
	"github.com/uptrace/bun"
)

// These benchmarks measure the SQLite half of a tool call: the platform data
// (error groups, watches, users) that MCP tools read alongside the log store.
//
// The parallel variant exists to make the connection-pool choice visible:
// OpenSQLite pins the pool to a single connection, so concurrent readers are
// serialized. If that ever becomes the bottleneck, the parallel benchmark is
// where it shows up — it will report the same per-op time as the serial one
// however many cores are thrown at it.

// benchGroupCount is the number of distinct error groups the lookups run over.
const benchGroupCount = 500

var benchNow = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// setupBenchDB opens a real on-disk database. In-memory SQLite would hide the
// page-cache and WAL behaviour these benchmarks exist to measure.
func setupBenchDB(tb testing.TB) *bun.DB {
	tb.Helper()
	db, err := OpenSQLite(filepath.Join(tb.TempDir(), "bench.db"))
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	if err := RunSQLiteMigrations(db); err != nil {
		db.Close()
		tb.Fatalf("migrate: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	return db
}

func seedErrorGroups(tb testing.TB, s store.ErrorGroupStore, n int) {
	tb.Helper()
	ctx := context.Background()
	for i := range n {
		if err := s.Upsert(ctx, store.LogEntry{
			ErrorFingerprint: fmt.Sprintf("fp-%d", i),
			Environment:      "production",
			Service:          "api",
			Level:            "error",
			ExceptionClass:   "NoMethodError",
			Message:          "undefined method `total' for nil",
			SourceFile:       "app/models/order.rb",
			SourceLine:       87,
			Timestamp:        benchNow,
		}); err != nil {
			tb.Fatalf("seed error group: %v", err)
		}
	}
}

func benchErrorGroupStore(tb testing.TB) store.ErrorGroupStore {
	tb.Helper()
	s := NewStores(setupBenchDB(tb), nil).ErrorGroupStore
	seedErrorGroups(tb, s, benchGroupCount)
	return s
}

// BenchmarkErrorGroupGet is the per-row enrichment every log search does for
// entries carrying a fingerprint.
func BenchmarkErrorGroupGet(b *testing.B) {
	s := benchErrorGroupStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := range b.N {
		if _, err := s.Get(ctx, fmt.Sprintf("fp-%d", i%benchGroupCount), "production"); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkErrorGroupGetParallel reports the same lookup under contention. It
// is the check on the single-connection pool: if this ever needs to scale with
// cores, this number has to move before anything else is worth changing.
func BenchmarkErrorGroupGetParallel(b *testing.B) {
	s := benchErrorGroupStore(b)
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, err := s.Get(ctx, fmt.Sprintf("fp-%d", i%benchGroupCount), "production"); err != nil {
				b.Fatal(err)
			}
			i++
		}
	})
}

// BenchmarkErrorGroupList backs errors(action:"list").
func BenchmarkErrorGroupList(b *testing.B) {
	s := benchErrorGroupStore(b)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		if _, err := s.List(ctx, store.ListErrorGroupParams{Limit: 50, Environment: "production"}); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkErrorGroupUpsert is on the ingest side: one write per error entry.
func BenchmarkErrorGroupUpsert(b *testing.B) {
	s := NewStores(setupBenchDB(b), nil).ErrorGroupStore
	ctx := context.Background()
	entry := store.LogEntry{
		Environment: "production", Service: "api", Level: "error",
		ExceptionClass: "NoMethodError", Message: "undefined method",
		SourceFile: "app/models/order.rb", SourceLine: 87, Timestamp: benchNow,
	}
	b.ResetTimer()
	for i := range b.N {
		entry.ErrorFingerprint = fmt.Sprintf("fp-%d", i%benchGroupCount)
		if err := s.Upsert(ctx, entry); err != nil {
			b.Fatal(err)
		}
	}
}

func init() {
	// Migration INFO lines interleave with benchmark output.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
}
