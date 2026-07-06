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
	files, _ := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{"ce": users}, nil)
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
	files, _ := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{}, nil)
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
		// Regression guard: amnezia MUST be on the exit-tunnel .conf. The default
		// preset (maximum_stealth_2026, CPS level 3) yields Jc=120. DPI can cut
		// plain WireGuard data packets even when the handshake passes — the real
		// dns.idoctor.mom server runs Jc=15 on its exit tunnels for this reason.
		// If this fails, renderBalancerExitConfs stopped wiring Amnezia.
		if !strings.Contains(exitFiles[i].Content, "Jc = ") {
			t.Errorf("exit %s missing amnezia block (Jc=) — DPI can block plain WG data", want)
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
	files, _ := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{}, nil)
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
	// Regression guard: the exit SERVER side must carry amnezia matching the
	// balancer's exit-client side (both render the chain's material). A
	// Jc-mismatch breaks the exit-tunnel handshake. Default preset → Jc=120.
	if !strings.Contains(f.Content, "Jc = ") {
		t.Error("exit server missing amnezia block (Jc=) — handshake will mismatch the balancer client side")
	}
	// Regression guard (live-verified 2026-07-04): the exit must MASQUERADE
	// BOTH the user subnet (10.8.0.0/24) AND the balancer-link subnet
	// (10.10.0.0/24). A balancer-routed client arrives at the exit with
	// source 10.10.0.2 (AWGExitLink.Address); MASQUERADE covering only
	// 10.8.0.0/24 leaves 10.10.0.0/24 un-NAT'd → internet can't route
	// responses back → egress through the balancer silently times out.
	if !strings.Contains(f.Content, "-s 10.8.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("exit server missing MASQUERADE for user subnet 10.8.0.0/24")
	}
	if !strings.Contains(f.Content, "-s 10.10.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("exit server missing MASQUERADE for balancer-link subnet 10.10.0.0/24 (balancer egress would time out)")
	}
}

// TestRenderExitServerAWGConf_MASQUERADEMultiSubnet verifies the MASQUERADE
// PostUp/PostDown handles a comma-separated subnet list — one iptables rule
// per subnet, both idempotent (-C || -A). Regression for the balancer-egress
// bug (live 2026-07-04): a single-subnet MASQUERADENetwork left balancer-link
// traffic (10.10.0.0/24) un-NAT'd, so egress through a balancer timed out.
func TestRenderExitServerAWGConf_MASQUERADEMultiSubnet(t *testing.T) {
	out := RenderExitServerAWGConf(ExitServerConfParams{
		ServerPrivateKey:  "P",
		ListenPort:        52000,
		MASQUERADENetwork: "10.8.0.0/24,10.10.0.0/24",
	})
	// Count actual MASQUERADE *adds* (-A POSTROUTING), not the -C check or -D del.
	adds := strings.Count(out, "iptables -t nat -A POSTROUTING -s 10.8.0.0/24")
	adds += strings.Count(out, "iptables -t nat -A POSTROUTING -s 10.10.0.0/24")
	if adds != 2 {
		t.Errorf("PostUp should emit 2 MASQUERADE -A rules (user + balancer-link), got %d in:\n%s", adds, out)
	}
	if !strings.Contains(out, "-s 10.8.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("missing user-subnet MASQUERADE")
	}
	if !strings.Contains(out, "-s 10.10.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("missing balancer-link subnet MASQUERADE")
	}
	// rp_filter=0 on exit awg0 — same invariant as the balancer client side
	// (live-verified 2026-07-04: return traffic to 10.10.0.2 / 10.8.0.x is
	// dropped by rp_filter=1 after awg-quick recreates the interface).
	if !strings.Contains(out, "net.ipv4.conf.awg0.rp_filter=0") {
		t.Error("exit server PostUp should set rp_filter=0 on awg0 (return traffic would be dropped)")
	}
	// PostDown must del both (idempotent `|| true`).
	if !strings.Contains(out, "iptables -t nat -D POSTROUTING -s 10.10.0.0/24 -o $WAN -j MASQUERADE 2>/dev/null || true") {
		t.Error("missing PostDown del for 10.10.0.0/24")
	}
	// Whitespace-tolerant: a space-separated list must parse the same way.
	outSpace := RenderExitServerAWGConf(ExitServerConfParams{
		ServerPrivateKey:  "P",
		ListenPort:        52000,
		MASQUERADENetwork: "10.8.0.0/24 10.10.0.0/24",
	})
	if !strings.Contains(outSpace, "-s 10.10.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("space-separated subnet list not parsed")
	}
}

// TestRenderExitServerAWGConf_MASQUERADESingleSubnet verifies a single subnet
// still emits exactly one MASQUERADE -A rule (back-compat with the pre-multi-
// subnet shape — non-balancer exits that only NAT the user subnet).
func TestRenderExitServerAWGConf_MASQUERADESingleSubnet(t *testing.T) {
	out := RenderExitServerAWGConf(ExitServerConfParams{
		ServerPrivateKey:  "P",
		ListenPort:        52000,
		MASQUERADENetwork: "10.8.0.0/24",
	})
	if got := strings.Count(out, "iptables -t nat -A POSTROUTING -s 10.8.0.0/24"); got != 1 {
		t.Errorf("single subnet should emit 1 MASQUERADE -A rule, got %d in:\n%s", got, out)
	}
	if !strings.Contains(out, "-s 10.8.0.0/24 -o $WAN -j MASQUERADE") {
		t.Error("missing user-subnet MASQUERADE")
	}
	if strings.Contains(out, "10.10.0.0/24") {
		t.Error("single-subnet render leaked 10.10.0.0/24 into the conf")
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
	files, _ := RenderNodeAWGConfs(nodeInfo, nil, nil, nil)
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
	files, _ := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, nil, nil)
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

// TestRenderNodeAWGConfs_StandalonePerInboundSubnet verifies a standalone AWG
// inbound with a distinct AWGServerAddress renders an awg0.conf carrying that
// subnet (the per-inbound server IP allocation, AGENTS.md #10).
func TestRenderNodeAWGConfs_StandalonePerInboundSubnet(t *testing.T) {
	ibPriv, _ := genPriv(t)
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "solo-sub"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, Tag: "sa-awg-sub", ServerPrivKey: ibPriv, AWGServerAddress: "10.8.1.1/24"},
		},
	}
	files, _ := RenderNodeAWGConfs(nodeInfo, nil, nil, nil)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "Address = 10.8.1.1/24") {
		t.Errorf("awg0.conf missing per-inbound server address; got:\n%s", files[0].Content)
	}
	// The default 10.8.0.1/24 must NOT appear when a distinct subnet is set.
	if strings.Contains(files[0].Content, "10.8.0.1/24") {
		t.Errorf("awg0.conf used the default 10.8.0.1/24 instead of the per-inbound 10.8.1.1/24")
	}
}

// TestRenderNodeAWGConfs_StandaloneDefaultSubnet verifies a standalone AWG
// inbound with an EMPTY AWGServerAddress falls back to the legacy default
// 10.8.0.1/24 (backward compat for inbounds created before the field existed).
func TestRenderNodeAWGConfs_StandaloneDefaultSubnet(t *testing.T) {
	ibPriv, _ := genPriv(t)
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "solo-def"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, Tag: "sa-awg-def", ServerPrivKey: ibPriv},
		},
	}
	files, _ := RenderNodeAWGConfs(nodeInfo, nil, nil, nil)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "Address = 10.8.0.1/24") {
		t.Errorf("awg0.conf missing default 10.8.0.1/24; got:\n%s", files[0].Content)
	}
}

// TestRenderNodeAWGConfs_CollisionWarning verifies the AGENTS.md #10 collision
// case: a node hosting BOTH a chain AWG entry (10.8.0.1/24) AND a standalone AWG
// inbound with the default (empty → 10.8.0.1/24) → the standalone is skipped
// with a loud warning in the returned warnings slice (was silently dropped
// before this change; now the operator sees the collision).
func TestRenderNodeAWGConfs_CollisionWarning(t *testing.T) {
	chainPriv, _ := genPriv(t)
	ibPriv, _ := genPriv(t)
	c := &model.Chain{
		Name:             "ce",
		UserProtocol:      model.UserProtocolAWG,
		Transport:        model.TransportXHTTP,
		AWGEntryServerPriv: chainPriv,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "n1.example.test:22", Role: model.NodeRoleEntry, Port: 51820},
		},
	}
	nodeInfo := &model.NodeInfo{
		Host: model.Host{ID: "n1"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51821, Tag: "sa-awg-collide", ServerPrivKey: ibPriv}, // default → 10.8.0.1/24, collides
		},
	}
	files, warns := RenderNodeAWGConfs(nodeInfo, []*model.Chain{c}, map[string][]model.User{"ce": nil}, nil)
	// Chain entry always produces awg0.conf.
	if len(files) != 1 {
		t.Fatalf("want 1 file (chain entry only), got %d", len(files))
	}
	if !strings.Contains(files[0].Content, "10.8.0.1/24") {
		t.Error("chain entry awg0.conf missing its 10.8.0.1/24")
	}
	// The standalone must be skipped (it would collide on awg0).
	if len(warns) == 0 {
		t.Fatal("expected a collision warning, got none (the old behavior silently dropped the standalone)")
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "collides") && strings.Contains(w, "sa-awg-collide") {
			found = true
		}
	}
	if !found {
		t.Errorf("collision warning not in %v", warns)
	}
}
