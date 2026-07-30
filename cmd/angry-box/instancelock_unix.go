//go:build !windows

package main

// instancelock_unix.go — Unix single-instance lock via flock(2).
//
// flock(LOCK_EX|LOCK_NB) on a sibling .lock file is the right primitive here:
//   - it is automatically released when the holding process exits or crashes
//     (fd close / process death), so a crashed daemon never leaves a stale lock
//     that blocks restarts (unlike a plain pidfile);
//   - LOCK_NB makes the attempt non-blocking — a contended lock returns
//     EWOULDBLOCK immediately instead of hanging serve;
//   - the lock is per-file (the .lock sibling), so it does not interfere with
//     the store's atomic write-rename.
//
// The .lock file also carries the holder's PID (written after acquiring) so the
// refused second instance can report WHO is holding it.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// AcquireInstanceLock takes an exclusive, non-blocking lock on
// <storePath>.lock. On success it returns a release func the caller MUST defer.
// On contention it returns an *ErrInstanceLocked describing the holder. The
// store's directory is created (best-effort) so the canonical absolute default
// works on a fresh system where /var/lib/angry-box may not exist yet.
func AcquireInstanceLock(storePath string) (release func(), err error) {
	lockPath := storePath + ".lock"
	if dir := filepath.Dir(storePath); dir != "" && dir != "." {
		// Best-effort: a missing dir is not fatal for a relative store; for the
		// canonical absolute default the caller (serve) validates writability.
		_ = os.MkdirAll(dir, 0o755)
	}

	// O_CREATE so the file exists; O_RDWR so we can write our PID after locking.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("instance lock: open %s: %w", lockPath, err)
	}

	// Non-blocking exclusive flock. EAGAIN/EWOULDBLOCK means someone else holds it.
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		holder := readLockHolder(f)
		f.Close()
		return nil, &ErrInstanceLocked{
			StorePath: storePath,
			HolderPID: holder,
			LockPath:  lockPath,
		}
	}

	// We hold the lock. Record our PID (truncate + write) for the next caller.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	// Keep the offset sane in case something reads it later.
	_, _ = f.Seek(0, 0)

	release = func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
	return release, nil
}

// readLockHolder reads the PID previously written by the holder. Best-effort —
// an old/empty/corrupt file yields "" (the caller reports "unknown").
func readLockHolder(f *os.File) string {
	buf := make([]byte, 32)
	_, _ = f.Seek(0, 0)
	n, _ := f.Read(buf)
	_, _ = f.Seek(0, 0)
	s := strings.TrimSpace(string(buf[:n]))
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	return ""
}
