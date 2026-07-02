package chain

// awg_config_check_test.go — verifies the orchestrator generates a VALID
// sing-box config for an AWG user-entry chain (multi-peer endpoint + per-user
// source_ip_cidr route rules) by running the real `sing-box check` against the
// rendered JSON. This is the "configs are created and AWG works as an inbound"
// smoke test — it does not bring up a tunnel (that needs a kernel AWG module on
// a Linux VPS, which the orchestrator installs at deploy time), but a passing
// check proves the config structure, field names, and route rules are accepted
// by sing-box-extended.
//
// The sing-box binary is resolved from, in order: deps/sing-box.exe (Windows,
// shipped with the repo), deps/sing-box (Linux), or `sing-box` on PATH. The
// test skips when no binary is available.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// findSingBoxBinary returns the path to a sing-box executable, or "" if none is
// available. Prefer the repo's deps/ binary (known to be sing-box-extended with
// with_wireguard) over a PATH lookup. The repo root is found by walking up from
// the test's working directory (the package dir) until a `deps/` dir appears.
func findSingBoxBinary() string {
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

// singBoxCheck runs `sing-box check -c <cfg>` and fails the test on a non-zero
// exit, including the config + stderr in the failure message.
func singBoxCheck(t *testing.T, cfgJSON []byte) {
	t.Helper()
	bin := findSingBoxBinary()
	if bin == "" {
		t.Skip("no sing-box binary found (deps/sing-box(.exe) or PATH) — skipping real check")
	}
	tmp, err := os.CreateTemp("", "awg_check_*.json")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(cfgJSON); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	tmp.Close()
	cmd := exec.Command(bin, "check", "-c", tmp.Name())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check failed: %v\nOutput: %s\nConfig:\n%s", err, out, cfgJSON)
	}
}

// awgServerKey is a real base64 (StdEncoding) X25519 private key so the
// generated endpoint passes sing-box's key validation (a random/garbage string
// is rejected). Generated once via GenerateWireGuardKeypair in a scratch run.
var awgServerPriv = func() string {
	priv, _, err := GenerateWireGuardKeypair()
	if err != nil {
		return ""
	}
	return priv
}()

// TestAWGMergedConfig_SingBoxCheck_EntryOnly renders a single-node AWG chain
// (entry == exit, the simplest case) with two users as multi-peer and runs
// sing-box check. Proves the AWG inbound (wireguard endpoint + TUN + multi-peer)
// is a valid config.
func TestAWGMergedConfig_SingBoxCheck_EntryOnly(t *testing.T) {
	if awgServerPriv == "" {
		t.Fatal("could not generate AWG server keypair")
	}
	c := &model.Chain{
		Name:              "awg-check",
		UserProtocol:      model.UserProtocolAWG,
		Transport:         model.TransportXHTTP,
		AWGEntryServerPriv: awgServerPriv,
		Nodes: []model.ChainNode{
			{ID: "n1", Addr: "n1.example.test:22", Role: model.NodeRoleEntry},
		},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "n1"}}
	users := []model.User{
		{Name: "alice", Active: true, AWGPublicKey: genAWGPub(t), AWGAddress: "10.8.0.2/32"},
		{Name: "bob", Active: true, AWGPublicKey: genAWGPub(t), AWGAddress: "10.8.0.3/32"},
	}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, map[string][]model.User{"awg-check": users})
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Sanity: the config actually contains a multi-peer wireguard endpoint.
	if !strings.Contains(string(cfgJSON), `"wireguard"`) {
		t.Fatalf("config has no wireguard endpoint:\n%s", cfgJSON)
	}
	if got := strings.Count(string(cfgJSON), `"public_key"`); got < 2 {
		t.Errorf("want >=2 wireguard peers, got %d in:\n%s", got, cfgJSON)
	}
	singBoxCheck(t, cfgJSON)
}

// TestAWGMergedConfig_SingBoxCheck_MultiHopWithRoute renders a 3-node AWG chain
// (entry -> middle -> exit) for the ENTRY node, with AB_ROUTE_DNS=1 so the route
// section (including per-client source_ip_cidr rules) is emitted, and runs
// sing-box check. Proves the AWG inbound + inter-node outbound + route rules are
// a valid config together.
func TestAWGMergedConfig_SingBoxCheck_MultiHopWithRoute(t *testing.T) {
	if awgServerPriv == "" {
		t.Fatal("could not generate AWG server keypair")
	}
	c := &model.Chain{
		Name:               "awg-mh",
		UserProtocol:       model.UserProtocolAWG,
		Transport:          model.TransportXHTTP,
		AWGEntryServerPriv: awgServerPriv,
		Nodes: []model.ChainNode{
			{ID: "entry", Addr: "entry.example.test:22", Role: model.NodeRoleEntry},
			{ID: "middle", Addr: "middle.example.test:22", Role: model.NodeRoleTransit},
			{ID: "exit", Addr: "exit.example.test:22"}, // last node = exit (auto)
		},
	}
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "entry"}}
	users := []model.User{
		{Name: "alice", Active: true, AWGPublicKey: genAWGPub(t), AWGAddress: "10.8.0.2/32",
			ChainExit: map[string]string{"awg-mh": "exit"}}, // pin two hops down
		{Name: "bob", Active: true, AWGPublicKey: genAWGPub(t), AWGAddress: "10.8.0.3/32"},
	}

	// AB_ROUTE_DNS=1 opts the route section in (see buildMergedNodeConfig).
	os.Setenv("AB_ROUTE_DNS", "1")
	defer os.Unsetenv("AB_ROUTE_DNS")

	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, map[string][]model.User{"awg-mh": users})
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Sanity: route section present with source_ip_cidr per-client rules.
	if !strings.Contains(string(cfgJSON), `"source_ip_cidr"`) {
		t.Errorf("config missing source_ip_cidr route rule:\n%s", cfgJSON)
	}
	if !strings.Contains(string(cfgJSON), `"route"`) {
		t.Errorf("config missing route section:\n%s", cfgJSON)
	}
	singBoxCheck(t, cfgJSON)
}

// TestAWGClientConf_ValidStructure verifies the per-user awg-quick .conf
// rendered by RenderClientAWGConf has the required [Interface]/[Peer] sections
// and the user's per-user address + key (no sing-box check — awg-quick .conf is
// not a sing-box config, but it must be structurally valid for awg-quick).
func TestAWGClientConf_ValidStructure(t *testing.T) {
	c := &model.Chain{
		Name:              "awg-client",
		UserProtocol:      model.UserProtocolAWG,
		AWGEntryServerPub: genAWGPub(t),
		Nodes:             []model.ChainNode{{ID: "n1", Addr: "n1.example.test:22", Role: model.NodeRoleEntry}},
	}
	u := &model.User{
		Name: "alice", Active: true,
		AWGPrivateKey: awgServerPriv, // reuse a real key so the field is populated
		AWGAddress:    "10.8.0.7/32",
	}
	conf, err := RenderClientAWGConf(ClientConfigParams{Chain: c, User: u})
	if err != nil {
		t.Fatalf("RenderClientAWGConf: %v", err)
	}
	for _, want := range []string{
		"[Interface]",
		"Address = 10.8.0.7/32",
		"PrivateKey = ",
		"[Peer]",
		"PublicKey = ",
		"Endpoint = n1.example.test:",
		"AllowedIPs = 0.0.0.0/0, ::/0",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("client .conf missing %q\n%s", want, conf)
		}
	}
}

// genAWGPub returns a real WireGuard public key (base64-Std) for test peers.
func genAWGPub(t *testing.T) string {
	t.Helper()
	_, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}
	return pub
}