package chain

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

func TestXHTTPTransportJSONCompatibility(t *testing.T) {
	// 1. Setup mock data
	p := &hopParams{
		Port:       443,
		UUID:       "12345678-1234-1234-1234-123456789012",
		ServerName: "example.com",
		PrivateKey: "private_key_hex",
		ShortID:    "short_id_hex",
	}
	preset := &ConnectionPreset{
		XHTTP: &XHTTPPreset{
			Methods: []string{"GET"},
			Paths:   []string{"/api/v1/test"},
			Hosts:   []string{"example.com"},
			Headers: map[string][]string{
				"User-Agent": {"TestAgent"},
			},
		},
	}
	tag := "inbound-tag"

	// 2. Generate new JSON via typed structs
	actualJSON := buildXHTTPTransportInbound(p, tag, preset)

	// 3. Construct expected JSON via map[string]any (old way)
	transport := map[string]any{
		"type":         "http",
		"host":         []string{p.ServerName},
		"path":         preset.XHTTP.Paths[0],
		"method":       preset.XHTTP.Methods[0],
		"headers":      preset.XHTTP.Headers,
		"idle_timeout": "15s",
		"ping_timeout": "15s",
	}

	// Mocking ApplyXHTTPObfuscation logic manually or we can call it on the struct and map
	// We'll just call it on our manual map to ensure full parity
	oldTransportOpts := &config.TransportOptions{
		Type:        "http",
		Host:        []string{p.ServerName},
		Path:        preset.XHTTP.Paths[0],
		Method:      preset.XHTTP.Methods[0],
		Headers:     preset.XHTTP.Headers,
		IdleTimeout: "15s",
		PingTimeout: "15s",
	}
	ApplyXHTTPObfuscation(oldTransportOpts, preset.XHTTP)
	transport["headers"] = oldTransportOpts.Headers
	if oldTransportOpts.Extra != nil {
		transport["extra"] = oldTransportOpts.Extra
	}

	expectedInb := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": p.Port,
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": p.UUID,
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": p.ServerName,
			"reality": map[string]any{
				"enabled":     true,
				"handshake": map[string]any{
					"server":      p.ServerName,
					"server_port": 443,
				},
				"private_key": p.PrivateKey,
				"short_id":    []string{p.ShortID},
			},
		},
		"transport": transport,
	}
	
	expectedJSONBytes, _ := json.Marshal(expectedInb)
	expectedJSON := string(expectedJSONBytes)

	// 4. Compare
	var expectedMap, actualMap map[string]any
	json.Unmarshal([]byte(expectedJSON), &expectedMap)
	json.Unmarshal(actualJSON, &actualMap)

	// Re-marshal with formatting for better comparison error messages if they fail
	prettyExpected, _ := json.MarshalIndent(expectedMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actualMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestTransportInboundJSONParity(t *testing.T) {
	p := &hopParams{
		Port:       443,
		UUID:       "uuid-1234",
		ServerName: "test.com",
		PrivateKey: "priv-hex",
		ShortID:    "short-hex",
	}
	tag := "in-tag"
	actualJSON := buildTransportInbound(p, tag)

	expectedMap := map[string]any{
		"type": "vless",
		"tag":  tag,
		"listen":      "0.0.0.0",
		"listen_port": p.Port,
		"users": []map[string]any{
			{
				"name": tag,
				"uuid": p.UUID,
			},
		},
		"tls": map[string]any{
			"enabled": true,
			"server_name": p.ServerName,
			"reality": map[string]any{
				"enabled":     true,
				"handshake": map[string]any{
					"server":      p.ServerName,
					"server_port": 443,
				},
				"private_key": p.PrivateKey,
				"short_id":    []string{p.ShortID},
			},
		},
	}

	expectedJSONBytes, _ := json.Marshal(expectedMap)
	
	var expMap, actMap map[string]any
	json.Unmarshal(expectedJSONBytes, &expMap)
	json.Unmarshal(actualJSON, &actMap)

	prettyExpected, _ := json.MarshalIndent(expMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestTransportOutboundJSONParity(t *testing.T) {
	p := &hopParams{
		Port:       443,
		UUID:       "uuid-1234",
		ServerName: "test.com",
		PrivateKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA", // valid base64 key without padding
		ShortID:    "short-hex",
	}
	tag := "out-tag"
	addr := "1.2.3.4"
	actualJSON, _ := buildTransportOutbound(p, addr, tag)

	pubKeyHex, _ := p.publicKeyB64()

	expectedMap := map[string]any{
		"type":        "vless",
		"tag":         tag,
		"server":      addr,
		"server_port": p.Port,
		"uuid":        p.UUID,
		"tls": map[string]any{
			"enabled":     true,
			"server_name": p.ServerName,
			"utls": map[string]any{
				"enabled":     true,
				"fingerprint": "chrome",
			},
			"reality": map[string]any{
				"enabled":    true,
				"public_key": pubKeyHex,
				"short_id":   p.ShortID,
			},
		},
	}

	expectedJSONBytes, _ := json.Marshal(expectedMap)
	
	var expMap, actMap map[string]any
	json.Unmarshal(expectedJSONBytes, &expMap)
	json.Unmarshal(actualJSON, &actMap)

	prettyExpected, _ := json.MarshalIndent(expMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestUserInboundJSONParity(t *testing.T) {
	actualJSON := buildUserInbound(8443, "uuid-1234", "user-in")

	expectedMap := map[string]any{
		"type": "vless",
		"tag":  "user-in",
		"listen":      "0.0.0.0",
		"listen_port": 8443,
		"users": []map[string]any{
			{
				"name": "user-in",
				"uuid": "uuid-1234",
				"flow": "xtls-rprx-vision",
			},
		},
		"tls": map[string]any{
			"enabled": false,
		},
		"transport": map[string]any{
			"type": "ws",
			"path": "/ws",
		},
	}

	expectedJSONBytes, _ := json.Marshal(expectedMap)
	
	var expMap, actMap map[string]any
	json.Unmarshal(expectedJSONBytes, &expMap)
	json.Unmarshal(actualJSON, &actMap)

	prettyExpected, _ := json.MarshalIndent(expMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestDirectOutboundJSONParity(t *testing.T) {
	actualJSON := buildDirectOutbound("direct-out")
	expectedJSON := `{"tag":"direct-out","type":"direct"}`

	var expMap, actMap map[string]any
	json.Unmarshal([]byte(expectedJSON), &expMap)
	json.Unmarshal(actualJSON, &actMap)

	prettyExpected, _ := json.MarshalIndent(expMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestMergedNodeConfigParity(t *testing.T) {
	chain := &model.Chain{
		Name:  "test-chain",
		Nodes: []model.ChainNode{
			{ID: "node0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k",
				TransitUUID: "uuid-entry", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA", TransitShortID: "sh0"},
			{ID: "node1", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k",
				TransitUUID: "uuid-next", TransitPrivKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_BQB", TransitShortID: "sh1"},
		},
		Strategy:              model.StrategyURLTest,
		Transport:             model.TransportReality,
		UserProtocol:          model.UserProtocolTUIC,
		TUICEntryUserUUID:     "tuic-uuid",
		TUICEntryUserPassword: "tuic-pass",
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "node0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}}

	cfg, report, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{chain}, nil, nil)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig failed: %v", err)
	}
	if report == nil || cfg == nil {
		t.Fatal("nil report or cfg")
	}
	if len(cfg.Inbounds) < 1 {
		t.Fatal("expected at least 1 inbound")
	}
	if len(cfg.Outbounds) < 1 {
		t.Fatal("expected at least 1 outbound")
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	var generic map[string]any
	if err := json.Unmarshal(cfgJSON, &generic); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tags := extractAllTags(string(cfgJSON))
	hasTag := func(want string) bool {
		for _, tg := range tags {
			if tg == want { return true }
		}
		return false
	}
	if !hasTag("ch-test-chain-user-in") {
		t.Errorf("missing tag ch-test-chain-user-in, got %v", tags)
	}
	if !hasTag("ch-test-chain-out-www") {
		t.Errorf("missing tag ch-test-chain-out-www, got %v", tags)
	}
}

func TestSingboxCheck(t *testing.T) {
	// Check if sing-box is installed
	_, err := exec.LookPath("sing-box")
	if err != nil {
		t.Skip("sing-box binary not found in PATH, skipping integration test")
	}

	// 1. Generate full mock config incorporating the new structs
	priv, _, err := GenerateRealityKeypair()
	if err != nil {
		t.Fatalf("GenerateRealityKeypair: %v", err)
	}
	p := &hopParams{
		Port:       443,
		UUID:       "12345678-1234-1234-1234-123456789012",
		ServerName: "www.cloudflare.com",
		PrivateKey: priv,
		ShortID:    "aabbccdd",
	}
	preset := &ConnectionPreset{}
	
	inboundJSON := buildXHTTPTransportInbound(p, "inbound-test", preset)
	var inb map[string]any
	json.Unmarshal(inboundJSON, &inb)

	configMap := map[string]any{
		"log": map[string]any{"level": "error"},
		"inbounds": []any{inb},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct"},
		},
	}

	configBytes, _ := json.Marshal(configMap)

	// 2. Write to temp file
	tmpFile, err := os.CreateTemp("", "singbox_test_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	tmpFile.Write(configBytes)
	tmpFile.Close()

	// 3. Run sing-box check
	cmd := exec.Command("sing-box", "check", "-c", tmpFile.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check failed: %v\nOutput: %s\nConfig: %s", err, output, configBytes)
	}
}

func TestSerializationSymmetry(t *testing.T) {
	rawJSON := `{
		"type": "vless",
		"tag": "my-outbound",
		"server": "1.1.1.1",
		"server_port": 443,
		"uuid": "my-uuid",
		"flow": "xtls-rprx-vision",
		"tls": {
			"enabled": true,
			"server_name": "example.com",
			"utls": {
				"enabled": true,
				"fingerprint": "chrome"
			},
			"reality": {
				"enabled": true,
				"public_key": "pubkey",
				"short_id": "shortid"
			}
		},
		"transport": {
			"type": "http",
			"host": ["example.com"],
			"path": "/api",
			"method": "POST",
			"headers": {
				"Host": ["example.com"]
			},
			"idle_timeout": "15s",
			"ping_timeout": "15s"
		},
		"multiplex": {
			"enabled": true
		}
	}`

	var out config.VLESSOutbound
	if err := json.Unmarshal([]byte(rawJSON), &out); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	reMarshaled, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var originalMap, newMap map[string]any
	json.Unmarshal([]byte(rawJSON), &originalMap)
	json.Unmarshal(reMarshaled, &newMap)

	origPretty, _ := json.MarshalIndent(originalMap, "", "  ")
	newPretty, _ := json.MarshalIndent(newMap, "", "  ")

	if !bytes.Equal(origPretty, newPretty) {
		t.Fatalf("Symmetry broken!\nExpected:\n%s\n\nActual:\n%s", origPretty, newPretty)
	}
}
