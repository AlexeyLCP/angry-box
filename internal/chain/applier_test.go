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
				"flow": "xtls-rprx-vision",
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
		"multiplex": map[string]any{
			"enabled": true,
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
		"flow":        "xtls-rprx-vision",
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
		"multiplex": map[string]any{
			"enabled": true,
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

func TestNodeConfigJSONParity(t *testing.T) {
	node := &model.ChainNode{Addr: "1.2.3.4:443"}
	nodes := []model.ChainNode{*node, {Addr: "5.6.7.8:443"}}
	
	p := &hopParams{
		Port:       443,
		UUID:       "uuid-1234",
		ServerName: "test.com",
		PrivateKey: "priv-hex",
		ShortID:    "short-hex",
	}
	nextP := &hopParams{
		Port:       443,
		UUID:       "uuid-5678",
		ServerName: "next.com",
		PrivateKey: "eE2tO7r8Ff_3hWwK-Qv6RzL0X1sP_bN4mD5Y8Vj_AQA",
		ShortID:    "short-next",
	}
	params := []*hopParams{p, nextP}

	preset := GetDefaultPreset()
	presetPtr := &preset

	actualJSONStr, err := buildNodeConfig(node, 0, 2, params, nodes, presetPtr, model.TransportReality, model.UserProtocolVLESSReality, model.StrategyURLTest)
	if err != nil {
		t.Fatalf("buildNodeConfig failed: %v", err)
	}

	// For the old map structure, we mock what it used to assemble.
	inbounds := []json.RawMessage{}
	inb := buildUserInbound(8443, p.UUID, "user-in")
	inbounds = append(inbounds, inb)

	outbounds := []json.RawMessage{}
	outb, _ := buildTransportOutbound(nextP, "5.6.7.8", "out-to-next.com")
	outbounds = append(outbounds, outb)
	outbounds = append(outbounds, buildDirectOutbound("direct-out"))

	stratOut := BuildStrategyOutbound(string(model.StrategyURLTest), []string{"out-to-next.com"})
	stratJSON, _ := json.Marshal(stratOut)
	outbounds = append(outbounds, stratJSON)

	outbounds = append(outbounds, []byte(`{"tag":"block","type":"block"}`)) // routing rule has block

	routingSection := BuildRoutingSection(presetPtr, stratOut.Tag)
	
	expectedMap := map[string]any{
		"log": map[string]any{
			"level":  "info",
			"output": "/var/log/sing-box/sing-box.log",
		},
		"inbounds":  inbounds,
		"outbounds": outbounds,
		"route":     routingSection,
		"dns":       BuildDNSWithDetour(stratOut.Tag, presetPtr.Routing.DirectDomains),
		"experimental": map[string]any{
			"cache_file": map[string]any{"enabled": true},
		},
	}

	expectedJSONBytes, _ := json.Marshal(expectedMap)

	var expMap, actMap map[string]any
	json.Unmarshal(expectedJSONBytes, &expMap)
	json.Unmarshal([]byte(actualJSONStr), &actMap)

	prettyExpected, _ := json.MarshalIndent(expMap, "", "  ")
	prettyActual, _ := json.MarshalIndent(actMap, "", "  ")

	if !bytes.Equal(prettyExpected, prettyActual) {
		t.Fatalf("JSON mismatch!\nExpected:\n%s\n\nActual:\n%s", prettyExpected, prettyActual)
	}
}

func TestSingboxCheck(t *testing.T) {
	// Check if sing-box is installed
	_, err := exec.LookPath("sing-box")
	if err != nil {
		t.Skip("sing-box binary not found in PATH, skipping integration test")
	}

	// 1. Generate full mock config incorporating the new structs
	p := &hopParams{
		Port:       443,
		UUID:       "12345678-1234-1234-1234-123456789012",
		ServerName: "example.com",
		PrivateKey: "private_key_hex",
		ShortID:    "short_id_hex",
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
