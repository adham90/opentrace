package auth

import (
	"fmt"
	"testing"
	"time"
)

// TestLoginTracker_LocksAfterMaxFailures keeps the brute-force guard working.
func TestLoginTracker_LocksAfterMaxFailures(t *testing.T) {
	lt := newLoginTracker()
	for i := 0; i < maxFailedLogins-1; i++ {
		lt.recordFailure("user@example.com")
		if lt.isLocked("user@example.com") {
			t.Fatalf("locked after %d failures, want %d", i+1, maxFailedLogins)
		}
	}
	lt.recordFailure("user@example.com")
	if !lt.isLocked("user@example.com") {
		t.Fatal("expected lockout after maxFailedLogins")
	}
}

// TestLoginTracker_StaleFailuresDecay proves old failures no longer add up:
// four failures long ago plus one now must not lock a legitimate user out.
func TestLoginTracker_StaleFailuresDecay(t *testing.T) {
	lt := newLoginTracker()
	email := "user@example.com"

	lt.entries[email] = &loginAttempt{
		failures:    maxFailedLogins - 1,
		lastFailure: time.Now().Add(-2 * failureDecayWindow),
	}

	lt.recordFailure(email)

	if lt.isLocked(email) {
		t.Fatal("stale failures triggered a lockout")
	}
	if got := lt.entries[email].failures; got != 1 {
		t.Fatalf("failures = %d, want 1 (counter should have decayed)", got)
	}
}

// TestLoginTracker_LockoutExpires ensures a lockout is not permanent.
func TestLoginTracker_LockoutExpires(t *testing.T) {
	lt := newLoginTracker()
	email := "user@example.com"
	lt.entries[email] = &loginAttempt{
		failures:    maxFailedLogins,
		lockedAt:    time.Now().Add(-2 * lockoutDuration),
		lastFailure: time.Now().Add(-2 * lockoutDuration),
	}

	if lt.isLocked(email) {
		t.Fatal("expired lockout still reported as locked")
	}
	if _, ok := lt.entries[email]; ok {
		t.Fatal("expired entry not removed")
	}
}

// TestLoginTracker_BoundedUnderEmailFlood proves the map cannot grow without
// bound from unauthenticated requests carrying unique addresses.
func TestLoginTracker_BoundedUnderEmailFlood(t *testing.T) {
	lt := newLoginTracker()
	for i := 0; i < maxTrackedLogins+5000; i++ {
		lt.recordFailure(fmt.Sprintf("flood-%d@example.com", i))
	}
	if n := len(lt.entries); n > maxTrackedLogins {
		t.Fatalf("tracker unbounded: %d entries > %d", n, maxTrackedLogins)
	}
}

// TestLoginTracker_FloodDoesNotClearLiveLockouts ensures the bounding does not
// hand an attacker a way to clear their own lockout.
func TestLoginTracker_FloodDoesNotClearLiveLockouts(t *testing.T) {
	lt := newLoginTracker()
	victim := "victim@example.com"
	for i := 0; i < maxFailedLogins; i++ {
		lt.recordFailure(victim)
	}
	if !lt.isLocked(victim) {
		t.Fatal("setup: victim should be locked")
	}

	for i := 0; i < maxTrackedLogins+100; i++ {
		lt.recordFailure(fmt.Sprintf("flood-%d@example.com", i))
	}

	if !lt.isLocked(victim) {
		t.Fatal("an email flood cleared a live lockout")
	}
}

func TestLoginTracker_SuccessClearsEntry(t *testing.T) {
	lt := newLoginTracker()
	lt.recordFailure("user@example.com")
	lt.recordSuccess("user@example.com")
	if _, ok := lt.entries["user@example.com"]; ok {
		t.Fatal("successful login should clear the entry")
	}
}
