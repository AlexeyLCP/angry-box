package chain

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

type MergeReport struct {
	NodeID           string      `json:"node_id"`
	StandaloneCount  int         `json:"standalone_count"`
	ChainsIncluded   []string    `json:"chains_included"`
	Ports            []PortUsage `json:"ports"`
	Warnings         []string    `json:"warnings,omitempty"`
	AddedInbounds    []string    `json:"added_inbounds,omitempty"`
	RemovedInbounds  []string    `json:"removed_inbounds,omitempty"`
}

type PortUsage struct {
	Port     int    `json:"port"`
	Claimant string `json:"claimant"`
	Role     string `json:"role"`
}

type chainRole struct {
	Chain       *model.Chain
	NodeIndex   int
	Node        *model.ChainNode
	IsEntry     bool
	IsTransit   bool
	HasOutbound bool
	Preset      ConnectionPreset
}

// RenderMergedNodeConfig is the exported variant of buildMergedNodeConfig for
// callers that need to preview/dry-run a node's merged config without pushing
// it (e.g. the Deploy Status hash comparison and config preview endpoint).
func RenderMergedNodeConfig(
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
) (*config.SingboxConfig, *MergeReport, error) {
	return buildMergedNodeConfig(nodeInfo, nodeChains)
}

func buildMergedNodeConfig(
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
) (*config.SingboxConfig, *MergeReport, error) {

	roles := resolveChainRoles(nodeInfo.ID, nodeChains)
	report := &MergeReport{NodeID: nodeInfo.ID}

	if err := detectPortConflicts(nodeInfo, roles, report); err != nil {
		return nil, report, err
	}

	var inbounds []json.RawMessage
	var outbounds []json.RawMessage
	var endpoints []json.RawMessage
	seenOB := map[string]bool{}

	for i := range roles {
		role := &roles[i]
		ins, outs, eps := buildChainRoleInOut(role)
		inbounds = append(inbounds, ins...)
		endpoints = append(endpoints, eps...)
		for _, ob := range outs {
			if tag := extractTag(ob); !seenOB[tag] {
				seenOB[tag] = true
				outbounds = append(outbounds, ob)
			}
		}
		report.ChainsIncluded = append(report.ChainsIncluded, role.Chain.Name)
	}

	for i, ib := range nodeInfo.Inbounds {
		ins, eps := buildStandaloneInOut(&ib, fmt.Sprintf("sa-%d-%s", i, ib.Protocol))
		inbounds = append(inbounds, ins...)
		endpoints = append(endpoints, eps...)
	}
	report.StandaloneCount = len(nodeInfo.Inbounds)

	addIfMissing(&outbounds, seenOB, buildDirectOutbound("direct-out"))
	if needsBlock(roles) {
		blockJSON, _ := json.Marshal(map[string]any{"type": "block", "tag": "block"})
		addIfMissing(&outbounds, seenOB, blockJSON)
	}

	cfg := &config.SingboxConfig{
		Log:          &config.LogOptions{Level: "info"},
		Inbounds:     inbounds,
		Outbounds:    outbounds,
		// Route/DNS disabled by default for sing-box 1.13 detour compat (the
		// minimal inbounds+outbounds config works; route+detour crashed 1.13).
		// AB_ROUTE_DNS=1 opts back in so e2e can verify whether the patched
		// extended build (1.13.14-extended-2.5.0-patched) now handles route/dns
		// — this is the CTO-review M10 follow-up, verified against a real VPS
		// rather than enabled blindly.
		Experimental: &config.ExperimentalOptions{CacheFile: &config.CacheFileOptions{Enabled: true}},
	}
	if len(endpoints) > 0 {
		cfg.Endpoints = endpoints
	}
	if os.Getenv("AB_ROUTE_DNS") == "1" && len(roles) > 0 {
		// Route: chain user/transport inbounds -> the chain's strategy outbound;
		// standalone inbounds -> direct-out (their OutboundTag or direct-out).
		cfg.Route = buildMergedRoute(roles, nodeInfo)
		// DNS: a chain-detoured resolver + a direct resolver, with the chain's
		// direct-domain rules routed direct.
		chainOutTag := "direct-out"
		for _, r := range roles {
			if r.HasOutbound {
				chainOutTag = chainInterNodeOutboundTag(&r)
				break
			}
		}
		var directDomains []string
		if p := resolveChainPreset(roles[0].Chain); p.Routing.DirectDomains != nil {
			directDomains = p.Routing.DirectDomains
		}
		dns := BuildDNSWithDetour(chainOutTag, directDomains)
		// Resolve names on-node (direct-out). Using dns-chain as final creates a
		// bootstrap loop: the chain outbound needs the target IP before the tunnel
		// can carry the DNS query that would learn that IP.
		dns.Final = "dns-direct"
		cfg.DNS = dns
	}

	return cfg, report, nil
}

// buildMergedRoute builds the route section for a node's merged config: chain
// user-in / transport-in inbounds are routed to the chain's strategy outbound
// (or direct-out if the node has no chain outbound), and each standalone
// inbound is routed to its OutboundTag (default direct-out).
func buildMergedRoute(roles []chainRole, nodeInfo *model.NodeInfo) *config.RoutingSection {
	var rules []config.RouteRuleEntry
	for _, role := range roles {
		cn := role.Chain.Name
		var inTags []string
		if role.IsEntry {
			inTags = append(inTags, fmt.Sprintf("ch-%s-user-in", cn))
		}
		if role.IsTransit {
			inTags = append(inTags, fmt.Sprintf("ch-%s-transport-in", cn))
		}
		if len(inTags) == 0 {
			continue
		}
		routeOut := "direct-out"
		if role.HasOutbound {
			routeOut = chainInterNodeOutboundTag(&role)
		}
		rules = append(rules, config.RouteRuleEntry{Inbound: inTags, Outbound: routeOut})
	}
	for i, ib := range nodeInfo.Inbounds {
		obTag := ib.OutboundTag
		if obTag == "" {
			obTag = "direct-out"
		}
		rules = append(rules, config.RouteRuleEntry{
			Inbound:  []string{fmt.Sprintf("sa-%d-%s", i, ib.Protocol)},
			Outbound: obTag,
		})
	}
	return &config.RoutingSection{
		Rules:                 rules,
		Final:                 "direct-out",
		AutoDetectInterface:   true,
		DefaultDomainResolver: "dns-direct",
	}
}

func resolveChainRoles(nodeID string, chains []*model.Chain) []chainRole {
	var roles []chainRole
	for _, c := range chains {
		for i := range c.Nodes {
			n := &c.Nodes[i]
			if n.ID != nodeID {
				continue
			}
			roles = append(roles, chainRole{
				Chain: c, NodeIndex: i, Node: n,
				IsEntry: i == 0, IsTransit: i > 0,
				HasOutbound: i < len(c.Nodes)-1,
				Preset: resolveChainPreset(c),
			})
			break
		}
	}
	return roles
}

func resolveChainPreset(c *model.Chain) ConnectionPreset {
	if c.ObfuscationProfile != "" {
		if p, ok := GetPreset(c.ObfuscationProfile); ok {
			return p
		}
	}
	return GetEffectivePreset(c)
}

func detectPortConflicts(nodeInfo *model.NodeInfo, roles []chainRole, report *MergeReport) error {
	type claim struct {
		port     int
		claimant string
		role     string
	}
	var claims []claim

	for _, r := range roles {
		port := r.Node.Port
		if port == 0 {
			if r.IsEntry {
				port = chainUserPort(r.Chain)
			} else {
				port = defaultTransportPort
			}
		}
		roleType := "transport-in"
		if r.IsEntry {
			roleType = "user-in"
		}
		claims = append(claims, claim{port, r.Chain.Name, roleType})
	}

	for _, ib := range nodeInfo.Inbounds {
		claims = append(claims, claim{ib.Port, "standalone", ib.Protocol})
	}

	for _, c := range claims {
		report.Ports = append(report.Ports, PortUsage{Port: c.port, Claimant: c.claimant, Role: c.role})
	}

	byPort := map[int][]claim{}
	for _, c := range claims {
		byPort[c.port] = append(byPort[c.port], c)
	}
	for port, group := range byPort {
		if len(group) > 1 {
			parts := make([]string, len(group))
			for i, c := range group {
				parts[i] = fmt.Sprintf("%s (%s)", c.claimant, c.role)
			}
			return fmt.Errorf("port %d conflict: %s", port, strings.Join(parts, " vs "))
		}
	}
	return nil
}

func buildChainRoleInOut(role *chainRole) (inbounds, outbounds, endpoints []json.RawMessage) {
	c := role.Chain
	cn := c.Name
	p := ensureHopParams(role)

	if role.IsEntry {
		userPort := chainUserPort(c)
		switch c.UserProtocol {
		case model.UserProtocolAWG:
			ep, _, err := buildAWGUserInbound(userPort, p.UUID,
				fmt.Sprintf("ch-%s-user-in", cn), &role.Preset,
				c.AWGEntryServerPriv, awgClientPub(c))
			if err == nil {
				endpoints = append(endpoints, ep)
			}
			tun := config.TUNInbound{
				Type:      "tun",
				Tag:       fmt.Sprintf("ch-%s-tun-in", cn),
				Address:   []string{"172.16.250.1/30"},
				AutoRoute: true,
			}
			tunJSON, _ := json.Marshal(tun)
			inbounds = append(inbounds, tunJSON)

		case model.UserProtocolTUIC:
			inb := buildTUICUserInbound(userPort, tuicUUID(c), tuicPassword(c),
				fmt.Sprintf("ch-%s-user-in", cn), &role.Preset, p)
			inbounds = append(inbounds, inb)

		default:
			inb := buildUserInbound(userPort, p.UUID,
				fmt.Sprintf("ch-%s-user-in", cn))
			inbounds = append(inbounds, inb)
		}
	}

	if role.IsTransit {
		tag := fmt.Sprintf("ch-%s-transport-in", cn)
		var inb json.RawMessage
		if c.Transport == model.TransportXHTTP {
			inb = buildXHTTPTransportInbound(p, tag, &role.Preset)
		} else {
			inb = buildTransportInbound(p, tag)
		}
		inbounds = append(inbounds, inb)
	}

	if role.HasOutbound {
		next := c.Nodes[role.NodeIndex+1]
		np := &hopParams{
			UUID:       next.TransitUUID,
			PrivateKey: next.TransitPrivKey,
			ShortID:    next.TransitShortID,
			Port:       next.Port,
			ServerName: ResolveServerName(&role.Preset),
		}
		if np.Port == 0 {
			np.Port = defaultTransportPort
		}
		if np.ServerName == "" {
			np.ServerName = DefaultRealitySNI
		}

		outTag := fmt.Sprintf("ch-%s-out-%s", cn, safeSNILabel(np.ServerName))
		var outb json.RawMessage
		var err error
		if c.Transport == model.TransportXHTTP {
			outb, err = buildXHTTPTransportOutbound(np, extractHost(next.Addr), outTag, &role.Preset)
		} else {
			outb, err = buildTransportOutbound(np, extractHost(next.Addr), outTag)
		}
		if err == nil {
			outbounds = append(outbounds, outb)
			// Linear chains have a single inter-node outbound; wrapping it in
			// urltest probes gstatic through the hop and returns EOF while transit
			// is still failing, which breaks routing and masks the real error.
		}
	}

	return
}

func ensureHopParams(role *chainRole) *hopParams {
	n := role.Node
	p := &hopParams{
		UUID:       n.TransitUUID,
		PrivateKey: n.TransitPrivKey,
		ShortID:    n.TransitShortID,
		Port:       n.Port,
		ServerName: ResolveServerName(&role.Preset),
	}
	if p.Port == 0 {
		if role.IsEntry {
			p.Port = defaultUserPort
		} else {
			p.Port = defaultTransportPort
		}
	}
	if p.UUID == "" {
		p.UUID = generateStableUUID()
	}
	if p.PrivateKey == "" {
		b := make([]byte, 32)
		rand.Read(b)
		clamped, err := clampRealityPrivateKeyB64(base64.RawURLEncoding.EncodeToString(b))
		if err != nil {
			clamped = base64.RawURLEncoding.EncodeToString(b)
		}
		p.PrivateKey = clamped
	} else if clamped, err := clampRealityPrivateKeyB64(p.PrivateKey); err == nil {
		p.PrivateKey = clamped
	}
	if p.ShortID == "" {
		b := make([]byte, 8)
		rand.Read(b)
		p.ShortID = hex.EncodeToString(b)
	}
	if p.ServerName == "" {
		p.ServerName = DefaultRealitySNI
	}
	return p
}

func ResolveServerName(preset *ConnectionPreset) string {
	if preset.Reality != nil && len(preset.Reality.ServerNames) > 0 {
		return preset.Reality.ServerNames[0]
	}
	if preset.XHTTP != nil && len(preset.XHTTP.Hosts) > 0 {
		return preset.XHTTP.Hosts[0]
	}
	return DefaultRealitySNI
}

func buildStandaloneInOut(ib *model.NodeInbound, tag string) (inbounds, endpoints []json.RawMessage) {
	preset := GetDefaultPreset()
	if ib.Obfuscation != "" {
		if p, ok := GetPreset(ib.Obfuscation); ok {
			preset = p
		}
	}
	serverName := ResolveServerName(&preset)

	switch ib.Protocol {
	case "vless-reality":
		inb := config.VLESSInbound{
			Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
			Users: []config.VLESSUser{{Name: "user", UUID: ib.UUID, Flow: "xtls-rprx-vision"}},
			TLS: &config.InboundTLSOptions{
				Enabled: true, ServerName: serverName,
				Reality: &config.InboundRealityOptions{
					Enabled: true, PrivateKey: ib.ServerPrivKey, ShortID: []string{ib.ShortID},
					Handshake: &config.RealityHandshake{Server: serverName, ServerPort: 443},
				},
			},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "awg":
		ep, _, err := buildAWGUserInbound(ib.Port, ib.UUID, tag, &preset, ib.ServerPrivKey, "")
		if err == nil {
			endpoints = append(endpoints, ep)
		}

	case "tuic":
		tls := &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
			ALPN:       []string{"h3"}, // required: QUIC/TUIC clients abort without an ALPN
		}

		cert := ib.TLSCertificate
		key := ib.TLSPrivateKey
		if cert == "" || key == "" {
			// Auto-generate self-signed cert so the inbound is valid.
			// Ideally this should be done at save time and persisted.
			var cerr error
			cert, key, cerr = GenerateSelfSignedCert(serverName)
			if cerr == nil {
				// Note: in production these should be persisted back to the store
				ib.TLSCertificate = cert
				ib.TLSPrivateKey = key
			}
		}

		if cert != "" && key != "" {
			tls.Certificate = cert
			tls.Key = key
		}

		inb := config.TUICInbound{
			Type:              "tuic",
			Tag:               tag,
			Listen:            "0.0.0.0",
			ListenPort:        ib.Port,
			Users:             []config.TUICUser{{UUID: ib.UUID, Password: ib.ServerPrivKey}},
			CongestionControl: "bbr",
			AuthTimeout:       "3s",
			ZeroRTTHandshake:  true,
			Heartbeat:         "10s",
			TLS:               tls,
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "xhttp":
		inb := config.VLESSInbound{
			Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
			Users: []config.VLESSUser{{Name: "user", UUID: ib.UUID}},
			Transport: &config.TransportOptions{
				Type: "http", Path: "/api", Method: "POST",
				Headers: map[string][]string{"Content-Type": {"application/json"}},
			},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "hysteria2":
		inb := config.Hysteria2Inbound{
			Type: "hysteria2", Tag: tag, Listen: "::", ListenPort: ib.Port,
			Users: []config.Hysteria2User{{Password: ib.UUID}},
			UpMbps: 1000, DownMbps: 1000,
			Obfs: &config.Hysteria2Obfs{Type: "salamander", Password: ib.ObfsPassword},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	default:
		inb := config.VLESSInbound{
			Type: "vless", Tag: tag, Listen: "0.0.0.0", ListenPort: ib.Port,
			Users: []config.VLESSUser{{Name: "user", UUID: ib.UUID, Flow: "xtls-rprx-vision"}},
			TLS: &config.InboundTLSOptions{Enabled: false},
			Transport: &config.TransportOptions{Type: "ws", Path: "/ws"},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)
	}

	return
}

func addIfMissing(outbounds *[]json.RawMessage, seen map[string]bool, ob json.RawMessage) {
	if tag := extractTag(ob); !seen[tag] {
		seen[tag] = true
		*outbounds = append(*outbounds, ob)
	}
}

func extractTag(raw json.RawMessage) string {
	var m struct{ Tag string `json:"tag"` }
	json.Unmarshal(raw, &m)
	return m.Tag
}

func needsBlock(roles []chainRole) bool {
	for _, role := range roles {
		routing := BuildRoutingSection(&role.Preset, "")
		for _, r := range routing.Rules {
			if r.Outbound == "block" {
				return true
			}
		}
	}
	return false
}

// tuicUUID returns the chain's TUIC entry UUID, generating a stable one if empty.
func tuicUUID(c *model.Chain) string {
	if c.TUICEntryUserUUID != "" {
		return c.TUICEntryUserUUID
	}
	return generateStableUUID()
}

// tuicPassword returns the chain's TUIC entry password, generating an
// INDEPENDENT one if empty (must not fall back to the UUID — CTO-review M7).
func tuicPassword(c *model.Chain) string {
	if c.TUICEntryUserPassword != "" {
		return c.TUICEntryUserPassword
	}
	return GenerateTUICPassword()
}

// chainUserPort returns the chain's user-entry listen port, defaulting to
// defaultUserPort (8443) when UserEntryPort is unset.
func chainUserPort(c *model.Chain) int {
	if c.UserEntryPort > 0 {
		return c.UserEntryPort
	}
	return defaultUserPort
}

// chainInterNodeOutboundTag is the vless/xhttp outbound tag for the next hop in
// a linear chain (no urltest wrapper).
func chainInterNodeOutboundTag(role *chainRole) string {
	sn := ResolveServerName(&role.Preset)
	return fmt.Sprintf("ch-%s-out-%s", role.Chain.Name, safeSNILabel(sn))
}

func safeSNILabel(sni string) string {
	if dot := strings.IndexByte(sni, '.'); dot > 0 {
		return sni[:dot]
	}
	if len(sni) > 16 {
		return sni[:16]
	}
	return sni
}

// awgClientPub returns a valid client public key for the AWG endpoint.
// Generates a random keypair if none is stored on the chain.
func awgClientPub(c *model.Chain) string {
	if c.AWGEntryClientPub != "" {
		return c.AWGEntryClientPub
	}
	_, pub, _ := generateWireGuardKeypair()
	return pub
}

// diffInboundTags compares old and new sing-box config JSON and returns
// which inbound/endpoint tags were added and removed.
func diffInboundTags(oldJSON, newJSON string) (added, removed []string) {
	oldTags := extractAllTags(oldJSON)
	newTags := extractAllTags(newJSON)

	oldSet := make(map[string]bool, len(oldTags))
	for _, t := range oldTags {
		oldSet[t] = true
	}
	newSet := make(map[string]bool, len(newTags))
	for _, t := range newTags {
		newSet[t] = true
	}

	for t := range newSet {
		if !oldSet[t] {
			added = append(added, t)
		}
	}
	for t := range oldSet {
		if !newSet[t] {
			removed = append(removed, t)
		}
	}
	return
}

// extractAllTags parses a sing-box config JSON and returns all tags
// from inbounds[], endpoints[] and outbounds[] arrays.
func extractAllTags(cfgJSON string) []string {
	if cfgJSON == "" {
		return nil
	}
	var raw struct {
		Inbounds  []json.RawMessage `json:"inbounds"`
		Endpoints []json.RawMessage `json:"endpoints"`
		Outbounds []json.RawMessage `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &raw); err != nil {
		return nil
	}
	var tags []string
	for _, inb := range raw.Inbounds {
		if t := extractTag(inb); t != "" {
			tags = append(tags, t)
		}
	}
	for _, ep := range raw.Endpoints {
		if t := extractTag(ep); t != "" {
			tags = append(tags, t)
		}
	}
	for _, ob := range raw.Outbounds {
		if t := extractTag(ob); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
