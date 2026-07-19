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
