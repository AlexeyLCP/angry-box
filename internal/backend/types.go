package backend

import "encoding/json"

type BackendStatus struct {
	Running bool   `json:"running"`
	Version string `json:"version"`
	PID     int    `json:"pid"`
}

// Supported protocols (v26)
const (
	ProtocolVLESS       = "vless"
	ProtocolVMess       = "vmess"
	ProtocolTrojan      = "trojan"
	ProtocolShadowsocks = "shadowsocks"
)

// Supported transports (v26 — XHTTP is default/recommended)
const (
	TransportXHTTP = "xhttp" // XHTTP (recommended for v26)
	TransportWS    = "ws"    // WebSocket
	TransportgRPC  = "grpc"  // gRPC
	TransportH2    = "h2"    // HTTP/2
	TransportQUIC  = "quic"  // QUIC
	TransportTCP   = "tcp"   // TCP (plain)
	TransportHTTP  = "http"  // HTTP
)

// Supported security
const (
	SecurityReality = "reality"
	SecurityTLS     = "tls"
	SecurityNone    = "none"
)

type InboundSpec struct {
	Tag      string          `json:"tag"`
	Protocol string          `json:"protocol"`
	Port     int             `json:"port"`
	Listen   string          `json:"listen,omitempty"`
	Settings json.RawMessage `json:"settings"`
	Stream   json.RawMessage `json:"stream,omitempty"`
}

type InboundResult struct {
	Tag  string `json:"tag"`
	Port int    `json:"port"`
}

type OutboundSpec struct {
	Tag         string          `json:"tag"`
	Protocol    string          `json:"protocol"`
	Settings    json.RawMessage `json:"settings"`
	Stream      json.RawMessage `json:"stream,omitempty"`
	SendThrough string          `json:"sendThrough,omitempty"`
}

type OutboundResult struct {
	Tag string `json:"tag"`
}

type RoutingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag,omitempty"`
	OutboundTag string   `json:"outboundTag"`
}

type RawConfig struct {
	Inbounds  []json.RawMessage `json:"inbounds"`
	Outbounds []json.RawMessage `json:"outbounds"`
	Routing   json.RawMessage   `json:"routing"`
}

// StreamSettings represents the streamSettings block in Xray config.
type StreamSettings struct {
	Network       string          `json:"network"`                  // "xhttp", "ws", "grpc", "h2", "quic", "tcp"
	Security      string          `json:"security,omitempty"`       // "reality", "tls", "none"
	XHTTPSettings *XHTTPSettings  `json:"xhttpSettings,omitempty"`  // XHTTP transport (v26 recommended)
	WSSettings    *WSSettings     `json:"wsSettings,omitempty"`     // WebSocket transport
	GRPCSettings  *GRPCSettings   `json:"grpcSettings,omitempty"`   // gRPC transport
	HTTPSettings  *HTTPSettings   `json:"httpSettings,omitempty"`   // HTTP/2 transport
	QUICSettings  *QUICSettings   `json:"quicSettings,omitempty"`   // QUIC transport
	TCPSettings   *TCPSettings    `json:"tcpSettings,omitempty"`    // TCP settings

	// Security settings
	RealitySettings *RealitySettings `json:"realitySettings,omitempty"`
	TLSSettings     *TLSSettings     `json:"tlsSettings,omitempty"`
}

// XHTTPSettings: XHTTP transport (Xray v26+).
// Recommended transport for DPI bypass and multi-stream multiplexing.
type XHTTPSettings struct {
	Host    string `json:"host,omitempty"`    // e.g. "discord.com"
	Path    string `json:"path,omitempty"`    // e.g. "/xray"
	Mode    string `json:"mode,omitempty"`    // "packet-up", "stream-up", "stream-one", "stream-upgrade"
	Header  string `json:"header,omitempty"`  // optional header type
}

// WSSettings: WebSocket transport.
type WSSettings struct {
	Path    string            `json:"path,omitempty"`
	Host    string            `json:"host,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// GRPCSettings: gRPC transport.
type GRPCSettings struct {
	ServiceName   string `json:"serviceName,omitempty"`
	MultiMode     bool   `json:"multiMode,omitempty"`
	IdleTimeout   int    `json:"idle_timeout,omitempty"`
	HealthCheck   bool   `json:"health_check,omitempty"`
}

// HTTPSettings: HTTP/2 transport.
type HTTPSettings struct {
	Host   []string `json:"host,omitempty"`
	Path   string   `json:"path,omitempty"`
	Method string   `json:"method,omitempty"`
}

// QUICSettings: QUIC transport.
type QUICSettings struct {
	Security string `json:"security,omitempty"`
	Key      string `json:"key,omitempty"`
	Header   string `json:"header,omitempty"`
}

// TCPSettings: TCP transport options.
type TCPSettings struct {
	Header string `json:"header,omitempty"` // "none", "http"
}

// RealitySettings: Reality security (v26 format).
// NOTE: v26 requires BOTH singular AND plural forms for backward compat.
type RealitySettings struct {
	ServerName  string   `json:"serverName,omitempty"`
	ServerNames []string `json:"serverNames,omitempty"` // v26 compat: provide BOTH
	PrivateKey  string   `json:"privateKey,omitempty"`
	ShortID     string   `json:"shortId,omitempty"`
	ShortIDs    []string `json:"shortIds,omitempty"` // v26 compat: provide BOTH
	Fingerprint string   `json:"fingerprint,omitempty"`
	PublicKey   string   `json:"publicKey,omitempty"`
	Dest        string   `json:"dest,omitempty"`
	Show        bool     `json:"show,omitempty"`
	Xver        int      `json:"xver,omitempty"`
}

// TLSSettings: TLS security.
type TLSSettings struct {
	ServerName    string `json:"serverName,omitempty"`
	AllowInsecure bool   `json:"allowInsecure,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
}
