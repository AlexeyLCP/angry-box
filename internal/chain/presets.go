package chain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/alexeylcp/angry-box/internal/domain/model"
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

// RoutingSection описывает сгенерированную секцию route для sing-box конфига.
type RoutingSection struct {
	Rules                  []RouteRuleEntry `json:"rules"`
	RuleSet                []RuleSetEntry   `json:"rule_set,omitempty"`
	Final                  string           `json:"final,omitempty"`
	AutoDetectInterface    bool             `json:"auto_detect_interface,omitempty"`
	DefaultDomainResolver  string           `json:"default_domain_resolver,omitempty"`
}

// RouteRuleEntry — одно правило маршрутизации.
type RouteRuleEntry struct {
	Inbound      []string `json:"inbound,omitempty"`
	Outbound     string   `json:"outbound"`
	GeoIP        []string `json:"geoip,omitempty"`
	GeoSite      []string `json:"geosite,omitempty"`
	DomainSuffix []string `json:"domain_suffix,omitempty"`
	RuleSet      []string `json:"rule_set,omitempty"`
}

// RuleSetEntry — удалённый набор правил (SRS).
type RuleSetEntry struct {
	Tag            string `json:"tag"`
	Type           string `json:"type"`
	Format         string `json:"format"`
	URL            string `json:"url"`
	DownloadDetour string `json:"download_detour,omitempty"`
	UpdateInterval string `json:"update_interval,omitempty"`
}

// ruleSetBaseURL — базовый URL для SRS-файлов sing-box.
const ruleSetBaseURL = "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set"
const ruleSetGeoSiteURL = "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set"

// BuildRoutingSection создаёт полноценную routing-секцию на основе пресета и имени цепочки.
func BuildRoutingSection(preset *ConnectionPreset, chainOutboundTag string) RoutingSection {
	section := RoutingSection{
		Rules:                 []RouteRuleEntry{},
		Final:                 chainOutboundTag,
		AutoDetectInterface:   true,
		DefaultDomainResolver: "dns-local",
	}

	ruleTags := map[string]bool{}

	// Direct geoip rules
	for _, geo := range preset.Routing.DirectGeoIP {
		tag := "geoip-" + geo
		ruleTags[tag] = true
		section.Rules = append(section.Rules, RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "direct-out",
		})
	}

	// Direct geosite rules
	for _, gs := range preset.Routing.DirectGeoSite {
		tag := gs
		ruleTags[tag] = true
		section.Rules = append(section.Rules, RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "direct-out",
		})
	}

	// Direct domain suffixes
	if len(preset.Routing.DirectDomains) > 0 {
		section.Rules = append(section.Rules, RouteRuleEntry{
			DomainSuffix: preset.Routing.DirectDomains,
			Outbound:     "direct",
		})
	}

	// Block rules
	for _, gs := range preset.Routing.BlockGeoSite {
		tag := gs
		ruleTags[tag] = true
		section.Rules = append(section.Rules, RouteRuleEntry{
			RuleSet:  []string{tag},
			Outbound: "block",
		})
	}

	// Build rule_set entries
	for tag := range ruleTags {
		entry := RuleSetEntry{
			Tag:    tag,
			Type:   "remote",
			Format: "binary",
		}

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

// BuildDNSSection создаёт DNS-секцию (sing-box 1.12+ non-legacy формат).
func BuildDNSSection(chainOutboundTag string) map[string]any {
	return map[string]any{
		"servers": []map[string]any{
			{
				"tag":    "dns-remote",
				"type":   "tls",
				"server": "1.1.1.1",
				"detour": chainOutboundTag,
			},
			{
				"tag":    "dns-local",
				"type":   "udp",
				"server": "8.8.8.8",
				"detour": "direct-out",
			},
		},
		"rules": []map[string]any{
			{
				"domain_suffix": []string{".ru", ".su", ".рф", ".ir", ".cn"},
				"server":        "dns-local",
			},
		},
		"final": "dns-remote",
	}
}
