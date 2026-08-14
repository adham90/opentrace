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
	// failureDecayWindow is how long a failed attempt counts towards a
	// lockout. Without decay, four mistyped passwords in January plus one in
	// June lock a legitimate user out.
	failureDecayWindow = 1 * time.Hour
	// maxTrackedLogins bounds the tracker map. Entries are keyed by the
	// caller-supplied email, so an unauthenticated flood of unique addresses
	// would otherwise grow it for the life of the process.
	maxTrackedLogins = 10000
)

// handler holds the dependencies for auth HTTP handlers.
type handler struct {
	userStore  store.UserStore
	auditStore store.AuditStore
	cfg        *config.Config

	loginTracker *loginTracker

	// firstAdminMu serializes the "no users yet → create first admin" path so
	// two concurrent /connect requests can't both pass the Count()==0 check and
	// each create an admin. ponytail: process-level lock — sufficient for the
	// single-binary self-hosted deployment; a DB partial-unique index would be
	// needed for a multi-process setup.
	firstAdminMu sync.Mutex
}

// ── Login tracker (brute-force protection) ────────────────────

type loginAttempt struct {
	failures    int
	lockedAt    time.Time
	lastFailure time.Time
}

// expired reports whether the entry carries no live information: its lockout
// (if any) has elapsed and its failures have decayed.
func (a *loginAttempt) expired(now time.Time) bool {
	if !a.lockedAt.IsZero() && now.Sub(a.lockedAt) <= lockoutDuration {
		return false
	}
	return now.Sub(a.lastFailure) > failureDecayWindow
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
	now := time.Now()
	entry, ok := lt.entries[email]
	if !ok {
		lt.pruneLocked(now)
		entry = &loginAttempt{}
		lt.entries[email] = entry
	}
	// Failures older than the decay window no longer count towards a lockout.
	if !entry.lastFailure.IsZero() && now.Sub(entry.lastFailure) > failureDecayWindow {
		entry.failures = 0
		entry.lockedAt = time.Time{}
	}
	entry.failures++
	entry.lastFailure = now
	if entry.failures >= maxFailedLogins {
		entry.lockedAt = now
	}
}

func (lt *loginTracker) isLocked(email string) bool {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	entry, ok := lt.entries[email]
	if !ok {
		return false
	}
	now := time.Now()
	if entry.expired(now) {
		delete(lt.entries, email)
		return false
	}
	if entry.lockedAt.IsZero() {
		return false
	}
	if now.Sub(entry.lockedAt) > lockoutDuration {
		// Lockout served: forget it so the counter starts fresh.
		delete(lt.entries, email)
		return false
	}
	return true
}

// pruneLocked drops entries that carry no live information, and — if the map
// is still at capacity — every entry that is not currently locked out. Callers
// must hold lt.mu.
func (lt *loginTracker) pruneLocked(now time.Time) {
	if len(lt.entries) < maxTrackedLogins {
		return
	}
	for email, entry := range lt.entries {
		if entry.expired(now) {
			delete(lt.entries, email)
		}
	}
	if len(lt.entries) < maxTrackedLogins {
		return
	}
	// Still full: keep only live lockouts. Dropping partial failure counts is
	// safe (a lockout takes maxFailedLogins fresh attempts anyway) and keeps
	// the map bounded under an email-flood.
	for email, entry := range lt.entries {
		if entry.lockedAt.IsZero() || now.Sub(entry.lockedAt) > lockoutDuration {
			delete(lt.entries, email)
		}
	}
}

func (lt *loginTracker) recordSuccess(email string) {
	lt.mu.Lock()
	defer lt.mu.Unlock()
	delete(lt.entries, email)
}
