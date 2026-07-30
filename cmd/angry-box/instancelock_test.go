package main

// instancelock_test.go — pins the single-instance contract: a second
// AcquireInstanceLock against the same store path is REFUSED with an
// *ErrInstanceLocked (the split-brain guard), and releasing lets a new acquire
// succeed. The holder PID is surfaced for the actionable error message.

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireInstanceLock_SecondIsRefused(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store.json")

	// First acquire succeeds.
	release1, err := AcquireInstanceLock(store)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	t.Cleanup(release1)

	// Second acquire against the SAME store must be refused.
	_, err2 := AcquireInstanceLock(store)
	if err2 == nil {
		t.Fatal("second acquire should have been refused (another instance holds the lock)")
	}
	var le *ErrInstanceLocked
	if !errors.As(err2, &le) {
		t.Fatalf("second acquire error should be *ErrInstanceLocked, got %T: %v", err2, err2)
	}
	if !strings.Contains(le.Error(), "already running") {
		t.Errorf("error message should mention 'already running', got: %s", le.Error())
	}
	if !strings.Contains(le.Error(), "store locked") {
		t.Errorf("error message should mention the locked store, got: %s", le.Error())
	}
}

func TestAcquireInstanceLock_ReleaseAllowsReacquire(t *testing.T) {
	store := filepath.Join(t.TempDir(), "store.json")

	release1, err := AcquireInstanceLock(store)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	release1()

	// After release, a fresh acquire must succeed (no stale lock — the whole
	// point of flock/LockFileEx over a bare pidfile).
	release2, err := AcquireInstanceLock(store)
	if err != nil {
		t.Fatalf("reacquire after release: %v", err)
	}
	release2()
}

func TestAcquireInstanceLock_DifferentStoresIndependent(t *testing.T) {
	// Two different stores = two independent locks (an operator CAN run two
	// instances with explicit different --file paths).
	a := filepath.Join(t.TempDir(), "a.json")
	b := filepath.Join(t.TempDir(), "b.json")
	ra, err := AcquireInstanceLock(a)
	if err != nil {
		t.Fatalf("acquire A: %v", err)
	}
	t.Cleanup(ra)
	rb, err := AcquireInstanceLock(b)
	if err != nil {
		t.Fatalf("acquire B should succeed (different store): %v", err)
	}
	rb()
}
