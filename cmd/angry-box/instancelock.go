package main

// instancelock.go — single-instance enforcement for `angry-box serve`.
//
// Two `angry-box serve` processes against the SAME store file diverge the
// fleet's state (the root cause of a tester's split-brain: one daemon from
// systemd, one launched by hand, each with its own store, each pushing a
// different config — user keys drift, nodes "won't connect"). The lock is held
// for the lifetime of the serve process and released on exit (or automatically
// by the OS if the process crashes — flock on Unix auto-releases on fd close /
// process death, so it is more reliable than a pidfile).
//
// The lock file is a SIBLING of the store: <storePath>.lock (NOT the store
// itself), so it never interferes with the store's atomic write-rename.
//
// Behavior: a second instance holding the same store is REFUSED with a clear,
// actionable error. An operator who genuinely needs a second instance must use
// a different --file (and therefore a different .lock).

// ErrInstanceLocked is returned by AcquireInstanceLock when another serve
// process already holds the lock for the requested store. Its message is safe
// to print verbatim to the operator.
type ErrInstanceLocked struct {
	StorePath string // the store path that is locked
	HolderPID string  // PID of the process holding the lock (best-effort, may be "")
	LockPath  string  // the .lock file path, for the operator
}

func (e *ErrInstanceLocked) Error() string {
	pid := e.HolderPID
	if pid == "" {
		pid = "unknown"
	}
	return "angry-box already running (PID " + pid + "), store locked: " + e.StorePath +
		". Stop the other instance, or run this one with a different --file."
}
