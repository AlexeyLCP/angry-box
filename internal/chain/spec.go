package chain

import "encoding/json"

// ── Client Inbound (entry node — users connect here) ──

// ClientInboundSpec defines how external clients connect to the entry node.
// Stored as JSON in ChainNode.InboundSpec for role="entry".
type ClientInboundSpec struct {
	ClientID    string `json:"client_id"`             // VLESS UUID (auto-generated if empty)
	Security    string `json:"security,omitempty"`    // "reality" or "tls" (default: "reality")
	RealityKey  string `json:"reality_key,omitempty"` // Reality private key
	RealityPub  string `json:"reality_pub,omitempty"` // Reality public key (for client config)
	ServerName  string `json:"server_name,omitempty"` // TLS/Reality camouflage host
	Password    string `json:"password,omitempty"`    // Trojan password
	Port        int    `json:"port,omitempty"`        // inbound port (default: 443)
	Transport   string `json:"transport,omitempty"`   // "xhttp", "ws", "grpc", "tcp"
	XHTTPHost   string `json:"xhttp_host,omitempty"`  // XHTTP host header
	XHTTPPath   string `json:"xhttp_path,omitempty"`  // XHTTP path
	XHTTPMode   string `json:"xhttp_mode,omitempty"`  // "packet-up" (client-facing)
	Fingerprint string `json:"fingerprint,omitempty"` // "chrome", "firefox", etc.
}

func DefaultClientInbound() ClientInboundSpec {
	return ClientInboundSpec{
		Security:    "reality",
		ServerName:  "discord.com",
		Port:        443,
		Transport:   "xhttp",
		XHTTPHost:   "discord.com",
		XHTTPPath:   "/download",
		XHTTPMode:   "packet-up",
		Fingerprint: "chrome",
	}
}

func ParseClientInbound(raw string) (ClientInboundSpec, error) {
	var s ClientInboundSpec
	if raw == "" || raw == "{}" {
		return DefaultClientInbound(), nil
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return DefaultClientInbound(), err
	}
	if s.Security == "" {
		s.Security = "reality"
	}
	if s.ServerName == "" {
		s.ServerName = "discord.com"
	}
	if s.Port == 0 {
		s.Port = 443
	}
	if s.Transport == "" {
		s.Transport = "xhttp"
	}
	if s.XHTTPMode == "" {
		s.XHTTPMode = "packet-up"
	}
	if s.Fingerprint == "" {
		s.Fingerprint = "chrome"
	}
	return s, nil
}

// ── Hop Inbound (hop/exit nodes — internal traffic between servers) ──

// HopInboundSpec defines a hop or exit node's internal inbound.
// Uses NO security (plain XHTTP between your own servers).
// Stored as JSON in ChainNode.InboundSpec for role="hop" or "exit".
type HopInboundSpec struct {
	ClientID  string `json:"client_id"`            // VLESS UUID
	Port      int    `json:"port"`                 // inbound port (default: 443)
	Transport string `json:"transport,omitempty"`  // "xhttp", "ws", "grpc", "tcp"
	XHTTPMode string `json:"xhttp_mode,omitempty"` // "stream-one" (hop-to-hop)
}

func DefaultHopInbound(port int) HopInboundSpec {
	if port == 0 {
		port = 443
	}
	return HopInboundSpec{
		Port:      port,
		Transport: "xhttp",
		XHTTPMode: "stream-one",
	}
}

func ParseHopInbound(raw string) (HopInboundSpec, error) {
	var s HopInboundSpec
	if raw == "" || raw == "{}" {
		return DefaultHopInbound(0), nil
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, err
	}
	if s.Port == 0 {
		s.Port = 443
	}
	if s.Transport == "" {
		s.Transport = "xhttp"
	}
	if s.XHTTPMode == "" {
		s.XHTTPMode = "stream-one"
	}
	return s, nil
}

// ── Outbound (how this node connects to the NEXT node) ──

// OutboundSpec overrides the default next-hop connection parameters.
// Default: connect to next server's host + its inbound port.
// Stored as JSON in ChainNode.OutboundSpec.
type OutboundSpec struct {
	Address string `json:"address,omitempty"` // override next server address
	Port    int    `json:"port,omitempty"`    // override next server port
}

func ParseOutbound(raw string) (OutboundSpec, error) {
	var s OutboundSpec
	if raw == "" || raw == "{}" {
		return s, nil
	}
	err := json.Unmarshal([]byte(raw), &s)
	return s, err
}

// ── Legacy aliases (backward compatibility) ──

// EntrySpec is a backward-compatible alias for ClientInboundSpec.
type EntrySpec = ClientInboundSpec

// DefaultEntrySpec is a backward-compatible alias.
func DefaultEntrySpec() EntrySpec { return DefaultClientInbound() }

// ParseEntrySpec is a backward-compatible alias.
func ParseEntrySpec(raw string) (EntrySpec, error) { return ParseClientInbound(raw) }

// HopSpec is a backward-compatible alias for HopInboundSpec.
type HopSpec = HopInboundSpec

// ParseHopSpec is a backward-compatible alias.
func ParseHopSpec(raw string) (HopSpec, error) { return ParseHopInbound(raw) }

// ── MustJSON ──

func MustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
