package chain

// awg3_entry_fix_test.go — regression tests for the v0.8.11 AWG 3.0 chain-entry
// fix (PROGRESS §39), live-found on the VladufQa node (5.188.19.239). Four bugs
// made an AWG3 chain entry unreachable even though the AWG3 render contract in
// awg3_mode_test.go held:
//
//	A. the userspace endpoint listened on chainEntryPort() (8443) while the
//	   client .conf and the kernel renderer both use the materialized inbound's
//	   Port (25086) — nothing was listening where clients dialed;
//	B. a leftover kernel awg-quick@awg0 from the pre-AWG3 deploy kept the UDP
//	   port, so sing-box crash-looped: "endpoint/awg[ch-VladVPN-user-in]: unable
//	   to update bind: listen udp4 0.0.0.0:8443: bind: address already in use";
//	C. the server resolved its preset from the CHAIN while the client resolved
//	   it from the PROFILE → divergent amnezia params (live: S1 15 vs 115);
//	E. the endpoint hardcoded address 10.8.0.1/32, ignoring the inbound's real
//	   subnet (10.8.1.1/24) where its peers live.

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// awg3ChainEntryFixture builds a levelized AWG chain whose entry references a
// materialized AWG3 profile inbound on the node — the exact shape the live
// deploy had. entryPort is the inbound's own port; the fixture asserts it
// differs from the chain's user-entry port so the port-source bug is observable.
func awg3ChainEntryFixture(t *testing.T, entryPort int, obfuscation, serverAddr string) (*model.Chain, *model.NodeInfo, *model.NodeInbound) {
	t.Helper()
	const profID = "prof-awg3"
	priv, _ := genPriv(t)
	ib := model.NodeInbound{
		Protocol:         "awg",
		Port:             entryPort,
		ProfileID:        profID,
		Tag:              profID,
		Source:           "standalone", // a profile shared with a chain entry keeps this
		ServerPrivKey:    priv,
		AWG3Mode:         true,
		Obfuscation:      obfuscation,
		AWGServerAddress: serverAddr,
	}
	EnsureInboundAWGMaterial(&ib, ResolveStandaloneAWGPreset(&ib))
	ni := &model.NodeInfo{
		Host:     model.Host{ID: "n1"},
		Inbounds: []model.NodeInbound{ib},
	}
	c := &model.Chain{
		Name:         "VladVPN",
		UserProtocol: model.UserProtocolAWG,
		Transport:    model.TransportXHTTP,
		Levels: []model.ChainLevel{{Nodes: []model.ChainNode{{
			ID: "n1", Addr: "5.188.19.239:22", Role: model.NodeRoleEntry, InboundRef: profID,
		}}}},
	}
	return c, ni, &ni.Inbounds[0]
}

// awg3EntryRole builds the entry chainRole for the fixture, with the CHAIN's
// preset set to the panel default (so a profile-preset win is observable).
func awg3EntryRole(c *model.Chain, chainPreset ConnectionPreset) *chainRole {
	return &chainRole{
		Chain:      c,
		Node:       &c.Levels[0].Nodes[0],
		IsEntry:    true,
		Preset:     chainPreset,
		LevelIndex: 0,
	}
}

// TestAWG3Mode_EndpointUsesInboundPort pins bug A: the userspace endpoint must
// listen on the MATERIALIZED inbound's port, not the chain's user-entry port.
func TestAWG3Mode_EndpointUsesInboundPort(t *testing.T) {
	const inboundPort = 25086
	c, ni, _ := awg3ChainEntryFixture(t, inboundPort, "", "")
	if got := chainEntryPort(c, "n1"); got != 8443 {
		t.Fatalf("fixture precondition: chainEntryPort = %d, want 8443 (must differ from the inbound port)", got)
	}
	users := []model.User{{Active: true, AWGPublicKey: genPub(t), AWGAddress: "10.8.0.2/32"}}
	_, _, endpoints, warns := buildChainRoleInOut(awg3EntryRole(c, GetDefaultPreset()), users, ni)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(endpoints) != 1 {
		t.Fatalf("want 1 AWG3 endpoint, got %d", len(endpoints))
	}
	if got := extractJSONInt(string(endpoints[0]), "listen_port"); got != inboundPort {
		t.Errorf("endpoint listen_port = %d, want %d (the materialized inbound's port)", got, inboundPort)
	}
}

// TestAWG3Mode_TeardownsKernelUnit pins bug B: an AWG3 chain entry must report
// awg0 for teardown (the pre-AWG3 kernel unit still holds the UDP port), while
// interfaces that ARE still rendered must never be torn down — the live node ran
// a legitimate second AWG interface with 3.16 GiB of traffic that must survive.
func TestAWG3Mode_TeardownsKernelUnit(t *testing.T) {
	otherPriv, _ := genPriv(t)

	// Case 1 (the live VladufQa shape): an AWG3 chain entry is the node's only
	// AWG listener. No kernel .conf is rendered, so the stale awg-quick@awg0
	// left over from the pre-AWG3 deploy still owns UDP :port and must be torn
	// down — otherwise sing-box dies with "bind: address already in use".
	c, ni, _ := awg3ChainEntryFixture(t, 25086, "", "")
	files, _ := RenderNodeAWGConfs(ni, []*model.Chain{c}, nil, nil)
	if len(files) != 0 {
		t.Fatalf("AWG3-only node must render no kernel .conf, got %+v", files)
	}
	teardown := AWGTeardownInterfaces(ni, []*model.Chain{c}, files)
	if !containsStr(teardown, "awg0") {
		t.Errorf("AWG3 chain entry must tear down the stale kernel awg0, got %v", teardown)
	}

	// Case 2: a non-AWG3 standalone inbound on the same node still renders a
	// kernel conf. Whatever interface it claims is REWRITTEN + restarted by
	// pushAWGConfs, so it must never appear in the teardown set — tearing it
	// down would kill a live kernel AWG listener (the node in the field ran a
	// second AWG interface carrying 3.16 GiB).
	c2, ni2, _ := awg3ChainEntryFixture(t, 25086, "", "")
	ni2.Inbounds = append(ni2.Inbounds, model.NodeInbound{
		Protocol: "awg", Port: 51999, Tag: "sa-live-awg", ServerPrivKey: otherPriv,
	})
	files2, _ := RenderNodeAWGConfs(ni2, []*model.Chain{c2}, nil, nil)
	if len(files2) == 0 {
		t.Fatal("the non-AWG3 standalone inbound must still render a kernel conf")
	}
	teardown2 := AWGTeardownInterfaces(ni2, []*model.Chain{c2}, files2)
	for _, f := range files2 {
		iface := awgIfaceFromService(f.ServiceName)
		if containsStr(teardown2, iface) {
			t.Errorf("teardown must NOT contain rendered interface %q (files: %+v, teardown: %v)", iface, files2, teardown2)
		}
	}

	// Case 3: a node with NO AWG3 inbound must have an empty teardown set — the
	// kernel path stays completely untouched (AGENTS #10/#11).
	kernelOnly := &model.NodeInfo{
		Host: model.Host{ID: "n2"},
		Inbounds: []model.NodeInbound{{
			Protocol: "awg", Port: 51820, Tag: "sa-0-awg", ServerPrivKey: otherPriv,
		}},
	}
	kFiles, _ := RenderNodeAWGConfs(kernelOnly, nil, nil, nil)
	if td := AWGTeardownInterfaces(kernelOnly, nil, kFiles); len(td) != 0 {
		t.Errorf("non-AWG3 node must have an empty teardown set, got %v", td)
	}

	// Case 4: an AWG3 STANDALONE inbound (no chain) also owns its port from
	// inside sing-box → its kernel awg0 must be torn down.
	saPriv, _ := genPriv(t)
	saIB := model.NodeInbound{
		Protocol: "awg", Port: 51841, Tag: "sa-awg3", ServerPrivKey: saPriv, AWG3Mode: true,
	}
	EnsureInboundAWGMaterial(&saIB, GetDefaultPreset())
	saNode := &model.NodeInfo{Host: model.Host{ID: "n3"}, Inbounds: []model.NodeInbound{saIB}}
	saFiles, _ := RenderNodeAWGConfs(saNode, nil, nil, nil)
	if td := AWGTeardownInterfaces(saNode, nil, saFiles); !containsStr(td, "awg0") {
		t.Errorf("AWG3 standalone inbound must tear down kernel awg0, got %v", td)
	}
}

// TestAWG3Mode_ServerAddressFromInbound pins bug E: the endpoint's own tunnel
// address must come from the inbound's subnet, so server and peers share a /24.
func TestAWG3Mode_ServerAddressFromInbound(t *testing.T) {
	c, ni, _ := awg3ChainEntryFixture(t, 25086, "", "10.8.1.1/24")
	users := []model.User{{Active: true, AWGPublicKey: genPub(t), AWGAddress: "10.8.1.2/32"}}
	_, _, endpoints, _ := buildChainRoleInOut(awg3EntryRole(c, GetDefaultPreset()), users, ni)
	if len(endpoints) != 1 {
		t.Fatalf("want 1 AWG3 endpoint, got %d", len(endpoints))
	}
	s := string(endpoints[0])
	if !strings.Contains(s, `"10.8.1.1/32"`) {
		t.Errorf("endpoint address must be 10.8.1.1/32 (host part of the inbound subnet):\n%s", s)
	}
	if strings.Contains(s, `"10.8.0.1/32"`) {
		t.Errorf("endpoint must NOT fall back to the hardcoded 10.8.0.1/32:\n%s", s)
	}
	// Empty AWGServerAddress keeps the historical default.
	if got := awgEndpointServerAddress(""); got != "10.8.0.1/32" {
		t.Errorf("empty AWGServerAddress → %q, want 10.8.0.1/32", got)
	}
	// A bare host address (no mask) is accepted too.
	if got := awgEndpointServerAddress("10.9.0.1"); got != "10.9.0.1/32" {
		t.Errorf("bare host → %q, want 10.9.0.1/32", got)
	}
}

// TestChainEntryPreset_ServerClientMatch pins bug C: the amnezia parameters in
// the server's endpoint JSON and in the client's .conf must be IDENTICAL. Live
// divergence: server S1=15 (chain preset) vs client S1=115 (profile preset).
func TestChainEntryPreset_ServerClientMatch(t *testing.T) {
	profPreset := awg3AlternatePreset(t)
	c, ni, ib := awg3ChainEntryFixture(t, 25086, profPreset.Name, "10.8.1.1/24")
	user := model.User{
		Name: "vlad", Active: true,
		AWGPrivateKey: "CLIENTPRIV", AWGPublicKey: genPub(t), AWGAddress: "10.8.1.2/32",
	}
	// The chain preset is the panel default and must LOSE to the profile's.
	_, _, endpoints, _ := buildChainRoleInOut(awg3EntryRole(c, GetDefaultPreset()), []model.User{user}, ni)
	if len(endpoints) != 1 {
		t.Fatalf("want 1 AWG3 endpoint, got %d", len(endpoints))
	}
	serverJSON := string(endpoints[0])

	clientConf, err := RenderClientAWGConf(ClientConfigParams{
		Chain: c, User: &user,
		EntryInboundResolver: func(nodeID, profileID string) *model.NodeInbound {
			if nodeID == "n1" && profileID == ib.ProfileID {
				return ib
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RenderClientAWGConf: %v", err)
	}

	// S1-S4 must match exactly (this is the 15-vs-115 regression).
	for _, k := range []string{"s1", "s2", "s3", "s4"} {
		server := extractJSONInt(serverJSON, k)
		client := confInt(t, clientConf, strings.ToUpper(k))
		if server != client {
			t.Errorf("%s mismatch: server endpoint %d vs client .conf %d", strings.ToUpper(k), server, client)
		}
	}
	// H1 must match too (header-junk ranges are part of the same handshake).
	if server, client := extractJSONString(serverJSON, "h1"), confStr(t, clientConf, "H1"); server != client {
		t.Errorf("H1 mismatch: server %q vs client %q", server, client)
	}
	// Jc must match (the flood profile is negotiated implicitly, AGENTS #17).
	if server, client := extractJSONInt(serverJSON, "jc"), confInt(t, clientConf, "Jc"); server != client {
		t.Errorf("Jc mismatch: server %d vs client %d", server, client)
	}
	// The client must dial the port the server listens on.
	if server, client := extractJSONInt(serverJSON, "listen_port"), confInt(t, clientConf, "Endpoint port"); server != client {
		t.Errorf("port mismatch: server listens %d, client dials %d", server, client)
	}
}

// TestChainEntryPreset_EmptyObfuscationKeepsChainPreset pins the guard in
// ResolveChainEntryPreset: an inbound WITHOUT its own Obfuscation must keep the
// chain's preset. Falling back to the panel default here would silently break
// every already-connected client of a custom-preset chain.
func TestChainEntryPreset_EmptyObfuscationKeepsChainPreset(t *testing.T) {
	chainPreset := awg3AlternatePreset(t)
	ib := &model.NodeInbound{Protocol: "awg", Port: 25086, Obfuscation: ""}
	if got := ResolveChainEntryPreset(chainPreset, ib); got.Name != chainPreset.Name {
		t.Errorf("empty Obfuscation must keep the chain preset %q, got %q", chainPreset.Name, got.Name)
	}
	// A nil inbound (legacy render) also keeps the chain preset.
	if got := ResolveChainEntryPreset(chainPreset, nil); got.Name != chainPreset.Name {
		t.Errorf("nil inbound must keep the chain preset %q, got %q", chainPreset.Name, got.Name)
	}
	// A named preset on the inbound wins.
	def := GetDefaultPreset()
	ib2 := &model.NodeInbound{Protocol: "awg", Obfuscation: def.Name}
	if got := ResolveChainEntryPreset(chainPreset, ib2); got.Name != def.Name {
		t.Errorf("inbound preset must win: got %q, want %q", got.Name, def.Name)
	}
	// An unknown preset name falls back to the chain's (never to the default).
	ib3 := &model.NodeInbound{Protocol: "awg", Obfuscation: "no-such-preset"}
	if got := ResolveChainEntryPreset(chainPreset, ib3); got.Name != chainPreset.Name {
		t.Errorf("unknown preset must fall back to the chain preset %q, got %q", chainPreset.Name, got.Name)
	}
}

// awg3AlternatePreset returns a named AWG preset that is NOT the panel default,
// so a wrong-source preset resolution is observable. Skips when none exists.
func awg3AlternatePreset(t *testing.T) ConnectionPreset {
	t.Helper()
	def := GetDefaultPreset()
	for _, name := range []string{
		"russia_2026_awg_robust", "iran_2026_awg_robust", "china_2026_awg_robust",
		"maximum_stealth_awg_robust", "pro_2026_awg_robust",
	} {
		p, ok := GetPreset(name)
		if ok && p.Name != def.Name && p.AWG != nil {
			return p
		}
	}
	t.Skip("no alternate AWG preset available to contrast with the default")
	return ConnectionPreset{}
}

// (containsStr lives in presets_test.go — reused here.)

// confStr pulls an INI value ("H1 = 1-2" → "1-2") from a rendered .conf.
func confStr(t *testing.T, conf, key string) string {
	t.Helper()
	for _, line := range strings.Split(conf, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	t.Fatalf("key %q not found in .conf:\n%s", key, conf)
	return ""
}

// confInt pulls an integer INI value from a rendered .conf. The special key
// "Endpoint port" returns the port from the "Endpoint = host:port" line.
func confInt(t *testing.T, conf, key string) int {
	t.Helper()
	raw := ""
	if key == "Endpoint port" {
		ep := confStr(t, conf, "Endpoint")
		i := strings.LastIndexByte(ep, ':')
		if i < 0 {
			t.Fatalf("Endpoint %q has no port", ep)
		}
		raw = ep[i+1:]
	} else {
		raw = confStr(t, conf, key)
	}
	if raw == "" {
		t.Fatalf("key %q is empty", key)
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			t.Fatalf("key %q value %q is not an integer", key, raw)
		}
		n = n*10 + int(ch-'0')
	}
	return n
}
