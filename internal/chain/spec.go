package chain

import "encoding/json"

// EntrySpec defines protocol parameters for an entry node's inbound.
// Stored as JSON in ChainNode.InboundSpec.
type EntrySpec struct {
	ClientID   string `json:"client_id"`            // VLESS UUID (generated if empty)
	Security   string `json:"security,omitempty"`   // "reality" or "tls" (default: "reality")
	RealityKey string `json:"reality_key,omitempty"` // Reality private key
	ServerName string `json:"server_name,omitempty"` // TLS/Reality camouflage host (default: "discord.com")
	Password   string `json:"password,omitempty"`    // Trojan password
}

// HopSpec defines protocol parameters for a hop or exit node.
// Stored as JSON in ChainNode.InboundSpec.
type HopSpec struct {
	ClientID string `json:"client_id"` // VLESS UUID
}

// OutboundSpec defines protocol parameters for an outbound to the next hop.
// Stored as JSON in ChainNode.OutboundSpec.
type OutboundSpec struct {
	Address string `json:"address,omitempty"` // override next server address
	Port    int    `json:"port,omitempty"`    // override next server port
}

// DefaultEntrySpec returns sensible defaults for an entry node.
func DefaultEntrySpec() EntrySpec {
	return EntrySpec{
		Security:   "reality",
		ServerName: "discord.com",
	}
}

// ParseEntrySpec parses an EntrySpec from a JSON string.
func ParseEntrySpec(raw string) (EntrySpec, error) {
	var s EntrySpec
	if raw == "" || raw == "{}" {
		return DefaultEntrySpec(), nil
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return DefaultEntrySpec(), err
	}
	if s.Security == "" {
		s.Security = "reality"
	}
	if s.ServerName == "" {
		s.ServerName = "discord.com"
	}
	return s, nil
}

// ParseHopSpec parses a HopSpec from a JSON string.
func ParseHopSpec(raw string) (HopSpec, error) {
	var s HopSpec
	if raw == "" || raw == "{}" {
		return s, nil
	}
	err := json.Unmarshal([]byte(raw), &s)
	return s, err
}

// MustJSON marshals a value to a JSON string, panicking on error.
func MustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
