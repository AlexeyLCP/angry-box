package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	// Security default: bind to the loopback interface only. Exposing the
	// control plane (which carries SSH private keys and can push configs that
	// become RCE on the fleet) on all interfaces over plain HTTP is unsafe.
	// Operators who need remote access must opt in via --listen / listen_addr
	// and ideally front it with TLS.
	if cfg.ListenAddr != "127.0.0.1:9080" {
		t.Errorf("unexpected default listen addr %q (want 127.0.0.1:9080)", cfg.ListenAddr)
	}
	if cfg.DefaultObfuscationProfile == "" {
		t.Error("default obfuscation profile should be set")
	}
}

func TestLoad_NonExistentFileReturnsDefault(t *testing.T) {
	cfg, err := Load("/this/path/does/not/exist/angry-box.toml")
	if err != nil {
		t.Fatalf("Load non-existent file should not error: %v", err)
	}
	if cfg.DefaultObfuscationProfile != DefaultConfig().DefaultObfuscationProfile {
		t.Error("should return default when file missing")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")

	content := `
listen_addr = ":9999"
default_obfuscation_profile = "iran_2026"
presets_file = "/etc/custom.json"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddr != ":9999" {
		t.Error("listen_addr not loaded")
	}
	if cfg.DefaultObfuscationProfile != "iran_2026" {
		t.Error("profile not loaded")
	}
	if cfg.PresetsFile != "/etc/custom.json" {
		t.Error("presets_file not loaded")
	}
}

// TestSave_FilePermissions0600 verifies that the config file — which carries
// the admin password bcrypt hash — is written with restrictive permissions so
// other users on the host cannot read it (CTO-review M2). On Windows the POSIX
// permission bits are not meaningful, so the mode assertion is unix-only.
func TestSave_FilePermissions0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "angry-box.toml")
	cfg := DefaultConfig()
	cfg.AuthPasswordHash = "$2a$10$dummyhash"
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if runtime.GOOS == "windows" {
		return // POSIX mode bits do not apply on Windows
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("config file perms = %o, want 0600 (it stores the admin password hash)", mode)
	}
}
