package takeover

// awg_takeover.go — dedicated renderer for AWG takeover (#4). When an existing
// AWG (kernel awg-quick) server is taken over, we convert it to a sing-box
// userspace wireguard endpoint preserving the server keypair + listen port +
// amnezia obfuscation (JC/JMIN/JMAX/S1-S4/H1-H4/I1-I5) + the full peer list.
//
// This does NOT go through generateAWGUser/NodeInbound: that path pulls amnezia
// from a named preset (not the imported server conf) and hardcodes a single
// peer with 10.8.0.2/32 — which would break the existing AWG clients (mismatched
// amnezia → handshake fail; lost peers → clients dropped). Instead we build the
// WireGuardEndpoint directly from ImportAWGConfigs' parsed AwgServerConfig +
// AwgPeerEntry, copying amnezia 1:1 (the field types match AmneziaOptions).
//
// Trade-off (hybrid approach): takeover does NOT create model.User entries for
// the imported peers, so per-client routing (source_ip_cidr) is not available
// on a takeover'd AWG inbound. It preserves VPN functionality (all existing
// clients keep connecting); per-client features are a follow-up.

import (
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// renderAWGTakeoverConfig builds a full sing-box config JSON from an imported
// AWG server config + peer list. The endpoint is userspace (System: false,
// wireguard-go — amnezia works via the patched binary), listens on
// server.ListenPort with server.PrivateKey, carries every peer, and an amnezia
// block copied 1:1 from the imported AwgServerConfig. Returns the pretty JSON.
func renderAWGTakeoverConfig(server *chain.AwgServerConfig, peers []chain.AwgPeerEntry) (string, error) {
	if server == nil {
		return "", fmt.Errorf("awg takeover: nil server config")
	}
	peersJSON := awgTakeoverPeers(peers)
	ep := config.WireGuardEndpoint{
		Type:       "wireguard",
		Tag:        "awg-takeover-in",
		System:     false, // userspace wireguard-go (amnezia works via the patched binary)
		MTU:        1420,
		Address:    []string{awgTakeoverServerAddress(server.Address)},
		PrivateKey: server.PrivateKey,
		ListenPort: server.ListenPort,
		Peers:      peersJSON,
		Amnezia:    awgTakeoverAmnezia(server),
	}
	epJSON, _ := json.Marshal(ep)
	cfg := struct {
		Log      *config.LogOptions        `json:"log"`
		Endpoints []json.RawMessage         `json:"endpoints"`
		Outbounds []json.RawMessage         `json:"outbounds"`
		Experimental *config.ExperimentalOptions `json:"experimental,omitempty"`
	}{
		Log:       &config.LogOptions{Level: "info"},
		Endpoints: []json.RawMessage{epJSON},
		Outbounds: []json.RawMessage{
			mustJSON(map[string]any{"type": "direct", "tag": "direct-out"}),
			mustJSON(map[string]any{"type": "block", "tag": "block"}),
		},
		Experimental: &config.ExperimentalOptions{CacheFile: &config.CacheFileOptions{Enabled: true}},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("awg takeover: marshal config: %w", err)
	}
	return string(out), nil
}

// awgTakeoverPeers converts imported AwgPeerEntry → WireGuardPeer. Each peer's
// AllowedIPs = the peer's AllowedIPs (if set) else its Address; falls back to
// 0.0.0.0/0 (a roaming client peer without a fixed AllowedIPs).
func awgTakeoverPeers(peers []chain.AwgPeerEntry) []config.WireGuardPeer {
	out := make([]config.WireGuardPeer, 0, len(peers))
	for _, p := range peers {
		if p.PublicKey == "" {
			continue
		}
		allowed := p.AllowedIPs
		if allowed == "" {
			allowed = p.Address
		}
		if allowed == "" {
			allowed = "0.0.0.0/0,::/0"
		}
		out = append(out, config.WireGuardPeer{
			PublicKey:  p.PublicKey,
			AllowedIPs: splitCSV(allowed),
		})
	}
	if len(out) == 0 {
		// No peers parsed: keep the endpoint valid with a placeholder (sing-box
		// rejects an empty peers array). Replaced once clients are re-added.
		out = []config.WireGuardPeer{{PublicKey: "CLIENT_PUBLIC_KEY_HERE", AllowedIPs: []string{"10.8.0.2/32"}}}
	}
	return out
}

// awgTakeoverAmnezia copies the imported server's amnezia fields 1:1 into the
// sing-box AmneziaOptions. JC/JMIN/JMAX/S1-S4 are int, H1-H4/I1-I5 are string
// (the "<b 0x...>" CPS form) — types match exactly. Returns nil when the server
// had no JC (no amnezia configured) so the endpoint renders without amnezia.
func awgTakeoverAmnezia(server *chain.AwgServerConfig) *config.AmneziaOptions {
	if server.JC == 0 {
		return nil // plain WireGuard, no amnezia obfuscation
	}
	return &config.AmneziaOptions{
		JC:   server.JC,
		JMIN: server.JMIN,
		JMAX: server.JMAX,
		S1:   server.S1,
		S2:   server.S2,
		S3:   server.S3,
		S4:   server.S4,
		H1:   server.H1,
		H2:   server.H2,
		H3:   server.H3,
		H4:   server.H4,
		I1:   server.I1,
		I2:   server.I2,
		I3:   server.I3,
		I4:   server.I4,
		I5:   server.I5,
	}
}

// awgTakeoverServerAddress normalizes the imported server Address (e.g.
// "10.8.0.1/24" from awg0.conf) for the endpoint; falls back to 10.8.0.1/24.
func awgTakeoverServerAddress(addr string) string {
	if addr == "" {
		return "10.8.0.1/24"
	}
	return addr
}

// splitCSV splits a comma-separated list (AllowedIPs form) into trimmed parts.
func splitCSV(s string) []string {
	var out []string
	cur := ""
	for _, c := range s {
		if c == ',' {
			if t := trim(cur); t != "" {
				out = append(out, t)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	if t := trim(cur); t != "" {
		out = append(out, t)
	}
	if len(out) == 0 {
		return []string{"0.0.0.0/0"}
	}
	return out
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func mustJSON(m map[string]any) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}