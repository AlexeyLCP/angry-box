//go:build awg3gen

package chain

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// TestAWG3GenPair generates an AWG 3.0 server endpoint JSON (userspace
// type:"awg" with HPK/CPM/RAT) + a matching kernel awg-quick client .conf, for
// the n1 live E2E gate (A5). Run with:
//
//	go test -tags awg3gen -run TestAWG3GenPair -v ./internal/chain/
//
// Outputs (overwritten each run):
//   /tmp/awg3-server.json   — sing-box config with the userspace endpoint
//   /tmp/awg3-client.conf   — awg-quick client .conf carrying AWG3 fields
//
// The server private key + client private key are written too so the live test
// can reprovision without regenerating. This is a throwaway generator, not a
// unit test — it asserts nothing beyond "the files were written".
func TestAWG3GenPair(t *testing.T) {
	// Robust preset (Jc=5) — handshake survives the budget VPS UDP rate-limit
	// (AGENTS #17); AWG3 header protection layers on top.
	preset := mustGetPresetAWGRobust(t)
	serverPriv, serverPub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	clientPriv, clientPub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}
	awg3 := GenerateAWG3Material()
	// Build a single-peer material: the client is the one peer. InboundAWGObfs
	// shape is not available without a NodeInbound; build the material by hand
	// (CPS off for the minimal test — AWG3 alone; H1-H4 from the preset's
	// degenerate fallback is fine since HPK is the new surface, but use the
	// generator's H ranges for realism).
	mat := GenerateAWGObfsMaterial(3, "quic")
	mat.AWG3Mode = awg3.AWG3Mode
	mat.HeaderProtectionKey = awg3.HeaderProtectionKey
	mat.ContentPaddingAddition = awg3.ContentPaddingAddition
	mat.RekeyAfterTime = awg3.RekeyAfterTime

	users := []model.User{{ID: "alice", Name: "alice", Active: true, AWGPublicKey: clientPub, AWGAddress: "10.8.0.2/32"}}
	port := 51841
	epJSON, derivedPub, err := buildAWGUserInboundMulti(port, "awg3-in", &preset, serverPriv, users, &mat)
	if err != nil {
		t.Fatalf("buildAWGUserInboundMulti: %v", err)
	}
	if derivedPub != serverPub {
		t.Logf("note: derived server pub %q != generated %q (using derived)", derivedPub, serverPub)
		serverPub = derivedPub
	}

	// Wrap the endpoint in a minimal sing-box config with a TUN inbound + direct
	// outbound (the generateAWGUser egress pattern) so sing-box can actually run it.
	var ep map[string]any
	if err := json.Unmarshal(epJSON, &ep); err != nil {
		t.Fatalf("unmarshal endpoint: %v", err)
	}
	cfg := map[string]any{
		"log":       map[string]any{"level": "info"},
		"endpoints": []any{ep},
		"inbounds": []any{map[string]any{
			"type": "tun", "tag": "tun-in",
			"address": []string{"172.16.250.1/30"}, "auto_route": true,
		}},
		"outbounds": []any{map[string]any{"type": "direct", "tag": "direct-out"}},
	}
	serverCfg, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile("/tmp/awg3-server.json", serverCfg, 0644); err != nil {
		t.Fatalf("write server cfg: %v", err)
	}

	// Client .conf via the same render path the product uses.
	clientConf := renderAWGQuickConf("144.31.224.212", port, clientPriv, serverPub, "10.8.0.2/24", &preset, &mat, model.AWGVersion3)
	if err := os.WriteFile("/tmp/awg3-client.conf", []byte(clientConf), 0644); err != nil {
		t.Fatalf("write client conf: %v", err)
	}

	t.Logf("server endpoint pub: %s", serverPub)
	t.Logf("HPK (hex): %s", awg3.HeaderProtectionKey)
	t.Logf("CPM: %s  RAT: %s", awg3.ContentPaddingAddition, awg3.RekeyAfterTime)
	t.Logf("wrote /tmp/awg3-server.json and /tmp/awg3-client.conf")
}

func mustGetPresetAWGRobust(t *testing.T) ConnectionPreset {
	p, ok := GetPreset("russia_2026_awg_robust")
	if !ok {
		t.Fatal("russia_2026_awg_robust preset not found")
	}
	return p
}

// ensure config import is used (the endpoint map is untyped, but config is
// referenced indirectly via buildAWGUserInboundMulti's returned JSON shape).
var _ = config.AwgEndpointOptions{}