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
