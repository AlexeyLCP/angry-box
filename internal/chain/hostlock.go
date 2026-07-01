package chain

// hostlock.go provides the per-host serialization primitive that all deploy
// entry points (CLI apply / apply-chain, web handlers, background auto-apply)
// must pass through. Two concurrent applies targeting the same node would
// otherwise interleave their SSH backup->write->restart sequences and corrupt
// the rollback chain the panel relies on (CTO-review C2). By routing every
// deploy through withHostLock, the per-node mutex serializes access regardless
// of which entry point initiated it.

import "sync"

var (
	hostLocksMu sync.Mutex
	hostLocks   = map[string]*sync.Mutex{}
)

// hostLock returns the per-host mutex, lazily creating it.
func hostLock(nodeID string) *sync.Mutex {
	hostLocksMu.Lock()
	defer hostLocksMu.Unlock()
	mu, ok := hostLocks[nodeID]
	if !ok {
		mu = &sync.Mutex{}
		hostLocks[nodeID] = mu
	}
	return mu
}

// withHostLock runs fn while holding the per-host mutex for nodeID, ensuring
// that no other apply (CLI, web, or background auto-apply) touches the same
// node concurrently. Different nodeIDs run concurrently. The function's
// return value is propagated to the caller so applier methods can return their
// errors through the lock boundary.
func withHostLock[T any](nodeID string, fn func() T) T {
	mu := hostLock(nodeID)
	mu.Lock()
	defer mu.Unlock()
	return fn()
}