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
	Type          string       `json:"type"` // always "direct"
	Tag           string       `json:"tag"`
	BindInterface string       `json:"bind_interface,omitempty"` // kernel AWG interface (awg-exit-nX)
	Dial          *DialOptions `json:"dial,omitempty"`
}

// BlockOutbound represents a simple block outbound connection.
type BlockOutbound struct {
	Type string `json:"type"` // always "block"
	Tag  string `json:"tag"`
}

// FallbackOutbound is the sing-box-extended priority fallback group. With our
// round-robin patch applied it does per-connection round-robin across the
// listed outbounds; blacklist_timeout temporarily excludes failing nodes.
type FallbackOutbound struct {
	Type            string   `json:"type"` // "fallback"
	Tag             string   `json:"tag"`
	Outbounds       []string `json:"outbounds"`
	BlacklistTimeout string  `json:"blacklist_timeout,omitempty"`
}

// StrategyOutbound represents a routing strategy outbound (e.g. urltest, failover).
type StrategyOutbound struct {
	Type      string       `json:"type"`
	Tag       string       `json:"tag"`
	Outbounds []string     `json:"outbounds"`
	Default   string       `json:"default,omitempty"`
	URL       string       `json:"url,omitempty"`
	Interval  string       `json:"interval,omitempty"`
	Tolerance int          `json:"tolerance,omitempty"`
	Dial      *DialOptions `json:"dial,omitempty"`
}

// DialOptions contains dial-specific settings (sing-box 1.12+).
type DialOptions struct {
	DomainResolver string `json:"domain_resolver,omitempty"`
}

// RoutingSection represents the route section of the configuration.
type RoutingSection struct {
	Rules                 []RouteRuleEntry `json:"rules"`
	RuleSet               []RuleSetEntry   `json:"rule_set,omitempty"`
	Final                 string           `json:"final,omitempty"`
	AutoDetectInterface   bool             `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver string           `json:"default_domain_resolver,omitempty"`
}

// RouteRuleEntry represents a single routing rule. sing-box 1.13+ uses action
// rules (sniff/hijack-dns/route/reject) instead of the old inbound/outbound
// pairing; we keep both shapes via omitempty.
type RouteRuleEntry struct {
	Inbound      []string `json:"inbound,omitempty"`
	Outbound     string   `json:"outbound,omitempty"`
	AuthUser     []string `json:"auth_user,omitempty"`
	GeoIP        []string `json:"geoip,omitempty"`
	GeoSite      []string `json:"geosite,omitempty"`
	Domain       []string `json:"domain,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	DomainKeyword []string `json:"domain_keyword,omitempty"`
	IPCidr       []string `json:"ip_cidr,omitempty"`
	SourceIPCIDR []string `json:"source_ip_cidr,omitempty"` // match source IP CIDR (AWG peer routing)
	Protocol     []string `json:"protocol,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
	Action       string   `json:"action,omitempty"` // sniff | hijack-dns | route | reject | ...
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
	Dial       *DialOptions        `json:"dial,omitempty"`
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
	Enabled          bool                    `json:"enabled"`
	ServerName       string                  `json:"server_name,omitempty"`
	ALPN             []string                `json:"alpn,omitempty"`
	MinVersion       string                  `json:"min_version,omitempty"`
	MaxVersion       string                  `json:"max_version,omitempty"`
	CurvePreferences []string                `json:"curve_preferences,omitempty"`
	UTLS             *UTLSOptions            `json:"utls,omitempty"`
	Reality          *OutboundRealityOptions `json:"reality,omitempty"`
	ECH              *ECHOptions             `json:"ech,omitempty"`
	// Insecure skips certificate verification. Required when the server uses a
	// per-node self-signed cert (TUIC/Hysteria2 standalone); never set for
	// REALITY (REALITY verifies via the public key, not a CA).
	Insecure               bool   `json:"insecure,omitempty"`
	Fragment               bool   `json:"fragment,omitempty"`
	FragmentFallbackDelay  string `json:"fragment_fallback_delay,omitempty"`
	RecordFragment         bool   `json:"record_fragment,omitempty"`
}

// InboundTLSOptions represents TLS options for inbound connections.
type InboundTLSOptions struct {
	Enabled               bool                   `json:"enabled"`
	ServerName            string                 `json:"server_name,omitempty"`
	ALPN                  []string               `json:"alpn,omitempty"`
	MinVersion            string                 `json:"min_version,omitempty"`
	MaxVersion            string                 `json:"max_version,omitempty"`
	CurvePreferences      []string               `json:"curve_preferences,omitempty"`
	Reality               *InboundRealityOptions `json:"reality,omitempty"`
	ECH                   *InboundECHOptions     `json:"ech,omitempty"`
	Certificate           string                 `json:"certificate,omitempty"`
	Key                   string                 `json:"key,omitempty"`
	CertificatePath       string                 `json:"certificate_path,omitempty"`
	KeyPath               string                 `json:"key_path,omitempty"`
}

// ECHOptions is the client-side ECH block (no key list on the client).
type ECHOptions struct {
	Enabled bool `json:"enabled"`
}

// InboundECHOptions is the server-side ECH block: a config list + the post-
// quantum signature schemes flag (sing-box-extended 2.5.0).
type InboundECHOptions struct {
	Enabled                    bool     `json:"enabled"`
	Key                        []string `json:"key,omitempty"`
	PQSignatureSchemesEnabled  bool     `json:"pq_signature_schemes_enabled,omitempty"`
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
	Mode        string              `json:"mode,omitempty"` // xhttp: packet-up | stream-up | auto
	Headers     map[string][]string `json:"headers,omitempty"`
	IdleTimeout string              `json:"idle_timeout,omitempty"`
	PingTimeout string              `json:"ping_timeout,omitempty"`
	Extra       *XHTTPExtra         `json:"extra,omitempty"`

	// sing-box-extended XHTTP obfuscation fields (REALITY+XHTTP max obfuscation).
	XPaddingBytes         string           `json:"x_padding_bytes,omitempty"`
	XPaddingObfsMode      bool             `json:"x_padding_obfs_mode,omitempty"`
	XPaddingMethod        string           `json:"x_padding_method,omitempty"`
	XPaddingPlacement     string           `json:"x_padding_placement,omitempty"`
	XPaddingKey           string           `json:"x_padding_key,omitempty"`
	XPaddingHeader        string           `json:"x_padding_header,omitempty"`
	SessionPlacement      string           `json:"session_placement,omitempty"`
	SeqPlacement          string           `json:"seq_placement,omitempty"`
	UplinkDataPlacement   string           `json:"uplink_data_placement,omitempty"`
	UplinkHTTPMethod      string           `json:"uplink_http_method,omitempty"`
	ScMaxEachPostBytes    string           `json:"sc_max_each_post_bytes,omitempty"`
	ScMinPostsIntervalMs  string           `json:"sc_min_posts_interval_ms,omitempty"`
	ScMaxBufferedPosts    int              `json:"sc_max_buffered_posts,omitempty"`
	ScStreamUpServerSecs  string           `json:"sc_stream_up_server_secs,omitempty"`
	NoGRPCHeader          bool             `json:"no_grpc_header,omitempty"`
	NoSSEHeader           bool             `json:"no_sse_header,omitempty"`
	Xmux                  *XmuxOptions     `json:"xmux,omitempty"`
}

// XmuxOptions is the XHTTP xmux multiplexing block.
type XmuxOptions struct {
	MaxConcurrency    string `json:"max_concurrency,omitempty"`
	HMaxRequestTimes  string `json:"h_max_request_times,omitempty"`
	HMaxReusableSecs  string `json:"h_max_reusable_secs,omitempty"`
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

// TUICOutbound represents a sing-box TUIC outbound (client side). Mirrors
// TUICInbound but targets a remote server instead of listening.
type TUICOutbound struct {
	Type              string              `json:"type"` // "tuic"
	Tag               string              `json:"tag"`
	Server            string              `json:"server"`
	ServerPort        int                 `json:"server_port"`
	UUID              string              `json:"uuid"`
	Password          string              `json:"password"`
	CongestionControl string              `json:"congestion_control,omitempty"`
	UDPRelayMode      string              `json:"udp_relay_mode,omitempty"` // native/quic
	ZeroRTTHandshake  bool                `json:"zero_rtt_handshake,omitempty"`
	Heartbeat         string              `json:"heartbeat,omitempty"`
	TLS               *OutboundTLSOptions `json:"tls,omitempty"`
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
	// Server-side endpoint peer endpoint (the client's address/port to dial
	// back, or the next-hop address/port for a chain-hop endpoint). Optional;
	// omitted for pure AllowedIPs-only peers.
	Address                  string `json:"address,omitempty"`
	Port                     int    `json:"port,omitempty"`
	PersistentKeepaliveInterval int `json:"persistent_keepalive_interval,omitempty"`
}

// WireGuardOutbound represents a sing-box wireguard OUTBOUND (the client side
// of a WireGuard link — dials a remote server endpoint). Used for inter-node
// AWG chain transport: the previous node's outbound dialing the next node's
// transit endpoint. Distinct from WireGuardEndpoint, which is the server side.
type WireGuardOutbound struct {
	Type           string          `json:"type"` // "wireguard"
	Tag            string          `json:"tag"`
	Server         string          `json:"server"`
	ServerPort     int             `json:"server_port"`
	LocalAddresses []string        `json:"local_addresses"` // this client's tunnel IPs
	PrivateKey     string          `json:"private_key"`     // client's WG private key
	PeerPublicKey  string          `json:"peer_public_key"` // server's WG public key
	PreSharedKey   string          `json:"pre_shared_key,omitempty"`
	MTU            int             `json:"mtu,omitempty"`
	Amnezia        *AmneziaOptions `json:"amnezia,omitempty"`
}

// AmneziaOptions represents AWG specific extensions for wireguard in
// sing-box-extended. JSON keys are lowercase (matching awg_presets'
// to_singbox_amnezia: jc/jmin/jmax/s1-s4/h1-h4/itime). H1-H4 are "lo-hi" range
// strings. I1-I5 are CPS packet strings in `<b 0xHEX>` / `<r N><b 0xHEX>` form
// (optional; when present must be even-length hex — the deploy path pads them).
type AmneziaOptions struct {
	JC    int    `json:"jc,omitempty"`
	JMIN  int    `json:"jmin,omitempty"`
	JMAX  int    `json:"jmax,omitempty"`
	S1    int    `json:"s1,omitempty"`
	S2    int    `json:"s2,omitempty"`
	S3    int    `json:"s3,omitempty"`
	S4    int    `json:"s4,omitempty"`
	H1    string `json:"h1,omitempty"`
	H2    string `json:"h2,omitempty"`
	H3    string `json:"h3,omitempty"`
	H4    string `json:"h4,omitempty"`
	I1    string `json:"i1,omitempty"`
	I2    string `json:"i2,omitempty"`
	I3    string `json:"i3,omitempty"`
	I4    string `json:"i4,omitempty"`
	I5    string `json:"i5,omitempty"`
	// ITime is NOT emitted to the sing-box endpoint JSON — sing-box-extended's
	// wireguard-go rejects "itime" at runtime ("IPC error -22: invalid UAPI
	// device key: itime") even though `sing-box check` accepts it. It is kept
	// here only as a holder so the client awg-quick .conf can render "Itime = N"
	// (awg-quick DOES support it). BuildAmneziaSection must NOT copy it into the
	// section that gets marshaled to the endpoint; renderAWGQuickConf reads it
	// from the preset directly.
	ITime int `json:"-"`
}

// TUNInbound represents a sing-box TUN inbound.
type TUNInbound struct {
	Type             string   `json:"type"` // "tun"
	Tag              string   `json:"tag"`
	InterfaceName    string   `json:"interface_name,omitempty"`
	Address          []string `json:"address,omitempty"`
	MTU              int      `json:"mtu,omitempty"`
	Stack            string   `json:"stack,omitempty"` // "mixed" recommended (kernel TCP + gVisor UDP for QUIC)
	AutoRoute        bool     `json:"auto_route,omitempty"`
	IncludeInterface []string `json:"include_interface,omitempty"` // kernel AWG server iface, e.g. ["awg0"]
	StrictRoute      bool     `json:"strict_route,omitempty"`
	AutoRedirect     bool     `json:"auto_redirect,omitempty"`
}

// DirectInbound represents a sing-box direct inbound.
type DirectInbound struct {
	Type    string `json:"type"` // always "direct"
	Tag     string `json:"tag"`
	Network string `json:"network,omitempty"`
}

// Hysteria2Inbound represents a sing-box Hysteria2 inbound.
type Hysteria2Inbound struct {
	Type       string             `json:"type"` // "hysteria2"
	Tag        string             `json:"tag"`
	Listen     string             `json:"listen,omitempty"`
	ListenPort int                `json:"listen_port,omitempty"`
	Users      []Hysteria2User    `json:"users"`
	UpMbps     int                `json:"up_mbps,omitempty"`
	DownMbps   int                `json:"down_mbps,omitempty"`
	Obfs       *Hysteria2Obfs     `json:"obfs,omitempty"`
	TLS        *InboundTLSOptions `json:"tls,omitempty"`
}

type Hysteria2User struct {
	Name     string `json:"name,omitempty"`
	Password string `json:"password"`
}

type Hysteria2Obfs struct {
	Type     string `json:"type"` // e.g. "salamander"
	Password string `json:"password"`
}
