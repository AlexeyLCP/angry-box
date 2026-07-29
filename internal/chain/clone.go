package chain

// clone.go — node cloning: duplicate a node's CONFIGURATION onto a new VPS with
// FRESH identity (P1b). The architectural twin of relocate.go, but with two
// inversions:
//
//  1. NEW node ID (collision-checked) — relocate mutates the same node in
//     place; clone appends a brand-new ChainNode alongside the source.
//  2. FRESH identity — relocate reuses all transit/exit material so existing
//     clients/peers keep working; clone REGENERATES everything (UUID, Reality
//     keys, ShortID, AWG client keys, MTProxy secret, transit WG keypairs,
//     transit UUID, transit IP, exit material) so the clone is a DISTINCT node,
//     not a second server sharing the source's credentials.
//
// What is COPIED (configuration, per the agreed scope): Protocol, Port,
// Obfuscation, ForUsers (the clone serves the same users immediately),
// OutboundTag, Source, AWGServerAddress (copied as-is — see risk note),
// Country, Bandwidth, AutoApply, UseSudo, chain membership + Role +
// ExitTargets (topology shape).
//
// What is REGENERATED (identity): UUID, ServerPrivKey/PubKey, ShortID, Tag,
// ObfsPassword, TLSCertificate/TLSPrivateKey, AWGClientPub/Priv (per inbound);
// TransitPrivKey/ShortID/UUID, TransitAWGServer*/Client*/Address,
// ExitAWGServer*/ExitAWGLinks (per chain membership).
//
// What is CLEARED (clone is a fresh, undeployed node): LastDeployedHash/At,
// PendingHostKeyFingerprint, Takeover.
//
// After saving the clone (Host + NodeInfo + appended ChainNode per chain),
// CloneNode re-runs ApplyChain for every chain the clone joined — the same
// re-deploy loop relocate uses — so dependent hops learn the clone's addr and
// the clone itself is provisioned on the new VPS.

import (
	"context"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// CloneReport is the outcome of a CloneNode call: which node was cloned, the
// new ID + addr, and the per-chain re-apply result (so the UI can show exactly
// which chains were provisioned and which failed).
type CloneReport struct {
	SourceID string
	NewID    string
	NewAddr  string
	Chains   []CloneChainResult
}

// CloneChainResult is one chain's re-apply outcome during a clone.
type CloneChainResult struct {
	Name    string
	Success bool
	Error   string
	Nodes   []NodeResult // per-node deploy results from ApplyChain
}

// CloneNode duplicates a node's configuration onto a new VPS with fresh identity.
// The source node is left untouched. The clone is appended (new ChainNode) to
// every chain the source belongs to, then ApplyChain re-deploys those chains so
// the clone is provisioned + dependent hops learn its addr.
//
// sourceID is the node to clone. newID is the clone's ID (operator-supplied,
// like capture — not auto-generated). newAddr is the new VPS (IP:port).
// newUser/newKeyPath are optional (empty keeps the source's). applier is the
// chain applier (chain.NewApplier(factory, connector)); must be non-nil.
// awgClientPubKey is forwarded to ApplyChain (pass "" for default).
//
// A failure on one chain is recorded, not fatal — the operator gets a full
// per-chain picture and can retry failed chains. The clone's Host/NodeInfo are
// saved BEFORE the re-deploy loop, so even if every chain re-apply fails the
// clone exists in the store (the operator can re-apply manually).
//
// This is the package-level entry point that accepts a chainApplier interface
// (so tests inject a fake without dialing SSH). The *Applier.CloneNode method
// is a thin wrapper that passes itself as the applier.
func CloneNode(ctx context.Context, store *Store, applier chainApplier, sourceID, newID, newAddr, newUser, newKeyPath, awgClientPubKey string) (*CloneReport, error) {
	if store == nil {
		return nil, fmt.Errorf("clone: nil store")
	}
	if applier == nil {
		return nil, fmt.Errorf("clone: nil applier")
	}
	newID = strings.TrimSpace(newID)
	newAddr = strings.TrimSpace(newAddr)
	if sourceID == "" || newID == "" || newAddr == "" {
		return nil, fmt.Errorf("clone: sourceID, newID, and newAddr are required")
	}
	if newID == sourceID {
		return nil, fmt.Errorf("clone: newID must differ from sourceID")
	}
	// Collision check: the new ID must not already exist.
	if _, err := store.GetHost(newID); err == nil {
		return nil, fmt.Errorf("clone: node %q already exists", newID)
	}

	srcInfo, err := store.GetNodeInfo(sourceID)
	if err != nil {
		return nil, fmt.Errorf("clone: source node %q not found: %w", sourceID, err)
	}

	// Collect the AWG /24 subnets already in use across the store so the clone's
	// AWG inbounds can be allocated fresh, collision-free subnets (a copied
	// AWGServerAddress would collide when the clone joins the same chain).
	takenSubnets := collectAWGServerSubnets(store)

	// 1. Build the clone's NodeInfo: copy configuration, clear deploy/takeover
	//    state, mint a new Host with the new VPS coords, regenerate inbound identity.
	if newUser == "" {
		newUser = srcInfo.User
	}
	if newKeyPath == "" {
		newKeyPath = srcInfo.KeyPath
	}
	cloneHost := &model.Host{
		ID:      newID,
		Addr:    newAddr,
		User:    newUser,
		KeyPath: newKeyPath,
	}
	if err := store.SaveHost(cloneHost); err != nil {
		return nil, fmt.Errorf("clone: save host: %w", err)
	}

	cloneInfo := &model.NodeInfo{
		Host:      *cloneHost,
		Country:   srcInfo.Country,
		Bandwidth: srcInfo.Bandwidth,
		Source:    "cloned",
		AutoApply: srcInfo.AutoApply,
		UseSudo:   srcInfo.UseSudo,
		Inbounds:  cloneInbounds(srcInfo.Inbounds, takenSubnets),
		// LastDeployedHash/At, PendingHostKeyFingerprint, Takeover: zero (fresh).
	}
	if err := store.SaveNodeInfo(cloneInfo); err != nil {
		return nil, fmt.Errorf("clone: save node_info: %w", err)
	}

	// 2. Append a fresh-identity ChainNode to every chain the source belongs to.
	affected, _ := store.GetChainsForNode(sourceID)
	for _, c := range affected {
		srcCN := findChainNode(c, sourceID)
		if srcCN == nil {
			continue // defensive: GetChainsForNode should only return chains containing it
		}
		cloneCN, err := cloneChainNode(*srcCN, c, newID, newUser, newKeyPath, newAddr, takenSubnets)
		if err != nil {
			return nil, fmt.Errorf("clone: chain %q: %w", c.Name, err)
		}
		c.Nodes = append(c.Nodes, cloneCN)
		if err := store.SaveChain(c); err != nil {
			return nil, fmt.Errorf("clone: save chain %q: %w", c.Name, err)
		}
	}

	report := &CloneReport{SourceID: sourceID, NewID: newID, NewAddr: newAddr}

	// 3. Re-apply each affected chain so the clone is provisioned on the new VPS
	//    and dependent hops learn its addr. Same pattern as relocate: a failure on
	//    one chain is recorded, not fatal.
	for _, c := range affected {
		resolved, err := store.ResolveNodes(c)
		res := CloneChainResult{Name: c.Name}
		if err != nil {
			res.Error = fmt.Sprintf("resolve: %v", err)
			report.Chains = append(report.Chains, res)
			continue
		}
		c.Nodes = resolved
		applyReport, err := applier.ApplyChain(ctx, store, c, awgClientPubKey)
		if err != nil {
			res.Error = err.Error()
			if applyReport != nil {
				res.Nodes = applyReport.Nodes
			}
			report.Chains = append(report.Chains, res)
			continue
		}
		res.Success = true
		res.Nodes = applyReport.Nodes
		_ = store.SaveChain(c) // persist any material ApplyChain generated
		report.Chains = append(report.Chains, res)
	}

	// 4. Audit the clone (best-effort).
	WriteAudit(store, "clone", "node", newID,
		map[string]string{"source": sourceID, "new_addr": newAddr,
			"chains": chainNames(affected), "success": fmt.Sprintf("%d/%d", countCloneSuccess(report.Chains), len(report.Chains))},
		"operator")

	return report, nil
}

// CloneNode is the *Applier method wrapper around the package-level CloneNode
// (production callers pass a real *Applier as the chainApplier).
func (a *Applier) CloneNode(ctx context.Context, store *Store, sourceID, newID, newAddr, newUser, newKeyPath, awgClientPubKey string) (*CloneReport, error) {
	return CloneNode(ctx, store, a, sourceID, newID, newAddr, newUser, newKeyPath, awgClientPubKey)
}

// cloneInbounds returns a copy of src with fresh identity on each inbound
// (UUID/Reality keys/ShortID/Tag/ObfsPassword/TLS/AWG client keys) and the
// configuration fields (Protocol/Port/Obfuscation/ForUsers/OutboundTag/Source)
// copied as-is. ForUsers is COPIED per the agreed scope (the clone serves the
// same users immediately). AWGServerAddress is NOT copied — for AWG inbounds a
// FRESH /24 is allocated from `taken` (the existing AWGServerAddress values
// across the store) so a clone joining the same chain does not collide with the
// source's subnet; the allocated subnet is appended to taken so two AWG inbounds
// on the same clone get distinct /24s. Non-AWG inbounds keep AWGServerAddress
// empty (it is AWG-only).
func cloneInbounds(src []model.NodeInbound, taken []string) []model.NodeInbound {
	out := make([]model.NodeInbound, 0, len(src))
	for _, ib := range src {
		c := model.NodeInbound{
			// Configuration (copied):
			Protocol:    ib.Protocol,
			Port:        ib.Port,
			Obfuscation: ib.Obfuscation,
			ForUsers:    append([]string(nil), ib.ForUsers...),
			OutboundTag: ib.OutboundTag,
			Source:      ib.Source,
			// Identity (regenerated below):
		}
		// AWG gets a fresh /24 (not the source's subnet) to avoid collisions.
		if ib.Protocol == "awg" {
			subnet := allocateAWGServerSubnet(taken)
			c.AWGServerAddress = subnet
			taken = append(taken, subnet)
		}
		if err := regenInboundIdentity(&c); err != nil {
			// Best-effort: log via the package logger pattern (WriteAudit is for
			// store events). A regen failure leaves the field empty — ApplyChain /
			// ApplyMergedNode will fill it on deploy. Keep going rather than abort
			// the whole clone for one inbound's keygen failure.
			_ = err
		}
		out = append(out, c)
	}
	return out
}

// regenInboundIdentity regenerates every identity field on ib in place. Called
// for each cloned inbound. Errors are non-fatal (caller keeps the inbound with
// empty identity for ApplyChain/ApplyMergedNode to fill).
func regenInboundIdentity(ib *model.NodeInbound) error {
	uuid, err := generateStableUUIDEquiv()
	if err == nil {
		ib.UUID = uuid
	}
	if tag, e := GenerateInboundTag(ib.Protocol); e == nil {
		ib.Tag = tag
	}
	// Reality / VLESS server keypair + short id.
	priv, pub, e := GenerateRealityKeypair()
	if e == nil {
		ib.ServerPrivKey = priv
		ib.ServerPubKey = pub
	}
	ib.ShortID = GenerateRealityShortIDNonEmpty()
	// MTProxy / Hysteria2 / Trojan / SS passwords — regenerate the relevant one.
	// We do not branch on protocol (cheap to mint all; empty ones are ignored
	// by the render path that does not use them).
	ib.ObfsPassword = GenerateHysteria2ObfsPassword()
	// AWG standalone sample client keypair.
	wgPriv, wgPub, e := GenerateWireGuardKeypair()
	if e == nil {
		ib.AWGClientPriv = wgPriv
		ib.AWGClientPub = wgPub
	}
	// TLS self-signed cert for TLS-based inbounds (TUIC/Hysteria2). Fresh per clone.
	if ib.TLSCertificate != "" || ib.TLSPrivateKey != "" {
		if cert, key, e := GenerateSelfSignedCert("angry-box-clone"); e == nil {
			ib.TLSCertificate = cert
			ib.TLSPrivateKey = key
		}
	}
	return nil
}

// cloneChainNode builds a fresh-identity ChainNode for the clone from srcCN:
// copies Port/Role/ExitTargets/Inbounds(regen'd), mints fresh transit WG keypairs
// + transit UUID + transit IP + exit material, and wires the new Host coords.
// `taken` is the in-use AWG /24 list (shared with the NodeInfo cloneInbounds
// call) so the ChainNode's AWG inbounds also get collision-free subnets.
func cloneChainNode(srcCN model.ChainNode, c *model.Chain, newID, newUser, newKeyPath, newAddr string, taken []string) (model.ChainNode, error) {
	cn := model.ChainNode{
		// Configuration (copied):
		Port:        srcCN.Port,
		Role:        srcCN.Role,
		ExitTargets: append([]string(nil), srcCN.ExitTargets...),
		Inbounds:    cloneInbounds(srcCN.Inbounds, taken),
		// Host coords (new):
		ID:      newID,
		Addr:    newAddr,
		User:    newUser,
		KeyPath: newKeyPath,
	}
	if err := regenChainNodeIdentity(&cn, c); err != nil {
		return cn, err
	}
	return cn, nil
}

// regenChainNodeIdentity mints fresh transit + exit material on cn. Transit IP
// is allocated collision-free against the chain's existing transit IPs.
func regenChainNodeIdentity(cn *model.ChainNode, c *model.Chain) error {
	// Transit Reality keypair + short id + shared UUID. Only the private key +
	// short id are needed for a transit Reality inbound (the server's public key
	// is derived by clients from the private key it embeds); the generated pub
	// is discarded here.
	priv, _, err := GenerateRealityKeypair()
	if err == nil {
		cn.TransitPrivKey = priv
		cn.TransitShortID = GenerateRealityShortIDNonEmpty()
	}
	cn.TransitUUID = GenerateTUICUUID()
	// Transit AWG keypairs (server + client) + inner tunnel IP.
	srvPriv, srvPub, err := GenerateWireGuardKeypair()
	if err == nil {
		cn.TransitAWGServerPriv = srvPriv
		cn.TransitAWGServerPub = srvPub
	}
	cliPriv, cliPub, err := GenerateWireGuardKeypair()
	if err == nil {
		cn.TransitAWGClientPriv = cliPriv
		cn.TransitAWGClientPub = cliPub
	}
	cn.TransitAWGAddress = allocateAWGTransitIP(transitIPsTaken(c))
	// Exit material (only meaningful for Role=exit, but mint unconditionally —
	// harmless and keeps the clone symmetric with the source's role).
	if cn.Role == model.NodeRoleExit {
		exitPriv, exitPub, err := GenerateWireGuardKeypair()
		if err == nil {
			cn.ExitAWGServerPriv = exitPriv
			cn.ExitAWGServerPub = exitPub
		}
		// ExitAWGLinks point at OTHER nodes (exit targets) by ID — these are the
		// balancer-side client interfaces. For a clone that is itself an exit, the
		// links are the balancer→this-exit interfaces, owned by the BALANCER node,
		// not the exit. So we leave ExitAWGLinks empty here (the balancer that
		// targets this clone will mint its own link on its re-apply). Copying the
		// source's links would point them at the source's exit targets with the
		// source's client keys — wrong for a distinct node.
	}
	return nil
}

// transitIPsTaken collects the existing TransitAWGAddress values in a chain so
// the clone's transit IP is allocated collision-free.
func transitIPsTaken(c *model.Chain) []string {
	taken := make([]string, 0, len(c.Nodes))
	for _, n := range c.Nodes {
		if n.TransitAWGAddress != "" {
			taken = append(taken, n.TransitAWGAddress)
		}
	}
	return taken
}

// findChainNode returns a pointer to the ChainNode with the given ID in c, or
// nil if not present.
func findChainNode(c *model.Chain, id string) *model.ChainNode {
	for i := range c.Nodes {
		if c.Nodes[i].ID == id {
			return &c.Nodes[i]
		}
	}
	return nil
}

// generateStableUUIDEquiv returns a v4 UUID (reuses the unexported
// generateStableUUID; wrapped so clone.go reads cleanly). Errors are not
// possible from generateStableUUID, but the signature mirrors other generators.
func generateStableUUIDEquiv() (string, error) {
	return generateStableUUID(), nil
}

// countCloneSuccess counts how many chain re-applies succeeded in a clone.
func countCloneSuccess(chains []CloneChainResult) int {
	n := 0
	for _, c := range chains {
		if c.Success {
			n++
		}
	}
	return n
}

// collectAWGServerSubnets gathers every AWGServerAddress currently in use by
// any node's standalone AWG inbound across the store, so CloneNode can pass the
// list to cloneInbounds and the clone's AWG inbounds get collision-free /24s.
// Best-effort: a store read failure yields an empty list (allocation then starts
// at 10.8.1.0/24, which is safe on a fresh/different node anyway).
func collectAWGServerSubnets(store *Store) []string {
	infos, err := store.ListNodeInfos()
	if err != nil {
		return nil
	}
	var taken []string
	for _, info := range infos {
		for _, ib := range info.Inbounds {
			if ib.Protocol == "awg" && ib.AWGServerAddress != "" {
				taken = append(taken, ib.AWGServerAddress)
			}
		}
	}
	return taken
}