package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDo_SuccessOnFirstAttempt(t *testing.T) {
	calls := 0
	err := Do(context.Background(), DefaultConfig(), func(_ context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDo_SuccessAfterRetry(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Config{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func(_ context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDo_AllAttemptsFail(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Config{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func(_ context.Context) error {
		calls++
		return errors.New("permanent")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if err.Error() != "permanent" {
		t.Errorf("expected 'permanent' error, got %q", err.Error())
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	cancel() // cancel immediately
	err := Do(ctx, Config{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
	}, func(_ context.Context) error {
		calls++
		return errors.New("fail")
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// Should have tried once, then context was cancelled before sleep
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDo_ExponentialBackoff(t *testing.T) {
	cfg := Config{
		MaxAttempts: 4,
		BaseDelay:   10 * time.Millisecond,
		MaxDelay:    100 * time.Millisecond,
	}
	start := time.Now()
	calls := 0
	Do(context.Background(), cfg, func(_ context.Context) error {
		calls++
		return errors.New("fail")
	})
	elapsed := time.Since(start)
	// Expected delays: 10ms + 20ms + 40ms = 70ms minimum
	if elapsed < 60*time.Millisecond {
		t.Errorf("expected at least 60ms total backoff, got %v", elapsed)
	}
	if calls != 4 {
		t.Errorf("expected 4 calls, got %d", calls)
	}
}

func TestDo_PermanentError(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Config{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func(_ context.Context) error {
		calls++
		return Permanent(errors.New("bad request"))
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry on permanent error), got %d", calls)
	}
	if err.Error() != "bad request" {
		t.Errorf("expected 'bad request', got %q", err.Error())
	}
}

func TestDo_PermanentAfterRetries(t *testing.T) {
	calls := 0
	err := Do(context.Background(), Config{
		MaxAttempts: 5,
		BaseDelay:   1 * time.Millisecond,
		MaxDelay:    10 * time.Millisecond,
	}, func(_ context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return Permanent(errors.New("permanent"))
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
	if err.Error() != "permanent" {
		t.Errorf("expected 'permanent', got %q", err.Error())
	}
}

func TestDo_MaxDelayCap(t *testing.T) {
	cfg := Config{
		MaxAttempts: 3,
		BaseDelay:   50 * time.Millisecond,
		MaxDelay:    50 * time.Millisecond, // cap at base
	}
	start := time.Now()
	Do(context.Background(), cfg, func(_ context.Context) error {
		return errors.New("fail")
	})
	elapsed := time.Since(start)
	// Two delays of 50ms each = 100ms
	if elapsed < 90*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("expected ~100ms total backoff, got %v", elapsed)
	}
}
