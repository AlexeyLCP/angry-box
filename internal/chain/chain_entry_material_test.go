package chain

import (
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestEnsureChainEntryMaterialization_SubnetAlignsToChainSubnet verifies the
// v0.8 subnet fix: a profile materialized as standalone (10.8.1.1/24) that
// becomes a chain entry moves to the chain-entry subnet 10.8.0.1/24 when free
// — so chain users allocated in 10.8.0.0/24 share the interface's /24.
func TestEnsureChainEntryMaterialization_SubnetAlignsToChainSubnet(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "store.json"))
	if err := st.SaveHost(&model.Host{ID: "n1", Addr: "n1:22"}); err != nil {
		t.Fatalf("SaveHost: %v", err)
	}
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	if err := st.SaveInboundProfile(prof); err != nil {
		t.Fatalf("SaveInboundProfile: %v", err)
	}
	// Standalone materialization first (10.8.1+ subnet, creds generated).
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err != nil {
		t.Fatalf("ApplyProfileToNodes: %v", err)
	}
	ib := st.ProfileInboundOn("n1", "p1")
	if ib.AWGServerAddress == "10.8.0.1/24" {
		t.Fatalf("standalone should not start on the chain subnet: %q", ib.AWGServerAddress)
	}
	privBefore := ib.ServerPrivKey

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
	if err := EnsureChainEntryMaterialization(st, c, GetDefaultPreset()); err != nil {
		t.Fatalf("EnsureChainEntryMaterialization: %v", err)
	}
	ib2 := st.ProfileInboundOn("n1", "p1")
	if ib2.AWGServerAddress != "10.8.0.1/24" {
		t.Errorf("subnet not aligned to chain-entry default: %q", ib2.AWGServerAddress)
	}
	if ib2.ServerPrivKey != privBefore {
		t.Error("creds rotated during alignment (must be preserved)")
	}
}

// TestEnsureChainEntryMaterialization_SubnetKeptWhenClaimed verifies alignment
// does NOT steal 10.8.0.1/24 when another AWG inbound on the node claims it.
func TestEnsureChainEntryMaterialization_SubnetKeptWhenClaimed(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "store.json"))
	// Legacy standalone AWG inbound with the DEFAULT subnet (empty = 10.8.0.1/24).
	if err := st.SaveNodeInfo(&model.NodeInfo{
		Host: model.Host{ID: "n1", Addr: "n1:22"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51850, Source: "standalone", Tag: "legacy", ServerPrivKey: "k"},
		},
	}); err != nil {
		t.Fatalf("SaveNodeInfo: %v", err)
	}
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	_ = st.SaveInboundProfile(prof)
	if _, err := ApplyProfileToNodes(st, prof, []string{"n1"}); err != nil {
		t.Fatalf("ApplyProfileToNodes: %v", err)
	}
	allocated := st.ProfileInboundOn("n1", "p1").AWGServerAddress

	c := &model.Chain{
		Name: "c1",
		Levels: []model.ChainLevel{
			{ID: "l0", Nodes: []model.ChainNode{{ID: "n1", Addr: "n1:22", InboundRef: "p1"}}},
			{ID: "l1", Nodes: []model.ChainNode{{ID: "n2", Addr: "n2:22"}}},
		},
	}
	if err := EnsureChainEntryMaterialization(st, c, GetDefaultPreset()); err != nil {
		t.Fatalf("EnsureChainEntryMaterialization: %v", err)
	}
	ib := st.ProfileInboundOn("n1", "p1")
	if ib.AWGServerAddress != allocated {
		t.Errorf("subnet stolen while claimed: %q -> %q", allocated, ib.AWGServerAddress)
	}
}

// TestEnsureChainEntryMaterialization_PortResync covers the stuck-port fix
// (chain_entry_material.go): a chain-entry inbound whose Port drifted to a
// bogus value (1, simulating a tester node where `awg show` reported
// `listening port: 1`) must be re-synced to chainEntryPort on the next apply.
// Before the fix the update-branch never touched ib.Port (only creds/subnet/
// material), so a bad port stuck forever — "история висит даже после нажатия
// применить" — and every client dialed a port nothing listened on.
func TestEnsureChainEntryMaterialization_PortResync(t *testing.T) {
	st := NewStore(filepath.Join(t.TempDir(), "store.json"))
	if err := st.SaveHost(&model.Host{ID: "n1", Addr: "n1:22"}); err != nil {
		t.Fatalf("SaveHost: %v", err)
	}
	prof := &model.InboundProfile{ID: "p1", Name: "AWG", Protocol: "awg", Port: 51840}
	if err := st.SaveInboundProfile(prof); err != nil {
		t.Fatalf("SaveInboundProfile: %v", err)
	}
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
	// Seed a materialized chain-entry inbound with a STUCK bogus port (1),
	// the exact `listening port: 1` the tester observed on awg0.
	if err := st.SaveNodeInfo(&model.NodeInfo{
		Host:     model.Host{ID: "n1", Addr: "n1:22"},
		Inbounds: []model.NodeInbound{{Protocol: "awg", Port: 1, Source: "chain:c1", Tag: "p1", ProfileID: "p1", ServerPrivKey: "k", AWGServerAddress: "10.8.0.1/24"}},
	}); err != nil {
		t.Fatalf("SaveNodeInfo: %v", err)
	}
	if err := EnsureChainEntryMaterialization(st, c, GetDefaultPreset()); err != nil {
		t.Fatalf("EnsureChainEntryMaterialization: %v", err)
	}
	got := st.ProfileInboundOn("n1", "p1").Port
	if want := chainEntryPort(c, "n1"); got != want {
		t.Errorf("stuck port not resynced: got %d, want %d (chainEntryPort)", got, want)
	}
	if got == 1 {
		t.Errorf("port still 1 after re-apply — client would dial a dead port")
	}
}

func TestEnsureUserAWGAddressPrefix(t *testing.T) {
	u := &model.User{ID: "u1", Protocols: []string{"awg"}}
	EnsureUserAWGAddressPrefix(u, nil, "10.8.1")
	if u.AWGAddress != "10.8.1.2/32" {
		t.Errorf("address = %q, want 10.8.1.2/32", u.AWGAddress)
	}
	// Default prefix preserved via the legacy entry point.
	u2 := &model.User{ID: "u2", Protocols: []string{"awg"}}
	EnsureUserAWGAddress(u2, nil)
	if u2.AWGAddress != "10.8.0.2/32" {
		t.Errorf("legacy default = %q, want 10.8.0.2/32", u2.AWGAddress)
	}
}
