package chain

// atomicwrite_test.go pins the atomic-write contract for Store persistence: a
// crash mid-write must not corrupt the store file. atomicWriteFile writes to a
// sibling temp file and renames it into place, so the destination is either the
// previous contents or the full new contents — never a partial write (CTO-
// review M3).

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAtomicWriteFile_ReplacesContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := os.WriteFile(path, []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWriteFile(path, []byte(`{"new":2}`), 0o600); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != `{"new":2}` {
		t.Errorf("content = %q, want %q", got, `{"new":2}`)
	}
}

func TestAtomicWriteFile_LeavesNoTempArtifacts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := atomicWriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	// Only the destination file should remain; no leftover .tmp siblings.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "store.json" {
			continue
		}
		t.Errorf("unexpected leftover temp artifact: %q", e.Name())
	}
}

func TestAtomicWriteFile_PreservesPermissionsOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	if err := atomicWriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("atomicWriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("perms = %o, want 0600", mode)
	}
}

func TestAtomicWriteFile_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "store.json")
	if err := atomicWriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("atomicWriteFile should create parent dirs, got: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "{}") {
		t.Errorf("unexpected content: %q", got)
	}
}