package chain

// backup_test.go — tests for the node + full-panel backup/restore helpers
// (ExportNode/ImportNode/ExportStore/ImportStore). Roundtrip + dedup + force
// semantics + format auto-detection.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestExportNode_UnknownID verifies a missing node ID returns ErrHostNotFound
// (not a nil-panic or empty backup).
func TestExportNode_UnknownID(t *testing.T) {
	s := tempStore(t)
	_, err := s.ExportNode("ghost")
	if err == nil {
		t.Fatal("expected error for unknown node ID")
	}
}

// TestExportNode_Roundtrip verifies an exported node restores cleanly onto a
// fresh store: the Host + NodeInfo + each chain membership (with transit
// material) come back identically.
func TestExportNode_Roundtrip(t *testing.T) {
	src := tempStore(t)
	seedHost(t, src, "n1", "1.1.1.1:22")
	src.SaveNodeInfo(&model.NodeInfo{Host: model.Host{ID: "n1", Addr: "1.1.1.1:22", User: "root"}, Country: "RU"})
	seeded := model.ChainNode{
		ID:                   "n1",
		Port:                 443,
		Role:                 model.NodeRoleExit,
		ExitTargets:          []string{"n1"},
		TransitAWGServerPriv: "awg-srv-priv",
		ExitAWGServerPriv:    "exit-srv-priv",
	}
	if err := src.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{seeded}}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}

	b, err := src.ExportNode("n1")
	if err != nil {
		t.Fatalf("ExportNode: %v", err)
	}
	if b.Format != BackupFormatNode {
		t.Errorf("Format = %q, want %q", b.Format, BackupFormatNode)
	}
	if b.Node.Addr != "1.1.1.1:22" {
		t.Errorf("Node.Addr = %q, want 1.1.1.1:22", b.Node.Addr)
	}
	if b.NodeInfo == nil || b.NodeInfo.Country != "RU" {
		t.Errorf("NodeInfo not carried: %+v", b.NodeInfo)
	}
	if len(b.Chains) != 1 || b.Chains[0].ChainName != "c1" {
		t.Fatalf("Chains = %+v, want [c1]", b.Chains)
	}
	if b.Chains[0].Node.TransitAWGServerPriv != "awg-srv-priv" {
		t.Errorf("chain membership dropped TransitAWGServerPriv: %+v", b.Chains[0].Node)
	}

	// Restore onto a fresh store. A node backup does not invent a half-chain,
	// so pre-create the chain stub on the destination (the operator would do
	// this, or import the full store). ImportNode then merges the node's
	// material into the matching chain.
	dst := tempStore(t)
	dst.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1"}}})
	if err := dst.ImportNode(b, false); err != nil {
		t.Fatalf("ImportNode: %v", err)
	}
	h, err := dst.GetHost("n1")
	if err != nil {
		t.Fatalf("GetHost after import: %v", err)
	}
	if h.Addr != "1.1.1.1:22" {
		t.Errorf("restored Host.Addr = %q, want 1.1.1.1:22", h.Addr)
	}
	info, _ := dst.GetNodeInfo("n1")
	if info == nil || info.Country != "RU" {
		t.Errorf("restored NodeInfo.Country missing: %+v", info)
	}
	c, err := dst.GetChain("c1")
	if err != nil {
		t.Fatalf("GetChain after import: %v", err)
	}
	if len(c.Nodes) != 1 || c.Nodes[0].ID != "n1" || c.Nodes[0].TransitAWGServerPriv != "awg-srv-priv" {
		t.Errorf("restored chain node missing material: %+v", c.Nodes)
	}
	if c.Nodes[0].Role != model.NodeRoleExit {
		t.Errorf("restored Role = %q, want exit", c.Nodes[0].Role)
	}
}

// TestImportNode_RefusesRerouteWithoutForce verifies importing a node whose ID
// already exists at a DIFFERENT addr is refused without force (so an
// accidental import does not silently reroute a live node).
func TestImportNode_RefusesRerouteWithoutForce(t *testing.T) {
	dst := tempStore(t)
	seedHost(t, dst, "n1", "1.1.1.1:22")
	b := &NodeBackup{
		backupEnvelope: backupEnvelope{Format: BackupFormatNode, Version: 1},
		Node:           model.ChainNode{ID: "n1", Addr: "9.9.9.9:22", User: "root"},
	}
	err := dst.ImportNode(b, false)
	if err == nil {
		t.Fatal("expected error: import would reroute live node without force")
	}
	h, _ := dst.GetHost("n1")
	if h.Addr != "1.1.1.1:22" {
		t.Errorf("live node was rerouted without force: Addr = %q", h.Addr)
	}
	// With force, the reroute succeeds.
	if err := dst.ImportNode(b, true); err != nil {
		t.Fatalf("ImportNode with force: %v", err)
	}
	h, _ = dst.GetHost("n1")
	if h.Addr != "9.9.9.9:22" {
		t.Errorf("force import did not reroute: Addr = %q", h.Addr)
	}
}

// TestImportNode_SkipsMissingChains verifies a node backup referencing chains
// that do not exist on the target install restores the Host/NodeInfo but
// reports the missing chains (does not invent half-chains).
func TestImportNode_SkipsMissingChains(t *testing.T) {
	dst := tempStore(t)
	b := &NodeBackup{
		backupEnvelope: backupEnvelope{Format: BackupFormatNode, Version: 1},
		Node:           model.ChainNode{ID: "n1", Addr: "1.1.1.1:22", User: "root"},
		Chains: []NodeChainMembership{
			{ChainName: "nope", Node: model.ChainNode{ID: "n1", TransitPrivKey: "x"}},
		},
	}
	err := dst.ImportNode(b, false)
	if err == nil {
		t.Fatal("expected error reporting skipped missing chains")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the skipped chain: %v", err)
	}
	// Host was still restored.
	if h, err := dst.GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("Host not restored despite skipped chains: %+v %v", h, err)
	}
}

// TestExportStore_ImportStore_Roundtrip verifies a full-panel backup restores
// onto a fresh store with all hosts + chains intact.
func TestExportStore_ImportStore_Roundtrip(t *testing.T) {
	src := tempStore(t)
	seedHost(t, src, "n1", "1.1.1.1:22")
	seedHost(t, src, "n2", "2.2.2.2:22")
	src.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1"}, {ID: "n2"}}})
	src.SaveSettings(&model.PanelSettings{Language: "ru", PanelCountry: "RU"})

	data, err := src.ExportStore()
	if err != nil {
		t.Fatalf("ExportStore: %v", err)
	}
	if format, err := detectBackupFormat(data); err != nil || format != BackupFormatStore {
		t.Fatalf("detectBackupFormat = %q %v, want %q", format, err, BackupFormatStore)
	}
	// Plaintext JSON (portable): no encryption magic header.
	if isEncrypted(data) {
		t.Errorf("ExportStore returned encrypted bytes; backup must be plaintext for portability")
	}

	dst := tempStore(t)
	if err := dst.ImportStore(data, false); err != nil {
		t.Fatalf("ImportStore (fresh dst, no force): %v", err)
	}
	if h, err := dst.GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("restored n1: %+v %v", h, err)
	}
	c, err := dst.GetChain("c1")
	if err != nil || len(c.Nodes) != 2 {
		t.Errorf("restored chain c1: %+v %v", c, err)
	}
	st, _ := dst.GetSettings()
	if st == nil || st.Language != "ru" {
		t.Errorf("restored settings: %+v", st)
	}
}

// TestImportStore_RefusesNonEmptyWithoutForce verifies importing a full store
// onto a non-empty target is refused without force (wipe protection).
func TestImportStore_RefusesNonEmptyWithoutForce(t *testing.T) {
	src := tempStore(t)
	seedHost(t, src, "n1", "1.1.1.1:22")
	data, _ := src.ExportStore()

	dst := tempStore(t)
	seedHost(t, dst, "existing", "9.9.9.9:22") // non-empty target
	err := dst.ImportStore(data, false)
	if err == nil {
		t.Fatal("expected error: import onto non-empty store without force")
	}
	// Existing host preserved.
	if h, err := dst.GetHost("existing"); err != nil || h == nil {
		t.Errorf("non-empty target was wiped without force: %+v %v", h, err)
	}
	// With force, it overwrites.
	if err := dst.ImportStore(data, true); err != nil {
		t.Fatalf("ImportStore with force: %v", err)
	}
	if _, err := dst.GetHost("existing"); err == nil {
		t.Errorf("force import did not overwrite (existing host still present)")
	}
	if h, err := dst.GetHost("n1"); err != nil || h.Addr != "1.1.1.1:22" {
		t.Errorf("force import did not restore n1: %+v %v", h, err)
	}
}

// TestDetectBackupFormat_Invalid verifies non-backup JSON is rejected.
func TestDetectBackupFormat_Invalid(t *testing.T) {
	if _, err := detectBackupFormat([]byte(`{"foo":"bar"}`)); err == nil {
		t.Fatal("expected error for non-backup JSON")
	}
	if _, err := detectBackupFormat([]byte(`not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// keep encoding/json imported (used by detectBackupFormat tests via raw bytes
// + the storeBackup anonymous struct is exercised through the public API).
var _ = json.Marshal