package chain

// awg_deploy.go — renders the kernel awg-quick .conf files a node needs under
// the kernel-AWG architecture, for the deploy flow to push alongside the
// sing-box config. The AWG interfaces (awg0 user-entry, awg-exit-nX balancer
// client tunnels, awg0 on Role=exit servers) are owned by the kernel via
// awg-quick; sing-box sits on top via the TUN overlay (BuildAWGTUNOverlay /
// RenderAWGBalancer). This aggregator is a pure function over the same inputs
// as buildMergedNodeConfig, so the deploy path calls both with identical args.
//
// Files produced (in stable order: awg0 first, then awg-exit-nX by index):
//   - chain AWG user-entry       → /etc/amnezia/amneziawg/awg0.conf      (awg-quick@awg0)
//   - multi-exit balancer client → /etc/amnezia/amneziawg/awg-exit-nX.conf (awg-quick@awg-exit-nX)
//   - Role=exit server           → /etc/amnezia/amneziawg/awg0.conf      (awg-quick@awg0)
//   - standalone AWG inbound     → /etc/amnezia/amneziawg/awg0.conf      (awg-quick@awg0)
//
// A node that is BOTH an AWG entry AND a multi-exit balancer (the
// dns.idoctor.mom pattern) gets awg0.conf + N awg-exit-nX.conf.

import (
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// AWGConfFile is one kernel awg-quick .conf to push to a node during deploy.
type AWGConfFile struct {
	Path        string // remote path under /etc/amnezia/amneziawg/
	ServiceName string // systemd unit to enable, e.g. awg-quick@awg0
	Content     string // the .conf body
}

// RenderNodeAWGConfs renders all kernel awg-quick .conf files a node needs,
// given the same inputs as buildMergedNodeConfig. Returns the files in stable
// order (awg0 first, then awg-exit-nX by link index). Empty when the node runs
// no kernel AWG interface (e.g. a Reality/XHTTP chain node).
func RenderNodeAWGConfs(
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
	usersByChain map[string][]model.User,
	usersByInbound map[string][]model.User,
) ([]AWGConfFile, []string) {
	if nodeInfo == nil {
		return nil, nil
	}
	roles := resolveChainRoles(nodeInfo.ID, nodeChains)
	var files []AWGConfFile
	var warnings []string

	// 1. Chain AWG user-entry → kernel awg0.conf with one [Peer] per user.
	//    AWG 3.0 entry: renders as a userspace `type:"awg"` sing-box endpoint
	//    (no awg0.conf) UNLESS the node's kernel supports AWG 3.0
	//    (KernelAWG3Supported, AGENTS #5 revision) — then it renders as a kernel
	//    awg0.conf carrying HPK/CPM/RAT (the same path as AWG 1.5/2.0, stable).
	chainEntryPresent := false
	kernelAWG3 := kernelAWG3EnabledFor(nodeInfo)
	for _, r := range roles {
		if r.IsEntry && r.Chain.UserProtocol == model.UserProtocolAWG {
			entry3 := chainEntryAWG3Inbound(nodeInfo, r.Chain, r.Node) != nil
			if entry3 && !kernelAWG3 {
				// AWG3 entry on a node without kernel-AWG3 → userspace endpoint.
				chainEntryPresent = false
				break
			}
			// Non-AWG3 entry, OR AWG3 entry on a kernel-AWG3 node → kernel awg0.conf.
			users := usersForChain(usersByChain, r.Chain.Name)
			files = append(files, renderChainEntryAWGConf(nodeInfo, r, users))
			chainEntryPresent = true
			break // one awg0.conf per node
		}
	}

	// 2. Multi-exit balancer → one awg-exit-nX.conf per ExitAWGLink.
	for _, r := range roles {
		if len(r.Node.ExitAWGLinks) > 0 {
			files = append(files, renderBalancerExitConfs(r)...)
			break
		}
	}

	// 3. Role=exit node → kernel awg0.conf accepting the balancer's tunnel.
	for _, r := range roles {
		if r.Node.Role == model.NodeRoleExit {
			if f, ok := renderExitServerConf(r); ok {
				files = append(files, f)
			}
			break
		}
	}

	// 4. Standalone AWG inbound → kernel awg0.conf. A node has exactly one awg0
	//    interface, so a standalone AWG inbound co-located with a chain AWG
	//    entry (both default to 10.8.0.1/24) collides. AGENTS.md Known Issue #10:
	//    rather than silently dropping the standalone (the old `if len(files)==0`
	//    guard did that), emit a loud warning when the collision is unavoidable
	//    (chain entry present + standalone with empty AWGServerAddress = default
	//    10.8.0.1/24). A standalone with a distinct AWGServerAddress (10.8.1.1/24,
	//    ...) would need a separate interface (awg1) — that multi-interface
	//    support is a follow-up; for now we warn and skip to keep awg0 consistent.
	standaloneAdded := false
	for i := range nodeInfo.Inbounds {
		ib := &nodeInfo.Inbounds[i]
		if ib.Protocol != "awg" {
			continue
		}
		if ib.EffectiveAWGVersion() == model.AWGVersion3 && !kernelAWG3 {
			// AWG 3.0 userspace fallback = `type:"awg"` endpoint rendered in the
			// merged config (buildStandaloneInOut), NOT a kernel awg0/awg1 .conf.
			// On a kernel-AWG3 node (KernelAWG3Supported) the v3 inbound falls
			// through to the kernel-render branches below (awg0/awg1 with HPK).
			continue
		}
		if IsChainSourcedInbound(ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, ib) {
			// Chain-entry materialized inbound — rendered by the chain entry
			// loop above (renderChainEntryAWGConf), not here. Without this
			// skip its non-empty AWGServerAddress would trigger the awg1
			// branch below and double-render the same listener (the profile
			// keeps Source="standalone" when shared with a chain entry, so
			// the reference check IsChainEntryInbound is required too).
			continue
		}
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("sa-%d-awg", i)
		}
		if chainEntryPresent && ib.AWGServerAddress == "" {
			// Default 10.8.0.1/24 collides with the chain entry's 10.8.0.1/24.
			// Skip + warn (the old behavior silently dropped it; now the
			// operator sees the collision in the MergeReport / deploy log).
			warnings = append(warnings, fmt.Sprintf(
				"node %q: standalone AWG inbound %q collides with the chain AWG entry on awg0 (both default to 10.8.0.1/24); the standalone is skipped. Set a distinct NodeInbound.AWGServerAddress (e.g. 10.8.1.1/24) to deploy it on a second interface awg1 (AGENTS.md #10).",
				nodeInfo.ID, tag))
			continue
		}
		if chainEntryPresent && ib.AWGServerAddress != "" {
			// Distinct subnet + a chain entry already claimed awg0 → deploy the
			// standalone on a SECOND kernel AWG interface (awg1) so the two
			// coexist on one node. Each gets its own awg-quick unit + subnet +
			// PostUp FORWARD rules. The TUN overlay must include BOTH awg0 and
			// awg1 (handled in tunIncludeInterfaces). AGENTS.md #10.
			files = append(files, renderStandaloneAWGConf(ib, tag, usersByInbound, "awg1"))
			continue
		}
		files = append(files, renderStandaloneAWGConf(ib, tag, usersByInbound, "awg0"))
		standaloneAdded = true
		break
	}
	_ = standaloneAdded

	return files, warnings
}

// AWGTeardownInterfaces returns the kernel AWG interfaces this node must NOT
// keep running — the ones that WOULD have been rendered as awg-quick .confs if
// the owning inbound were not in AWG 3.0 mode. AWG3 inbounds render as an
// in-process userspace `type:"awg"` sing-box endpoint that binds the SAME UDP
// port; a leftover awg-quick@awgN from a previous non-AWG3 deploy keeps that
// port and sing-box dies at startup:
//
//	endpoint/awg[ch-X-user-in]: unable to update bind: create ipv4 connection:
//	listen udp4 0.0.0.0:8443: bind: address already in use
//	FATAL start service: ...
//
// (live crash-loop on the VladufQa node, PROGRESS §39 — RenderNodeAWGConfs
// correctly stopped EMITTING awg0.conf for AWG3, but nothing ever stopped the
// already-running unit.)
//
// Interfaces that ARE in the rendered file set are never returned — a node can
// legitimately run other kernel AWG interfaces alongside an AWG3 endpoint (e.g.
// a second standalone inbound on awg2 carrying live traffic) and those must not
// be touched. Result is deduplicated and stable-ordered.
func AWGTeardownInterfaces(
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
	renderedFiles []AWGConfFile,
) []string {
	if nodeInfo == nil {
		return nil
	}
	keep := map[string]bool{}
	for _, f := range renderedFiles {
		keep[awgIfaceFromService(f.ServiceName)] = true
	}

	var out []string
	seen := map[string]bool{}
	add := func(iface string) {
		if iface == "" || keep[iface] || seen[iface] {
			return
		}
		seen[iface] = true
		out = append(out, iface)
	}

	// 1. AWG3 chain entry — would have owned awg0 (renderChainEntryAWGConf).
	//    On a kernel-AWG3 node (KernelAWG3Supported) the v3 entry RENDERS a
	//    kernel awg0 (with HPK), so awg0 is in keep and must NOT be torn down —
	//    the teardown logic below only applies to the userspace fallback.
	roles := resolveChainRoles(nodeInfo.ID, nodeChains)
	hasAWGChainEntry := false
	kernelAWG3 := kernelAWG3EnabledFor(nodeInfo)
	for _, r := range roles {
		if r.IsEntry && r.Chain.UserProtocol == model.UserProtocolAWG {
			hasAWGChainEntry = true
			if !kernelAWG3 && chainEntryAWG3Inbound(nodeInfo, r.Chain, r.Node) != nil {
				add("awg0")
			}
			break // one chain entry per node
		}
	}

	// 2. AWG3 standalone inbound — would have owned awg0, or awg1 when a chain
	//    entry claimed awg0 (mirrors the RenderNodeAWGConfs branch logic). Same
	//    kernel-AWG3 gate: on a kernel-AWG3 node the v3 standalone renders a
	//    kernel awg0/awg1 (in keep), so it is not torn down.
	chainEntryClaimsAWG0 := false
	for _, f := range renderedFiles {
		if awgIfaceFromService(f.ServiceName) == "awg0" {
			chainEntryClaimsAWG0 = true
			break
		}
	}
	for i := range nodeInfo.Inbounds {
		ib := &nodeInfo.Inbounds[i]
		if ib.Protocol != "awg" || ib.EffectiveAWGVersion() != model.AWGVersion3 {
			continue
		}
		if kernelAWG3 {
			// v3 inbound renders a kernel awg0/awg1 on this node — it's in keep.
			continue
		}
		if IsChainSourcedInbound(ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, ib) {
			continue // handled by the chain-entry branch above
		}
		if (chainEntryClaimsAWG0 || hasAWGChainEntry) && ib.AWGServerAddress != "" {
			add("awg1")
			continue
		}
		add("awg0")
	}

	// 3. Stale awg0 teardown: if an AWG chain entry or AWG inbound exists on the node
	//    but awg0 is not in keep (e.g. AWG3 mode or unmapped), ensure awg0 is torn down.
	if !keep["awg0"] && hasAWGChainEntry {
		add("awg0")
	}

	return out
}

// renderChainEntryAWGConf renders the user-entry awg0.conf for a chain AWG
// entry node. v2 (InboundRef set): reads the MATERIALIZED entry inbound from
// the node's NodeInfo (profile credentials, port, subnet, CPS/H material) —
// the profile is the source of truth. Legacy fallback (no InboundRef, or the
// materialization is missing): renders from the chain's own fields exactly as
// before, so un-migrated chains keep working.
func renderChainEntryAWGConf(nodeInfo *model.NodeInfo, r chainRole, users []model.User) AWGConfFile {
	if r.Node.InboundRef != "" {
		if ib := inboundByProfileID(nodeInfo, r.Node.InboundRef); ib != nil {
			// Same shared preset resolver as the client .conf render — a
			// divergence here emits an amnezia block the client can't match
			// (live: server S1=15 vs client S1=115, PROGRESS §39).
			return renderAWGServerConfFromInbound(ib, ResolveChainEntryPreset(r.Preset, ib), users, "awg0")
		}
	}
	return renderChainEntryAWG0Conf(r, users)
}

// renderChainEntryAWG0Conf renders the user-entry awg0.conf for a chain AWG
// entry node: kernel AWG server with one [Peer] per credentialed user and the
// chain's persisted amnezia obfuscation material.
func renderChainEntryAWG0Conf(r chainRole, users []model.User) AWGConfFile {
	c := r.Chain
	preset := r.Preset
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	var peers []AWGServerPeer
	for _, u := range users {
		if !u.Active || u.AWGPublicKey == "" || u.AWGAddress == "" {
			continue
		}
		peers = append(peers, AWGServerPeer{PublicKey: u.AWGPublicKey, AllowedIPs: u.AWGAddress})
	}
	return AWGConfFile{
		Path:        awg0ConfPath,
		ServiceName: "awg-quick@awg0",
		Content: RenderServerAWGConf(AWGServerConfParams{
			ServerPrivateKey: c.AWGEntryServerPriv,
			ListenPort:       chainEntryPort(c, r.Node.ID),
			TunnelAddress:    "10.8.0.1/24",
			MTU:              1420,
			Amnezia:          BuildAWGAmnezia(awg, &preset, ChainAWGObfsMaterial(c)),
			Peers:            peers,
			TUNInterface:     tunInterfaceName, // sing-box-tun: PostUp/PostDown FORWARD rules
		}),
	}
}

// renderBalancerExitConfs renders one awg-exit-nX.conf per ExitAWGLink on a
// multi-exit balancer node. Each is the client end of a kernel AWG tunnel to
// the remote exit server (a Role=exit node).
func renderBalancerExitConfs(r chainRole) []AWGConfFile {
	// Resolve the chain's preset + amnezia material for the exit-tunnel amnezia
	// block. DPI can block plain WireGuard data packets (handshake passes, data
	// gets cut), so exit tunnels need obfuscation — the real dns.idoctor.mom
	// uses Jc=15 on its exit tunnels. Use the chain's material so both ends match.
	preset := r.Preset
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	amnezia := BuildAWGAmnezia(awg, &preset, ChainAWGObfsMaterial(r.Chain))

	var files []AWGConfFile
	for _, link := range r.Node.ExitAWGLinks {
		exit := chainNodeByID(r.Chain, link.TargetID)
		if exit == nil {
			continue // malformed chain — ensureAWGExitLinks should have caught this
		}
		endpoint := fmt.Sprintf("%s:%d", extractHost(exit.Addr), exit.ExitAWGListenPort)
		files = append(files, AWGConfFile{
			Path:        fmt.Sprintf("%s/%s.conf", awgConfDir, link.InterfaceName),
			ServiceName: "awg-quick@" + link.InterfaceName,
			Content: RenderExitAWGConf(ExitClientConfParams{
				InterfaceName:    link.InterfaceName,
				ClientPrivateKey: link.ClientPriv,
				ClientAddress:    link.Address,
				ClientListenPort: link.ClientPort,
				ExitPublicKey:    exit.ExitAWGServerPub,
				ExitEndpoint:     endpoint,
				// Amnezia ON on exit links — the real dns.idoctor.mom server uses
				// amnezia (Jc=15) on its exit tunnels too. DPI can block plain
				// WireGuard data packets (handshake passes, data gets cut), so even
				// "trusted server-to-server" tunnels need obfuscation. Use the
				// chain's amnezia material (same I1-I5/H1-H4 as the user-entry) so
				// the exit-tunnel handshake matches.
				Amnezia: amnezia,
			}),
		})
	}
	return files
}

// renderExitServerConf renders the awg0.conf for a Role=exit node — the server
// end of an exit link, accepting the balancer's tunnel as its single peer. The
// balancer's client public key + inner IP come from the balancer node's
// ExitAWGLinks entry targeting this exit. Returns ok=false when no balancer
// targets this exit (a dangling exit node — skip rather than emit a peerless
// .conf that would never receive traffic).
func renderExitServerConf(r chainRole) (AWGConfFile, bool) {
	link, _ := balancerLinkTargetingExit(r.Chain, r.Node.ID)
	if link == nil {
		return AWGConfFile{}, false
	}
	// Resolve amnezia material matching the balancer's exit-client side.
	preset := r.Preset
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	amnezia := BuildAWGAmnezia(awg, &preset, ChainAWGObfsMaterial(r.Chain))
	return AWGConfFile{
		Path:        awg0ConfPath,
		ServiceName: "awg-quick@awg0",
		Content: RenderExitServerAWGConf(ExitServerConfParams{
			ServerPrivateKey:   r.Node.ExitAWGServerPriv,
			ListenPort:         r.Node.ExitAWGListenPort,
			TunnelAddress:      "10.11.0.1/24",
			MTU:                1420,
			BalancerPublicKey:  link.ClientPub,
			BalancerAllowedIPs: link.Address,
			// NAT BOTH subnets that can arrive on awg0: the user subnet (direct
			// user→exit, 10.8.0.0/24) AND the balancer-link subnet (balancer
			// awg-exit-nX inner IP, 10.10.0.0/24 — clients reaching the exit
			// THROUGH a balancer arrive with source 10.10.0.2). Without the
			// 10.10.0.0/24 rule, balancer-routed egress silently times out
			// (exit sends packets with private 10.10.0.2 source, internet can't
			// route responses back). Verified live 2026-07-04.
			MASQUERADENetwork: "10.8.0.0/24,10.10.0.0/24",
			Amnezia:           amnezia,
		}),
	}, true
}

// renderStandaloneAWGConf renders the awg0.conf for a standalone AWG inbound
// (no chain): kernel AWG server with one [Peer] per credentialed user. Amnezia
// comes from the inbound's preset (nil material → degenerate H1-H4, fresh
// I1-I5 — the standalone path has no chain to persist material on).
// ifaceName is the kernel AWG interface name (awg0 / awg1) — a standalone
// co-located with a chain entry uses awg1 with a distinct subnet (AGENTS.md #10).
func renderStandaloneAWGConf(ib *model.NodeInbound, tag string, usersByInbound map[string][]model.User, ifaceName string) AWGConfFile {
	return renderAWGServerConfFromInbound(ib, ResolveStandaloneAWGPreset(ib), usersByInbound[tag], ifaceName)
}

// renderAWGServerConfFromInbound is the shared kernel AWG server conf renderer
// for BOTH standalone AWG inbounds and chain-entry materialized inbounds: one
// [Peer] per credentialed user, the inbound's persisted obfs material, its
// per-inbound subnet, and the TUN-overlay FORWARD rules.
func renderAWGServerConfFromInbound(ib *model.NodeInbound, preset ConnectionPreset, users []model.User, ifaceName string) AWGConfFile {
	awg := preset.AWG
	if awg == nil {
		awg = &AWGPreset{JC: 4, JMIN: 40, JMAX: 70, H1: 1, H2: 2, H3: 3, H4: 4}
	}
	var peers []AWGServerPeer
	for _, u := range users {
		if !u.Active || u.AWGPublicKey == "" || u.AWGAddress == "" {
			continue
		}
		peers = append(peers, AWGServerPeer{PublicKey: u.AWGPublicKey, AllowedIPs: u.AWGAddress})
	}
	var amnezia *config.AmneziaOptions
	if awg.CPSLevel > 0 || preset.CPSLevel > 0 {
		// Persisted material (EnsureInboundAWGMaterial at deploy/conf render):
		// proper quadrant H1-H4 instead of the preset's degenerate "N-N", and
		// CPS I1-I5 identical to what the client conf renders.
		amnezia = BuildAWGAmnezia(awg, &preset, InboundAWGObfsMaterial(ib))
	}
	// Per-inbound server tunnel address (AGENTS.md #10): default 10.8.0.1/24 for
	// backward compat (existing standalone inbounds stay on the chain-entry
	// subnet); a distinct AWGServerAddress (10.8.1.1/24, ...) is the per-inbound
	// subnet that avoids colliding with a co-located chain entry.
	tunnelAddr := ib.AWGServerAddress
	if tunnelAddr == "" {
		tunnelAddr = "10.8.0.1/24"
	}
	return AWGConfFile{
		Path:        awgConfPath(ifaceName),
		ServiceName: awgServiceName(ifaceName),
		Content: RenderServerAWGConf(AWGServerConfParams{
			ServerPrivateKey: ib.ServerPrivKey,
			ListenPort:       ib.Port,
			TunnelAddress:    tunnelAddr,
			MTU:              1420,
			Amnezia:          amnezia,
			// AWG 3.0 header protection (kernel-AWG3 path, AGENTS #5 revision):
			// pass the persisted HPK/CPM/RAT material so RenderServerAWGConf
			// emits HeaderProtectionKey/ContentPaddingAddition/RekeyAfterTime into
			// [Interface]. Only set for a v3 inbound (the render gate in
			// RenderNodeAWGConfs already ensured this conf is only emitted on a
			// kernel-AWG3 node). The HPK is hex-persisted; writeAWG3ConfLines
			// hex→base64 converts it for the awg-quick .conf form.
			AWG3:          inboundAWG3MaterialForKernel(ib),
			Peers:         peers,
			TUNInterface:  tunInterfaceName, // sing-box-tun: PostUp/PostDown FORWARD rules
			InterfaceName: ifaceName,        // awg0 / awg1 — PostUp references this
		}),
	}
}

// inboundAWG3MaterialForKernel returns the AWG 3.0 material to feed into the
// kernel-render AWGServerConfParams.AWG3 for a v3 inbound, or nil when the
// inbound is not AWG 3.0 (so non-v3 inbounds render a plain kernel conf). The
// returned *AWGObfsMaterial carries the hex-persisted HPK + CPM/RAT ranges.
func inboundAWG3MaterialForKernel(ib *model.NodeInbound) *AWGObfsMaterial {
	if ib == nil || ib.EffectiveAWGVersion() != model.AWGVersion3 {
		return nil
	}
	return InboundAWGObfsMaterial(ib)
}

// renderStandaloneAWG0Conf is kept for backward-compat with any caller that
// hardcodes awg0 (tests); it delegates to renderStandaloneAWGConf with awg0.
func renderStandaloneAWG0Conf(ib *model.NodeInbound, tag string, usersByInbound map[string][]model.User) AWGConfFile {
	return renderStandaloneAWGConf(ib, tag, usersByInbound, "awg0")
}

// balancerLinkTargetingExit finds the balancer node's ExitAWGLinks entry that
// targets the given exit node ID, and returns it together with the balancer
// node. A chain has at most one balancer targeting a given exit. Returns
// (nil, nil) when no balancer targets this exit (a dangling exit node).
func balancerLinkTargetingExit(c *model.Chain, exitID string) (*model.AWGExitLink, *model.ChainNode) {
	if c == nil {
		return nil, nil
	}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		for j := range n.ExitAWGLinks {
			if n.ExitAWGLinks[j].TargetID == exitID {
				return &n.ExitAWGLinks[j], n
			}
		}
	}
	return nil, nil
}
