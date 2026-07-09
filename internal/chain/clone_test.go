package chain

// clone_test.go — unit tests for node cloning (P1b). Uses the fakeRelocateApplier
// (which satisfies chainApplier) so no real SSH/deploy happens. Asserts: fresh
// identity, source untouched, new ChainNode appended, audit written, report
// correct, validation errors.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// seedSourceNode builds a store with one source node + one chain containing it,
// with distinct identity material so the clone's fresh identity is checkable.
func seedSourceNode(t *testing.T, s *Store) {
	t.Helper()
	seedHost(t, s, "src", "1.1.1.1:22")
	if err := s.SaveNodeInfo(&model.NodeInfo{
		Host:      model.Host{ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
		Country:   "RU",
		Bandwidth: "1Gbps",
		Inbounds: []model.NodeInbound{{
			Protocol: "vless-reality", Port: 443, ForUsers: []string{"u1"},
			UUID: "SRC-UUID", ServerPrivKey: "SRC-PRIV", ServerPubKey: "SRC-PUB",
			ShortID: "src-sid", Tag: "src-tag",
		}},
	}); err != nil {
		t.Fatalf("SaveNodeInfo: %v", err)
	}
	c := &model.Chain{Name: "c1", Transport: "reality", Nodes: []model.ChainNode{{
		ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k", Port: 8443,
		Role: model.NodeRoleEntry, ExitTargets: []string{"exit1"},
		TransitPrivKey: "SRC-TRANSIT-PRIV", TransitShortID: "src-tsid", TransitUUID: "src-tuuid",
		TransitAWGServerPriv: "SRC-AWG-SRV", TransitAWGAddress: "10.9.0.2/32",
		Inbounds: []model.NodeInbound{{Protocol: "vless-reality", Port: 443, UUID: "SRC-CN-UUID", ServerPrivKey: "SRC-CN-PRIV"}},
	}}}
	if err := s.SaveChain(c); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
}

// TestCloneNode_FreshIdentityAndUntouchedSource — clone has fresh identity
// (distinct UUID/keys/ShortID/Tag on inbound + distinct transit keys/UUID/IP on
// the ChainNode), the source is untouched (same UUID/keys/IP/role/ExitTargets),
// and the clone is appended (not replacing) to the chain.
func TestCloneNode_FreshIdentityAndUntouchedSource(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	fa := newFakeRelocateApplier()

	report, err := CloneNode(context.Background(), s, fa, "src", "clone1", "9.9.9.9:22", "", "", "")
	if err != nil {
		t.Fatalf("CloneNode: %v", err)
	}
	if report.NewID != "clone1" || report.NewAddr != "9.9.9.9:22" || len(report.Chains) != 1 || !report.Chains[0].Success {
		t.Fatalf("report unexpected: %+v", report)
	}

	// Clone NodeInfo exists with fresh inbound identity + copied config.
	ci, err := s.GetNodeInfo("clone1")
	if err != nil {
		t.Fatalf("clone nodeinfo not found: %v", err)
	}
	if ci.Country != "RU" || ci.Bandwidth != "1Gbps" {
		t.Fatalf("clone config not copied: %+v", ci)
	}
	if ci.Source != "cloned" {
		t.Fatalf("clone Source = %q, want cloned", ci.Source)
	}
	if len(ci.Inbounds) != 1 {
		t.Fatalf("clone inbounds = %d, want 1", len(ci.Inbounds))
	}
	cib := ci.Inbounds[0]
	if cib.UUID == "" || cib.UUID == "SRC-UUID" {
		t.Fatalf("clone inbound UUID = %q, want fresh non-empty (not SRC-UUID)", cib.UUID)
	}
	if cib.ServerPrivKey == "" || cib.ServerPrivKey == "SRC-PRIV" {
		t.Fatalf("clone inbound ServerPrivKey not fresh: %q", cib.ServerPrivKey)
	}
	if cib.ShortID == "" || cib.ShortID == "src-sid" {
		t.Fatalf("clone inbound ShortID not fresh: %q", cib.ShortID)
	}
	if cib.Tag == "" || cib.Tag == "src-tag" {
		t.Fatalf("clone inbound Tag not fresh: %q", cib.Tag)
	}
	// ForUsers COPIED (clone serves the same users).
	if len(cib.ForUsers) != 1 || cib.ForUsers[0] != "u1" {
		t.Fatalf("clone ForUsers = %v, want [u1] (copied)", cib.ForUsers)
	}
	if cib.Protocol != "vless-reality" || cib.Port != 443 {
		t.Fatalf("clone inbound config not copied: %+v", cib)
	}

	// Source NodeInfo UNTOUCHED.
	si, _ := s.GetNodeInfo("src")
	if si.Inbounds[0].UUID != "SRC-UUID" || si.Inbounds[0].ServerPrivKey != "SRC-PRIV" {
		t.Fatalf("source inbound identity changed: %+v", si.Inbounds[0])
	}

	// Chain now has TWO nodes (source + appended clone); clone has fresh transit
	// material + copied Role/ExitTargets; source untouched.
	c, _ := s.GetChain("c1")
	if len(c.Nodes) != 2 {
		t.Fatalf("chain nodes = %d, want 2 (source + appended clone)", len(c.Nodes))
	}
	srcCN := c.Nodes[0]
	cloneCN := c.Nodes[1]
	if srcCN.ID != "src" || cloneCN.ID != "clone1" {
		t.Fatalf("chain node order wrong: %v", []string{c.Nodes[0].ID, c.Nodes[1].ID})
	}
	if srcCN.TransitPrivKey != "SRC-TRANSIT-PRIV" || srcCN.TransitUUID != "src-tuuid" {
		t.Fatalf("source transit material changed: %+v", srcCN)
	}
	if cloneCN.TransitPrivKey == "" || cloneCN.TransitPrivKey == "SRC-TRANSIT-PRIV" {
		t.Fatalf("clone transit priv not fresh: %q", cloneCN.TransitPrivKey)
	}
	if cloneCN.TransitUUID == "" || cloneCN.TransitUUID == "src-tuuid" {
		t.Fatalf("clone transit UUID not fresh: %q", cloneCN.TransitUUID)
	}
	if cloneCN.TransitAWGAddress == "" || cloneCN.TransitAWGAddress == "10.9.0.2/32" {
		t.Fatalf("clone transit IP not fresh: %q", cloneCN.TransitAWGAddress)
	}
	if !strings.HasPrefix(cloneCN.TransitAWGAddress, "10.9.0.") {
		t.Fatalf("clone transit IP not in 10.9.0.0/24: %q", cloneCN.TransitAWGAddress)
	}
	// Role + ExitTargets COPIED.
	if cloneCN.Role != model.NodeRoleEntry || len(cloneCN.ExitTargets) != 1 || cloneCN.ExitTargets[0] != "exit1" {
		t.Fatalf("clone ChainNode config not copied: %+v", cloneCN)
	}
}

// TestCloneNode_ValidationErrors — missing args / same ID / existing new ID.
func TestCloneNode_ValidationErrors(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	fa := newFakeRelocateApplier()

	cases := []struct {
		name                          string
		sourceID, newID, newAddr string
		wantSub                       string
	}{
		{"empty source", "", "clone1", "9.9.9.9:22", "sourceID"},
		{"empty newID", "src", "", "9.9.9.9:22", "newID"},
		{"empty addr", "src", "clone1", "", "newAddr"},
		{"same ID", "src", "src", "9.9.9.9:22", "differ from sourceID"},
		{"newID exists", "src", "src", "9.9.9.9:22", "differ from sourceID"}, // "src" exists
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CloneNode(context.Background(), s, fa, tc.sourceID, tc.newID, tc.newAddr, "", "", "")
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

// TestCloneNode_SourceNotFound — cloning a non-existent node → error.
func TestCloneNode_SourceNotFound(t *testing.T) {
	s := tempStore(t)
	fa := newFakeRelocateApplier()
	_, err := CloneNode(context.Background(), s, fa, "ghost", "clone1", "9.9.9.9:22", "", "", "")
	if err == nil {
		t.Fatal("expected error for non-existent source")
	}
}

// TestCloneNode_NewIDCollision — a newID that already exists → error (collision).
func TestCloneNode_NewIDCollision(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	// Add a second node to collide with.
	seedHost(t, s, "other", "2.2.2.2:22")
	fa := newFakeRelocateApplier()
	_, err := CloneNode(context.Background(), s, fa, "src", "other", "9.9.9.9:22", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want 'already exists'", err)
	}
}

// TestCloneNode_NilApplier — nil applier → error.
func TestCloneNode_NilApplier(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	_, err := CloneNode(context.Background(), s, nil, "src", "clone1", "9.9.9.9:22", "", "", "")
	if err == nil || !strings.Contains(err.Error(), "nil applier") {
		t.Fatalf("err = %v, want nil applier", err)
	}
}

// TestCloneNode_AuditWritten — a clone writes a "clone" audit entry.
func TestCloneNode_AuditWritten(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	fa := newFakeRelocateApplier()

	if _, err := CloneNode(context.Background(), s, fa, "src", "clone1", "9.9.9.9:22", "", "", ""); err != nil {
		t.Fatalf("CloneNode: %v", err)
	}
	logs, err := s.ListAuditLogs(0)
	if err != nil {
		t.Fatalf("ListAuditLogs: %v", err)
	}
	found := false
	for _, l := range logs {
		if l.Action == "clone" && l.TargetID == "clone1" {
			found = true
			if !strings.Contains(l.PayloadJSON, "src") {
				t.Fatalf("audit payload missing source: %q", l.PayloadJSON)
			}
		}
	}
	if !found {
		t.Fatal("no 'clone' audit entry written")
	}
}

// TestCloneNode_OneChainFailureNonFatal — if one chain re-apply fails, the
// clone still exists + the report carries the per-chain error (not a hard abort).
func TestCloneNode_OneChainFailureNonFatal(t *testing.T) {
	s := tempStore(t)
	seedSourceNode(t, s)
	// Add a second chain that will fail.
	s.SaveChain(&model.Chain{Name: "c2", Transport: "reality", Nodes: []model.ChainNode{{ID: "src", Addr: "1.1.1.1:22"}}})
	fa := newFakeRelocateApplier()
	fa.failChain = "c2"
	fa.errForCall = errLine2("deploy boom")

	report, err := CloneNode(context.Background(), s, fa, "src", "clone1", "9.9.9.9:22", "", "", "")
	if err != nil {
		t.Fatalf("CloneNode should not hard-fail on one chain error: %v", err)
	}
	if len(report.Chains) != 2 {
		t.Fatalf("report chains = %d, want 2", len(report.Chains))
	}
	succ, fail := 0, 0
	for _, c := range report.Chains {
		if c.Success {
			succ++
		} else {
			fail++
		}
	}
	if succ != 1 || fail != 1 {
		t.Fatalf("report success/fail = %d/%d, want 1/1", succ, fail)
	}
	// Clone still exists in the store despite the failed chain.
	if _, err := s.GetHost("clone1"); err != nil {
		t.Fatalf("clone host missing after one chain failed: %v", err)
	}
}

// errLine2 is a trivial error local to this test file (errLine exists in the
// web package; this is package chain).
type errLine2 string

func (e errLine2) Error() string { return string(e) }

// TestAllocateAWGServerSubnet_Free — empty taken → 10.8.1.1/24; one taken → next.
func TestAllocateAWGServerSubnet_Free(t *testing.T) {
	if got := allocateAWGServerSubnet(nil); got != "10.8.1.1/24" {
		t.Fatalf("empty taken = %q, want 10.8.1.1/24", got)
	}
	if got := allocateAWGServerSubnet([]string{"10.8.1.1/24"}); got != "10.8.2.1/24" {
		t.Fatalf("taken [10.8.1.1/24] = %q, want 10.8.2.1/24", got)
	}
	// 10.8.0.0/24 (legacy chain-entry default) is in taken → still starts at 10.8.1.
	if got := allocateAWGServerSubnet([]string{"10.8.0.1/24"}); got != "10.8.1.1/24" {
		t.Fatalf("legacy taken = %q, want 10.8.1.1/24", got)
	}
}

// TestAllocateAWGServerSubnet_SkipsLegacyDefault — never returns 10.8.0.X (that
// /24 is reserved for the chain AWG entry so a standalone clone never collides).
func TestAllocateAWGServerSubnet_SkipsLegacyDefault(t *testing.T) {
	// Even with 10.8.1..10.8.5 taken, the next is 10.8.6 — never 10.8.0.
	taken := []string{}
	for i := 1; i <= 5; i++ {
		taken = append(taken, fmt.Sprintf("10.8.%d.1/24", i))
	}
	got := allocateAWGServerSubnet(taken)
	if got != "10.8.6.1/24" {
		t.Fatalf("got %q, want 10.8.6.1/24", got)
	}
	if strings.HasPrefix(got, "10.8.0.") {
		t.Fatalf("allocator returned legacy 10.8.0.0/24: %q", got)
	}
}

// TestCloneNode_AWGSubnetFresh — a source with an AWG standalone inbound on
// 10.8.0.1/24: the clone gets a FRESH /24 (10.8.1.1/24), the source is
// untouched, and the two subnets differ (no collision when the clone joins the
// source's chain).
func TestCloneNode_AWGSubnetFresh(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "src", "1.1.1.1:22")
	s.SaveNodeInfo(&model.NodeInfo{
		Host:    model.Host{ID: "src", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
		Country: "RU",
		Inbounds: []model.NodeInbound{{
			Protocol: "awg", Port: 51820, AWGServerAddress: "10.8.0.1/24",
			UUID: "SRC-UUID", ServerPrivKey: "SRC-PRIV", ShortID: "src-sid", Tag: "src-tag",
		}},
	})
	fa := newFakeRelocateApplier()

	if _, err := CloneNode(context.Background(), s, fa, "src", "clone1", "9.9.9.9:22", "", "", ""); err != nil {
		t.Fatalf("CloneNode: %v", err)
	}

	ci, _ := s.GetNodeInfo("clone1")
	cib := ci.Inbounds[0]
	if cib.AWGServerAddress == "" || cib.AWGServerAddress == "10.8.0.1/24" {
		t.Fatalf("clone AWGServerAddress = %q, want a fresh /24 (not the source's)", cib.AWGServerAddress)
	}
	if !strings.HasPrefix(cib.AWGServerAddress, "10.8.") || strings.HasPrefix(cib.AWGServerAddress, "10.8.0.") {
		t.Fatalf("clone AWGServerAddress = %q, want 10.8.X.1/24 with X>=1", cib.AWGServerAddress)
	}

	// Source untouched.
	si, _ := s.GetNodeInfo("src")
	if si.Inbounds[0].AWGServerAddress != "10.8.0.1/24" {
		t.Fatalf("source AWGServerAddress changed: %q", si.Inbounds[0].AWGServerAddress)
	}

	// Two AWG inbounds on the same clone get distinct /24s.
	s2 := tempStore(t)
	seedHost(t, s2, "src2", "1.1.1.1:22")
	s2.SaveNodeInfo(&model.NodeInfo{
		Host: model.Host{ID: "src2", Addr: "1.1.1.1:22", User: "root", KeyPath: "/k"},
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", Port: 51820, AWGServerAddress: "10.8.0.1/24"},
			{Protocol: "awg", Port: 51821, AWGServerAddress: "10.8.5.1/24"},
		},
	})
	fa2 := newFakeRelocateApplier()
	if _, err := CloneNode(context.Background(), s2, fa2, "src2", "clone2", "9.9.9.9:22", "", "", ""); err != nil {
		t.Fatalf("CloneNode 2: %v", err)
	}
	ci2, _ := s2.GetNodeInfo("clone2")
	sub0 := ci2.Inbounds[0].AWGServerAddress
	sub1 := ci2.Inbounds[1].AWGServerAddress
	if sub0 == sub1 {
		t.Fatalf("two AWG inbounds on the clone got the same subnet: %q == %q", sub0, sub1)
	}
}