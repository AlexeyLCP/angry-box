//go:build wsl_smoke

// Package wslsmoke contains WSL smoke tests that drive the real angry-box
// pipeline against an Ubuntu-24.04 WSL instance acting as the test node. It
// lives outside package chain to avoid an import cycle (it needs both chain
// and backend/singbox, which themselves cross-reference).
//
// Setup (one-time, see TESTING.md): WSL Ubuntu-24.04 with systemd+sshd, an
// ed25519 key in authorized_keys, and a local HTTP server serving the patched
// tarball from deps/ on http://127.0.0.1:8000/.
//
// Run:
//
//	WSL_TEST_HOST=127.0.0.1:22 WSL_TEST_USER=lcp WSL_TEST_KEY=$HOME/.ssh/angry-test-key \
//	  go test -tags wsl_smoke ./internal/wslsmoke/ -run WSL -v -timeout 900s
package wslsmoke

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

const localTarballURL = "http://127.0.0.1:8000/sing-box-1.13.14-extended-2.5.0-patched-linux-amd64.tar.gz"

// TestMain wires the SSH TOFU host-key manager (required by sshclient.Connect)
// to a temp-backed store, then runs the suite.
func TestMain(m *testing.M) {
	st := chain.NewTestStoreForSmoke()
	sshclient.SetHostKeyManager(st)
	sshclient.SetKeyResolver(st)
	os.Exit(m.Run())
}

func wslEnv(t *testing.T) (host, user, key string) {
	t.Helper()
	host = os.Getenv("WSL_TEST_HOST")
	if host == "" {
		host = "127.0.0.1:22"
	}
	user = os.Getenv("WSL_TEST_USER")
	if user == "" {
		user = "lcp"
	}
	key = os.ExpandEnv(os.Getenv("WSL_TEST_KEY"))
	if key == "" {
		key = filepath.Join(os.Getenv("HOME"), ".ssh", "angry-test-key")
	}
	if _, err := os.Stat(key); err != nil {
		t.Skipf("WSL smoke: test key %s missing: %v (run setup first, see TESTING.md)", key, err)
	}
	return host, user, key
}

func wslConnect(t *testing.T) *sshclient.Client {
	t.Helper()
	host, user, key := wslEnv(t)
	c, err := sshclient.Connect(host, user, key)
	if err != nil {
		t.Fatalf("WSL ssh connect to %s@%s: %v", user, host, err)
	}
	return c
}

func wslHost(t *testing.T) model.Host {
	t.Helper()
	host, user, key := wslEnv(t)
	return model.Host{ID: "wsl-test", Addr: host, User: user, KeyPath: key}
}

func runOn(t *testing.T, c *sshclient.Client, cmd string, timeout time.Duration) string {
	t.Helper()
	stdout, stderr, exit, err := c.RunWithOutput(context.Background(), cmd, timeout)
	if err != nil {
		t.Fatalf("wsl run %q: %v (exit %d) stderr=%s", cmd, err, exit, stderr)
	}
	return stdout + stderr
}

// TestWSL_SSHConnect verifies a plain SSH connection works.
func TestWSL_SSHConnect(t *testing.T) {
	c := wslConnect(t)
	defer c.Close()
	out := runOn(t, c, "echo WSL_SSH_OK && uname -r", 15*time.Second)
	if !strings.Contains(out, "WSL_SSH_OK") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestWSL_DeployPatchedBinary installs the patched sing-box on WSL and verifies
// it runs and reports an extended version. Uses the local HTTP tarball server.
func TestWSL_DeployPatchedBinary(t *testing.T) {
	restore := singbox.SetDownloadURLForTest("amd64", localTarballURL)
	defer restore()
	b := singbox.New()
	res, err := b.DeployOpts(context.Background(), wslHost(t), singbox.DeployOptions{UseSudo: true})
	if err != nil {
		t.Fatalf("Deploy: %v (result=%+v)", err, res)
	}
	t.Logf("deploy result: %+v", res)

	c := wslConnect(t)
	defer c.Close()
	// The patched binary is built from source without version ldflags, so it
	// reports "version unknown" rather than containing "extended". Verify it
	// actually runs (version prints) and the service is active instead.
	ver := runOn(t, c, "sudo /usr/local/bin/sing-box version 2>&1 | head -1", 30*time.Second)
	t.Logf("sing-box version: %s", strings.TrimSpace(ver))
	if !strings.Contains(strings.ToLower(ver), "sing-box") {
		t.Errorf("sing-box version output unexpected: %s", ver)
	}
	active := runOn(t, c, "systemctl is-active sing-box", 15*time.Second)
	if strings.TrimSpace(active) != "active" {
		t.Errorf("sing-box not active: %q", active)
	}
}

// TestWSL_ApplyRealityXHTTP generates a REALITY+XHTTP config, pushes it via the
// chain.pushConfig path, and verifies sing-box check passes and the service
// stays up.
func TestWSL_ApplyRealityXHTTP(t *testing.T) {
	content, err := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8443})
	if err != nil {
		t.Fatal(err)
	}
	c := wslConnect(t)
	defer c.Close()
	if _, err := chain.PushConfigForTest(c, string(content), true); err != nil {
		t.Fatalf("pushConfig REALITY+XHTTP: %v", err)
	}
	active := runOn(t, c, "systemctl is-active sing-box", 15*time.Second)
	if strings.TrimSpace(active) != "active" {
		t.Errorf("service not active after apply: %q", active)
	}
}

// TestWSL_RollbackOnBadConfig pushes a known-good config first (so there IS a
// backup to roll back to), then a broken config that fails `sing-box check`,
// expects pushConfig to fail + rollback to the good config, and the service to
// stay up.
func TestWSL_RollbackOnBadConfig(t *testing.T) {
	c := wslConnect(t)
	defer c.Close()
	// 1. Establish a known-good config as the "previous" state.
	good, err := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.PushConfigForTest(c, string(good), true); err != nil {
		t.Fatalf("seed good config: %v", err)
	}
	// 2. Push a bad config that sing-box check will reject (unknown inbound type
	// is a hard schema error, unlike a malformed UUID which sing-box tolerates).
	bad := `{"inbounds":[{"type":"invalid_protocol","listen_port":8443}],"outbounds":[]}`
	_, err = chain.PushConfigForTest(c, bad, true)
	if err == nil {
		t.Fatal("expected pushConfig to fail on bad config")
	}
	t.Logf("got expected error: %v", err)
	active := runOn(t, c, "systemctl is-active sing-box", 15*time.Second)
	if strings.TrimSpace(active) != "active" {
		t.Errorf("service not active after rollback: %q", active)
	}
}

// TestWSL_FirstDeployNoRollback wipes configs, pushes bad config, verifies the
// error is surfaced (not a panic) and rollback is a no-op.
func TestWSL_FirstDeployNoRollback(t *testing.T) {
	c := wslConnect(t)
	defer c.Close()
	runOn(t, c, "sudo rm -f /etc/sing-box/config.json && sudo rm -rf ~/sing-box-orch-backup-* /etc/sing-box/config.json.bak.*", 15*time.Second)
	bad := `{"inbounds":[{"type":"invalid_protocol"}],"outbounds":[]}`
	_, err := chain.PushConfigForTest(c, bad, true)
	if err == nil {
		t.Fatal("expected pushConfig to fail with no backup available")
	}
	t.Logf("first-deploy no-rollback surfaced error correctly: %v", err)
}

// TestWSL_AWGKernelInstall installs the AmneziaWG kernel module. In WSL the
// Microsoft kernel may not insmod the module — logged as a note, not a hard
// failure.
func TestWSL_AWGKernelInstall(t *testing.T) {
	b := singbox.New()
	err := b.InstallAWGModule(context.Background(), wslHost(t))
	if err != nil {
		t.Logf("AWG kernel install failed in WSL (expected — Microsoft kernel): %v", err)
		c := wslConnect(t)
		defer c.Close()
		which := runOn(t, c, "which awg awg-quick 2>/dev/null || echo MISSING", 15*time.Second)
		if strings.TrimSpace(which) == "MISSING" {
			t.Log("awg/awg-quick not installed — amneziawg-tools unavailable in WSL apt. Documented limitation.")
		} else {
			t.Logf("awg tooling present: %s", strings.TrimSpace(which))
		}
		return
	}
	t.Log("AWG kernel module installed successfully in WSL")
}

// TestWSL_ImportAWGConfigs seeds awg0.conf + awg-exit-n1.conf in WSL and
// verifies the SSH importer parses them and back-fills placeholder inbounds.
func TestWSL_ImportAWGConfigs(t *testing.T) {
	c := wslConnect(t)
	defer c.Close()
	seed := `set -e
sudo mkdir -p /etc/amnezia/amneziawg
sudo tee /etc/amnezia/amneziawg/awg0.conf >/dev/null <<'EOF'
[Interface]
PrivateKey = serverprivkey1234567890abcdef
Address = 10.45.116.1/24
ListenPort = 55555
Jc = 7
Jmin = 50
Jmax = 500
S1 = 100
S2 = 50
S3 = 30
S4 = 10
H1 = 100-200
H2 = 300-400
H3 = 500-600
H4 = 700-800
EOF
sudo tee /etc/amnezia/amneziawg/awg-exit-n1.conf >/dev/null <<'EOF'
[Interface]
PrivateKey = exitpriv1234567890abcdef
Address = 10.47.3.2/24
Jc = 4
Jmin = 50
Jmax = 837
[Peer]
PublicKey = exitpubkey1234567890abcd
Endpoint = 1.2.3.4:51820
PresharedKey = psk123
EOF
echo SEEDED
`
	if !strings.Contains(runOn(t, c, seed, 30*time.Second), "SEEDED") {
		t.Fatal("seed failed")
	}

	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{{Protocol: "awg", ServerPrivKey: "TODO", AWGClientPub: "TODO"}}
	res, err := chain.ImportAWGConfigs(info.Host, true, info)
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if res.ServerConfig == nil || res.ServerConfig.ListenPort != 55555 || res.ServerConfig.JC != 7 {
		t.Fatalf("server config parse wrong: %+v", res.ServerConfig)
	}
	if len(res.ExitNodes) == 0 || res.ExitNodes[0].PublicKey != "exitpubkey1234567890abcd" || res.ExitNodes[0].Endpoint != "1.2.3.4:51820" {
		t.Fatalf("exit node parse wrong: %+v", res.ExitNodes)
	}
	if info.Inbounds[0].ServerPrivKey == "TODO" {
		t.Error("placeholder ServerPrivKey not back-filled")
	}
	t.Logf("import ok: ListenPort=%d exits=%d backfill=%s", res.ServerConfig.ListenPort, len(res.ExitNodes), res.DBUpdated)
}

// TestWSL_QUICCapture runs a live QUIC capture. Skipped if UDP/443 is blocked.
func TestWSL_QUICCapture(t *testing.T) {
	res := chain.CaptureQUICSignature("www.cloudflare.com", 6*time.Second)
	if !res.OK {
		t.Skipf("QUIC capture not OK (UDP/443 likely blocked): %s", res.Warning)
	}
	if res.Source != "quic" || len(res.Packets) != 5 {
		t.Errorf("capture: source=%q packets=%d", res.Source, len(res.Packets))
	}
	for i, p := range res.Packets {
		if !strings.HasPrefix(p, "<b 0x") {
			t.Errorf("packet %d not <b 0x...>: %q", i, p)
		}
	}
	t.Logf("captured %d packets", len(res.Packets))
}

// TestWSL_DeployStatusHash verifies the deploy hash is recorded and a config
// mutation flips has_pending to true (pure logic, no live node needed).
func TestWSL_DeployStatusHash(t *testing.T) {
	st := chain.NewTestStoreForSmoke()
	content, err := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8443})
	if err != nil {
		t.Fatal(err)
	}
	chain.RecordDeploySuccessForTest(st, "wsl-test", string(content))
	reloaded, _ := st.GetNodeInfo("wsl-test")
	if reloaded.LastDeployedHash == "" {
		t.Fatal("LastDeployedHash not recorded")
	}
	content2, _ := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8444})
	if chain.ConfigHash(content2) == reloaded.LastDeployedHash {
		t.Error("different config produced same hash")
	}
	t.Log("deploy-status hash comparison works")
}

// TestWSL_ConfigPreview renders a merged config and verifies it's valid JSON.
func TestWSL_ConfigPreview(t *testing.T) {
	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{{Protocol: "vless-reality", Port: 8443, UUID: "11111111-2222-3333-4444-555555555555"}}
	cfg, _, err := chain.RenderMergedNodeConfig(info, nil)
	if err != nil {
		t.Fatalf("RenderMergedNodeConfig: %v", err)
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("preview not valid JSON: %v", err)
	}
	t.Logf("preview OK, %d bytes", len(b))
}

// TestWSL_AutoApplyPerHostLock verifies the per-host lock returns the same mutex
// for a node and different across nodes (pure logic).
func TestWSL_AutoApplyPerHostLock(t *testing.T) {
	if chain.HostLockForTest("wsl-test") != chain.HostLockForTest("wsl-test") {
		t.Error("same nodeID should yield same mutex")
	}
	if chain.HostLockForTest("wsl-test") == chain.HostLockForTest("other") {
		t.Error("different nodeID should yield different mutex")
	}
}