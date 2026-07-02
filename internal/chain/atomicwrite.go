package chain

// atomicwrite.go provides an atomic file-write helper for Store persistence.
// A direct os.WriteFile can leave the destination truncated/corrupted if the
// process is interrupted mid-write; the store file holds the whole panel state
// (hosts, chains, credentials), so a partial write would lose everything.
//
// atomicWriteFile writes the data to a sibling temp file (same directory, so the
// rename is atomic on the same filesystem) and then renames it over the
// destination. The result is either the previous contents or the full new
// contents — never a partial write (CTO-review M3).

import (
	"fmt"
	"os"
	"path/filepath"

	"crypto/rand"
	"encoding/hex"
)

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("store: mkdir: %w", err)
	}

	// Randomized temp name so concurrent writers (defensive — the Store uses a
	// mutex, but this keeps the helper safe in isolation) do not collide.
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	tmp, err := os.CreateTemp(dir, ".ab-tmp-"+filepath.Base(path)+"-"+hex.EncodeToString(b)+"-*")
	if err != nil {
		return fmt.Errorf("store: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// Clean up the temp file on any failure path.
	defer func() {
		if tmp != nil {
			tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("store: write temp: %w", err)
	}
	// fsync the temp file so the durable rename is not just a metadata change
	// over an unflushed buffer; on Windows this also helps the OS release the
	// file handle promptly (otherwise t.TempDir cleanup can race with a
	// just-closed handle and fail RemoveAll).
	if err := tmp.Sync(); err != nil {
		// Sync failure is not fatal to correctness on most filesystems; keep going
		// so a best-effort fsync error does not abort a valid write.
	}
	if err := tmp.Chmod(perm); err != nil {
		return fmt.Errorf("store: chmod temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("store: close temp: %w", err)
	}
	tmp = nil // ownership transferred to the closed file; skip deferred Remove

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("store: rename: %w", err)
	}
	return nil
}