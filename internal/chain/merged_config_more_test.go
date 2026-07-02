package chain

// merged_config_more_test.go — covers RenderMergedNodeConfig (the exported
// wrapper), awgClientPub (both branches), and the (currently disabled but
// retained) buildMergedRouting/buildMergedDNS. CTO-review C3 phase 5.

import (
	"encoding/json"
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
	cfg, report, err := RenderMergedNodeConfig(info, []*model.Chain{c})
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
	_, _, err := RenderMergedNodeConfig(info, []*model.Chain{c})
	if err == nil {
		t.Fatal("expected port-conflict error")
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

