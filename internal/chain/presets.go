package chain

import (
	_ "embed"
	"encoding/json"
	"fmt"
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
	PaddingBytes    string `json:"padding_bytes,omitempty"`      // "min-max" or single value, e.g. "300-1800"
	Multiplex       bool   `json:"multiplex,omitempty"`
	MaxConcurrency  string `json:"max_concurrency,omitempty"`    // e.g. "4-48"
	UpstreamHost    string `json:"upstream_host,omitempty"`
	DownstreamHost  string `json:"downstream_host,omitempty"`
	UpstreamAlpn    string `json:"upstream_alpn,omitempty"`
	DownstreamAlpn  string `json:"downstream_alpn,omitempty"`
	StealthLevel    int    `json:"stealth_level,omitempty"`      // 0-3, drives mode/padding strength
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
	H1   int `json:"h1"`
	H2   int `json:"h2"`
	H3   int `json:"h3"`
	H4   int `json:"h4"`

	// 2026 advanced CPS / I1-I5 support (from pumbaX/awg2.sh best practices)
	CPSLevel int    `json:"cps_level,omitempty"`
	Mimicry  string `json:"mimicry,omitempty"` // "quic" | "sip" | "dns" | "none"

	// Optional I1 packet override (base64 or special keywords "quic-1200", "dns-1232")
	I1Packet string `json:"i1_packet,omitempty"`
}

// ConnectionPreset — основной составной пресет (2026 extended)
type ConnectionPreset struct {
	Name        string         `json:"name"`
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
	if err := LoadPresets(nil); err != nil {
		panic("failed to load default obfuscation presets: " + err.Error())
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

// GetEffectivePreset возвращает пресет, который следует использовать для данной цепочки.
// Приоритет: явный override на цепочке (chain.ObfuscationProfile) > глобальный дефолт из конфига.
func GetEffectivePreset(c *model.Chain) ConnectionPreset {
	if c != nil && c.ObfuscationProfile != "" {
		if p, ok := GetPreset(c.ObfuscationProfile); ok {
			return p
		}
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
			entry.URL = ruleSetGeoSiteURL + "/" + tag + ".srs"
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
