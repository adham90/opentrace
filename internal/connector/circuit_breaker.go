package connector

import (
	"errors"
	"log/slog"
	"sync"
	"time"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"    // normal operation
	CircuitOpen     CircuitState = "open"      // failing fast
	CircuitHalfOpen CircuitState = "half_open" // testing recovery
)

// ErrCircuitOpen is returned when the circuit breaker is open.
var ErrCircuitOpen = errors.New("circuit breaker is open: connector unavailable")

// CircuitBreaker implements the circuit breaker pattern for connector operations.
type CircuitBreaker struct {
	mu           sync.Mutex
	name         string        // connector name for logging
	state        CircuitState
	failCount    int
	successCount int           // consecutive successes in half-open
	lastFailTime time.Time
	threshold    int           // failures before opening
	resetTimeout time.Duration // how long to wait before half-open
	halfOpenMax  int           // successes needed to close from half-open
}

// CircuitBreakerConfig configures a circuit breaker.
type CircuitBreakerConfig struct {
	Threshold    int           // failures before opening (default: 5)
	ResetTimeout time.Duration // wait before half-open (default: 30s)
	HalfOpenMax  int           // successes to close (default: 2)
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Threshold:    5,
		ResetTimeout: 30 * time.Second,
		HalfOpenMax:  2,
	}
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(name string, cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenMax <= 0 {
		cfg.HalfOpenMax = 2
	}
	return &CircuitBreaker{
		name:         name,
		state:        CircuitClosed,
		threshold:    cfg.Threshold,
		resetTimeout: cfg.ResetTimeout,
		halfOpenMax:  cfg.HalfOpenMax,
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	// Check if open circuit should transition to half-open
	if cb.state == CircuitOpen && time.Since(cb.lastFailTime) >= cb.resetTimeout {
		return CircuitHalfOpen
	}
	return cb.state
}

// Allow checks if a request should be allowed through.
// Returns an error if the circuit is open.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return nil
	case CircuitOpen:
		if time.Since(cb.lastFailTime) >= cb.resetTimeout {
			cb.state = CircuitHalfOpen
			cb.successCount = 0
			slog.Info("circuit breaker half-open", "connector", cb.name)
			return nil
		}
		return ErrCircuitOpen
	case CircuitHalfOpen:
		return nil
	}
	return nil
}

// RecordSuccess records a successful operation.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitHalfOpen:
		cb.successCount++
		if cb.successCount >= cb.halfOpenMax {
			cb.state = CircuitClosed
			cb.failCount = 0
			cb.successCount = 0
			slog.Info("circuit breaker closed (recovered)", "connector", cb.name)
		}
	case CircuitClosed:
		cb.failCount = 0 // reset on success
	}
}

// RecordFailure records a failed operation.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailTime = time.Now()

	switch cb.state {
	case CircuitClosed:
		cb.failCount++
		if cb.failCount >= cb.threshold {
			cb.state = CircuitOpen
			slog.Warn("circuit breaker opened", "connector", cb.name, "failures", cb.failCount)
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.successCount = 0
		slog.Warn("circuit breaker re-opened (half-open test failed)", "connector", cb.name)
	}
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failCount = 0
	cb.successCount = 0
}
