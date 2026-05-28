package chain

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestGenerateWireGuardKeypair_Valid(t *testing.T) {
	priv, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair failed: %v", err)
	}
	if len(priv) == 0 || len(pub) == 0 {
		t.Error("empty keys returned")
	}

	// Should be valid base64 and 44 chars (32 bytes base64)
	if len(priv) != 44 || len(pub) != 44 {
		t.Errorf("unexpected key length: priv=%d pub=%d", len(priv), len(pub))
	}

	// Decode should succeed
	if _, err := base64.StdEncoding.DecodeString(priv); err != nil {
		t.Error("priv is not valid base64")
	}
	if _, err := base64.StdEncoding.DecodeString(pub); err != nil {
		t.Error("pub is not valid base64")
	}
}

func TestBuildAWGUserInbound_WithClientKey(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	data, serverPub, err := buildAWGUserInbound(8443, "test-uuid", "user-in", &preset, "MY-CLIENT-PUB", "", "", "", "", "", "")
	if err != nil {
		t.Fatalf("buildAWGUserInbound failed: %v", err)
	}

	s := string(data)
	if !strings.Contains(s, "MY-CLIENT-PUB") {
		t.Error("client pubkey not present in peers")
	}
	if strings.Contains(s, "CLIENT_PUBLIC_KEY_HERE") {
		t.Error("placeholder appeared when client key was provided")
	}
	if serverPub == "" || len(serverPub) != 44 {
		t.Error("server public key was not returned")
	}
}

func TestBuildAWGUserInbound_WithPreGeneratedServerKey(t *testing.T) {
	preset := MustGetPreset("maximum_stealth_2026")

	// Generate once
	serverPriv, serverPub1, _ := GenerateWireGuardKeypair()

	data, serverPub2, err := buildAWGUserInbound(8443, "uuid", "tag", &preset, "client-pub", serverPriv, "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if serverPub1 != serverPub2 {
		t.Error("server pub from pre-generated path does not match the one returned")
	}

	s := string(data)
	if !strings.Contains(s, serverPriv) {
		t.Error("pre-generated private key was not used in the config")
	}

	// Stronger check: parse JSON and verify the exact private_key field
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse AWG inbound JSON: %v", err)
	}
	if parsed["private_key"] != serverPriv {
		t.Errorf("private_key in generated config does not exactly match pre-generated one")
	}
}

func TestDeriveWireGuardPublicFromPrivate(t *testing.T) {
	priv, expectedPub, _ := GenerateWireGuardKeypair()

	derived, err := deriveWireGuardPublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("derive failed: %v", err)
	}

	if derived != expectedPub {
		t.Errorf("derived pub does not match original pub")
	}
}

// TestAWGKeyConsistencyInEntryNode simulates the critical path fixed in ApplyChain:
// server keypair is generated once, used for the pushed config, and the pub is captured for the report.
func TestAWGKeyConsistencyInEntryNode(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	// This is what ApplyChain now does for AWG entry: generate once
	serverPriv, serverPub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}

	// Build the user inbound using the pre-generated priv (like the fixed path)
	data, returnedPub, err := buildAWGUserInbound(8443, "entry-uuid", "user-in", &preset, "client-pub-123", serverPriv, "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	if returnedPub != serverPub {
		t.Error("returned server pub does not match the one generated for the report")
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)

	if parsed["private_key"] != serverPriv {
		t.Error("config does not contain the exact server private key that will be reported")
	}
}

// --- buildNodeConfig and transport builders tests ---

func makeTestHopParams(port int) *hopParams {
	p, _ := generateHopParams(port, &ConnectionPreset{
		Reality: &RealityPreset{ServerNames: []string{"www.microsoft.com"}},
	})
	return p
}

func TestBuildNodeConfig_EntryNode_AWG_SingleNode(t *testing.T) {
	preset := MustGetPreset("china_2026")
	params := []*hopParams{makeTestHopParams(443)}
	nodes := []model.ChainNode{{ID: "node1", Addr: "1.2.3.4:22"}}

	cfg, err := buildNodeConfig(&nodes[0], 0, 1, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolAWG)
	if err != nil {
		t.Fatalf("buildNodeConfig failed: %v", err)
	}

	s := string(cfg)
	if !strings.Contains(s, `"type": "wireguard"`) {
		t.Error("expected wireguard user inbound on entry node with AWG")
	}
	// Single node AWG chain has no transport links, only the user inbound + direct
	if !strings.Contains(s, `"type": "direct"`) {
		t.Error("single node should have direct outbound")
	}
}

func TestBuildNodeConfig_MiddleNode_XHTTP(t *testing.T) {
	preset := MustGetPreset("russia_2026")
	p1 := makeTestHopParams(443)
	p2 := makeTestHopParams(443)
	p3 := makeTestHopParams(443)
	params := []*hopParams{p1, p2, p3}
	nodes := []model.ChainNode{
		{ID: "n1", Addr: "1.1.1.1:22"},
		{ID: "n2", Addr: "2.2.2.2:22"},
		{ID: "n3", Addr: "3.3.3.3:22"},
	}

	cfg, err := buildNodeConfig(&nodes[1], 1, 3, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolTUIC)
	if err != nil {
		t.Fatal(err)
	}

	s := string(cfg)
	if !strings.Contains(s, `"type": "vless"`) || !strings.Contains(s, `"transport":`) {
		t.Error("middle node should have transport inbound + outbound")
	}
	if !strings.Contains(s, `"type": "http"`) {
		t.Error("expected XHTTP on middle node")
	}
}

func TestBuildNodeConfig_LastNode_Direct(t *testing.T) {
	preset := MustGetPreset("maximum_stealth_2026")
	p := makeTestHopParams(443)
	params := []*hopParams{p}
	nodes := []model.ChainNode{{ID: "exit", Addr: "9.9.9.9:22"}}

	cfg, err := buildNodeConfig(&nodes[0], 0, 1, params, nodes, &preset, model.TransportReality, model.UserProtocolVLESSReality)
	if err != nil {
		t.Fatal(err)
	}

	s := string(cfg)
	if !strings.Contains(s, `"type": "direct"`) {
		t.Error("last node should have direct outbound")
	}
	if !strings.Contains(s, `"type": "vless"`) {
		t.Error("entry node should still have user inbound")
	}
}

// Note: Direct XHTTP builder tests are covered indirectly via buildNodeConfig tests above.
// The builders are internal and their behavior is validated through higher-level node config generation.

func TestBuildNodeConfig_TUIC_Entry_With_XHTTP_Transport(t *testing.T) {
	preset := MustGetPreset("iran_2026")
	p1 := makeTestHopParams(443)
	p2 := makeTestHopParams(443)
	params := []*hopParams{p1, p2}
	nodes := []model.ChainNode{
		{ID: "entry", Addr: "1.1.1.1:22"},
		{ID: "exit", Addr: "2.2.2.2:22"},
	}

	cfg, err := buildNodeConfig(&nodes[0], 0, 2, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolTUIC)
	if err != nil {
		t.Fatal(err)
	}

	s := string(cfg)
	if !strings.Contains(s, `"type": "tuic"`) {
		t.Error("expected tuic user inbound")
	}
	if !strings.Contains(s, `"type": "http"`) {
		t.Error("expected XHTTP transport outbound from entry")
	}
	// Iran preset influence
	if !strings.Contains(s, "/msdownload") {
		t.Error("expected iran XHTTP path")
	}
}

func TestBuildNodeConfig_MultiHop_FullChain(t *testing.T) {
	preset := MustGetPreset("maximum_stealth_2026")
	p1 := makeTestHopParams(443)
	p2 := makeTestHopParams(443)
	p3 := makeTestHopParams(443)
	params := []*hopParams{p1, p2, p3}
	nodes := []model.ChainNode{
		{ID: "n1", Addr: "10.0.0.1:22"},
		{ID: "n2", Addr: "10.0.0.2:22"},
		{ID: "n3", Addr: "10.0.0.3:22"},
	}

	// Entry with AWG
	cfg0, _ := buildNodeConfig(&nodes[0], 0, 3, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolAWG)
	if !strings.Contains(string(cfg0), `"type": "wireguard"`) {
		t.Error("entry should be AWG wireguard")
	}

	// Middle
	cfg1, _ := buildNodeConfig(&nodes[1], 1, 3, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolAWG)
	if !strings.Contains(string(cfg1), `"type": "http"`) {
		t.Error("middle should have XHTTP transport")
	}

	// Exit
	cfg2, _ := buildNodeConfig(&nodes[2], 2, 3, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolAWG)
	if !strings.Contains(string(cfg2), `"type": "direct"`) {
		t.Error("exit should have direct outbound")
	}
}

// === ApplyReport / AWG material population tests (the core of the big fix) ===

func TestAWGEntryKeyMaterial_Population(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	// Simulate what the fixed ApplyChain does for the entry node
	serverPriv, serverPub, _ := GenerateWireGuardKeypair()
	clientPub := "test-client-pub-for-report"

	data, returnedPub, err := buildAWGUserInbound(8443, "uuid-123", "user-in", &preset, clientPub, serverPriv, "", "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}

	// The report would capture this
	if returnedPub != serverPub {
		t.Fatal("pub returned from builder must match the one captured for ApplyReport")
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)

	// Simulate building the AWGClientMaterial that goes into ApplyReport
	material := &AWGClientMaterial{
		ServerPub:     serverPub,
		ClientPubUsed: clientPub,
		ClientPriv:    "", // would be set only on auto-generation at higher level
	}

	if material.ServerPub == "" || material.ClientPubUsed == "" {
		t.Error("ApplyReport.AWG material is incomplete")
	}
	if parsed["private_key"] != serverPriv {
		t.Error("config private_key does not match what was recorded for the report")
	}
}

