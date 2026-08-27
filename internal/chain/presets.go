package chain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

//go:embed default_presets.json
var defaultPresetsJSON []byte

var (
	presetsMu sync.RWMutex
	presets   = make(map[string]ConnectionPreset)
)

// RealityPreset — настройки для Reality-обфускации
type RealityPreset struct {
	ServerNames  []string `json:"server_names"` // список SNI для рандомизации
	Fingerprints []string `json:"fingerprints"` // chrome, firefox, safari, edge и т.д.
	ShortIDLen   int      `json:"short_id_len"` // длина short_id
}

// XHTTPPreset — настройки для XHTTP транспорта (очень сильный вариант в 2025-2026)
type XHTTPPreset struct {
	Methods     []string            `json:"methods"`
	Paths       []string            `json:"paths"`
	Hosts       []string            `json:"hosts"`
	Headers     map[string][]string `json:"headers"`
	IdleTimeout string              `json:"idle_timeout"`
	PingTimeout string              `json:"ping_timeout"`

	// 2026 advanced XHTTP obfuscation fields (from community research: Xray XHTTP, Naive, Hysteria Gecko)
	PaddingBytes   string `json:"padding_bytes,omitempty"` // "min-max" or single value, e.g. "300-1800"
	Multiplex      bool   `json:"multiplex,omitempty"`
	MaxConcurrency string `json:"max_concurrency,omitempty"` // e.g. "4-48"
	UpstreamHost   string `json:"upstream_host,omitempty"`
	DownstreamHost string `json:"downstream_host,omitempty"`
	UpstreamAlpn   string `json:"upstream_alpn,omitempty"`
	DownstreamAlpn string `json:"downstream_alpn,omitempty"`
	StealthLevel   int    `json:"stealth_level,omitempty"` // 0-3, drives mode/padding strength
}

// TUICPreset — настройки для TUIC
type TUICPreset struct {
	CongestionControls []string `json:"congestion_controls"`
	AuthTimeout        string   `json:"auth_timeout"`
}

// AWGPreset — настройки для AmneziaWG (2026 extended)
type AWGPreset struct {
	JC   int `json:"jc"`
	JMIN int `json:"jmin"`
	JMAX int `json:"jmax"`
	S1   int `json:"s1"`
	S2   int `json:"s2"`
	S3   int `json:"s3,omitempty"` // cookie-junk count (0 = unset/legacy)
	S4   int `json:"s4,omitempty"` // transport-junk count (0 = unset/legacy)
	H1   int `json:"h1"`
	H2   int `json:"h2"`
	H3   int `json:"h3"`
	H4   int `json:"h4"`

	// ITime: I1-I5 concealment-packet lifetime in seconds (0 = unset/legacy).
	// Controls how long the AWG peer caches a sent I-packet to recognize its
	// echo; mismatched ITime server↔client can drop concealment replay.
	ITime int `json:"itime,omitempty"`

	// 2026 advanced CPS / I1-I5 support (from pumbaX/awg2.sh best practices)
	CPSLevel int    `json:"cps_level,omitempty"`
	Mimicry  string `json:"mimicry,omitempty"` // "quic" | "sip" | "dns" | "none"

	// Optional I1 packet override (base64 or special keywords "quic-1200", "dns-1232")
	I1Packet string `json:"i1_packet,omitempty"`

	// Version is the AmneziaWG protocol version this preset targets — "1.5",
	// "2", or "3" (model.AWGVersion*). Empty defaults to "2" (the current
	// default kernel+CPS path) so legacy presets resolve without a value. The
	// value drives both UI grouping (PresetOption.Group) and the preset↔version
	// compatibility resolver (ResolvePresetsByAWGVersion): an AWG 3.0 inbound
	// can only pair with a v3 preset, etc. The v3 presets set S1-S4 >= 12
	// (HeaderCipherNonceSize constraint for header protection) and minimize
	// H1-H4 as redundant (HPK encrypts the low-entropy header fields the H
	// markers otherwise fingerprint). See AGENTS.md #5 (revision) +
	// awg_version.go for the taxonomy.
	Version string `json:"version,omitempty"`
}

// ConnectionPreset — основной составной пресет (2026 extended)
type ConnectionPreset struct {
	Name        string         `json:"name"`
	Protocol    string         `json:"protocol,omitempty"` // "awg"|"vless-reality"|"xhttp"|"tuic"|"" (legacy/global — resolvable but excluded from dropdowns)
	Description string         `json:"description"`
	Reality     *RealityPreset `json:"reality,omitempty"`
	XHTTP       *XHTTPPreset   `json:"xhttp,omitempty"`
	TUIC        *TUICPreset    `json:"tuic,omitempty"`
	AWG         *AWGPreset     `json:"awg,omitempty"`

	// New 2026 top-level fields for maximum control
	CPSLevel   int    `json:"cps_level,omitempty"`
	AWGMimicry string `json:"awg_mimicry,omitempty"`

	// Routing rules — country-specific traffic steering
	Routing struct {
		DirectGeoIP   []string `json:"direct_geoip,omitempty"`   // geoip codes for direct access
		DirectGeoSite []string `json:"direct_geosite,omitempty"` // geosite categories for direct access
		DirectDomains []string `json:"direct_domains,omitempty"` // domain suffixes for direct access
		BlockGeoSite  []string `json:"block_geosite,omitempty"`  // geosite categories to block
	} `json:"routing,omitempty"`
}

// LoadPresets загружает встроенные пресеты + (опционально) мерджит внешние.
// Внешние пресеты имеют приоритет.
func LoadPresets(external []ConnectionPreset) error {
	presetsMu.Lock()
	defer presetsMu.Unlock()

	// Clear to prevent accumulation on repeated calls (was causing stale external presets to linger)
	for k := range presets {
		delete(presets, k)
	}

	// Загружаем дефолтные заново
	var defaults []ConnectionPreset
	if err := json.Unmarshal(defaultPresetsJSON, &defaults); err != nil {
		return fmt.Errorf("failed to parse default presets: %w", err)
	}

	for _, p := range defaults {
		presets[p.Name] = p
	}

	// Мерджим внешние (перезаписывают)
	for _, p := range external {
		presets[p.Name] = p
	}

	return nil
}

// GetPreset возвращает пресет по имени (thread-safe)
func GetPreset(name string) (ConnectionPreset, bool) {
	presetsMu.RLock()
	defer presetsMu.RUnlock()
	p, ok := presets[name]
	return p, ok
}

// ListPresets возвращает все доступные имена пресетов
func ListPresets() []string {
	presetsMu.RLock()
	defer presetsMu.RUnlock()

	names := make([]string, 0, len(presets))
	for name := range presets {
		names = append(names, name)
	}
	return names
}

// ListPresetsForProtocol returns preset names tagged with the given protocol.
// Legacy/global presets (Protocol == "") are EXCLUDED from the dropdown — they
// are resolvable by name (for existing chains) but not offered for new
// selection. The dropdown shows only protocol-scoped presets.
func ListPresetsForProtocol(protocol string) []string {
	presetsMu.RLock()
	defer presetsMu.RUnlock()

	names := make([]string, 0, len(presets))
	for name, p := range presets {
		if p.Protocol == protocol {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// PresetOption is a UI-facing preset descriptor: the preset name plus the
// fields the chain/inbound preset dropdown needs to group presets (by protocol
// + stealth/robust) and show the AWG junk-packet count (Jc) inline. Jc is the
// single most important handshake-relevant knob (AGENTS #17: Jc=120 kills
// handshake on budget VPS; Jc<=10 = robust). Group is a short label used for
// <optgroup> rendering ("Stealth (Jc=120)", "Robust (Jc≤10)", ...).
type PresetOption struct {
	Name     string
	Protocol string // "" = global/all
	Jc       int    // AWG junk-packet count; 0 when the preset has no AWG section
	Robust   bool   // true for *_awg_robust presets (Jc<=10, budget-VPS friendly)
	// Version is the AmneziaWG protocol version this preset targets
	// (model.AWGVersion*; "" = "2"). Drives the AWG · 3.0 optgroup so the
	// operator can pick a header-protection preset vs a classic 2.0 one.
	Version string
}

// Group returns a short optgroup label for the dropdown.
func (o PresetOption) Group() string {
	switch {
	case o.Protocol == "xhttp":
		return "XHTTP"
	case o.Protocol == "vless-reality":
		return "Reality"
	case o.Version == "3" && o.Protocol == "awg":
		return "AWG · 3.0 (header protection)"
	case o.Robust:
		return "AWG · Robust (бюджетные VPS)"
	case o.Protocol == "awg":
		return "AWG · Stealth (Jc=120, premium)"
	default:
		return "Все протоколы (Stealth)"
	}
}

// ListPresetsDetailed returns every preset as a PresetOption, sorted by name.
// Used by the chain + inbound preset dropdowns so the operator can see Jc +
// robust/stealth grouping at a glance instead of guessing from preset names.
func ListPresetsDetailed() []PresetOption {
	presetsMu.RLock()
	defer presetsMu.RUnlock()
	out := make([]PresetOption, 0, len(presets))
	for name, p := range presets {
		opt := PresetOption{Name: name, Protocol: p.Protocol}
		if p.AWG != nil {
			opt.Jc = p.AWG.JC
			// AWG presets default to v2 when Version is unset (the current
			// kernel+CPS baseline); only v3 presets carry "3" explicitly so
			// they land in the AWG · 3.0 optgroup.
			if v := p.AWG.Version; v != "" {
				opt.Version = v
			} else {
				opt.Version = model.AWGVersion2
			}
		}
		opt.Robust = strings.HasSuffix(name, "_awg_robust")
		out = append(out, opt)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// PresetGroup is a named bucket of preset options for <optgroup> rendering.
type PresetGroup struct {
	Label   string
	Options []PresetOption
}

// GroupPresets buckets PresetOptions by Group() label, preserving a stable,
// UI-friendly order: AWG 3.0 (header protection, max stealth) first, then AWG
// Robust (the recommended default for budget VPS, AGENTS #17), then AWG Stealth
// (2.0, Jc=120), then Reality, then XHTTP, then global.
func GroupPresets(opts []PresetOption) []PresetGroup {
	// Stable order of group labels.
	order := []string{
		"AWG · 3.0 (header protection)",
		"AWG · Robust (бюджетные VPS)",
		"AWG · Stealth (Jc=120, premium)",
		"Reality",
		"XHTTP",
		"Все протоколы (Stealth)",
	}
	idx := map[string]int{}
	for i, l := range order {
		idx[l] = i
	}
	buckets := map[string][]PresetOption{}
	for _, o := range opts {
		g := o.Group()
		buckets[g] = append(buckets[g], o)
	}
	var out []PresetGroup
	for _, label := range order {
		if opts, ok := buckets[label]; ok {
			out = append(out, PresetGroup{Label: label, Options: opts})
		}
	}
	// Any group label not in the predefined order (future presets) — append at end.
	for label, opts := range buckets {
		if _, ok := idx[label]; ok {
			continue
		}
		out = append(out, PresetGroup{Label: label, Options: opts})
	}
	return out
}

// presetSupportsProtocol checks whether a preset has a relevant section for the protocol.
func presetSupportsProtocol(p ConnectionPreset, protocol string) bool {
	switch protocol {
	case "awg":
		return p.AWG != nil
	case "tuic":
		return p.TUIC != nil
	case "vless-reality":
		return p.Reality != nil
	case "naive", "mieru", "mtproxy", "trusttunnel":
		return false
	default: // shadowsocks, trojan, vmess, hysteria2, telemt — transport obfuscation
		return p.XHTTP != nil
	}
}

// MustGetPreset — как GetPreset, но паникует если не найден (удобно для тестов и дефолтов)
func MustGetPreset(name string) ConnectionPreset {
	p, ok := GetPreset(name)
	if !ok {
		panic(fmt.Sprintf("obfuscation preset %q not found", name))
	}
	return p
}

var defaultPresetName = "maximum_stealth_2026"

func init() {
	// The embedded default_presets.json is fixed at compile time, so a parse
	// failure here is a build-time bug, not a runtime condition. Panic keeps the
	// generator honest: shipping an orchestrator with an unparseable preset base
	// would silently degrade every deploy. The recover middleware in web/auth
	// does NOT cover init(), so this panic does abort startup — by design.
	if err := LoadPresets(nil); err != nil {
		panic(fmt.Sprintf("angry-box: failed to load embedded default obfuscation presets (build-time bug, fix default_presets.json): %v", err))
	}
}

// SetDefaultProfile устанавливает глобальный дефолтный профиль обфускации.
// Обычно вызывается один раз при старте из конфига.
func SetDefaultProfile(name string) {
	if _, ok := GetPreset(name); ok {
		defaultPresetName = name
	}
}

// GetDefaultPreset возвращает текущий дефолтный пресет обфускации.
func GetDefaultPreset() ConnectionPreset {
	return MustGetPreset(defaultPresetName)
}

// GetDefaultPresetName возвращает имя текущего дефолтного профиля.
func GetDefaultPresetName() string {
	return defaultPresetName
}

// GetDefaultPresetForProtocol returns the default preset for a protocol. If
// PanelSettings.DefaultPresetByProtocol is set (via the caller — this function
// does not read the store), the caller should pass that name in via
// SetDefaultProfile; here we use a built-in per-protocol default that the
// caller can override. Falls back to the global default for unknown protocols.
func GetDefaultPresetForProtocol(protocol string) ConnectionPreset {
	if name := defaultPresetForProtocol(protocol); name != "" {
		if p, ok := GetPreset(name); ok {
			return p
		}
	}
	return GetDefaultPreset()
}

// defaultPresetForProtocol returns the built-in default preset name for a
// protocol. The caller (applier) may override via PanelSettings.
func defaultPresetForProtocol(protocol string) string {
	switch protocol {
	case "awg":
		return "maximum_stealth_2026_awg"
	case "vless-reality":
		return "maximum_stealth_2026_reality"
	case "xhttp", "":
		return "xhttp_max_stealth_2026"
	default:
		return "" // fall back to global default
	}
}

// defaultPresetForAWGVersion returns the built-in default AWG preset name for a
// protocol version (model.AWGVersion*). v3 → the header-protection preset,
// v1.5/v2 (and unknown) → the classic 2.0 stealth preset. Used by the preset
// resolver when an inbound's selected preset is incompatible with its version
// (e.g. a v3 inbound paired with a v2-only preset by an old store).
func defaultPresetForAWGVersion(version string) string {
	switch version {
	case model.AWGVersion3, model.AWGVersion31:
		return "maximum_stealth_2026_awg3"
	default: // AWGVersion1x, AWGVersion2, ""
		return "maximum_stealth_2026_awg"
	}
}

// PresetSupportsAWGVersion reports whether the given preset's AWG section is
// compatible with the requested AWG protocol version. Compatibility rules:
//   - v3 requires a preset whose AWG.Version is "3" (S1-S4 >= 12, minimized
//     H1-H4 — the header-protection contract). A v2 preset is NOT promoted to
//     v3 silently because S1-S4 may be < 12 and the H-marker choice assumes no
//     HPK layer.
//   - v1.5 / v2 accept any preset whose AWG.Version is NOT "3" (a v3 preset's
//     minimized H1-H4 / raised S1-S4 would still render on a 2.0 kernel, but
//     the result is suboptimal — the resolver prefers a native v2 preset).
//
// An AWG preset with Version "" is treated as "2" (the legacy default).
func PresetSupportsAWGVersion(p ConnectionPreset, version string) bool {
	if p.AWG == nil {
		return false
	}
	presetVer := p.AWG.Version
	if presetVer == "" {
		presetVer = model.AWGVersion2
	}
	if version == "" {
		version = model.AWGVersion2
	}
	if model.IsAWG3Family(version) {
		return presetVer == model.AWGVersion3
	}
	return presetVer != model.AWGVersion3
}

// GetEffectivePreset возвращает пресет, который следует использовать для данной цепочки.
// Приоритет: явный override на цепочке (chain.ObfuscationProfile) > per-protocol
// default (when UserProtocol is non-empty) > глобальный дефолт из конфига.
func GetEffectivePreset(c *model.Chain) ConnectionPreset {
	if c != nil && c.ObfuscationProfile != "" {
		if p, ok := GetPreset(c.ObfuscationProfile); ok {
			return p
		}
	}
	if c != nil && c.UserProtocol != "" {
		return GetDefaultPresetForProtocol(string(c.UserProtocol))
	}
	return GetDefaultPreset()
}

// ruleSetBaseURL — базовый URL для SRS-файлов sing-box.
const ruleSetBaseURL = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set"
const ruleSetGeoSiteURL = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set"

// BuildRoutingSection создаёт полноценную routing-секцию на основе пресета и имени цепочки.
func BuildRoutingSection(preset *ConnectionPreset, chainOutboundTag string) config.RoutingSection {
	section := config.RoutingSection{
		Rules:                 []config.RouteRuleEntry{},
		Final:                 chainOutboundTag,
		AutoDetectInterface:   true,
		DefaultDomainResolver: "dns-direct",
	}

	ruleTags := map[string]bool{}

	// Direct geoip rules (route specific countries directly)
	for _, geo := range preset.Routing.DirectGeoIP {
		tag := "geoip-" + geo
		ruleTags[tag] = true
		section.Rules = append(section.Rules, config.RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "direct-out",
		})
	}

	// Direct geosite rules (route specific sites directly)
	for _, gs := range preset.Routing.DirectGeoSite {
		tag := gs
		ruleTags[tag] = true
		section.Rules = append(section.Rules, config.RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "direct-out",
		})
	}

	// Direct domain suffixes (always direct, no rule_set needed)
	if len(preset.Routing.DirectDomains) > 0 {
		section.Rules = append(section.Rules, config.RouteRuleEntry{
			DomainSuffix: preset.Routing.DirectDomains,
			Outbound:     "direct-out",
		})
	}

	// Block rules (ads, malware, etc.)
	for _, gs := range preset.Routing.BlockGeoSite {
		tag := gs
		ruleTags[tag] = true
		section.Rules = append(section.Rules, config.RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "block",
		})
	}

	// Build rule_set entries with direct download detour
	for tag := range ruleTags {
		entry := config.RuleSetEntry{
			Tag:            tag,
			Type:           "remote",
			Format:         "binary",
			DownloadDetour: "direct-out",
			UpdateInterval: "24h",
		}

		// Determine URL based on tag prefix
		isGeoIP := false
		for _, g := range preset.Routing.DirectGeoIP {
			if "geoip-"+g == tag {
				isGeoIP = true
				break
			}
		}

		if isGeoIP {
			entry.URL = ruleSetBaseURL + "/" + tag + ".srs"
		} else {
			entry.URL = ruleSetGeoSiteURL + "/geosite-" + tag + ".srs"
		}
		section.RuleSet = append(section.RuleSet, entry)
	}

	return section
}

// BuildStrategyOutbound создаёт стратегический outbound (urltest/selector/failover).
func BuildStrategyOutbound(strategy string, outboundTags []string) *config.StrategyOutbound {
	if len(outboundTags) == 0 {
		return nil
	}
	switch strategy {
	case string(model.StrategyURLTest):
		return &config.StrategyOutbound{
			Type:      "urltest",
			Tag:       "auto-test",
			Outbounds: outboundTags,
			URL:       "https://www.gstatic.com/generate_204",
			Interval:  "3m",
			Tolerance: 50,
		}
	case string(model.StrategySelector):
		def := outboundTags[0]
		return &config.StrategyOutbound{
			Type:      "selector",
			Tag:       "select",
			Outbounds: outboundTags,
			Default:   def,
		}
	default:
		return nil
	}
}

// BuildDNSWithDetour создаёт DNS-секцию с detour через outbound цепочки.
func BuildDNSWithDetour(chainOutboundTag string, directDomains []string) *config.DNSConfig {
	dnsServers := []config.DNSServer{
		{Tag: "dns-chain", Type: "tls", Server: "1.1.1.1", Detour: chainOutboundTag},
		{Tag: "dns-direct", Type: "udp", Server: "8.8.8.8", Detour: "direct-out"},
	}
	var dnsRules []config.DNSRule
	if len(directDomains) > 0 {
		dnsRules = append(dnsRules, config.DNSRule{
			DomainSuffix: directDomains,
			Server:       "dns-direct",
		})
	}
	return &config.DNSConfig{
		Servers: dnsServers,
		Rules:   dnsRules,
		Final:   "dns-chain",
	}
}

// BuildDNSSection создаёт DNS-секцию (sing-box 1.12+ non-legacy формат).
func BuildDNSSection(chainOutboundTag string) *config.DNSConfig {
	return &config.DNSConfig{
		Servers: []config.DNSServer{
			{
				Tag:    "dns-remote",
				Type:   "tls",
				Server: "1.1.1.1",
				Detour: chainOutboundTag,
			},
			{
				Tag:    "dns-local",
				Type:   "udp",
				Server: "8.8.8.8",
				Detour: "direct-out",
			},
		},
		Rules: []config.DNSRule{
			{
				DomainSuffix: []string{".ru", ".su", ".рф", ".ir", ".cn"},
				Server:       "dns-local",
			},
		},
		Final: "dns-remote",
	}
}
