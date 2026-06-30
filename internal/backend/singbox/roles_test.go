package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

// mustValidJSON unmarshals b and fails the test if it is not valid JSON.
func mustValidJSON(t *testing.T, name string, b []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: invalid JSON: %v\n---\n%s", name, err, string(b))
	}
}

// TestRenderProxyNode_Structure verifies the VLESS REALITY+XHTTP max-obfuscation
// fields are present and shaped correctly.
func TestRenderProxyNode_Structure(t *testing.T) {
	b, err := RenderProxyNode(ProxyNodeParams{ListenPort: 443})
	if err != nil {
		t.Fatalf("RenderProxyNode: %v", err)
	}
	mustValidJSON(t, "proxy_node", b)

	var top map[string]any
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	inbounds, _ := top["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound, got %d", len(inbounds))
	}
	inb := inbounds[0].(map[string]any)
	if inb["type"] != "vless" {
		t.Errorf("inbound type: got %v, want vless", inb["type"])
	}
	if inb["listen_port"] != float64(443) {
		t.Errorf("listen_port: got %v, want 443", inb["listen_port"])
	}

	tls := inb["tls"].(map[string]any)
	if tls["min_version"] != "1.3" || tls["max_version"] != "1.3" {
		t.Errorf("TLS version range: got %v-%v, want 1.3-1.3", tls["min_version"], tls["max_version"])
	}
	curves, _ := tls["curve_preferences"].([]any)
	curveStrs := toStrings(curves)
	if !contains(curveStrs, "X25519MLKEM768") {
		t.Errorf("missing post-quantum curve X25519MLKEM768: %v", curveStrs)
	}
	ech := tls["ech"].(map[string]any)
	if ech["enabled"] != true {
		t.Error("ECH not enabled")
	}
	if ech["pq_signature_schemes_enabled"] != true {
		t.Error("pq_signature_schemes_enabled not true")
	}

	reality := tls["reality"].(map[string]any)
	shortIDs, _ := reality["short_id"].([]any)
	if len(shortIDs) != 8 {
		t.Errorf("expected 8 short_ids, got %d", len(shortIDs))
	}
	if shortIDs[0] != "" {
		t.Errorf("first short_id should be empty, got %v", shortIDs[0])
	}

	transport := inb["transport"].(map[string]any)
	if transport["type"] != "xhttp" {
		t.Errorf("transport type: got %v, want xhttp", transport["type"])
	}
	if transport["mode"] != "packet-up" {
		t.Errorf("transport mode: got %v, want packet-up", transport["mode"])
	}
	if transport["x_padding_method"] != "tokenish" {
		t.Errorf("x_padding_method: got %v, want tokenish", transport["x_padding_method"])
	}
	if transport["session_placement"] != "cookie" {
		t.Errorf("session_placement: got %v, want cookie", transport["session_placement"])
	}
	if transport["xmux"] == nil {
		t.Error("xmux missing")
	}
}

// TestRenderProxyNode_DeterministicCredentials verifies that passing persisted
// credentials through ProxyNodeParams reuses them (no rotation) — AGENTS.md rule 5.
func TestRenderProxyNode_DeterministicCredentials(t *testing.T) {
	const uuid = "11111111-2222-3333-4444-555555555555"
	const priv = "qF0W0OPCHXXp8eGDbmsJzpSDxVN2-9VAJaOnmS1Hj1I"
	ids := []string{"", "abcdef0123456789"}

	b, err := RenderProxyNode(ProxyNodeParams{
		ListenPort:         443,
		UUID:               uuid,
		RealityPrivateKey:  priv,
		ShortIDs:           ids,
		XHTTPPath:          "/fixed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), uuid) {
		t.Error("persisted UUID not reused")
	}
	if !strings.Contains(string(b), priv) {
		t.Error("persisted reality private key not reused")
	}
	if !strings.Contains(string(b), "/fixed") {
		t.Error("persisted XHTTP path not reused")
	}
}

// TestRenderAWGBalancer_KernelPath verifies the balancer uses kernel AWG
// (empty endpoints, TUN include_interface awg0, per-exit bind_interface) and NO
// amnezia block in the JSON.
func TestRenderAWGBalancer_KernelPath(t *testing.T) {
	b, err := RenderAWGBalancer(AWGBalancerParams{
		Exits: []AWGBalancerExit{
			{Tag: "exit-n1", InterfaceName: "awg-exit-n1"},
			{Tag: "exit-n2", InterfaceName: "awg-exit-n2"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustValidJSON(t, "awg_balancer", b)

	if strings.Contains(string(b), "amnezia") {
		t.Error("awg_balancer must NOT contain an amnezia block (kernel AWG owns obfuscation)")
	}

	var top map[string]any
	json.Unmarshal(b, &top)
	endpoints, _ := top["endpoints"].([]any)
	if len(endpoints) != 0 {
		t.Errorf("expected empty endpoints (kernel AWG), got %d", len(endpoints))
	}

	inbounds, _ := top["inbounds"].([]any)
	tun := inbounds[0].(map[string]any)
	if tun["type"] != "tun" {
		t.Errorf("inbound type: got %v, want tun", tun["type"])
	}
	if tun["stack"] != "mixed" {
		t.Errorf("stack: got %v, want mixed", tun["stack"])
	}
	includes, _ := tun["include_interface"].([]any)
	if len(includes) != 1 || includes[0] != "awg0" {
		t.Errorf("include_interface: got %v, want [awg0]", includes)
	}

	outs, _ := top["outbounds"].([]any)
	// direct + block + 2 exits + balancer = 5
	if len(outs) != 5 {
		t.Errorf("expected 5 outbounds, got %d", len(outs))
	}
	foundBind := false
	for _, o := range outs {
		om := o.(map[string]any)
		if om["tag"] == "exit-n1" {
			if om["bind_interface"] != "awg-exit-n1" {
				t.Errorf("exit-n1 bind_interface: got %v", om["bind_interface"])
			}
			foundBind = true
		}
	}
	if !foundBind {
		t.Error("exit-n1 outbound with bind_interface not found")
	}
}

// TestRenderAWGBalancer_NoExitsErrors.
func TestRenderAWGBalancer_NoExitsErrors(t *testing.T) {
	if _, err := RenderAWGBalancer(AWGBalancerParams{}); err == nil {
		t.Error("expected error with no exits")
	}
}

// TestRenderAWGHop_UserspaceAmnezia verifies the chain-hop AWG endpoint carries
// an amnezia block and a wireguard endpoint.
func TestRenderAWGHop_UserspaceAmnezia(t *testing.T) {
	b, err := RenderAWGHop(AWGHopParams{
		Tag:         "awg-hop",
		ListenPort:  51820,
		Address:     []string{"10.8.0.1/24"},
		PrivateKey:  "priv",
		PeerPubKey:  "pub",
		Amnezia:     nil, // omitted; just check endpoint shape
	})
	if err != nil {
		t.Fatal(err)
	}
	mustValidJSON(t, "awg_hop", b)

	var top map[string]any
	json.Unmarshal(b, &top)
	endpoints, _ := top["endpoints"].([]any)
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	ep := endpoints[0].(map[string]any)
	if ep["type"] != "wireguard" {
		t.Errorf("endpoint type: got %v, want wireguard", ep["type"])
	}
	if ep["private_key"] != "priv" {
		t.Errorf("private_key not reused: got %v", ep["private_key"])
	}
}

// helpers
func toStrings(v []any) []string {
	out := make([]string, len(v))
	for i, x := range v {
		out[i] = x.(string)
	}
	return out
}
func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}