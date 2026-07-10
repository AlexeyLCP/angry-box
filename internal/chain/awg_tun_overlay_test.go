package chain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

func mustJSONFields(t *testing.T, raw json.RawMessage, fields map[string]any) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal %s: %v", string(raw), err)
	}
	for k, want := range fields {
		got, ok := m[k]
		if !ok {
			t.Errorf("missing field %q in %s", k, string(raw))
			continue
		}
		if !equalJSON(got, want) {
			t.Errorf("field %q = %#v, want %#v", k, got, want)
		}
	}
}

func equalJSON(a, b any) bool {
	return jsonEqual(a, b)
}

func jsonEqual(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

func findInboundByType(inbounds []json.RawMessage, typ string) json.RawMessage {
	for _, raw := range inbounds {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["type"] == typ {
			return raw
		}
	}
	return nil
}

func findOutboundByTag(outbounds []json.RawMessage, tag string) json.RawMessage {
	for _, raw := range outbounds {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["tag"] == tag {
			return raw
		}
	}
	return nil
}

// TestBuildAWGTUNOverlay_SingleEgress verifies a standalone AWG node with one
// awg0 interface and no exit interfaces: TUN captures awg0, route targets the
// single direct outbound (no fallback balancer).
func TestBuildAWGTUNOverlay_SingleEgress(t *testing.T) {
	inb, outb, route := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
	})

	tun := findInboundByType(inb, "tun")
	if tun == nil {
		t.Fatal("missing TUN inbound")
	}
	mustJSONFields(t, tun, map[string]any{
		"type":              "tun",
		"tag":               "tun-in",
		"interface_name":    "sing-box-tun",
		"mtu":               float64(1200),
		"stack":             "mixed",
		"auto_route":        true,
		"include_interface": []any{"awg0"},
		"address":           []any{"172.16.250.1/30"},
	})
	// strict_route defaults to false and is omitempty, so it must NOT appear
	// (sing-box treats absence as false — matches the reference config).
	var tunMap map[string]any
	json.Unmarshal(tun, &tunMap)
	if _, ok := tunMap["strict_route"]; ok {
		t.Errorf("strict_route should be omitted (false/omitempty), got %v", tunMap["strict_route"])
	}

	if findOutboundByTag(outb, "direct") == nil {
		t.Error("missing direct outbound")
	}
	// No exits → no balancer, no exit-direct outbounds.
	if findOutboundByTag(outb, "balancer") != nil {
		t.Error("single-egress must not emit a fallback balancer")
	}

	if len(route) != 3 {
		t.Fatalf("want 3 route rules, got %d", len(route))
	}
	if route[0].Action != "sniff" {
		t.Errorf("rule 0 action = %q, want sniff", route[0].Action)
	}
	if route[1].Action != "hijack-dns" || len(route[1].Protocol) != 1 || route[1].Protocol[0] != "dns" {
		t.Errorf("rule 1 = %#v, want dns hijack-dns", route[1])
	}
	if route[2].Outbound != "direct" || len(route[2].Inbound) != 1 || route[2].Inbound[0] != "tun-in" {
		t.Errorf("rule 2 = %#v, want tun-in -> direct", route[2])
	}
}

// TestBuildAWGTUNOverlay_ForwardOutbound verifies that when ForwardOutbound is
// set (a linear AWG chain entry with a downstream hop), the tun-in catch-all
// routes to that inter-node outbound — NOT "direct". Without this, every AWG
// user egresses from the entry node and chain forwarding is silently broken
// (the highest-impact bug from the post-rework review: the overlay catch-all
// targeted "direct", shadowing the chain's inter-node forward).
func TestBuildAWGTUNOverlay_ForwardOutbound(t *testing.T) {
	inb, _, route := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
		ForwardOutbound:   "ch-mh-out-www.cloudflare.com",
	})
	_ = inb
	// The tun-in catch-all rule must target the inter-node forward, not direct.
	var tunRule *config.RouteRuleEntry
	for i := range route {
		if len(route[i].Inbound) == 1 && route[i].Inbound[0] == "tun-in" {
			tunRule = &route[i]
			break
		}
	}
	if tunRule == nil {
		t.Fatal("missing tun-in catch-all route rule")
	}
	if tunRule.Outbound != "ch-mh-out-www.cloudflare.com" {
		t.Errorf("tun-in catch-all outbound = %q, want ch-mh-out-www.cloudflare.com (inter-node forward)", tunRule.Outbound)
	}
	if tunRule.Outbound == "direct" {
		t.Error("tun-in catch-all targets direct — linear AWG entry would egress locally, breaking chain forwarding")
	}
}

// TestBuildAWGTUNOverlay_ForwardOutboundOverridesBalancer verifies
// ForwardOutbound takes priority over the balancer when both are set (in
// practice they're mutually exclusive — a multi-exit balancer has no chain hop
// — but the param priority must be deterministic, not surprising).
func TestBuildAWGTUNOverlay_ForwardOutboundOverridesBalancer(t *testing.T) {
	_, _, route := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
		ExitInterfaces:    []string{"awg-exit-n1", "awg-exit-n2"},
		BalancerTag:       "balancer",
		ForwardOutbound:   "ch-x-out-hop",
	})
	for _, r := range route {
		if len(r.Inbound) == 1 && r.Inbound[0] == "tun-in" {
			if r.Outbound != "ch-x-out-hop" {
				t.Errorf("tun-in outbound = %q, ForwardOutbound must win over balancer", r.Outbound)
			}
			return
		}
	}
	t.Fatal("missing tun-in route rule")
}

// TestBuildAWGTUNOverlay_MultiExitBalancer verifies the dns.idoctor.mom
// reference shape for a multi-exit balancer: TUN includes ONLY awg0 (the
// interface where user traffic arrives — NOT the awg-exit-nX client interfaces,
// which are outbound-side via bind_interface; capturing them would loop). One
// direct outbound per exit with bind_interface, a fallback group rotating
// across them, and the route targeting the balancer.
func TestBuildAWGTUNOverlay_MultiExitBalancer(t *testing.T) {
	inb, outb, route := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		// include_interface is awg0 only — exit interfaces are outbound-side.
		IncludeInterfaces: []string{"awg0"},
		ExitInterfaces:    []string{"awg-exit-n1", "awg-exit-n2", "awg-exit-n3"},
		BalancerTag:       "balancer",
	})

	tun := findInboundByType(inb, "tun")
	if tun == nil {
		t.Fatal("missing TUN inbound")
	}
	mustJSONFields(t, tun, map[string]any{
		"include_interface": []any{"awg0"},
	})

	for _, iface := range []string{"awg-exit-n1", "awg-exit-n2", "awg-exit-n3"} {
		ob := findOutboundByTag(outb, exitOutboundTag(iface))
		if ob == nil {
			t.Errorf("missing direct outbound for %s", iface)
			continue
		}
		mustJSONFields(t, ob, map[string]any{
			"type":           "direct",
			"bind_interface": iface,
		})
	}

	fb := findOutboundByTag(outb, "balancer")
	if fb == nil {
		t.Fatal("missing fallback balancer outbound")
	}
	var fbMap map[string]any
	if err := json.Unmarshal(fb, &fbMap); err != nil {
		t.Fatal(err)
	}
	if fbMap["type"] != "fallback" {
		t.Errorf("balancer type = %v, want fallback", fbMap["type"])
	}
	if fbMap["blacklist_timeout"] != "30s" {
		t.Errorf("blacklist_timeout = %v, want 30s", fbMap["blacklist_timeout"])
	}
	wantOutbounds := []any{exitOutboundTag("awg-exit-n1"), exitOutboundTag("awg-exit-n2"), exitOutboundTag("awg-exit-n3")}
	if !jsonEqual(fbMap["outbounds"], wantOutbounds) {
		t.Errorf("balancer outbounds = %#v, want %#v", fbMap["outbounds"], wantOutbounds)
	}

	if route[2].Outbound != "balancer" {
		t.Errorf("route target = %q, want balancer", route[2].Outbound)
	}
}

// TestBuildAWGTUNOverlay_NoEndpoints verifies the renderer never emits a
// userspace WireGuard endpoint — the kernel owns the AWG interfaces. This is
// the core architectural invariant (userspace wireguard-go panics with
// amnezia). Any "wireguard"-typed inbound/endpoint would be a regression.
func TestBuildAWGTUNOverlay_NoEndpoints(t *testing.T) {
	inb, outb, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
		ExitInterfaces:    []string{"awg-exit-n1"},
	})
	for _, raw := range inb {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["type"] == "wireguard" {
			t.Errorf("TUN-overlay must NOT emit a userspace wireguard inbound: %s", string(raw))
		}
	}
	for _, raw := range outb {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["type"] == "wireguard" {
			t.Errorf("TUN-overlay must NOT emit a userspace wireguard outbound: %s", string(raw))
		}
	}
}

// TestExitOutboundTag verifies the awg-exit-nX → outbound tag derivation stays
// stable (route rules and the fallback group reference it by this tag).
func TestExitOutboundTag(t *testing.T) {
	cases := map[string]string{
		"awg-exit-n1": "n1-direct",
		"awg-exit-n7": "n7-direct",
		"awg0":        "awg0-direct",
	}
	for in, want := range cases {
		if got := exitOutboundTag(in); got != want {
			t.Errorf("exitOutboundTag(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestBuildAWGTUNOverlay_DefaultsApply verifies empty IncludeInterfaces
// defaults to awg0 and empty FinalOutbound to direct (a minimal params struct
// still produces a valid overlay).
func TestBuildAWGTUNOverlay_DefaultsApply(t *testing.T) {
	inb, _, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{})
	tun := findInboundByType(inb, "tun")
	if tun == nil {
		t.Fatal("missing TUN inbound with default params")
	}
	var m map[string]any
	json.Unmarshal(tun, &m)
	ifaces, _ := m["include_interface"].([]any)
	if len(ifaces) != 1 || ifaces[0] != "awg0" {
		t.Errorf("default include_interface = %#v, want [awg0]", ifaces)
	}
}

// TestBuildAWGTUNOverlay_AutoRedirectDefaultOff verifies auto_redirect is OFF by
// default (it's a P0a candidate §21.5 #0 but breaks sing-box check on hosts
// without nftables, so opt-in only). OFF with omitempty means the field is
// ABSENT from the JSON (sing-box treats absent as false).
func TestBuildAWGTUNOverlay_AutoRedirectDefaultOff(t *testing.T) {
	inb, _, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{})
	tun := findInboundByType(inb, "tun")
	if tun == nil {
		t.Fatal("missing TUN inbound")
	}
	var m map[string]any
	if err := json.Unmarshal(tun, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, present := m["auto_redirect"]; present && v != false {
		t.Errorf("default auto_redirect = %#v present, want absent or false", v)
	}
}

// TestBuildAWGTUNOverlay_AutoRedirectOptIn verifies setting AutoRedirect to true
// renders auto_redirect:true in the TUN inbound (the operator opt-in path for
// the P0a §21.5 #0 candidate fix on a confirmed-clean VPS).
func TestBuildAWGTUNOverlay_AutoRedirectOptIn(t *testing.T) {
	on := true
	inb, _, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{AutoRedirect: &on})
	tun := findInboundByType(inb, "tun")
	if tun == nil {
		t.Fatal("missing TUN inbound")
	}
	var m map[string]any
	if err := json.Unmarshal(tun, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["auto_redirect"] != true {
		t.Errorf("opt-in auto_redirect = %#v, want true", m["auto_redirect"])
	}
}

// TestBuildAWGTUNOverlay_JSONValid ensures the rendered inbounds/outbounds are
// valid JSON (marshal round-trip), guarding against struct-tag typos.
func TestBuildAWGTUNOverlay_JSONValid(t *testing.T) {
	inb, outb, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
		ExitInterfaces:    []string{"awg-exit-n1", "awg-exit-n2"},
		BalancerTag:       "balancer",
	})
	for _, raw := range append(append([]json.RawMessage{}, inb...), outb...) {
		if !json.Valid(raw) {
			t.Errorf("invalid JSON: %s", string(raw))
		}
	}
}

// Smoke check that the include_interface ordering/contents render as a JSON
// array (not an object) — sing-box rejects a string where an array is expected.
func TestBuildAWGTUNOverlay_IncludeInterfaceIsArray(t *testing.T) {
	inb, _, _ := BuildAWGTUNOverlay(AWGTUNOverlayParams{
		IncludeInterfaces: []string{"awg0"},
	})
	tun := findInboundByType(inb, "tun")
	raw, _ := json.Marshal(tun)
	if !strings.Contains(string(raw), `"include_interface":["awg0"]`) {
		t.Errorf("include_interface must render as a JSON array; got %s", string(raw))
	}
}

// TestTunIncludeInterfaces_BalancerIncludesExitIfaces verifies the helper used
// by buildMergedNodeConfig returns awg0 PLUS every awg-exit-nX the balancer
// owns. Regression (live-verified 2026-07-04): without awg-exit-nX in
// include_interface, sing-box direct outbounds that bind_interface: awg-exit-nX
// time out on dial — the SYN-ACK arrives on the kernel awg-exit-nX but sing-box
// never captures it, so egress through the balancer silently fails. With the
// exit ifaces listed, sing-box captures the response and the connection
// completes (verified: curl through the tunnel returns the exit's public IP).
func TestTunIncludeInterfaces_BalancerIncludesExitIfaces(t *testing.T) {
	node := &model.ChainNode{
		ExitAWGLinks: []model.AWGExitLink{
			{TargetID: "exit1", InterfaceName: "awg-exit-n1"},
			{TargetID: "exit2", InterfaceName: "awg-exit-n2"},
		},
	}
	got := tunIncludeInterfaces(node)
	want := map[string]bool{"awg0": true, "awg-exit-n1": true, "awg-exit-n2": true}
	if len(got) != len(want) {
		t.Fatalf("tunIncludeInterfaces = %v, want %d entries (awg0 + 2 exit ifaces)", got, len(want))
	}
	for _, iface := range got {
		if !want[iface] {
			t.Errorf("tunIncludeInterfaces: unexpected iface %q (want awg0 + awg-exit-n1/n2)", iface)
		}
	}
	// Non-balancer node (no ExitAWGLinks) → just awg0.
	if got := tunIncludeInterfaces(&model.ChainNode{}); len(got) != 1 || got[0] != "awg0" {
		t.Errorf("non-balancer tunIncludeInterfaces = %v, want [awg0]", got)
	}
	// nil node → just awg0 (no panic).
	if got := tunIncludeInterfaces(nil); len(got) != 1 || got[0] != "awg0" {
		t.Errorf("nil-node tunIncludeInterfaces = %v, want [awg0]", got)
	}
}
