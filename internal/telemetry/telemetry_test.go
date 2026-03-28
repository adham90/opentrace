package telemetry

import (
	"errors"
	"testing"
	"time"
)

// --- Startup ---

func TestStartup_Record(t *testing.T) {
	s := NewStartup()
	start := time.Now()
	time.Sleep(5 * time.Millisecond)
	s.Record("database", start)

	stages := s.Stages()
	if len(stages) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(stages))
	}
	if stages[0].Name != "database" {
		t.Errorf("name = %q, want 'database'", stages[0].Name)
	}
	if stages[0].Duration < 5*time.Millisecond {
		t.Errorf("duration too short: %v", stages[0].Duration)
	}
}

func TestStartup_Total(t *testing.T) {
	s := NewStartup()
	time.Sleep(5 * time.Millisecond)
	if s.Total() < 5*time.Millisecond {
		t.Errorf("total too short: %v", s.Total())
	}
}

func TestStartup_Summary(t *testing.T) {
	s := NewStartup()
	s.Record("db", time.Now())
	s.Record("mcp", time.Now())

	summary := s.Summary()
	stages, ok := summary["stages"].([]map[string]any)
	if !ok {
		t.Fatal("expected stages in summary")
	}
	if len(stages) != 2 {
		t.Errorf("expected 2 stages, got %d", len(stages))
	}
}

// --- GoroutineHealth ---

func TestGoroutineHealth_Register(t *testing.T) {
	g := NewGoroutineHealth()
	g.Register("watcher")

	status := g.Status()
	if len(status) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(status))
	}
	if status[0].Name != "watcher" {
		t.Errorf("name = %q, want 'watcher'", status[0].Name)
	}
}

func TestGoroutineHealth_Ping(t *testing.T) {
	g := NewGoroutineHealth()
	g.Register("checker")
	g.Ping("checker")
	g.Ping("checker")

	status := g.Status()
	if status[0].PingCount != 2 {
		t.Errorf("ping_count = %d, want 2", status[0].PingCount)
	}
}

func TestGoroutineHealth_PingUnregistered(t *testing.T) {
	g := NewGoroutineHealth()
	g.Ping("unknown")

	status := g.Status()
	if len(status) != 1 {
		t.Fatal("ping should auto-register worker")
	}
}

func TestGoroutineHealth_RecordError(t *testing.T) {
	g := NewGoroutineHealth()
	g.Register("worker")
	g.RecordError("worker", errors.New("connection lost"))

	status := g.Status()
	if status[0].ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", status[0].ErrorCount)
	}
	if status[0].LastError != "connection lost" {
		t.Errorf("last_error = %q, want 'connection lost'", status[0].LastError)
	}
}

func TestGoroutineHealth_IsHealthy(t *testing.T) {
	g := NewGoroutineHealth()
	g.Register("worker")
	g.Ping("worker")

	if !g.IsHealthy(time.Minute) {
		t.Error("should be healthy after recent ping")
	}
}

func TestGoroutineHealth_IsUnhealthy(t *testing.T) {
	g := NewGoroutineHealth()
	g.Register("worker")
	// Set a ping far in the past to simulate staleness.
	g.mu.Lock()
	g.workers["worker"].PingCount = 1
	g.workers["worker"].LastPingAt = time.Now().Add(-10 * time.Minute)
	g.mu.Unlock()

	if g.IsHealthy(time.Minute) {
		t.Error("should be unhealthy with stale ping")
	}
}

// --- StoreMetrics ---

func TestStoreMetrics_Record(t *testing.T) {
	m := NewStoreMetrics()
	m.Record("LogStore.Search", 50*time.Millisecond, nil)
	m.Record("LogStore.Search", 100*time.Millisecond, nil)
	m.Record("LogStore.Search", 30*time.Millisecond, errors.New("timeout"))

	snap := m.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(snap))
	}
	op := snap[0]
	if op.CallCount != 3 {
		t.Errorf("call_count = %d, want 3", op.CallCount)
	}
	if op.ErrorCount != 1 {
		t.Errorf("error_count = %d, want 1", op.ErrorCount)
	}
	if op.MaxMs != 100 {
		t.Errorf("max_ms = %d, want 100", op.MaxMs)
	}
	if op.TotalMs != 180 {
		t.Errorf("total_ms = %d, want 180", op.TotalMs)
	}
	if op.AvgMs != 60 {
		t.Errorf("avg_ms = %f, want 60", op.AvgMs)
	}
}

func TestStoreMetrics_MultipleOperations(t *testing.T) {
	m := NewStoreMetrics()
	m.Record("LogStore.Search", 10*time.Millisecond, nil)
	m.Record("ErrorStore.List", 20*time.Millisecond, nil)

	snap := m.Snapshot()
	if len(snap) != 2 {
		t.Errorf("expected 2 operations, got %d", len(snap))
	}
}

func TestStoreMetrics_Reset(t *testing.T) {
	m := NewStoreMetrics()
	m.Record("LogStore.Search", 10*time.Millisecond, nil)
	m.Reset()

	snap := m.Snapshot()
	if len(snap) != 0 {
		t.Errorf("expected 0 after reset, got %d", len(snap))
	}
}
