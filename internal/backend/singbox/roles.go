package singbox

// roles.go — unified, role-based sing-box config generator.
//
// This replaces the two divergent generation branches that produced broken
// configs (standalone generateUser/generateTransport vs chain build*Inbound).
// Like the Python project's render_config(server), we pick a template by ROLE
// and render one consistent, sing-box-2.5.0-valid config:
//
//   - RoleProxyNode   : VLESS REALITY+XHTTP max obfuscation (terminating node)
//   - RoleAWGBalancer : kernel AWG (awg-quick) + TUN + bind_interface balancer
//   - RoleAWGHop      : userspace AWG wireguard endpoint with amnezia (chain hop)
//
// All generated credentials (UUID, Reality private key, short IDs, AWG keys)
// are persisted by the caller (chain applier / store) so re-generation does not
// rotate them and drop clients — see AGENTS.md rule 5.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
	"golang.org/x/crypto/curve25519"
)

// Role identifies a sing-box node's purpose.
type Role string

const (
	RoleProxyNode   Role = "proxy_node"
	RoleAWGBalancer Role = "awg_balancer"
	RoleAWGHop      Role = "awg_hop"
)

// RealitySNI is the default REALITY handshake target. TLS 1.3 + H2, popular,
// not DPI-blocked. Overridable via ProxyNodeParams.SNIDomain.
const defaultRealitySNI = "www.cloudflare.com"

// ─── ProxyNode ─────────────────────────────────────────────────────────────

// ProxyNodeParams configures a proxy_node (VLESS REALITY+XHTTP).
type ProxyNodeParams struct {
	ListenPort int    // default 443
	SNIDomain  string // REALITY target; default www.microsoft.com
	UUID       string // persisted; if empty a fresh one is generated
	// RealityPrivateKey (base64-url X25519) persisted; if empty, generated.
	RealityPrivateKey string
	// ShortIDs persisted (hex strings). If empty, generated (8 ids, first "").
	ShortIDs  []string
	XHTTPPath string // XHTTP path; if empty, a random one is generated
}

// RenderProxyNode renders a VLESS REALITY+XHTTP max-obfuscation sing-box config.
// The returned bytes are a valid, sing-box-check-passing JSON config.
func RenderProxyNode(p ProxyNodeParams) ([]byte, error) {
	if p.ListenPort == 0 {
		p.ListenPort = 443
	}
	sni := p.SNIDomain
	if sni == "" {
		sni = defaultRealitySNI
	}
	if p.UUID == "" {
		p.UUID = generateUUID()
	}
	if p.RealityPrivateKey == "" {
		priv, err := generateRealityPrivateKey()
		if err != nil {
			return nil, err
		}
		p.RealityPrivateKey = priv
	}
	if len(p.ShortIDs) == 0 {
		p.ShortIDs = generateRealityShortIDs(8)
	}
	if p.XHTTPPath == "" {
		p.XHTTPPath = randomXHTTPPath()
	}

	inbound := map[string]any{
		"type":        "vless",
		"tag":         "vless-reality-xhttp-in",
		"listen":      "0.0.0.0",
		"listen_port": p.ListenPort,
		"users": []map[string]any{
			{"name": "default", "uuid": p.UUID, "flow": "xtls-rprx-vision"},
		},
		"tls": map[string]any{
			"enabled":     true,
			"server_name": sni,
			"alpn":        []string{"h2", "http/1.1"},
			"min_version": "1.3",
			"max_version": "1.3",
			"reality": map[string]any{
				"enabled": true,
				"handshake": map[string]any{
					"server":      sni,
					"server_port": 443,
				},
				"private_key":         p.RealityPrivateKey,
				"short_id":            p.ShortIDs,
				"max_time_difference": "1m",
			},
			// NOTE: ECH is intentionally omitted — sing-box-extended rejects
			// "Reality is conflict with ECH". ECH applies to plain TLS, not REALITY.
		},
		"transport": xhttpTransportMap(sni, p.XHTTPPath),
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("marshal inbound: %w", err)
	}

	direct, err := marshal(config.DirectOutbound{Type: "direct", Tag: "direct"})
	if err != nil {
		return nil, err
	}
	block, err := marshal(config.BlockOutbound{Type: "block", Tag: "block"})
	if err != nil {
		return nil, err
	}

	cfg := config.SingboxConfig{
		Log: &config.LogOptions{Level: "info", Timestamp: true},
		DNS: &config.DNSConfig{
			Servers: []config.DNSServer{{Tag: "dns-cloudflare", Type: "udp", Server: "1.1.1.1"}},
			Final:   "dns-cloudflare",
		},
		Endpoints: []json.RawMessage{},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{direct, block},
		Route: &config.RoutingSection{
			Rules: []config.RouteRuleEntry{
				{Action: "sniff"},
				{Protocol: []string{"dns"}, Action: "hijack-dns"},
			},
			Final:                 "direct",
			AutoDetectInterface:   true,
			DefaultDomainResolver: "dns-cloudflare",
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// xhttpTransportMap builds the XHTTP transport sub-object with maximum
// obfuscation (tokenish padding, cookie placement, xmux, suppressed headers).
// Used by both the proxy_node inbound and the chain hop outbound.
func xhttpTransportMap(sni, path string) map[string]any {
	return map[string]any{
		"type":                     "xhttp",
		"mode":                     "packet-up",
		"host":                     sni,
		"path":                     path,
		"x_padding_bytes":          "100-1000",
		"x_padding_obfs_mode":      true,
		"x_padding_method":         "tokenish",
		"x_padding_placement":      "queryInHeader",
		"x_padding_key":            "x_padding",
		"x_padding_header":         "X-Padding",
		"session_placement":        "cookie",
		"seq_placement":            "cookie",
		"uplink_data_placement":    "cookie",
		"uplink_http_method":       "POST",
		"sc_max_each_post_bytes":   "50000-200000",
		"sc_min_posts_interval_ms": "30-100",
		"sc_max_buffered_posts":    30,
		"sc_stream_up_server_secs": "20-80",
		"no_grpc_header":           true,
		"no_sse_header":            true,
		"xmux": map[string]any{
			"max_concurrency":     "2-4",
			"h_max_request_times": "600-900",
			"h_max_reusable_secs": "1800-3000",
		},
		"headers": map[string]string{
			"User-Agent":      "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36",
			"Accept":          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			"Accept-Language": "en-US,en;q=0.5",
		},
	}
}

// ─── AWG Balancer (kernel) ─────────────────────────────────────────────────

// AWGBalancerParams configures a kernel-AWG balancer node: the AWG server
// interface (awg0) and the per-exit client interfaces (awg-exit-nX) are owned by
// the kernel via awg-quick; sing-box captures awg0 traffic through a TUN overlay
// and routes it across the exit interfaces via a fallback round-robin group with
// bind_interface direct outbounds. Mirrors the dns.idoctor.mom reference
// (VPN/orchestrator/app/templates/awg_balancer.json.j2).
//
// The kernel awg0.conf / awg-exit-nX.conf are rendered separately (the chain
// package's RenderServerAWGConf / RenderExitAWGConf) and pushed as their own
// files — this renderer only produces the sing-box config that sits on top.
type AWGBalancerParams struct {
	// ExitInterfaces are the kernel AWG client interface names (awg-exit-n1,
	// awg-exit-n2, ...) the balancer rotates across. Each becomes a direct
	// outbound with bind_interface. Empty produces a single-egress overlay
	// (no fallback group — route targets the bare direct outbound).
	ExitInterfaces []string
	// BalancerTag is the fallback group tag. Empty defaults to "balancer".
	// Ignored when fewer than two exit interfaces are present.
	BalancerTag string
}

// RenderAWGBalancer renders a kernel-AWG balancer sing-box config: a TUN
// inbound capturing awg0, one direct outbound per exit interface bound to that
// interface, a fallback group rotating across them, and route rules steering
// TUN traffic to the balancer. Endpoints is intentionally empty — the kernel
// owns the WireGuard interfaces; a userspace endpoint would panic with
// chacha20poly1305 under AmneziaWG obfuscation.
func RenderAWGBalancer(p AWGBalancerParams) ([]byte, error) {
	if p.BalancerTag == "" {
		p.BalancerTag = "balancer"
	}

	// include_interface MUST list awg0 AND every awg-exit-nX the balancer
	// owns. awg0 captures user/client traffic; awg-exit-nX captures the
	// RESPONSE traffic for sing-box direct outbounds that use
	// bind_interface: awg-exit-nX (without it the kernel delivers the
	// SYN-ACK to a dead local socket and the dial times out — egress through
	// the balancer silently fails). Live-verified 2026-07-04.
	includeIfaces := []string{"awg0"}
	includeIfaces = append(includeIfaces, p.ExitInterfaces...)
	tun := config.TUNInbound{
		Type:             "tun",
		Tag:              "tun-in",
		InterfaceName:    "sing-box-tun",
		Address:          []string{"172.16.250.1/30"},
		MTU:              1200,
		Stack:            "mixed", // kernel TCP + gVisor UDP so QUIC through-traffic works
		AutoRoute:        true,
		IncludeInterface: includeIfaces,
		StrictRoute:      false,
	}
	tunJSON, err := marshal(tun)
	if err != nil {
		return nil, err
	}
	direct, err := marshal(config.DirectOutbound{Type: "direct", Tag: "direct"})
	if err != nil {
		return nil, err
	}
	block, err := marshal(config.BlockOutbound{Type: "block", Tag: "block"})
	if err != nil {
		return nil, err
	}

	outbounds := []json.RawMessage{direct, block}

	// One direct outbound per exit interface, bound to the kernel AWG iface.
	exitTags := make([]string, 0, len(p.ExitInterfaces))
	for _, iface := range p.ExitInterfaces {
		tag := "exit-" + iface // awg-exit-n1 -> exit-awg-exit-n1 (stable, route-referenced)
		ob, err := marshal(config.DirectOutbound{
			Type:          "direct",
			Tag:           tag,
			BindInterface: iface,
		})
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, ob)
		exitTags = append(exitTags, tag)
	}

	// Fallback balancer rotating across the exit outbounds (round-robin on the
	// patched sing-box-extended build; priority fallback on vanilla). Only when
	// there is more than one exit — a single exit routes directly.
	routeOut := "direct"
	if len(exitTags) > 1 {
		fb, err := marshal(config.FallbackOutbound{
			Type:             "fallback",
			Tag:              p.BalancerTag,
			Outbounds:        exitTags,
			BlacklistTimeout: "30s",
		})
		if err != nil {
			return nil, err
		}
		outbounds = append(outbounds, fb)
		routeOut = p.BalancerTag
	} else if len(exitTags) == 1 {
		routeOut = exitTags[0]
	}

	// Route rules match the reference: sniff → BitTorrent block → DNS hijack →
	// TUN traffic to the balancer. inbound:["tun-in"] (NOT source_ip_cidr)
	// because TUN NAT changes the source IP (nuances-bugs §source_ip_cidr vs inbound).
	rules := []config.RouteRuleEntry{
		{Action: "sniff"},
		{Protocol: []string{"bittorrent"}, Outbound: "block"},
		{Protocol: []string{"dns"}, Action: "hijack-dns"},
		{Inbound: []string{"tun-in"}, Outbound: routeOut},
	}

	cfg := config.SingboxConfig{
		Log:       &config.LogOptions{Level: "info", Timestamp: true},
		Endpoints: []json.RawMessage{}, // empty — kernel owns AWG; no userspace endpoint
		Inbounds:  []json.RawMessage{tunJSON},
		Outbounds: outbounds,
		Route: &config.RoutingSection{
			Rules:               rules,
			Final:               "direct",
			AutoDetectInterface: true,
		},
		Experimental: &config.ExperimentalOptions{CacheFile: &config.CacheFileOptions{Enabled: true}},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ─── AWG Hop (userspace, chain hop) ────────────────────────────────────────

// AWGHopParams configures a userspace AWG wireguard endpoint used as a chain
// hop. This path runs sing-box's wireguard endpoint with amnezia — which is
// only safe because the patched binary (deps/) fixes the chacha20poly1305
// buffer-overlap panic. I1-I5 are optional; when present the deploy path pads
// odd-length hex to even (sing-box wireguard-go rejects odd-length hex).
type AWGHopParams struct {
	Tag          string
	ListenPort   int
	Address      []string // e.g. ["10.8.0.1/24"]
	PrivateKey   string   // base64 WG/AWG private key; persisted
	PeerPubKey   string   // next-hop public key
	PeerEndpoint string   // next-hop host:port (empty for the final hop)
	Amnezia      *config.AmneziaOptions
}

// RenderAWGHop renders a config whose only inbound is a sing-box wireguard
// endpoint with an amnezia block. Outbound is direct (the hop egresses to the
// next hop via the peer endpoint / kernel route).
func RenderAWGHop(p AWGHopParams) ([]byte, error) {
	if p.Tag == "" {
		p.Tag = "awg-hop"
	}
	if p.ListenPort == 0 {
		p.ListenPort = 51820
	}
	if len(p.Address) == 0 {
		p.Address = []string{"10.8.0.1/24"}
	}
	if p.PrivateKey == "" {
		priv, pub, err := generateWGKeypair()
		if err != nil {
			return nil, err
		}
		p.PrivateKey = priv
		_ = pub // peer pubkey is supplied separately for hops
	}

	peer := map[string]any{
		"public_key":  p.PeerPubKey,
		"allowed_ips": []string{"0.0.0.0/0"},
	}
	if p.PeerEndpoint != "" {
		host, port := splitEndpoint(p.PeerEndpoint)
		peer["address"] = host
		if port != 0 {
			peer["port"] = port
		}
		peer["persistent_keepalive_interval"] = 25
	}

	endpoint := map[string]any{
		"type":        "wireguard",
		"tag":         p.Tag,
		"system":      false,
		"mtu":         1420, // match all other AWG endpoints (buildAWGUserInbound*, buildAWGTransport*, generateAWGUser); MTU must match on both ends of a WireGuard pair or large packets fragment/drop.
		"address":     p.Address,
		"private_key": p.PrivateKey,
		"peers":       []map[string]any{peer},
	}
	if p.Amnezia != nil {
		endpoint["amnezia"] = p.Amnezia
	}
	epJSON, err := json.Marshal(endpoint)
	if err != nil {
		return nil, fmt.Errorf("marshal wireguard endpoint: %w", err)
	}

	direct, err := marshal(config.DirectOutbound{Type: "direct", Tag: "direct"})
	if err != nil {
		return nil, err
	}
	block, err := marshal(config.BlockOutbound{Type: "block", Tag: "block"})
	if err != nil {
		return nil, err
	}

	cfg := config.SingboxConfig{
		Log:       &config.LogOptions{Level: "info", Timestamp: true},
		Endpoints: []json.RawMessage{epJSON},
		Outbounds: []json.RawMessage{direct, block},
		Route: &config.RoutingSection{
			Rules:               []config.RouteRuleEntry{{Action: "sniff"}},
			Final:               "direct",
			AutoDetectInterface: true,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ─── shared helpers ─────────────────────────────────────────────────────────

// marshal serializes v to a json.RawMessage, returning an error instead of
// panicking. Marshal of our typed structs should never fail in practice, but a
// future field change (e.g. a non-string Key with omitempty) could make one
// un-marshalable — returning the error keeps the deploy path from crashing the
// orchestrator (CTO-review #3: no panics in the request/deploy path).
func marshal(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("singbox: marshal: %w", err)
	}
	return b, nil
}

// generateRealityPrivateKey returns a base64-url X25519 key pair's private half
// (REALITY uses the same X25519 as WireGuard).
func generateRealityPrivateKey() (string, error) {
	priv := make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		return "", err
	}
	// Clamp per RFC 7748 like wireguard-go does.
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	return base64.RawURLEncoding.EncodeToString(priv), nil
}

// generateRealityShortIDs returns n short ids (first is ""), even-length hex.
func generateRealityShortIDs(n int) []string {
	ids := make([]string, 0, n)
	ids = append(ids, "")
	for i := 1; i < n; i++ {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		ids = append(ids, hex.EncodeToString(b))
	}
	return ids
}

// generateWGKeypair returns base64 WireGuard private/public keys.
func generateWGKeypair() (privB64, pubB64 string, err error) {
	priv := make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return "", "", err
	}
	priv[0] &= 248
	priv[31] &= 127
	priv[31] |= 64
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(priv), base64.StdEncoding.EncodeToString(pub), nil
}

// randomXHTTPPath returns a random XHTTP path like "/<8 url-safe chars>".
func randomXHTTPPath() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "/" + base64.RawURLEncoding.EncodeToString(b)
}

// splitEndpoint splits host:port into host and int port. port is 0 if absent.
// It uses net.SplitHostPort so bracketed IPv6 literals ([2001:db8::1]:51820)
// and bare IPv6 addresses parse correctly instead of being split at the last
// ':' (CTO-review H8).
func splitEndpoint(ep string) (string, int) {
	host, portStr, err := net.SplitHostPort(ep)
	if err != nil {
		// No port present: the whole input is the host (covers bare IPv6 like
		// "2001:db8::1" and plain hostnames like "example.com").
		return ep, 0
	}
	port := 0
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}
	return host, port
}
