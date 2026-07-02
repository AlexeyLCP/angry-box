package chain

// tempdir_test.go provides a Windows-friendly temp-directory helper for tests.
// t.TempDir() registers a cleanup that calls os.RemoveAll once; on Windows a
// just-closed/renamed file handle can still be held by the OS for a few
// milliseconds, so RemoveAll fails with "The directory is not empty" even
// though the test logic itself passed. tempDir registers a retried cleanup so
// flaky cleanup does not turn a green test red.

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// tempDir creates a fresh temp directory and registers a retried removal
// cleanup. Use this instead of t.TempDir() in tests that drive atomicWriteFile
// (the Store) heavily, where Windows handle-release races with RemoveAll.
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ab-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() {
		// On non-Windows RemoveAll is reliably immediate; on Windows retry a few
		// times to let the OS release recently-closed file handles.
		if runtime.GOOS != "windows" {
			_ = os.RemoveAll(dir)
			return
		}
		for attempt := 0; attempt < 10; attempt++ {
			if err := os.RemoveAll(dir); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = os.RemoveAll(dir) // best effort
	})
	return dir
}

// tempStoreFile returns a store path inside a tempDir (retried cleanup).
func tempStoreFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(tempDir(t), "store.json")
}