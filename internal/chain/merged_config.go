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
	NodeID          string      `json:"node_id"`
	StandaloneCount int         `json:"standalone_count"`
	ChainsIncluded  []string    `json:"chains_included"`
	Ports           []PortUsage `json:"ports"`
	Warnings        []string    `json:"warnings,omitempty"`
	AddedInbounds   []string    `json:"added_inbounds,omitempty"`
	RemovedInbounds []string    `json:"removed_inbounds,omitempty"`
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
	// LevelIndex is the node's level in the v2 levels model (-1 for legacy
	// flat chains). Level 0 is the user-facing entry level.
	LevelIndex int
	// NextNodes is the downstream group this node forwards to (the nodes of
	// the next level). Empty on the last level. For legacy flat chains it
	// holds at most the single next node, so all readers can treat it
	// uniformly.
	NextNodes []model.ChainNode
}

// MergedNodeConfigParams carries everything buildMergedNodeConfig needs.
// Required: NodeInfo and NodeChains. The three user maps are optional:
// nil/empty falls back to the single-user/legacy behavior (mirrors the
// ClientConfigParams "derive defaults inside" idiom in this package). Production
// callers should prefer NewMergedNodeConfigParams, which derives the three user
// collections from the store so the per-client routing plumbing (usersByChain,
// usersByInbound) and the node-level MTProxy user list are populated uniformly.
type MergedNodeConfigParams struct {
	// ── node-level (required) ──
	NodeInfo *model.NodeInfo
	// ── chain-level (required; only consumed by resolveChainRoles) ──
	NodeChains []*model.Chain
	// ── users (all optional; nil → legacy single-user fallback) ──
	// UsersByChain maps chain name -> users assigned to that chain (via
	// User.ChainNames); the chain's user-entry inbound is rendered multi-user.
	UsersByChain map[string][]model.User
	// UsersByInbound maps standalone inbound Tag -> users (per-peer AWG).
	UsersByInbound map[string][]model.User
	// MtproxyUsers drives the node-level MTProxy inbound (one inbound per user).
	MtproxyUsers []*model.User
	// RouteRules are the node's manual (operator) routing rules, priority-
	// sorted. Empty = no manual routing. Expanded into sing-box route rules
	// above the structural cascade (routing.go).
	RouteRules []*model.RouteRule
	// AllUsers resolves RouteRule.UserIDs to identities (AWG inner IP /
	// auth_user). Only needed when RouteRules is non-empty.
	AllUsers []model.User
}

// NewMergedNodeConfigParams builds a MergedNodeConfigParams from the store,
// deriving the three user collections (usersByChain, usersByInbound, and the
// node's MTProxy users) the same way every deploy caller used to do inline.
// This is the single source of truth for the per-client routing plumbing —
// callers no longer repeat the usersByChainMap/usersByInboundMap/
// ListMTProxyUsersForNode trio (CTO-review §4: the 5-param assembly was a
// 7-arg-smell because of this repeated derivation).
func NewMergedNodeConfigParams(store *Store, nodeInfo *model.NodeInfo, chains []*model.Chain) MergedNodeConfigParams {
	rules, err := store.ListRouteRulesForNode(nodeInfo.ID)
	if err != nil {
		rules = nil // a store read failure must not break the deploy; manual rules just drop out
	}
	var allUsers []model.User
	if len(rules) > 0 {
		if us, err := store.ListUsers(); err == nil {
			for _, u := range us {
				if u != nil {
					allUsers = append(allUsers, *u)
				}
			}
		}
	}
	return MergedNodeConfigParams{
		NodeInfo:       nodeInfo,
		NodeChains:     chains,
		UsersByChain:   usersByChainMap(store, chains),
		UsersByInbound: usersByInboundMap(store, nodeInfo.Inbounds),
		MtproxyUsers:   store.ListMTProxyUsersForNode(nodeInfo.ID),
		RouteRules:     rules,
		AllUsers:       allUsers,
	}
}

// RenderMergedNodeConfigStore renders a node's merged config with FULL store
// plumbing (multi-user inbounds + manual route rules) — the deploy-equivalent
// preview/hash path. Use RenderMergedNodeConfig only for tests/throwaway
// renders that must not touch the store.
func RenderMergedNodeConfigStore(store *Store, nodeInfo *model.NodeInfo, nodeChains []*model.Chain) (*config.SingboxConfig, *MergeReport, error) {
	return buildMergedNodeConfig(NewMergedNodeConfigParams(store, nodeInfo, nodeChains))
}

// RenderMergedNodeConfig is the exported variant of buildMergedNodeConfig for
// callers that need to preview/dry-run a node's merged config without pushing
// it (e.g. the Deploy Status hash comparison and config preview endpoint).
// mtproxyUsers, when non-nil, drives the node-level MTProxy inbound emission
// (same as the deploy path's ListMTProxyUsersForNode); pass nil for nodes with
// no MTProxy users. It does not know about per-chain users, so chain entry
// inbounds fall back to the chain-wide shared credentials (single-user),
// matching the pre-per-user behavior. Use buildMergedNodeConfig with a
// UsersByChain map to emit multi-user inbounds.
func RenderMergedNodeConfig(
	nodeInfo *model.NodeInfo,
	nodeChains []*model.Chain,
	mtproxyUsers []*model.User,
) (*config.SingboxConfig, *MergeReport, error) {
	return buildMergedNodeConfig(MergedNodeConfigParams{
		NodeInfo:     nodeInfo,
		NodeChains:   nodeChains,
		MtproxyUsers: mtproxyUsers,
	})
}

// buildMergedNodeConfig renders a node's merged sing-box config from its
// standalone inbounds plus every chain that includes it. UsersByChain, when
// non-nil, maps chain name -> users assigned to that chain (via User.ChainNames);
// the chain's user-entry inbound is then rendered multi-user (one Users[] entry
// per user with their per-user creds). When nil/empty for a chain, the entry
// inbound falls back to the chain-wide shared credentials (single-user, legacy).
func buildMergedNodeConfig(p MergedNodeConfigParams) (*config.SingboxConfig, *MergeReport, error) {
	nodeInfo := p.NodeInfo
	nodeChains := p.NodeChains
	usersByChain := p.UsersByChain
	usersByInbound := p.UsersByInbound
	mtproxyUsers := p.MtproxyUsers

	roles := resolveChainRoles(nodeInfo.ID, nodeChains)
	report := &MergeReport{NodeID: nodeInfo.ID}

	if err := detectPortConflicts(nodeInfo, nodeChains, roles, report); err != nil {
		return nil, report, err
	}

	var inbounds []json.RawMessage
	var outbounds []json.RawMessage
	var endpoints []json.RawMessage
	seenOB := map[string]bool{}

	// roleErrors collects hard build errors from buildChainRoleInOut (currently
	// only the frozen-Hysteria2-transport case, which produces no inbound/outbound
	// — a broken chain). These MUST fail the deploy loudly rather than silently
	// shipping a config missing its transport/user inbound. Collected separately
	// from report.Warnings (which are non-fatal advisories).
	var roleErrors []string
	for i := range roles {
		role := &roles[i]
		users := usersForChain(usersByChain, role.Chain.Name)
		ins, outs, eps, roleWarnings := buildChainRoleInOut(role, users, nodeInfo)
		inbounds = append(inbounds, ins...)
		endpoints = append(endpoints, eps...)
		for _, ob := range outs {
			if tag := extractTag(ob); !seenOB[tag] {
				seenOB[tag] = true
				outbounds = append(outbounds, ob)
			}
		}
		roleErrors = append(roleErrors, roleWarnings...)
		report.ChainsIncluded = append(report.ChainsIncluded, role.Chain.Name)
	}
	if len(roleErrors) > 0 {
		// A frozen/unsupported transport produced a broken config (missing
		// inbound or outbound). Fail the build so the deploy surfaces the error
		// instead of pushing a non-functional chain.
		return nil, report, fmt.Errorf("merged config: %s", strings.Join(roleErrors, "; "))
	}

	// Caddy mode: caddy owns 80/443, standalone inbounds move to loopback on
	// remapped ports behind the SNI routes (see chain.RemapInboundPorts). nil
	// map = legacy direct-listen mode.
	var caddyPorts map[int]int
	if CaddyMode(nodeInfo) {
		caddyPorts = RemapInboundPorts(nodeInfo.Inbounds)
	}

	for i, ib := range nodeInfo.Inbounds {
		if IsChainSourcedInbound(&ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, &ib) {
			// Chain-entry materialized inbound — rendered via the chain role
			// path (renderChainEntryAWGConf / buildChainRoleInOut), not as a
			// standalone. Skipping here avoids a double listener on the same
			// port (profile inbounds keep Source="standalone" when shared
			// with a chain entry, hence the reference check too).
			continue
		}
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("sa-%d-%s", i, ib.Protocol) // legacy index-based tag (backward compat)
		}
		ins, eps := buildStandaloneInOut(&ib, tag, usersByInbound, nodeInfo, i, caddyPorts)
		inbounds = append(inbounds, ins...)
		endpoints = append(endpoints, eps...)
	}
	report.StandaloneCount = len(nodeInfo.Inbounds)

	// MTProxy inbounds are emitted at the node level (not in buildStandaloneInOut
	// / buildChainRoleInOut, which have no access to the MtproxyUser list). A
	// node can carry MTProxy as a standalone NodeInbound (protocol "mtproxy") or
	// as a chain user-entry (UserProtocol == MTProxy); both are built from the
	// node's mtproxyUsers via buildMTProxyInbound. Skipped when there are no
	// enabled users with a secret (sing-box rejects an mtproxy inbound with an
	// empty users[]).
	enabledMTProxy := mtproxyUsersForNode(mtproxyUsers)
	if len(enabledMTProxy) > 0 {
		// Standalone MTProxy inbounds.
		for i, ib := range nodeInfo.Inbounds {
			if ib.Protocol != "mtproxy" || IsChainSourcedInbound(&ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, &ib) {
				continue
			}
			tag := ib.Tag
			if tag == "" {
				tag = fmt.Sprintf("sa-%d-mtproxy", i)
			}
			port := mtproxyInboundPort(ib.Port)
			if caddyPorts != nil {
				// MTProxy FakeTLS cannot sit behind the SNI router; in caddy
				// mode it must vacate the owned TCP ports (gating blocks NEW
				// ones, this evicts legacy stores). The evicted listener is
				// loopback-only and effectively retired.
				port = CaddyEvictPort(port)
			}
			if inb := buildMTProxyInbound(port, tag, enabledMTProxy); inb != nil {
				inbounds = append(inbounds, inb)
			}
		}
		// Chain MTProxy entry inbound(s).
		for _, r := range roles {
			if !r.IsEntry || r.Chain.UserProtocol != model.UserProtocolMTProxy {
				continue
			}
			tag := chainUserInboundTag(r.Chain, r.Node.ID)
			port := chainEntryPort(r.Chain, r.Node.ID)
			if inb := buildMTProxyInbound(mtproxyInboundPort(port), tag, enabledMTProxy); inb != nil {
				inbounds = append(inbounds, inb)
			}
		}
	}

	// Kernel-AWG TUN overlay: when this node runs a kernel AWG server (a chain
	// AWG entry, a multi-exit balancer with ExitTargets, or a standalone AWG
	// inbound), sing-box must capture awg0 traffic via a TUN inbound and route
	// it across the exit interfaces through a fallback balancer. The AWG
	// interface itself is owned by the kernel (awg-quick), pushed separately as
	// awg0.conf — NOT a userspace sing-box endpoint. The overlay carries the
	// route rules (sniff/hijack-dns/tun-in→balancer) that make the TUN useful,
	// so AWG nodes get a Route section regardless of AB_ROUTE_DNS.
	var awgOverlayRoute []config.RouteRuleEntry
	if overlay := awgTUNOverlayNeeded(roles, nodeInfo); overlay {
		node := awgOverlayNode(roles, nodeInfo)
		ins, outs, rts := BuildAWGTUNOverlay(AWGTUNOverlayParams{
			IncludeInterfaces: tunIncludeInterfacesForNode(node, nodeInfo, nodeChains),
			ExitInterfaces:    exitInterfacesForNode(node),
			BalancerTag:       balancerTagForNode(node),
			FinalOutbound:     "direct",
			// AutoRedirect is opt-in via AB_AWG_AUTO_REDIRECT=1 (egress-trial
			// harness, PROGRESS §21): nftables prerouting capture of forwarded
			// ingress. Requires the nftables package on the node (Debian 13
			// ships nft only; without it sing-box fails to start).
			AutoRedirect: awgAutoRedirectFromEnv(),
			// A linear AWG chain entry with a downstream hop forwards TUN traffic
			// to the inter-node outbound — without this the catch-all targets
			// "direct" and every AWG user egresses from the entry node, never
			// reaching the downstream hop (chain forwarding broken). "" for
			// multi-exit balancers (their awg-exit-nX handles exit) and last-node /
			// standalone cases (egress direct).
			ForwardOutbound: awgForwardOutboundForRoles(roles),
		})
		inbounds = append(inbounds, ins...)
		for _, ob := range outs {
			addIfMissing(&outbounds, seenOB, ob)
		}
		awgOverlayRoute = rts
	}

	addIfMissing(&outbounds, seenOB, buildDirectOutbound("direct-out"))
	injectAWGOutboundDirects(&outbounds, seenOB, nodeInfo)
	if needsBlock(roles) {
		blockJSON, _ := json.Marshal(map[string]any{"type": "block", "tag": "block"})
		addIfMissing(&outbounds, seenOB, blockJSON)
	}

	logLevel := "info"
	if v := os.Getenv("AB_LOG_LEVEL"); v != "" {
		logLevel = v
	}
	cfg := &config.SingboxConfig{
		Log:       &config.LogOptions{Level: logLevel},
		Inbounds:  inbounds,
		Outbounds: outbounds,
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
	// Manual (operator) route rules (routing.go): expanded regardless of
	// AB_ROUTE_DNS — a node with operator-defined rules needs a route section.
	var manualEx ManualRouteExpansion
	if len(p.RouteRules) > 0 {
		scope := manualRuleInboundScope(roles, nodeInfo, nodeChains, len(awgOverlayRoute) > 0)
		manualEx = ExpandManualRouteRules(p.RouteRules, scope, p.AllUsers)
		report.Warnings = append(report.Warnings, manualEx.Warnings...)
	}

	if os.Getenv("AB_ROUTE_DNS") == "1" && len(roles) > 0 {
		// Route: chain user/transport inbounds -> the chain's strategy outbound;
		// standalone inbounds -> direct-out (their OutboundTag or direct-out).
		// Per-client routing: when usersByChain carries users with a ChainExit pin
		// for this chain, emit an auth_user rule steering that user's traffic to the
		// chosen exit's outbound (direct-out if the exit is THIS node, the
		// inter-node outbound if it is the next hop). Requires AB_ROUTE_DNS=1.
		cfg.Route = buildMergedRoute(roles, nodeInfo, usersByChain, manualEx.Rules)
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
	} else if len(manualEx.Rules) > 0 {
		// Manual (operator) route rules without AB_ROUTE_DNS: the node still needs
		// a route section. No DNS section is emitted, so no dns-server tag may be
		// referenced (default_domain_resolver stays empty).
		cfg.Route = buildMergedRoute(roles, nodeInfo, usersByChain, manualEx.Rules)
		cfg.Route.DefaultDomainResolver = ""
	}
	if cfg.Route != nil && len(manualEx.RuleSets) > 0 {
		cfg.Route.RuleSet = append(cfg.Route.RuleSet, manualEx.RuleSets...)
	}
	// Domain/rule_set matching needs sniffing. The AWG TUN overlay already leads
	// with a sniff action rule; a node without the overlay gets its own.
	if cfg.Route != nil && len(manualEx.Rules) > 0 && len(awgOverlayRoute) == 0 {
		cfg.Route.Rules = append([]config.RouteRuleEntry{{Action: "sniff"}}, cfg.Route.Rules...)
	}
	// Kernel-AWG nodes need the TUN-overlay route rules (sniff/hijack-dns/
	// tun-in→forward/balancer/direct) regardless of AB_ROUTE_DNS — without them
	// the TUN captures traffic but routes it nowhere. Merge order matters:
	//   1. Action rules (sniff, dns-hijack) FIRST — they're pre-route actions
	//      (sniff runs on every connection; dns-hijack terminates DNS only) and
	//      don't shadow per-client routing.
	//   2. Per-client rules (buildMergedRoute, keyed on tun-in + source_ip_cidr
	//      for AWG) MIDDLE — so a pinned user matches before the catch-all.
	//   3. The tun-in catch-all (→forward/balancer/direct) LAST — it's the
	//      fallback for unpinned AWG users. Putting it first (the old code) would
	//      shadow every per-client pin: first-match-wins, catch-all matches all
	//      tun-in traffic, the source_ip_cidr rules never fire.
	if len(awgOverlayRoute) > 0 {
		if cfg.Route == nil {
			cfg.Route = &config.RoutingSection{
				Final:               "direct",
				AutoDetectInterface: true,
			}
		}
		var actionRules, catchAll []config.RouteRuleEntry
		for _, r := range awgOverlayRoute {
			if len(r.Inbound) > 0 {
				catchAll = append(catchAll, r) // the tun-in→routeOut catch-all
			} else {
				actionRules = append(actionRules, r) // sniff, dns-hijack
			}
		}
		cfg.Route.Rules = append(append(actionRules, cfg.Route.Rules...), catchAll...)
	}

	return cfg, report, nil
}

// buildMergedRoute builds the route section for a node's merged config: chain
// user-in / transport-in inbounds are routed to the chain's outbound (or
// direct-out if the node has no chain outbound), and each standalone inbound is
// routed to its OutboundTag (default direct-out).
//
// Per-client routing (phase B4): when usersByChain carries users with a
// ChainExit pin for a chain, emit a per-user route rule for that chain's
// entry/transit inbound steering the user's traffic to the chosen exit.
//
// Two mechanisms, by user-entry protocol:
//   - TUIC/VLESS/Hysteria2: auth_user rule (the inbound carries a users[] array
//     and the rule matches the user's auth identity — UUID for TUIC, Name for
//     VLESS/Hysteria2). auth_user is an inbound-identity match, so it only
//     works where the user's identity is re-asserted — i.e. on the entry. A pin
//     to a node further than one hop down is NOT expressible (transit hops do
//     not carry the end-user identity) and is skipped.
//   - AWG: source_ip_cidr rule (each user is a WireGuard peer with a unique
//     tunnel IP = AWGAddress). The inner source IP is preserved end-to-end
//     through the inter-node XHTTP tunnel, so EVERY hop the user's traffic
//     traverses can route by source IP. This makes a pin to ANY downstream node
//     expressible: each hop before the pinned exit forwards down the chain
//     (inter-node outbound), and the pinned exit itself routes to direct-out.
//
// Per-client rules must come BEFORE the generic inbound->outbound rules so they
// take precedence. A user without the protocol's per-user cred (auth identity
// for TUIC/VLESS, AWGAddress for AWG) is skipped — they fall back to the
// chain's default route.
func buildMergedRoute(roles []chainRole, nodeInfo *model.NodeInfo, usersByChain map[string][]model.User, manualRules []config.RouteRuleEntry) *config.RoutingSection {
	var rules []config.RouteRuleEntry

	// Manual (operator) rules FIRST — above the per-client pins and the
	// structural cascade (operator decision wins; first-match-wins, 2026-08-27).
	// They arrive priority-sorted from ExpandManualRouteRules.
	rules = append(rules, manualRules...)

	// Per-client rules first (highest precedence for the matched user). Emitted
	// on every node the user's traffic traverses: on the entry for the user-in
	// inbound, and on each transit for the transport-in inbound.
	for _, role := range roles {
		users := usersForChain(usersByChain, role.Chain.Name)
		if len(users) == 0 {
			continue
		}
		var inTag string
		if role.IsEntry {
			// Under the kernel-AWG architecture, AWG user traffic arrives via the
			// TUN overlay inbound (tun-in), NOT the old userspace ch-<chain>-user-in
			// endpoint (which no longer exists). Re-key AWG-entry per-client rules
			// to tun-in so they actually match. Non-AWG entries (TUIC/VLESS/MTProxy)
			// still use their dedicated sing-box inbound tag.
			if role.Chain.UserProtocol == model.UserProtocolAWG {
				inTag = tunInboundTag
			} else {
				inTag = chainUserInboundTag(role.Chain, role.Node.ID)
			}
		} else if role.IsTransit {
			inTag = fmt.Sprintf("ch-%s-transport-in", role.Chain.Name)
		} else {
			continue
		}
		nextID := ""
		if role.HasOutbound {
			nextID = role.Chain.Nodes[role.NodeIndex+1].ID
		}
		for _, u := range users {
			exitID, pinned := u.ChainExit[role.Chain.Name]
			if !pinned || exitID == "" {
				continue
			}
			if role.Chain.UserProtocol == model.UserProtocolAWG {
				// AWG: route by the peer's inner source IP. The source IP travels
				// through the chain, so a pin to any downstream node works:
				//   - this node IS the pinned exit -> direct-out (egress here);
				//   - this node is BEFORE the pinned exit and has an outbound ->
				//     forward down the chain (the pinned exit will direct-out it).
				// A user without AWGAddress cannot be matched by source_ip_cidr.
				if u.AWGAddress == "" {
					continue
				}
				switch {
				case exitID == role.Node.ID:
					rules = append(rules, config.RouteRuleEntry{
						Inbound: []string{inTag}, SourceIPCIDR: []string{u.AWGAddress}, Outbound: "direct-out",
					})
				case role.HasOutbound && nodeIsAtOrBefore(role, exitID):
					rules = append(rules, config.RouteRuleEntry{
						Inbound: []string{inTag}, SourceIPCIDR: []string{u.AWGAddress}, Outbound: chainInterNodeOutboundTag(&role),
					})
				}
				continue
			}
			// TUIC/VLESS/Hysteria2: auth_user match. auth_user is an inbound
			// identity match, only re-asserted on the entry; a pin beyond the
			// next hop is not expressible and is skipped.
			authID := userAuthIdentity(u, role.Chain.UserProtocol)
			if authID == "" {
				continue
			}
			switch {
			case exitID == role.Node.ID:
				// Pinned exit is THIS node: egress here (direct-out), do not forward.
				rules = append(rules, config.RouteRuleEntry{
					Inbound: []string{inTag}, AuthUser: []string{authID}, Outbound: "direct-out",
				})
			case role.HasOutbound && exitID == nextID:
				// Pinned exit is the next hop: forward to it (it will egress there
				// via its own auth_user direct-out rule).
				rules = append(rules, config.RouteRuleEntry{
					Inbound: []string{inTag}, AuthUser: []string{authID}, Outbound: chainInterNodeOutboundTag(&role),
				})
			}
			// Other pins (exit further down) are not expressible in a linear chain
			// without per-hop auth_user propagation; skipped -> default route.
		}
	}

	// Generic inbound -> outbound rules (fallback for unpinned users and transit).
	// AWG entries are skipped here: under the kernel-AWG architecture the
	// TUN-overlay catch-all (tun-in→forward/balancer/direct, emitted by
	// BuildAWGTUNOverlay) handles the unpinned-AWG-user default route. Emitting a
	// second tun-in rule here would either duplicate or shadow the overlay one.
	// Per-client AWG pins are already emitted above (keyed to tun-in); the
	// overlay catch-all is the fallback for everyone else.
	for _, role := range roles {
		if role.IsEntry && role.Chain.UserProtocol == model.UserProtocolAWG {
			continue // overlay catch-all handles unpinned AWG users
		}
		var inTags []string
		if role.IsEntry {
			inTags = append(inTags, chainUserInboundTag(role.Chain, role.Node.ID))
		}
		if role.IsTransit {
			inTags = append(inTags, fmt.Sprintf("ch-%s-transport-in", role.Chain.Name))
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

// manualRuleInboundScope returns the USER-FACING inbound tags of a node — the
// inbound scope of manual route rules. Inter-node TRANSIT inbounds are
// deliberately excluded: a domain/block rule matching transit traffic would
// break the cascade (first-match-wins). Scope = tun-in (AWG TUN overlay),
// non-AWG chain user-in tags, and the standalone sa-* tags.
func manualRuleInboundScope(roles []chainRole, nodeInfo *model.NodeInfo, nodeChains []*model.Chain, hasOverlay bool) []string {
	var tags []string
	seen := map[string]bool{}
	add := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			tags = append(tags, t)
		}
	}
	if hasOverlay {
		add(tunInboundTag)
	}
	for _, role := range roles {
		if role.IsEntry && role.Chain.UserProtocol != model.UserProtocolAWG {
			add(chainUserInboundTag(role.Chain, role.Node.ID))
		}
	}
	for i, ib := range nodeInfo.Inbounds {
		if IsChainSourcedInbound(&ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, &ib) {
			continue
		}
		tag := ib.Tag
		if tag == "" {
			tag = fmt.Sprintf("sa-%d-%s", i, ib.Protocol)
		}
		add(tag)
	}
	return tags
}

// resolveChainRoles maps a nodeID to its role(s) across all chains that
// contain it. A node appears once per chain. The role is determined by the
// explicit ChainNode.Role field when set, otherwise by position (index 0 =
// entry, the rest transit) for backward compatibility with pre-multi-entry
// store.json. Multi-entry: several nodes may carry Role=entry and each
// becomes a user-facing entry for the chain. HasOutbound is positional — a
// node has an inter-node outbound unless it is the last node in the chain
// (an entry in the middle still forwards to the next hop).
func resolveChainRoles(nodeID string, chains []*model.Chain) []chainRole {
	var roles []chainRole
	for _, c := range chains {
		if c.IsLevelized() {
			// v2 levels: the level position IS the role. Level 0 = entry;
			// last level = exit side; nodes in between are transit. An
			// explicit Role=exit marker inside a level still suppresses the
			// sing-box transit inbound (the kernel AWG exit-link owns it).
			flatIdx := 0
			found := false
			for li := range c.Levels {
				for ni := range c.Levels[li].Nodes {
					n := &c.Levels[li].Nodes[ni]
					if n.ID == nodeID {
						var next []model.ChainNode
						if li+1 < len(c.Levels) {
							next = c.Levels[li+1].Nodes
						}
						roles = append(roles, chainRole{
							Chain: c, NodeIndex: flatIdx, Node: n,
							IsEntry:     li == 0,
							IsTransit:   li > 0 && n.Role != model.NodeRoleExit,
							HasOutbound: li+1 < len(c.Levels),
							Preset:      resolveChainPreset(c),
							LevelIndex:  li,
							NextNodes:   next,
						})
						found = true
						break
					}
					flatIdx++
				}
				if found {
					break
				}
			}
			continue
		}
		for i := range c.Nodes {
			n := &c.Nodes[i]
			if n.ID != nodeID {
				continue
			}
			isEntry := n.Role == model.NodeRoleEntry || (n.Role == "" && i == 0)
			isTransit := n.Role == model.NodeRoleTransit || (n.Role == "" && i > 0)
			var next []model.ChainNode
			if i+1 < len(c.Nodes) {
				next = []model.ChainNode{c.Nodes[i+1]}
			}
			roles = append(roles, chainRole{
				Chain: c, NodeIndex: i, Node: n,
				IsEntry: isEntry, IsTransit: isTransit,
				HasOutbound: i < len(c.Nodes)-1,
				Preset:      resolveChainPreset(c),
				LevelIndex:  -1,
				NextNodes:   next,
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

func detectPortConflicts(nodeInfo *model.NodeInfo, nodeChains []*model.Chain, roles []chainRole, report *MergeReport) error {
	type claim struct {
		port     int
		claimant string
		role     string
	}
	var claims []claim

	// Caddy mode: the utility owns the public TCP 80/443. Standalone inbounds
	// are remapped (below) so they never collide; chain transport/entry roles
	// that still sit on 443/80 collide with caddy itself and must fail loudly
	// (move the chain port — rule #8). UDP-only standalone listeners (kernel
	// AWG on UDP 443) do NOT collide with the TCP owners and are exempt.
	caddyMode := CaddyMode(nodeInfo)
	var caddyPorts map[int]int
	if caddyMode {
		caddyPorts = RemapInboundPorts(nodeInfo.Inbounds)
	}

	for _, r := range roles {
		port := r.Node.Port
		if r.IsEntry && r.Node.InboundRef != "" {
			if ib := inboundByProfileID(nodeInfo, r.Node.InboundRef); ib != nil && ib.Port > 0 {
				port = ib.Port
			}
		}
		if port == 0 {
			if r.IsEntry {
				port = chainEntryPort(r.Chain, r.Node.ID)
			} else {
				port = defaultTransportPort
			}
		}
		roleType := "transport-in"
		if r.IsEntry {
			roleType = "user-in"
		}
		if caddyMode && caddyOwnedPorts[port] {
			return fmt.Errorf("port %d conflict: caddy (utility) vs %s (%s) — the caddy utility owns 80/443 on this node; move the chain listener to another port", port, r.Chain.Name, roleType)
		}
		claims = append(claims, claim{port, r.Chain.Name, roleType})
	}

	for i, ib := range nodeInfo.Inbounds {
		if IsChainSourcedInbound(&ib) || IsChainEntryInbound(nodeChains, nodeInfo.ID, &ib) {
			// Chain-entry materialized inbound: its listen port is the chain
			// entry port, already claimed via the role above — claiming it
			// again here would report a phantom self-conflict.
			continue
		}
		// Claim the EFFECTIVE listen port, not the raw NodeInbound.Port. A
		// standalone MTProxy inbound with Port=0 renders on 443 (mtproxyInboundPort
		// default — MTProxy's canonical FakeTLS port); claiming 0 would bypass
		// collision detection and let it silently clash with a chain MTProxy entry
		// (or any other inbound) that also resolves to 443 on the same node. Other
		// protocols with Port=0 fall through unchanged (their renderer either
		// errors or is never bound without an explicit port).
		port := ib.Port
		if ib.Protocol == "mtproxy" {
			port = mtproxyInboundPort(ib.Port)
			if caddyMode {
				port = CaddyEvictPort(port)
			}
		} else if caddyPorts != nil {
			if p, ok := caddyPorts[i]; ok {
				port = p
			}
		}
		claims = append(claims, claim{port, "standalone", ib.Protocol})
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
			allUserIn := true
			for i, c := range group {
				parts[i] = fmt.Sprintf("%s (%s)", c.claimant, c.role)
				if c.role != "user-in" {
					allUserIn = false
				}
			}
			msg := fmt.Sprintf("port %d conflict: %s", port, strings.Join(parts, " vs "))
			// Two chain user-entries landing on the same port almost always
			// means several AWG entry chains on ONE node whose entry inbounds
			// were never deployed (so both fell back to the shared default
			// port). The kernel-AWG design hosts a single awg0 user-entry per
			// node — point each chain at its own deployed inbound (or node).
			if allUserIn && len(group) > 1 {
				msg += " — several user-entry chains share this node and none of their inbounds is deployed here (both fell back to the default entry port). Deploy a distinct inbound per chain on the Inbounds page, or put the chains on different nodes; a node hosts one kernel-AWG (awg0) user-entry."
			}
			return fmt.Errorf("%s", msg)
		}
	}
	return nil
}

// usersForChain returns the users assigned to a chain from the usersByChain
// map, or nil when the map has no entry for that chain (single-user fallback).
func usersForChain(usersByChain map[string][]model.User, chainName string) []model.User {
	if usersByChain == nil {
		return nil
	}
	return usersByChain[chainName]
}

// hasAWGPeerUsers reports whether at least one user in the slice is active and
// carries per-user AWG creds (AWGPublicKey + AWGAddress) — i.e. can become a
// WireGuard peer. Used to decide multi-peer vs legacy single-peer standalone AWG.
func hasAWGPeerUsers(users []model.User) bool {
	for _, u := range users {
		if u.Active && u.AWGPublicKey != "" && u.AWGAddress != "" {
			return true
		}
	}
	return false
}

// nodeIsAtOrBefore reports whether the pinned exit node (exitID) sits strictly
// downstream of role's node in the chain — i.e. role's node is BEFORE the
// pinned exit and must forward traffic down the chain to reach it. Used by AWG
// source-IP routing: a hop before the pinned exit forwards down the chain; the
// pinned exit itself direct-outs. Returns false when exitID is this node, is
// upstream, or is not found (handled by other branches).
func nodeIsAtOrBefore(role chainRole, exitID string) bool {
	c := role.Chain
	for i, n := range c.Nodes {
		if n.ID == exitID {
			return i > role.NodeIndex
		}
	}
	return false
}

// userAuthIdentity returns the sing-box inbound identity for auth_user route
// matching. VLESS inbounds match by the user's Name; TUIC matches by UUID
// (TUICUser has no Name field, so the UUID is the identity). Hysteria2 matches
// by Name (Hysteria2User has a Name field). Returns "" when the user has no
// per-user cred for the protocol (cannot be matched by auth_user — skip).
func userAuthIdentity(u model.User, proto model.UserProtocol) string {
	switch proto {
	case model.UserProtocolTUIC:
		return u.TUICUUID
	case model.UserProtocolVLESSReality:
		if u.VLESSUUID == "" {
			return ""
		}
		return u.Name // VLESS users[] carry Name + UUID; auth_user matches Name
	default:
		return u.Name
	}
}

func buildChainRoleInOut(role *chainRole, users []model.User, nodeInfo *model.NodeInfo) (inbounds, outbounds, endpoints []json.RawMessage, warnings []string) {
	c := role.Chain
	cn := c.Name
	p := ensureHopParams(role)

	if role.IsEntry {
		userPort := chainEntryPort(c, role.Node.ID)
		inTag := chainUserInboundTag(c, role.Node.ID)
		switch c.UserProtocol {
		case model.UserProtocolAWG:
			// Kernel-AWG architecture (default): the user-entry awg0 interface
			// is owned by the kernel (awg-quick@awg0), not a sing-box userspace
			// endpoint — userspace wireguard-go panics with chacha20poly1305
			// under AmneziaWG obfuscation. sing-box captures awg0 traffic via a
			// TUN overlay (include_interface:["awg0"]) emitted once at the node
			// level by buildMergedNodeConfig (see awgTUNOverlayNeeded). The
			// per-user peers live in the separately-pushed awg0.conf
			// (RenderServerAWGConf), not in the sing-box config. So nothing is
			// emitted here — UNLESS the materialized entry inbound has AWG 3.0
			// selected (AGENTS #5, revision): AWG3 fields (header protection key /
			// content padding / rekey-after-time) are rendered via the userspace
			// `type:"awg"` endpoint path with the per-user peers inline (one peer
			// per user, like buildAWGUserInboundMulti) in slice 1. The TUN overlay
			// + awg0.conf are skipped for this inbound (see awgTUNOverlayNeeded /
			// RenderNodeAWGConfs AWG3 branches). Slice 2 will add a kernel-render
			// path now that the amnezia-box kernel module gained native HPK
			// (PR #192, 2026-07-30).
			if awg3Entry := chainEntryAWG3Inbound(nodeInfo, c, role.Node); awg3Entry != nil && !kernelAWG3EnabledFor(nodeInfo) {
				// Kernel-AWG3 not available on this node → render the userspace
				// `type:"awg"` endpoint fallback. When KernelAWG3Supported is
				// true, the kernel-render path (RenderNodeAWGConfs → awg0.conf
				// with HPK) takes over and this endpoint is NOT emitted.
				//
				// Port/subnet/preset come from the MATERIALIZED inbound, not the
				// chain's own fields: the client .conf renders ib.Port and the
				// kernel renderer uses ib.Port too, so an endpoint on
				// chainEntryPort() would listen where no client dials (live bug:
				// server 8443 vs client 25086, PROGRESS §39). The preset is
				// resolved through the SHARED resolver so the server's amnezia
				// block matches the client's byte-for-byte.
				entryPort := userPort
				if awg3Entry.Port > 0 {
					entryPort = awg3Entry.Port
				}
				entryPreset := ResolveChainEntryPreset(role.Preset, awg3Entry)
				epJSON, _, err := buildAWGUserInboundMultiAddr(
					entryPort, inTag, &entryPreset, awg3Entry.ServerPrivKey, users,
					InboundAWGObfsMaterial(awg3Entry), awg3Entry.AWGServerAddress)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf(
						"chain %q: AWG3 user-entry endpoint build failed: %v", cn, err))
				} else {
					endpoints = append(endpoints, epJSON)
				}
			}
			_ = userPort
			_ = inTag

		case model.UserProtocolTUIC:
			tuicUsers, err := chainTUICUsers(c, users)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"chain %q: failed to generate TUIC entry password: %v", cn, err))
				break
			}
			inb := buildTUICInboundWithUsers(userPort, tuicUsers, inTag, &role.Preset, p)
			inbounds = append(inbounds, inb)

		case model.UserProtocolMTProxy:
			// MTProxy chain entry: the mtproxy inbound is emitted at the node
			// level by buildMergedNodeConfig (which has the node's MtproxyUser
			// list), not here — buildChainRoleInOut has no access to the MTProxy
			// users. No-op case just prevents the default VLESS fallthrough.
			_ = userPort
			_ = inTag

		default:
			inb := buildUserInboundMulti(userPort, p.UUID, inTag, users)
			inbounds = append(inbounds, inb)
		}
	}

	if role.IsTransit {
		tag := fmt.Sprintf("ch-%s-transport-in", cn)
		switch c.Transport {
		case model.TransportXHTTP:
			inbounds = append(inbounds, buildXHTTPTransportInbound(p, tag, &role.Preset))
		case model.TransportAWG:
			// AWG transit is a WireGuard ENDPOINT (not a VLESS inbound); route
			// rules still match it by the transport-in tag. The previous node
			// (i-1) is this endpoint's single peer.
			var prev *model.ChainNode
			if role.NodeIndex > 0 {
				prev = &c.Nodes[role.NodeIndex-1]
			}
			endpoints = append(endpoints, buildAWGTransportInbound(role.Node, prev, tag, &role.Preset, ChainAWGObfsMaterial(c)))
		case model.TransportHysteria2:
			// Hysteria2 inter-node transport is FROZEN (AGENTS.md Known Issues
			// #11 — like TUIC). There is no builder, so refuse loudly instead of
			// silently falling through to Reality (a silent Reality fallback
			// would misconfigure the chain: the operator chose Hysteria2 but
			// got Reality, with mismatched keys/params). The error propagates up
			// through buildMergedNodeConfig and fails the deploy with a clear
			// message rather than shipping a wrong config.
			warnings = append(warnings, fmt.Sprintf(
				"chain %q: Hysteria2 transport is not implemented (frozen, see AGENTS.md #11); use AWG/XHTTP/Reality", cn))
		default: // Reality
			inbounds = append(inbounds, buildTransportInbound(p, tag))
		}
	}

	if role.HasOutbound {
		// Downstream group: for v2 chains the whole next level; for legacy
		// flat chains the single next node. Each target gets its own outbound
		// (from its persisted transit material); a multi-target group is
		// wrapped in a strategy group outbound (default: fallback round-robin).
		nextNodes := role.NextNodes
		if len(nextNodes) == 0 && role.NodeIndex+1 < len(c.Nodes) {
			nextNodes = []model.ChainNode{c.Nodes[role.NodeIndex+1]}
		}
		group := len(nextNodes) > 1
		var memberTags []string
		for _, next := range nextNodes {
			next := next
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
				np.ServerName = EffectiveDefaultSNI()
			}

			var outTag string
			var outb json.RawMessage
			var err error
			isAWGOut := false
			switch c.Transport {
			case model.TransportXHTTP:
				outTag = transportOutTag(cn, np.ServerName, next.ID, group)
				outb, err = buildXHTTPTransportOutbound(np, extractHost(next.Addr), outTag, &role.Preset)
			case model.TransportAWG:
				// AWG has no SNI; label the outbound by the next node's ID. The
				// client side is a WireGuard endpoint (sing-box-extended 1.13 has
				// no wireguard outbound), so it goes into endpoints[], not
				// outbounds[] — route rules still reference it by tag.
				outTag = fmt.Sprintf("ch-%s-out-awg-%s", cn, safeSNILabel(next.ID))
				outb, err = buildAWGTransportOutbound(role.Node, &next, extractHost(next.Addr), outTag, &role.Preset, ChainAWGObfsMaterial(c))
				isAWGOut = true
			case model.TransportHysteria2:
				// Hysteria2 inter-node transport is FROZEN (AGENTS.md #11). Refuse
				// loudly — no outbound emitted, a warning recorded so the operator
				// sees the chain chose Hysteria2 but got nothing (rather than a
				// silent Reality fallback with mismatched keys/params).
				warnings = append(warnings, fmt.Sprintf(
					"chain %q: Hysteria2 transport outbound is not implemented (frozen, see AGENTS.md #11); use AWG/XHTTP/Reality", cn))
			default: // Reality
				outTag = transportOutTag(cn, np.ServerName, next.ID, group)
				outb, err = buildTransportOutbound(np, extractHost(next.Addr), outTag)
			}
			if err == nil && outb != nil {
				if isAWGOut {
					endpoints = append(endpoints, outb)
				} else {
					outbounds = append(outbounds, outb)
					memberTags = append(memberTags, outTag)
				}
			}
		}
		if len(memberTags) > 1 {
			// Multi-node downstream level: wrap the per-target outbounds in the
			// level's strategy group (default fallback = round-robin, the
			// production-verified path). Linear single-target chains keep the
			// bare outbound — wrapping a single hop in urltest probes gstatic
			// through the hop and returns EOF while transit is still failing,
			// which breaks routing and masks the real error.
			grp, err := buildStrategyGroupOutbound(
				effectiveGroupStrategy(role.Chain.LevelStrategy(role.Node.ID)),
				levelGroupTag(cn, role.LevelIndex+1), memberTags)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("chain %q: %v", cn, err))
			} else {
				outbounds = append(outbounds, grp)
			}
		}
	}

	return
}

// transportOutTag builds the inter-node outbound tag. Single-target chains
// keep the legacy SNI-derived tag (existing route rules/tests match it); a
// multi-target group suffixes the target node ID so each member is unique.
func transportOutTag(chainName, serverName, targetID string, group bool) string {
	base := fmt.Sprintf("ch-%s-out-%s", chainName, safeSNILabel(serverName))
	if group {
		return fmt.Sprintf("%s-%s", base, safeSNILabel(targetID))
	}
	return base
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
		p.ServerName = EffectiveDefaultSNI()
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
	return EffectiveDefaultSNI()
}

func buildStandaloneInOut(ib *model.NodeInbound, tag string, usersByInbound map[string][]model.User, nodeInfo *model.NodeInfo, index int, caddyPorts map[int]int) (inbounds, endpoints []json.RawMessage) {
	preset := GetDefaultPreset()
	if ib.Obfuscation != "" {
		if p, ok := GetPreset(ib.Obfuscation); ok {
			preset = p
		}
	}
	serverName := ResolveServerName(&preset)
	if ib.ServerName != "" {
		serverName = ib.ServerName
	}

	// Caddy-mode listen: only inbounds that caddy actually FRONTS move to
	// loopback behind the SNI router — those on caddy-owned ports (remapped)
	// and the TLS-utility protocols (naive/trusttunnel, always SNI-routed).
	// Everything else keeps its direct public listener so it stays reachable.
	// nil caddyPorts = legacy direct-listen mode.
	listen := "0.0.0.0"
	port := ib.Port
	if caddyPorts != nil {
		fronted := caddyOwnedPorts[ib.Port] || TLSUtilityProtocols[ib.Protocol]
		if fronted {
			listen = "127.0.0.1"
			if p, ok := caddyPorts[index]; ok {
				port = p
			}
		}
	}

	switch ib.Protocol {
	case "vless-reality":
		// Shared inbound UUID first (keeps pre-multi-user clients working),
		// then one entry per credentialed user (per-client access).
		vusers := []config.VLESSUser{{Name: "user", UUID: ib.UUID, Flow: "xtls-rprx-vision"}}
		seen := map[string]bool{ib.UUID: true}
		for _, u := range usersByInbound[tag] {
			if !u.Active || u.VLESSUUID == "" || seen[u.VLESSUUID] {
				continue
			}
			seen[u.VLESSUUID] = true
			vusers = append(vusers, config.VLESSUser{Name: u.Name, UUID: u.VLESSUUID, Flow: "xtls-rprx-vision"})
		}
		inb := config.VLESSInbound{
			Type: "vless", Tag: tag, Listen: listen, ListenPort: port,
			Users: vusers,
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
		// Kernel-AWG architecture (default): the standalone AWG server interface
		// (awg0) is owned by the kernel (awg-quick@awg0), not a sing-box
		// userspace endpoint (userspace wireguard-go panics with
		// chacha20poly1305 under AmneziaWG obfuscation). sing-box captures awg0
		// traffic via a TUN overlay (include_interface:["awg0"]) emitted once at
		// the node level by buildMergedNodeConfig (see awgTUNOverlayNeeded).
		// The per-user peers live in the separately-pushed awg0.conf
		// (RenderServerAWGConf), not in the sing-box config. Nothing is emitted
		// here — UNLESS this inbound has AWG 3.0 selected (AGENTS #5, revision):
		// AWG3 fields are rendered via the userspace `type:"awg"` endpoint path
		// with the per-user peers inline (slice 1). The TUN overlay + awg0.conf
		// are skipped for this inbound (awgTUNOverlayNeeded /
		// RenderNodeAWGConfs). Slice 2 adds a kernel-render path now that the
		// amnezia-box kernel module has native HPK (PR #192, 2026-07-30).
		// AWG 3.0 (AGENTS #5 revision). When the node's kernel module + tools
		// support header protection (KernelAWG3Supported, probed at deploy),
		// the v3 inbound renders via the kernel awg-quick +
		// sing-box-TUN-overlay path — RenderNodeAWGConfs emits awg0.conf with
		// HPK/CPM/RAT, and awgTUNOverlayNeeded enables the overlay — so NO
		// userspace endpoint is emitted here. Only fall back to the userspace
		// `type:"awg"` endpoint (the v0.8.10 path) when the kernel can't.
		if model.IsAWG3Family(ib.EffectiveAWGVersion()) && !kernelAWG3EnabledFor(nodeInfo) {
			users := usersByInbound[tag]
			// The endpoint's own tunnel address must sit in the inbound's subnet
			// (AWGServerAddress), not the hardcoded 10.8.0.1/32 — otherwise the
			// server and its peers land on different /24s (PROGRESS §39).
			epJSON, _, err := buildAWGUserInboundMultiAddr(
				port, tag, &preset, ib.ServerPrivKey, users,
				InboundAWGObfsMaterial(ib), ib.AWGServerAddress)
			if err == nil {
				endpoints = append(endpoints, epJSON)
			}
		}
		_ = preset
		_ = serverName

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
			Listen:            listen,
			ListenPort:        port,
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
			Type: "vless", Tag: tag, Listen: listen, ListenPort: port,
			Users: []config.VLESSUser{{Name: "user", UUID: ib.UUID}},
			Transport: &config.TransportOptions{
				Type: "http", Path: "/api", Method: "POST",
				Headers: map[string][]string{"Content-Type": {"application/json"}},
			},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "hysteria2":
		hListen := "::"
		if caddyPorts != nil {
			hListen = listen
		}
		inb := config.Hysteria2Inbound{
			Type: "hysteria2", Tag: tag, Listen: hListen, ListenPort: port,
			Users:  []config.Hysteria2User{{Password: ib.UUID}},
			UpMbps: 1000, DownMbps: 1000,
			Obfs: &config.Hysteria2Obfs{Type: "salamander", Password: ib.ObfsPassword},
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "naive":
		tls := &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
			ALPN:       []string{"h2"},
		}
		if caddyPorts != nil {
			// Caddy mode: serve the node's acme SAN cert by path (it covers
			// this inbound's SNI subdomain). No server_name — the subdomain
			// varies per inbound and the cert is selected by the file.
			tls.ServerName = ""
			tls.CertificatePath, tls.KeyPath = CertPaths(nodeInfo.TLSDomain)
		} else {
			cert, key := ib.TLSCertificate, ib.TLSPrivateKey
			if cert == "" || key == "" {
				if c, k, err := GenerateSelfSignedCert(serverName); err == nil {
					cert, key = c, k
					ib.TLSCertificate = c
					ib.TLSPrivateKey = k
				}
			}
			if cert != "" && key != "" {
				tls.Certificate = cert
				tls.Key = key
			}
		}
		var nusers []config.NaiveUser
		for _, u := range usersByInbound[tag] {
			if !u.Active || u.NaiveUsername == "" || u.NaivePassword == "" {
				continue
			}
			nusers = append(nusers, config.NaiveUser{Username: u.NaiveUsername, Password: u.NaivePassword})
		}
		inb := config.NaiveInbound{
			Type: "naive", Tag: tag, Listen: listen, ListenPort: port,
			Users: nusers, Network: "tcp", TLS: tls,
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "trusttunnel":
		tls := &config.InboundTLSOptions{
			Enabled:    true,
			ServerName: serverName,
			ALPN:       []string{"h2"},
		}
		if caddyPorts != nil {
			tls.ServerName = ""
			tls.CertificatePath, tls.KeyPath = CertPaths(nodeInfo.TLSDomain)
		} else {
			cert, key := ib.TLSCertificate, ib.TLSPrivateKey
			if cert == "" || key == "" {
				if c, k, err := GenerateSelfSignedCert(serverName); err == nil {
					cert, key = c, k
					ib.TLSCertificate = c
					ib.TLSPrivateKey = k
				}
			}
			if cert != "" && key != "" {
				tls.Certificate = cert
				tls.Key = key
			}
		}
		var tusers []config.TrustTunnelUser
		for _, u := range usersByInbound[tag] {
			if !u.Active || u.TrustTunnelUsername == "" || u.TrustTunnelPassword == "" {
				continue
			}
			tusers = append(tusers, config.TrustTunnelUser{Name: u.TrustTunnelUsername, Password: u.TrustTunnelPassword})
		}
		inb := config.TrustTunnelInbound{
			Type: "trusttunnel", Tag: tag, Listen: listen, ListenPort: port,
			Users: tusers, Network: "tcp", TLS: tls,
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "mieru":
		transport := ib.MieruTransport
		if transport == "" {
			transport = "TCP"
		}
		var musers []config.MieruUser
		for _, u := range usersByInbound[tag] {
			if !u.Active || u.MieruUsername == "" || u.MieruPassword == "" {
				continue
			}
			musers = append(musers, config.MieruUser{Name: u.MieruUsername, Password: u.MieruPassword})
		}
		inb := config.MieruInbound{
			Type: "mieru", Tag: tag, Listen: listen, ListenPort: port,
			Users: musers, Transport: transport,
		}
		data, _ := json.Marshal(inb)
		inbounds = append(inbounds, data)

	case "mtproxy":
		// MTProxy inbounds are emitted at the node level by buildMergedNodeConfig
		// (which has the node's MtproxyUser list from the store), not here —
		// buildStandaloneInOut has no access to the MTProxy users. This no-op
		// case just prevents the default VLESS/WS fallthrough. The actual
		// mtproxy inbound is built by buildMTProxyInbound in the node-level loop.
		_ = preset
		_ = serverName

	default:
		inb := config.VLESSInbound{
			Type: "vless", Tag: tag, Listen: listen, ListenPort: port,
			Users:     []config.VLESSUser{{Name: "user", UUID: ib.UUID, Flow: "xtls-rprx-vision"}},
			TLS:       &config.InboundTLSOptions{Enabled: false},
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
	var m struct {
		Tag string `json:"tag"`
	}
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
// Returns an error if the generator fails instead of panicking in the deploy
// path (CTO-review #3: no panics in the request/deploy path).
func tuicPassword(c *model.Chain) (string, error) {
	if c.TUICEntryUserPassword != "" {
		return c.TUICEntryUserPassword, nil
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

// chainEntryNodes returns the chain's entry nodes. For v2 (levelized) chains
// that is simply level 0. For legacy flat chains: nodes with an explicit
// Role=entry, falling back to index 0 when no node carries an explicit role
// (backward compat: a legacy chain has one entry at index 0).
func chainEntryNodes(c *model.Chain) []*model.ChainNode {
	if c.IsLevelized() {
		var entries []*model.ChainNode
		for i := range c.Levels[0].Nodes {
			entries = append(entries, &c.Levels[0].Nodes[i])
		}
		return entries
	}
	var entries []*model.ChainNode
	for i := range c.Nodes {
		n := &c.Nodes[i]
		if n.Role == model.NodeRoleEntry {
			entries = append(entries, n)
		}
	}
	if len(entries) == 0 && len(c.Nodes) > 0 {
		entries = []*model.ChainNode{&c.Nodes[0]}
	}
	return entries
}

// chainEntryIndex is the 0-based ordinal of nodeID among the chain's entry
// nodes (0 for the first entry). Used to assign distinct ports when a chain
// has multiple entry nodes: the Nth entry listens on chainUserPort(c)+N so
// the entries don't collide. Returns 0 for a single-entry chain.
func chainEntryIndex(c *model.Chain, nodeID string) int {
	entries := chainEntryNodes(c)
	for i, n := range entries {
		if n.ID == nodeID {
			return i
		}
	}
	return 0
}

// chainEntryPort returns the listen port for a specific entry node. The first
// entry uses the chain's base user-entry port; subsequent entries increment by
// one to avoid port collisions on the same host (only relevant when multiple
// entry nodes happen to share a host, which detectPortConflicts still flags).
func chainEntryPort(c *model.Chain, nodeID string) int {
	return chainUserPort(c) + chainEntryIndex(c, nodeID)
}

// chainUserInboundTag is the inbound tag for a chain's user-entry inbound on a
// given node. Single-entry chains keep the legacy tag "ch-<chain>-user-in" so
// existing route rules and tests continue to match; multi-entry chains suffix
// the node ID so each entry's inbound is uniquely addressable for routing.
func chainUserInboundTag(c *model.Chain, nodeID string) string {
	if len(chainEntryNodes(c)) > 1 {
		return fmt.Sprintf("ch-%s-user-in-%s", c.Name, nodeID)
	}
	return fmt.Sprintf("ch-%s-user-in", c.Name)
}

// chainInterNodeOutboundTag is the outbound tag route rules steer traffic to
// for the node's downstream hop. With a multi-node downstream level it is the
// strategy GROUP tag (levelGroupTag); single-target chains keep the legacy
// per-hop tag so existing route rules and tests continue to match. For
// Reality/XHTTP the tag is derived from the SNI; for AWG transport (no SNI)
// from the next node's ID.
func chainInterNodeOutboundTag(role *chainRole) string {
	if len(role.NextNodes) > 1 {
		return levelGroupTag(role.Chain.Name, role.LevelIndex+1)
	}
	if role.Chain.Transport == model.TransportAWG {
		nextID := ""
		if role.HasOutbound && role.NodeIndex+1 < len(role.Chain.Nodes) {
			nextID = role.Chain.Nodes[role.NodeIndex+1].ID
		}
		return fmt.Sprintf("ch-%s-out-awg-%s", role.Chain.Name, safeSNILabel(nextID))
	}
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
