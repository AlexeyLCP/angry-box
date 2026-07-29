package chain

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// meshChain builds a levelized chain: 2 entries → 2 exits (xhttp transport).
func meshChain() *model.Chain {
	return &model.Chain{
		Name:         "mesh",
		UserProtocol: model.UserProtocolVLESSReality,
		Transport:    model.TransportXHTTP,
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{
				{ID: "e1", Addr: "1.1.1.1:22", InboundRef: "chain-entry-mesh"},
				{ID: "e2", Addr: "2.2.2.2:22", InboundRef: "chain-entry-mesh"},
			}},
			{ID: "l1", Nodes: []model.ChainNode{
				{ID: "x1", Addr: "3.3.3.3:22", Port: 443},
				{ID: "x2", Addr: "4.4.4.4:22", Port: 443},
			}},
		},
	}
}

// seedMeshTransit fills the mesh chain's exit nodes with REAL transit key
// material (the Reality outbound builder derives the pubkey from the private
// key and drops invalid material silently).
func seedMeshTransit(t *testing.T, c *model.Chain) {
	t.Helper()
	preset := resolveChainPreset(c)
	c.EachNode(func(_ int, n *model.ChainNode) {
		if n.TransitPrivKey != "" {
			return
		}
		p, err := generateHopParams(443, &preset)
		if err != nil {
			t.Fatalf("generateHopParams: %v", err)
		}
		n.TransitPrivKey = p.PrivateKey
		n.TransitShortID = p.ShortID
		n.TransitUUID = p.UUID
	})
	c.Nodes = c.AllNodes()
}

func TestBuildStrategyGroupOutbound_RenderShapes(t *testing.T) {
	members := []string{"out-a", "out-b"}

	// Default ("") = fallback round-robin.
	raw, err := buildStrategyGroupOutbound("", "grp", members)
	if err != nil {
		t.Fatalf("fallback: %v", err)
	}
	var fb struct {
		Type      string   `json:"type"`
		Tag       string   `json:"tag"`
		Outbounds []string `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &fb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fb.Type != "fallback" || fb.Tag != "grp" || len(fb.Outbounds) != 2 {
		t.Errorf("fallback shape: %+v", fb)
	}

	// Explicit fallback same as default.
	raw2, _ := buildStrategyGroupOutbound(model.StrategyFallback, "grp", members)
	if string(raw) != string(raw2) {
		t.Errorf("default != explicit fallback:\n%s\n%s", raw, raw2)
	}

	// urltest.
	raw, err = buildStrategyGroupOutbound(model.StrategyURLTest, "grp", members)
	if err != nil {
		t.Fatalf("urltest: %v", err)
	}
	var ut struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Interval  string `json:"interval"`
		Tolerance int    `json:"tolerance"`
	}
	_ = json.Unmarshal(raw, &ut)
	if ut.Type != "urltest" || ut.URL == "" || ut.Interval == "" {
		t.Errorf("urltest shape: %+v", ut)
	}

	// failover = urltest approximation (tight interval, zero tolerance).
	raw, err = buildStrategyGroupOutbound(model.StrategyFailover, "grp", members)
	if err != nil {
		t.Fatalf("failover: %v", err)
	}
	var fo struct {
		Type      string `json:"type"`
		Interval  string `json:"interval"`
		Tolerance int    `json:"tolerance"`
	}
	_ = json.Unmarshal(raw, &fo)
	if fo.Type != "urltest" || fo.Interval != "1m" || fo.Tolerance != 0 {
		t.Errorf("failover shape: %+v", fo)
	}

	// selector pins Default to the first member.
	raw, err = buildStrategyGroupOutbound(model.StrategySelector, "grp", members)
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	var sel struct {
		Type    string `json:"type"`
		Default string `json:"default"`
	}
	_ = json.Unmarshal(raw, &sel)
	if sel.Type != "selector" || sel.Default != "out-a" {
		t.Errorf("selector shape: %+v", sel)
	}

	// Errors: unknown strategy, empty members.
	if _, err := buildStrategyGroupOutbound("bogus", "grp", members); err == nil {
		t.Error("unknown strategy should fail loudly")
	}
	if _, err := buildStrategyGroupOutbound("", "grp", nil); err == nil {
		t.Error("empty members should fail")
	}
}

func TestResolveChainRoles_Levels(t *testing.T) {
	c := meshChain()
	c.Nodes = c.AllNodes() // simulate the store's synced flat view

	r1 := resolveChainRoles("e1", []*model.Chain{c})
	if len(r1) != 1 {
		t.Fatalf("roles for e1: %d", len(r1))
	}
	if !r1[0].IsEntry || r1[0].IsTransit || !r1[0].HasOutbound || r1[0].LevelIndex != 0 {
		t.Errorf("e1 role: %+v", r1[0])
	}
	if len(r1[0].NextNodes) != 2 {
		t.Errorf("e1 NextNodes: %+v", r1[0].NextNodes)
	}

	rx := resolveChainRoles("x1", []*model.Chain{c})
	if len(rx) != 1 {
		t.Fatalf("roles for x1: %d", len(rx))
	}
	if rx[0].IsEntry || !rx[0].IsTransit || rx[0].HasOutbound || rx[0].LevelIndex != 1 {
		t.Errorf("x1 role: %+v", rx[0])
	}
	if len(rx[0].NextNodes) != 0 {
		t.Errorf("x1 NextNodes should be empty (last level): %+v", rx[0].NextNodes)
	}
}

func TestBuildChainRoleInOut_MeshGroup(t *testing.T) {
	c := meshChain()
	seedMeshTransit(t, c)
	roles := resolveChainRoles("e1", []*model.Chain{c})
	_, outs, _, warns := buildChainRoleInOut(&roles[0], nil, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings: %v", warns)
	}
	// Expect 2 per-target outbounds + 1 fallback group.
	var tags []string
	for _, raw := range outs {
		var probe struct {
			Tag string `json:"tag"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			t.Fatalf("unmarshal outbound: %v", err)
		}
		tags = append(tags, probe.Tag)
	}
	if len(tags) != 3 {
		t.Fatalf("want 3 outbounds (2 members + 1 group), got %v", tags)
	}
	grpTag := levelGroupTag("mesh", 1)
	if tags[2] != grpTag {
		t.Errorf("group tag = %q, want %q (tags: %v)", tags[2], grpTag, tags)
	}
	// Members carry per-target suffixes; route rules reference the group.
	if !strings.Contains(tags[0], "x1") || !strings.Contains(tags[1], "x2") {
		t.Errorf("member tags should carry target IDs: %v", tags)
	}
	if got := chainInterNodeOutboundTag(&roles[0]); got != grpTag {
		t.Errorf("chainInterNodeOutboundTag = %q, want group %q", got, grpTag)
	}
}

func TestChainInterNodeOutboundTag_SingleKeepsLegacy(t *testing.T) {
	c := &model.Chain{
		Name:      "lin",
		Transport: model.TransportXHTTP,
		Nodes: []model.ChainNode{
			{ID: "a", Addr: "1.1.1.1:22"},
			{ID: "b", Addr: "2.2.2.2:22"},
		},
	}
	roles := resolveChainRoles("a", []*model.Chain{c})
	got := chainInterNodeOutboundTag(&roles[0])
	if strings.Contains(got, "grp") {
		t.Errorf("single-target chain must keep the legacy tag, got %q", got)
	}
	if !strings.HasPrefix(got, "ch-lin-out-") {
		t.Errorf("legacy tag shape: %q", got)
	}
}

func TestValidateChainTopology(t *testing.T) {
	// Empty level.
	c := &model.Chain{Name: "bad", Levels: []model.ChainLevel{{ID: "l0", Nodes: nil}}}
	if err := validateChainTopology(c); err == nil {
		t.Error("empty level should fail")
	}
	// AWG transport with a grouped level → loud refusal.
	c2 := &model.Chain{
		Name: "awg-group", Transport: model.TransportAWG,
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "a"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "b"}, {ID: "c"}}},
		},
	}
	if err := validateChainTopology(c2); err == nil || !strings.Contains(err.Error(), "single-node levels") {
		t.Errorf("AWG transport + group should fail loudly, got %v", err)
	}
	// AWG transport with single-node levels → OK.
	c3 := &model.Chain{
		Name: "awg-linear", Transport: model.TransportAWG,
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "a"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "b"}}},
		},
	}
	if err := validateChainTopology(c3); err != nil {
		t.Errorf("linear AWG levels should pass: %v", err)
	}
	// Legacy chain (no levels) → no-op.
	if err := validateChainTopology(&model.Chain{Name: "legacy"}); err != nil {
		t.Errorf("legacy chain: %v", err)
	}
}

func TestEnsureChainEntryMaterialization(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "store.json"))
	prof := &model.InboundProfile{ID: "chain-entry-mesh", Name: "mesh entry", Protocol: "awg", Port: 8443}
	if err := st.SaveInboundProfile(prof); err != nil {
		t.Fatalf("SaveInboundProfile: %v", err)
	}
	c := meshChain() // entries carry InboundRef chain-entry-mesh
	c.AWGEntryServerPriv = "chain-priv"
	c.AWGEntryServerPub = "chain-pub"
	preset := resolveChainPreset(c)

	if err := EnsureChainEntryMaterialization(st, c, preset); err != nil {
		t.Fatalf("EnsureChainEntryMaterialization: %v", err)
	}
	ib := st.ProfileInboundOn("e1", prof.ID)
	if ib == nil {
		t.Fatal("no materialized inbound on e1")
	}
	// Chain's existing keypair preferred (clients keep working).
	if ib.ServerPrivKey != "chain-priv" || ib.ServerPubKey != "chain-pub" {
		t.Errorf("chain keypair not preferred: %+v", ib)
	}
	if ib.Source != "chain:mesh" || ib.Tag != prof.ID || ib.AWGServerAddress != "10.8.0.1/24" {
		t.Errorf("materialized shape: %+v", ib)
	}
	// Idempotent: creds preserved on a second run.
	ib.ServerPrivKey = "rotated"
	if ni, err := st.GetNodeInfo("e1"); err == nil {
		for i := range ni.Inbounds {
			if ni.Inbounds[i].ProfileID == prof.ID {
				ni.Inbounds[i].ServerPrivKey = "rotated"
			}
		}
		_ = st.SaveNodeInfo(ni)
	}
	if err := EnsureChainEntryMaterialization(st, c, preset); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if ib2 := st.ProfileInboundOn("e1", prof.ID); ib2.ServerPrivKey != "rotated" {
		t.Errorf("creds clobbered on re-materialization: %q", ib2.ServerPrivKey)
	}

	// Legacy chain: no-op.
	if err := EnsureChainEntryMaterialization(st, &model.Chain{Name: "legacy"}, preset); err != nil {
		t.Errorf("legacy chain: %v", err)
	}
}

func TestRenderChainEntryAWGConf_ViaInboundRef(t *testing.T) {
	c := testAWGChain() // entry creds + CPS material on the chain (migration shape)
	st := newV1Store(t, &storeFile{
		Hosts:  []*model.Host{{ID: "entry", Addr: "1.1.1.1:22"}},
		Chains: []*model.Chain{c},
	})
	migrateNow(t, st)

	mc, _ := st.GetChain("c1")
	ni, _ := st.GetNodeInfo("entry")
	users := []model.User{{ID: "u1", Active: true, AWGPublicKey: "user-pub", AWGAddress: "10.8.0.2/32"}}

	roles := resolveChainRoles("entry", []*model.Chain{mc})
	if len(roles) != 1 {
		t.Fatalf("roles: %d", len(roles))
	}
	viaRef := renderChainEntryAWGConf(ni, roles[0], users)
	legacy := renderChainEntryAWG0Conf(chainRole{
		Chain: mc, NodeIndex: 0, Node: &mc.Nodes[0],
		IsEntry: true, HasOutbound: true, Preset: resolveChainPreset(mc),
	}, users)
	if viaRef.Content != legacy.Content {
		t.Fatalf("InboundRef render diverged from legacy.\n--- ref ---\n%s\n--- legacy ---\n%s", viaRef.Content, legacy.Content)
	}

	// No double render: exactly one awg0 file, no phantom awg1.
	files, _ := RenderNodeAWGConfs(ni, []*model.Chain{mc}, map[string][]model.User{"c1": users}, nil)
	awg0, awg1 := 0, 0
	for _, f := range files {
		if strings.Contains(f.Path, "awg0.conf") {
			awg0++
		}
		if strings.Contains(f.Path, "awg1.conf") {
			awg1++
		}
	}
	if awg0 != 1 || awg1 != 0 {
		t.Errorf("double render: awg0=%d awg1=%d (files: %+v)", awg0, awg1, files)
	}
}

func TestRenderClientAWGConf_ViaResolver(t *testing.T) {
	c := testAWGChain()
	c.Levels = []model.ChainLevel{
		{ID: "l0", Nodes: []model.ChainNode{{ID: "entry", Addr: "1.1.1.1:22", InboundRef: "chain-entry-c1"}}},
		{ID: "l1", Nodes: []model.ChainNode{{ID: "exit", Addr: "2.2.2.2:22"}}},
	}
	c.Nodes = c.AllNodes()
	ib := &model.NodeInbound{
		Protocol: "awg", Port: 9999, ProfileID: "chain-entry-c1",
		ServerPubKey: "profile-pub-key", AWGServerAddress: "10.8.0.1/24",
		AWGCPSLevel: 2, AWGCPSMimicry: "quic",
		AWGCPSI1: "<b 0x0102>", AWGH1: "5-1000", AWGH2: "1001-2000", AWGH3: "2001-3000", AWGH4: "3001-4000",
	}
	conf, err := RenderClientAWGConf(ClientConfigParams{
		Chain: c,
		User:  &model.User{ID: "u1", AWGPrivateKey: "user-priv", AWGAddress: "10.8.0.2/32"},
		EntryInboundResolver: func(nodeID, profileID string) *model.NodeInbound {
			if nodeID == "entry" && profileID == "chain-entry-c1" {
				return ib
			}
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RenderClientAWGConf: %v", err)
	}
	if !strings.Contains(conf, "PublicKey = profile-pub-key") {
		t.Errorf("client conf must use the profile server pubkey:\n%s", conf)
	}
	if !strings.Contains(conf, "Endpoint = 1.1.1.1:9999") {
		t.Errorf("client conf must use the profile port:\n%s", conf)
	}
	if !strings.Contains(conf, "I1 = <b 0x0102>") {
		t.Errorf("client conf must use the profile CPS material:\n%s", conf)
	}
}

func TestStandaloneInOut_MultiUserVLESS(t *testing.T) {
	ib := model.NodeInbound{
		Protocol: "vless-reality", Port: 443, Tag: "ib1",
		UUID: "shared-uuid", ServerPrivKey: "priv", ShortID: "sid",
	}
	users := map[string][]model.User{
		"ib1": {
			{ID: "u1", Name: "alice", Active: true, VLESSUUID: "alice-uuid"},
			{ID: "u2", Name: "bob", Active: false, VLESSUUID: "bob-uuid"}, // inactive — skipped
			{ID: "u3", Name: "carol", Active: true, VLESSUUID: ""},        // no creds — skipped
		},
	}
	ins, _ := buildStandaloneInOut(&ib, "ib1", users)
	if len(ins) != 1 {
		t.Fatalf("want 1 inbound, got %d", len(ins))
	}
	var parsed struct {
		Users []struct {
			Name string `json:"name"`
			UUID string `json:"uuid"`
		} `json:"users"`
	}
	if err := json.Unmarshal(ins[0], &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Users) != 2 {
		t.Fatalf("want shared+1 per-user entries, got %+v", parsed.Users)
	}
	if parsed.Users[0].UUID != "shared-uuid" || parsed.Users[1].UUID != "alice-uuid" {
		t.Errorf("users: %+v", parsed.Users)
	}
}

func TestDetectPortConflicts_ChainEntryNoSelfConflict(t *testing.T) {
	// A node that is an AWG chain entry AND carries the materialized entry
	// inbound (Source=chain:*) must NOT self-conflict on the entry port.
	c := testAWGChain()
	st := newV1Store(t, &storeFile{
		Hosts:  []*model.Host{{ID: "entry", Addr: "1.1.1.1:22"}},
		Chains: []*model.Chain{c},
	})
	migrateNow(t, st)
	mc, _ := st.GetChain("c1")
	ni, _ := st.GetNodeInfo("entry")
	_, _, err := buildMergedNodeConfig(MergedNodeConfigParams{
		NodeInfo:   ni,
		NodeChains: []*model.Chain{mc},
	})
	if err != nil {
		t.Fatalf("phantom port conflict for chain-entry materialization: %v", err)
	}
}

// TestRenderNodeAWGConfs_ProfileEntryNotDoubleRendered is the regression test
// for the live deploy failure: a profile inbound (Source="standalone")
// referenced as a chain entry must render EXACTLY ONE awg0.conf — the
// standalone loop must skip it (previously it double-rendered on awg1 and the
// second awg-quick unit failed on the duplicate listen port).
func TestRenderNodeAWGConfs_ProfileEntryNotDoubleRendered(t *testing.T) {
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	ni := &model.NodeInfo{
		Host: model.Host{ID: "n1", Addr: "n1:22"},
		Inbounds: []model.NodeInbound{{
			Protocol: "awg", Port: 51840, Source: "standalone", Tag: "p1", ProfileID: "p1",
			ServerPrivKey: "priv", ServerPubKey: "pub", AWGServerAddress: "10.8.0.1/24",
		}},
	}
	c := &model.Chain{
		Name:         "live",
		UserProtocol: model.UserProtocolAWG,
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", Addr: "n1:22", InboundRef: "p1"}}},
		},
	}
	c.Nodes = c.AllNodes()
	_ = prof
	users := []model.User{{ID: "u1", Active: true, AWGPublicKey: "upub", AWGAddress: "10.8.0.2/32"}}
	files, _ := RenderNodeAWGConfs(ni, []*model.Chain{c}, map[string][]model.User{"live": users}, nil)
	awg0, awg1 := 0, 0
	for _, f := range files {
		if strings.Contains(f.Path, "awg0.conf") {
			awg0++
		}
		if strings.Contains(f.Path, "awg1.conf") {
			awg1++
		}
	}
	if awg0 != 1 || awg1 != 0 {
		t.Fatalf("double render: awg0=%d awg1=%d (files: %+v)", awg0, awg1, files)
	}

	// The merged config must also skip the profile inbound in the standalone
	// loop (no phantom port conflict / duplicate listener).
	_, _, err := buildMergedNodeConfig(MergedNodeConfigParams{
		NodeInfo: ni, NodeChains: []*model.Chain{c},
		UsersByChain: map[string][]model.User{"live": users},
	})
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
}

// TestIsChainEntryInbound covers the reference check directly.
func TestIsChainEntryInbound(t *testing.T) {
	c := &model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", InboundRef: "p1"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "n2"}}},
		},
	}
	if !IsChainEntryInbound([]*model.Chain{c}, "n1", &model.NodeInbound{ProfileID: "p1"}) {
		t.Error("profile referenced by entry node not detected")
	}
	if IsChainEntryInbound([]*model.Chain{c}, "n2", &model.NodeInbound{ProfileID: "p1"}) {
		t.Error("different node must not match")
	}
	if IsChainEntryInbound([]*model.Chain{c}, "n1", &model.NodeInbound{ProfileID: "other"}) {
		t.Error("different profile must not match")
	}
	if IsChainEntryInbound([]*model.Chain{c}, "n1", &model.NodeInbound{}) {
		t.Error("empty ProfileID must not match")
	}
}
