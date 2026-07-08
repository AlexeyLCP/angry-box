package model

import (
	"testing"
	"time"
)

// TestComputeStatus covers every branch of User.ComputeStatus: disabled,
// expired, on_hold (start_on_first_use before first fetch), and active. The
// "limited" state is intentionally NOT produced here — it is set only by the
// future P0b-2 stats poller, never by ComputeStatus itself.
func TestComputeStatus(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)

	tests := []struct {
		name string
		u    User
		want string
	}{
		{"active by default", User{Active: true}, "active"},
		{"disabled when inactive", User{Active: false}, "disabled"},
		{"disabled wins over expired", User{Active: false, ExpiresAt: past}, "disabled"},
		{"expired when active + past expiry", User{Active: true, ExpiresAt: past}, "expired"},
		{"active when future expiry", User{Active: true, ExpiresAt: future}, "active"},
		{"on_hold when start_on_first_use and never fetched", User{Active: true, ExpireStrategy: "start_on_first_use"}, "on_hold"},
		{"active when start_on_first_use and already fetched", User{Active: true, ExpireStrategy: "start_on_first_use", FirstUseAt: time.Now()}, "active"},
		{"active when fixed_date strategy with no expiry set", User{Active: true, ExpireStrategy: "fixed_date"}, "active"},
		{"expired when fixed_date strategy and past expiry", User{Active: true, ExpireStrategy: "fixed_date", ExpiresAt: past}, "expired"},
		{"active when never strategy ignores expiry", User{Active: true, ExpireStrategy: "never", ExpiresAt: past}, "active"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.u.ComputeStatus(); got != tt.want {
				t.Errorf("ComputeStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestIsExpired is a sanity check on the existing helper used by ComputeStatus.
func TestIsExpired(t *testing.T) {
	zero := User{}
	if zero.IsExpired() {
		t.Error("zero ExpiresAt must not be expired")
	}
	past := User{ExpiresAt: time.Now().Add(-time.Second)}
	if !past.IsExpired() {
		t.Error("past ExpiresAt must be expired")
	}
	future := User{ExpiresAt: time.Now().Add(time.Hour)}
	if future.IsExpired() {
		t.Error("future ExpiresAt must not be expired")
	}
}