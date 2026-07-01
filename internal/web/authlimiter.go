package web

// authlimiter.go is a small in-memory rate limiter for authentication failures,
// keyed by remote IP. After maxFailures within a sliding window, further
// attempts are rejected (429) until the window elapses. This throttles
// brute-force / credential-stuffing against the Basic-Auth gate (CTO-review L3).
//
// It is intentionally process-local (no distributed state): the panel runs as a
// single daemon. State is guarded by a mutex; the window is sliding per failure
// timestamp.

import (
	"sync"
	"time"
)

type authLimiter struct {
	mu          sync.Mutex
	maxFailures int
	window      time.Duration
	failures    map[string][]time.Time
}

// defaultAuthLimiter is the process-wide limiter for the auth gate. 5 failures
// per IP per 15 minutes throttles brute-force without locking out a forgetful
// admin for too long (CTO-review L3).
var defaultAuthLimiter = newAuthLimiter(5, 15*time.Minute)

func newAuthLimiter(maxFailures int, window time.Duration) *authLimiter {
	if maxFailures < 1 {
		maxFailures = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &authLimiter{maxFailures: maxFailures, window: window, failures: map[string][]time.Time{}}
}

// allow reports whether an auth attempt from ip may proceed. It returns false
// when ip has recorded maxFailures (or more) failures within the window.
func (l *authLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	keep := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	if len(keep) == 0 {
		delete(l.failures, ip)
	} else {
		l.failures[ip] = keep
	}
	return len(keep) < l.maxFailures
}

// recordFailure logs an auth failure for ip, retaining only failures within the
// current window.
func (l *authLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	keep := l.failures[ip][:0]
	for _, t := range l.failures[ip] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.failures[ip] = append(keep, now)
}