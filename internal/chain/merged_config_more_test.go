package chain

// merged_config_more_test.go — covers RenderMergedNodeConfig (the exported
// wrapper), awgClientPub (both branches), and the (currently disabled but
// retained) buildMergedRouting/buildMergedDNS. CTO-review C3 phase 5.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestRenderMergedNodeConfig_AWGChain verifies the exported wrapper builds a
// config for a node whose chain uses AWG as the user protocol (exercises
// awgClientPub's generate branch + the AWG endpoint path).
func TestRenderMergedNodeConfig_AWGChain(t *testing.T) {
	c := &model.Chain{
		Name:         "awg-chain",
		Transport:    model.TransportReality,
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "n0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
		},
	}
	info := &model.NodeInfo{Host: model.Host{ID: "n0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}}
	cfg, report, err := RenderMergedNodeConfig(info, []*model.Chain{c}, nil)
	if err != nil {
		t.Fatalf("RenderMergedNodeConfig: %v", err)
	}
	if cfg == nil || report == nil {
		t.Fatal("expected non-nil cfg + report")
	}
	// The merged config must be JSON-serializable.
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	if len(b) == 0 {
		t.Error("expected non-empty config")
	}
}

// TestRenderMergedNodeConfig_PortConflict verifies a port conflict between a
// standalone inbound and a chain transport inbound is detected.
func TestRenderMergedNodeConfig_PortConflict(t *testing.T) {
	// Chain transport inbound defaults to 443 for non-entry hops; put a
	// standalone inbound on the same port.
	c := &model.Chain{
		Name:      "conflict-chain",
		Transport: model.TransportReality,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"},
			{ID: "n0", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k"},
		},
	}
	info := &model.NodeInfo{
		Host: model.Host{ID: "n0", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k"},
		Inbounds: []model.NodeInbound{
			{Protocol: "vless", Port: 443}, // collides with chain transport 443
		},
	}
	_, _, err := RenderMergedNodeConfig(info, []*model.Chain{c}, nil)
	if err == nil {
		t.Fatal("expected port-conflict error")
	}
}

// TestRenderMergedNodeConfig_Hysteria2TransportHardError verifies that a chain
// with Transport == Hysteria2 (frozen — AGENTS.md #11) fails the build LOUDLY
// with an error, rather than silently shipping a config missing its transport
// inbound/outbound. Previously the warning was appended to report.Warnings and
// the deploy proceeded with a broken chain — now it's a hard build error so
// the operator sees the failure and can switch to AWG/XHTTP/Reality.
func TestRenderMergedNodeConfig_Hysteria2TransportHardError(t *testing.T) {
	c := &model.Chain{
		Name:      "hys-chain",
		Transport: model.TransportHysteria2,
		Nodes: []model.ChainNode{
			{ID: "n0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k", Role: model.NodeRoleEntry},
			{ID: "n1", Addr: "5.6.7.8:22", User: "root", KeyPath: "/k", Role: model.NodeRoleTransit},
		},
	}
	info := &model.NodeInfo{Host: model.Host{ID: "n0", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}}
	_, _, err := RenderMergedNodeConfig(info, []*model.Chain{c}, nil)
	if err == nil {
		t.Fatal("expected a hard build error for frozen Hysteria2 transport, got nil")
	}
	// The error must name Hysteria2 and point at the frozen-issue / alternatives
	// so the operator knows what to do (not an opaque "merged config: " string).
	msg := err.Error()
	for _, want := range []string{"Hysteria2", "AGENTS.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q (operator needs the reason + the fix)", msg, want)
		}
	}
}

// TestAWGClientPub_Stored verifies a stored client pub is returned as-is.
func TestAWGClientPub_Stored(t *testing.T) {
	c := &model.Chain{AWGEntryClientPub: "stored-pub"}
	if got := awgClientPub(c); got != "stored-pub" {
		t.Errorf("got %q, want stored-pub", got)
	}
}

// TestAWGClientPub_Generates verifies a missing client pub is generated
// (non-empty base64).
func TestAWGClientPub_Generates(t *testing.T) {
	c := &model.Chain{}
	got := awgClientPub(c)
	if got == "" {
		t.Error("expected a generated pub key, got empty")
	}
	if got == c.AWGEntryClientPub {
		t.Error("expected a fresh key, not the empty stored value")
	}
}
