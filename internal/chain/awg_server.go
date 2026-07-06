package chain

// awg_server.go — server-side awg0.conf generator for the kernel-awg-quick +
// sing-box-TUN-overlay architecture. The AWG server interface is owned by the
// kernel (awg-quick@awg0), NOT by a sing-box userspace endpoint — userspace
// wireguard-go panics with chacha20poly1305 under AmneziaWG obfuscation
// (VPN/docs/sing-box-extended.md). sing-box only routes traffic via a TUN
// overlay (include_interface:["awg0"]) on top of this kernel interface.
//
// The .conf format mirrors the per-user client .conf (renderAWGQuickConf in
// clientconfig.go) but inverted: [Interface] holds the SERVER private key,
// listen port and amnezia obfuscation (which awg-quick parses only within
// [Interface] — never after [Peer]); one [Peer] per user carrying the user's
// public key and AllowedIPs = the user's inner tunnel IP (10.8.0.X/32), so the
// kernel can route per-client by source IP. Itime is intentionally omitted
// (awg setconf rejects "Itime"; sing-box UAPI rejects "itime").

import (
	"fmt"
	"net"
	"strings"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// AWGServerPeer is one [Peer] entry in the server-side awg0.conf: the user's
// WireGuard public key and their inner tunnel IP (the peer's AllowedIPs,
// which doubles as the per-client source_ip_cidr route key).
type AWGServerPeer struct {
	PublicKey  string // user.AWGPublicKey — the peer's WG public key
	AllowedIPs string // user.AWGAddress, e.g. "10.8.0.2/32" (single host route)
}

// AWGServerConfParams describes a kernel awg0.conf to render.
type AWGServerConfParams struct {
	// ServerPrivateKey is the AWG server's WireGuard private key
	// (model.Chain.AWGEntryServerPriv for chain entry; node.AWGClientPriv for
	// standalone). Required.
	ServerPrivateKey string
	// ListenPort is the UDP port awg0 binds (the user-entry port, e.g. 51820).
	ListenPort int
	// TunnelAddress is the server's inner tunnel IP with CIDR, e.g.
	// "10.8.0.1/24" (chain user-entry) or "10.9.0.1/24" (AWG transit inbound).
	TunnelAddress string
	// MTU for the awg0 interface. 1420 matches the client .conf; AWG transit
	// uses 1280. Zero defaults to 1420.
	MTU int
	// Amnezia is the obfuscation block (JC/JMIN/JMAX/S1-S4/H1-H4/I1-I5).
	// nil disables obfuscation (plain WireGuard). Server and client MUST share
	// identical values — build it from the same *AWGObfsMaterial via
	// BuildAmneziaSection so CPS I1-I5 / H1-H4 match.
	Amnezia *config.AmneziaOptions
	// Peers are the per-user [Peer] entries (one per connected user).
	Peers []AWGServerPeer
	// TUNInterface is the sing-box TUN overlay interface name to forward
	// traffic to/from. When non-empty (the kernel-AWG + sing-box-TUN-overlay
	// architecture), PostUp/PostDown lines are emitted with iptables FORWARD
	// rules between the AWG interface and the TUN interface — without these the
	// kernel routes AWG→TUN via policy routing (table 2022) but the FORWARD
	// chain drops return traffic, so egress through the tunnel silently fails
	// (verified: the working dns.idoctor.mom reference has exactly these rules).
	// Empty = no PostUp/PostDown (non-overlay use, e.g. plain AWG without sing-box).
	TUNInterface string
	// InterfaceName is the kernel AWG interface name (awg0, awg1, ...). Empty
	// defaults to "awg0". Used in PostUp/PostDown rp_filter/FORWARD rules so a
	// second standalone AWG inbound (awg1, distinct subnet) gets its own rules
	// coexisting with the chain entry's awg0 (AGENTS.md Known Issue #10
	// multi-AWG-interface follow-up).
	InterfaceName string
}

// RenderServerAWGConf renders a kernel awg-quick .conf for the AWG server
// interface (awg0). The amnezia block is emitted inside [Interface] BEFORE
// [Peer] — awg-quick strips the .conf and passes it to `awg setconf`, which
// parses amnezia fields only within [Interface]; after [Peer] it fails with
// "Line unrecognized: Jc=...". Itime is never written (runtime-breaking: awg
// setconf rejects "Itime", sing-box UAPI rejects "itime").
func RenderServerAWGConf(p AWGServerConfParams) string {
	if p.TunnelAddress == "" {
		p.TunnelAddress = "10.8.0.1/24"
	}
	if p.MTU == 0 {
		p.MTU = 1420
	}
	if p.InterfaceName == "" {
		p.InterfaceName = "awg0"
	}
	iface := p.InterfaceName
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", p.TunnelAddress))
	if p.ListenPort > 0 {
		b.WriteString(fmt.Sprintf("ListenPort = %d\n", p.ListenPort))
	}
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", p.ServerPrivateKey))
	b.WriteString(fmt.Sprintf("MTU = %d\n", p.MTU))

	// Amnezia obfuscation — must sit in [Interface], before [Peer]. Values are
	// shared server↔client (built from the same persisted AWGObfsMaterial) so
	// the CPS handshake matches. Itime is dropped on purpose (see file header).
	if p.Amnezia != nil {
		writeAmneziaConfLines(&b, p.Amnezia)
	}

	// PostUp/PostDown: when a TUN overlay interface is specified, add iptables
	// FORWARD rules between the AWG interface and the TUN so the kernel's policy
	// routing (table 2022, set by sing-box auto_route) actually delivers traffic.
	// Without these the FORWARD chain can drop return traffic and egress through
	// the tunnel silently fails. Mirrors the dns.idoctor.mom reference awg0.conf.
	// Also sets ip_forward=1 (belt-and-braces — the deploy flow sets it via
	// sysctl too, but awg-quick restarting without it would break). Also disables
	// rp_filter on the AWG interface — the kernel's strict reverse-path check
	// drops tunneled packets whose source IP (10.8.0.x) would route back via a
	// different interface than the one they arrived on, silently breaking egress
	// through the TUN overlay (live-verified 2026-07-04). awg-quick recreates
	// the interface on every restart, so the sysctl must be re-applied in PostUp.
	// The AWG interface name (awg0/awg1) is parameterized so a second standalone
	// AWG inbound on awg1 gets its own rules coexisting with awg0.
	if p.TUNInterface != "" {
		tun := p.TUNInterface
		b.WriteString(fmt.Sprintf(
			"PostUp = echo 1 > /proc/sys/net/ipv4/ip_forward; "+
				"sysctl -w net.ipv4.conf.%s.rp_filter=0 2>/dev/null; "+
				"iptables -C FORWARD -i %s -o %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -i %s -o %s -j ACCEPT; "+
				"iptables -C FORWARD -i %s -o %s -j ACCEPT 2>/dev/null || iptables -A FORWARD -i %s -o %s -j ACCEPT\n",
			iface, iface, tun, iface, tun, tun, iface, tun, iface))
		b.WriteString(fmt.Sprintf(
			"PostDown = iptables -D FORWARD -i %s -o %s -j ACCEPT 2>/dev/null || true; "+
				"iptables -D FORWARD -i %s -o %s -j ACCEPT 2>/dev/null || true\n",
			iface, tun, tun, iface))
	}

	for _, peer := range p.Peers {
		if peer.PublicKey == "" {
			continue // skip uncredentialed users rather than emit a broken [Peer]
		}
		b.WriteString("\n")
		b.WriteString("[Peer]\n")
		b.WriteString(fmt.Sprintf("PublicKey = %s\n", peer.PublicKey))
		allowed := peer.AllowedIPs
		if allowed == "" {
			allowed = "10.8.0.2/32"
		}
		b.WriteString(fmt.Sprintf("AllowedIPs = %s\n", allowed))
	}
	return b.String()
}

// writeAmneziaConfLines writes the Jc/Jmin/Jmax/S1-S4/H1-H4/I1-I5 fields into
// the [Interface] section in the exact order awg-quick/awg setconf expects.
// Itime is intentionally omitted (runtime-breaking in both awg setconf and
// sing-box UAPI — see AGENTS.md Known Issues and commit 6f1a108).
func writeAmneziaConfLines(b *strings.Builder, a *config.AmneziaOptions) {
	b.WriteString(fmt.Sprintf("Jc = %d\n", a.JC))
	b.WriteString(fmt.Sprintf("Jmin = %d\n", a.JMIN))
	b.WriteString(fmt.Sprintf("Jmax = %d\n", a.JMAX))
	b.WriteString(fmt.Sprintf("S1 = %d\n", a.S1))
	b.WriteString(fmt.Sprintf("S2 = %d\n", a.S2))
	b.WriteString(fmt.Sprintf("S3 = %d\n", a.S3))
	b.WriteString(fmt.Sprintf("S4 = %d\n", a.S4))
	if a.H1 != "" {
		b.WriteString(fmt.Sprintf("H1 = %s\n", a.H1))
		b.WriteString(fmt.Sprintf("H2 = %s\n", a.H2))
		b.WriteString(fmt.Sprintf("H3 = %s\n", a.H3))
		b.WriteString(fmt.Sprintf("H4 = %s\n", a.H4))
	}
	if a.I1 != "" {
		b.WriteString(fmt.Sprintf("I1 = %s\n", a.I1))
		b.WriteString(fmt.Sprintf("I2 = %s\n", a.I2))
		b.WriteString(fmt.Sprintf("I3 = %s\n", a.I3))
		b.WriteString(fmt.Sprintf("I4 = %s\n", a.I4))
		b.WriteString(fmt.Sprintf("I5 = %s\n", a.I5))
	}
}

// ─── Exit tunnels (multi-exit balancer) ────────────────────────────────────
//
// A multi-exit balancer node runs N kernel AWG client interfaces
// (awg-exit-n1, awg-exit-n2, ...) — one per exit target — each dialing its
// remote exit server. Each exit target is a ChainNode with Role=exit running
// its own kernel awg0 that accepts the balancer's tunnel. These .conf files
// are pushed alongside the sing-box config and enabled as
// awg-quick@awg-exit-nX / awg-quick@awg0 respectively. sing-box then binds
// direct outbounds to the awg-exit-nX interfaces and rotates across them via
// the fallback balancer (BuildAWGTUNOverlay / RenderAWGBalancer).
//
// Exit links are service tunnels between trusted servers; amnezia obfuscation
// is OFF by default (the DPI-facing surface is the user-entry awg0, not these).
// A non-nil Amnezia opts in when an operator wants to obfuscate inter-node
// traffic too.

// ExitClientConfParams describes a balancer-side kernel awg-exit-nX.conf: the
// client end of an exit link, dialing the remote exit server.
type ExitClientConfParams struct {
	// InterfaceName is the kernel interface name, e.g. "awg-exit-n1". Used only
	// for documentation — awg-quick derives the interface from the filename.
	InterfaceName string
	// ClientPrivateKey is the balancer's WireGuard private key for this link
	// (AWGExitLink.ClientPriv). Required.
	ClientPrivateKey string
	// ClientAddress is the balancer's inner tunnel IP for this link, e.g.
	// "10.10.0.2/32" (AWGExitLink.Address). Required.
	ClientAddress string
	// ClientListenPort is the fixed source port the client binds (helps NAT'd
	// VPSes — without it awg-quick picks a random ephemeral port and handshake
	// responses can miss it after a re-handshake). 0 = omit (awg-quick chooses).
	ClientListenPort int
	// MTU for the exit interface. 0 defaults to 1280 (matches RenderAWGHop's
	// inter-node MTU; smaller than the user-entry 1420 to leave headroom for
	// the double-encapsulation through the exit).
	MTU int
	// ExitPublicKey is the remote exit server's WireGuard public key
	// (ChainNode.ExitAWGServerPub). Required.
	ExitPublicKey string
	// ExitEndpoint is the remote exit server's "host:port" (the exit node's SSH
	// host + its ExitAWGListenPort). Required.
	ExitEndpoint string
	// Amnezia, when non-nil, enables amnezia obfuscation on this exit link
	// (fields go in [Interface] before [Peer], same as RenderServerAWGConf).
	Amnezia *config.AmneziaOptions
}

// RenderExitAWGConf renders a balancer-side kernel awg-exit-nX.conf — the client
// tunnel to one remote exit server. The interface name is implied by the
// filename (awg-quick@awg-exit-nX ← /etc/amnezia/amneziawg/awg-exit-nX.conf);
// InterfaceName here is documentary only.
func RenderExitAWGConf(p ExitClientConfParams) string {
	if p.ClientAddress == "" {
		p.ClientAddress = "10.10.0.2/32"
	}
	if p.MTU == 0 {
		p.MTU = 1280
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", p.ClientAddress))
	// Table = off is CRITICAL: without it awg-quick installs a default route
	// (because AllowedIPs = 0.0.0.0/0) through the exit tunnel, which captures
	// ALL egress traffic — including SSH — and locks out the VPS. With Table = off
	// awg-quick creates the interface but does NOT touch the routing table;
	// sing-box's bind_interface handles routing instead (it binds outbound sockets
	// directly to the awg-exit-nX interface, no route table entry needed). This
	// is how the real dns.idoctor.mom server runs 4 exit tunnels simultaneously
	// without lockout (each has Table = off in its [Interface]).
	b.WriteString("Table = off\n")
	if p.ClientListenPort > 0 {
		b.WriteString(fmt.Sprintf("ListenPort = %d\n", p.ClientListenPort))
	}
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", p.ClientPrivateKey))
	b.WriteString(fmt.Sprintf("MTU = %d\n", p.MTU))
	if p.Amnezia != nil {
		writeAmneziaConfLines(&b, p.Amnezia)
	}
	// rp_filter=0 on the exit-client interface is CRITICAL for balancer egress
	// (live-verified 2026-07-04). sing-box direct outbounds use bind_interface:
	// awg-exit-nX to dial through this kernel tunnel (source = the balancer's
	// inner IP 10.10.0.X). The SYN-ACK arrives on awg-exit-nX with dst=10.10.0.X;
	// with rp_filter=1 the kernel's strict reverse-path check drops it (10.10.0.X
	// routes to `local`, but the packet arrived on awg-exit-nX, not lo), so
	// sing-box never sees the response and the dial times out — egress through
	// the balancer silently fails. awg-quick recreates the interface on every
	// restart, so the sysctl must be re-applied in PostUp.
	iface := p.InterfaceName
	if iface == "" {
		iface = "awg-exit-n0"
	}
	b.WriteString(fmt.Sprintf("PostUp = sysctl -w net.ipv4.conf.%s.rp_filter=0 2>/dev/null\n", iface))
	b.WriteString("\n[Peer]\n")
	b.WriteString(fmt.Sprintf("PublicKey = %s\n", p.ExitPublicKey))
	host, port := splitHostPort(p.ExitEndpoint)
	if host != "" {
		b.WriteString(fmt.Sprintf("Endpoint = %s:%d\n", host, port))
	}
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String()
}

// ExitServerConfParams describes a Role=exit node's kernel awg0.conf — the
// server end of an exit link, accepting the balancer's tunnel.
type ExitServerConfParams struct {
	// ServerPrivateKey is the exit node's WireGuard private key
	// (ChainNode.ExitAWGServerPriv). Required.
	ServerPrivateKey string
	// ListenPort is the UDP port the exit awg0 binds (ChainNode.ExitAWGListenPort).
	ListenPort int
	// TunnelAddress is the exit server's inner tunnel IP with CIDR, e.g.
	// "10.11.0.1/24". Empty defaults to 10.11.0.1/24.
	TunnelAddress string
	// MTU for the exit server interface. 0 defaults to 1420.
	MTU int
	// BalancerPublicKey is the balancer's client public key for this link
	// (AWGExitLink.ClientPub). The exit accepts only this peer.
	BalancerPublicKey string
	// BalancerAllowedIPs is the AllowedIPs for the balancer peer — the
	// balancer's inner IP (AWGExitLink.Address), e.g. "10.10.0.2/32". Empty
	// defaults to 10.10.0.2/32.
	BalancerAllowedIPs string
	// Amnezia, when non-nil, enables amnezia obfuscation (must match the
	// balancer's ExitClientConfParams.Amnezia for the handshake to succeed).
	Amnezia *config.AmneziaOptions
	// MASQUERADENetwork, when non-empty, emits PostUp/PostDown lines with
	// iptables MASQUERADE for each listed subnet — NATs tunneled traffic to
	// the exit's public IP so the internet routes responses back. Without
	// this, the exit sends packets to the internet with the private inner IP
	// as source — the internet can't route the response back, so egress
	// silently fails (data out, nothing back).
	//
	// CRITICAL (verified live 2026-07-04): an exit serving a balancer must
	// NAT BOTH subnets that can arrive on awg0:
	//   - the user subnet (10.8.0.0/24) — direct user→exit (linear chain)
	//   - the balancer-link subnet (10.10.0.0/24) — balancer awg-exit-nX
	//     inner IP (AWGExitLink.Address), used when a client reaches the exit
	//     THROUGH a balancer (entry tun-in → n1-direct bind_interface
	//     awg-exit-n1 → exit awg0). The balancer does not SNAT the client's
	//     source, so packets arrive at the exit with source 10.10.0.2 — a
	//     MASQUERADE covering only 10.8.0.0/24 leaves 10.10.0.0/24 un-NAT'd,
	//     the internet can't route responses back to 10.10.0.2, and egress
	//     through the balancer silently times out (data out, nothing back).
	//
	// Accepts a comma- or space-separated list (e.g. "10.8.0.0/24,10.10.0.0/24");
	// each entry gets its own MASQUERADE rule. Mirrors the real exit server
	// (n1) PostUp:
	//   iptables -t nat -A POSTROUTING -s <network> -o <wan> -j MASQUERADE
	// Also adds FORWARD ACCEPT for awg0 (so the kernel forwards tunneled→wan).
	// Empty = no MASQUERADE (exit used only for balancer, not direct internet).
	MASQUERADENetwork string
	// WANInterface is the exit's public-facing interface for MASQUERADE (e.g.
	// "ens4"). Auto-detected when empty via `ip route show default` — the
	// PostUp script resolves it at runtime so we don't hardcode a wrong iface.
	WANInterface string
}

// RenderExitServerAWGConf renders a Role=exit node's kernel awg0.conf — the
// server end of an exit link. It accepts exactly one peer (the balancer's
// client for this link). Multiple balancers would each be a separate [Peer].
func RenderExitServerAWGConf(p ExitServerConfParams) string {
	if p.TunnelAddress == "" {
		p.TunnelAddress = "10.11.0.1/24"
	}
	if p.MTU == 0 {
		p.MTU = 1420
	}
	if p.BalancerAllowedIPs == "" {
		p.BalancerAllowedIPs = "10.10.0.2/32"
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", p.TunnelAddress))
	if p.ListenPort > 0 {
		b.WriteString(fmt.Sprintf("ListenPort = %d\n", p.ListenPort))
	}
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", p.ServerPrivateKey))
	b.WriteString(fmt.Sprintf("MTU = %d\n", p.MTU))
	if p.Amnezia != nil {
		writeAmneziaConfLines(&b, p.Amnezia)
	}
	// MASQUERADE + FORWARD for internet egress: when MASQUERADENetwork is set,
	// emit PostUp/PostDown that NAT tunneled user traffic to the exit's public
	// IP. Without this the exit sends packets to the internet with the user's
	// private inner IP (10.8.0.x) — the internet can't route responses back,
	// so egress silently fails (data out, nothing back). Mirrors the real exit
	// server (n1) PostUp. The WAN interface is auto-detected at runtime via
	// `ip route show default` when WANInterface is empty (don't hardcode it —
	// different VPSes have different iface names: ens3, ens4, eth0...).
	if p.MASQUERADENetwork != "" {
		wan := p.WANInterface
		if wan == "" {
			// Auto-detect: resolve the default-route interface at PostUp time.
			// `ip -o route show default` prints "default via X dev IFACE" — awk
			// extracts IFACE. This runs on the remote VPS, not the orchestrator.
			wan = "$(ip -o route show default 0.0.0.0/0 2>/dev/null | awk '{print $5}' | head -1)"
		}
		// Parse the subnet list (comma- or space-separated) and emit one
		// MASQUERADE add/del per subnet. A single subnet keeps the old shape
		// (one rule); two subnets (user + balancer-link) get two rules. Each
		// is idempotent via -C ... || -A so re-applying is safe.
		nets := strings.FieldsFunc(p.MASQUERADENetwork, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		var masqUp, masqDown strings.Builder
		for _, n := range nets {
			n = strings.TrimSpace(n)
			if n == "" {
				continue
			}
			masqUp.WriteString(fmt.Sprintf(
				"iptables -t nat -C POSTROUTING -s %s -o $WAN -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s %s -o $WAN -j MASQUERADE; ",
				n, n))
			masqDown.WriteString(fmt.Sprintf(
				"iptables -t nat -D POSTROUTING -s %s -o $WAN -j MASQUERADE 2>/dev/null || true; ",
				n))
		}
		b.WriteString(fmt.Sprintf(
			"PostUp = echo 1 > /proc/sys/net/ipv4/ip_forward; "+
				"sysctl -w net.ipv4.conf.awg0.rp_filter=0 2>/dev/null; "+
				"WAN=%s; %s"+
				"iptables -C FORWARD -i awg0 -j ACCEPT 2>/dev/null || iptables -A FORWARD -i awg0 -j ACCEPT; "+
				"iptables -C FORWARD -o awg0 -j ACCEPT 2>/dev/null || iptables -A FORWARD -o awg0 -j ACCEPT\n",
			wan, masqUp.String()))
		b.WriteString(fmt.Sprintf(
			"PostDown = WAN=%s; %s"+
				"iptables -D FORWARD -i awg0 -j ACCEPT 2>/dev/null || true; "+
				"iptables -D FORWARD -o awg0 -j ACCEPT 2>/dev/null || true\n",
			wan, masqDown.String()))
	}
	if p.BalancerPublicKey != "" {
		b.WriteString("\n[Peer]\n")
		b.WriteString(fmt.Sprintf("PublicKey = %s\n", p.BalancerPublicKey))
		b.WriteString(fmt.Sprintf("AllowedIPs = %s\n", p.BalancerAllowedIPs))
	}
	return b.String()
}

// splitHostPort splits "host:port" into host and int port, returning
// (host, 0) when there is no port. Tolerates bare hosts (no port) and
// bracketed IPv6 ([::1]:51820) via net.SplitHostPort.
func splitHostPort(endpoint string) (string, int) {
	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		return endpoint, 0
	}
	port := 0
	fmt.Sscanf(portStr, "%d", &port)
	return host, port
}
