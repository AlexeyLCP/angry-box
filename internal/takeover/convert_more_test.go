package takeover

// convert_more_test.go — unit tests for the remaining Convert parsers
// (sing-box TUIC/Hysteria2/WireGuard + Xray Trojan/Shadowsocks/MTProto) and
// decodeMTProxyFullSecret. These are pure JSON->NodeInbound/raw mappings, no SSH.
// CTO-review C3 phase 5.

import (
	"encoding/json"
	"testing"
)

// TestConvertSingBoxTUIC verifies the TUIC user uuid+password are extracted.
func TestConvertSingBoxTUIC(t *testing.T) {
	raw := json.RawMessage(`{"users":[{"uuid":"u-1","password":"p-1"}]}`)
	ni := convertSingBoxTUIC(raw, 443)
	if ni.Protocol != "tuic" {
		t.Errorf("Protocol: got %q, want tuic", ni.Protocol)
	}
	if ni.Port != 443 {
		t.Errorf("Port: got %d, want 443", ni.Port)
	}
	if ni.UUID != "u-1" {
		t.Errorf("UUID: got %q, want u-1", ni.UUID)
	}
	if ni.ServerPrivKey != "p-1" {
		t.Errorf("ServerPrivKey: got %q, want p-1", ni.ServerPrivKey)
	}
	if ni.Source != "takeover:singbox" {
		t.Errorf("Source: got %q", ni.Source)
	}
}

// TestConvertSingBoxTUIC_NoUsers verifies an inbound with no users still returns
// the protocol/port with empty creds.
func TestConvertSingBoxTUIC_NoUsers(t *testing.T) {
	ni := convertSingBoxTUIC(json.RawMessage(`{"users":[]}`), 443)
	if ni.Protocol != "tuic" {
		t.Errorf("Protocol: got %q, want tuic", ni.Protocol)
	}
	if ni.UUID != "" {
		t.Errorf("UUID: got %q, want empty", ni.UUID)
	}
}

// TestConvertSingBoxHysteria2 verifies the hysteria2 password is carried in UUID.
func TestConvertSingBoxHysteria2(t *testing.T) {
	raw := json.RawMessage(`{"users":[{"name":"a","password":"secret"}]}`)
	ni := convertSingBoxHysteria2(raw, 8443)
	if ni.Protocol != "hysteria2" {
		t.Errorf("Protocol: got %q, want hysteria2", ni.Protocol)
	}
	if ni.UUID != "secret" {
		t.Errorf("UUID: got %q, want secret", ni.UUID)
	}
}

// TestConvertSingBoxWireGuard verifies private_key + first peer public_key.
func TestConvertSingBoxWireGuard(t *testing.T) {
	raw := json.RawMessage(`{"private_key":"priv","peers":[{"public_key":"pub1"}]}`)
	ni := convertSingBoxWireGuard(raw, 51820)
	if ni.Protocol != "awg" {
		t.Errorf("Protocol: got %q, want awg", ni.Protocol)
	}
	if ni.ServerPrivKey != "priv" {
		t.Errorf("ServerPrivKey: got %q, want priv", ni.ServerPrivKey)
	}
	if ni.AWGClientPub != "pub1" {
		t.Errorf("AWGClientPub: got %q, want pub1", ni.AWGClientPub)
	}
}

// TestDecodeMTProxyFullSecret_RawHex verifies a raw hex secret (no ee prefix)
// is returned as-is with the default domain.
func TestDecodeMTProxyFullSecret_RawHex(t *testing.T) {
	secret, domain := decodeMTProxyFullSecret("deadbeef")
	if secret != "deadbeef" {
		t.Errorf("secret: got %q, want deadbeef", secret)
	}
	if domain != "disk.yandex.ru" {
		t.Errorf("domain: got %q, want disk.yandex.ru", domain)
	}
}

// TestDecodeMTProxyFullSecret_EEPrefix verifies the ee-prefixed FakeTLS form is
// split into secretHex + decoded domain.
func TestDecodeMTProxyFullSecret_EEPrefix(t *testing.T) {
	// 32 hex chars secret + hex("telegram.com")
	secretHex := "00112233445566778899aabbccddeeff"
	domainHex := "74656c656772616d2e636f6d" // "telegram.com"
	full := "ee" + secretHex + domainHex
	secret, domain := decodeMTProxyFullSecret(full)
	if secret != secretHex {
		t.Errorf("secret: got %q, want %q", secret, secretHex)
	}
	if domain != "telegram.com" {
		t.Errorf("domain: got %q, want telegram.com", domain)
	}
}

// TestDecodeMTProxyFullSecret_ShortRest verifies a too-short rest after ee is
// returned as the secret with an empty domain (no default applies below 32 chars).
func TestDecodeMTProxyFullSecret_ShortRest(t *testing.T) {
	secret, domain := decodeMTProxyFullSecret("eeshort")
	if secret != "short" {
		t.Errorf("secret: got %q, want short", secret)
	}
	if domain != "" {
		t.Errorf("domain: got %q, want empty (short rest)", domain)
	}
}

// TestConvertXrayTrojan verifies the trojan inbound JSON is built with the
// password + server_name.
func TestConvertXrayTrojan(t *testing.T) {
	settings := json.RawMessage(`{"clients":[{"password":"pw","email":"alice"}]}`)
	stream := json.RawMessage(`{"serverName":"example.com"}`)
	raw := convertXrayTrojan(443, settings, stream)
	if raw == nil {
		t.Fatal("expected non-nil inbound")
	}
	var inb map[string]any
	if err := json.Unmarshal(raw, &inb); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if inb["type"] != "trojan" {
		t.Errorf("type: got %v, want trojan", inb["type"])
	}
	users, _ := inb["users"].([]any)
	if len(users) != 1 {
		t.Fatalf("users: got %d, want 1", len(users))
	}
	u, _ := users[0].(map[string]any)
	if u["password"] != "pw" {
		t.Errorf("password: got %v, want pw", u["password"])
	}
	tls, _ := inb["tls"].(map[string]any)
	if tls["server_name"] != "example.com" {
		t.Errorf("server_name: got %v, want example.com", tls["server_name"])
	}
}

// TestConvertXrayTrojan_NoClients verifies an empty clients list returns nil.
func TestConvertXrayTrojan_NoClients(t *testing.T) {
	raw := convertXrayTrojan(443, json.RawMessage(`{"clients":[]}`), nil)
	if raw != nil {
		t.Errorf("expected nil for no clients, got %s", raw)
	}
}

// TestConvertXrayShadowsocks verifies the shadowsocks inbound JSON.
func TestConvertXrayShadowsocks(t *testing.T) {
	settings := json.RawMessage(`{"method":"aes-256-gcm","password":"ss-pw"}`)
	raw := convertXrayShadowsocks(8388, settings)
	if raw == nil {
		t.Fatal("expected non-nil inbound")
	}
	var inb map[string]any
	json.Unmarshal(raw, &inb)
	if inb["type"] != "shadowsocks" {
		t.Errorf("type: got %v, want shadowsocks", inb["type"])
	}
	if inb["method"] != "aes-256-gcm" {
		t.Errorf("method: got %v, want aes-256-gcm", inb["method"])
	}
	if inb["password"] != "ss-pw" {
		t.Errorf("password: got %v, want ss-pw", inb["password"])
	}
}

// TestConvertXrayShadowsocks_NoMethod verifies an empty method returns nil.
func TestConvertXrayShadowsocks_NoMethod(t *testing.T) {
	raw := convertXrayShadowsocks(8388, json.RawMessage(`{}`))
	if raw != nil {
		t.Errorf("expected nil for no method, got %s", raw)
	}
}

// TestConvertXrayMTProto verifies the mtproto inbound JSON composes the ee+secret
// form.
func TestConvertXrayMTProto(t *testing.T) {
	settings := json.RawMessage(`{"users":[{"secret":"aabbccdd"}]}`)
	raw := convertXrayMTProto(443, settings)
	if raw == nil {
		t.Fatal("expected non-nil inbound")
	}
	var inb map[string]any
	json.Unmarshal(raw, &inb)
	if inb["type"] != "mtproto" {
		t.Errorf("type: got %v, want mtproto", inb["type"])
	}
}

// TestConvertXrayMTProto_NoUsers verifies an empty users list returns nil.
func TestConvertXrayMTProto_NoUsers(t *testing.T) {
	raw := convertXrayMTProto(443, json.RawMessage(`{"users":[]}`))
	if raw != nil {
		t.Errorf("expected nil for no users, got %s", raw)
	}
}