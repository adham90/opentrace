package watcher

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEventHub_PublishSubscribe(t *testing.T) {
	hub := NewEventHub()
	runID := uuid.New()
	hub.Register(runID)

	ch, buffered, unsub, ok := hub.Subscribe(runID)
	if !ok {
		t.Fatal("expected Subscribe to succeed")
	}
	defer unsub()

	if len(buffered) != 0 {
		t.Fatalf("expected 0 buffered events, got %d", len(buffered))
	}

	hub.Publish(runID, RunEvent{Type: "thinking", Content: "analyzing", Time: time.Now()})
	hub.Publish(runID, RunEvent{Type: "tool_call", ToolName: "query_db", Time: time.Now()})

	// Should receive both events
	select {
	case ev := <-ch:
		if ev.Type != "thinking" {
			t.Errorf("expected thinking, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}

	select {
	case ev := <-ch:
		if ev.Type != "tool_call" {
			t.Errorf("expected tool_call, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventHub_CatchUp(t *testing.T) {
	hub := NewEventHub()
	runID := uuid.New()
	hub.Register(runID)

	// Publish events before subscribing
	hub.Publish(runID, RunEvent{Type: "thinking", Content: "first", Time: time.Now()})
	hub.Publish(runID, RunEvent{Type: "tool_call", ToolName: "query", Time: time.Now()})

	ch, buffered, unsub, ok := hub.Subscribe(runID)
	if !ok {
		t.Fatal("expected Subscribe to succeed")
	}
	defer unsub()

	if len(buffered) != 2 {
		t.Fatalf("expected 2 buffered events, got %d", len(buffered))
	}
	if buffered[0].Type != "thinking" {
		t.Errorf("buffered[0].Type = %s, want thinking", buffered[0].Type)
	}
	if buffered[1].ToolName != "query" {
		t.Errorf("buffered[1].ToolName = %s, want query", buffered[1].ToolName)
	}

	// New events should still come through the channel
	hub.Publish(runID, RunEvent{Type: "final", Content: "done", Time: time.Now()})
	select {
	case ev := <-ch:
		if ev.Type != "final" {
			t.Errorf("expected final, got %s", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventHub_MarkDone(t *testing.T) {
	hub := NewEventHub()
	runID := uuid.New()
	hub.Register(runID)

	ch, _, unsub, ok := hub.Subscribe(runID)
	if !ok {
		t.Fatal("expected Subscribe to succeed")
	}
	defer unsub()

	hub.MarkDone(runID)

	// Channel should be closed
	select {
	case _, open := <-ch:
		if open {
			t.Error("expected channel to be closed")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestEventHub_MarkDone_LateSubscriber(t *testing.T) {
	hub := NewEventHub()
	runID := uuid.New()
	hub.Register(runID)

	hub.Publish(runID, RunEvent{Type: "thinking", Content: "test", Time: time.Now()})
	hub.MarkDone(runID)

	// Late subscriber should get buffered events and a closed channel
	ch, buffered, _, ok := hub.Subscribe(runID)
	if !ok {
		t.Fatal("expected Subscribe to succeed (within 60s cleanup window)")
	}

	if len(buffered) != 1 {
		t.Fatalf("expected 1 buffered event, got %d", len(buffered))
	}

	// Channel should already be closed
	select {
	case _, open := <-ch:
		if open {
			t.Error("expected channel to be closed for late subscriber")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestEventHub_NonBlocking(t *testing.T) {
	hub := NewEventHub()
	runID := uuid.New()
	hub.Register(runID)

	// Publishing with no subscribers should not block
	for i := 0; i < 100; i++ {
		hub.Publish(runID, RunEvent{Type: "thinking", Content: "test", Time: time.Now()})
	}

	// Events should be buffered
	_, buffered, _, ok := hub.Subscribe(runID)
	if !ok {
		t.Fatal("expected Subscribe to succeed")
	}
	if len(buffered) != 100 {
		t.Fatalf("expected 100 buffered events, got %d", len(buffered))
	}
}

func TestEventHub_UnknownRun(t *testing.T) {
	hub := NewEventHub()

	// Subscribe to non-existent run
	_, _, _, ok := hub.Subscribe(uuid.New())
	if ok {
		t.Error("expected Subscribe to return ok=false for unknown run")
	}

	// Publish to non-existent run should be a no-op
	hub.Publish(uuid.New(), RunEvent{Type: "thinking", Time: time.Now()})
}
