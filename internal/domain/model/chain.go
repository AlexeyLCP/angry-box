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
	Name     string      `json:"name"`
	Nodes    []ChainNode `json:"nodes"`
	Strategy Strategy    `json:"strategy"`
}

// Host converts a ChainNode to a Host for SSH operations.
func (n ChainNode) Host() Host {
	return Host{
		ID:      n.ID,
		Addr:    n.Addr,
		User:    n.User,
		KeyPath: n.KeyPath,
	}
}
