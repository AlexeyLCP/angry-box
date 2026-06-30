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
const defaultRealitySNI = "www.microsoft.com"

// ─── ProxyNode ─────────────────────────────────────────────────────────────

// ProxyNodeParams configures a proxy_node (VLESS REALITY+XHTTP).
type ProxyNodeParams struct {
	ListenPort int    // default 443
	SNIDomain  string // REALITY target; default www.microsoft.com
	UUID       string // persisted; if empty a fresh one is generated
	// RealityPrivateKey (base64-url X25519) persisted; if empty, generated.
	RealityPrivateKey string
	// ShortIDs persisted (hex strings). If empty, generated (8 ids, first "").
	ShortIDs []string
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
			"enabled":           true,
			"server_name":       sni,
			"alpn":              []string{"h2", "http/1.1"},
			"min_version":       "1.3",
			"max_version":       "1.3",
			"curve_preferences": []string{"X25519", "X25519MLKEM768"},
			"reality": map[string]any{
				"enabled": true,
				"handshake": map[string]any{
					"server":      sni,
					"server_port": 443,
				},
				"private_key":        p.RealityPrivateKey,
				"short_id":           p.ShortIDs,
				"max_time_difference": "1m",
			},
			"ech": map[string]any{
				"enabled":                     true,
				"key":                         []string{}, // operator runs `sing-box generate ech-keypair`
				"pq_signature_schemes_enabled": true,
			},
		},
		"transport": xhttpTransportMap(sni, p.XHTTPPath),
	}

	inboundJSON, err := json.Marshal(inbound)
	if err != nil {
		return nil, fmt.Errorf("marshal inbound: %w", err)
	}

	cfg := config.SingboxConfig{
		Log: &config.LogOptions{Level: "info", Timestamp: true},
		DNS: &config.DNSConfig{
			Servers: []config.DNSServer{{Tag: "dns-cloudflare", Type: "udp", Server: "1.1.1.1"}},
			Final:   "dns-cloudflare",
		},
		Endpoints: []json.RawMessage{},
		Inbounds:  []json.RawMessage{inboundJSON},
		Outbounds: []json.RawMessage{
			mustMarshal(config.DirectOutbound{Type: "direct", Tag: "direct"}),
			mustMarshal(config.BlockOutbound{Type: "block", Tag: "block"}),
		},
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
		"type":                    "xhttp",
		"mode":                    "packet-up",
		"host":                    sni,
		"path":                    path,
		"x_padding_bytes":         "100-1000",
		"x_padding_obfs_mode":     true,
		"x_padding_method":        "tokenish",
		"x_padding_placement":     "queryInHeader",
		"x_padding_key":           "x_padding",
		"x_padding_header":        "X-Padding",
		"session_placement":       "cookie",
		"seq_placement":           "cookie",
		"uplink_data_placement":   "cookie",
		"uplink_http_method":      "POST",
		"sc_max_each_post_bytes":  "50000-200000",
		"sc_min_posts_interval_ms": "30-100",
		"sc_max_buffered_posts":   30,
		"sc_stream_up_server_secs": "20-80",
		"no_grpc_header":          true,
		"no_sse_header":           true,
		"xmux": map[string]any{
			"max_concurrency":    "2-4",
			"h_max_request_times": "600-900",
			"h_max_reusable_secs": "1800-3000",
		},
		"headers": map[string][]string{
			"User-Agent":      {"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"},
			"Accept":          {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
			"Accept-Language": {"en-US,en;q=0.5"},
		},
	}
}

// ─── AWG Balancer (kernel) ─────────────────────────────────────────────────

// AWGBalancerExit is one kernel AWG exit interface the balancer binds to.
type AWGBalancerExit struct {
	Tag           string // outbound tag, e.g. "exit-n1"
	InterfaceName string // kernel interface, e.g. "awg-exit-n1"
}

// AWGBalancerParams configures an awg_balancer (kernel AWG + TUN + fallback).
type AWGBalancerParams struct {
	Exits       []AWGBalancerExit
	Strategy    string // "fallback" (default, patched round-robin) or "urltest"
	AutoRedirect bool
}

// RenderAWGBalancer renders a kernel-AWG balancer config: empty endpoints, TUN
// inbound with include_interface:["awg0"], per-exit direct+bind_interface, and
// a fallback/urltest balancer. NO amnezia in the JSON — obfuscation lives in
// the kernel awg-*.conf files managed by awg-quick.
func RenderAWGBalancer(p AWGBalancerParams) ([]byte, error) {
	if p.Strategy == "" {
		p.Strategy = "fallback"
	}
	if len(p.Exits) == 0 {
		return nil, fmt.Errorf("awg_balancer needs at least one exit interface")
	}

	tun := config.TUNInbound{
		Type:             "tun",
		Tag:              "tun-in",
		InterfaceName:    "sing-box-tun",
		Address:          []string{"172.16.250.1/30"},
		MTU:              1200,
		Stack:            "mixed",
		AutoRoute:        true,
		IncludeInterface: []string{"awg0"},
		StrictRoute:      false,
		AutoRedirect:     p.AutoRedirect,
	}
	tunJSON, _ := json.Marshal(tun)

	outbounds := []json.RawMessage{
		mustMarshal(config.DirectOutbound{Type: "direct", Tag: "direct"}),
		mustMarshal(config.BlockOutbound{Type: "block", Tag: "block"}),
	}
	exitTags := make([]string, 0, len(p.Exits))
	for _, e := range p.Exits {
		exitTags = append(exitTags, e.Tag)
		outbounds = append(outbounds, mustMarshal(config.DirectOutbound{
			Type:          "direct",
			Tag:           e.Tag,
			BindInterface: e.InterfaceName,
		}))
	}

	var balancerJSON json.RawMessage
	switch p.Strategy {
	case "urltest":
		balancerJSON = mustMarshal(config.StrategyOutbound{
			Type: "urltest", Tag: "balancer", Outbounds: exitTags,
			URL: "https://www.gstatic.com/generate_204", Interval: "3m",
		})
	default: // fallback (patched round-robin)
		balancerJSON = mustMarshal(config.FallbackOutbound{
			Type: "fallback", Tag: "balancer", Outbounds: exitTags,
			BlacklistTimeout: "30s",
		})
	}
	outbounds = append(outbounds, balancerJSON)

	cfg := config.SingboxConfig{
		Log: &config.LogOptions{Level: "info", Timestamp: true},
		DNS: &config.DNSConfig{
			Servers: []config.DNSServer{
				{Tag: "dns-cloudflare", Type: "udp", Server: "1.1.1.1"},
				{Tag: "dns-google", Type: "udp", Server: "8.8.8.8"},
			},
			Final: "dns-cloudflare",
		},
		Endpoints: []json.RawMessage{},
		Inbounds:  []json.RawMessage{tunJSON},
		Outbounds: outbounds,
		Route: &config.RoutingSection{
			Rules: []config.RouteRuleEntry{
				{Action: "sniff"},
				{Protocol: []string{"bittorrent"}, Action: "route", Outbound: "block"},
				{Protocol: []string{"dns"}, Action: "hijack-dns"},
				{Inbound: []string{"tun-in"}, Action: "route", Outbound: "balancer"},
			},
			Final:                 "direct",
			AutoDetectInterface:   true,
			DefaultDomainResolver: "dns-cloudflare",
		},
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
		"mtu":         1280,
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

	cfg := config.SingboxConfig{
		Log:      &config.LogOptions{Level: "info", Timestamp: true},
		Endpoints: []json.RawMessage{epJSON},
		Outbounds: []json.RawMessage{
			mustMarshal(config.DirectOutbound{Type: "direct", Tag: "direct"}),
			mustMarshal(config.BlockOutbound{Type: "block", Tag: "block"}),
		},
		Route: &config.RoutingSection{
			Rules:               []config.RouteRuleEntry{{Action: "sniff"}},
			Final:               "direct",
			AutoDetectInterface: true,
		},
	}
	return json.MarshalIndent(cfg, "", "  ")
}

// ─── shared helpers ─────────────────────────────────────────────────────────

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// marshal of our typed structs should never fail; panic keeps the
		// generator honest if a field becomes un-marshalable.
		panic(fmt.Sprintf("singbox: mustMarshal: %v", err))
	}
	return b
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
func splitEndpoint(ep string) (string, int) {
	host, portStr := ep, ""
	for i := len(ep) - 1; i >= 0; i-- {
		if ep[i] == ':' {
			host, portStr = ep[:i], ep[i+1:]
			break
		}
	}
	port := 0
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}
	return host, port
}