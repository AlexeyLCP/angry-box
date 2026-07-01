package web

// authlimiter_test.go pins the auth rate-limiter: after maxFailures within the
// window, further attempts from the same IP are rejected with 429 until the
// window elapses. This throttles brute-force / credential-stuffing against the
// Basic-Auth gate (CTO-review L3).

import (
	"testing"
	"time"
)

func TestAuthLimiter_AllowsUntilThreshold(t *testing.T) {
	lim := newAuthLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !lim.allow("10.0.0.1") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		lim.recordFailure("10.0.0.1")
	}
	// 4th attempt within the window must be blocked.
	if lim.allow("10.0.0.1") {
		t.Fatal("4th attempt within window should be blocked")
	}
}

func TestAuthLimiter_DifferentIPsIndependent(t *testing.T) {
	lim := newAuthLimiter(2, time.Minute)
	lim.recordFailure("10.0.0.1")
	lim.recordFailure("10.0.0.1")
	if lim.allow("10.0.0.1") {
		t.Error("10.0.0.1 should be blocked after 2 failures")
	}
	if !lim.allow("10.0.0.2") {
		t.Error("different IP must not be blocked")
	}
}

func TestAuthLimiter_WindowExpiryReleases(t *testing.T) {
	lim := newAuthLimiter(2, 30*time.Millisecond)
	lim.recordFailure("10.0.0.1")
	lim.recordFailure("10.0.0.1")
	if lim.allow("10.0.0.1") {
		t.Fatal("should be blocked within window")
	}
	time.Sleep(45 * time.Millisecond)
	if !lim.allow("10.0.0.1") {
		t.Fatal("should be allowed after the window expires")
	}
}

func TestAuthLimiter_SuccessDoesNotConsumeQuota(t *testing.T) {
	lim := newAuthLimiter(2, time.Minute)
	// Recording a failure then a successful check should not block the next
	// legitimate attempt — allow() only counts failures, not successes.
	lim.recordFailure("10.0.0.1")
	if !lim.allow("10.0.0.1") {
		t.Fatal("single failure should not block")
	}
}