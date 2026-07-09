package chain

// awg_tun_overlay.go — sing-box TUN-overlay renderer for the kernel-AWG server
// architecture. The AWG server interface (awg0 + awg-exit-nX) is owned by the
// kernel via awg-quick; sing-box does NOT run a userspace WireGuard endpoint
// (userspace wireguard-go panics with chacha20poly1305 under AmneziaWG
// obfuscation — VPN/docs/sing-box-extended.md). Instead sing-box captures
// traffic from the kernel AWG interfaces through a TUN inbound
// (include_interface) and routes it across the exit interfaces via a fallback
// round-robin balancer with bind_interface direct outbounds.
//
// This mirrors the dns.idoctor.mom reference
// (VPN/orchestrator/app/templates/awg_balancer.json.j2):
//   endpoints: []
//   inbounds:  [TUN{interface_name:"sing-box-tun", address:["172.16.250.1/30"],
//                    mtu:1200, auto_route:true, stack:"mixed",
//                    include_interface:["awg0"], strict_route:false}]
//   outbounds: [direct, block, direct{tag:exit-n1, bind_interface:awg-exit-n1}, ...,
//               fallback{tag:balancer, outbounds:[exit-n1,...], blacklist_timeout:"30s"}]
//   route:     [{action:sniff}, {protocol:dns, action:hijack-dns},
//               {inbound:["tun-in"], action:route, outbound:balancer}, ...]

import (
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// tunInboundTag is the tag of the TUN inbound that captures kernel AWG traffic.
const tunInboundTag = "tun-in"

// tunInterfaceName is the interface name sing-box creates for the overlay TUN.
// Distinct from awg0 (kernel AWG) so include_interface can target awg0 only.
const tunInterfaceName = "sing-box-tun"

// tunAddress is the overlay TUN's inner address (a /30 is enough — it only
// carries traffic between sing-box and the kernel AWG interfaces).
var tunAddress = []string{"172.16.250.1/30"}

const tunMTU = 1200

// AWGTUNOverlayParams describes the sing-box TUN-overlay pieces to render for a
// kernel-AWG server node (user-entry standalone, chain entry, or multi-exit
// balancer). The kernel awg0.conf / awg-exit-nX.conf are rendered separately by
// RenderServerAWGConf / RenderExitAWGConf and pushed as their own files.
type AWGTUNOverlayParams struct {
	// IncludeInterfaces are the kernel AWG interface names the TUN captures
	// traffic from. Always includes "awg0" (user-entry); a multi-exit balancer
	// also includes its awg-exit-nX client interfaces so their egress goes
	// through the balancer (matches the reference's bind_interface model — the
	// direct outbounds bind to these, and the TUN must not re-capture them).
	IncludeInterfaces []string
	// ExitInterfaces are the kernel AWG client interfaces (awg-exit-nX) the
	// balancer rotates across. Each becomes a direct outbound with
	// bind_interface. Empty for a single-egress standalone AWG node (no
	// balancer — traffic routes to a plain direct outbound).
	ExitInterfaces []string
	// BalancerTag is the tag of the fallback group. Empty when there is no
	// balancer (single egress); the route rule then targets the single direct.
	BalancerTag string
	// FinalOutbound is the route final outbound tag (default "direct").
	FinalOutbound string
	// ForwardOutbound, when non-empty, is the inter-node outbound tag the TUN
	// catch-all rule forwards to — used for a LINEAR AWG chain entry that has a
	// downstream hop (HasOutbound). Without this the catch-all targets "direct"
	// and every AWG user egresses from the entry node, never reaching the
	// downstream hop — chain forwarding is silently broken. Mutually exclusive
	// with ExitInterfaces in practice (a multi-exit balancer exits via its
	// awg-exit-nX interfaces, not via a chain hop). Priority when set:
	// ForwardOutbound > balancer > direct.
	ForwardOutbound string
}

// BuildAWGTUNOverlay renders the inbounds, outbounds, and route rules for a
// kernel-AWG + sing-box-TUN-overlay server node. The caller merges these into
// the node's SingboxConfig (inbounds replace the userspace AWG endpoint that
// used to live in endpoints[]; outbounds/route are appended/merged).
//
// Returns (inbounds, outbounds, route). Endpoints are intentionally empty —
// the kernel owns the WireGuard interfaces; sing-box has no userspace endpoint.
func BuildAWGTUNOverlay(p AWGTUNOverlayParams) (inbounds, outbounds []json.RawMessage, route []config.RouteRuleEntry) {
	if len(p.IncludeInterfaces) == 0 {
		p.IncludeInterfaces = []string{"awg0"}
	}
	if p.FinalOutbound == "" {
		p.FinalOutbound = "direct"
	}

	// TUN inbound — captures traffic from the kernel AWG interfaces.
	// stack:"mixed" = kernel TCP + gVisor UDP so QUIC through-traffic works
	// (VPN/docs/nuances-bugs-patches.md: stack "system" breaks UDP/QUIC).
	tun := config.TUNInbound{
		Type:             "tun",
		Tag:              tunInboundTag,
		InterfaceName:    tunInterfaceName,
		Address:          tunAddress,
		MTU:              tunMTU,
		Stack:            "mixed",
		AutoRoute:        true,
		IncludeInterface: p.IncludeInterfaces,
		StrictRoute:      false,
	}
	tunJSON, _ := json.Marshal(tun)
	inbounds = append(inbounds, tunJSON)

	// direct + block outbounds (the block is added by the caller via needsBlock;
	// direct is always present as the final/egress fallback).
	outbounds = append(outbounds, buildDirectOutbound("direct"))

	// One direct outbound per exit interface, bound to that kernel AWG iface.
	exitTags := make([]string, 0, len(p.ExitInterfaces))
	for _, iface := range p.ExitInterfaces {
		tag := exitOutboundTag(iface)
		ob := config.DirectOutbound{
			Type:          "direct",
			Tag:           tag,
			BindInterface: iface,
		}
		data, _ := json.Marshal(ob)
		outbounds = append(outbounds, data)
		exitTags = append(exitTags, tag)
	}

	// Fallback balancer rotating across the exit outbounds (round-robin on the
	// patched sing-box-extended build; priority fallback on vanilla). Only
	// emitted when there is more than one exit — a single exit routes directly.
	routeOut := "direct"
	if p.BalancerTag != "" && len(exitTags) > 1 {
		fb := config.FallbackOutbound{
			Type:             "fallback",
			Tag:              p.BalancerTag,
			Outbounds:        exitTags,
			BlacklistTimeout: "30s",
		}
		data, _ := json.Marshal(fb)
		outbounds = append(outbounds, data)
		routeOut = p.BalancerTag
	} else if len(exitTags) == 1 {
		routeOut = exitTags[0]
	}
	// A linear AWG chain entry with a downstream hop forwards TUN traffic to the
	// inter-node outbound — WITHOUT this the catch-all targets "direct" and every
	// AWG user egresses from the entry node, never reaching the downstream hop
	// (chain forwarding silently broken). ForwardOutbound takes priority over the
	// balancer/direct (a multi-exit balancer has no chain hop; a linear entry has
	// no exit interfaces — the two are mutually exclusive in practice).
	if p.ForwardOutbound != "" {
		routeOut = p.ForwardOutbound
	}

	// Route rules: sniff → DNS hijack → TUN traffic to the balancer/direct.
	// The reference uses inbound:["tun-in"] (NOT source_ip_cidr) because TUN
	// NAT changes the source IP — source_ip_cidr matching breaks (nuances-bugs
	// §"Route rules: source_ip_cidr vs inbound").
	route = append(route,
		config.RouteRuleEntry{Action: "sniff"},
		config.RouteRuleEntry{Protocol: []string{"dns"}, Action: "hijack-dns"},
		config.RouteRuleEntry{Inbound: []string{tunInboundTag}, Outbound: routeOut},
	)
	return inbounds, outbounds, route
}

// exitOutboundTag derives the sing-box outbound tag for a kernel AWG exit
// interface (awg-exit-n1 → "exit-n1"). Kept stable so route rules and the
// fallback group reference the same tag.
func exitOutboundTag(iface string) string {
	// awg-exit-n1 -> exit-n1; if the interface name doesn't follow the pattern,
	// fall back to the bare interface name.
	const prefix = "awg-exit-"
	if len(iface) > len(prefix) && iface[:len(prefix)] == prefix {
		return iface[len(prefix):] + "-direct"
	}
	return iface + "-direct"
}

// exitInterfacesForNode returns the awg-exit-nX interface names a balancer node
// has, derived from its persisted ExitAWGLinks. Empty for non-balancer nodes.
func exitInterfacesForNode(node *model.ChainNode) []string {
	if node == nil {
		return nil
	}
	out := make([]string, 0, len(node.ExitAWGLinks))
	for _, link := range node.ExitAWGLinks {
		if link.InterfaceName != "" {
			out = append(out, link.InterfaceName)
		}
	}
	return out
}

// tunIncludeInterfaces lists the kernel AWG interfaces the TUN must capture
// from. This is ALWAYS awg0 (the user-entry interface where client traffic
// arrives) PLUS every awg-exit-nX client interface the balancer owns.
//
// CRITICAL (live-verified 2026-07-04): the awg-exit-nX interfaces MUST be in
// include_interface. sing-box direct outbounds use bind_interface: awg-exit-nX
// to dial through the kernel exit tunnel (source = the balancer's inner IP
// 10.10.0.X). The response comes back on the kernel awg-exit-nX interface
// (dst = 10.10.0.X, which is the interface's own address). Without
// include_interface listing awg-exit-nX, sing-box's TUN does not capture that
// response, the kernel delivers it to a local socket that nothing is listening
// on, and the dial times out — so egress through the balancer silently fails
// (SYN goes out via awg-exit-nX, SYN-ACK arrives on awg-exit-nX but never
// reaches sing-box). With awg-exit-nX in include_interface, sing-box captures
// the response and the connection completes. Verified on live VPSes:
// server-2 (kernel AWG client 10.8.0.99) → entry awg0 → tun-in → n1-direct
// (bind_interface awg-exit-n1) → exit awg0 → MASQUERADE → internet, egress IP
// = exit's public IP. Before the fix, `curl --interface awg0` timed out with
// sing-box logging `dial tcp ... i/o timeout`; after, it returns the exit IP.
//
// The earlier comment ("must NOT be in include_interface, or the TUN would
// re-capture egress traffic and loop") was WRONG — include_interface captures
// INCOMING traffic on those interfaces (responses), not the outgoing egress
// (which goes via bind_interface sockets, not the TUN). No loop occurs.
//
// OPEN P0a bug (2026-07-09, see docs/PROGRESS.md §21): include_interface per
// upstream docs/impl SHOULD also capture FORWARDED INGRESS (not just response
// traffic) — it is a route-filter on ingress interface, applied at prerouting/
// routing, so forwarded transit from awg0 (src 10.8.0.x, dst = remote IP, NOT a
// local socket) is intended behavior. But empirically (live VPS §15.2/§15.3) the
// user→internet forwarded ingress on awg0 is NOT captured (tun empty, trace
// "No entries") while the awg-exit-nX response direction above WORKS.
// Candidates: (a) we never enabled auto_redirect (recommended flag, see §21.5 #0);
// (b) SagerNet/sing-box#3805 — multi-element include_set renders as { "", "" }
// → all packets bypass (only bites ≥2 elements; single ["awg0"] is safe, but
// this list can grow to ≥2 with awg1/awg-exit-nX). Diagnose with
// `nft list chain inet sing-box prerouting` + `ip rule show` on the live VPS.
// tunIncludeInterfaces returns the kernel AWG interface names the sing-box TUN
// overlay must capture (include_interface). Always includes awg0 (the chain
// entry / first standalone); appends awg1 when a standalone AWG inbound with a
// distinct AWGServerAddress coexists with a chain entry (the multi-AWG-interface
// scheme — AGENTS.md #10), so sing-box captures traffic arriving on BOTH kernel
// AWG interfaces. Also appends the exit interfaces (awg-exit-nX) on a balancer.
func tunIncludeInterfaces(node *model.ChainNode) []string {
	ifaces := []string{"awg0"}
	ifaces = append(ifaces, exitInterfacesForNode(node)...)
	return ifaces
}

// tunIncludeInterfacesForNode is the nodeInfo-aware variant: it also appends awg1
// when nodeInfo has a standalone AWG inbound with a distinct subnet (co-located
// with a chain entry that claimed awg0). Used by the merged-config builder where
// both the chain node and the nodeInfo are in scope.
func tunIncludeInterfacesForNode(node *model.ChainNode, nodeInfo *model.NodeInfo) []string {
	ifaces := tunIncludeInterfaces(node)
	if nodeInfo != nil {
		for _, ib := range nodeInfo.Inbounds {
			if ib.Protocol == "awg" && ib.AWGServerAddress != "" {
				// A standalone AWG inbound with a distinct subnet → deployed on
				// awg1 (see RenderNodeAWGConfs). The TUN overlay must include it.
				ifaces = append(ifaces, "awg1")
				break
			}
		}
	}
	return ifaces
}

// balancerTagForNode returns the fallback balancer tag for a node, or "" when
// the node has fewer than two exit interfaces (no balancer needed).
func balancerTagForNode(node *model.ChainNode) string {
	if node == nil || len(exitInterfacesForNode(node)) < 2 {
		return ""
	}
	return fmt.Sprintf("%s-balancer", node.ID)
}

// awgForwardOutboundForRoles returns the inter-node outbound tag the TUN
// catch-all should forward to for a LINEAR AWG chain entry that has a
// downstream hop — so AWG user traffic reaches the next node instead of
// egressing from the entry node. Returns "" for:
//   - a multi-exit balancer (ExitTargets present): exits via awg-exit-nX, not a
//     chain hop — the balancer handles routing, no forward needed.
//   - a standalone AWG node (no chain role): egresses direct.
//   - a linear AWG entry that IS the last node (no downstream hop): egresses
//     direct.
//
// Only the linear-AWG-entry-with-downstream-hop case returns a non-empty tag.
func awgForwardOutboundForRoles(roles []chainRole) string {
	for _, r := range roles {
		if !r.IsEntry || r.Chain.UserProtocol != model.UserProtocolAWG {
			continue
		}
		// Multi-exit balancer: the balancer route handles this; no chain forward.
		if len(r.Node.ExitTargets) > 0 {
			return ""
		}
		if !r.HasOutbound {
			return "" // last node, no downstream hop → egress direct
		}
		return chainInterNodeOutboundTag(&r)
	}
	return ""
}

// awgTUNOverlayNeeded reports whether this node runs a kernel AWG server that
// needs a sing-box TUN overlay. True when:
//   - a chain role is an AWG user-entry (kernel awg0 accepts clients), or
//   - a chain role is a multi-exit balancer (ExitTargets present → awg-exit-nX), or
//   - the node has a standalone AWG inbound.
//
// Linear AWG transit (inter-node transport) does NOT need an overlay — that
// link is a point-to-point kernel tunnel between two nodes, not user-facing
// traffic to capture, and the existing transit wiring handles it.
func awgTUNOverlayNeeded(roles []chainRole, nodeInfo *model.NodeInfo) bool {
	for _, r := range roles {
		if r.IsEntry && r.Chain.UserProtocol == model.UserProtocolAWG {
			return true
		}
		if len(r.Node.ExitTargets) > 0 {
			return true
		}
	}
	if nodeInfo != nil {
		for _, ib := range nodeInfo.Inbounds {
			if ib.Protocol == "awg" {
				return true
			}
		}
	}
	return false
}

// awgOverlayNode returns the ChainNode whose ExitAWGLinks drive the TUN overlay
// (the multi-exit balancer), or the AWG entry node when there are no exit
// targets (single-egress). Returns nil when no overlay applies.
func awgOverlayNode(roles []chainRole, nodeInfo *model.NodeInfo) *model.ChainNode {
	for _, r := range roles {
		if len(r.Node.ExitTargets) > 0 {
			return r.Node
		}
	}
	for _, r := range roles {
		if r.IsEntry && r.Chain.UserProtocol == model.UserProtocolAWG {
			return r.Node
		}
	}
	// Standalone AWG inbound: no chain node carries exit links, so return a
	// zero-value ChainNode — the overlay uses awg0-only defaults (no exits).
	return nil
}
