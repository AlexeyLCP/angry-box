package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds runtime settings for the angry-box orchestrator itself.
type Config struct {
	ListenAddr     string `toml:"listen_addr"`
	StoreFile      string `toml:"store_file"`
	DefaultBackend string `toml:"default_backend"`
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		ListenAddr:     ":8090",
		StoreFile:      "/etc/angry-box/store.json",
		DefaultBackend: "sing-box",
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

	return cfg, nil
}

// DefaultConfigPath returns the standard location for the orchestrator config.
func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "angry-box", "angry-box.toml")
	}
	return "/etc/angry-box/angry-box.toml"
}
