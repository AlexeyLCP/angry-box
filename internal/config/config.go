package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/bcrypt"
)

// Config holds runtime settings for the angry-box orchestrator itself.
type Config struct {
	ListenAddr string `toml:"listen_addr"`
	StoreFile  string `toml:"store_file"`

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
		// Security default: bind to loopback only. The control plane carries SSH
		// private keys and can push configs that become RCE on the fleet, so it
		// must not be exposed on all interfaces over plain HTTP by default.
		// Operators who need remote access must explicitly opt in via
		// --listen / listen_addr (ideally fronted by TLS).
		ListenAddr:                "127.0.0.1:9080",
		StoreFile:                 DefaultStorePath(),
		DefaultObfuscationProfile: "maximum_stealth_2026", // безопасный дефолт
		PresetsFile:               "",                     // no extra presets by default
		AuthEnabled:               true,                   // by default, authentication is enabled
		AuthUsername:              "admin",
	}
}

// DefaultStorePath returns the canonical absolute default location for the
// store file. It is root-aware so that two `angry-box serve` invocations — one
// from systemd (cwd /var/lib/angry-box) and one launched by hand from a
// different directory — converge on the SAME file instead of silently using
// CWD-relative store.json and splitting the fleet's state (the root cause of a
// tester's "node won't connect" split-brain: two daemons, two stores, divergent
// user keys). An operator can still override with --file or the config store_file.
//
// Resolution:
//   - Linux/macOS, running as root (euid 0)    -> /var/lib/angry-box/store.json
//   - Linux/macOS, non-root                    -> $XDG_DATA_HOME/angry-box/store.json,
//                                                 else $HOME/.local/share/angry-box/store.json
//   - Windows                                  -> %APPDATA%/angry-box/store.json
//   - fallback (no HOME / resolution failure)  -> store.json (relative, CWD) — legacy
//
// The directory is created lazily by the caller (NewStore / instance lock).
func DefaultStorePath() string {
	switch runtime.GOOS {
	case "windows":
		if dir, err := os.UserConfigDir(); err == nil && dir != "" {
			return filepath.Join(dir, "angry-box", "store.json")
		}
	default: // linux, darwin, *bsd, etc.
		if os.Geteuid() == 0 {
			return "/var/lib/angry-box/store.json"
		}
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "angry-box", "store.json")
		}
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, ".local", "share", "angry-box", "store.json")
		}
	}
	// Last-resort fallback: keep the legacy relative behavior so a totally
	// environment-less invocation still runs (store next to the binary).
	return "store.json"
}

// Load loads configuration from the given path (TOML).
// If the file does not exist, it returns DefaultConfig.
//
// Unknown fields are rejected: a typo in a config key (e.g. auth_passord_hash)
// would otherwise be silently ignored and the panel could start with a stale
// value (e.g. empty password hash → silent regeneration). DisallowUnknownFields
// surfaces such typos as a hard error at startup so the operator fixes the
// config instead of debugging silent fallback behaviour (CTO-review §2/§8).
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	fileExtisted := true
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fileExtisted = false
	} else {
		f, err := os.Open(path)
		if err != nil {
			// Race guard: file existed at stat but is gone now, or unreadable.
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
		dec := toml.NewDecoder(f)
		md, err := dec.Decode(cfg)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
		// Reject unknown top-level keys: a typo in a config key (e.g.
		// auth_passord_hash) would otherwise be silently ignored and the
		// panel could start with a stale value (e.g. empty password hash →
		// silent regeneration). BurntSushi/toml v1.6.0 has no
		// DisallowUnknownFields; emulate it via MetaData.Undecoded()
		// (CTO-review §2/§8).
		if undec := md.Undecoded(); len(undec) > 0 {
			names := make([]string, 0, len(undec))
			for _, k := range undec {
				names = append(names, k.String())
			}
			return nil, fmt.Errorf("config %s: unknown field(s): %s", path, strings.Join(names, ", "))
		}
	}

	// Apply some sane fallbacks
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultConfig().ListenAddr
	}
	if cfg.StoreFile == "" {
		cfg.StoreFile = DefaultConfig().StoreFile
	}
	if cfg.DefaultObfuscationProfile == "" {
		cfg.DefaultObfuscationProfile = DefaultConfig().DefaultObfuscationProfile
	}
	if cfg.AuthUsername == "" {
		cfg.AuthUsername = "admin"
	}

	needsSave := !fileExtisted

	// Если аутентификация включена, но пароль не задан, сгенерируем случайный.
	if cfg.AuthEnabled && cfg.AuthPasswordHash == "" {
		b := make([]byte, 8)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("failed to generate admin password: %w", err)
		}
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
		// Persist the generated/normalized config. A failure here is not fatal
		// (the panel can still run with in-memory config), but the operator must
		// be warned — otherwise they would not know the config file is missing
		// or unwritable (CTO-review §2 silent-failure finding).
		if err := cfg.Save(path); err != nil {
			log.Printf("WARNING: could not save config to %s: %v (running with in-memory config)", path, err)
		}
	}

	return cfg, nil
}

// Save marshals the config back to TOML file.
//
// The file carries the admin password bcrypt hash, so it is created with mode
// 0600 (owner read/write only) to prevent other users on the host from reading
// the hash for offline brute-force (CTO-review M2). The directory is created
// with 0755 so it remains traversable.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		return err
	}
	// Chmod after write: an existing file may have been created with looser
	// perms earlier, and OpenFile only applies the mode on creation.
	return os.Chmod(path, 0o600)
}

// DefaultConfigPath returns the standard location for the orchestrator config.
// Portable by default: CWD on Windows and when no XDG_CONFIG_HOME is set, so the
// binary runs "from the desktop" without writing to /etc. System packagers can
// set XDG_CONFIG_HOME or pass --config explicitly.
func DefaultConfigPath() string {
	if runtime.GOOS == "windows" {
		return "angry-box.toml"
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "angry-box", "angry-box.toml")
	}
	return "angry-box.toml"
}
