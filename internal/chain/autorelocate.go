package chain

// autorelocate.go — P2b auto-relocate decision layer. When the health state
// machine (nodehealth.go) transitions a node to down/unreachable, the
// background metrics loop (internal/web/server.go collectAllMetrics) asks
// AutoRelocateDecision whether the node should be relocated onto a warm-pool
// spare, and if so runs chain.RelocateNode with the spare's address (the SSH
// deploy lives in relocate.go — this file is the pure, SSH-free decision
// part, so it is fully unit-testable).
//
// Guardrails (all must hold, in check order):
//  1. Global master switch: PanelSettings.AutoRelocate.Enabled.
//  2. Per-node opt-in: NodeInfo.AutoRelocate.
//  3. Cooldown: LastAutoRelocateAt older than CooldownHours (default 6).
//  4. A spare exists: NodeInfo.Spare && not the node itself && not in any
//     chain && no user-facing inbounds.
//
// Every outcome (taken or skipped) is auditable — the caller writes the
// audit entry with the returned reason.

import (
	"errors"
	"fmt"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// DefaultAutoRelocateCooldown is the minimum interval between two
// auto-relocations of the same node when CooldownHours is unset (6h — long
// enough to break relocation loops on a flapping VPS, short enough to heal a
// node that dies twice in a week).
const DefaultAutoRelocateCooldown = 6 * time.Hour

// AutoRelocateDecision reports whether the node should be auto-relocated now
// and, when yes, which spare to consume. reason is a short machine-stable
// string for the audit log ("disabled-global", "no-spare", ...).
func AutoRelocateDecision(st *Store, nodeID string, now time.Time) (spare *model.NodeInfo, reason string, ok bool) {
	settings, err := st.GetSettings()
	if err != nil {
		return nil, fmt.Sprintf("settings-error: %v", err), false
	}
	if settings.AutoRelocate == nil || !settings.AutoRelocate.Enabled {
		return nil, "disabled-global", false
	}
	node, err := st.GetNodeInfo(nodeID)
	if err != nil {
		return nil, fmt.Sprintf("node-error: %v", err), false
	}
	if node.Spare {
		return nil, "is-spare", false // never relocate inventory itself
	}
	if !node.AutoRelocate {
		return nil, "disabled-node", false
	}
	cooldown := DefaultAutoRelocateCooldown
	if settings.AutoRelocate.CooldownHours > 0 {
		cooldown = time.Duration(settings.AutoRelocate.CooldownHours) * time.Hour
	}
	if !node.LastAutoRelocateAt.IsZero() && now.Sub(node.LastAutoRelocateAt) < cooldown {
		return nil, "cooldown", false
	}
	spare, err = PickSpare(st, nodeID)
	if err != nil {
		return nil, fmt.Sprintf("spare-error: %v", err), false
	}
	if spare == nil {
		return nil, "no-spare", false
	}
	return spare, "go", true
}

// PickSpare returns the first warm-pool spare eligible for consumption: marked
// NodeInfo.Spare, not the excluded node, referenced by no chain, and with no
// user-facing inbounds (a spare carrying users is not spare capacity). Order
// is store order (operator controls priority by creation order). Returns
// (nil, nil) when the pool is empty.
func PickSpare(st *Store, excludeNodeID string) (*model.NodeInfo, error) {
	nodes, err := st.ListNodeInfos()
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range nodes {
		if !n.Spare || n.ID == excludeNodeID {
			continue
		}
		if len(n.Inbounds) > 0 {
			continue
		}
		chains, err := st.GetChainsForNode(n.ID)
		if err != nil {
			return nil, fmt.Errorf("chains for spare %q: %w", n.ID, err)
		}
		if len(chains) > 0 {
			continue
		}
		return n, nil
	}
	return nil, nil
}

// ConsumeSpare removes a spare's NodeInfo+Metrics and Host record after its
// address was taken over by a relocated node. Best-effort: the relocation
// itself already succeeded, so leftovers are cosmetic (a stale duplicate
// NodeInfo sharing the address) — errors are returned for the audit log but
// should not fail the operation. Idempotent: already-removed records are not
// an error.
func ConsumeSpare(st *Store, spareID string) error {
	if err := st.DeleteNodeInfo(spareID); err != nil {
		return fmt.Errorf("delete spare nodeinfo %q: %w", spareID, err)
	}
	if err := st.DeleteHost(spareID); err != nil && !errors.Is(err, ErrHostNotFound) {
		return fmt.Errorf("delete spare host %q: %w", spareID, err)
	}
	return nil
}
