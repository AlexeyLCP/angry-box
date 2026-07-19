package model

// Strategy defines how traffic is distributed across chain nodes.
type Strategy string

const (
	StrategyURLTest  Strategy = "urltest"
	StrategyFailover Strategy = "failover"
	StrategySelector Strategy = "selector"
	StrategyBond     Strategy = "bond"
	// StrategyFallback is the per-connection round-robin group rendered as the
	// patched sing-box-extended "fallback" outbound (the production-verified
	// multi-exit path). It is the DEFAULT strategy for multi-node chain
	// levels — urltest is an explicit opt-in only. UI label: "Round-robin
	// (fallback)".
	StrategyFallback Strategy = "fallback"
)

// ChainNodeRole explicitly designates a node's role in a chain. Empty means
// "auto": the entry (user-facing) node is index 0, all others are transit.
// Setting Role=entry on several nodes enables multi-entry load balancing
// (several user-facing entries sharing one chain). Backward compatible —
// existing store.json with empty roles behaves as before.
type ChainNodeRole string

const (
	NodeRoleEntry   ChainNodeRole = "entry"
	NodeRoleTransit ChainNodeRole = "transit"
	NodeRoleExit    ChainNodeRole = "exit"
)

// AWGExitLink is a balancer-side kernel AWG client tunnel to one exit target.
// A node with ExitTargets owns one awg-exit-nX interface per target; each link
// points at a ChainNode with Role=exit (which owns the matching server keypair).
type AWGExitLink struct {
	TargetID      string `json:"target_id"`
	InterfaceName string `json:"interface_name,omitempty"` // awg-exit-n1, awg-exit-n2, ...
	ClientPriv    string `json:"client_priv,omitempty"`
	ClientPub     string `json:"client_pub,omitempty"`
	Address       string `json:"address,omitempty"`     // balancer inner IP, 10.10.0.X/32
	ClientPort    int    `json:"client_port,omitempty"` // fixed source/listen port for awg-exit-nX
}

// ChainNode is a single hop in a proxy chain.
type ChainNode struct {
	ID      string        `json:"id"`             // user-provided name for this node
	Addr    string        `json:"addr"`           // SSH address (IP:port)
	User    string        `json:"user"`           // SSH user
	KeyPath string        `json:"keyPath"`        // path to SSH private key
	Port    int           `json:"port"`           // inbound port for transport on this node
	Role    ChainNodeRole `json:"role,omitempty"` // explicit entry/transit designation; empty = auto

	TransitPrivKey string `json:"transit_priv_key,omitempty"` // For Reality
	TransitShortID string `json:"transit_short_id,omitempty"` // For Reality
	TransitUUID    string `json:"transit_uuid,omitempty"`     // Shared UUID for auth

	// Inter-node AWG transport keys (chain.Transport == "awg"). One WireGuard
	// link per hop: the transit node listens (server keypair), the previous
	// node dials (client keypair). Generated once in ApplyChain and persisted
	// (Rule 5 — stable across redeploys so links don't break). The inner tunnel
	// subnet is 10.9.0.0/24 (kept separate from the user-entry 10.8.0.0/24).
	TransitAWGServerPriv string `json:"transit_awg_server_priv,omitempty"` // this node's WG server priv (transit inbound)
	TransitAWGServerPub  string `json:"transit_awg_server_pub,omitempty"`  // derived/persisted server pub
	TransitAWGClientPriv string `json:"transit_awg_client_priv,omitempty"` // this node's WG client priv (outbound to next hop)
	TransitAWGClientPub  string `json:"transit_awg_client_pub,omitempty"`  // derived/persisted client pub
	TransitAWGAddress    string `json:"transit_awg_address,omitempty"`     // this node's client inner tunnel IP, 10.9.0.X/32
	// TransitAWGClientPort is the FIXED source port this node's AWG transport
	// CLIENT endpoint binds. Without it sing-box picks a random ephemeral port,
	// which breaks on NAT'd VPSes (GCloud): the peer's handshake response is sent
	// to a port that no longer maps to this endpoint after a re-handshake retry.
	// 0 = assign a deterministic port in ApplyChain (51820 + nodeIndex + 1).
	TransitAWGClientPort int `json:"transit_awg_client_port,omitempty"`

	// Multi-exit AWG balancer topology (NodeRoleEntry/Transit with ExitTargets).
	// ExitTargets lists chain node IDs with Role=exit. The balancer node builds
	// one kernel AWG client interface per target (awg-exit-nX), and sing-box
	// routes TUN traffic across those interfaces via a fallback group. Each exit
	// node listens with its own kernel awg0 server keypair. This is independent
	// from the linear Chain.Nodes next-hop path, preserving existing multihop.
	ExitTargets []string `json:"exit_targets,omitempty"`

	// Kernel AWG exit-link material for the balancer-side client interfaces
	// (awg-exit-nX). One link per ExitTargets entry. Generated once and
	// persisted (Rule 5) so exit tunnels stay stable across redeploys.
	ExitAWGLinks []AWGExitLink `json:"exit_awg_links,omitempty"`

	// Kernel AWG exit server material for nodes with Role=exit. The exit node
	// accepts the balancer's awg-exit-nX peer on awg0.
	ExitAWGServerPriv string `json:"exit_awg_server_priv,omitempty"`
	ExitAWGServerPub  string `json:"exit_awg_server_pub,omitempty"`
	ExitAWGListenPort int    `json:"exit_awg_listen_port,omitempty"`

	// InboundRef references an InboundProfile.ID materialized on this node.
	// On the chain's entry level it selects the user-facing listener clients
	// connect to (required for v2 chains). On transit/exit levels it
	// parametrizes the chain-generated transport listener (protocol, port,
	// obfuscation preset taken from the profile; credentials stay the chain's
	// own persisted transit keys) — optional, the validation and render paths
	// honor it from v2 onward.
	InboundRef string `json:"inbound_ref,omitempty"`

	Inbounds []NodeInbound `json:"inbounds,omitempty"` // Standalone inbounds configured for this node
}

// Chain is an ordered list of nodes forming a multi-hop proxy path.
type Chain struct {
	Name  string      `json:"name"`
	Nodes []ChainNode `json:"nodes"` // legacy flat path — read by the v1→v2 migration only; use AllNodes()
	// Levels is the v2 authoring model: ordered hop levels, each a group of
	// one or more nodes with a selection Strategy toward the next level.
	// Level 0 is the user-facing entry, the last level is the exit. The flat
	// Nodes slice is derived from Levels via AllNodes() — Levels is the
	// single source of truth; nothing writes Nodes anymore.
	Levels             []ChainLevel `json:"levels,omitempty"`
	Strategy           Strategy      `json:"strategy"`
	Transport          TransportType `json:"transport,omitempty"`           // transport between nodes (xhttp/reality)
	UserProtocol       UserProtocol  `json:"user_protocol,omitempty"`       // user entry protocol (tuic/awg/vless-reality)
	ObfuscationProfile string        `json:"obfuscation_profile,omitempty"` // optional explicit profile override (e.g. "china_2026")
	// UserEntryPort overrides the default user-entry listen port (8443). 0 keeps
	// the default. Needed when the VPS firewall only opens 443/standard ports —
	// the entry can listen on 443 instead of 8443.
	UserEntryPort int `json:"user_entry_port,omitempty"`

	// Stable user-entry credentials (generated once at chain creation for AWG/TUIC).
	// These must remain stable across applies so that client configs do not break.
	// Only rotated explicitly via "rotate entry creds" operation.
	AWGEntryServerPriv string `json:"awg_entry_server_priv,omitempty"`
	AWGEntryServerPub  string `json:"awg_entry_server_pub,omitempty"`
	AWGEntryClientPub  string `json:"awg_entry_client_pub,omitempty"`

	// AWG CPS obfuscation material (I1-I5), generated ONCE and persisted so the
	// server endpoint and every client .conf render the SAME I1-I5. Without this
	// the CPS handshake breaks — the server and client would each get random,
	// mismatched I1-I5 (the AWGObfsMaterial type existed but was never wired).
	// CPSLevel/Mimicry are derived from the chain's preset; the I1-I5 strings are
	// the "<b 0x...>" form sing-box-extended / awg-quick expect. Empty = no CPS.
	AWGCPSLevel   int    `json:"awg_cps_level,omitempty"`
	AWGCPSMimicry string `json:"awg_cps_mimicry,omitempty"`
	// AWGCPSCaptureDomain, when non-empty with AWGCPSMimicry=="quic-live", makes
	// EnsureChainAWGMaterial run a LIVE QUIC capture against this domain (dial
	// UDP 443, send a real AEAD-encrypted Initial with SNI=domain, capture the
	// server's response packets as I1-I5). Empty = use the synthesized CPS
	// packets (the default — no network needed). See awgcapture.go +
	// quic_initial_aead.go.
	AWGCPSCaptureDomain string `json:"awg_cps_capture_domain,omitempty"`
	// AWGCPSCapturedDomain records the domain the current I1-I5 were captured
	// from (live path only). A change to AWGCPSCaptureDomain invalidates the
	// cache and triggers a re-capture; unchanged, the persisted I1-I5 are kept
	// across redeploys (Rule 5 — live capture yields different packets each run,
	// so we capture ONCE per domain and persist).
	AWGCPSCapturedDomain string `json:"awg_cps_captured_domain,omitempty"`
	// AWGCPSCaptureFailedDomain records the last domain a live capture FAILED
	// against (network down, domain doesn't speak QUIC). It suppresses re-dialing
	// that same domain on every subsequent redeploy (a flaky/unreachable domain
	// would otherwise force a UDP round-trip + timeout on each ApplyChain because
	// the fallback persists AWGCPSMimicry="quic", which never matches the preset's
	// "quic-live" → cache miss → re-capture → re-fail). A change to
	// AWGCPSCaptureDomain clears this marker so a new domain is retried. Reset
	// only by a domain change or an explicit re-capture request.
	AWGCPSCaptureFailedDomain string `json:"awg_cps_capture_failed_domain,omitempty"`
	AWGCPSI1                  string `json:"awg_cps_i1,omitempty"`
	AWGCPSI2                  string `json:"awg_cps_i2,omitempty"`
	AWGCPSI3                  string `json:"awg_cps_i3,omitempty"`
	AWGCPSI4                  string `json:"awg_cps_i4,omitempty"`
	AWGCPSI5                  string `json:"awg_cps_i5,omitempty"`

	// AWG H1-H4 obfuscation header-junk ranges, generated ONCE (proper quadrant
	// ranges per the AmneziaWG manual: 4 non-overlapping ranges in [5, 2^31-1],
	// width >= 1000) and persisted so server and client render identical H
	// ranges. Without this the preset's single-int H1-H4 get emitted as
	// degenerate zero-width "N-N" ranges — header-junk randomization is off,
	// a stealth regression. Empty = fall back to the preset's degenerate ranges.
	AWGH1 string `json:"awg_h1,omitempty"`
	AWGH2 string `json:"awg_h2,omitempty"`
	AWGH3 string `json:"awg_h3,omitempty"`
	AWGH4 string `json:"awg_h4,omitempty"`

	TUICEntryUserUUID     string `json:"tuic_entry_user_uuid,omitempty"`
	TUICEntryUserPassword string `json:"tuic_entry_user_password,omitempty"`
}

// UserProtocol for the client-facing entry point.
type UserProtocol string

const (
	UserProtocolVLESSReality UserProtocol = "vless-reality"
	UserProtocolTUIC         UserProtocol = "tuic"
	UserProtocolAWG          UserProtocol = "awg"     // AmneziaWG
	UserProtocolMTProxy      UserProtocol = "mtproxy" // Telegram MTProxy FakeTLS
)

// TransportType for inter-node links.
type TransportType string

const (
	TransportReality   TransportType = "reality"
	TransportXHTTP     TransportType = "xhttp"
	TransportAWG       TransportType = "awg"
	TransportHysteria2 TransportType = "hysteria2"
)

// Host converts a ChainNode to a Host for SSH operations.
func (n ChainNode) Host() Host {
	return Host{
		ID:      n.ID,
		Addr:    n.Addr,
		User:    n.User,
		KeyPath: n.KeyPath,
	}
}

// IsLevelized reports whether the chain uses the v2 levels model. Legacy
// chains (pre-v2 store, not yet migrated) have only the flat Nodes slice.
func (c *Chain) IsLevelized() bool { return len(c.Levels) > 0 }

// AllNodes flattens the chain into the legacy ordered node list: level 0
// nodes first (entry), then each subsequent level in order. It is the SINGLE
// way to read the chain's nodes — Levels is the source of truth, the flat
// Nodes field is read only by the v1→v2 migration. For a legacy (not yet
// migrated) chain AllNodes falls back to the flat slice.
func (c *Chain) AllNodes() []ChainNode {
	if !c.IsLevelized() {
		return c.Nodes
	}
	var out []ChainNode
	for _, lv := range c.Levels {
		out = append(out, lv.Nodes...)
	}
	return out
}

// NodeByID returns the chain node with the given ID, searching levels first
// (falling back to the legacy flat list). Nil when absent.
func (c *Chain) NodeByID(id string) *ChainNode {
	if c.IsLevelized() {
		for li := range c.Levels {
			for ni := range c.Levels[li].Nodes {
				if c.Levels[li].Nodes[ni].ID == id {
					return &c.Levels[li].Nodes[ni]
				}
			}
		}
		return nil
	}
	for ni := range c.Nodes {
		if c.Nodes[ni].ID == id {
			return &c.Nodes[ni]
		}
	}
	return nil
}

// LevelIndexOf returns the level index containing nodeID, or -1. For legacy
// flat chains it derives the level from Role/position (entry nodes = 0, exit
// nodes = last, transit in order) so callers can treat old chains uniformly.
func (c *Chain) LevelIndexOf(nodeID string) int {
	if !c.IsLevelized() {
		for i, n := range c.Nodes {
			if n.ID == nodeID {
				return i
			}
		}
		return -1
	}
	for li, lv := range c.Levels {
		for _, n := range lv.Nodes {
			if n.ID == nodeID {
				return li
			}
		}
	}
	return -1
}

// NextLevelNodes returns the nodes of the level FOLLOWING the one containing
// nodeID (the node's downstream group). Empty for the last level / unknown.
func (c *Chain) NextLevelNodes(nodeID string) []ChainNode {
	li := c.LevelIndexOf(nodeID)
	if li < 0 {
		return nil
	}
	if c.IsLevelized() {
		if li+1 >= len(c.Levels) {
			return nil
		}
		return c.Levels[li+1].Nodes
	}
	if li+1 >= len(c.Nodes) {
		return nil
	}
	return []ChainNode{c.Nodes[li+1]}
}

// LevelStrategy returns the strategy the level containing nodeID uses to
// distribute traffic across its downstream group. Empty means the default
// (StrategyFallback for multi-node downstream groups).
func (c *Chain) LevelStrategy(nodeID string) Strategy {
	if !c.IsLevelized() {
		return c.Strategy
	}
	if li := c.LevelIndexOf(nodeID); li >= 0 {
		return c.Levels[li].Strategy
	}
	return ""
}

// EachNode calls fn for every node in flat order (level 0 first, then each
// subsequent level) with a MUTABLE pointer into the chain's backing store —
// the levels for v2 chains, the flat slice for legacy ones. Writers that
// generate/persist per-node material (ApplyChain key generation) MUST use
// this so the mutation lands in the levels (the v2 source of truth), not in
// a throwaway copy returned by AllNodes().
func (c *Chain) EachNode(fn func(flatIndex int, n *ChainNode)) {
	i := 0
	if c.IsLevelized() {
		for li := range c.Levels {
			for ni := range c.Levels[li].Nodes {
				fn(i, &c.Levels[li].Nodes[ni])
				i++
			}
		}
		return
	}
	for ni := range c.Nodes {
		fn(i, &c.Nodes[ni])
		i++
	}
}

// SetAllNodes writes a flat ordered node list back into the chain: for v2
// chains it redistributes the nodes into their levels in flat order (the
// slice must have come from AllNodes so lengths match); for legacy chains it
// replaces the flat Nodes slice. Used by ResolveNodes to write the
// host-resolved copies back without losing the levels structure.
func (c *Chain) SetAllNodes(nodes []ChainNode) {
	if !c.IsLevelized() {
		c.Nodes = nodes
		return
	}
	k := 0
	for li := range c.Levels {
		for ni := range c.Levels[li].Nodes {
			if k < len(nodes) {
				c.Levels[li].Nodes[ni] = nodes[k]
				k++
			}
		}
	}
}
