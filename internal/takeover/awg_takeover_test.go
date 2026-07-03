package takeover

// awg_takeover_test.go — verifies the dedicated AWG takeover renderer preserves
// the imported server keypair + listen port + amnezia (JC/S1-S4/H1-H4/I1-I5) +
// the full peer list, and that the produced config passes a real sing-box check.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// findSingBoxBinaryE2E locates the repo's deps sing-box binary (sing-box-extended
// with with_wireguard) by walking up from the test cwd. Returns "" if absent.
func findSingBoxBinaryE2E() string {
	exe := "sing-box"
	if runtime.GOOS == "windows" {
		exe = "sing-box.exe"
	}
	dir, err := os.Getwd()
	if err == nil {
		for d := dir; d != filepath.Dir(d); d = filepath.Dir(d) {
			cand := filepath.Join(d, "deps", exe)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
	}
	if p, err := exec.LookPath("sing-box"); err == nil {
		return p
	}
	return ""
}

// TestRenderAWGTakeoverConfig_Fields verifies the rendered endpoint carries the
// imported server PrivateKey/ListenPort, all peers, and amnezia 1:1.
func TestRenderAWGTakeoverConfig_Fields(t *testing.T) {
	server := &chain.AwgServerConfig{
		PrivateKey: "YNXtAzepDqRv9H52osJVDQnznT5AM11eCK3ESpwSt04=",
		ListenPort: 51820,
		Address:    "10.8.0.1/24",
		JC:         120, JMIN: 50, JMAX: 1000,
		S1: 115, S2: 45, S3: 22, S4: 12,
		H1: "12345-98765", H2: "600000000-700000000",
		H3: "1100000000-1200000000", H4: "1700000000-1900000000",
		I1: "<b 0xc30000000108>", I2: "<b 0x6a>", I3: "<b 0x5e>",
		I4: "<b 0x65>", I5: "<b 0x63>",
	}
	peers := []chain.AwgPeerEntry{
		{Name: "alice", PublicKey: "pub-alice", AllowedIPs: "10.8.0.2/32"},
		{Name: "bob", PublicKey: "pub-bob", AllowedIPs: "10.8.0.3/32,::/128"},
		{Name: "carol", PublicKey: "", AllowedIPs: "10.8.0.4/32"}, // no pub -> skipped
	}
	cfgJSON, err := renderAWGTakeoverConfig(server, peers)
	if err != nil {
		t.Fatalf("renderAWGTakeoverConfig: %v", err)
	}
	// Parse the config + endpoint.
	var top struct {
		Endpoints []json.RawMessage `json:"endpoints"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &top); err != nil {
		t.Fatalf("unmarshal config: %v\n%s", err, cfgJSON)
	}
	if len(top.Endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(top.Endpoints))
	}
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(top.Endpoints[0], &ep); err != nil {
		t.Fatalf("unmarshal endpoint: %v", err)
	}
	if ep.PrivateKey != server.PrivateKey {
		t.Errorf("private_key=%s, want server priv", ep.PrivateKey)
	}
	if ep.ListenPort != 51820 {
		t.Errorf("listen_port=%d, want 51820", ep.ListenPort)
	}
	if ep.System {
		t.Error("system=true, want false (userspace)")
	}
	// 2 valid peers (carol skipped — no pub).
	if len(ep.Peers) != 2 {
		t.Fatalf("want 2 peers, got %d: %+v", len(ep.Peers), ep.Peers)
	}
	if ep.Peers[0].PublicKey != "pub-alice" || len(ep.Peers[0].AllowedIPs) != 1 || ep.Peers[0].AllowedIPs[0] != "10.8.0.2/32" {
		t.Errorf("peer 0 = %+v, want pub-alice [10.8.0.2/32]", ep.Peers[0])
	}
	if len(ep.Peers[1].AllowedIPs) != 2 {
		t.Errorf("peer 1 allowed_ips=%v, want 2 entries (comma split)", ep.Peers[1].AllowedIPs)
	}
	// Amnezia 1:1.
	if ep.Amnezia == nil {
		t.Fatal("amnezia nil despite server JC=120")
	}
	if ep.Amnezia.JC != 120 || ep.Amnezia.S3 != 22 || ep.Amnezia.S4 != 12 {
		t.Errorf("amnezia JC=%d S3=%d S4=%d, want 120/22/12", ep.Amnezia.JC, ep.Amnezia.S3, ep.Amnezia.S4)
	}
	if ep.Amnezia.H1 != "12345-98765" || ep.Amnezia.I1 != "<b 0xc30000000108>" {
		t.Errorf("amnezia H1=%q I1=%q, want 12345-98765 / <b 0xc30000000108>", ep.Amnezia.H1, ep.Amnezia.I1)
	}
}

// TestRenderAWGTakeoverConfig_NoAmnezia — server with JC=0 (plain WireGuard)
// renders an endpoint WITHOUT an amnezia block.
func TestRenderAWGTakeoverConfig_NoAmnezia(t *testing.T) {
	server := &chain.AwgServerConfig{
		PrivateKey: "YNXtAzepDqRv9H52osJVDQnznT5AM11eCK3ESpwSt04=",
		ListenPort: 51820,
		// JC=0 -> plain WireGuard
	}
	cfgJSON, err := renderAWGTakeoverConfig(server, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(cfgJSON, `"amnezia"`) {
		t.Errorf("config should have no amnezia block for plain WireGuard:\n%s", cfgJSON)
	}
}

// TestRenderAWGTakeoverConfig_SingBoxCheck runs a real `sing-box check` against
// the rendered config (proves the amnezia 1:1 copy + peer shape is valid).
func TestRenderAWGTakeoverConfig_SingBoxCheck(t *testing.T) {
	bin := findSingBoxBinaryE2E()
	if bin == "" {
		t.Skip("no sing-box binary found (deps/sing-box(.exe) or PATH)")
	}
	server := &chain.AwgServerConfig{
		PrivateKey: "YNXtAzepDqRv9H52osJVDQnznT5AM11eCK3ESpwSt04=",
		ListenPort: 51820, Address: "10.8.0.1/24",
		JC: 120, JMIN: 50, JMAX: 1000, S1: 115, S2: 45, S3: 22, S4: 12,
		H1: "12345-98765", H2: "600000000-700000000",
		H3: "1100000000-1200000000", H4: "1700000000-1900000000",
		I1: "<b 0xc30000000108>", I2: "<b 0x6a>", I3: "<b 0x5e>", I4: "<b 0x65>", I5: "<b 0x63>",
	}
	peers := []chain.AwgPeerEntry{
		{PublicKey: "Z1XXLsKYkYxuiYjJIkRvtIKFepCYHTgON+GwPq7SOV4=", AllowedIPs: "10.8.0.2/32"},
	}
	cfgJSON, err := renderAWGTakeoverConfig(server, peers)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	tmp, _ := os.CreateTemp("", "awg_takeover_*.json")
	defer os.Remove(tmp.Name())
	tmp.Write([]byte(cfgJSON))
	tmp.Close()
	cmd := exec.Command(bin, "check", "-c", tmp.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check failed: %v\nOutput: %s\nConfig:\n%s", err, out, cfgJSON)
	}
}