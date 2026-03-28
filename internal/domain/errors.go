// Package domain defines shared error types and values used across all domain
// services. Adapters wrap these with context. Handlers check them with errors.Is.
package domain

import "errors"

// Sentinel errors for domain-level error handling.
// Use fmt.Errorf("context: %w", domain.ErrNotFound) in adapters.
// Use errors.Is(err, domain.ErrNotFound) in handlers.
var (
	// ErrNotFound indicates the requested entity does not exist.
	ErrNotFound = errors.New("not found")

	// ErrNotConfigured indicates a required dependency (store, service) is nil.
	ErrNotConfigured = errors.New("not configured")

	// ErrInvalidInput indicates the caller provided bad arguments.
	ErrInvalidInput = errors.New("invalid input")

	// ErrUnauthorized indicates the caller lacks permission.
	ErrUnauthorized = errors.New("unauthorized")

	// ErrConflict indicates a uniqueness or state conflict.
	ErrConflict = errors.New("conflict")
)
