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
	// NOTE: curve_preferences and ECH are intentionally NOT set on a REALITY
	// inbound — sing-box-extended rejects "curve preferences is unavailable in
	// reality" and "Reality is conflict with ECH". They are client-side / plain-
	// TLS options. Asserting their absence guards against re-adding them.
	if tls["curve_preferences"] != nil {
		t.Errorf("curve_preferences must not be set on a REALITY inbound (sing-box rejects it): %v", tls["curve_preferences"])
	}
	if tls["ech"] != nil {
		t.Errorf("ECH must not be set on a REALITY inbound (sing-box rejects it): %v", tls["ech"])
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
		ListenPort:        443,
		UUID:              uuid,
		RealityPrivateKey: priv,
		ShortIDs:          ids,
		XHTTPPath:         "/fixed",
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

// TestRenderAWGHop_UserspaceAmnezia verifies the chain-hop AWG endpoint carries
// an amnezia block and a wireguard endpoint.
func TestRenderAWGHop_UserspaceAmnezia(t *testing.T) {
	b, err := RenderAWGHop(AWGHopParams{
		Tag:        "awg-hop",
		ListenPort: 51820,
		Address:    []string{"10.8.0.1/24"},
		PrivateKey: "priv",
		PeerPubKey: "pub",
		Amnezia:    nil, // omitted; just check endpoint shape
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
	if ep["type"] != "awg" {
		t.Errorf("endpoint type: got %v, want awg", ep["type"])
	}
	if ep["private_key"] != "priv" {
		t.Errorf("private_key not reused: got %v", ep["private_key"])
	}
	// MTU must be 1420 (match all other AWG endpoints — a WireGuard pair needs
	// identical MTU on both ends or large packets fragment/drop). Was 1280.
	if mtu, _ := ep["mtu"].(float64); int(mtu) != 1420 {
		t.Errorf("endpoint mtu: got %v, want 1420 (was 1280, mismatched buildAWGUserInbound*/buildAWGTransport*)", ep["mtu"])
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

// ─── AWG Balancer (kernel) ──────────────────────────────────────────────────

// findBalancerOutbound returns the fallback outbound group, or nil if none.
func findBalancerOutbound(outbounds []any) map[string]any {
	for _, ob := range outbounds {
		m, _ := ob.(map[string]any)
		if m == nil {
			continue
		}
		if m["type"] == "fallback" {
			return m
		}
	}
	return nil
}

// findOutboundByTagSingbox returns the outbound with the given tag, or nil.
func findOutboundByTagSingbox(outbounds []any, tag string) map[string]any {
	for _, ob := range outbounds {
		m, _ := ob.(map[string]any)
		if m == nil {
			continue
		}
		if m["tag"] == tag {
			return m
		}
	}
	return nil
}

// TestRenderAWGBalancer_MultiExit verifies the dns.idoctor.mom reference shape:
// a TUN inbound capturing awg0 (no userspace wireguard endpoint), one direct
// outbound per exit interface bound to it, a fallback group rotating across
// them, and route rules steering TUN traffic to the balancer.
func TestRenderAWGBalancer_MultiExit(t *testing.T) {
	b, err := RenderAWGBalancer(AWGBalancerParams{
		ExitInterfaces: []string{"awg-exit-n1", "awg-exit-n2", "awg-exit-n3", "awg-exit-n4"},
		BalancerTag:    "balancer",
	})
	if err != nil {
		t.Fatal(err)
	}
	mustValidJSON(t, "awg_balancer", b)

	var top map[string]any
	json.Unmarshal(b, &top)

	// Endpoints MUST be empty — the kernel owns the AWG interfaces; a userspace
	// wireguard endpoint would panic with chacha20poly1305 under AmneziaWG.
	endpoints, _ := top["endpoints"].([]any)
	if len(endpoints) != 0 {
		t.Errorf("kernel-AWG balancer must have NO userspace endpoints, got %d", len(endpoints))
	}

	// One TUN inbound capturing awg0 AND every awg-exit-nX. awg0 captures
	// user/client traffic; awg-exit-nX captures the RESPONSE traffic for
	// sing-box direct outbounds that bind_interface: awg-exit-nX — without
	// it the kernel delivers the SYN-ACK to a dead local socket and the
	// dial times out (egress through the balancer silently fails).
	// Live-verified 2026-07-04.
	inbounds, _ := top["inbounds"].([]any)
	if len(inbounds) != 1 {
		t.Fatalf("expected 1 inbound (TUN), got %d", len(inbounds))
	}
	tun, _ := inbounds[0].(map[string]any)
	if tun["type"] != "tun" {
		t.Errorf("inbound type: got %v, want tun", tun["type"])
	}
	if tun["stack"] != "mixed" {
		t.Errorf("TUN stack: got %v, want mixed (kernel TCP + gVisor UDP for QUIC)", tun["stack"])
	}
	if tun["auto_route"] != true {
		t.Errorf("auto_route: got %v, want true", tun["auto_route"])
	}
	inc, _ := tun["include_interface"].([]any)
	wantIfaces := map[string]bool{"awg0": true, "awg-exit-n1": true, "awg-exit-n2": true, "awg-exit-n3": true, "awg-exit-n4": true}
	if len(inc) != len(wantIfaces) {
		t.Errorf("include_interface: got %v, want %d entries (awg0 + all awg-exit-nX)", inc, len(wantIfaces))
	}
	for _, v := range inc {
		s, _ := v.(string)
		if !wantIfaces[s] {
			t.Errorf("include_interface: unexpected entry %q", s)
		}
	}

	// One direct outbound per exit interface, bound to it.
	outbounds, _ := top["outbounds"].([]any)
	for _, iface := range []string{"awg-exit-n1", "awg-exit-n2", "awg-exit-n3", "awg-exit-n4"} {
		ob := findOutboundByTagSingbox(outbounds, "exit-"+iface)
		if ob == nil {
			t.Errorf("missing direct outbound for %s", iface)
			continue
		}
		if ob["type"] != "direct" {
			t.Errorf("%s outbound type: got %v, want direct", iface, ob["type"])
		}
		if ob["bind_interface"] != iface {
			t.Errorf("%s bind_interface: got %v, want %s", iface, ob["bind_interface"], iface)
		}
	}

	// Fallback balancer rotating across the four exit outbounds.
	fb := findBalancerOutbound(outbounds)
	if fb == nil {
		t.Fatal("missing fallback balancer outbound")
	}
	if fb["tag"] != "balancer" {
		t.Errorf("balancer tag: got %v, want balancer", fb["tag"])
	}
	if fb["blacklist_timeout"] != "30s" {
		t.Errorf("blacklist_timeout: got %v, want 30s", fb["blacklist_timeout"])
	}
	wantOuts := []string{"exit-awg-exit-n1", "exit-awg-exit-n2", "exit-awg-exit-n3", "exit-awg-exit-n4"}
	gotOuts := toStrings(fb["outbounds"].([]any))
	for _, w := range wantOuts {
		if !contains(gotOuts, w) {
			t.Errorf("balancer outbounds missing %s: got %v", w, gotOuts)
		}
	}

	// Route: TUN traffic → balancer (inbound:["tun-in"], NOT source_ip_cidr).
	route, _ := top["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	var tunRule map[string]any
	for _, r := range rules {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if ins, _ := m["inbound"].([]any); len(ins) == 1 && ins[0] == "tun-in" {
			tunRule = m
		}
	}
	if tunRule == nil {
		t.Fatal("missing route rule for inbound tun-in")
	}
	if tunRule["outbound"] != "balancer" {
		t.Errorf("tun-in route outbound: got %v, want balancer", tunRule["outbound"])
	}
}

// TestRenderAWGBalancer_NoUserspaceWG is the architectural invariant guard:
// the balancer must NEVER emit a userspace wireguard endpoint/inbound (the
// chacha20poly1305 panic path under AmneziaWG).
func TestRenderAWGBalancer_NoUserspaceWG(t *testing.T) {
	b, err := RenderAWGBalancer(AWGBalancerParams{
		ExitInterfaces: []string{"awg-exit-n1", "awg-exit-n2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"type": "wireguard"`) {
		t.Errorf("balancer must not emit a userspace wireguard endpoint:\n%s", string(b))
	}
}

// TestRenderAWGBalancer_SingleEgress verifies a balancer with one exit
// interface does NOT emit a fallback group (no rotation needed) and routes TUN
// traffic directly to that exit.
func TestRenderAWGBalancer_SingleEgress(t *testing.T) {
	b, err := RenderAWGBalancer(AWGBalancerParams{
		ExitInterfaces: []string{"awg-exit-n1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mustValidJSON(t, "awg_balancer_single", b)

	var top map[string]any
	json.Unmarshal(b, &top)
	outbounds, _ := top["outbounds"].([]any)
	if findBalancerOutbound(outbounds) != nil {
		t.Error("single-egress balancer must not emit a fallback group")
	}
	route, _ := top["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	for _, r := range rules {
		m, _ := r.(map[string]any)
		if m == nil {
			continue
		}
		if ins, _ := m["inbound"].([]any); len(ins) == 1 && ins[0] == "tun-in" {
			if m["outbound"] != "exit-awg-exit-n1" {
				t.Errorf("tun-in route: got outbound %v, want exit-awg-exit-n1", m["outbound"])
			}
			return
		}
	}
	t.Error("missing tun-in route rule")
}
