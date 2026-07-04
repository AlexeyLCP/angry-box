package takeover

// awg_takeover_test.go — verifies the AWG takeover renderer under the kernel-AWG
// architecture: the takeover KEEPS awg-quick@awg0 running (kernel owns the AWG
// interface, peers, amnezia) and pushes a sing-box TUN-overlay config that
// captures awg0 via include_interface:["awg0"] and routes traffic direct. No
// userspace wireguard endpoint is emitted (that path panics under AmneziaWG).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
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

// TestRenderAWGTakeoverConfig_TUNOverlay verifies the rendered config is a
// TUN-overlay (kernel-AWG architecture): a TUN inbound with
// include_interface:["awg0"], direct/block outbounds, route tun-in→direct, and
// NO userspace wireguard endpoint (the chacha20poly1305 panic path under
// AmneziaWG). The kernel awg-quick@awg0 keeps the server keypair + amnezia +
// peers from the imported awg0.conf — those are NOT in the sing-box config.
func TestRenderAWGTakeoverConfig_TUNOverlay(t *testing.T) {
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
	}
	cfgJSON, err := renderAWGTakeoverConfig(server, peers)
	if err != nil {
		t.Fatalf("renderAWGTakeoverConfig: %v", err)
	}

	// Endpoints MUST be empty — the kernel owns awg0; a userspace wireguard
	// endpoint would panic with chacha20poly1305 under AmneziaWG.
	var top struct {
		Endpoints []json.RawMessage `json:"endpoints"`
		Inbounds  []json.RawMessage `json:"inbounds"`
		Outbounds []json.RawMessage `json:"outbounds"`
		Route     *struct {
			Rules []map[string]any `json:"rules"`
		} `json:"route"`
	}
	if err := json.Unmarshal([]byte(cfgJSON), &top); err != nil {
		t.Fatalf("unmarshal config: %v\n%s", err, cfgJSON)
	}
	if len(top.Endpoints) != 0 {
		t.Errorf("kernel-AWG takeover must have NO userspace endpoints, got %d: %s", len(top.Endpoints), top.Endpoints)
	}

	// One TUN inbound capturing awg0.
	var tunFound bool
	for _, raw := range top.Inbounds {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && m["type"] == "tun" {
			tunFound = true
			inc, _ := m["include_interface"].([]any)
			if len(inc) != 1 || inc[0] != "awg0" {
				t.Errorf("TUN include_interface = %v, want [awg0]", inc)
			}
			if m["stack"] != "mixed" {
				t.Errorf("TUN stack = %v, want mixed", m["stack"])
			}
		}
		if json.Unmarshal(raw, &m) == nil && m["type"] == "wireguard" {
			t.Errorf("takeover must NOT emit a userspace wireguard endpoint: %s", string(raw))
		}
	}
	if !tunFound {
		t.Fatal("kernel-AWG takeover config must have a TUN inbound capturing awg0")
	}

	// Route: tun-in → direct (the single-egress overlay).
	var tunRule map[string]any
	for _, r := range top.Route.Rules {
		if ins, _ := r["inbound"].([]any); len(ins) == 1 && ins[0] == "tun-in" {
			tunRule = r
		}
	}
	if tunRule == nil {
		t.Fatal("missing route rule for inbound tun-in")
	}
	if tunRule["outbound"] != "direct" {
		t.Errorf("tun-in route outbound = %v, want direct", tunRule["outbound"])
	}
}

// TestRenderAWGTakeoverConfig_NoAmneziaBlock — the sing-box TUN-overlay config
// carries no amnezia block regardless of the imported server's amnezia (the
// obfuscation lives in the kernel awg0.conf, which the takeover leaves on disk).
func TestRenderAWGTakeoverConfig_NoAmneziaBlock(t *testing.T) {
	server := &chain.AwgServerConfig{
		PrivateKey: "YNXtAzepDqRv9H52osJVDQnznT5AM11eCK3ESpwSt04=",
		ListenPort: 51820,
		JC:         120, // amnezia present in the imported awg0.conf
	}
	cfgJSON, err := renderAWGTakeoverConfig(server, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(cfgJSON, `"amnezia"`) {
		t.Errorf("TUN-overlay config must NOT carry amnezia (it lives in the kernel awg0.conf):\n%s", cfgJSON)
	}
}

// TestRenderAWGTakeoverConfig_SingBoxCheck runs a real `sing-box check` against
// the rendered TUN-overlay config (proves the structure is valid).
func TestRenderAWGTakeoverConfig_SingBoxCheck(t *testing.T) {
	bin := findSingBoxBinaryE2E()
	if bin == "" {
		t.Skip("no sing-box binary found (deps/sing-box(.exe) or PATH)")
	}
	server := &chain.AwgServerConfig{
		PrivateKey: "YNXtAzepDqRv9H52osJVDQnznT5AM11eCK3ESpwSt04=",
		ListenPort: 51820, Address: "10.8.0.1/24",
		JC: 120, JMIN: 50, JMAX: 1000, S1: 115, S2: 45, S3: 22, S4: 12,
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
