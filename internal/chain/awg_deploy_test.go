package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestRenderNodeAWGConfs_ChainEntry renders a chain AWG entry node's awg0.conf
// and verifies it carries the server key, listen port, and one [Peer] per
// credentialed user (with the chain's persisted amnezia when CPS is on).
func TestRenderNodeAWGConfs_ChainEntry(t *testing.T) {
	c := &model.Chain{
		Name:               "ce",
		UserProtocol:       model.UserProtocolAWG,
		Transport:          model.TransportXHTTP,
		AWGEntryServerPriv: awgServerPriv,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "n1.example.test:22", Role: model.NodeRoleEntry},
		},
	}
	users := []model.User{
		{Name: "alice", Active: true, AWGPublicKey: genPub(t), AWGAddress: "10.8.0.2/32"},
		{Name: "bob", Active: true, AWGPublicKey: genPub(t), AWGAddress: "10.8.0.3/32"},
		{Name: "inactive", Active: false, AWGPublicKey: genPub(t), AWGAddress: "10.8.0.4/32"},
		{Name: "nocreds", Active: true}, // no AWG creds -> skipped
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "n1"}}
	files := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{"ce": users}, nil)
	if len(files) != 1 {
		t.Fatalf("want 1 file (awg0.conf), got %d", len(files))
	}
	f := files[0]
	if f.Path != awg0ConfPath {
		t.Errorf("path = %q, want %q", f.Path, awg0ConfPath)
	}
	if f.ServiceName != "awg-quick@awg0" {
		t.Errorf("service = %q, want awg-quick@awg0", f.ServiceName)
	}
	if !strings.Contains(f.Content, "PrivateKey = "+awgServerPriv) {
		t.Error("missing server private key")
	}
	// 2 credentialed active users -> 2 [Peer] sections (inactive + nocreds skipped).
	if got := strings.Count(f.Content, "[Peer]"); got != 2 {
		t.Errorf("want 2 [Peer], got %d:\n%s", got, f.Content)
	}
	if !strings.Contains(f.Content, users[0].AWGPublicKey) || !strings.Contains(f.Content, users[1].AWGPublicKey) {
		t.Error("missing a credentialed user's public key")
	}
	if strings.Contains(f.Content, users[2].AWGPublicKey) {
		t.Error("inactive user must be skipped")
	}
}

// TestRenderNodeAWGConfs_MultiExitBalancer renders a balancer node's
// awg-exit-nX.conf files — one per ExitAWGLink — plus the user-entry awg0.conf
// when the balancer is also the chain entry (the dns.idoctor.mom pattern).
func TestRenderNodeAWGConfs_MultiExitBalancer(t *testing.T) {
	balPriv, _ := genPriv(t)
	exitPriv, exitPub := genPriv(t)
	c := &model.Chain{
		Name:               "mx",
		UserProtocol:       model.UserProtocolAWG,
		AWGEntryServerPriv: balPriv,
		Nodes: []model.ChainNode{
			{
				ID: "bal", Addr: "bal.example.test:22", Role: model.NodeRoleEntry,
				ExitTargets: []string{"exit1", "exit2"},
				ExitAWGLinks: []model.AWGExitLink{
					{TargetID: "exit1", InterfaceName: "awg-exit-n1", ClientPriv: "cpriv1", ClientPub: "cpub1", Address: "10.10.0.2/32", ClientPort: 51901},
					{TargetID: "exit2", InterfaceName: "awg-exit-n2", ClientPriv: "cpriv2", ClientPub: "cpub2", Address: "10.10.0.3/32", ClientPort: 51902},
				},
			},
			{ID: "exit1", Addr: "144.31.224.212:22", Role: model.NodeRoleExit, ExitAWGServerPriv: exitPriv, ExitAWGServerPub: exitPub, ExitAWGListenPort: 52000},
			{ID: "exit2", Addr: "144.31.157.106:22", Role: model.NodeRoleExit, ExitAWGServerPriv: exitPriv, ExitAWGServerPub: exitPub, ExitAWGListenPort: 52001},
		},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "bal"}}
	files := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{}, nil)
	// awg0.conf (user entry) + awg-exit-n1.conf + awg-exit-n2.conf.
	if len(files) != 3 {
		t.Fatalf("want 3 files (awg0 + 2 exits), got %d: %+v", len(files), files)
	}
	// First file is the user-entry awg0.conf.
	if files[0].ServiceName != "awg-quick@awg0" {
		t.Errorf("file 0 service = %q, want awg-quick@awg0", files[0].ServiceName)
	}
	// Two exit client confs, each bound to its awg-exit-nX interface.
	exitFiles := files[1:]
	for i, want := range []string{"awg-exit-n1", "awg-exit-n2"} {
		if exitFiles[i].ServiceName != "awg-quick@"+want {
			t.Errorf("exit file %d service = %q, want awg-quick@%s", i, exitFiles[i].ServiceName, want)
		}
		if !strings.Contains(exitFiles[i].Content, "PrivateKey = "+c.Nodes[0].ExitAWGLinks[i].ClientPriv) {
			t.Errorf("exit %s missing client private key", want)
		}
		if !strings.Contains(exitFiles[i].Content, exitPub) {
			t.Errorf("exit %s missing exit server public key", want)
		}
	}
}

// TestRenderNodeAWGConfs_ExitServer renders a Role=exit node's awg0.conf — the
// server end of an exit link, accepting the balancer's tunnel as its peer.
func TestRenderNodeAWGConfs_ExitServer(t *testing.T) {
	exitPriv, exitPub := genPriv(t)
	c := &model.Chain{
		Name:         "mx",
		UserProtocol: model.UserProtocolAWG,
		Nodes: []model.ChainNode{
			{ID: "bal", Addr: "bal.example.test:22", Role: model.NodeRoleEntry, ExitTargets: []string{"exit1"}},
			{
				ID: "exit1", Addr: "144.31.224.212:22", Role: model.NodeRoleExit,
				ExitAWGServerPriv: exitPriv, ExitAWGServerPub: exitPub, ExitAWGListenPort: 52000,
			},
		},
	}
	// The balancer has an ExitAWGLinks entry targeting exit1 — that's where the
	// exit server reads the balancer's client pubkey + inner IP from.
	c.Nodes[0].ExitAWGLinks = []model.AWGExitLink{
		{TargetID: "exit1", InterfaceName: "awg-exit-n1", ClientPriv: "cpriv", ClientPub: "BALCLIENTPUB", Address: "10.10.0.2/32", ClientPort: 51901},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "exit1"}}
	files := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{}, nil)
	if len(files) != 1 {
		t.Fatalf("want 1 file (exit awg0.conf), got %d", len(files))
	}
	f := files[0]
	if f.ServiceName != "awg-quick@awg0" {
		t.Errorf("service = %q, want awg-quick@awg0", f.ServiceName)
	}
	if !strings.Contains(f.Content, "PrivateKey = "+exitPriv) {
		t.Error("missing exit server private key")
	}
	if !strings.Contains(f.Content, "ListenPort = 52000") {
		t.Error("missing exit server listen port")
	}
	if !strings.Contains(f.Content, "PublicKey = BALCLIENTPUB") {
		t.Error("missing balancer client public key in [Peer]")
	}
	if !strings.Contains(f.Content, "AllowedIPs = 10.10.0.2/32") {
		t.Error("missing balancer inner IP as AllowedIPs")
	}
}

// TestRenderNodeAWGConfs_Standalone renders a standalone AWG inbound (no chain)
// and verifies the awg0.conf carries the inbound's server key + port.
func TestRenderNodeAWGConfs_Standalone(t *testing.T) {
	ibPriv, _ := genPriv(t)
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "solo"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, Tag: "sa-awg", ServerPrivKey: ibPriv},
		},
	}
	files := RenderNodeAWGConfs(nodeInfo, nil, nil, nil)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	f := files[0]
	if f.ServiceName != "awg-quick@awg0" {
		t.Errorf("service = %q, want awg-quick@awg0", f.ServiceName)
	}
	if !strings.Contains(f.Content, "PrivateKey = "+ibPriv) {
		t.Error("missing standalone server private key")
	}
	if !strings.Contains(f.Content, "ListenPort = 51820") {
		t.Error("missing standalone listen port")
	}
}

// TestRenderNodeAWGConfs_NoAWG verifies a non-AWG node (Reality/XHTTP chain,
// no standalone AWG inbound) renders no kernel .conf files.
func TestRenderNodeAWGConfs_NoAWG(t *testing.T) {
	c := &model.Chain{
		Name:         "reality",
		UserProtocol: model.UserProtocolVLESSReality,
		Transport:    "reality",
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "n1.example.test:22", Role: model.NodeRoleEntry},
		},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "n1"}}
	files := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, nil, nil)
	if len(files) != 0 {
		t.Errorf("non-AWG node must render no kernel .conf files, got %d: %+v", len(files), files)
	}
}

// genPub returns a real WireGuard public key for test peers.
func genPub(t *testing.T) string {
	t.Helper()
	_, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// genPriv returns a real (priv, pub) WireGuard keypair for test nodes.
func genPriv(t *testing.T) (string, string) {
	t.Helper()
	return genAWGKeypair(t)
}
