package takeover

// awg_takeover.go — dedicated renderer for AWG takeover (#4 / #8). When an
// existing AWG (kernel awg-quick) server is taken over, we KEEP the kernel
// awg-quick@awg0 service running (it owns the AWG interface, peers, amnezia
// obfuscation — userspace wireguard-go would panic with chacha20poly1305 under
// AmneziaWG) and push a sing-box TUN-overlay config that captures awg0 traffic
// via include_interface:["awg0"] and routes it out direct.
//
// The imported awg0.conf is left untouched on disk (the kernel keeps serving
// existing clients with their original amnezia + peers). sing-box sits on top:
//   endpoints: []
//   inbounds:  [TUN{include_interface:["awg0"], stack:"mixed", auto_route}]
//   outbounds: [direct, block]
//   route:     [sniff, dns-hijack, tun-in→direct]
//
// This mirrors the dns.idoctor.mom kernel-AWG architecture
// (VPN/orchestrator/app/templates/awg_balancer.json.j2) — a single-egress node
// with no exit tunnels. The previous userspace-endpoint takeover is gone (it
// required disabling awg-quick@awg0 to free the port, and crashed under
// amnezia). Per-client source_ip_cidr routing is not wired here (the imported
// peers have no model.User records); that's a follow-up.

import (
	"encoding/json"
	"fmt"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// renderAWGTakeoverConfig builds a sing-box TUN-overlay config for a taken-over
// kernel AWG server. The kernel awg-quick@awg0 keeps running (peers + amnezia
// come from the imported awg0.conf, untouched); sing-box captures awg0 traffic
// via a TUN inbound and routes it direct. No userspace wireguard endpoint is
// emitted (that path panics under AmneziaWG). The server/peers params are
// accepted for API compatibility but are NOT used by the TUN-overlay config —
// they live in the awg0.conf the kernel already owns.
func renderAWGTakeoverConfig(server *chain.AwgServerConfig, peers []chain.AwgPeerEntry) (string, error) {
	if server == nil {
		return "", fmt.Errorf("awg takeover: nil server config")
	}
	// Single-egress TUN overlay: awg0 only, no exit tunnels, no balancer.
	// BuildAWGTUNOverlay emits the TUN inbound + direct/block outbounds + route
	// rules (sniff, dns-hijack, tun-in→direct). Endpoints stays empty.
	inbounds, outbounds, route := chain.BuildAWGTUNOverlay(chain.AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
		FinalOutbound:     "direct",
	})

	cfg := struct {
		Log          *config.LogOptions          `json:"log"`
		Endpoints    []json.RawMessage           `json:"endpoints"`
		Inbounds     []json.RawMessage           `json:"inbounds"`
		Outbounds    []json.RawMessage           `json:"outbounds"`
		Route        *config.RoutingSection      `json:"route"`
		Experimental *config.ExperimentalOptions `json:"experimental,omitempty"`
	}{
		Log:       &config.LogOptions{Level: "info"},
		Endpoints: []json.RawMessage{}, // empty — kernel owns awg0
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route: &config.RoutingSection{
			Rules:               route,
			Final:               "direct",
			AutoDetectInterface: true,
		},
		Experimental: &config.ExperimentalOptions{CacheFile: &config.CacheFileOptions{Enabled: true}},
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("awg takeover: marshal config: %w", err)
	}
	return string(out), nil
}
