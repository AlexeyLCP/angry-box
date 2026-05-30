package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/bcrypt"
)

// Config holds runtime settings for the angry-box orchestrator itself.
type Config struct {
	ListenAddr     string `toml:"listen_addr"`
	StoreFile      string `toml:"store_file"`
	DefaultBackend string `toml:"default_backend"`

	// DefaultObfuscationProfile — профиль обфускации по умолчанию.
	// Возможные значения: "russia_2026", "iran_2026", "china_2026", "maximum_stealth_2026"
	// Можно сменить в любой момент через Web UI или редактирование конфига.
	DefaultObfuscationProfile string `toml:"default_obfuscation_profile"`

	// PresetsFile — optional path to a JSON file with additional ConnectionPreset entries.
	// These are merged after the built-in ones (user presets win on name collision).
	// Useful for custom country profiles or lab testing.
	PresetsFile string `toml:"presets_file"`

	// Web UI Authentication
	AuthEnabled      bool   `toml:"auth_enabled"`
	AuthUsername     string `toml:"auth_username"`
	AuthPasswordHash string `toml:"auth_password_hash"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:                ":8090",
		StoreFile:                 "/etc/angry-box/store.json",
		DefaultBackend:            "sing-box",
		DefaultObfuscationProfile: "maximum_stealth_2026", // безопасный дефолт
		PresetsFile:               "",                     // no extra presets by default
		AuthEnabled:               true,                   // by default, authentication is enabled
		AuthUsername:              "admin",
	}
}

// Load loads configuration from the given path (TOML).
// If the file does not exist, it returns DefaultConfig.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, err
	}

	// Apply some sane fallbacks
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultConfig().ListenAddr
	}
	if cfg.StoreFile == "" {
		cfg.StoreFile = DefaultConfig().StoreFile
	}
	if cfg.DefaultBackend == "" {
		cfg.DefaultBackend = DefaultConfig().DefaultBackend
	}
	if cfg.DefaultObfuscationProfile == "" {
		cfg.DefaultObfuscationProfile = DefaultConfig().DefaultObfuscationProfile
	}
	if cfg.AuthUsername == "" {
		cfg.AuthUsername = "admin"
	}

	needsSave := false

	// Если аутентификация включена, но пароль не задан, сгенерируем случайный.
	if cfg.AuthEnabled && cfg.AuthPasswordHash == "" {
		b := make([]byte, 8)
		rand.Read(b)
		randomPass := hex.EncodeToString(b)
		
		hash, err := bcrypt.GenerateFromPassword([]byte(randomPass), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("failed to hash generated password: %w", err)
		}
		cfg.AuthPasswordHash = string(hash)
		needsSave = true
		
		log.Println("=========================================================")
		log.Println("WARNING: No admin password found in config.")
		log.Printf("Generated random password for '%s': %s\n", cfg.AuthUsername, randomPass)
		log.Println("Please save this password or change it in Settings -> Auth.")
		log.Println("=========================================================")
	}

	if needsSave {
		_ = cfg.Save(path)
	}

	return cfg, nil
}

// Save marshals the config back to TOML file.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// DefaultConfigPath returns the standard location for the orchestrator config.
func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "angry-box", "angry-box.toml")
	}
	return "/etc/angry-box/angry-box.toml"
}
