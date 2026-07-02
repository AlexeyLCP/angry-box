package chain

// clientconfig.go — renders a full sing-box CLIENT config (not just a share-link)
// for a chain's user entry, so an operator can run sing-box locally and actually
// connect through the chain. This is the counterpart of buildMergedNodeConfig:
// the server side is built there; the client side is built here.
//
// Currently supports a TUIC user entry (the most common single-hop + multi-hop
// entry protocol). The client dials the entry node's TUIC inbound and routes all
// traffic through it; for multi-hop chains the entry node's own route/strategy
// forwards to the next hop, so the client only needs to reach the entry.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// ClientConfigParams carries what the generator needs: the chain (for entry
// protocol + creds + entry node address) and the local proxy listen address.
//
// The TUIC user entry is single-user: it uses the chain-wide entry creds
// (TUICEntryUserUUID/Password). There is intentionally no per-user cred field
// here — model.User has no TUIC UUID/password, and adding a User field that is
// silently ignored would mislead callers (CodeRabbit H2). Per-user routing is
// a future feature that will require per-user inbounds on the server first.
type ClientConfigParams struct {
	Chain *model.Chain
	// LocalProxyAddr is the SOCKS/HTTP listen address for the client's inbound
	// (e.g. "127.0.0.1:1080"). Defaults to 127.0.0.1:1080 (SOCKS) + mixed.
	LocalProxyAddr string
	// EntryHostOverride, when set, replaces the parsed entry node address in the
	// TUIC outbound (e.g. "127.0.0.1" when the client runs on the entry VPS).
	EntryHostOverride string
}

// RenderClientConfig produces the sing-box client config JSON for the chain's
// user entry/entries. Returns the pretty-printed JSON.
func RenderClientConfig(params ClientConfigParams) (string, error) {
	c := params.Chain
	if c == nil || len(c.Nodes) == 0 {
		return "", fmt.Errorf("client config: chain has no nodes")
	}

	// Resolve the entry node(s). Multi-entry: build one outbound per entry and a
	// strategy wrapper (urltest/selector/failover) over them per Chain.Strategy.
	// Single-entry: a single "tuic-out" outbound with no wrapper (legacy behavior).
	entries := chainEntryNodes(c)
	if len(entries) == 0 {
		return "", fmt.Errorf("client config: chain %q has no entry node", c.Name)
	}

	listen := params.LocalProxyAddr
	if listen == "" {
		listen = "127.0.0.1:1080"
	}
	listenHost, listenPort := splitListen(listen)

	var outbounds []json.RawMessage
	var inbounds []json.RawMessage
	chainOutTag := "tuic-out" // single-entry default; replaced by strategy tag when multi-entry

	switch c.UserProtocol {
	case model.UserProtocolTUIC:
		uuid := c.TUICEntryUserUUID
		password := c.TUICEntryUserPassword
		if uuid == "" {
			uuid = tuicUUID(c)
		}
		if password == "" {
			password = tuicPassword(c)
		}
		serverName := DefaultRealitySNI
		if p := resolveChainPreset(c); p.Reality != nil && len(p.Reality.ServerNames) > 0 {
			serverName = p.Reality.ServerNames[0]
		}

		var entryTags []string
		for _, n := range entries {
			host := extractHost(n.Addr)
			if params.EntryHostOverride != "" {
				host = params.EntryHostOverride
			}
			if host == "" {
				return "", fmt.Errorf("client config: cannot parse entry addr %q", n.Addr)
			}
			// Single-entry chains use the legacy "tuic-out" tag (no suffix, no
			// strategy wrapper) so existing e2e helpers and configs keep working.
			// Multi-entry chains suffix the node ID for unique addressability.
			tag := "tuic-out"
			if len(entries) > 1 {
				tag = fmt.Sprintf("tuic-out-%s", n.ID)
			}
			entryTags = append(entryTags, tag)
			// TUIC uses a self-signed cert on the server; the client must skip
			// verification (the cert is generated per-node and not CA-signed).
			outb := config.TUICOutbound{
				Type:              "tuic",
				Tag:               tag,
				Server:            host,
				ServerPort:        chainEntryPort(c, n.ID),
				UUID:              uuid,
				Password:          password,
				CongestionControl: "bbr",
				UDPRelayMode:      "native",
				ZeroRTTHandshake:  true,
				Heartbeat:         "10s",
				TLS: &config.OutboundTLSOptions{
					Enabled:    true,
					ServerName: serverName,
					ALPN:       []string{"h3"},
					Insecure:   true, // self-signed cert; see comment above
				},
			}
			ob, _ := json.Marshal(outb)
			outbounds = append(outbounds, ob)
		}

		// Multi-entry: wrap the per-entry outbounds in a strategy outbound so the
		// client load-balances (urltest auto-selects the healthiest; selector uses
		// the default; failover tries in order). Route/DNS detour through the
		// strategy tag instead of a single tuic-out.
		if len(entryTags) > 1 {
			strat := BuildStrategyOutbound(string(c.Strategy), entryTags)
			if strat == nil {
				// Unknown strategy or none set: fall back to urltest so multi-entry
				// is always usable. This revives the previously-dead
				// BuildStrategyOutbound and the previously-ignored Chain.Strategy.
				strat = BuildStrategyOutbound(string(model.StrategyURLTest), entryTags)
			}
			strat.Tag = "chain-lb"
			chainOutTag = "chain-lb"
			sb, _ := json.Marshal(strat)
			outbounds = append(outbounds, sb)
		}

	default:
		return "", fmt.Errorf("client config: unsupported user protocol %q (TUIC is implemented)", c.UserProtocol)
	}

	// direct outbound (final).
	direct := config.DirectOutbound{Type: "direct", Tag: "direct-out"}
	db, _ := json.Marshal(direct)
	outbounds = append(outbounds, db)

	// Local SOCKS+HTTP mixed inbound so a browser/curl can use the client.
	inb := map[string]any{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      listenHost,
		"listen_port": listenPort,
		"users":       []any{},
	}
	ib, _ := json.Marshal(inb)
	inbounds = append(inbounds, ib)

	// Route everything through the chain outbound; direct is the final fallback.
	route := &config.RoutingSection{
		Rules: []config.RouteRuleEntry{
			{Outbound: chainOutTag},
		},
		Final:                 chainOutTag,
		AutoDetectInterface:   true,
		DefaultDomainResolver: "dns-direct",
	}

	// DNS: a remote resolver through the chain + a direct for bootstrap.
	// Final is dns-direct (NOT dns-remote): resolving the chain target's name
	// through the chain outbound is a bootstrap loop — the tunnel needs the
	// target IP before it can carry the DNS query that would learn that IP.
	// dns-remote is still available for post-establish lookups via route rules.
	// Mirrors the server-side fix in buildMergedNodeConfig (CTO-review H1).
	dns := &config.DNSConfig{
		Servers: []config.DNSServer{
			{Tag: "dns-remote", Type: "tls", Server: "1.1.1.1", Detour: chainOutTag},
			{Tag: "dns-direct", Type: "udp", Server: "8.8.8.8", Detour: "direct-out"},
		},
		Final: "dns-direct",
	}

	cfg := &config.SingboxConfig{
		Log:       &config.LogOptions{Level: "info"},
		Inbounds:  inbounds,
		Outbounds: outbounds,
		Route:     route,
		DNS:       dns,
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// splitListen splits "host:port" into host and int port, defaulting the host to
// 127.0.0.1 when absent. Uses net.SplitHostPort via extractHost's sibling logic.
func splitListen(addr string) (string, int) {
	host, port := splitHostPortSafe(addr)
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 1080
	}
	return host, port
}

// splitHostPortSafe is a non-error variant of net.SplitHostPort for the client
// listen address (which is always a simple host:port).
func splitHostPortSafe(addr string) (string, int) {
	idx := strings.LastIndexByte(addr, ':')
	if idx < 0 {
		return addr, 0
	}
	host := addr[:idx]
	port := 0
	for _, r := range addr[idx+1:] {
		if r < '0' || r > '9' {
			return host, 0
		}
		port = port*10 + int(r-'0')
	}
	return host, port
}