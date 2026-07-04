package singbox

// standalone_awg_test.go — tests for generateStandaloneNode (the per-protocol
// standalone config builder) and InstallAWGModule. CTO-review C3 phase 5.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestGenerateConfig_StandaloneNode_MissingInbounds verifies the error when no
// inbounds slice is passed in Extra.
func TestGenerateConfig_StandaloneNode_MissingInbounds(t *testing.T) {
	b := New(nil)
	_, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{})
	if err == nil {
		t.Fatal("expected missing-inbounds error")
	}
	if !strings.Contains(err.Error(), "inbounds") {
		t.Errorf("got %q, want inbounds error", err.Error())
	}
}

// TestGenerateConfig_StandaloneNode_VLESS verifies a vless-reality inbound
// produces valid JSON with the inbound.
func TestGenerateConfig_StandaloneNode_VLESS(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": []model.NodeInbound{
			{Protocol: "vless", Port: 8443, UUID: "uuid-1"},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("config is not valid JSON: %s", cfg.Content)
	}
	if !strings.Contains(cfg.Content, "vless") {
		t.Errorf("config has no vless inbound: %s", cfg.Content)
	}
}

// TestGenerateConfig_StandaloneNode_AWG verifies an awg inbound produces valid
// JSON with an endpoint.
func TestGenerateConfig_StandaloneNode_AWG(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": []model.NodeInbound{
			{Protocol: "awg", Port: 51820},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("config is not valid JSON: %s", cfg.Content)
	}
}

// TestGenerateConfig_StandaloneNode_TUIC verifies a tuic inbound produces valid
// JSON with a tuic inbound.
func TestGenerateConfig_StandaloneNode_TUIC(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": []model.NodeInbound{
			{Protocol: "tuic", Port: 443, UUID: "uuid-t", ServerPrivKey: "pw-t"},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("config is not valid JSON: %s", cfg.Content)
	}
	if !strings.Contains(cfg.Content, "tuic") {
		t.Errorf("config has no tuic inbound: %s", cfg.Content)
	}
}

// TestGenerateConfig_StandaloneNode_Hysteria2 verifies a hysteria2 inbound
// produces valid JSON.
func TestGenerateConfig_StandaloneNode_Hysteria2(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": []model.NodeInbound{
			{Protocol: "hysteria2", Port: 443, UUID: "secret", ObfsPassword: "obfs"},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("config is not valid JSON: %s", cfg.Content)
	}
}

// TestGenerateConfig_StandaloneNode_MTProxy verifies an mtproxy inbound produces
// valid JSON.
func TestGenerateConfig_StandaloneNode_MTProxy(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigStandaloneNode, model.ConfigParams{
		Extra: map[string]any{"inbounds": []model.NodeInbound{
			{Protocol: "mtproxy", Port: 443, UUID: "aabbccdd"},
		}},
	})
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("config is not valid JSON: %s", cfg.Content)
	}
}

// TestGenerateConfig_UnknownType verifies an unknown config type is rejected.
func TestGenerateConfig_UnknownType(t *testing.T) {
	b := New(nil)
	_, err := b.GenerateConfig(model.ConfigType(99), model.ConfigParams{})
	if err == nil {
		t.Fatal("expected unknown-type error")
	}
}

// TestGenerateConfig_Transport verifies the CLI transport path (vless-reality
// default) produces valid JSON.
func TestGenerateConfig_Transport(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigTransport, model.ConfigParams{Port: 443})
	if err != nil {
		t.Fatalf("GenerateConfig transport: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("transport config is not valid JSON: %s", cfg.Content)
	}
}

// TestGenerateConfig_Transport_AWG verifies the CLI AWG transport path.
func TestGenerateConfig_Transport_AWG(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigTransport, model.ConfigParams{Port: 51820, Protocol: "awg"})
	if err != nil {
		t.Fatalf("GenerateConfig AWG: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("AWG config is not valid JSON: %s", cfg.Content)
	}
}

// TestGenerateConfig_Transport_TUIC verifies the CLI TUIC transport path.
func TestGenerateConfig_Transport_TUIC(t *testing.T) {
	b := New(nil)
	cfg, err := b.GenerateConfig(model.ConfigTransport, model.ConfigParams{Port: 443, Protocol: "tuic"})
	if err != nil {
		t.Fatalf("GenerateConfig TUIC: %v", err)
	}
	if !jsonValid(cfg.Content) {
		t.Errorf("TUIC config is not valid JSON: %s", cfg.Content)
	}
}

// ─── InstallAWGModule ───────────────────────────────────────────────────────

// TestInstallAWGModule_AlreadyLoaded verifies the short-circuit when the kernel
// module is already loaded (only persist modules-load.d, no apt).
func TestInstallAWGModule_AlreadyLoaded(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "lsmod", out: "loaded"},
		fakeRule{substring: "modules-load.d/amneziawg.conf", out: ""},
		fakeRule{substring: "", out: ""},
	)
	b := New(&fakeConnector{client: fake})
	if err := b.InstallAWGModule(context.Background(), hostA); err != nil {
		t.Fatalf("InstallAWGModule: %v", err)
	}
	if fake.Saw("apt-get install -y -qq amneziawg") {
		t.Error("should have short-circuited, but ran amneziawg apt install")
	}
}

// TestInstallAWGModule_InstallSuccess verifies the PPA install path runs apt +
// persists modules-load.d and verifies awg-quick.
func TestInstallAWGModule_InstallSuccess(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "lsmod", outs: []string{"not_loaded", "loaded"}},
		fakeRule{substring: "Installing build prerequisites", out: ""},
		fakeRule{substring: "modules-load.d/amneziawg.conf", out: ""},
		fakeRule{substring: "awg-quick", out: "/usr/bin/awg-quick"},
		fakeRule{substring: "", out: ""},
	)
	b := New(&fakeConnector{client: fake})
	if err := b.InstallAWGModule(context.Background(), hostA); err != nil {
		t.Fatalf("InstallAWGModule: %v", err)
	}
	if !fake.Saw("apt-get install -y -qq amneziawg") {
		t.Error("amneziawg PPA apt install not run")
	}
	// Regression guard: the DKMS fallback must read PACKAGE_VERSION from
	// dkms.conf into AB_AWG_MODVER (not hardcode -v 1.0.0) so a tarball bump
	// doesn't silently break dkms add/build/install version matching.
	if !fake.Saw("AB_AWG_MODVER") || !fake.Saw("PACKAGE_VERSION") {
		t.Error("DKMS fallback missing dynamic AB_AWG_MODVER / PACKAGE_VERSION extraction")
	}
}

// TestInstallAWGModule_InstallFails verifies an install failure surfaces.
func TestInstallAWGModule_InstallFails(t *testing.T) {
	fake := newFakeSSH(
		fakeRule{substring: "lsmod", out: "not_loaded"},
		fakeRule{substring: "Installing build prerequisites", out: "", err: errAny},
		fakeRule{substring: "", out: ""},
	)
	b := New(&fakeConnector{client: fake})
	err := b.InstallAWGModule(context.Background(), hostA)
	if err == nil {
		t.Fatal("expected install failure")
	}
	if !strings.Contains(err.Error(), "amneziawg install failed") {
		t.Errorf("got %q, want install-failed", err.Error())
	}
}

// TestInstallAWGModule_ConnectFails verifies a connect failure surfaces.
func TestInstallAWGModule_ConnectFails(t *testing.T) {
	b := New(&fakeConnector{err: errors.New("no route")})
	if err := b.InstallAWGModule(context.Background(), hostA); err == nil {
		t.Fatal("expected connect failure")
	}
}

// jsonValid reports whether s is non-empty and parses as JSON.
func jsonValid(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	return json.Unmarshal([]byte(s), new(any)) == nil
}
