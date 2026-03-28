package auth

import (
	"sync"
	"time"

	"github.com/adham90/opentrace/internal/config"
	"github.com/adham90/opentrace/pkg/store"
)

const (
	maxFailedLogins = 5
	lockoutDuration = 15 * time.Minute
)

// handler holds the dependencies for auth HTTP handlers.
type handler struct {
	userStore  store.UserStore
	auditStore store.AuditStore
	cfg        *config.Config

	loginTracker *loginTracker
}

// ── Login tracker (brute-force protection) ────────────────────

type loginAttempt struct {
	failures int
	lockedAt time.Time
}

type loginTracker struct {
	mu      sync.Mutex
	entries map[string]*loginAttempt
}

func newLoginTracker() *loginTracker {
	return &loginTracker{entries: make(map[string]*loginAttempt)}
}

func (lt *loginTracker) recordFailure(email string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	entry, ok := lt.entries[email]
	if !ok {
		entry = &loginAttempt{}
		lt.entries[email] = entry
	}
	entry.failures++
	if entry.failures >= maxFailedLogins {
		entry.lockedAt = time.Now()
	}
}

func (lt *loginTracker) isLocked(email string) bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	entry, ok := lt.entries[email]
	if !ok {
		return false
	}
	if entry.lockedAt.IsZero() {
		return false
	}
	if time.Since(entry.lockedAt) > lockoutDuration {
		delete(lt.entries, email)
		return false
	}
	return true
}

func (lt *loginTracker) recordSuccess(email string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	delete(lt.entries, email)
}
