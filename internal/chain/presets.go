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
	CPSLevel int    `json:"cps_level,omitempty"`
	AWGMimicry string `json:"awg_mimicry,omitempty"`
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
