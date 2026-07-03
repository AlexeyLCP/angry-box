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
// protocol + entry node address) and the local proxy listen address.
//
// User, when set, supplies per-user credentials: the TUIC outbound authenticates
// with the user's TUICUUID/TUICPassword instead of the chain-wide shared creds,
// so the server's multi-user inbound (B3) + auth_user route rules (B4) can
// identify and steer this client. When User is nil or has no per-user creds,
// the chain-wide creds are used (legacy behavior).
type ClientConfigParams struct {
	Chain *model.Chain
	// User is optional; when set and populated with per-user creds, the client
	// authenticates as that user (per-client routing). Nil -> chain-wide creds.
	User *model.User
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

	// Per-client entry selection: when the user pins a ChainExit to a node that
	// IS an entry of this chain, connect only to that entry (the user egresses
	// there). This is how per-client routing is realized for multi-hop chains —
	// the inter-node hops do not propagate the end-user identity, so the pinned
	// node must be the user's direct entry. When the pin is not an entry (or
	// absent), fall back to all entries (load-balanced / first).
	if params.User != nil {
		if pinned, ok := params.User.ChainExit[c.Name]; ok && pinned != "" {
			for _, n := range entries {
				if n.ID == pinned {
					entries = []*model.ChainNode{n}
					break
				}
			}
		}
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
		// Per-user creds take precedence; fall back to chain-wide shared creds
		// when the user is nil or has no per-user TUIC identity (legacy).
		uuid := c.TUICEntryUserUUID
		password := c.TUICEntryUserPassword
		if params.User != nil {
			if params.User.TUICUUID != "" {
				uuid = params.User.TUICUUID
			}
			if params.User.TUICPassword != "" {
				password = params.User.TUICPassword
			}
		}
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

// RenderClientAWGConf produces an awg-quick client .conf (NOT a sing-box JSON)
// for a chain whose user-entry protocol is AWG. Each user connects as their own
// WireGuard peer: the .conf carries the user's AWGPrivateKey and AWGAddress
// (the per-user inner IP that the server's per-client source_ip_cidr rules
// match on). When params.User is nil or lacks per-user AWG creds, the chain-wide
// shared client key/IP is used as a fallback (legacy single-client behavior).
//
// Entry selection mirrors RenderClientConfig: a user with a ChainExit pin to a
// direct entry of this chain connects only to that entry; otherwise the first
// entry is used (single entry) — AWG has no client-side load balancing, the
// user dials one endpoint. EntryHostOverride replaces the parsed entry address
// (e.g. "127.0.0.1" when the client runs on the entry VPS itself).
//
// Returns an error when the chain is not AWG or has no entry node.
func RenderClientAWGConf(params ClientConfigParams) (string, error) {
	c := params.Chain
	if c == nil {
		return "", fmt.Errorf("awg client conf: nil chain")
	}
	if c.UserProtocol != model.UserProtocolAWG {
		return "", fmt.Errorf("awg client conf: chain %q user protocol is %q, not awg", c.Name, c.UserProtocol)
	}
	entries := chainEntryNodes(c)
	if len(entries) == 0 {
		return "", fmt.Errorf("awg client conf: chain %q has no entry node", c.Name)
	}
	// Per-client entry selection: pin to a direct entry when ChainExit matches
	// one of this chain's entries (the user egresses there).
	entry := entries[0]
	if params.User != nil {
		if pinned, ok := params.User.ChainExit[c.Name]; ok && pinned != "" {
			for _, n := range entries {
				if n.ID == pinned {
					entry = n
					break
				}
			}
		}
	}

	// Per-user AWG creds take precedence over the chain-wide shared creds so
	// each user's .conf authenticates as their own peer (per-client routing).
	// Fallback to chain-wide when the user has no per-user identity (legacy).
	clientPriv := ""
	address := ""
	if params.User != nil {
		clientPriv = params.User.AWGPrivateKey
		address = params.User.AWGAddress
	}
	serverPub := c.AWGEntryServerPub
	port := chainEntryPort(c, entry.ID)

	host := params.EntryHostOverride
	if host == "" {
		host = strings.Split(entry.Addr, ":")[0]
	}
	// Use the chain's effective preset + persisted CPS material so the client
	// .conf carries the SAME amnezia block (Jc/S1/S2/H1-H4/I1-I5) as the server
	// endpoint — a mismatch breaks the AWG handshake. Previously this hardcoded
	// GetDefaultPreset(), which diverged whenever the chain used a non-default
	// ObfuscationProfile.
	preset := resolveChainPreset(c)
	return renderAWGQuickConf(host, port, clientPriv, serverPub, address, &preset, ChainAWGObfsMaterial(c)), nil
}

// renderAWGQuickConf builds the awg-quick .conf text. preset + material supply
// the Amnezia obfuscation params — they MUST match the server endpoint's
// amnezia block or the handshake fails (chain callers pass the chain's preset
// + persisted AWGObfsMaterial). clientPriv/address empty -> legacy fallback
// (placeholder key + 10.8.0.2/24); the .conf is still structurally valid.
func renderAWGQuickConf(host string, port int, clientPriv, serverPub, address string, preset *ConnectionPreset, material *AWGObfsMaterial) string {
	if address == "" {
		address = "10.8.0.2/24"
	}
	if clientPriv == "" {
		clientPriv = "CLIENT_PRIVATE_KEY_HERE"
	}
	if serverPub == "" {
		serverPub = "SERVER_PUBLIC_KEY_HERE"
	}
	var b strings.Builder
	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", address))
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", clientPriv))
	b.WriteString("MTU = 1420\n")
	// Amnezia obfuscation params belong in [Interface] (BEFORE [Peer]). awg-quick
	// passes the stripped config to `awg setconf`, which parses amnezia fields
	// only within [Interface]; emitting them after [Peer] makes setconf fail
	// with "Line unrecognized: Jc=...". The values must match the server
	// endpoint's amnezia block (chain preset + persisted CPS material).
	if preset != nil && preset.AWG != nil {
		amn := BuildAWGAmnezia(preset.AWG, preset, material)
		if amn != nil {
			b.WriteString(fmt.Sprintf("Jc = %d\n", amn.JC))
			b.WriteString(fmt.Sprintf("Jmin = %d\n", amn.JMIN))
			b.WriteString(fmt.Sprintf("Jmax = %d\n", amn.JMAX))
			b.WriteString(fmt.Sprintf("S1 = %d\n", amn.S1))
			b.WriteString(fmt.Sprintf("S2 = %d\n", amn.S2))
			b.WriteString(fmt.Sprintf("S3 = %d\n", amn.S3))
			b.WriteString(fmt.Sprintf("S4 = %d\n", amn.S4))
			b.WriteString(fmt.Sprintf("H1 = %s\n", amn.H1))
			b.WriteString(fmt.Sprintf("H2 = %s\n", amn.H2))
			b.WriteString(fmt.Sprintf("H3 = %s\n", amn.H3))
			b.WriteString(fmt.Sprintf("H4 = %s\n", amn.H4))
			if amn.I1 != "" {
				b.WriteString(fmt.Sprintf("I1 = %s\n", amn.I1))
				b.WriteString(fmt.Sprintf("I2 = %s\n", amn.I2))
				b.WriteString(fmt.Sprintf("I3 = %s\n", amn.I3))
				b.WriteString(fmt.Sprintf("I4 = %s\n", amn.I4))
				b.WriteString(fmt.Sprintf("I5 = %s\n", amn.I5))
			}
		}
	}
	b.WriteString("\n[Peer]\n")
	b.WriteString(fmt.Sprintf("PublicKey = %s\n", serverPub))
	b.WriteString("AllowedIPs = 0.0.0.0/0, ::/0\n")
	b.WriteString(fmt.Sprintf("Endpoint = %s:%d\n", host, port))
	b.WriteString("PersistentKeepalive = 25\n")
	return b.String()
}