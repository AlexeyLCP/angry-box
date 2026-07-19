package chain

// chain_entry_material.go — materialization of chain entry inbounds from
// their referenced InboundProfile onto the entry nodes (v2 levels model).
//
// The profile is the node-independent template; each entry node carries a
// materialized NodeInbound (ProfileID == ChainNode.InboundRef) holding the
// per-node credentials the render and client-link paths read. Materialization
// is idempotent and credential-preserving: an existing materialized inbound
// is kept as-is (Rule 5 — clients never reconfigure on redeploy); only
// missing pieces (a new node's creds, stale AWG obfs material) are filled.

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// EnsureChainEntryMaterialization makes every entry-level node of a levelized
// chain carry the materialized inbound its InboundRef points at. Called by
// ApplyChain before the deploy loop so a chain created without the UI's
// materialization step (CLI, relocated node, wiped NodeInfo) self-heals at
// apply time. Legacy (non-levelized) chains are a no-op — they render from
// the chain's own fields.
func EnsureChainEntryMaterialization(store *Store, c *model.Chain, preset ConnectionPreset) error {
	if !c.IsLevelized() {
		return nil
	}
	for ni := range c.Levels[0].Nodes {
		n := &c.Levels[0].Nodes[ni]
		if n.InboundRef == "" {
			continue // legacy-style entry without a profile reference
		}
		prof, err := store.GetInboundProfile(n.InboundRef)
		if err != nil {
			return fmt.Errorf("chain %q entry node %q: %w", c.Name, n.ID, err)
		}
		nodeInfo, err := store.GetNodeInfo(n.ID)
		if err != nil {
			nodeInfo = &model.NodeInfo{Host: model.Host{ID: n.ID, Addr: n.Addr, User: n.User, KeyPath: n.KeyPath}}
		}
		if ensureMaterializedEntryInbound(nodeInfo, c, n, prof, preset) {
			if err := store.SaveNodeInfo(nodeInfo); err != nil {
				return fmt.Errorf("chain %q entry node %q: save materialized inbound: %w", c.Name, n.ID, err)
			}
		}
	}
	return nil
}

// ensureMaterializedEntryInbound upserts the chain-entry NodeInbound on the
// node (keyed by ProfileID). Returns true when the NodeInfo changed and must
// be persisted. Existing inbounds are only topped up (missing creds / stale
// AWG material), never regenerated.
func ensureMaterializedEntryInbound(ni *model.NodeInfo, c *model.Chain, entry *model.ChainNode, prof *model.InboundProfile, preset ConnectionPreset) bool {
	for i := range ni.Inbounds {
		ib := &ni.Inbounds[i]
		if ib.ProfileID != prof.ID {
			continue
		}
		changed := false
		// Top up missing per-node credentials (e.g. a relocated node whose
		// NodeInfo was rebuilt from the host alone).
		if ib.Protocol == string(model.UserProtocolAWG) {
			if ib.ServerPrivKey == "" {
				priv, pub, err := generateWireGuardKeypair()
				if err == nil {
					ib.ServerPrivKey = priv
					ib.ServerPubKey = pub
					changed = true
				}
			}
			before := ib.AWGCPSI1
			EnsureInboundAWGMaterial(ib, preset)
			if ib.AWGCPSI1 != before {
				changed = true
			}
		} else if ib.UUID == "" {
			ib.UUID = generateStableUUID()
			changed = true
		}
		return changed
	}

	// Not materialized yet — create it. For AWG chains prefer the chain's
	// existing entry keypair (a chain that rendered from chain fields before
	// keeps its clients) and only generate fresh creds for genuinely new
	// chains.
	ib := model.NodeInbound{
		Protocol:    prof.Protocol,
		Port:        chainEntryPort(c, entry.ID),
		Obfuscation: prof.Obfuscation,
		Source:      "chain:" + c.Name,
		Tag:         prof.ID,
		ProfileID:   prof.ID,
	}
	switch prof.Protocol {
	case string(model.UserProtocolAWG):
		if c.AWGEntryServerPriv != "" {
			ib.ServerPrivKey = c.AWGEntryServerPriv
			ib.ServerPubKey = c.AWGEntryServerPub
		} else if priv, pub, err := generateWireGuardKeypair(); err == nil {
			ib.ServerPrivKey = priv
			ib.ServerPubKey = pub
		}
		ib.AWGServerAddress = "10.8.0.1/24"
		EnsureInboundAWGMaterial(&ib, preset)
	case string(model.UserProtocolTUIC):
		ib.UUID = c.TUICEntryUserUUID
	default: // vless-reality: the chain entry historically used the entry's transit UUID
		ib.UUID = entry.TransitUUID
		if ib.UUID == "" {
			ib.UUID = generateStableUUID()
		}
	}
	ni.Inbounds = append(ni.Inbounds, ib)
	return true
}
