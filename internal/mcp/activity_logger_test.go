package mcp

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adham90/opentrace/internal/store"
)

// mockMCPActivityStore counts Log calls for testing.
type mockMCPActivityStore struct {
	store.MCPActivityStore
	count atomic.Int32
}

func (m *mockMCPActivityStore) Log(_ context.Context, _ store.LogMCPActivityParams) error {
	m.count.Add(1)
	return nil
}

func TestActivityLogger_DrainOnClose(t *testing.T) {
	mock := &mockMCPActivityStore{}
	al := NewActivityLogger(mock, 100, 2)

	// Enqueue several entries
	for i := 0; i < 10; i++ {
		al.Log(store.LogMCPActivityParams{
			ToolName: "test",
		})
	}

	// Close should drain all entries
	al.Close()

	got := mock.count.Load()
	if got != 10 {
		t.Errorf("processed %d entries, want 10", got)
	}
}

func TestActivityLogger_DropWhenFull(t *testing.T) {
	mock := &mockMCPActivityStore{}
	// Very small buffer, 0 workers initially — fill the buffer
	al := &ActivityLogger{
		ch:    make(chan store.LogMCPActivityParams, 2),
		store: mock,
	}

	// Fill the buffer
	al.Log(store.LogMCPActivityParams{ToolName: "a"})
	al.Log(store.LogMCPActivityParams{ToolName: "b"})

	// This should be dropped (buffer full, no workers)
	al.Log(store.LogMCPActivityParams{ToolName: "c"})

	if len(al.ch) != 2 {
		t.Errorf("channel len = %d, want 2 (3rd should be dropped)", len(al.ch))
	}
}

func TestActivityLogger_CloseWaitsForWorkers(t *testing.T) {
	mock := &mockMCPActivityStore{}
	al := NewActivityLogger(mock, 10, 1)

	al.Log(store.LogMCPActivityParams{ToolName: "test"})

	done := make(chan struct{})
	go func() {
		al.Close()
		close(done)
	}()

	select {
	case <-done:
		// Close returned, workers finished
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return within 5 seconds — workers may not be draining")
	}

	if mock.count.Load() != 1 {
		t.Errorf("processed %d entries, want 1", mock.count.Load())
	}
}
