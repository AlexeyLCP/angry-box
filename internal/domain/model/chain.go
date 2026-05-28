package model

// Strategy defines how traffic is distributed across chain nodes.
type Strategy string

const (
	StrategyURLTest  Strategy = "urltest"
	StrategyFailover Strategy = "failover"
	StrategySelector Strategy = "selector"
	StrategyBond     Strategy = "bond"
)

// ChainNode is a single hop in a proxy chain.
type ChainNode struct {
	ID      string `json:"id"`      // user-provided name for this node
	Addr    string `json:"addr"`    // SSH address (IP:port)
	User    string `json:"user"`    // SSH user
	KeyPath string `json:"keyPath"` // path to SSH private key
	Port    int    `json:"port"`    // inbound port for transport on this node
}

// Chain is an ordered list of nodes forming a multi-hop proxy path.
type Chain struct {
	Name               string        `json:"name"`
	Nodes              []ChainNode   `json:"nodes"`
	Strategy           Strategy      `json:"strategy"`
	Transport          TransportType `json:"transport,omitempty"`           // transport between nodes (xhttp/reality)
	UserProtocol       UserProtocol  `json:"user_protocol,omitempty"`       // user entry protocol (tuic/awg/vless-reality)
	ObfuscationProfile string        `json:"obfuscation_profile,omitempty"` // optional explicit profile override (e.g. "china_2026")

	// Stable user-entry credentials (generated once at chain creation for AWG/TUIC).
	// These must remain stable across applies so that client configs do not break.
	// Only rotated explicitly via "rotate entry creds" operation.
	AWGEntryServerPriv string `json:"awg_entry_server_priv,omitempty"`
	AWGEntryServerPub  string `json:"awg_entry_server_pub,omitempty"`

	TUICEntryUserUUID     string `json:"tuic_entry_user_uuid,omitempty"`
	TUICEntryUserPassword string `json:"tuic_entry_user_password,omitempty"`
}

// UserProtocol for the client-facing entry point.
type UserProtocol string

const (
	UserProtocolVLESSReality UserProtocol = "vless-reality"
	UserProtocolTUIC         UserProtocol = "tuic"
	UserProtocolAWG          UserProtocol = "awg" // AmneziaWG
)

// TransportType for inter-node links.
type TransportType string

const (
	TransportReality TransportType = "reality"
	TransportXHTTP   TransportType = "xhttp"
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
