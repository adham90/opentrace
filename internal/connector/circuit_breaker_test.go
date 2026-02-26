package connector

import (
	"errors"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedAllowsRequests(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    5,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  2,
	})

	if err := cb.Allow(); err != nil {
		t.Fatalf("closed circuit should allow requests, got: %v", err)
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("state = %q, want %q", cb.State(), CircuitClosed)
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    3,
		ResetTimeout: time.Hour, // long timeout so it stays open
		HalfOpenMax:  2,
	})

	// Record failures up to threshold
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q after %d failures", cb.State(), CircuitOpen, 3)
	}
}

func TestCircuitBreaker_RejectsWhenOpen(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: time.Hour, // long timeout so it stays open
		HalfOpenMax:  2,
	})

	cb.RecordFailure()
	cb.RecordFailure()

	err := cb.Allow()
	if err == nil {
		t.Fatal("open circuit should reject requests")
	}
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("error = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreaker_TransitionsToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  2,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q", cb.State(), CircuitOpen)
	}

	// Wait for reset timeout
	time.Sleep(5 * time.Millisecond)

	// State should report half-open
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("state = %q, want %q after reset timeout", cb.State(), CircuitHalfOpen)
	}

	// Allow should succeed and transition to half-open
	if err := cb.Allow(); err != nil {
		t.Fatalf("half-open circuit should allow requests, got: %v", err)
	}
}

func TestCircuitBreaker_ClosesAfterHalfOpenSuccess(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  2,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(5 * time.Millisecond)

	// Transition to half-open via Allow
	if err := cb.Allow(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Record enough successes to close
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Fatalf("state = %q, want %q after half-open successes", cb.State(), CircuitClosed)
	}

	// Should allow requests now
	if err := cb.Allow(); err != nil {
		t.Fatalf("closed circuit should allow requests, got: %v", err)
	}
}

func TestCircuitBreaker_ReopensOnHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  2,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for reset timeout
	time.Sleep(5 * time.Millisecond)

	// Transition to half-open via Allow
	if err := cb.Allow(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// One success, then a failure in half-open
	cb.RecordSuccess()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q after half-open failure", cb.State(), CircuitOpen)
	}
}

func TestCircuitBreaker_ResetClearsState(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    2,
		ResetTimeout: time.Hour,
		HalfOpenMax:  2,
	})

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q", cb.State(), CircuitOpen)
	}

	cb.Reset()

	if cb.State() != CircuitClosed {
		t.Fatalf("state = %q, want %q after reset", cb.State(), CircuitClosed)
	}
	if err := cb.Allow(); err != nil {
		t.Fatalf("reset circuit should allow requests, got: %v", err)
	}
}

func TestCircuitBreaker_SuccessResetsFailCount(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    3,
		ResetTimeout: time.Hour,
		HalfOpenMax:  2,
	})

	// Record 2 failures (below threshold)
	cb.RecordFailure()
	cb.RecordFailure()

	// A success should reset the fail count
	cb.RecordSuccess()

	// Now 2 more failures should NOT open the circuit (count was reset)
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitClosed {
		t.Fatalf("state = %q, want %q — success should have reset fail count", cb.State(), CircuitClosed)
	}

	// One more failure (now at 3) should open it
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q after 3 consecutive failures", cb.State(), CircuitOpen)
	}
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	cfg := DefaultCircuitBreakerConfig()
	if cfg.Threshold != 5 {
		t.Errorf("Threshold = %d, want 5", cfg.Threshold)
	}
	if cfg.ResetTimeout != 30*time.Second {
		t.Errorf("ResetTimeout = %v, want 30s", cfg.ResetTimeout)
	}
	if cfg.HalfOpenMax != 2 {
		t.Errorf("HalfOpenMax = %d, want 2", cfg.HalfOpenMax)
	}
}

func TestCircuitBreaker_ZeroConfigUsesDefaults(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{})

	// Should use defaults: threshold=5, resetTimeout=30s, halfOpenMax=2
	// Record 4 failures — should still be closed
	for i := 0; i < 4; i++ {
		cb.RecordFailure()
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("state = %q, want %q after 4 failures (threshold should be 5)", cb.State(), CircuitClosed)
	}

	// 5th failure should open
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %q, want %q after 5 failures", cb.State(), CircuitOpen)
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker("test", CircuitBreakerConfig{
		Threshold:    100,
		ResetTimeout: time.Millisecond,
		HalfOpenMax:  2,
	})

	done := make(chan struct{})

	// Concurrent failures
	go func() {
		for i := 0; i < 50; i++ {
			cb.RecordFailure()
		}
		done <- struct{}{}
	}()

	// Concurrent successes
	go func() {
		for i := 0; i < 50; i++ {
			cb.RecordSuccess()
		}
		done <- struct{}{}
	}()

	// Concurrent state checks
	go func() {
		for i := 0; i < 50; i++ {
			_ = cb.State()
			_ = cb.Allow()
		}
		done <- struct{}{}
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
}
