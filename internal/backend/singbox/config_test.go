package singbox

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func resetDefaultProfile(t *testing.T) {
	t.Helper()
	chain.SetDefaultProfile("maximum_stealth_2026")
}

// TestGenerateConfig_Transport_DefaultIsRealityXHTTP: the CLI `config -type
// transport` path now renders VLESS REALITY+XHTTP max obfuscation (the unified
// role renderer), regardless of the legacy "transport" profile — no more fake
// configs.
func TestGenerateConfig_Transport_DefaultIsRealityXHTTP(t *testing.T) {
	resetDefaultProfile(t)
	b := New()
	cfg, err := b.GenerateConfig(model.ConfigTransport, model.ConfigParams{Port: 443})
	if err != nil {
		t.Fatalf("GenerateConfig transport: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cfg.Content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	inb := parsed["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "vless" {
		t.Errorf("expected vless, got %v", inb["type"])
	}
	transport := inb["transport"].(map[string]any)
	if transport["type"] != "xhttp" {
		t.Errorf("expected xhttp transport, got %v", transport["type"])
	}
	if transport["x_padding_method"] != "tokenish" {
		t.Errorf("expected tokenish padding, got %v", transport["x_padding_method"])
	}
}

// TestGenerateConfig_User_AWG: CLI `config -type user -protocol awg` renders a
// real userspace AWG wireguard endpoint (with amnezia), NOT a fake tun+direct
// server config. This is the bug the refactor fixed.
func TestGenerateConfig_User_AWG(t *testing.T) {
	resetDefaultProfile(t)
	b := New()
	cfg, err := b.GenerateConfig(model.ConfigUser, model.ConfigParams{
		Port: 8443, Protocol: "awg",
	})
	if err != nil {
		t.Fatalf("GenerateConfig user AWG: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cfg.Content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	eps, ok := parsed["endpoints"].([]any)
	if !ok || len(eps) == 0 {
		t.Fatal("AWG user config must have a wireguard endpoint (not a tun inbound)")
	}
	ep := eps[0].(map[string]any)
	if ep["type"] != "wireguard" {
		t.Errorf("endpoint type: got %v, want wireguard", ep["type"])
	}
}

// TestGenerateConfig_User_TUIC: CLI `config -type user -protocol tuic` renders
// a REAL TUIC inbound (not a wireguard/AWG endpoint as it did before the fix).
func TestGenerateConfig_User_TUIC(t *testing.T) {
	resetDefaultProfile(t)
	b := New()
	cfg, err := b.GenerateConfig(model.ConfigUser, model.ConfigParams{
		Port: 8443, Protocol: "tuic",
	})
	if err != nil {
		t.Fatalf("GenerateConfig user TUIC: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(cfg.Content), &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	inb := parsed["inbounds"].([]any)[0].(map[string]any)
	if inb["type"] != "tuic" {
		t.Fatalf("expected tuic inbound, got %v (the pre-refactor bug was tuic->wireguard)", inb["type"])
	}
	// TUIC users must carry uuid + password.
	users := inb["users"].([]any)
	if len(users) == 0 {
		t.Fatal("tuic inbound has no users")
	}
	u := users[0].(map[string]any)
	if u["uuid"] == nil || u["password"] == nil {
		t.Error("tuic user missing uuid/password")
	}
}

// TestGenerateConfig_User_DefaultIsRealityXHTTP: with no explicit protocol,
// the user config falls back to VLESS REALITY+XHTTP (not AWG as before).
func TestGenerateConfig_User_DefaultIsRealityXHTTP(t *testing.T) {
	resetDefaultProfile(t)
	b := New()
	cfg, err := b.GenerateConfig(model.ConfigUser, model.ConfigParams{Port: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.Content, `"type": "vless"`) {
		t.Errorf("default user config should be vless reality+xhttp, got: %s", cfg.Content[:200])
	}
	if !strings.Contains(cfg.Content, `"reality"`) {
		t.Error("default user config missing reality")
	}
}

// TestGenerateConfig_AllProfilesProduceValidJSON ensures every profile still
// yields valid JSON through the new role renderer.
func TestGenerateConfig_AllProfilesProduceValidJSON(t *testing.T) {
	resetDefaultProfile(t)
	b := New()
	for _, prof := range []string{"russia_2026", "iran_2026", "china_2026", "maximum_stealth_2026"} {
		chain.SetDefaultProfile(prof)
		cfg, err := b.GenerateConfig(model.ConfigTransport, model.ConfigParams{Port: 443})
		if err != nil {
			t.Errorf("profile %s: %v", prof, err)
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(cfg.Content), &v); err != nil {
			t.Errorf("profile %s: invalid JSON: %v", prof, err)
		}
	}
}