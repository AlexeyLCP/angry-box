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

	if len(priv) != 44 || len(pub) != 44 {
		t.Errorf("unexpected key length: priv=%d pub=%d", len(priv), len(pub))
	}

	if _, err := base64.StdEncoding.DecodeString(priv); err != nil {
		t.Error("priv is not valid base64")
	}
	if _, err := base64.StdEncoding.DecodeString(pub); err != nil {
		t.Error("pub is not valid base64")
	}
}

func TestBuildAWGUserInbound_WithClientKey(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	ep, tunInb, serverPub, err := buildAWGUserInbound(8443, "test-uuid", "user-in", &preset, "MY-CLIENT-PUB", "")
	if err != nil {
		t.Fatalf("buildAWGUserInbound failed: %v", err)
	}

	// buildAWGUserInbound uses json.Marshal (compact, no spaces)
	epS := string(ep)
	if !strings.Contains(epS, "MY-CLIENT-PUB") {
		t.Error("client pubkey not present in endpoint peers")
	}
	if !strings.Contains(epS, `"type":"wireguard"`) {
		t.Error("endpoint should be wireguard type")
	}
	if strings.Contains(epS, "CLIENT_PUBLIC_KEY_HERE") {
		t.Error("placeholder appeared when client key was provided")
	}

	tunS := string(tunInb)
	if !strings.Contains(tunS, `"type":"tun"`) {
		t.Error("user inbound should be TUN type")
	}

	if serverPub == "" || len(serverPub) != 44 {
		t.Error("server public key was not returned")
	}
}

func TestBuildAWGUserInbound_WithPreGeneratedServerKey(t *testing.T) {
	preset := MustGetPreset("maximum_stealth_2026")

	serverPriv, serverPub1, _ := GenerateWireGuardKeypair()

	ep, _, serverPub2, err := buildAWGUserInbound(8443, "uuid", "tag", &preset, "client-pub", serverPriv)
	if err != nil {
		t.Fatal(err)
	}

	if serverPub1 != serverPub2 {
		t.Error("server pub from pre-generated path does not match the one returned")
	}

	epS := string(ep)
	if !strings.Contains(epS, serverPriv) {
		t.Error("pre-generated private key was not used in the config")
	}

	var parsed map[string]any
	if err := json.Unmarshal(ep, &parsed); err != nil {
		t.Fatalf("failed to parse AWG endpoint JSON: %v", err)
	}
	if parsed["private_key"] != serverPriv {
		t.Errorf("private_key in generated endpoint does not exactly match pre-generated one")
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

func TestAWGKeyConsistencyInEntryNode(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	serverPriv, serverPub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}

	ep, _, returnedPub, err := buildAWGUserInbound(8443, "entry-uuid", "user-in", &preset, "client-pub-123", serverPriv)
	if err != nil {
		t.Fatal(err)
	}

	if returnedPub != serverPub {
		t.Error("returned server pub does not match the one generated for the report")
	}

	var parsed map[string]any
	json.Unmarshal(ep, &parsed)

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
	// WireGuard is now an endpoint, not an inbound
	if !strings.Contains(s, `"type": "wireguard"`) {
		t.Error("expected wireguard endpoint on entry node with AWG")
	}
	// TUN inbound for user traffic
	if !strings.Contains(s, `"type": "tun"`) {
		t.Error("expected TUN user inbound on entry node with AWG")
	}
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

	// Entry with AWG: wireguard endpoint + TUN inbound
	cfg0, _ := buildNodeConfig(&nodes[0], 0, 3, params, nodes, &preset, model.TransportXHTTP, model.UserProtocolAWG)
	if !strings.Contains(string(cfg0), `"type": "wireguard"`) {
		t.Error("entry should have wireguard endpoint")
	}
	if !strings.Contains(string(cfg0), `"type": "tun"`) {
		t.Error("entry should have TUN inbound")
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

// === ApplyReport / AWG material population tests ===

func TestAWGEntryKeyMaterial_Population(t *testing.T) {
	preset := MustGetPreset("russia_2026")

	serverPriv, serverPub, _ := GenerateWireGuardKeypair()
	clientPub := "test-client-pub-for-report"

	ep, _, returnedPub, err := buildAWGUserInbound(8443, "uuid-123", "user-in", &preset, clientPub, serverPriv)
	if err != nil {
		t.Fatal(err)
	}

	if returnedPub != serverPub {
		t.Fatal("pub returned from builder must match the one captured for ApplyReport")
	}

	var parsed map[string]any
	json.Unmarshal(ep, &parsed)

	material := &AWGClientMaterial{
		ServerPub:     serverPub,
		ClientPubUsed: clientPub,
		ClientPriv:    "",
	}

	if material.ServerPub == "" || material.ClientPubUsed == "" {
		t.Error("ApplyReport.AWG material is incomplete")
	}
	if parsed["private_key"] != serverPriv {
		t.Error("config private_key does not match what was recorded for the report")
	}
}
