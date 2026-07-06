package chain

// errors_test.go — verifies the sentinel not-found errors are matchable via
// errors.Is so callers can programmatically distinguish not-found from other
// errors. CTO-review §2 finding.

import (
	"errors"
	"testing"
)

func TestSentinelErrors_HostNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetHost("nonexistent")
	if err == nil {
		t.Fatal("GetHost nonexistent: expected error, got nil")
	}
	if !errors.Is(err, ErrHostNotFound) {
		t.Errorf("GetHost error is not ErrHostNotFound: %v", err)
	}
}

func TestSentinelErrors_ChainNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetChain("nonexistent")
	if err == nil {
		t.Fatal("GetChain nonexistent: expected error, got nil")
	}
	if !errors.Is(err, ErrChainNotFound) {
		t.Errorf("GetChain error is not ErrChainNotFound: %v", err)
	}
}

func TestSentinelErrors_UserNotFound(t *testing.T) {
	st := newTestStore(t)
	_, err := st.GetUser("nonexistent")
	if err == nil {
		t.Fatal("GetUser nonexistent: expected error, got nil")
	}
	if !errors.Is(err, ErrUserNotFound) {
		t.Errorf("GetUser error is not ErrUserNotFound: %v", err)
	}
}