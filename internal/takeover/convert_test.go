package takeover

import (
	"encoding/json"
	"strings"
	"testing"
)

// realisticXrayVLESSRealityXHTTP mirrors configs/original-xray-config.json's
// vless-ha inbound (the production takeover target).
const realisticXrayVLESSRealityXHTTP = `{
  "inbounds": [
    {
      "port": 54321,
      "protocol": "vless",
      "tag": "vless-ha",
      "settings": {
        "clients": [{"id":"002799e0-c94d-4952-b4df-3f508c17235b","flow":"","email":"1rv1jvxc"}],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "xhttp",
        "xhttpSettings": {"path":"/","host":"","mode":"auto"},
        "security": "reality",
        "realitySettings": {
          "target":"127.0.0.1:5443",
          "serverNames":["ozon.ru"],
          "privateKey":"UOKjZ21vV-aQ1tYfV5ajrOHDlbuTn0nx6PjvsxfMAnQ",
          "shortIds":["ad89eccd","b4784f","7a","460cbbeccdd2a7b3"]
        }
      }
    }
  ]
}`

// TestConvertXrayConfig_VLESSRealityXHTTP verifies the primary takeover case:
// a vless+reality+xhttp Xray inbound is converted to a vless-reality
// NodeInbound preserving UUID/port/privateKey/shortId/sni.
func TestConvertXrayConfig_VLESSRealityXHTTP(t *testing.T) {
	det := &Detection{Type: DetectedXray, ConfigContent: realisticXrayVLESSRealityXHTTP}
	inbounds, extra, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 native inbound, got %d (extra=%d)", len(inbounds), len(extra))
	}
	ni := inbounds[0]
	if ni.Protocol != "vless-reality" {
		t.Errorf("Protocol: got %q, want vless-reality", ni.Protocol)
	}
	if ni.Port != 54321 {
		t.Errorf("Port: got %d, want 54321", ni.Port)
	}
	if ni.UUID != "002799e0-c94d-4952-b4df-3f508c17235b" {
		t.Errorf("UUID: got %q", ni.UUID)
	}
	if ni.ServerPrivKey != "UOKjZ21vV-aQ1tYfV5ajrOHDlbuTn0nx6PjvsxfMAnQ" {
		t.Errorf("ServerPrivKey (reality private): got %q", ni.ServerPrivKey)
	}
	if ni.ShortID != "ad89eccd" {
		t.Errorf("ShortID: got %q, want ad89eccd", ni.ShortID)
	}
	if ni.Obfuscation != "ozon.ru" {
		t.Errorf("Obfuscation (sni): got %q, want ozon.ru", ni.Obfuscation)
	}
	if ni.Source != "takeover:xray" {
		t.Errorf("Source: got %q", ni.Source)
	}
}

// TestConvertXrayConfig_SplithttpNormalized verifies Xray "splithttp" network
// is handled (not crashed) — it should still produce a vless-reality inbound.
func TestConvertXrayConfig_SplithttpNormalized(t *testing.T) {
	cfg := strings.Replace(realisticXrayVLESSRealityXHTTP, `"network": "xhttp"`, `"network": "splithttp"`, 1)
	cfg = strings.Replace(cfg, `"xhttpSettings"`, `"splithttpSettings"`, 1)
	det := &Detection{Type: DetectedXray, ConfigContent: cfg}
	inbounds, _, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert splithttp: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].Protocol != "vless-reality" {
		t.Errorf("splithttp should convert to vless-reality: %+v", inbounds)
	}
}

// TestConvertXrayConfig_VMESS verifies a vmess inbound becomes raw sing-box JSON.
func TestConvertXrayConfig_VMESS(t *testing.T) {
	cfg := `{"inbounds":[{"port":8443,"protocol":"vmess","settings":{"clients":[{"id":"abcd-vmess-uuid","email":"alice"}]}}]}`
	det := &Detection{Type: DetectedXray, ConfigContent: cfg}
	inbounds, extra, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(inbounds) != 0 {
		t.Errorf("vmess should be extra (no native case), got native inbounds: %+v", inbounds)
	}
	if len(extra) != 1 {
		t.Fatalf("expected 1 extra raw inbound, got %d", len(extra))
	}
	var inb map[string]any
	if err := json.Unmarshal(extra[0], &inb); err != nil {
		t.Fatal(err)
	}
	if inb["type"] != "vmess" {
		t.Errorf("extra type: got %v, want vmess", inb["type"])
	}
	if int(inb["listen_port"].(float64)) != 8443 {
		t.Errorf("listen_port: %v", inb["listen_port"])
	}
}

// TestConvertXrayConfig_MTProto verifies the ee+secret+hex(domain) composition.
func TestConvertXrayConfig_MTProto(t *testing.T) {
	cfg := `{"inbounds":[{"port":443,"protocol":"mtproto","settings":{"users":[{"secret":"dd83b231c9ccf32ef09f48c8f63765ab4f"}]}}]}`
	det := &Detection{Type: DetectedXray, ConfigContent: cfg}
	_, extra, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(extra) != 1 {
		t.Fatalf("expected 1 extra, got %d", len(extra))
	}
	var inb map[string]any
	json.Unmarshal(extra[0], &inb)
	if inb["type"] != "mtproto" {
		t.Errorf("type: %v", inb["type"])
	}
	users := inb["users"].([]any)
	secret := users[0].(map[string]any)["secret"].(string)
	if !strings.HasPrefix(secret, "ee") {
		t.Errorf("mtproto secret should be ee-prefixed: %s", secret)
	}
	if !strings.Contains(secret, "83b231c9ccf32ef09f48c8f63765ab4f") {
		t.Errorf("mtproto secret should contain the raw secret hex: %s", secret)
	}
}

// TestConvertSingBoxConfig_MTProtoDecode verifies an existing sing-box mtproto
// inbound's ee-secret is decoded back to (secretHex, domain).
func TestConvertSingBoxConfig_MTProtoDecode(t *testing.T) {
	// ee + 83b231c9ccf32ef09f48c8f63765ab4f + hex("disk.yandex.ru")
	cfg := `{"inbounds":[{"type":"mtproto","tag":"mtp-in","listen_port":443,"users":[{"name":"u1","secret":"ee83b231c9ccf32ef09f48c8f63765ab4f6469736b2e79616e6465782e7275"}]}]}`
	det := &Detection{Type: DetectedSingBox, ConfigContent: cfg}
	inbounds, _, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(inbounds))
	}
	ni := inbounds[0]
	if ni.Protocol != "mtproxy" {
		t.Errorf("Protocol: %q", ni.Protocol)
	}
	if ni.UUID != "83b231c9ccf32ef09f48c8f63765ab4f" {
		t.Errorf("secretHex (UUID): %q", ni.UUID)
	}
	if ni.Obfuscation != "disk.yandex.ru" {
		t.Errorf("FakeTLS domain (Obfuscation): %q", ni.Obfuscation)
	}
}

// TestConvertSingBoxConfig_VLESSReality verifies an existing sing-box vless
// reality inbound round-trips into a vless-reality NodeInbound.
func TestConvertSingBoxConfig_VLESSReality(t *testing.T) {
	cfg := `{"inbounds":[{"type":"vless","tag":"vless-in","listen_port":443,"users":[{"name":"u","uuid":"abc-uuid","flow":"xtls-rprx-vision"}],"tls":{"server_name":"www.microsoft.com","reality":{"private_key":"privkey123","short_id":["sid1","sid2"]}}}]}`
	det := &Detection{Type: DetectedSingBox, ConfigContent: cfg}
	inbounds, _, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(inbounds) != 1 {
		t.Fatalf("expected 1, got %d", len(inbounds))
	}
	ni := inbounds[0]
	if ni.Protocol != "vless-reality" || ni.UUID != "abc-uuid" || ni.ServerPrivKey != "privkey123" || ni.ShortID != "sid1" || ni.Obfuscation != "www.microsoft.com" {
		t.Errorf("converted inbound wrong: %+v", ni)
	}
}

// TestConvertMTProxyConfig_RawHexSecret verifies a telemt-style raw hex secret
// is composed into the ee+secret+hex(domain) form.
func TestConvertMTProxyConfig_RawHexSecret(t *testing.T) {
	cfg := `# telemt config
secret = "83b231c9ccf32ef09f48c8f63765ab4f"
`
	det := &Detection{Type: DetectedMTProxy, ConfigContent: cfg}
	inbounds, extra, err := Convert(det)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if len(inbounds) != 1 || inbounds[0].UUID != "83b231c9ccf32ef09f48c8f63765ab4f" {
		t.Errorf("inbound: %+v", inbounds)
	}
	if len(extra) != 1 {
		t.Fatalf("expected 1 extra raw inbound, got %d", len(extra))
	}
}

// TestConvertMTProxyConfig_UnsupportedFormat verifies a non-hex secret yields
// an explicit error (not a silent success).
func TestConvertMTProxyConfig_UnsupportedFormat(t *testing.T) {
	cfg := `secret = "not-hex-at-all"`
	det := &Detection{Type: DetectedMTProxy, ConfigContent: cfg}
	_, _, err := Convert(det)
	if err == nil {
		t.Error("expected error for non-hex secret")
	}
	if !strings.Contains(err.Error(), "no 32-hex secret") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestConvert_NoneDetected verifies nothing-to-convert returns an error.
func TestConvert_NoneDetected(t *testing.T) {
	det := &Detection{Type: DetectedNone}
	_, _, err := Convert(det)
	if err == nil {
		t.Error("expected error for none detected")
	}
}