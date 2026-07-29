package chain

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// newV1Store writes a storeFile at schema v1 directly (bypassing migrateOnce)
// and returns the store ready for an explicit migrateOnce() call.
func newV1Store(t *testing.T, sf *storeFile) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path) // migrateOnce on nonexistent file = no-op
	st.mu.Lock()
	sf.SchemaVersion = 1
	if err := st.writeStore(sf); err != nil {
		t.Fatalf("writeStore: %v", err)
	}
	st.mu.Unlock()
	return st
}

func migrateNow(t *testing.T, st *Store) {
	t.Helper()
	st.migrateOnce()
	st.mu.RLock()
	sf, err := st.readStore()
	st.mu.RUnlock()
	if err != nil {
		t.Fatalf("readStore after migration: %v", err)
	}
	if sf.SchemaVersion != currentSchemaVersion {
		t.Fatalf("schema_version = %d after migration, want %d", sf.SchemaVersion, currentSchemaVersion)
	}
}

func TestMigrateV2_StandaloneInboundsCollapse(t *testing.T) {
	st := newV1Store(t, &storeFile{
		// Hosts for n1/n2 — the v2->v3 orphan cleanup drops NodeInfos whose
		// Host is absent, so the fixtures must keep both nodes.
		Hosts: []*model.Host{
			{ID: "n1", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
			{ID: "n2", Addr: "2.2.2.2:22", User: "root", KeyPath: "/k"},
		},
		NodeInfos: []*model.NodeInfo{
			{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22"}, Inbounds: []model.NodeInbound{
				{Protocol: "awg", Port: 51840, Obfuscation: "default", Source: "standalone", Tag: "ib-n1", ServerPrivKey: "key1"},
			}},
			{Host: model.Host{ID: "n2", Addr: "2.2.2.2:22"}, Inbounds: []model.NodeInbound{
				{Protocol: "awg", Port: 51840, Obfuscation: "default", Source: "standalone", Tag: "ib-n2", ServerPrivKey: "key2"},
			}},
		},
	})
	migrateNow(t, st)

	profs, err := st.ListInboundProfiles()
	if err != nil {
		t.Fatalf("ListInboundProfiles: %v", err)
	}
	if len(profs) != 1 {
		t.Fatalf("want 1 collapsed profile, got %d (%+v)", len(profs), profs)
	}
	p := profs[0]
	if p.Protocol != "awg" || p.Port != 51840 || p.Obfuscation != "default" {
		t.Errorf("profile fields: %+v", p)
	}
	// Both materialized inbounds point at the profile; per-node creds untouched.
	for _, nodeID := range []string{"n1", "n2"} {
		ni, err := st.GetNodeInfo(nodeID)
		if err != nil {
			t.Fatalf("GetNodeInfo %s: %v", nodeID, err)
		}
		if ni.Inbounds[0].ProfileID != p.ID {
			t.Errorf("node %s: ProfileID = %q, want %q", nodeID, ni.Inbounds[0].ProfileID, p.ID)
		}
	}
	if ni, _ := st.GetNodeInfo("n1"); ni.Inbounds[0].ServerPrivKey != "key1" {
		t.Errorf("n1 creds clobbered: %q", ni.Inbounds[0].ServerPrivKey)
	}
	if ni, _ := st.GetNodeInfo("n2"); ni.Inbounds[0].ServerPrivKey != "key2" {
		t.Errorf("n2 creds clobbered: %q", ni.Inbounds[0].ServerPrivKey)
	}
	// ProfileNodes derives placement from the inbounds.
	nodes := st.ProfileNodes(p.ID)
	if len(nodes) != 2 {
		t.Errorf("ProfileNodes = %v, want 2 nodes", nodes)
	}
}

func TestMigrateV2_StandaloneDistinctGroups(t *testing.T) {
	st := newV1Store(t, &storeFile{
		NodeInfos: []*model.NodeInfo{
			{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22"}, Inbounds: []model.NodeInbound{
				{Protocol: "awg", Port: 51840, Obfuscation: "preset-a", Source: "standalone"},
				{Protocol: "awg", Port: 51840, Obfuscation: "preset-b", Source: "standalone"},
				{Protocol: "vless-reality", Port: 443, Source: "standalone"},
			}},
		},
	})
	migrateNow(t, st)
	profs, _ := st.ListInboundProfiles()
	if len(profs) != 3 {
		t.Fatalf("want 3 distinct profiles, got %d (%+v)", len(profs), profs)
	}
	ids := map[string]bool{}
	for _, p := range profs {
		if ids[p.ID] {
			t.Errorf("duplicate profile ID %q", p.ID)
		}
		ids[p.ID] = true
	}
}

func testAWGChain() *model.Chain {
	return &model.Chain{
		Name:         "c1",
		UserProtocol: model.UserProtocolAWG,
		Transport:    model.TransportXHTTP,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
			{ID: "exit", Addr: "2.2.2.2:22", User: "root", KeyPath: "/k"},
		},
		AWGEntryServerPriv: "entry-priv",
		AWGEntryServerPub:  "entry-pub",
		AWGCPSLevel:        2,
		AWGCPSMimicry:      "quic",
		AWGCPSI1:           "<b 0x0102>",
		AWGCPSI2:           "<b 0x0304>",
		AWGH1:              "5-1000",
		AWGH2:              "1001-2000",
		AWGH3:              "2001-3000",
		AWGH4:              "3001-4000",
	}
}

func TestMigrateV2_ChainAWGEntryProfile(t *testing.T) {
	c := testAWGChain()
	st := newV1Store(t, &storeFile{
		Hosts:  []*model.Host{{ID: "entry", Addr: "1.1.1.1:22"}, {ID: "exit", Addr: "2.2.2.2:22"}},
		Chains: []*model.Chain{c},
	})
	migrateNow(t, st)

	p, err := st.GetInboundProfile("chain-entry-c1")
	if err != nil {
		t.Fatalf("entry profile: %v", err)
	}
	if p.Protocol != "awg" || p.Port != 8443 {
		t.Errorf("profile proto/port: %q/%d", p.Protocol, p.Port)
	}

	// Entry node's materialized inbound carries the chain's EXISTING creds.
	ib := st.ProfileInboundOn("entry", p.ID)
	if ib == nil {
		t.Fatal("no materialized inbound on entry node")
	}
	if ib.ServerPrivKey != "entry-priv" || ib.ServerPubKey != "entry-pub" {
		t.Errorf("AWG keys not preserved: %+v", ib)
	}
	if ib.AWGServerAddress != "10.8.0.1/24" {
		t.Errorf("subnet: %q", ib.AWGServerAddress)
	}
	if ib.AWGCPSI1 != "<b 0x0102>" || ib.AWGCPSI2 != "<b 0x0304>" || ib.AWGH1 != "5-1000" || ib.AWGH4 != "3001-4000" {
		t.Errorf("CPS/H material not preserved: %+v", ib)
	}
	if ib.Source != "chain:c1" || ib.Tag != "chain-entry-c1" {
		t.Errorf("source/tag: %q/%q", ib.Source, ib.Tag)
	}

	// Chain is levelized: [entry] -> [exit], InboundRef set on the entry.
	mc, err := st.GetChain("c1")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if !mc.IsLevelized() {
		t.Fatal("chain not levelized after migration")
	}
	if len(mc.Levels) != 2 {
		t.Fatalf("want 2 levels, got %d", len(mc.Levels))
	}
	if len(mc.Levels[0].Nodes) != 1 || mc.Levels[0].Nodes[0].ID != "entry" {
		t.Errorf("level 0: %+v", mc.Levels[0].Nodes)
	}
	if mc.Levels[0].Nodes[0].InboundRef != "chain-entry-c1" {
		t.Errorf("InboundRef = %q", mc.Levels[0].Nodes[0].InboundRef)
	}
	if len(mc.Levels[1].Nodes) != 1 || mc.Levels[1].Nodes[0].ID != "exit" {
		t.Errorf("level 1: %+v", mc.Levels[1].Nodes)
	}
	// Flat view preserved in order.
	all := mc.AllNodes()
	if len(all) != 2 || all[0].ID != "entry" || all[1].ID != "exit" {
		t.Errorf("AllNodes order: %+v", all)
	}
	// SaveChain keeps Nodes in sync with Levels.
	if len(mc.Nodes) != 2 {
		t.Errorf("flat Nodes not synced on save: %d", len(mc.Nodes))
	}
}

func TestMigrateV2_ChainLevelsMultiEntryExit(t *testing.T) {
	c := &model.Chain{
		Name: "multi",
		Nodes: []model.ChainNode{
			{ID: "e1", Addr: "1.1.1.1:22", Role: model.NodeRoleEntry},
			{ID: "e2", Addr: "2.2.2.2:22", Role: model.NodeRoleEntry},
			{ID: "mid", Addr: "3.3.3.3:22"},
			{ID: "x1", Addr: "4.4.4.4:22", Role: model.NodeRoleExit},
			{ID: "x2", Addr: "5.5.5.5:22", Role: model.NodeRoleExit},
		},
	}
	st := newV1Store(t, &storeFile{Chains: []*model.Chain{c}})
	migrateNow(t, st)
	mc, _ := st.GetChain("multi")
	if len(mc.Levels) != 3 {
		t.Fatalf("want 3 levels, got %d (%+v)", len(mc.Levels), mc.Levels)
	}
	if len(mc.Levels[0].Nodes) != 2 {
		t.Errorf("entry level group: %+v", mc.Levels[0].Nodes)
	}
	if len(mc.Levels[1].Nodes) != 1 || mc.Levels[1].Nodes[0].ID != "mid" {
		t.Errorf("mid level: %+v", mc.Levels[1].Nodes)
	}
	if len(mc.Levels[2].Nodes) != 2 {
		t.Errorf("exit level group: %+v", mc.Levels[2].Nodes)
	}
	// Both entries got InboundRef.
	for _, n := range mc.Levels[0].Nodes {
		if n.InboundRef != "chain-entry-multi" {
			t.Errorf("entry %s InboundRef = %q", n.ID, n.InboundRef)
		}
	}
	// AllNodes flat order: entries, transit, exits.
	var order []string
	for _, n := range mc.AllNodes() {
		order = append(order, n.ID)
	}
	want := []string{"e1", "e2", "mid", "x1", "x2"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("flat order = %v, want %v", order, want)
		}
	}
}

func TestMigrateV2_VLESSEntryUUIDPreserved(t *testing.T) {
	c := &model.Chain{
		Name:         "vc",
		UserProtocol: model.UserProtocolVLESSReality,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "1.1.1.1:22", TransitUUID: "entry-transit-uuid"},
			{ID: "exit", Addr: "2.2.2.2:22"},
		},
	}
	st := newV1Store(t, &storeFile{
		// Hosts for entry/exit — v2->v3 orphan cleanup drops NodeInfos whose
		// Host is absent; the v2 migration materializes a NodeInfo for the
		// chain entry, so keep its Host or the test loses the materialized
		// inbound it asserts on.
		Hosts: []*model.Host{
			{ID: "entry", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
			{ID: "exit", Addr: "2.2.2.2:22", User: "root", KeyPath: "/k"},
		},
		Chains: []*model.Chain{c},
	})
	migrateNow(t, st)
	ib := st.ProfileInboundOn("entry", "chain-entry-vc")
	if ib == nil {
		t.Fatal("no materialized inbound")
	}
	// The chain VLESS entry rendered with the entry's TransitUUID — the
	// materialized inbound must carry the same UUID so clients keep working.
	if ib.UUID != "entry-transit-uuid" {
		t.Errorf("UUID = %q, want entry-transit-uuid", ib.UUID)
	}
}

func TestMigrateV2_Idempotent(t *testing.T) {
	c := testAWGChain()
	st := newV1Store(t, &storeFile{
		// Hosts for every node the migration materializes a NodeInfo for —
		// the v2->v3 orphan cleanup drops NodeInfos whose Host is absent, so
		// the test fixtures must keep entry/exit/n1 or the idempotency
		// re-run would see the materialized "entry" NodeInfo vanish.
		Hosts: []*model.Host{
			{ID: "n1", Addr: "9.9.9.9:22", User: "root", KeyPath: "/k"},
			{ID: "entry", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
			{ID: "exit", Addr: "2.2.2.2:22", User: "root", KeyPath: "/k"},
		},
		NodeInfos: []*model.NodeInfo{
			{Host: model.Host{ID: "n1", Addr: "9.9.9.9:22"}, Inbounds: []model.NodeInbound{
				{Protocol: "awg", Port: 51840, Source: "standalone"},
			}},
		},
		Chains: []*model.Chain{c},
	})
	migrateNow(t, st)
	profs1, _ := st.ListInboundProfiles()
	ni1, _ := st.GetNodeInfo("entry")
	// Re-run the whole migration chain — must be a no-op.
	st.migrateOnce()
	profs2, _ := st.ListInboundProfiles()
	ni2, _ := st.GetNodeInfo("entry")
	if len(profs1) != len(profs2) {
		t.Errorf("profiles duplicated on re-run: %d -> %d", len(profs1), len(profs2))
	}
	if len(ni1.Inbounds) != len(ni2.Inbounds) {
		t.Errorf("inbounds duplicated on re-run: %d -> %d", len(ni1.Inbounds), len(ni2.Inbounds))
	}
	mc, _ := st.GetChain("c1")
	if len(mc.Levels) != 2 {
		t.Errorf("levels changed on re-run: %d", len(mc.Levels))
	}
}

// TestMigrateV2_RenderEquivalence_AWGEntry is the key safety net: the
// materialized entry inbound must render an awg0.conf byte-identical to what
// the legacy chain-entry renderer produced from the chain's own fields — so
// switching the render source (Stage B) changes nothing on the wire.
func TestMigrateV2_RenderEquivalence_AWGEntry(t *testing.T) {
	c := testAWGChain()
	st := newV1Store(t, &storeFile{
		Hosts:  []*model.Host{{ID: "entry", Addr: "1.1.1.1:22"}},
		Chains: []*model.Chain{c},
	})

	users := []model.User{{
		ID: "u1", Name: "alice", Active: true,
		AWGPublicKey: "user-pub", AWGAddress: "10.8.0.2/32",
	}}

	// Legacy render: from the chain fields.
	preset := resolveChainPreset(c)
	legacy := renderChainEntryAWG0Conf(chainRole{
		Chain: c, NodeIndex: 0, Node: &c.Nodes[0],
		IsEntry: true, HasOutbound: true, Preset: preset,
	}, users)

	migrateNow(t, st)

	// New render: from the materialized inbound.
	ib := st.ProfileInboundOn("entry", "chain-entry-c1")
	if ib == nil {
		t.Fatal("no materialized inbound")
	}
	migrated := renderStandaloneAWGConf(ib, ib.Tag, map[string][]model.User{ib.Tag: users}, "awg0")

	if legacy.Content != migrated.Content {
		t.Fatalf("awg0.conf diverged after migration.\n--- legacy ---\n%s\n--- migrated ---\n%s", legacy.Content, migrated.Content)
	}
}

func TestStore_InboundProfileCRUD(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "store.json"))
	p := &model.InboundProfile{ID: "p1", Name: "AWG main", Protocol: "awg", Port: 51840}
	if err := st.SaveInboundProfile(p); err != nil {
		t.Fatalf("SaveInboundProfile: %v", err)
	}
	got, err := st.GetInboundProfile("p1")
	if err != nil || got.Name != "AWG main" {
		t.Fatalf("GetInboundProfile: %v %+v", err, got)
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt not stamped")
	}
	// Update in place.
	p.Name = "renamed"
	if err := st.SaveInboundProfile(p); err != nil {
		t.Fatalf("update: %v", err)
	}
	profs, _ := st.ListInboundProfiles()
	if len(profs) != 1 || profs[0].Name != "renamed" {
		t.Fatalf("update didn't replace: %+v", profs)
	}
	if err := st.DeleteInboundProfile("p1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetInboundProfile("p1"); !errors.Is(err, ErrInboundProfileNotFound) {
		t.Errorf("want ErrInboundProfileNotFound, got %v", err)
	}
}

func TestStore_DeleteInboundProfile_RefusesWhenReferenced(t *testing.T) {
	st := newV1Store(t, &storeFile{
		Chains: []*model.Chain{{
			Name: "c1",
			Nodes: []model.ChainNode{
				{ID: "entry", Addr: "1.1.1.1:22"},
				{ID: "exit", Addr: "2.2.2.2:22"},
			},
		}},
	})
	migrateNow(t, st) // creates chain-entry-c1 + sets InboundRef on entry
	if err := st.DeleteInboundProfile("chain-entry-c1"); !errors.Is(err, ErrInboundProfileInUse) {
		t.Errorf("want ErrInboundProfileInUse, got %v", err)
	}
	// Profile survives.
	if _, err := st.GetInboundProfile("chain-entry-c1"); err != nil {
		t.Errorf("profile deleted despite guard: %v", err)
	}
}

func TestChain_EachNode_MutatesLevels(t *testing.T) {
	c := &model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "a"}, {ID: "b"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "c"}}},
		},
	}
	c.EachNode(func(i int, n *model.ChainNode) {
		n.Port = 1000 + i
	})
	if c.Levels[0].Nodes[0].Port != 1000 || c.Levels[0].Nodes[1].Port != 1001 || c.Levels[1].Nodes[0].Port != 1002 {
		t.Fatalf("EachNode mutations did not land in levels: %+v", c.Levels)
	}
	// SetAllNodes redistributes into levels in flat order.
	c.SetAllNodes([]model.ChainNode{{ID: "a", Port: 1}, {ID: "b", Port: 2}, {ID: "c", Port: 3}})
	if c.Levels[0].Nodes[1].Port != 2 || c.Levels[1].Nodes[0].Port != 3 {
		t.Fatalf("SetAllNodes: %+v", c.Levels)
	}
}

func TestSaveChain_SyncsNodesFromLevels(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(filepath.Join(dir, "store.json"))
	c := &model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "a", Addr: "1.1.1.1:22"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "b", Addr: "2.2.2.2:22"}}},
		},
	}
	if err := st.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	got, _ := st.GetChain("c1")
	if len(got.Nodes) != 2 || got.Nodes[0].ID != "a" || got.Nodes[1].ID != "b" {
		t.Fatalf("flat Nodes not synced from levels: %+v", got.Nodes)
	}
	if len(got.Levels) != 2 {
		t.Fatalf("levels lost: %+v", got.Levels)
	}
}
