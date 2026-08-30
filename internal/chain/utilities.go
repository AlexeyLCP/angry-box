package chain

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

var tlsDomainRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

func ValidTLSDomain(d string) bool {
	d = strings.ToLower(strings.TrimSpace(d))
	return d != "" && len(d) <= 253 && tlsDomainRe.MatchString(d)
}

// ─── Node utilities ("spinal cord") ──────────────────────────────────────────
// The node-side utility bundle: caddy (layer4 SNI router owning 80/443) +
// acme.sh (HTTP-01 certs) + fakesite (camouflage) + pushed subscription
// statics. The orchestrator is the ONLY writer — every artifact is rendered
// here and pushed over SSH; a node keeps no local config state.

// Node-side paths + internal ports. Caddy owns the PUBLIC 80/443; everything
// else listens on loopback behind the layer4 routes.
const (
	UtilityRoot  = "/opt/angry-box"
	CaddyDir     = UtilityRoot + "/caddy"
	CaddyBin     = CaddyDir + "/caddy"
	Caddyfile    = CaddyDir + "/Caddyfile"
	CaddyUnit    = "/etc/systemd/system/ab-caddy.service"
	CaddyService = "ab-caddy"
	SubDir       = UtilityRoot + "/sub"  // per-user subscription statics (Phase 2)
	SiteDir      = UtilityRoot + "/site" // fakesite webroot + acme webroot
	CertRoot     = "/etc/angry-box-certs"
	AcmeBin      = "/root/.acme.sh/acme.sh" // installed under the PRIVILEGED user

	CaddyInternalHTTPSPort = 8443 // layer4 -> caddy own-domain TLS listener
	CaddyInternalHTTPPort  = 8080 // layer4 -> acme webroot + redirect
	PanelRelayPort         = 8900 // ssh -R tunnel exit for the panel relay (Phase 4)
)

// caddyOwnedPorts are the ports the caddy listeners own on a caddy-mode node.
// Inbounds configured on these ports are remapped to internal ports
// (RemapInboundPorts) so caddy can bind them.
var caddyOwnedPorts = map[int]bool{
	80: true, 443: true,
	CaddyInternalHTTPPort: true, CaddyInternalHTTPSPort: true, PanelRelayPort: true,
}

// TLSUtilityProtocols are inbound protocols that terminate TLS THEMSELVES and
// must serve the acme-issued cert on a per-inbound SNI subdomain behind the
// caddy layer4 router (Reality is NOT here — it steals the fingerprint and
// gets the raw default passthrough instead).
var TLSUtilityProtocols = map[string]bool{
	"naive":       true,
	"trusttunnel": true,
}

// RemapInboundPorts computes the effective listen ports for a node's inbounds
// in caddy mode. Inbounds on caddy-owned ports move to internal ports
// (11000+, assigned deterministically by slice order); all others keep their
// port. BOTH the Caddyfile renderer and the sing-box config renderer MUST use
// this function so the two layers agree on where each inbound listens.
// UDP-only inbounds (kernel/userspace AWG, mieru-over-UDP) are exempt: caddy
// owns the TCP ports only, so a UDP listener on 443 never collides.
func RemapInboundPorts(inbounds []model.NodeInbound) map[int]int {
	native := map[int]int{}
	for _, ib := range inbounds {
		native[ib.Port]++
	}
	res := make(map[int]int, len(inbounds))
	next := 11000
	taken := map[int]bool{}
	for i, ib := range inbounds {
		if udpOnlyInbound(&ib) || !caddyOwnedPorts[ib.Port] {
			res[i] = ib.Port
			continue
		}
		for caddyOwnedPorts[next] || native[next] > 0 || taken[next] {
			next++
		}
		res[i] = next
		taken[next] = true
	}
	return res
}

// udpOnlyInbound reports whether the inbound listens on UDP only (no TCP
// socket that could collide with the caddy TCP listeners).
func udpOnlyInbound(ib *model.NodeInbound) bool {
	if ib.Protocol == "awg" {
		return true
	}
	return ib.Protocol == "mieru" && strings.EqualFold(ib.MieruTransport, "UDP")
}

// CaddyMode reports whether the node runs behind the caddy utility (TLS domain
// set AND caddy installed): caddy owns 80/443, standalone inbounds move to
// loopback behind SNI routes, TLS-terminating protocols serve the acme cert.
func CaddyMode(info *model.NodeInfo) bool {
	return info != nil && strings.TrimSpace(info.TLSDomain) != "" && info.UtilityInstalled(model.UtilityCaddy)
}

// CaddyEvictPort returns a deterministic internal port for a listener that must
// vacate a caddy-owned port but CANNOT be SNI-fronted (mtproxy FakeTLS). The
// 12000+ range is reserved for these evictions so they never collide with
// RemapInboundPorts' 11000+ assignments. Non-owned ports pass through.
func CaddyEvictPort(port int) int {
	if !caddyOwnedPorts[port] {
		return port
	}
	return 12000 + (port % 1000)
}

// sniSubdomain returns the stable SNI label for a TLS-utility inbound: the
// protocol name for the first inbound of its kind, <proto>-N after that. The
// full SNI is <label>.<domain>; every label lands in the node's SAN cert.
func sniSubdomain(seen map[string]int, proto string) string {
	n := seen[proto]
	seen[proto]++
	if n == 0 {
		return proto
	}
	return fmt.Sprintf("%s-%d", proto, n)
}

// SNIRoute is one layer4 TLS-SNI passthrough entry in the rendered Caddyfile.
type SNIRoute struct {
	Host string // full SNI, e.g. "naive.node1.example.com"
	Port int    // loopback port of the target sing-box inbound
}

// CaddyPlan is everything RenderCaddyfile needs. Build it with BuildCaddyPlan
// so the SNI/port derivation stays in one place.
type CaddyPlan struct {
	Domain      string // NodeInfo.TLSDomain (required)
	RelayPort   int    // panel relay loopback port (0 = no relay route yet)
	RealityPort int    // default-passthrough target (0 = none → drop unmatched)
	SNIRoutes   []SNIRoute
	Revision    int64 // stamped into the header ("last config wins" audit)
}

// CaddySANs returns the domain list the node's acme SAN certificate must
// cover: the primary domain, the panel subdomain and every SNI route host.
func (p CaddyPlan) CaddySANs() []string {
	sans := []string{p.Domain, "panel." + p.Domain}
	for _, r := range p.SNIRoutes {
		sans = append(sans, r.Host)
	}
	return sans
}

// BuildCaddyPlan derives the caddy routing plan from a node's state. Only
// standalone inbounds participate (chain-sourced ones are skipped exactly like
// the standalone render cycles — AGENTS §18 invariant).
func BuildCaddyPlan(info *model.NodeInfo, revision int64) (CaddyPlan, error) {
	if info == nil || strings.TrimSpace(info.TLSDomain) == "" {
		return CaddyPlan{}, fmt.Errorf("node has no TLS domain set")
	}
	plan := CaddyPlan{
		Domain:    strings.TrimSpace(info.TLSDomain),
		RelayPort: PanelRelayPort,
		Revision:  revision,
	}
	ports := RemapInboundPorts(info.Inbounds)
	seen := map[string]int{}
	for i := range info.Inbounds {
		ib := &info.Inbounds[i]
		if IsChainSourcedInbound(ib) {
			continue
		}
		port := ports[i]
		switch {
		case ib.Protocol == "vless-reality":
			if plan.RealityPort == 0 {
				plan.RealityPort = port
			}
		case TLSUtilityProtocols[ib.Protocol]:
			label := sniSubdomain(seen, ib.Protocol)
			plan.SNIRoutes = append(plan.SNIRoutes, SNIRoute{
				Host: label + "." + plan.Domain,
				Port: port,
			})
		}
	}
	return plan, nil
}

// CaddyFrontedHosts maps standalone inbound INDEX -> public SNI host
// (<slug>.<domain>) for the TLS-utility protocols on a caddy-mode node, in
// exactly the slug order BuildCaddyPlan uses. nil = not caddy mode. Client
// link builders use it to point naive/trusttunnel users at the SNI subdomain
// instead of the raw node IP.
func CaddyFrontedHosts(info *model.NodeInfo) map[int]string {
	if !CaddyMode(info) {
		return nil
	}
	res := map[int]string{}
	seen := map[string]int{}
	for i := range info.Inbounds {
		ib := &info.Inbounds[i]
		if IsChainSourcedInbound(ib) || !TLSUtilityProtocols[ib.Protocol] {
			continue
		}
		label := sniSubdomain(seen, ib.Protocol)
		res[i] = label + "." + strings.TrimSpace(info.TLSDomain)
	}
	return res
}

// CertPaths returns the fullchain/key file paths for a domain under CertRoot
// (acme.sh --install-cert targets; sing-box TLS inbounds reference the same
// files so one cert serves both layers).
func CertPaths(domain string) (cert, key string) {
	if !ValidTLSDomain(domain) {
		return "", ""
	}
	return CertRoot + "/" + domain + "/fullchain.pem", CertRoot + "/" + domain + "/key.pem"
}

// RenderCaddyfile renders the node's Caddyfile from the plan. The shape:
//
//   - layer4 :443 — raw-TCP SNI routing: own domains -> the internal caddy
//     TLS listener, protocol subdomains -> the matching sing-box inbound,
//     DEFAULT -> the Reality inbound (no cert, stolen fingerprint).
//   - layer4 :80 -> internal HTTP listener (acme webroot + https redirect).
//   - internal HTTPS listener — fakesite + /sub statics (UA/query negotiation)
//     + panel.<domain> relay reverse-proxy.
//   - internal HTTP listener — acme-challenge file_server, redirect otherwise.
func RenderCaddyfile(p CaddyPlan) (string, error) {
	if !ValidTLSDomain(p.Domain) {
		return "", fmt.Errorf("caddyfile: invalid domain")
	}
	cert, key := CertPaths(p.Domain)
	var b strings.Builder

	fmt.Fprintf(&b, "# Rendered by angry-box — do not edit on the node (store rev %d)\n", p.Revision)
	b.WriteString(`{
	admin off
	auto_https off
	layer4 {
		:443 {
`)
	fmt.Fprintf(&b, "\t\t\t@own tls_sni %s panel.%s\n", p.Domain, p.Domain)
	b.WriteString("\t\t\troute @own {\n")
	fmt.Fprintf(&b, "\t\t\t\treverse_proxy 127.0.0.1:%d\n", CaddyInternalHTTPSPort)
	b.WriteString("\t\t\t}\n")
	for i, r := range p.SNIRoutes {
		fmt.Fprintf(&b, "\t\t\t@sni%d tls_sni %s\n", i, r.Host)
		fmt.Fprintf(&b, "\t\t\troute @sni%d {\n", i)
		fmt.Fprintf(&b, "\t\t\t\treverse_proxy 127.0.0.1:%d\n", r.Port)
		b.WriteString("\t\t\t}\n")
	}
	if p.RealityPort > 0 {
		b.WriteString("\t\t\troute {\n")
		fmt.Fprintf(&b, "\t\t\t\treverse_proxy 127.0.0.1:%d\n", p.RealityPort)
		b.WriteString("\t\t\t}\n")
	}
	b.WriteString(`		}
		:80 {
			route {
`)
	fmt.Fprintf(&b, "\t\t\t\treverse_proxy 127.0.0.1:%d\n", CaddyInternalHTTPPort)
	b.WriteString(`			}
		}
	}
}

`)

	// Internal HTTPS listener: own domains (SAN cert), panel relay, /sub, site.
	fmt.Fprintf(&b, "https://127.0.0.1:%d {\n", CaddyInternalHTTPSPort)
	fmt.Fprintf(&b, "\ttls %s %s\n", cert, key)
	fmt.Fprintf(&b, "\t@panel host panel.%s\n", p.Domain)
	b.WriteString("\thandle @panel {\n")
	if p.RelayPort > 0 {
		fmt.Fprintf(&b, "\t\treverse_proxy 127.0.0.1:%d\n", p.RelayPort)
	} else {
		b.WriteString("\t\tabort\n")
	}
	b.WriteString("\t}\n")
	b.WriteString(`	handle_path /sub/* {
		root * ` + SubDir + `
		@clash query format=clash
		@vpn query format=vpn
		@raw query format=raw
		@b64 query format=base64
		@browser header User-Agent *Mozilla*
		rewrite @clash {path}.clash.yaml
		rewrite @vpn {path}.vpn
		rewrite @raw {path}.raw
		rewrite @b64 {path}.b64
		rewrite @browser {path}.html
		file_server {
			try_files {path} {path}.b64
		}
	}
	root * ` + SiteDir + `
	file_server
}

`)

	// Internal HTTP listener: acme webroot first, then permanent https redirect.
	fmt.Fprintf(&b, "http://127.0.0.1:%d {\n", CaddyInternalHTTPPort)
	b.WriteString(`	handle /.well-known/acme-challenge/* {
		root * ` + SiteDir + `
		file_server
	}
`)
	fmt.Fprintf(&b, "\tredir https://%s{uri} permanent\n", p.Domain)
	b.WriteString("}\n")
	return b.String(), nil
}

// DefaultFakesite is the camouflage page served on the node's primary domain
// when the fakesite utility is installed without operator-provided content.
// Deliberately boring: a plausible personal page, no proxy fingerprints.
const DefaultFakesite = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Notes</title>
<style>
body{font-family:Georgia,serif;max-width:640px;margin:3rem auto;padding:0 1rem;color:#222;line-height:1.6}
h1{font-size:1.6rem}footer{margin-top:3rem;color:#999;font-size:.85rem}
</style>
</head>
<body>
<h1>Notes &amp; links</h1>
<p>Welcome to my small corner of the web. I collect recipes, hiking routes
and occasional book notes here.</p>
<p>Nothing interesting yet — check back later.</p>
<footer>&copy; 2026</footer>
</body>
</html>
`
