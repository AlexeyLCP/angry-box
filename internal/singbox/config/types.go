package config

import "encoding/json"

// SingboxConfig represents the root configuration structure for a sing-box node.
type SingboxConfig struct {
	Log          *LogOptions          `json:"log,omitempty"`
	DNS          *DNSConfig           `json:"dns,omitempty"`
	Endpoints    []json.RawMessage    `json:"endpoints,omitempty"`
	Inbounds     []json.RawMessage    `json:"inbounds"`
	Outbounds    []json.RawMessage    `json:"outbounds"`
	Route        *RoutingSection      `json:"route,omitempty"`
	Experimental *ExperimentalOptions `json:"experimental,omitempty"`
}

// LogOptions represents the logging configuration.
type LogOptions struct {
	Level     string `json:"level,omitempty"`
	Timestamp bool   `json:"timestamp,omitempty"`
	Output    string `json:"output,omitempty"`
}

// ExperimentalOptions contains experimental features.
type ExperimentalOptions struct {
	CacheFile *CacheFileOptions `json:"cache_file,omitempty"`
}

// CacheFileOptions contains cache file configuration.
type CacheFileOptions struct {
	Enabled bool `json:"enabled"`
}

// DNSConfig represents the DNS section of the configuration.
type DNSConfig struct {
	Servers []DNSServer `json:"servers,omitempty"`
	Rules   []DNSRule   `json:"rules,omitempty"`
	Final   string      `json:"final,omitempty"`
}

// DNSServer represents a single DNS server.
type DNSServer struct {
	Tag    string `json:"tag"`
	Type   string `json:"type"`
	Server string `json:"server"`
	Detour string `json:"detour,omitempty"`
}

// DNSRule represents a rule for DNS routing.
type DNSRule struct {
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	Server       string   `json:"server"`
}

// DirectOutbound represents a simple direct outbound connection.
type DirectOutbound struct {
	Type string `json:"type"` // always "direct"
	Tag  string `json:"tag"`
}

// BlockOutbound represents a simple block outbound connection.
type BlockOutbound struct {
	Type string `json:"type"` // always "block"
	Tag  string `json:"tag"`
}

// StrategyOutbound represents a routing strategy outbound (e.g. urltest, failover).
type StrategyOutbound struct {
	Type      string   `json:"type"`
	Tag       string   `json:"tag"`
	Outbounds []string `json:"outbounds"`
	Default   string   `json:"default,omitempty"`
	URL       string   `json:"url,omitempty"`
	Interval  string   `json:"interval,omitempty"`
	Tolerance int      `json:"tolerance,omitempty"`
}

// RoutingSection represents the route section of the configuration.
type RoutingSection struct {
	Rules                 []RouteRuleEntry `json:"rules"`
	RuleSet               []RuleSetEntry   `json:"rule_set,omitempty"`
	Final                 string           `json:"final,omitempty"`
	AutoDetectInterface   bool             `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver string           `json:"default_domain_resolver,omitempty"`
}

// RouteRuleEntry represents a single routing rule.
type RouteRuleEntry struct {
	Inbound      []string `json:"inbound,omitempty"`
	Outbound     string   `json:"outbound"`
	GeoIP        []string `json:"geoip,omitempty"`
	GeoSite      []string `json:"geosite,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
}

// RuleSetEntry represents an external rule set (SRS).
type RuleSetEntry struct {
	Tag            string `json:"tag"`
	Type           string `json:"type"`
	Format         string `json:"format"`
	URL            string `json:"url"`
	DownloadDetour string `json:"download_detour,omitempty"`
	UpdateInterval string `json:"update_interval,omitempty"`
}

// VLESSOutbound represents a sing-box VLESS outbound.
type VLESSOutbound struct {
	Type       string              `json:"type"` // always "vless"
	Tag        string              `json:"tag"`
	Server     string              `json:"server"`
	ServerPort int                 `json:"server_port"`
	UUID       string              `json:"uuid"`
	Flow       string              `json:"flow,omitempty"`
	TLS        *OutboundTLSOptions `json:"tls,omitempty"`
	Multiplex  *MultiplexOptions   `json:"multiplex,omitempty"`
	Transport  *TransportOptions   `json:"transport,omitempty"`
}

// VLESSInbound represents a sing-box VLESS inbound.
type VLESSInbound struct {
	Type       string             `json:"type"` // always "vless"
	Tag        string             `json:"tag"`
	Listen     string             `json:"listen,omitempty"`
	ListenPort int                `json:"listen_port,omitempty"`
	Users      []VLESSUser        `json:"users"`
	TLS        *InboundTLSOptions `json:"tls,omitempty"`
	Multiplex  *MultiplexOptions  `json:"multiplex,omitempty"`
	Transport  *TransportOptions  `json:"transport,omitempty"`
}

// VLESSUser represents a user in a VLESS inbound.
type VLESSUser struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	Flow string `json:"flow,omitempty"`
}

// OutboundTLSOptions represents TLS options for outbound connections.
type OutboundTLSOptions struct {
	Enabled    bool                    `json:"enabled"`
	ServerName string                  `json:"server_name,omitempty"`
	UTLS       *UTLSOptions            `json:"utls,omitempty"`
	Reality    *OutboundRealityOptions `json:"reality,omitempty"`
}

// InboundTLSOptions represents TLS options for inbound connections.
type InboundTLSOptions struct {
	Enabled    bool                   `json:"enabled"`
	ServerName string                 `json:"server_name,omitempty"`
	Reality    *InboundRealityOptions `json:"reality,omitempty"`
}

// UTLSOptions represents uTLS options.
type UTLSOptions struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

// OutboundRealityOptions represents REALITY options for outbound connections.
type OutboundRealityOptions struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"` // A single string
}

// InboundRealityOptions represents REALITY options for inbound connections.
type InboundRealityOptions struct {
	Enabled    bool              `json:"enabled"`
	Handshake  *RealityHandshake `json:"handshake,omitempty"`
	PrivateKey string            `json:"private_key"`
	ShortID    []string          `json:"short_id"` // Array of strings
}

// RealityHandshake represents the fallback server configuration for REALITY.
type RealityHandshake struct {
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
}

// MultiplexOptions represents connection multiplexing options.
type MultiplexOptions struct {
	Enabled bool `json:"enabled"`
}

// TransportOptions represents transport layer options (e.g. xhttp).
type TransportOptions struct {
	Type        string              `json:"type"` // e.g. "xhttp", "ws", "http"
	Host        []string            `json:"host,omitempty"`
	Path        string              `json:"path,omitempty"`
	Method      string              `json:"method,omitempty"`
	Headers     map[string][]string `json:"headers,omitempty"`
	IdleTimeout string              `json:"idle_timeout,omitempty"`
	PingTimeout string              `json:"ping_timeout,omitempty"`
	Extra       *XHTTPExtra         `json:"extra,omitempty"`
}

// XHTTPExtra contains sing-box-extended specific options for XHTTP transport.
type XHTTPExtra struct {
	MaxStealth    bool   `json:"max_stealth,omitempty"`
	ScramblingKey string `json:"scrambling_key,omitempty"`
}

// TUICInbound represents a sing-box TUIC inbound.
type TUICInbound struct {
	Type              string             `json:"type"` // "tuic"
	Tag               string             `json:"tag"`
	Listen            string             `json:"listen,omitempty"`
	ListenPort        int                `json:"listen_port,omitempty"`
	Users             []TUICUser         `json:"users"`
	CongestionControl string             `json:"congestion_control,omitempty"`
	AuthTimeout       string             `json:"auth_timeout,omitempty"`
	ZeroRTTHandshake  bool               `json:"zero_rtt_handshake,omitempty"`
	Heartbeat         string             `json:"heartbeat,omitempty"`
	TLS               *InboundTLSOptions `json:"tls,omitempty"`
}

// TUICUser represents a user in a TUIC inbound.
type TUICUser struct {
	UUID     string `json:"uuid"`
	Password string `json:"password"`
}

// WireGuardEndpoint represents a wireguard inbound/outbound.
type WireGuardEndpoint struct {
	Type       string          `json:"type"` // "wireguard"
	Tag        string          `json:"tag"`
	System     bool            `json:"system"`
	MTU        int             `json:"mtu,omitempty"`
	Address    []string        `json:"address,omitempty"`
	PrivateKey string          `json:"private_key"`
	ListenPort int             `json:"listen_port,omitempty"`
	Peers      []WireGuardPeer `json:"peers"`
	Amnezia    *AmneziaOptions `json:"amnezia,omitempty"`
}

// WireGuardPeer represents a peer in a wireguard endpoint.
type WireGuardPeer struct {
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

// AmneziaOptions represents AWG specific extensions for wireguard in sing-box-extended.
type AmneziaOptions struct {
	JC   int    `json:"jc,omitempty"`
	JMIN int    `json:"jmin,omitempty"`
	JMAX int    `json:"jmax,omitempty"`
	S1   int    `json:"s1,omitempty"`
	S2   int    `json:"s2,omitempty"`
	H1   int    `json:"h1,omitempty"`
	H2   int    `json:"h2,omitempty"`
	H3   int    `json:"h3,omitempty"`
	H4   int    `json:"h4,omitempty"`
	I1   string `json:"i1,omitempty"`
	I2   string `json:"i2,omitempty"`
	I3   string `json:"i3,omitempty"`
	I4   string `json:"i4,omitempty"`
	I5   string `json:"i5,omitempty"`
}

// TUNInbound represents a sing-box TUN inbound.
type TUNInbound struct {
	Type          string   `json:"type"` // "tun"
	Tag           string   `json:"tag"`
	InterfaceName string   `json:"interface_name,omitempty"`
	Address       []string `json:"address,omitempty"`
	MTU           int      `json:"mtu,omitempty"`
	Stack         string   `json:"stack,omitempty"`
	AutoRoute     bool     `json:"auto_route,omitempty"`
}

// DirectInbound represents a sing-box direct inbound.
type DirectInbound struct {
	Type    string `json:"type"` // always "direct"
	Tag     string `json:"tag"`
	Network string `json:"network,omitempty"`
}
