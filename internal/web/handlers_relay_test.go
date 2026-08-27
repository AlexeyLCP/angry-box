package web

import (
	"testing"
	"time"
)

func TestRelayLocalAddr(t *testing.T) {
	for in, want := range map[string]string{
		"":               "127.0.0.1:9080",
		":9080":          "127.0.0.1:9080",
		"0.0.0.0:9080":   "127.0.0.1:9080",
		"127.0.0.1:8081": "127.0.0.1:8081",
		"10.0.0.5:9090":  "10.0.0.5:9090",
	} {
		if got := relayLocalAddr(in); got != want {
			t.Errorf("relayLocalAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotifyLockoutOnceDedupes(t *testing.T) {
	lockoutNotifyMu.Lock()
	prevHook, prevMap := authLockoutNotify, lastLockoutNotify
	authLockoutNotify = nil
	lastLockoutNotify = map[string]time.Time{}
	lockoutNotifyMu.Unlock()
	t.Cleanup(func() {
		lockoutNotifyMu.Lock()
		authLockoutNotify, lastLockoutNotify = prevHook, prevMap
		lockoutNotifyMu.Unlock()
	})

	calls := 0
	lockoutNotifyMu.Lock()
	authLockoutNotify = func(ip string) { calls++ }
	lockoutNotifyMu.Unlock()

	notifyLockoutOnce("1.2.3.4")
	notifyLockoutOnce("1.2.3.4") // deduped
	notifyLockoutOnce("5.6.7.8") // distinct IP -> new alert
	if calls != 2 {
		t.Fatalf("lockout notify calls = %d, want 2 (one per IP per window)", calls)
	}
}
