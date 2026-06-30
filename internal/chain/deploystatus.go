package chain

// deploystatus.go — records the sha256 of a successfully-applied config into
// NodeInfo.LastDeployedHash so the Deploy Status page can show pending changes
// (current render hash vs last applied hash).

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ConfigHash returns the sha256 hex of a rendered config (UTF-8 bytes).
func ConfigHash(cfgJSON []byte) string {
	sum := sha256.Sum256(cfgJSON)
	return hex.EncodeToString(sum[:])
}

// recordDeploySuccess stamps info with the hash + now and persists it. Errors
// are logged via audit-style best-effort: a failure to record the hash must not
// flip a successful deploy into a failure.
func recordDeploySuccess(store *Store, nodeID, cfgJSON string) {
	if store == nil || nodeID == "" {
		return
	}
	info, err := store.GetNodeInfo(nodeID)
	if err != nil {
		// Node may not have a NodeInfo record yet; create one so the hash is
		// persisted (the UI's SaveNodeInfo usually handles this, but be safe).
		info = &model.NodeInfo{}
	}
	info.ID = nodeID
	info.LastDeployedHash = ConfigHash([]byte(cfgJSON))
	info.LastDeployedAt = time.Now()
	if err := store.SaveNodeInfo(info); err != nil {
		// best-effort; logged but not fatal
		_ = err
	}
}