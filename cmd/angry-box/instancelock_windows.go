//go:build windows

package main

// instancelock_windows.go — Windows single-instance lock via LockFileEx.
//
// Windows has no flock; the closest equivalent is a byte-range lock via
// LockFileEx (LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY) on a sibling
// .lock file. Like flock it is automatically released when the holding process
// exits or the handle is closed, so a crashed daemon leaves no stale lock.
//
// This product's primary platform is Linux (orchestrator on a VPS); Windows
// serve is a development/convenience path, but the same split-brain risk
// applies, so the lock is enforced here too.

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx     = kernel32.NewProc("LockFileEx")
	procUnlockFileEx   = kernel32.NewProc("UnlockFileEx")
)

const (
	windowsLockfileExclusive       = 0x00000002 // LOCKFILE_EXCLUSIVE_LOCK
	windowsLockfileFailImmediately = 0x00000001 // LOCKFILE_FAIL_IMMEDIATELY
)

// AcquireInstanceLock takes an exclusive, non-blocking byte-range lock on
// <storePath>.lock. On success it returns a release func the caller MUST defer.
// On contention it returns an *ErrInstanceLocked. The store's directory is
// created best-effort.
func AcquireInstanceLock(storePath string) (release func(), err error) {
	lockPath := storePath + ".lock"
	if dir := filepath.Dir(storePath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}

	// FILE_SHARE read|write|delete so another process can at least open the
	// file (to read/report the holder), but the LockFileEx range is still
	// exclusive.
	createFlags := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	utf16, _ := windows.UTF16PtrFromString(lockPath)
	handle, err := windows.CreateFile(
		utf16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		createFlags,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("instance lock: open %s: %w", lockPath, err)
	}

	// Lock byte range [0,1) exclusively, non-blocking.
	ol := new(syscall.Overlapped)
	r1, _, _ := procLockFileEx.Call(
		uintptr(handle),
		windowsLockfileExclusive|windowsLockfileFailImmediately,
		0,
		1, 0, // number of bytes (low, high) to lock = 1
		uintptr(unsafe.Pointer(ol)),
	)
	if r1 == 0 {
		windows.CloseHandle(handle)
		return nil, &ErrInstanceLocked{
			StorePath: storePath,
			HolderPID: readHolderFromFile(lockPath),
			LockPath:  lockPath,
		}
	}

	// Record our PID for the next caller (informational; the real lock is the
	// byte range, not the content).
	_ = os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)

	release = func() {
		ol2 := new(syscall.Overlapped)
		// UnlockFileEx takes the byte count as the 3rd param (in the reserved
		// 2-arg slot of the syscall) — 1 byte to match the lock.
		_, _, _ = procUnlockFileEx.Call(
			uintptr(handle),
			0,
			1, 0,
			uintptr(unsafe.Pointer(ol2)),
		)
		_ = windows.CloseHandle(handle)
	}
	return release, nil
}

// readHolderFromFile reads the PID a prior holder wrote. Best-effort.
func readHolderFromFile(lockPath string) string {
	b, err := os.ReadFile(lockPath)
	if err != nil {
		return ""
	}
	s := stringsTrimSpace(string(b))
	return s
}

func stringsTrimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
