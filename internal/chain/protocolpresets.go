package chain

// protocolpresets.go — protocol reference catalogs + obfuscation levels, ported
// from VPN/orchestrator/app/services/protocol_presets.py. These are constant
// tables exposed to the UI (forms, dropdowns) and to credential generators so
// the frontend can offer "Generate" buttons and validated selects.

import "strings"

// REALITY_SNI_DOMAINS — vetted REALITY handshake targets (TLS 1.3 + H2, popular,
// not DPI-blocked). Used as the default SNI pool for VLESS-REALITY.
var REALITY_SNI_DOMAINS = []string{
	"www.microsoft.com", "www.cloudflare.com", "www.apple.com",
	"gateway.icloud.com", "itunes.apple.com", "www.tesla.com",
	"dl.google.com", "google.com", "www.samsung.com",
	"www.lovelive-anime.jp", "ozon.ru",
}

// REALITY_FINGERPRINTS — uTLS fingerprints for REALITY clients.
var REALITY_FINGERPRINTS = []string{
	"chrome", "firefox", "edge", "safari", "360", "qq",
	"ios", "android", "random", "randomized",
}

// VMess protocol constants.
var (
	VMESS_SECURITY    = []string{"auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305"}
	VMESS_TRANSPORTS  = []string{"http", "ws", "quic", "grpc", "httpupgrade"}
	VMESS_WS_PATHS    = []string{"/", "/vmess", "/ray"}
	VMESS_DEFAULT_SEC = "auto"
)

// TROJAN_ALPN — ALPN order for Trojan TLS.
var (
	TROJAN_ALPN        = []string{"h2", "http/1.1", "h3"}
	TROJAN_DEFAULT_ALPN = []string{"h2", "http/1.1"}
)

// SS_CIPHERS maps a Shadowsocks 2022/legacy cipher to its key length in bytes
// (0 = no fixed length / legacy). Used by GenerateSSPassword.
var SS_CIPHERS = map[string]int{
	"2022-blake3-aes-128-gcm":       16,
	"2022-blake3-aes-256-gcm":       32,
	"2022-blake3-chacha20-poly1305": 32,
	"none":                          0,
	"aes-128-gcm":                   0,
	"aes-192-gcm":                   0,
	"aes-256-gcm":                   0,
	"chacha20-ietf-poly1305":        0,
	"xchacha20-ietf-poly1305":       0,
}

const SS_DEFAULT_CIPHER = "2022-blake3-aes-128-gcm"

// Hysteria2 constants.
var (
	HYSTERIA2_OBF_TYPES         = []string{"salamander", "gecko"}
	HYSTERIA2_DEFAULT_OBF       = "salamander"
	HYSTERIA2_BBR_PROFILES      = []string{"conservative", "standard", "aggressive"}
	HYSTERIA2_DEFAULT_BBR       = "standard"
	HYSTERIA2_MASQUERADE_TYPES  = []string{"file", "proxy", "string"}
)

// TUIC constants.
var (
	TUIC_CONGESTION_CONTROL = []string{"cubic", "new_reno", "bbr"}
	TUIC_DEFAULT_CONGESTION = "cubic"
	TUIC_UDP_RELAY_MODES    = []string{"native", "quic"}
)

// TLS constants.
var (
	TLS_ALPN              = []string{"h3", "h2", "http/1.1"}
	TLS_VERSIONS          = []string{"1.0", "1.1", "1.2", "1.3"}
	TLS_CURVES            = []string{"X25519", "P256", "P384", "P521", "X25519MLKEM768"}
	TLS_CLIENT_AUTH_TYPES = []string{"no", "request", "require-any", "verify-if-given", "require-and-verify"}
)

// ProtocolPresetsCatalog is the JSON-serialisable catalog returned by the
// /ui/api/protocol-presets endpoint for the UI forms.
type ProtocolPresetsCatalog struct {
	RealitySNIDomains      []string       `json:"reality_sni_domains"`
	RealityFingerprints    []string       `json:"reality_fingerprints"`
	VmessSecurity          []string       `json:"vmess_security"`
	VmessTransports        []string       `json:"vmess_transports"`
	SSCiphers              map[string]int `json:"ss_ciphers"`
	Hysteria2ObfTypes      []string       `json:"hysteria2_obf_types"`
	Hysteria2BBRProfiles   []string       `json:"hysteria2_bbr_profiles"`
	TuicCongestionControl  []string       `json:"tuic_congestion_control"`
	TuicUDPRelayModes      []string       `json:"tuic_udp_relay_modes"`
	TLSALPN                []string       `json:"tls_alpn"`
	TLSCurves              []string       `json:"tls_curves"`
	ObfuscationLevels      []string       `json:"obfuscation_levels"`
}

// GetProtocolPresetsCatalog returns the catalog for UI/API consumption.
func GetProtocolPresetsCatalog() ProtocolPresetsCatalog {
	return ProtocolPresetsCatalog{
		RealitySNIDomains:     REALITY_SNI_DOMAINS,
		RealityFingerprints:   REALITY_FINGERPRINTS,
		VmessSecurity:         VMESS_SECURITY,
		VmessTransports:       VMESS_TRANSPORTS,
		SSCiphers:             SS_CIPHERS,
		Hysteria2ObfTypes:     HYSTERIA2_OBF_TYPES,
		Hysteria2BBRProfiles:  HYSTERIA2_BBR_PROFILES,
		TuicCongestionControl: TUIC_CONGESTION_CONTROL,
		TuicUDPRelayModes:     TUIC_UDP_RELAY_MODES,
		TLSALPN:               TLS_ALPN,
		TLSCurves:             TLS_CURVES,
		ObfuscationLevels:     []string{"maximum", "high", "standard", "minimal"},
	}
}

// ─── Obfuscation levels ─────────────────────────────────────────────────────

// ObfuscationLevel describes one stealth tier. The fields drive which TLS
// layers, transport, padding and placement a generated config uses.
type ObfuscationLevel struct {
	Description        string   `json:"description"`
	Transport          string   `json:"transport,omitempty"`
	TLSLayers          []string `json:"tls_layers"`
	Flow               string   `json:"flow"`
	UTLSFingerprint    string   `json:"utls_fingerprint"`
	CurvePreferences   []string `json:"curve_preferences"`
	XPaddingMethod     string   `json:"x_padding_method,omitempty"`
	XPaddingBytes      string   `json:"x_padding_bytes,omitempty"`
	SessionPlacement   string   `json:"session_placement,omitempty"`
	UplinkDataPlacement string  `json:"uplink_data_placement,omitempty"`
	Mode               string   `json:"mode,omitempty"`
}

// OBFUSCATION_LEVELS — four named stealth tiers (ported from protocol_presets.py).
var OBFUSCATION_LEVELS = map[string]ObfuscationLevel{
	"maximum": {
		Description: "REALITY + XHTTP + ECH + fragment + post-quantum curves",
		Transport:   "xhttp",
		TLSLayers:   []string{"reality", "ech", "fragment", "record_fragment"},
		Flow:        "xtls-rprx-vision", UTLSFingerprint: "chrome",
		CurvePreferences:   []string{"X25519", "X25519MLKEM768"},
		XPaddingMethod:     "tokenish", XPaddingBytes: "100-1000",
		SessionPlacement:   "cookie", UplinkDataPlacement: "cookie",
		Mode: "packet-up",
	},
	"high": {
		Description: "REALITY + XHTTP + uTLS chrome",
		Transport:   "xhttp",
		TLSLayers:   []string{"reality"},
		Flow:        "xtls-rprx-vision", UTLSFingerprint: "chrome",
		CurvePreferences:   []string{"X25519"},
		XPaddingMethod:     "repeat-x", XPaddingBytes: "100-1000",
		SessionPlacement:   "path", UplinkDataPlacement: "body",
		Mode: "auto",
	},
	"standard": {
		Description: "REALITY + WebSocket + uTLS chrome",
		Transport:   "ws",
		TLSLayers:   []string{"reality"},
		Flow:        "xtls-rprx-vision", UTLSFingerprint: "chrome",
		CurvePreferences:   []string{"X25519"},
		UplinkDataPlacement: "body",
	},
	"minimal": {
		Description: "REALITY + plain TCP + uTLS chrome",
		Transport:   "",
		TLSLayers:   []string{"reality"},
		Flow:        "", UTLSFingerprint: "chrome",
		CurvePreferences: []string{"X25519"},
	},
}

// GetObfuscationLevel returns the level spec by name (case-insensitive),
// falling back to "maximum" when unknown.
func GetObfuscationLevel(name string) ObfuscationLevel {
	if lvl, ok := OBFUSCATION_LEVELS[strings.ToLower(name)]; ok {
		return lvl
	}
	return OBFUSCATION_LEVELS["maximum"]
}