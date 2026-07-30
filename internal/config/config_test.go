package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// The store default must be a canonical absolute path (root-aware), NOT the
	// legacy CWD-relative "store.json" — two daemons with different cwd must
	// converge on the same file (split-brain root cause, AGENTS store-path note).
	if cfg.StoreFile == "" || cfg.StoreFile == "store.json" {
		t.Errorf("default store file should be a canonical absolute path, got %q", cfg.StoreFile)
	}
	if !filepath.IsAbs(cfg.StoreFile) {
		// Only acceptable in a HOME-less fallback environment; flag it so a
		// regression to a bare relative literal is caught.
		t.Errorf("default store file should be absolute, got %q", cfg.StoreFile)
	}
	if !strings.Contains(cfg.StoreFile, "angry-box") {
		t.Errorf("default store file should live under an angry-box dir, got %q", cfg.StoreFile)
	}
}

// TestDefaultStorePath_Absolute pins the root-aware resolution: on a normal
// dev/CI environment (HOME set, not a Windows fallback) the path is absolute
// and names the store.json under an angry-box data dir. The exact prefix varies
// by euid/HOME (root → /var/lib, user → ~/.local/share or XDG), so we assert
// shape, not a specific prefix.
func TestDefaultStorePath_Absolute(t *testing.T) {
	p := DefaultStorePath()
	if p == "" {
		t.Fatal("DefaultStorePath returned empty")
	}
	// Fallback "store.json" is only acceptable when HOME is unset — rare in
	// tests. If HOME is set, we expect an absolute path.
	if home := os.Getenv("HOME"); home != "" && runtime.GOOS != "windows" {
		if !filepath.IsAbs(p) {
			t.Errorf("DefaultStorePath = %q, want absolute when HOME is set", p)
		}
		if !strings.HasSuffix(p, "store.json") {
			t.Errorf("DefaultStorePath = %q, want it to end in store.json", p)
		}
	}
}

func TestLoad_NonExistentFileReturnsDefault(t *testing.T) {
	// Use a platform-portable non-existent path (under the test temp dir).
	// The previous "/this/path/does/not/exist/..." Unix-absolute path is
	// unreliable on Windows where MSYS path translation can make it resolve
	// to an existing file, which would trip the unknown-field strict check.
	path := filepath.Join(t.TempDir(), "definitely-nonexistent-angry-box.toml")
	cfg, err := Load(path)
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

// TestLoad_RejectsUnknownFields verifies the strict-mode unknown-field
// rejection: a typo in a config key (e.g. auth_passord_hash) must surface as a
// hard error at startup rather than being silently ignored and dropping the
// panel to stale defaults (CTO-review §2/§8).
func TestLoad_RejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "typo.toml")
	content := `
listen_addr = ":9999"
default_backend = "sing-box"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for unknown field 'default_backend', got nil")
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
