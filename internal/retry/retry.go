package retry

import (
	"context"
	"errors"
	"math"
	"time"
)

// PermanentError wraps an error to signal that it should not be retried.
type PermanentError struct {
	Err error
}

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent wraps err so that Do stops retrying immediately.
func Permanent(err error) error {
	return &PermanentError{Err: err}
}

// Config controls retry behavior.
type Config struct {
	MaxAttempts int           // Total attempts (including the first). Default: 3.
	BaseDelay   time.Duration // Initial delay between retries. Default: 1s.
	MaxDelay    time.Duration // Cap on backoff delay. Default: 10s.
}

// DefaultConfig returns sensible retry defaults.
func DefaultConfig() Config {
	return Config{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
	}
}

// Do retries fn with exponential backoff until it succeeds, the context is
// cancelled, or MaxAttempts is exhausted. Returns the last error on failure.
func Do(ctx context.Context, cfg Config, fn func(ctx context.Context) error) error {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 1 * time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 10 * time.Second
	}

	var lastErr error
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}

		// Stop immediately on permanent errors
		var pe *PermanentError
		if errors.As(lastErr, &pe) {
			return pe.Err
		}

		// Don't sleep after the last attempt
		if attempt+1 >= cfg.MaxAttempts {
			break
		}

		// Exponential backoff: baseDelay * 2^attempt, capped at maxDelay
		delay := time.Duration(float64(cfg.BaseDelay) * math.Pow(2, float64(attempt)))
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}
