package chain

import (
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func newProfileTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(filepath.Join(t.TempDir(), "store.json"))
}

func seedNodes(t *testing.T, st *Store, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := st.SaveHost(&model.Host{ID: id, Addr: id + ":22"}); err != nil {
			t.Fatalf("SaveHost %s: %v", id, err)
		}
		if err := st.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: id, Addr: id + ":22"}}); err != nil {
			t.Fatalf("SaveNodeInfo %s: %v", id, err)
		}
	}
}

func TestApplyProfileToNodes_AddGeneratesCredsOnce(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1", "n2")
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	if err := st.SaveInboundProfile(prof); err != nil {
		t.Fatalf("SaveInboundProfile: %v", err)
	}

	res, err := ApplyProfileToNodes(st, prof, []string{"n1", "n2"})
	if err != nil {
		t.Fatalf("ApplyProfileToNodes: %v", err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("want 2 added, got %+v", res)
	}
	ib1 := st.ProfileInboundOn("n1", "p1")
	ib2 := st.ProfileInboundOn("n2", "p1")
	if ib1 == nil || ib2 == nil {
		t.Fatal("materialized inbounds missing")
	}
	if ib1.ServerPrivKey == "" || ib1.ServerPubKey == "" || ib2.ServerPrivKey == "" {
		t.Error("AWG creds not generated")
	}
	if ib1.AWGServerAddress == "" || ib2.AWGServerAddress == "" {
		t.Error("AWG subnet not allocated")
	}
	// Distinct nodes MAY share the same /24 (each node's awg interface is
	// local to it; user IP reuse across nodes is safe — AGENTS.md #10).
	if ib1.AWGServerAddress == "10.8.0.1/24" {
		t.Errorf("profile subnets must come from the 10.8.1+ range (10.8.0 is the chain-entry legacy default), got %q", ib1.AWGServerAddress)
	}
	if ib1.Tag != "p1" || ib1.ProfileID != "p1" || ib1.Source != "standalone" {
		t.Errorf("materialized shape: %+v", ib1)
	}

	// Re-save with the same nodes: creds preserved, nothing added.
	res2, err := ApplyProfileToNodes(st, prof, []string{"n1", "n2"})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if len(res2.Added) != 0 {
		t.Errorf("second apply added: %+v", res2.Added)
	}
	if ib1b := st.ProfileInboundOn("n1", "p1"); ib1b.ServerPrivKey != ib1.ServerPrivKey {
		t.Error("creds rotated on re-save (must be generated exactly once)")
	}
}

func TestApplyProfileToNodes_RemoveAndUpdate(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1", "n2")
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1", "n2"}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Param change on kept node: port updated, creds preserved.
	prof.Port = 51841
	_ = st.SaveInboundProfile(prof)
	res, err := ApplyProfileToNodes(st, prof, []string{"n1"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(res.Updated) != 1 || res.Updated[0] != "n1" {
		t.Errorf("updated: %+v", res)
	}
	if len(res.Removed) != 1 || res.Removed[0] != "n2" {
		t.Errorf("removed: %+v", res)
	}
	if ib := st.ProfileInboundOn("n1", "p1"); ib.Port != 51841 {
		t.Errorf("port not synced: %d", ib.Port)
	}
	if ib := st.ProfileInboundOn("n2", "p1"); ib != nil {
		t.Errorf("n2 inbound not removed: %+v", ib)
	}
	if got := st.ProfileNodes("p1"); len(got) != 1 || got[0] != "n1" {
		t.Errorf("ProfileNodes after diff: %v", got)
	}
}

func TestApplyProfileToNodes_RemoveBlockedByChainRef(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1")
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Chain references the profile on n1.
	c := &model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", Addr: "n1:22", InboundRef: "p1"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "n2", Addr: "n2:22"}}},
		},
	}
	if err := st.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	res, err := ApplyProfileToNodes(st, prof, nil)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(res.Blocked) != 1 || res.Blocked[0] != "n1" {
		t.Errorf("removal must be blocked by chain ref: %+v", res)
	}
	if len(res.Removed) != 0 {
		t.Errorf("removed despite block: %+v", res.Removed)
	}
	if ib := st.ProfileInboundOn("n1", "p1"); ib == nil {
		t.Error("inbound removed despite chain ref")
	}
}

func TestApplyProfileToNodes_RemoveWithUsersWarns(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1")
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Attach users to the materialized inbound.
	ni, _ := st.GetNodeInfo("n1")
	for i := range ni.Inbounds {
		if ni.Inbounds[i].ProfileID == "p1" {
			ni.Inbounds[i].ForUsers = []string{"u1", "u2"}
		}
	}
	_ = st.SaveNodeInfo(ni)

	res, err := ApplyProfileToNodes(st, prof, nil)
	if err != nil {
		t.Fatalf("remove: %v", err)
	}
	if len(res.Removed) != 1 {
		t.Errorf("removed: %+v", res)
	}
	if res.UsersLostOn["n1"] != 2 {
		t.Errorf("UsersLostOn: %+v", res.UsersLostOn)
	}
}

func TestApplyProfileToNodes_PortConflict(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1")
	ni, _ := st.GetNodeInfo("n1")
	ni.Inbounds = append(ni.Inbounds, model.NodeInbound{Protocol: "vless-reality", Port: 443, Tag: "other", Source: "standalone"})
	_ = st.SaveNodeInfo(ni)

	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 443}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err == nil {
		t.Error("expected port conflict error")
	}
	if ib := st.ProfileInboundOn("n1", "p1"); ib != nil {
		t.Error("inbound created despite conflict")
	}
}

func TestApplyProfileToNodes_VLESSCreds(t *testing.T) {
	st := newProfileTestStore(t)
	seedNodes(t, st, "n1")
	prof := &model.InboundProfile{ID: "p1", Name: "VLESS", Protocol: "vless-reality", Port: 443}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	ib := st.ProfileInboundOn("n1", "p1")
	if ib.UUID == "" || ib.ServerPrivKey == "" || ib.ServerPubKey == "" || ib.ShortID == "" {
		t.Errorf("VLESS Reality creds incomplete: %+v", ib)
	}
}
