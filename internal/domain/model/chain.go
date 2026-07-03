package model

// Strategy defines how traffic is distributed across chain nodes.
type Strategy string

const (
	StrategyURLTest  Strategy = "urltest"
	StrategyFailover Strategy = "failover"
	StrategySelector Strategy = "selector"
	StrategyBond     Strategy = "bond"
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
)

// ChainNode is a single hop in a proxy chain.
type ChainNode struct {
	ID      string `json:"id"`      // user-provided name for this node
	Addr    string `json:"addr"`    // SSH address (IP:port)
	User    string `json:"user"`    // SSH user
	KeyPath string `json:"keyPath"` // path to SSH private key
	Port    int    `json:"port"`    // inbound port for transport on this node
	Role    ChainNodeRole `json:"role,omitempty"` // explicit entry/transit designation; empty = auto

	TransitPrivKey string        `json:"transit_priv_key,omitempty"` // For Reality
	TransitShortID string        `json:"transit_short_id,omitempty"` // For Reality
	TransitUUID    string        `json:"transit_uuid,omitempty"`     // Shared UUID for auth

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

	Inbounds []NodeInbound `json:"inbounds,omitempty"` // Standalone inbounds configured for this node
}

// Chain is an ordered list of nodes forming a multi-hop proxy path.
type Chain struct {
	Name               string        `json:"name"`
	Nodes              []ChainNode   `json:"nodes"`
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
	AWGCPSI1      string `json:"awg_cps_i1,omitempty"`
	AWGCPSI2      string `json:"awg_cps_i2,omitempty"`
	AWGCPSI3      string `json:"awg_cps_i3,omitempty"`
	AWGCPSI4      string `json:"awg_cps_i4,omitempty"`
	AWGCPSI5      string `json:"awg_cps_i5,omitempty"`

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
