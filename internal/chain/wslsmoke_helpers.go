//go:build wsl_smoke

package chain

// wslsmoke_helpers_test.go — exported shims so the wslsmoke test package (which
// cannot use internal chain unexported helpers without an import cycle) can
// drive pushConfig, recordDeploySuccess, hostLock, and a temp-backed store.
// Guarded by the wsl_smoke build tag — never in production.

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// PushConfigForTest wraps the unexported pushConfig for the WSL smoke tests.
// useSudo mirrors NodeInfo.UseSudo (true for non-root SSH users).
func PushConfigForTest(client *sshclient.Client, cfgContent string, useSudo bool) (string, error) {
	return pushConfig(client, cfgContent, useSudo)
}

// RecordDeploySuccessForTest wraps recordDeploySuccess for the smoke tests.
func RecordDeploySuccessForTest(store *Store, nodeID, cfgJSON string) {
	recordDeploySuccess(store, nodeID, cfgJSON)
}

// HostLockForTest wraps the per-host auto-apply lock; returns the *sync.Mutex
// for nodeID so the smoke test can assert identity across calls.
func HostLockForTest(nodeID string) *sync.Mutex {
	return hostLock(nodeID)
}

// NewTestStoreForSmoke returns a Store backed by a fresh temp dir.
func NewTestStoreForSmoke() *Store {
	dir, err := os.MkdirTemp("", "wslsmoke-*")
	if err != nil {
		panic(err)
	}
	return NewStore(filepath.Join(dir, "smoke-store.json"))
}

// keep imports used (model is referenced by callers transitively)
var _ = model.NodeInfo{}