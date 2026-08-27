package web

// sub_singbox.go — the ?format=singbox subscription: a complete sing-box
// client config (SFA/SFI/SFW) rendered from the same share links the other
// formats use. AWG .conf -> type:"awg" endpoint in client mode (all AmneziaWG
// obfuscation fields carried over); vless:// -> vless+reality outbound. More
// than one proxy is wrapped in a urltest group so the client auto-selects the
// healthiest — the same strategy model the server chains use.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// buildUserSingboxJSON renders the user's sing-box client config. Returns an
// error when no link is expressible in sing-box form (the endpoint then 404s,
// same as an empty subscription).
func buildUserSingboxJSON(u *model.User, links []string) (string, error) {
	name := yamlSafe(u.Name)
	var outbounds []json.RawMessage
	var tags []string
	for i, l := range links {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		tag := fmt.Sprintf("%s-%d", name, i+1)
		var ob any
		switch {
		case vpnuri.IsAWGConf(l):
			ob = singboxAWGOutbound(tag, l)
		case strings.HasPrefix(l, "vless://"):
			ob = singboxVLESSOutbound(tag, l)
		default:
			// naive/mieru/trusttunnel/mtproxy share links have no sing-box
			// builder yet — skipped in this format (still served by the other
			// formats).
			continue
		}
		if ob == nil {
			continue
		}
		data, err := json.Marshal(ob)
		if err != nil {
			continue
		}
		outbounds = append(outbounds, data)
		tags = append(tags, tag)
	}
	if len(outbounds) == 0 {
		return "", fmt.Errorf("no sing-box-capable links")
	}

	// Multiple proxies -> urltest wrapper (auto-select), mirroring the fleet's
	// default group strategy.
	proxyTag := tags[0]
	if len(tags) > 1 {
		strat := chain.BuildStrategyOutbound(string(model.StrategyURLTest), tags)
		if strat == nil {
			strat = chain.BuildStrategyOutbound("urltest", tags)
		}
		proxyTag = strat.Tag
		sb, _ := json.Marshal(strat)
		outbounds = append(outbounds, sb)
	}

	direct, _ := json.Marshal(config.DirectOutbound{Type: "direct", Tag: "direct-out"})
	outbounds = append(outbounds, direct)

	mixed, _ := json.Marshal(map[string]any{
		"type":        "mixed",
		"tag":         "mixed-in",
		"listen":      "127.0.0.1",
		"listen_port": 2080,
	})

	cfg := &config.SingboxConfig{
		Log:       &config.LogOptions{Level: "warn"},
		Inbounds:  []json.RawMessage{mixed},
		Outbounds: outbounds,
		Route: &config.RoutingSection{
			Rules:               []config.RouteRuleEntry{{Outbound: proxyTag}},
			Final:               proxyTag,
			AutoDetectInterface: true,
		},
		DNS: &config.DNSConfig{
			Servers: []config.DNSServer{
				{Tag: "dns-remote", Type: "tls", Server: "1.1.1.1", Detour: proxyTag},
				{Tag: "dns-direct", Type: "udp", Server: "8.8.8.8", Detour: "direct-out"},
			},
			Final: "dns-direct", // bootstrap-safe: see RenderClientConfig's DNS comment
		},
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// singboxAWGOutbound converts an awg-quick .conf (the share-link form) into a
// sing-box type:"awg" endpoint in CLIENT mode (no listen_port; the server is a
// single peer with allowed_ips 0.0.0.0/0). Every AmneziaWG obfuscation field
// present in the conf is carried over flat, exactly like the server endpoints.
func singboxAWGOutbound(tag, conf string) *config.AwgEndpointOptions {
	f := vpnuri.ParseConfFields(conf)
	priv := f["PrivateKey"]
	pub := f["PublicKey"]
	if priv == "" || pub == "" {
		return nil
	}
	host, port := "", 51820
	if ep := f["Endpoint"]; ep != "" {
		if i := strings.LastIndex(ep, ":"); i > 0 {
			host = ep[:i]
			if p, err := strconv.Atoi(ep[i+1:]); err == nil {
				port = p
			}
		}
	}
	if host == "" {
		return nil
	}
	addr := f["Address"]
	if addr == "" {
		addr = "10.8.0.2/32"
	}
	ka := 25
	if v := f["PersistentKeepalive"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ka = n
		}
	}
	mtu := 1420
	if v := f["MTU"]; v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			mtu = n
		}
	}

	ep := &config.AwgEndpointOptions{
		Type:       "awg",
		Tag:        tag,
		PrivateKey: priv,
		Address:    []string{addr},
		MTU:        mtu,
		Peers: []config.AwgPeerOptions{{
			PublicKey:                   pub,
			PresharedKey:                f["PresharedKey"],
			AllowedIPs:                  []string{"0.0.0.0/0", "::/0"},
			Address:                     host,
			Port:                        port,
			PersistentKeepaliveInterval: ka,
		}},
		Jc:   atoiOr(f["Jc"], 0),
		Jmin: atoiOr(f["Jmin"], 0),
		Jmax: atoiOr(f["Jmax"], 0),
		S1:   atoiOr(f["S1"], 0),
		S2:   atoiOr(f["S2"], 0),
		S3:   atoiOr(f["S3"], 0),
		S4:   atoiOr(f["S4"], 0),
		H1:   f["H1"],
		H2:   f["H2"],
		H3:   f["H3"],
		H4:   f["H4"],
		I1:   f["I1"],
		I2:   f["I2"],
		I3:   f["I3"],
		I4:   f["I4"],
		I5:   f["I5"],
	}
	if hpk := f["HeaderProtectionKey"]; hpk != "" {
		ep.HeaderProtectionKey = hpk
	}
	return ep
}

// singboxVLESSOutbound converts a vless:// share link into a vless outbound
// (reality TLS when the link carries security=reality).
func singboxVLESSOutbound(tag, uri string) *config.VLESSOutbound {
	rest := strings.TrimPrefix(uri, "vless://")
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	userhost, query, _ := strings.Cut(rest, "?")
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return nil
	}
	uuid := userhost[:at]
	hp := userhost[at+1:]
	host, portStr, ok := strings.Cut(hp, ":")
	if !ok {
		return nil
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil
	}
	q := map[string]string{}
	for _, part := range strings.Split(query, "&") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			q[k] = v
		}
	}
	ob := &config.VLESSOutbound{
		Type:       "vless",
		Tag:        tag,
		Server:     host,
		ServerPort: port,
		UUID:       uuid,
		Flow:       q["flow"],
	}
	if q["security"] == "reality" {
		tls := &config.OutboundTLSOptions{
			Enabled:    true,
			ServerName: q["sni"],
		}
		if q["fp"] != "" {
			tls.UTLS = &config.UTLSOptions{Enabled: true, Fingerprint: q["fp"]}
		}
		tls.Reality = &config.OutboundRealityOptions{
			Enabled:   true,
			PublicKey: q["pbk"],
			ShortID:   q["sid"],
		}
		ob.TLS = tls
	}
	return ob
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
