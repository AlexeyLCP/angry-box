package chain

// profile_deploy.go — materialization of InboundProfiles onto nodes with an
// explicit diff semantic (the "which nodes is this profile on" write path).
//
// Source of truth for placement is NodeInbound.ProfileID; this is the ONLY
// writer that adds/removes those links. Rules honored:
//   - credentials are generated EXACTLY ONCE per (profile, node) — re-saving
//     a profile never rotates a kept node's keys (clients don't break);
//   - removing a node whose chain references the profile via InboundRef is
//     REFUSED (the chain must be edited first);
//   - removing a node with users on the inbound is allowed but reported
//     (the UI warns "N clients lose this config");
//   - parameter changes (port/preset) touch only the materialized inbound —
//     creds preserved — and mark the node for re-apply.

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// ProfileDeployResult reports what ApplyProfileToNodes changed.
type ProfileDeployResult struct {
	Added      []string        // nodes that got a fresh materialized inbound
	Removed    []string        // nodes whose materialized inbound was dropped
	Updated    []string        // kept nodes whose port/preset changed (re-apply)
	Blocked    []string        // removals refused: a chain references the profile here
	UsersLostOn map[string]int // nodeID → users that lose access (removed with users)
}

// AffectedNodes returns every node that needs a re-deploy (added, removed,
// updated) — blocked nodes are untouched.
func (r *ProfileDeployResult) AffectedNodes() []string {
	out := make([]string, 0, len(r.Added)+len(r.Removed)+len(r.Updated))
	out = append(out, r.Added...)
	out = append(out, r.Removed...)
	out = append(out, r.Updated...)
	return out
}

// ApplyProfileToNodes diffs the desired node list against the current
// materialization and applies the delta. It also syncs port/preset on kept
// nodes (credentials preserved). The profile itself must already be saved
// (SaveInboundProfile) — this only touches NodeInfo materializations.
func ApplyProfileToNodes(st *Store, prof *model.InboundProfile, desired []string) (*ProfileDeployResult, error) {
	res := &ProfileDeployResult{UsersLostOn: map[string]int{}}
	current := st.ProfileNodes(prof.ID)
	curSet := map[string]bool{}
	for _, id := range current {
		curSet[id] = true
	}
	desSet := map[string]bool{}
	for _, id := range desired {
		desSet[id] = true
	}

	chains, err := st.ListChains()
	if err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}

	// ── pre-flight: validate ALL additions before mutating anything, so a
	// port conflict can't leave a half-applied diff (removals done, adds
	// failed). NodeInfos created here are reused by the addition pass.
	addTargets := map[string]*model.NodeInfo{}
	for _, nodeID := range desired {
		if curSet[nodeID] {
			continue
		}
		ni, err := st.GetNodeInfo(nodeID)
		if err != nil {
			host, herr := st.GetHost(nodeID)
			if herr != nil {
				return res, fmt.Errorf("node %q: %w", nodeID, herr)
			}
			ni = &model.NodeInfo{Host: *host}
		}
		if conflict := inboundPortConflict(ni, prof.Port, prof.ID); conflict != "" {
			return res, fmt.Errorf("node %q: %s", nodeID, conflict)
		}
		addTargets[nodeID] = ni
	}

	// ── removals ──
	for _, nodeID := range current {
		if desSet[nodeID] {
			continue
		}
		if blockedBy := chainReferencingProfileOn(chains, prof.ID, nodeID); blockedBy != "" {
			res.Blocked = append(res.Blocked, nodeID)
			continue
		}
		ni, err := st.GetNodeInfo(nodeID)
		if err != nil {
			continue // node gone — nothing to remove
		}
		var kept []model.NodeInbound
		removed := false
		for _, ib := range ni.Inbounds {
			if ib.ProfileID == prof.ID {
				removed = true
				if len(ib.ForUsers) > 0 {
					res.UsersLostOn[nodeID] = len(ib.ForUsers)
				}
				continue
			}
			kept = append(kept, ib)
		}
		if !removed {
			continue
		}
		ni.Inbounds = kept
		if err := st.SaveNodeInfo(ni); err != nil {
			return res, fmt.Errorf("node %q: remove inbound: %w", nodeID, err)
		}
		res.Removed = append(res.Removed, nodeID)
	}

	// ── updates on kept nodes (port/preset sync; creds preserved) ──
	for _, nodeID := range current {
		if !desSet[nodeID] {
			continue
		}
		ni, err := st.GetNodeInfo(nodeID)
		if err != nil {
			continue
		}
		changed := false
		for i := range ni.Inbounds {
			ib := &ni.Inbounds[i]
			if ib.ProfileID != prof.ID {
				continue
			}
			if ib.Port != prof.Port {
				ib.Port = prof.Port
				changed = true
			}
			if ib.Obfuscation != prof.Obfuscation {
				ib.Obfuscation = prof.Obfuscation
				changed = true
			}
			if ib.Protocol == string(model.UserProtocolAWG) {
				before := ib.AWGCPSI1 + ib.AWGH1
				preset := ResolveStandaloneAWGPreset(ib)
				ApplyProfileMaterialToInbound(ib, prof, preset)
				if ib.AWGCPSI1+ib.AWGH1 != before {
					changed = true
				}
			}
		}
		if changed {
			if err := st.SaveNodeInfo(ni); err != nil {
				return res, fmt.Errorf("node %q: update inbound: %w", nodeID, err)
			}
			res.Updated = append(res.Updated, nodeID)
		}
	}

	// ── additions (pre-flight already validated conflicts above) ──
	for _, nodeID := range desired {
		if curSet[nodeID] {
			continue
		}
		ni := addTargets[nodeID]
		ib, err := buildProfileInbound(prof, ni)
		if err != nil {
			return res, fmt.Errorf("node %q: %w", nodeID, err)
		}
		ni.Inbounds = append(ni.Inbounds, ib)
		if err := st.SaveNodeInfo(ni); err != nil {
			return res, fmt.Errorf("node %q: save inbound: %w", nodeID, err)
		}
		res.Added = append(res.Added, nodeID)
	}
	return res, nil
}

// chainReferencingProfileOn returns the name of a chain whose node carries an
// InboundRef to the profile, or "" — removal of such a materialization would
// silently break the chain's entry.
func chainReferencingProfileOn(chains []*model.Chain, profileID, nodeID string) string {
	for _, c := range chains {
		for _, n := range c.AllNodes() {
			if n.ID == nodeID && n.InboundRef == profileID {
				return c.Name
			}
		}
	}
	return ""
}

// inboundPortConflict returns a human-readable conflict when another inbound
// on the node already claims the port.
func inboundPortConflict(ni *model.NodeInfo, port int, profileID string) string {
	if port <= 0 {
		return ""
	}
	for _, ib := range ni.Inbounds {
		if ib.ProfileID == profileID {
			continue
		}
		effective := ib.Port
		if ib.Protocol == "mtproxy" && effective == 0 {
			effective = 443
		}
		if effective == port {
			return fmt.Sprintf("port %d already used by inbound %q (%s)", port, ib.Tag, ib.Protocol)
		}
	}
	return ""
}

// buildProfileInbound creates the materialized NodeInbound for a profile on a
// node with fresh per-node credentials (generated exactly once here).
func buildProfileInbound(prof *model.InboundProfile, ni *model.NodeInfo) (model.NodeInbound, error) {
	ib := model.NodeInbound{
		Protocol:    prof.Protocol,
		Port:        prof.Port,
		Obfuscation: prof.Obfuscation,
		Source:      "standalone",
		Tag:         prof.ID,
		ProfileID:   prof.ID,
	}
	preset := GetDefaultPreset()
	if prof.Obfuscation != "" {
		if p, ok := GetPreset(prof.Obfuscation); ok {
			preset = p
		}
	}
	switch prof.Protocol {
	case "awg":
		priv, pub, err := generateWireGuardKeypair()
		if err != nil {
			return ib, fmt.Errorf("generate awg keypair: %w", err)
		}
		ib.ServerPrivKey = priv
		ib.ServerPubKey = pub
		ib.AWGServerAddress = allocateAWGServerSubnet(awgSubnetsInUseOnNode(ni))
		ApplyProfileMaterialToInbound(&ib, prof, preset)
	case "vless-reality":
		p, err := generateHopParams(prof.Port, &preset)
		if err != nil {
			return ib, fmt.Errorf("generate reality params: %w", err)
		}
		ib.UUID = p.UUID
		ib.ServerPrivKey = p.PrivateKey
		ib.ShortID = p.ShortID
		pub, err := p.publicKeyB64()
		if err != nil {
			return ib, fmt.Errorf("derive reality pubkey: %w", err)
		}
		ib.ServerPubKey = pub
	case "mtproxy":
		// Secrets are per-user (User.MTProxySecret); nothing per-node here.
	case "tuic":
		// Frozen legacy profile (edit-only — new tuic profiles are rejected at
		// the UI by ValidateStandaloneProtocol, but migrated ones must still
		// materialize so their chains keep rendering).
		ib.UUID = generateStableUUID()
	default:
		return ib, fmt.Errorf("unsupported profile protocol %q", prof.Protocol)
	}
	return ib, nil
}

// awgSubnetsInUseOnNode lists the AWG /24 server subnets already claimed on
// the node (explicit AWGServerAddress values, plus the legacy default
// 10.8.0.1/24 when any AWG inbound leaves it empty — e.g. a chain entry).
func awgSubnetsInUseOnNode(ni *model.NodeInfo) []string {
	var taken []string
	legacyDefaultUsed := false
	for _, ib := range ni.Inbounds {
		if ib.Protocol != "awg" {
			continue
		}
		if ib.AWGServerAddress != "" {
			taken = append(taken, ib.AWGServerAddress)
		} else {
			legacyDefaultUsed = true
		}
	}
	if legacyDefaultUsed {
		taken = append(taken, "10.8.0.1/24")
	}
	return taken
}
