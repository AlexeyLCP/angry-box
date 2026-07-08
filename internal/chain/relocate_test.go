package chain

// relocate_test.go — tests for node relocation (RelocateNode). Uses a fake
// applier that records ApplyChain calls + returns a canned success report,
// so the relocation data-flow (Addr update in 3 places + per-chain re-apply +
// transit-key reuse) is verified without dialing SSH.

import (
	"context"
	"sync"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// fakeRelocateApplier records every ApplyChain call + the chain it was called
// with, and returns a canned all-success report. callCount / calledWith let a
// test assert that RelocateNode re-applied exactly the affected chains.
type fakeRelocateApplier struct {
	mu          sync.Mutex
	calls       []string // chain names, in call order
	seenChains  map[string]*model.Chain
	failChain   string // if set, ApplyChain returns an error for this chain
	errForCall  error
}

func newFakeRelocateApplier() *fakeRelocateApplier {
	return &fakeRelocateApplier{seenChains: map[string]*model.Chain{}}
}

func (f *fakeRelocateApplier) ApplyChain(ctx context.Context, store *Store, c *model.Chain, awgClientPubKey string) (*ApplyReport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c.Name)
	// Snapshot the chain as seen by the applier (post-ResolveNodes) so the test
	// can assert the relocated node's Addr propagated + transit keys survived.
	snap := *c
	snap.Nodes = append([]model.ChainNode(nil), c.Nodes...)
	f.seenChains[c.Name] = &snap
	if f.failChain != "" && c.Name == f.failChain {
		return &ApplyReport{ChainName: c.Name, Nodes: []NodeResult{{ID: "x", Success: false, Error: "boom"}}}, f.errForCall
	}
	return &ApplyReport{ChainName: c.Name, Nodes: []NodeResult{{ID: "ok", Success: true}}}, nil
}

// TestRelocateNode_UpdatesAddrInThreePlaces verifies the Addr moves in Host,
// NodeInfo.Host, and the ChainNode snapshot — and that RelocateNode re-applies
// every affected chain.
func TestRelocateNode_UpdatesAddrInThreePlaces(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	s.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"}, Country: "RU"})
	c := &model.Chain{Name: "c1", Nodes: []model.ChainNode{
		{ID: "n1", TransitPrivKey: "REALITY-PRIV", TransitAWGServerPriv: "awg-srv-priv", Addr: "1.1.1.1:22"},
	}}
	s.SaveChain(c)

	fa := newFakeRelocateApplier()
	report, err := RelocateNode(context.Background(), s, fa, "n1", "9.9.9.9:22", "", "", "")
	if err != nil {
		t.Fatalf("RelocateNode: %v", err)
	}
	if report.OldAddr != "1.1.1.1:22" || report.NewAddr != "9.9.9.9:22" {
		t.Errorf("report addrs: old=%q new=%q", report.OldAddr, report.NewAddr)
	}

	// Host updated.
	h, _ := s.GetHost("n1")
	if h.Addr != "9.9.9.9:22" {
		t.Errorf("Host.Addr = %q, want 9.9.9.9:22", h.Addr)
	}
	// NodeInfo.Host updated.
	info, _ := s.GetNodeInfo("n1")
	if info.Addr != "9.9.9.9:22" {
		t.Errorf("NodeInfo.Addr = %q, want 9.9.9.9:22", info.Addr)
	}
	if info.Country != "RU" {
		t.Errorf("NodeInfo.Country changed: %q (relocate must preserve non-Host fields)", info.Country)
	}
	// ChainNode snapshot updated.
	got, _ := s.GetChain("c1")
	if got.Nodes[0].Addr != "9.9.9.9:22" {
		t.Errorf("ChainNode.Addr = %q, want 9.9.9.9:22", got.Nodes[0].Addr)
	}

	// ApplyChain was called once for the affected chain.
	if len(fa.calls) != 1 || fa.calls[0] != "c1" {
		t.Errorf("ApplyChain calls = %v, want [c1]", fa.calls)
	}
	if len(report.Chains) != 1 || !report.Chains[0].Success {
		t.Errorf("report.Chains = %+v, want 1 success", report.Chains)
	}
}

// TestRelocateNode_PropagatesNewAddrToApplier verifies the chain the applier
// receives carries the NEW addr on the relocated node (so the re-deploy dials
// the new VPS), while the transit material is preserved (reused, not
// regenerated).
func TestRelocateNode_PropagatesNewAddrToApplier(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	seedHost(t, s, "n2", "2.2.2.2:22")
	c := &model.Chain{Name: "c1", Nodes: []model.ChainNode{
		{ID: "n1", TransitPrivKey: "REALITY-PRIV", TransitAWGServerPriv: "awg-srv-priv"},
		{ID: "n2"},
	}}
	s.SaveChain(c)

	fa := newFakeRelocateApplier()
	if _, err := RelocateNode(context.Background(), s, fa, "n1", "9.9.9.9:22", "", "", ""); err != nil {
		t.Fatalf("RelocateNode: %v", err)
	}
	snap := fa.seenChains["c1"]
	if snap == nil {
		t.Fatal("applier did not receive chain c1")
	}
	var n1 *model.ChainNode
	for i := range snap.Nodes {
		if snap.Nodes[i].ID == "n1" {
			n1 = &snap.Nodes[i]
		}
	}
	if n1 == nil {
		t.Fatal("n1 missing from applier snapshot")
	}
	if n1.Addr != "9.9.9.9:22" {
		t.Errorf("applier saw n1.Addr = %q, want 9.9.9.9:22 (new IP must propagate to re-deploy)", n1.Addr)
	}
	// Transit material preserved (reused on the new VPS, not regenerated).
	if n1.TransitPrivKey != "REALITY-PRIV" {
		t.Errorf("n1.TransitPrivKey = %q, want REALITY-PRIV (relocate must reuse keys)", n1.TransitPrivKey)
	}
	if n1.TransitAWGServerPriv != "awg-srv-priv" {
		t.Errorf("n1.TransitAWGServerPriv = %q, want awg-srv-priv (relocate must reuse AWG keys)", n1.TransitAWGServerPriv)
	}
}

// TestRelocateNode_UnknownNode verifies a missing node ID returns an error
// (not a nil panic) and triggers no ApplyChain calls.
func TestRelocateNode_UnknownNode(t *testing.T) {
	s := tempStore(t)
	fa := newFakeRelocateApplier()
	_, err := RelocateNode(context.Background(), s, fa, "ghost", "9.9.9.9:22", "", "", "")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
	if len(fa.calls) != 0 {
		t.Errorf("ApplyChain called for unknown node: %v", fa.calls)
	}
}

// TestRelocateNode_PreservesTransitKeysAcrossReapply verifies the persisted
// transit material on the relocated node is unchanged after the relocate +
// re-apply (the core relocation invariant — keys are reused, not rotated).
func TestRelocateNode_PreservesTransitKeysAcrossReapply(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	seeded := model.ChainNode{
		ID:                   "n1",
		TransitPrivKey:       "REALITY-PRIV",
		TransitShortID:       "deadbeef",
		TransitUUID:          "uuid-1",
		TransitAWGServerPriv: "awg-srv-priv",
		TransitAWGServerPub:  "awg-srv-pub",
		ExitAWGServerPriv:    "exit-srv-priv",
		Role:                 model.NodeRoleExit,
		ExitTargets:          []string{"n1"},
	}
	s.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{seeded}})

	fa := newFakeRelocateApplier()
	if _, err := RelocateNode(context.Background(), s, fa, "n1", "9.9.9.9:22", "", "", ""); err != nil {
		t.Fatalf("RelocateNode: %v", err)
	}
	got, _ := s.GetChain("c1")
	n := got.Nodes[0]
	if n.TransitPrivKey != "REALITY-PRIV" || n.TransitAWGServerPriv != "awg-srv-priv" || n.ExitAWGServerPriv != "exit-srv-priv" {
		t.Errorf("transit material changed across relocate (must be reused): %+v", n)
	}
	if n.Role != model.NodeRoleExit || len(n.ExitTargets) != 1 {
		t.Errorf("Role/ExitTargets changed across relocate: %+v", n)
	}
}

// TestRelocateNode_OneChainFailureDoesNotAbortOthers verifies a failing chain
// re-apply is recorded as failed but does not stop the remaining chains from
// re-deploying.
func TestRelocateNode_OneChainFailureDoesNotAbortOthers(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22")
	s.SaveChain(&model.Chain{Name: "good", Nodes: []model.ChainNode{{ID: "n1"}}})
	s.SaveChain(&model.Chain{Name: "bad", Nodes: []model.ChainNode{{ID: "n1"}}})

	fa := newFakeRelocateApplier()
	fa.failChain = "bad"
	fa.errForCall = errFakeApply
	report, err := RelocateNode(context.Background(), s, fa, "n1", "9.9.9.9:22", "", "", "")
	if err != nil {
		t.Fatalf("RelocateNode returned top-level error: %v (should carry per-chain failures)", err)
	}
	if len(report.Chains) != 2 {
		t.Fatalf("want 2 chain results, got %d", len(report.Chains))
	}
	byName := map[string]RelocateChainResult{}
	for _, r := range report.Chains {
		byName[r.Name] = r
	}
	if !byName["good"].Success {
		t.Errorf("good chain should have succeeded")
	}
	if byName["bad"].Success || byName["bad"].Error == "" {
		t.Errorf("bad chain should be failed with an error: %+v", byName["bad"])
	}
}

// TestRelocateNode_UpdatesUserAndKey verifies optional newUser/newKeyPath are
// applied (and empty values preserve the current ones).
func TestRelocateNode_UpdatesUserAndKey(t *testing.T) {
	s := tempStore(t)
	seedHost(t, s, "n1", "1.1.1.1:22") // User=root, KeyPath=/key
	s.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1"}}})
	fa := newFakeRelocateApplier()
	if _, err := RelocateNode(context.Background(), s, fa, "n1", "9.9.9.9:22", "lcp", "ssh-key-1", ""); err != nil {
		t.Fatalf("RelocateNode: %v", err)
	}
	h, _ := s.GetHost("n1")
	if h.User != "lcp" {
		t.Errorf("User = %q, want lcp", h.User)
	}
	if h.KeyPath != "ssh-key-1" {
		t.Errorf("KeyPath = %q, want ssh-key-1", h.KeyPath)
	}
}

// errFakeApply is a sentinel returned by the fake applier for a chosen chain.
var errFakeApply = &fakeApplyErr{}

type fakeApplyErr struct{}

func (e *fakeApplyErr) Error() string { return "fake apply failure" }