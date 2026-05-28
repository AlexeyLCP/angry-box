package chain

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

//go:embed default_presets.json
var defaultPresetsJSON []byte

var (
	presetsMu sync.RWMutex
	presets   = make(map[string]ConnectionPreset)
	rng       = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// IntRange supports JSON number (fixed) or [min, max] array.
// On unmarshal from range it immediately samples a concrete value (good for presets).
// For stable per-chain values (esp. entry AWG CPS), generation happens later in applier/cps.
type IntRange int

func (ir *IntRange) UnmarshalJSON(b []byte) error {
	// Try number first
	var v int
	if err := json.Unmarshal(b, &v); err == nil {
		*ir = IntRange(v)
		return nil
	}
	// Try [min, max]
	var arr []int
	if err := json.Unmarshal(b, &arr); err == nil && len(arr) == 2 && arr[0] <= arr[1] {
		if arr[0] == arr[1] {
			*ir = IntRange(arr[0])
			return nil
		}
		*ir = IntRange(arr[0] + rng.Intn(arr[1]-arr[0]+1))
		return nil
	}
	return fmt.Errorf("IntRange: invalid value %s (want number or [min,max])", string(b))
}

func (ir IntRange) MarshalJSON() ([]byte, error) {
	return json.Marshal(int(ir))
}

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

// AWGPreset — настройки для AmneziaWG (AmneziaWG 2.0 + CPS/I1-I5 from pumbaX best practices 2026).
// Fields accept either fixed number or [min, max] in JSON (sampled at load for variety across restarts).
// For entry-node AWG, stable I1..I5 + server key are generated once at chain creation (stored on Chain)
// so client configs never break on re-apply.
type AWGPreset struct {
	JC   IntRange `json:"jc"`
	JMIN IntRange `json:"jmin"`
	JMAX IntRange `json:"jmax"`
	S1   IntRange `json:"s1"`
	S2   IntRange `json:"s2"`
	S3   IntRange `json:"s3,omitempty"`
	S4   IntRange `json:"s4,omitempty"`
	H1   IntRange `json:"h1"`
	H2   IntRange `json:"h2"`
	H3   IntRange `json:"h3"`
	H4   IntRange `json:"h4"`

	// CPS / I1-I5 hints (0 = off/"packet":"none", 1 = only I1, 2 = I1 + random later, 3 = full I1-I5)
	CPSLevel int    `json:"cps_level,omitempty"`
	Mimicry  string `json:"mimicry,omitempty"` // "quic" (recommended for RU), "sip", "dns"
}

// ConnectionPreset — основной составной пресет
type ConnectionPreset struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Reality     *RealityPreset `json:"reality,omitempty"`
	XHTTP       *XHTTPPreset   `json:"xhttp,omitempty"`
	TUIC        *TUICPreset    `json:"tuic,omitempty"`
	AWG         *AWGPreset     `json:"awg,omitempty"`
}

// Concrete returns the scalar AWG parameters after any range sampling done at JSON load.
// Safe for nil (returns conservative defaults).
func (a *AWGPreset) Concrete() (jc, jmin, jmax, s1, s2, s3, s4, h1, h2, h3, h4 int) {
	if a == nil {
		return 4, 40, 70, 0, 0, 0, 0, 1, 2, 3, 4
	}
	return int(a.JC), int(a.JMIN), int(a.JMAX), int(a.S1), int(a.S2), int(a.S3), int(a.S4), int(a.H1), int(a.H2), int(a.H3), int(a.H4)
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
