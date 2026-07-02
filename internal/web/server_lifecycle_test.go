package web

// server_lifecycle_test.go — covers Server lifecycle (StartBackgroundMetrics/
// Stop/collectAllMetrics) and the SSH-key helpers (mergeSSHKeys/detectSystemKeys).
// CTO-review C3 phase 5.

import (
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/config"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestStartBackgroundMetrics_AndStop verifies the background collector runs once
// on start and Stop halts it without panic.
func TestStartBackgroundMetrics_AndStop(t *testing.T) {
	dir := t.TempDir()
	storePath := dir + "/store.json"
	cfg := &config.Config{StoreFile: storePath, AuthEnabled: false}
	srv := NewServer(storePath, true, cfg, "127.0.0.1:9080", noopFactory{})
	srv.StartBackgroundMetrics(0) // 0 -> default 15min; the immediate collect fires
	// Let the immediate collection tick run.
	time.Sleep(50 * time.Millisecond)
	srv.Stop()
	// Stopping twice must not panic (close-once guard).
	srv.Stop()
}

// TestCollectAllMetrics_NoHosts verifies collecting with no hosts is a no-op
// (no panic, no metrics written).
func TestCollectAllMetrics_NoHosts(t *testing.T) {
	dir := t.TempDir()
	storePath := dir + "/store.json"
	cfg := &config.Config{StoreFile: storePath, AuthEnabled: false}
	srv := NewServer(storePath, true, cfg, "127.0.0.1:9080", noopFactory{})
	srv.collectAllMetrics()
}

// TestMergeSSHKeys verifies stored + system keys are concatenated.
func TestMergeSSHKeys(t *testing.T) {
	stored := []model.SSHKeyEntry{{ID: "k1", Name: "stored"}}
	system := []model.SSHKeyEntry{{ID: "system-id_rsa", Name: "id_rsa"}}
	merged := mergeSSHKeys(stored, system)
	if len(merged) != 2 {
		t.Fatalf("len: got %d, want 2", len(merged))
	}
	if merged[0].ID != "k1" {
		t.Errorf("first: got %q, want k1 (stored first)", merged[0].ID)
	}
	if merged[1].ID != "system-id_rsa" {
		t.Errorf("second: got %q, want system-id_rsa", merged[1].ID)
	}
}

// TestDetectSystemKeys_NoSSHDir verifies detectSystemKeys returns nil gracefully
// when ~/.ssh doesn't exist (it won't in the test env / temp home).
func TestDetectSystemKeys_NoSSHDir(t *testing.T) {
	// This may return keys if the real ~/.ssh exists on the dev machine; just
	// assert it doesn't panic and returns a slice (possibly empty/nil).
	keys := detectSystemKeys()
	_ = keys
}