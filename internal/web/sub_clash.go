package web

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
)

// buildClashYAML renders a Clash Meta (mihomo) subscription covering AWG
// (wireguard + amnezia-wg-option) and vless:// share links. Naive/mieru/
// TrustTunnel are omitted — Clash formats do not carry those protocols.
func buildClashYAML(userName string, links []string) string {
	var names []string
	var proxies []string
	for i, l := range links {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		name := fmt.Sprintf("%s-%d", yamlSafe(userName), i+1)
		var block string
		switch {
		case vpnuri.IsAWGConf(l):
			block = clashAWGProxy(name, l)
		case strings.HasPrefix(l, "vless://"):
			block = clashVLESSProxy(name, l)
		default:
			continue
		}
		if block == "" {
			continue
		}
		names = append(names, name)
		proxies = append(proxies, block)
	}
	if len(proxies) == 0 {
		return "proxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n"
	}
	var b strings.Builder
	b.WriteString("proxies:\n")
	for _, p := range proxies {
		b.WriteString(p)
	}
	b.WriteString("proxy-groups:\n")
	b.WriteString("  - name: PROXY\n    type: select\n    proxies:\n")
	for _, n := range names {
		b.WriteString("      - ")
		b.WriteString(n)
		b.WriteString("\n")
	}
	b.WriteString("rules:\n  - MATCH,PROXY\n")
	return b.String()
}

func clashAWGProxy(name, conf string) string {
	f := vpnuri.ParseConfFields(conf)
	priv := f["PrivateKey"]
	pub := f["PublicKey"]
	if priv == "" || pub == "" {
		return ""
	}
	host, port := "0.0.0.0", 51820
	if ep := f["Endpoint"]; ep != "" {
		if i := strings.LastIndex(ep, ":"); i > 0 {
			host = ep[:i]
			if p, err := strconv.Atoi(ep[i+1:]); err == nil {
				port = p
			}
		}
	}
	ip := f["Address"]
	if i := strings.Index(ip, "/"); i > 0 {
		ip = ip[:i]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  - name: %s\n    type: wireguard\n    server: %s\n    port: %d\n    udp: true\n    private-key: %s\n    public-key: %s\n",
		yamlQuote(name), yamlQuote(host), port, yamlQuote(priv), yamlQuote(pub))
	if ip != "" {
		fmt.Fprintf(&b, "    ip: %s\n", yamlQuote(ip))
	}
	if psk := f["PresharedKey"]; psk != "" {
		fmt.Fprintf(&b, "    pre-shared-key: %s\n", yamlQuote(psk))
	}
	if mtu := f["MTU"]; mtu != "" {
		fmt.Fprintf(&b, "    mtu: %s\n", mtu)
	}
	if ka := f["PersistentKeepalive"]; ka != "" {
		fmt.Fprintf(&b, "    persistent-keepalive: %s\n", ka)
	}
	opt := clashAmneziaOpt(f)
	if opt != "" {
		b.WriteString("    amnezia-wg-option:\n")
		b.WriteString(opt)
	}
	return b.String()
}

func clashAmneziaOpt(f map[string]string) string {
	var b strings.Builder
	ver := 2
	if f["HeaderProtectionKey"] != "" {
		ver = 3
	} else if f["S3"] == "" && f["I1"] == "" {
		ver = 1
	}
	fmt.Fprintf(&b, "      version: %d\n", ver)
	for _, k := range []string{"Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4"} {
		if v := f[k]; v != "" {
			fmt.Fprintf(&b, "      %s: %s\n", strings.ToLower(k), v)
		}
	}
	for _, k := range []string{"H1", "H2", "H3", "H4", "I1", "I2", "I3", "I4", "I5"} {
		if v := f[k]; v != "" {
			fmt.Fprintf(&b, "      %s: %s\n", strings.ToLower(k), yamlQuote(v))
		}
	}
	if hpk := f["HeaderProtectionKey"]; hpk != "" {
		fmt.Fprintf(&b, "      header-protection-key: %s\n", yamlQuote(hpk))
	}
	return b.String()
}

func clashVLESSProxy(name, uri string) string {
	// vless://uuid@host:port?type=...&security=...&pbk=...&sni=...&sid=...
	rest := strings.TrimPrefix(uri, "vless://")
	if i := strings.Index(rest, "#"); i >= 0 {
		rest = rest[:i]
	}
	userhost, query, _ := strings.Cut(rest, "?")
	at := strings.LastIndex(userhost, "@")
	if at < 0 {
		return ""
	}
	uuid := userhost[:at]
	hp := userhost[at+1:]
	host, portStr, ok := strings.Cut(hp, ":")
	if !ok {
		return ""
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return ""
	}
	q := map[string]string{}
	for _, part := range strings.Split(query, "&") {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			q[k] = v
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  - name: %s\n    type: vless\n    server: %s\n    port: %d\n    uuid: %s\n    udp: true\n    network: %s\n",
		yamlQuote(name), yamlQuote(host), port, yamlQuote(uuid), yamlQuote(orDefault(q["type"], "tcp")))
	if q["security"] == "reality" {
		b.WriteString("    tls: true\n    reality-opts:\n")
		if q["pbk"] != "" {
			fmt.Fprintf(&b, "      public-key: %s\n", yamlQuote(q["pbk"]))
		}
		if q["sid"] != "" {
			fmt.Fprintf(&b, "      short-id: %s\n", yamlQuote(q["sid"]))
		}
		if q["sni"] != "" {
			fmt.Fprintf(&b, "    servername: %s\n", yamlQuote(q["sni"]))
		}
		if q["fp"] != "" {
			fmt.Fprintf(&b, "    client-fingerprint: %s\n", yamlQuote(q["fp"]))
		}
		if q["flow"] != "" {
			fmt.Fprintf(&b, "    flow: %s\n", yamlQuote(q["flow"]))
		}
	}
	return b.String()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func yamlSafe(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "user"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func yamlQuote(s string) string {
	if s == "" {
		return `""`
	}
	need := false
	for _, r := range s {
		if r == ':' || r == '#' || r == '{' || r == '}' || r == '[' || r == ']' || r == ',' || r == '&' || r == '*' || r == '!' || r == ' ' || r == '"' || r == '\'' {
			need = true
			break
		}
	}
	if !need {
		return s
	}
	return strconv.Quote(s)
}
