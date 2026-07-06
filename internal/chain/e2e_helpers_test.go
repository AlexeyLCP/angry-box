//go:build e2e

package chain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// E2E VPS roles — entry / middle / exit for multi-hop chains.
const (
	e2eRoleEntry  = 0
	e2eRoleMiddle = 1
	e2eRoleExit   = 2
)

// e2eServers — three Debian 12 x86_64 VPSes with passwordless sudo for user lcp.
var e2eServers = []struct {
	ID      string
	Addr    string
	User    string
	KeyFile string
	Role    string
}{
	{ID: "e2e-entry", Addr: "34.62.128.71:22", User: "lcp", KeyFile: "id_ed25519", Role: "entry"},
	{ID: "e2e-middle", Addr: "207.175.40.161:22", User: "lcp", KeyFile: "id_ed25519", Role: "middle"},
	{ID: "e2e-exit", Addr: "23.251.133.38:22", User: "lcp", KeyFile: "id_ed25519", Role: "exit"},
}

var (
	e2eHeavyMu        sync.Mutex
	e2eHeavyHeld      bool
	e2eServersResetOnce sync.Once
)

func sshKeyPath(filename string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ssh", filename)
}

func newStore(t *testing.T) *chain.Store {
	t.Helper()
	return chain.NewStore(filepath.Join(t.TempDir(), "e2e-store.json"))
}

func e2eContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// e2eResetAllServers fixes common post-crash state on disposable test VPSes:
// root-owned sing-box binary (blocks redeploy) and stale failed units.
func e2eResetAllServers(t *testing.T) {
	t.Helper()
	for _, srv := range e2eServers {
		client, err := sshclient.Connect(srv.Addr, srv.User, sshKeyPath(srv.KeyFile))
		if err != nil {
			t.Logf("reset: connect %s: %v", srv.Role, err)
			continue
		}
		_, _ = client.Run(`sudo chown lcp:lcp /usr/local/bin/sing-box 2>/dev/null
sudo chmod 755 /usr/local/bin/sing-box 2>/dev/null
sudo systemctl reset-failed sing-box 2>/dev/null || true`)
		client.Close()
	}
}

// e2eHeavy serializes tests that mutate remote VPS state. Set E2E_SKIP_HEAVY=1
// to skip the entire heavy suite (e.g. quick CI smoke).
func e2eHeavy(t *testing.T) {
	t.Helper()
	if os.Getenv("E2E_SKIP_HEAVY") == "1" {
		t.Skip("E2E_SKIP_HEAVY=1")
	}
	e2eHeavyMu.Lock()
	e2eHeavyHeld = true
	e2eServersResetOnce.Do(func() { e2eResetAllServers(t) })
	t.Cleanup(func() {
		e2eHeavyHeld = false
		e2eHeavyMu.Unlock()
	})
}

func e2eServer(role int) (id, addr, user, key string) {
	s := e2eServers[role]
	return s.ID, s.Addr, s.User, sshKeyPath(s.KeyFile)
}

// e2eServerIP returns the bare IP (no :22 port) of a VPS by role. Used to
// assert which backend a selector/urltest routed traffic through (egress IP).
func e2eServerIP(role int) string {
	_, addr, _, _ := e2eServer(role)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func e2eHost(role int) model.Host {
	id, addr, user, key := e2eServer(role)
	return model.Host{ID: id, Addr: addr, User: user, KeyPath: key}
}

func e2eConnect(t *testing.T, role int) *sshclient.Client {
	t.Helper()
	_, addr, user, key := e2eServer(role)
	client, err := sshclient.Connect(addr, user, key)
	if err != nil {
		t.Fatalf("ssh connect role=%d (%s): %v", role, addr, err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func registerNode(t *testing.T, store *chain.Store, host model.Host, useSudo bool) {
	t.Helper()
	store.SaveHost(&host)
	store.SaveNodeInfo(&model.NodeInfo{Host: host, UseSudo: useSudo})
}

// registerChainNodes registers every hop with UseSudo. Chain node IDs must match
// the NodeInfo records ApplyChain looks up (a mismatch leaves UseSudo=false and
// deploy fails on root-owned /usr/local/bin/sing-box).
func registerChainNodes(t *testing.T, store *chain.Store, nodes []model.ChainNode, useSudo bool) {
	t.Helper()
	for _, n := range nodes {
		registerNode(t, store, model.Host{ID: n.ID, Addr: n.Addr, User: n.User, KeyPath: n.KeyPath}, useSudo)
	}
}

func newApplier() *chain.Applier {
	return chain.NewApplier(factory.New(nil), nil)
}

type deployChainOpts struct {
	awgClientPub string
}

func deployChain(t *testing.T, store *chain.Store, c *model.Chain, opts deployChainOpts) *chain.ApplyReport {
	t.Helper()
	ctx := e2eContext(t, 8*time.Minute)
	applier := newApplier()
	report, err := applier.ApplyChain(ctx, store, c, opts.awgClientPub)
	if err != nil {
		logDeployFailure(t, c, report, err)
		t.Fatalf("ApplyChain %q: %v", c.Name, err)
	}
	for _, n := range report.Nodes {
		if !n.Success {
			logDeployFailure(t, c, report, fmt.Errorf("node %s: %s", n.ID, n.Error))
			t.Fatalf("node %s failed: %s", n.ID, n.Error)
		}
	}
	t.Logf("deploy OK: chain=%s profile=%s nodes=%d", c.Name, report.Profile, len(report.Nodes))
	return report
}

func logDeployFailure(t *testing.T, c *model.Chain, report *chain.ApplyReport, err error) {
	t.Helper()
	t.Logf("deploy failure: %v", err)
	if report != nil {
		b, _ := json.MarshalIndent(report, "", "  ")
		t.Logf("apply report:\n%s", b)
	}
	if c != nil {
		b, _ := json.MarshalIndent(c, "", "  ")
		t.Logf("chain spec:\n%s", b)
	}
	for i := range c.Nodes {
		role := e2eRoleForAddr(c.Nodes[i].Addr)
		if logs := fetchSingBoxLogs(t, role, 40); logs != "" {
			t.Logf("sing-box journal (node %s):\n%s", c.Nodes[i].ID, logs)
		}
	}
}

func e2eRoleForAddr(addr string) int {
	for i, s := range e2eServers {
		if s.Addr == addr {
			return i
		}
	}
	return e2eRoleMiddle
}

func fetchRemoteConfig(t *testing.T, role int) string {
	t.Helper()
	client := e2eConnect(t, role)
	out, err := client.Run("sudo cat /etc/sing-box/config.json 2>/dev/null")
	if err != nil {
		t.Fatalf("fetch config role=%d: %v", role, err)
	}
	return out
}

func fetchSingBoxLogs(t *testing.T, role int, lines int) string {
	t.Helper()
	client := e2eConnect(t, role)
	out, _ := client.Run(fmt.Sprintf(
		"sudo journalctl -u sing-box -n %d --no-pager 2>/dev/null", lines))
	return strings.TrimSpace(out)
}

func assertNodeHealthy(t *testing.T, role int, expectListenPort int) {
	t.Helper()
	client := e2eConnect(t, role)
	active, _ := client.Run("sudo systemctl is-active sing-box 2>/dev/null")
	if strings.TrimSpace(active) != "active" {
		logs := fetchSingBoxLogs(t, role, 30)
		t.Fatalf("sing-box not active on role=%d: %q\njournal:\n%s",
			role, strings.TrimSpace(active), logs)
	}
	if expectListenPort > 0 {
		check := fmt.Sprintf(
			"sudo ss -lntu 'sport = :%d' 2>/dev/null | grep -q ':%d' && echo yes || echo no",
			expectListenPort, expectListenPort)
		listening, _ := client.Run(check)
		if strings.TrimSpace(listening) != "yes" {
			cfg := fetchRemoteConfig(t, role)
			t.Fatalf("role=%d not listening on %d\nconfig excerpt:\n%s",
				role, expectListenPort, truncate(cfg, 2000))
		}
	}
}

func assertConfigContains(t *testing.T, cfg string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(cfg, s) {
			t.Errorf("config missing %q\n--- config ---\n%s", s, truncate(cfg, 3000))
		}
	}
}

func assertPostDeployHash(t *testing.T, store *chain.Store, nodeID string, cfg string) {
	t.Helper()
	info, err := store.GetNodeInfo(nodeID)
	if err != nil {
		t.Fatalf("GetNodeInfo(%s): %v", nodeID, err)
	}
	want := chain.ConfigHash([]byte(cfg))
	if info.LastDeployedHash != want {
		t.Errorf("LastDeployedHash mismatch: got %s want %s", info.LastDeployedHash, want)
	}
	if info.LastDeployedHash == "" {
		t.Error("LastDeployedHash empty after successful apply")
	}
}

func verifyClientConnectivity(t *testing.T, c *model.Chain, expectEgressRole int) {
	t.Helper()
	if os.Getenv("AB_ROUTE_DNS") != "1" {
		t.Skip("set AB_ROUTE_DNS=1 to verify end-to-end client routing")
	}
	// TUIC/QUIC from WSL workstations is often blocked; run the client on the
	// chain entry VPS (loopback → deployed TUIC inbound → chain → exit) instead.
	if os.Getenv("E2E_CLIENT_LOCAL") == "1" {
		verifyClientConnectivityLocal(t, c, expectEgressRole)
		return
	}
	verifyClientConnectivityOnEntry(t, c, expectEgressRole)
}

func verifyClientConnectivityOnEntry(t *testing.T, c *model.Chain, expectEgressRole int) {
	t.Helper()
	if len(c.Nodes) == 0 {
		t.Fatal("chain has no nodes")
	}
	entryRole := e2eRoleForAddr(c.Nodes[0].Addr)
	cfgJSON, err := chain.RenderClientConfig(chain.ClientConfigParams{
		Chain:               c,
		LocalProxyAddr:      "127.0.0.1:11080",
		EntryHostOverride:   "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	// Resolve names on the VPS directly — routing DNS through tuic-out before the
	// tunnel is up causes a bootstrap loop and empty curl responses.
	cfgJSON = strings.ReplaceAll(cfgJSON, `"final": "dns-remote"`, `"final": "dns-direct"`)
	t.Logf("entry-side client config (role=%d):\n%s", entryRole, cfgJSON)

	client := e2eConnect(t, entryRole)
	ctx := e2eContext(t, 2*time.Minute)
	remoteCfg := "/tmp/e2e-client-" + c.Name + ".json"
	remoteLog := remoteCfg + ".log"
	if err := client.UploadText(ctx, cfgJSON, remoteCfg, 0o600); err != nil {
		t.Fatalf("upload client config: %v", err)
	}
	defer func() { _, _ = client.Run("rm -f " + remoteCfg + " " + remoteLog) }()

	checkCmd := fmt.Sprintf("/usr/local/bin/sing-box check -c %s", remoteCfg)
	if out, err := client.Run(checkCmd); err != nil {
		t.Fatalf("remote client check: %v\n%s", err, out)
	}

	runScript := fmt.Sprintf(`CFG=%q
LOG=%q
pkill -f "sing-box run -c $CFG" 2>/dev/null || true
/usr/local/bin/sing-box run -c "$CFG" >"$LOG" 2>&1 &
BPID=$!
sleep 10
IP=$(curl -s --max-time 30 -x socks5h://127.0.0.1:11080 https://ifconfig.me || true)
kill "$BPID" 2>/dev/null || true
wait "$BPID" 2>/dev/null || true
echo EGRESS:$IP
echo ---LOG---
tail -30 "$LOG" 2>/dev/null || true
`, remoteCfg, remoteLog)
	out, _, _, err := client.RunWithOutput(ctx, runScript, 2*time.Minute)
	if err != nil && !strings.Contains(out, "EGRESS:") {
		t.Fatalf("remote client run: %v\n%s", err, out)
	}
	t.Logf("remote client output:\n%s", out)

	gotIP := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "EGRESS:") {
			gotIP = strings.TrimSpace(strings.TrimPrefix(line, "EGRESS:"))
		}
	}
	_, expectAddr, _, _ := e2eServer(expectEgressRole)
	expectIP := strings.TrimSpace(expectAddr[:strings.LastIndexByte(expectAddr, ':')])
	t.Logf("egress IP=%s expect exit=%s", gotIP, expectIP)
	if gotIP == "" {
		t.Fatal("empty egress IP from entry-side client")
	}
	if gotIP != expectIP {
		t.Errorf("egress %q != exit node %q", gotIP, expectIP)
	}
}

func verifyClientConnectivityLocal(t *testing.T, c *model.Chain, expectEgressRole int) {
	t.Helper()
	clientBin := e2eClientBinary(t)
	cfgJSON, err := chain.RenderClientConfig(chain.ClientConfigParams{
		Chain:          c,
		LocalProxyAddr: "127.0.0.1:11080",
	})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	cfgJSON = strings.ReplaceAll(cfgJSON, `"final": "dns-remote"`, `"final": "dns-direct"`)
	t.Logf("local client config:\n%s", cfgJSON)

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "client.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("write client config: %v", err)
	}
	if checkOut, err := exec.Command(clientBin, "check", "-c", cfgPath).CombinedOutput(); err != nil {
		t.Fatalf("client check: %v\n%s", err, checkOut)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, clientBin, "run", "-c", cfgPath)
	var runLog strings.Builder
	cmd.Stdout = &runLog
	cmd.Stderr = &runLog
	if err := cmd.Start(); err != nil {
		t.Fatalf("start client: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	time.Sleep(3 * time.Second)

	egress, err := exec.Command("curl", "-s", "--max-time", "25",
		"-x", "socks5h://127.0.0.1:11080", "https://ifconfig.me").CombinedOutput()
	if err != nil {
		t.Fatalf("curl through chain: %v\nclient log:\n%s", err, runLog.String())
	}
	gotIP := strings.TrimSpace(string(egress))
	_, expectAddr, _, _ := e2eServer(expectEgressRole)
	expectIP := strings.TrimSpace(expectAddr[:strings.LastIndexByte(expectAddr, ':')])
	t.Logf("egress IP=%s expect exit=%s", gotIP, expectIP)
	if gotIP == "" {
		t.Fatal("empty egress IP")
	}
	if gotIP != expectIP {
		t.Errorf("egress %q != exit node %q", gotIP, expectIP)
	}
}

func e2eClientBinary(t *testing.T) string {
	t.Helper()
	// On Linux/WSL run the native binary — sing-box.exe cannot read /tmp paths.
	// On Windows hosts, deps/sing-box.exe is preferred.
	var candidates []string
	if runtime.GOOS == "windows" {
		candidates = []string{
			filepath.Join("deps", "sing-box.exe"),
			filepath.Join("..", "..", "deps", "sing-box.exe"),
		}
	} else {
		candidates = []string{
			"/usr/local/bin/sing-box",
			filepath.Join("deps", "sing-box.exe"),
			filepath.Join("..", "..", "deps", "sing-box.exe"),
		}
	}
	for _, p := range candidates {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("no local sing-box client binary (build deps/sing-box.exe or install sing-box)")
	return ""
}

func performRollbackTest(t *testing.T, role int, nodeID string) {
	t.Helper()
	client := e2eConnect(t, role)
	good, err := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8443})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := chain.PushConfig(context.Background(), client, nodeID, string(good), true); err != nil {
		t.Fatalf("seed good config: %v", err)
	}
	bad := `{"inbounds":[{"type":"invalid_protocol","listen_port":8443}],"outbounds":[]}`
	_, err = chain.PushConfig(context.Background(), client, nodeID, bad, true)
	if err == nil {
		t.Fatal("expected push to fail on invalid config")
	}
	t.Logf("expected push error: %v", err)
	assertNodeHealthy(t, role, 0)
	cfg := fetchRemoteConfig(t, role)
	if !strings.Contains(cfg, "vless") {
		t.Errorf("rollback did not restore vless config; got:\n%s", truncate(cfg, 1500))
	}
}

func buildChainNodes(roles ...int) []model.ChainNode {
	var nodes []model.ChainNode
	for _, r := range roles {
		id, addr, user, key := e2eServer(r)
		nodes = append(nodes, model.ChainNode{
			ID: id, Addr: addr, User: user, KeyPath: key,
		})
	}
	return nodes
}

func baseChain(name string, nodes []model.ChainNode) *model.Chain {
	return &model.Chain{
		Name:         name,
		Nodes:        nodes,
		Strategy:     model.StrategyURLTest,
		Transport:    model.TransportXHTTP,
		UserProtocol: model.UserProtocolTUIC,
		UserEntryPort: 443,
	}
}

func seedAWGConf(t *testing.T, role int, extraPeerName string) func() {
	t.Helper()
	client := e2eConnect(t, role)
	seed := `sudo mkdir -p /etc/amnezia/amneziawg
sudo tee /etc/amnezia/amneziawg/awg0.conf >/dev/null <<'AWGEOF'
[Interface]
PrivateKey = aGm5k9p3R2tXvY1bC8dE4fG7hJ0kLmNoPqRsTuVwXyZ=
Address = 10.8.0.1/24
ListenPort = 51820
Jc = 7
Jmin = 50
Jmax = 500
AWGEOF
sudo tee /etc/amnezia/amneziawg/awg0-peers.list >/dev/null <<'PEEREOF'
[Peer]
PublicKey = dJp8n2s6U5wYbB4cF1gH7iJ0kM3lNpOrQsSuVwXyZaBc=
AllowedIPs = 10.8.0.2/32
Name = existing-peer
PEEREOF
`
	if extraPeerName != "" {
		seed += fmt.Sprintf(`
sudo tee -a /etc/amnezia/amneziawg/awg0-peers.list >/dev/null <<'PEEREOF'
[Peer]
PublicKey = eKq9o3t7V6xZcC5dG2hI8jK1lN4mOqPrRtTvUwXyZaBd=
AllowedIPs = 10.8.0.3/32
Name = %s
PEEREOF
`, extraPeerName)
	}
	ctx := e2eContext(t, 60*time.Second)
	if _, _, _, err := client.RunWithOutput(ctx, seed, 60*time.Second); err != nil {
		t.Fatalf("seed AWG: %v", err)
	}
	return func() {
		c2, err := sshclient.Connect(e2eServers[role].Addr, e2eServers[role].User, sshKeyPath(e2eServers[role].KeyFile))
		if err == nil {
			_, _ = c2.Run("sudo rm -rf /etc/amnezia/amneziawg")
			c2.Close()
		}
	}
}

func readAWGPeersList(t *testing.T, role int) string {
	t.Helper()
	client := e2eConnect(t, role)
	out, _ := client.Run("sudo cat /etc/amnezia/amneziawg/awg0-peers.list 2>/dev/null")
	return out
}

func stopSingBox(t *testing.T, role int) {
	t.Helper()
	client := e2eConnect(t, role)
	_, _ = client.Run("sudo systemctl stop sing-box 2>/dev/null")
}

func startSingBox(t *testing.T, role int) {
	t.Helper()
	client := e2eConnect(t, role)
	_, _ = client.Run("sudo systemctl start sing-box 2>/dev/null")
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated]"
}

const e2eBackendSocksPort = 11082

// deploySocksBackend starts a minimal SOCKS ingress on a VPS for urltest targets.
func deploySocksBackend(t *testing.T, role int, nodeID string) {
	t.Helper()
	cfg := fmt.Sprintf(`{
  "log": {"level": "info"},
  "inbounds": [{"type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": %d}],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"final": "direct"}
}`, e2eBackendSocksPort)
	client := e2eConnect(t, role)
	if _, err := chain.PushConfig(context.Background(), client, nodeID, cfg, true); err != nil {
		t.Fatalf("deploy socks backend role=%d: %v", role, err)
	}
}

// deployURLTestBalancer pushes urltest across SOCKS outbounds to live backends.
func deployURLTestBalancer(t *testing.T, entryRole int, nodeID string, backendRoles ...int) {
	t.Helper()
	if len(backendRoles) < 2 {
		t.Fatal("need at least 2 backends for urltest")
	}
	var outbounds []map[string]any
	var tags []string
	for i, r := range backendRoles {
		_, addr, _, _ := e2eServer(r)
		host := addr[:strings.LastIndexByte(addr, ':')]
		tag := fmt.Sprintf("backend-%d", i)
		tags = append(tags, tag)
		outbounds = append(outbounds, map[string]any{
			"type":        "socks",
			"tag":         tag,
			"server":      host,
			"server_port": e2eBackendSocksPort,
			"version":     "5",
		})
	}
	outbounds = append(outbounds, map[string]any{
		"type":      "urltest",
		"tag":       "lb-urltest",
		"outbounds": tags,
		"url":       "http://www.gstatic.com/generate_204",
		"interval":  "5s",
		"tolerance": 50,
	})
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	cfg := map[string]any{
		"log":       map[string]any{"level": "info"},
		"inbounds":  []map[string]any{{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 11081}},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": []map[string]any{{"inbound": []string{"mixed-in"}, "outbound": "lb-urltest"}},
			"final": "direct",
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	client := e2eConnect(t, entryRole)
	if _, err := chain.PushConfig(context.Background(), client, nodeID, string(b), true); err != nil {
		t.Fatalf("deploy urltest balancer: %v", err)
	}
}

// deploySelectorBalancer pushes a selector across SOCKS outbounds to live
// backends. Unlike urltest, the selector does no health probing: it routes all
// traffic to the configured default backend until the selector's default is
// changed. defaultIdx picks which backend index is the initial default.
// Returns the entry-node ID so the caller can re-deploy with a new default.
func deploySelectorBalancer(t *testing.T, entryRole int, nodeID string, defaultIdx int, backendRoles ...int) {
	t.Helper()
	if len(backendRoles) < 2 {
		t.Fatal("need at least 2 backends for selector")
	}
	if defaultIdx < 0 || defaultIdx >= len(backendRoles) {
		t.Fatalf("defaultIdx %d out of range [0,%d)", defaultIdx, len(backendRoles))
	}
	var outbounds []map[string]any
	var tags []string
	for i, r := range backendRoles {
		_, addr, _, _ := e2eServer(r)
		host := addr[:strings.LastIndexByte(addr, ':')]
		tag := fmt.Sprintf("backend-%d", i)
		tags = append(tags, tag)
		outbounds = append(outbounds, map[string]any{
			"type":        "socks",
			"tag":         tag,
			"server":      host,
			"server_port": e2eBackendSocksPort,
			"version":     "5",
		})
	}
	outbounds = append(outbounds, map[string]any{
		"type":      "selector",
		"tag":       "lb-selector",
		"outbounds": tags,
		"default":   tags[defaultIdx],
	})
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	cfg := map[string]any{
		"log":       map[string]any{"level": "info"},
		"inbounds":  []map[string]any{{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 11081}},
		"outbounds": outbounds,
		"route": map[string]any{
			"rules": []map[string]any{{"inbound": []string{"mixed-in"}, "outbound": "lb-selector"}},
			"final": "direct",
		},
	}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	client := e2eConnect(t, entryRole)
	if _, err := chain.PushConfig(context.Background(), client, nodeID, string(b), true); err != nil {
		t.Fatalf("deploy selector balancer: %v", err)
	}
}

// captureQUICOrSkip tries several QUIC-enabled domains; skips if UDP/443 blocked.
func captureQUICOrSkip(t *testing.T) chain.CaptureResult {
	t.Helper()
	for _, domain := range []string{"www.google.com", "one.one.one.one", "www.cloudflare.com", "dns.google"} {
		res := chain.CaptureQUICSignature(domain, 8*time.Second)
		if res.OK {
			t.Logf("QUIC capture ok on %s (%d packets)", domain, len(res.Packets))
			return res
		}
		t.Logf("QUIC capture %s: %s", domain, res.Warning)
	}
	t.Skip("QUIC capture unavailable on all candidate domains (UDP/443 likely blocked)")
	return chain.CaptureResult{}
}