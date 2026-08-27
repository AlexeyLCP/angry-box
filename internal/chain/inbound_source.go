package chain

// inbound_source.go — helpers to distinguish chain-owned inbounds from
// operator-owned (standalone/profile) ones on NodeInfo.Inbounds.
//
// Chain-entry materialized inbounds (Source "chain:<name>") live on
// NodeInfo.Inbounds so per-node credentials persist next to the node — but
// they are rendered via the CHAIN role path (renderChainEntryAWGConf /
// buildChainRoleInOut), so every standalone-inbound loop must skip them to
// avoid double renders, double port claims, and phantom awg1 interfaces.

import (
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// IsChainSourcedInbound reports whether the inbound is chain-owned
// (Source "chain:<name>"). Chain-owned inbounds are read-only views the
// chain machinery renders from; standalone loops skip them. Exported: the
// web layer needs the same check when listing a node's standalone inbounds.
func IsChainSourcedInbound(ib *model.NodeInbound) bool {
	return strings.HasPrefix(ib.Source, "chain:")
}

// IsChainEntryInbound reports whether the inbound is rendered as a chain's
// ENTRY listener on this node: its ProfileID is referenced by an entry-level
// (level 0) ChainNode.InboundRef in one of the node's chains. The chain role
// path (renderChainEntryAWGConf / buildChainRoleInOut) renders the listener
// from the materialized inbound — every standalone render/claim loop must
// skip it too, or the same listener is emitted TWICE on the same port (the
// live deploy failure class: chain entry on awg0 + duplicate on awg1).
//
// Unlike IsChainSourcedInbound (a static Source flag), this is the REFERENCE
// check — profile inbounds keep Source="standalone" when shared between
// standalone use and chain entries, so Source alone cannot detect them.
func IsChainEntryInbound(nodeChains []*model.Chain, nodeID string, ib *model.NodeInbound) bool {
	if ib.ProfileID == "" {
		return false
	}
	for _, c := range nodeChains {
		if !c.IsLevelized() || len(c.Levels) == 0 {
			continue
		}
		for _, n := range c.Levels[0].Nodes {
			if n.ID == nodeID && n.InboundRef == ib.ProfileID {
				return true
			}
		}
	}
	return false
}

// inboundByProfileID finds the materialized inbound for a profile on the
// node, or nil — the per-node credentials the InboundRef render paths read.
func inboundByProfileID(nodeInfo *model.NodeInfo, profileID string) *model.NodeInbound {
	if nodeInfo == nil || profileID == "" {
		return nil
	}
	for i := range nodeInfo.Inbounds {
		if nodeInfo.Inbounds[i].ProfileID == profileID {
			return &nodeInfo.Inbounds[i]
		}
	}
	return nil
}

// chainEntryAWG3Inbound returns the materialized chain-entry AWG inbound on the
// node when it has AWG 3.0 mode on (AGENTS #5), or nil otherwise. v2 chains
// resolve it via the entry ChainNode.InboundRef (a profile ID); legacy chains
// match by Source == "chain:"+chain.Name. Returns nil when nodeInfo is nil or
// the entry inbound is not in AWG3 mode — the caller falls back to the default
// kernel-awg0 path.
func chainEntryAWG3Inbound(nodeInfo *model.NodeInfo, c *model.Chain, entry *model.ChainNode) *model.NodeInbound {
	if nodeInfo == nil {
		return nil
	}
	for i := range nodeInfo.Inbounds {
		ib := &nodeInfo.Inbounds[i]
		if ib.Protocol != "awg" {
			continue
		}
		match := false
		if entry != nil && entry.InboundRef != "" {
			match = ib.ProfileID == entry.InboundRef
		}
		if !match {
			match = ib.Source == "chain:"+c.Name
		}
		if match && model.IsAWG3Family(ib.EffectiveAWGVersion()) {
			return ib
		}
	}
	return nil
}
