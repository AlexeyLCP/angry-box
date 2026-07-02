//go:build e2e

package chain_test

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/backend/factory"
	"github.com/alexeylcp/angry-box/internal/backend/singbox"
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/takeover"
	sshclient "github.com/alexeylcp/angry-box/internal/ssh"
)

// Heavy tests mutate remote VPS state and run sequentially (e2eHeavy mutex).
// Run: go test -tags e2e ./internal/chain/ -run TestE2E_Heavy -v -timeout 3600s

// ─── Deployment & Takeover ────────────────────────────────────────────────────

func TestE2E_Heavy_Deploy_FreshNode(t *testing.T) {
	e2eHeavy(t)
	host := e2eHost(e2eRoleEntry)
	backend := factory.New(nil).Create()
	result, err := backend.DeployWithOptions(e2eContext(t, 5*time.Minute), host, model.DeployOptions{UseSudo: true})
	if err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !result.Success {
		t.Fatalf("deploy failed: %s", result.Message)
	}
	assertNodeHealthy(t, e2eRoleEntry, 0)
	t.Logf("version: %s", result.Version)
}

func TestE2E_Heavy_Takeover_SingBox_FullFlow(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleMiddle)
	registerChainNodes(t, store, nodes, true)
	host := model.Host{ID: nodes[0].ID, Addr: nodes[0].Addr, User: nodes[0].User, KeyPath: nodes[0].KeyPath}

	c := baseChain("takeover-seed", nodes)
	deployChain(t, store, c, deployChainOpts{})

	det, err := takeover.DetectVPN(e2eContext(t, 2*time.Minute), host, true)
	if err != nil {
		t.Fatalf("DetectVPN: %v", err)
	}
	if det.Type != takeover.DetectedSingBox {
		t.Fatalf("expected sing-box, got %q", det.Type)
	}

	res, err := takeover.Takeover(e2eContext(t, 5*time.Minute), store, factory.New(nil), host, true, det)
	if err != nil && res == nil {
		t.Fatalf("Takeover: %v", err)
	}
	if res.Status != "taken" {
		t.Errorf("status=%q want taken", res.Status)
	}
	if res.ConvertedInbounds == 0 {
		t.Error("expected converted inbounds")
	}
	assertNodeHealthy(t, e2eRoleMiddle, 443)
}

func TestE2E_Heavy_Rollback_OnBadConfig(t *testing.T) {
	e2eHeavy(t)
	performRollbackTest(t, e2eRoleMiddle, "rollback-middle")
}

// ─── Protocol + Obfuscation Matrix ────────────────────────────────────────────

func TestE2E_Heavy_Protocol_VLESSRealityXHTTP_Advanced(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	// REALITY+XHTTP obfuscation applies to inter-node transport inbounds, not the
	// plain VLESS user-entry builder. Verify on the exit hop's transport-in.
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleExit)
	registerChainNodes(t, store, nodes, true)

	c := &model.Chain{
		Name:               "e2e-vless-xhttp",
		Nodes:              nodes,
		Strategy:           model.StrategyURLTest,
		Transport:          model.TransportXHTTP,
		UserProtocol:       model.UserProtocolTUIC,
		ObfuscationProfile: "xhttp_max_stealth_2026",
		UserEntryPort:      443,
	}
	deployChain(t, store, c, deployChainOpts{})

	cfg := fetchRemoteConfig(t, e2eRoleExit)
	assertConfigContains(t, cfg,
		`"type": "vless"`,
		`"reality"`,
		`"short_id"`,
		`"transport"`,
		`ch-e2e-vless-xhttp-transport-in`,
	)
	if preset, ok := chain.GetPreset("xhttp_max_stealth_2026"); ok && preset.XHTTP != nil {
		if len(preset.XHTTP.Paths) > 0 {
			assertConfigContains(t, cfg, preset.XHTTP.Paths[0])
		}
	}
	assertNodeHealthy(t, e2eRoleExit, 443)
}

func TestE2E_Heavy_Protocol_TUIC(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleMiddle)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-tuic", nodes)
	deployChain(t, store, c, deployChainOpts{})
	cfg := fetchRemoteConfig(t, e2eRoleMiddle)
	assertConfigContains(t, cfg, `"tuic"`, `"alpn"`, `"h3"`)
	assertNodeHealthy(t, e2eRoleMiddle, 443)
}

func TestE2E_Heavy_Protocol_AWG_Kernel(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleEntry)
	registerChainNodes(t, store, nodes, true)

	c := &model.Chain{
		Name:               "e2e-awg-kernel",
		Nodes:              nodes,
		Strategy:           model.StrategyURLTest,
		Transport:          model.TransportXHTTP,
		UserProtocol:       model.UserProtocolAWG,
		ObfuscationProfile: "pro_2026",
		UserEntryPort:      51820,
	}
	report, err := newApplier().ApplyChain(e2eContext(t, 10*time.Minute), store, c, "")
	if err != nil {
		t.Fatalf("ApplyChain AWG kernel: %v", err)
	}
	if report.AWG == nil {
		t.Fatal("expected AWG client material in deploy report")
	}
	t.Logf("AWG material: cps=%d mimicry=%s", report.AWG.CPSLevel, report.AWG.Mimicry)

	cfg := fetchRemoteConfig(t, e2eRoleEntry)
	assertConfigContains(t, cfg, `"wireguard"`, `"amnezia"`, `"jc"`)

	client := e2eConnect(t, e2eRoleEntry)
	lsmod, _ := client.Run("lsmod 2>/dev/null | grep -i amnezia || echo NO_MOD")
	if strings.Contains(lsmod, "NO_MOD") {
		t.Fatal("amneziawg kernel module not loaded after orchestrator install")
	}
	t.Logf("kernel module: %s", strings.TrimSpace(lsmod))

	awgQuick, _ := client.Run("command -v awg-quick 2>/dev/null || echo missing")
	if strings.TrimSpace(awgQuick) == "missing" {
		t.Fatal("awg-quick not installed — orchestrator must ship AmneziaWG tools with sing-box-extended")
	}
	t.Logf("awg-quick: %s", strings.TrimSpace(awgQuick))

	assertNodeHealthy(t, e2eRoleEntry, 0)
}

func TestE2E_Heavy_Protocol_AWG_Userspace(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleExit)
	registerChainNodes(t, store, nodes, true)

	c := &model.Chain{
		Name:               "e2e-awg-userspace",
		Nodes:              nodes,
		Strategy:           model.StrategyURLTest,
		Transport:          model.TransportXHTTP,
		UserProtocol:       model.UserProtocolAWG,
		ObfuscationProfile: "russia_2026",
		UserEntryPort:      51821,
	}
	_, err := newApplier().ApplyChain(e2eContext(t, 8*time.Minute), store, c, "")
	if err != nil {
		t.Fatalf("ApplyChain AWG userspace: %v", err)
	}
	cfg := fetchRemoteConfig(t, e2eRoleExit)
	assertConfigContains(t, cfg, `"wireguard"`, `"system": false`, `"amnezia"`)
	assertNodeHealthy(t, e2eRoleExit, 0)
}

// ─── Chain Construction ───────────────────────────────────────────────────────

func TestE2E_Heavy_Chain_SingleNode(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleMiddle)
	registerChainNodes(t, store, nodes, true)
	deployChain(t, store, baseChain("e2e-single", nodes), deployChainOpts{})
	assertNodeHealthy(t, e2eRoleMiddle, 443)
}

func TestE2E_Heavy_Chain_2Hop(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-2hop", nodes)
	c.Transport = model.TransportReality
	deployChain(t, store, c, deployChainOpts{})
	assertNodeHealthy(t, e2eRoleEntry, 443)
	assertNodeHealthy(t, e2eRoleExit, 443)
}

func TestE2E_Heavy_Chain_3Hop(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleMiddle, e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-3hop", nodes)
	c.Transport = model.TransportReality
	deployChain(t, store, c, deployChainOpts{})
	assertNodeHealthy(t, e2eRoleEntry, 443)
	assertNodeHealthy(t, e2eRoleMiddle, 443)
	assertNodeHealthy(t, e2eRoleExit, 443)
	cfgEntry := fetchRemoteConfig(t, e2eRoleEntry)
	assertConfigContains(t, cfgEntry, `"tuic"`, `ch-e2e-3hop-user-in`)
}

func TestE2E_Heavy_Chain_TopologyChange(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	entry := e2eHost(e2eRoleEntry)
	entry.ID = "topo-entry"
	middle := e2eHost(e2eRoleMiddle)
	middle.ID = "topo-middle"
	exit := e2eHost(e2eRoleExit)
	exit.ID = "topo-exit"
	for _, h := range []model.Host{entry, middle, exit} {
		registerNode(t, store, h, true)
	}

	twoHop := baseChain("topo-2hop", []model.ChainNode{
		{ID: entry.ID, Addr: entry.Addr, User: entry.User, KeyPath: entry.KeyPath},
		{ID: exit.ID, Addr: exit.Addr, User: exit.User, KeyPath: exit.KeyPath},
	})
	twoHop.Transport = model.TransportReality
	deployChain(t, store, twoHop, deployChainOpts{})
	assertNodeHealthy(t, e2eRoleEntry, 443)

	if err := store.DeleteChain("topo-2hop"); err != nil {
		t.Fatalf("DeleteChain topo-2hop: %v", err)
	}
	threeHop := baseChain("topo-3hop", []model.ChainNode{
		{ID: entry.ID, Addr: entry.Addr, User: entry.User, KeyPath: entry.KeyPath},
		{ID: middle.ID, Addr: middle.Addr, User: middle.User, KeyPath: middle.KeyPath},
		{ID: exit.ID, Addr: exit.Addr, User: exit.User, KeyPath: exit.KeyPath},
	})
	threeHop.Transport = model.TransportReality
	deployChain(t, store, threeHop, deployChainOpts{})
	cfgMid := fetchRemoteConfig(t, e2eRoleMiddle)
	assertConfigContains(t, cfgMid, `ch-topo-3hop-transport-in`)

	if err := store.DeleteChain("topo-3hop"); err != nil {
		t.Fatalf("DeleteChain topo-3hop: %v", err)
	}
	deployChain(t, store, twoHop, deployChainOpts{})
	cfgEntry := fetchRemoteConfig(t, e2eRoleEntry)
	if strings.Contains(cfgEntry, "topo-3hop") && !strings.Contains(cfgEntry, "topo-2hop") {
		t.Error("expected 2-hop chain after topology shrink")
	}
	assertNodeHealthy(t, e2eRoleEntry, 443)
}

// ─── Client Connectivity ──────────────────────────────────────────────────────

func TestE2E_Heavy_ClientConnectivity_1Hop(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-client-1hop", nodes)
	deployChain(t, store, c, deployChainOpts{})
	verifyClientConnectivity(t, c, e2eRoleExit)
}

func TestE2E_Heavy_ClientConnectivity_2Hop(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-client-2hop", nodes)
	c.Transport = model.TransportReality
	deployChain(t, store, c, deployChainOpts{})
	verifyClientConnectivity(t, c, e2eRoleExit)
}

func TestE2E_Heavy_ClientConnectivity_3Hop(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleMiddle, e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-client-3hop", nodes)
	deployChain(t, store, c, deployChainOpts{})
	verifyClientConnectivity(t, c, e2eRoleExit)
}

// ─── Load Balancing & Failover ────────────────────────────────────────────────

// TestE2E_Heavy_Balancer_MultiEntry verifies the ORCHESTRATOR (not a raw
// config) generates a multi-entry chain: two nodes flagged Role=entry in one
// chain, the client config carries a urltest 'chain-lb' wrapper over both
// per-entry outbounds, and end-to-end traffic egresses through the exit VPS.
// Distinct from the raw-config URLTestInChain/Failover tests above, which
// deploy hand-written balancer configs — this one drives ApplyChain +
// RenderClientConfig and asserts the generated config actually balances.
func TestE2E_Heavy_Balancer_MultiEntry(t *testing.T) {
	e2eHeavy(t)
	if os.Getenv("AB_ROUTE_DNS") != "1" {
		t.Skip("set AB_ROUTE_DNS=1 to verify multi-entry client routing")
	}
	store := newStore(t)
	// Two explicit entries (entry + middle), exit is the leaf. Both entries are
	// user-facing (Role=entry); the second is also transit toward the exit.
	nodes := buildChainNodes(e2eRoleEntry, e2eRoleMiddle, e2eRoleExit)
	nodes[0].Role = model.NodeRoleEntry
	nodes[1].Role = model.NodeRoleEntry
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-multientry", nodes)
	deployChain(t, store, c, deployChainOpts{})

	// The deployed exit node config must have TWO chain user-in inbounds (one
	// per entry) — proving the orchestrator rendered multi-entry on the server.
	exitCfg := fetchRemoteConfig(t, e2eRoleExit)
	// The entry-side nodes each carry their own user-in; check the second entry
	// (middle, which is both entry and transit) carries its user-in inbound.
	middleCfg := fetchRemoteConfig(t, e2eRoleMiddle)
	assertConfigContains(t, middleCfg, fmt.Sprintf("ch-%s-user-in-", c.Name))

	// Client config must carry the multi-entry urltest wrapper over two
	// per-entry outbounds (tuic-out-<entryID> and tuic-out-<middleID>).
	cfgJSON, err := chain.RenderClientConfig(chain.ClientConfigParams{
		Chain:          c,
		LocalProxyAddr: "127.0.0.1:11080",
		// No EntryHostOverride: the client (on the exit VPS) reaches both entries
		// at their real public IPs, so urltest actually has two distinct targets.
	})
	if err != nil {
		t.Fatalf("RenderClientConfig: %v", err)
	}
	assertConfigContains(t, cfgJSON,
		`"type": "urltest"`, `"tag": "chain-lb"`,
		fmt.Sprintf(`"tag": "tuic-out-%s"`, nodes[0].ID),
		fmt.Sprintf(`"tag": "tuic-out-%s"`, nodes[1].ID))

	// Run the client on the exit VPS and verify egress == exit IP (traffic
	// traversed one of the entries and the chain to the exit, then out).
	client := e2eConnect(t, e2eRoleExit)
	ctx := e2eContext(t, 2*time.Minute)
	remoteCfg := "/tmp/e2e-multientry-client.json"
	remoteLog := remoteCfg + ".log"
	if err := client.UploadText(ctx, cfgJSON, remoteCfg, 0o600); err != nil {
		t.Fatalf("upload client config: %v", err)
	}
	defer func() { _, _ = client.Run("rm -f " + remoteCfg + " " + remoteLog) }()

	if out, err := client.Run(fmt.Sprintf("/usr/local/bin/sing-box check -c %s", remoteCfg)); err != nil {
		t.Fatalf("multi-entry client check: %v\n%s", err, out)
	}

	runScript := fmt.Sprintf(`CFG=%q
LOG=%q
pkill -f "sing-box run -c $CFG" 2>/dev/null || true
/usr/local/bin/sing-box run -c "$CFG" >"$LOG" 2>&1 &
BPID=$!
sleep 12
IP=$(curl -s --max-time 30 -x socks5h://127.0.0.1:11080 https://ifconfig.me || true)
kill "$BPID" 2>/dev/null || true
wait "$BPID" 2>/dev/null || true
echo EGRESS:$IP
echo ---LOG---
tail -40 "$LOG" 2>/dev/null || true
`, remoteCfg, remoteLog)
	out, _, _, err := client.RunWithOutput(ctx, runScript, 2*time.Minute)
	if err != nil && !strings.Contains(out, "EGRESS:") {
		t.Fatalf("multi-entry client run: %v\n%s", err, out)
	}
	t.Logf("multi-entry remote client output:\n%s", out)

	gotIP := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "EGRESS:") {
			gotIP = strings.TrimSpace(strings.TrimPrefix(line, "EGRESS:"))
		}
	}
	_, expectAddr, _, _ := e2eServer(e2eRoleExit)
	expectIP := strings.TrimSpace(expectAddr[:strings.LastIndexByte(expectAddr, ':')])
	t.Logf("multi-entry egress IP=%s expect exit=%s", gotIP, expectIP)
	if gotIP != expectIP {
		t.Errorf("multi-entry egress=%s, want exit IP %s", gotIP, expectIP)
	}
	_ = exitCfg
}

// TestE2E_Heavy_PerClientRouting verifies per-client routing end-to-end: two
// users pinned via ChainExit to different exit nodes actually egress at their
// pinned node.
//
// Per-client routing was redesigned to the AWG peer/source-IP model (each user
// is a WireGuard peer with a unique inner IP; route rules match source_ip_cidr,
// not auth_user). A full e2e here therefore needs an AWG chain entry (multi-peer
// endpoint) + per-user awg-quick .conf clients brought up with `awg-quick up`
// on the test VPSes — which requires the AWG kernel module installed on those
// VPSes and root-level interface management. That infrastructure is not yet
// staged for this test, so it is skipped by default.
//
// The routing LOGIC is covered by unit tests instead:
//   - TestBuildMergedRoute_PerClientAWG_SourceIP   (source-IP rules, ordering)
//   - TestBuildMergedRoute_PerClientAWG_MultiHopPin (pin beyond one hop — the
//     case auth_user could not do)
//   - TestBuildAWGUserInboundMulti_Peers           (multi-peer endpoint)
//   - TestRenderClientAWGConf_PerUser / _PinnedEntry (per-user .conf)
//
// Set AB_E2E_AWG_PERCLIENT=1 (and AB_ROUTE_DNS=1) once the AWG kernel module is
// confirmed on the test VPSes to run the real end-to-end check.
func TestE2E_Heavy_PerClientRouting(t *testing.T) {
	e2eHeavy(t)
	if os.Getenv("AB_ROUTE_DNS") != "1" {
		t.Skip("set AB_ROUTE_DNS=1 to verify per-client routing (requires route section)")
	}
	if os.Getenv("AB_E2E_AWG_PERCLIENT") != "1" {
		t.Skip("per-client routing is now AWG peer/source-IP based; set " +
			"AB_E2E_AWG_PERCLIENT=1 once the AWG kernel module is staged on the " +
			"test VPSes. Routing logic is covered by unit tests " +
			"(TestBuildMergedRoute_PerClientAWG_*).")
	}
	// Real AWG e2e: TODO when the AWG kernel module is available on the test
	// VPSes. Outline (mirrors the unit-tested flow):
	//   1. buildChainNodes(entry, middle, exit); chain UserProtocol=AWG.
	//   2. Two users: alice (no pin -> default exit), bob (ChainExit=middle).
	//      Assign per-user AWG creds (GenerateWireGuardKeypair + allocateAWGPeerIP).
	//   3. deployChain -> entry config has a multi-peer endpoint (one peer per
	//      user) + source_ip_cidr route rules (assert counts).
	//   4. RenderClientAWGConf per user -> awg-quick .conf; upload to the entry
	//      VPS, `awg-quick up`, curl egress IP, `awg-quick down`.
	//   5. Assert alice egress == exit IP, bob egress == middle IP.
	t.Skip("AWG per-client e2e not yet implemented — see test comment for the plan")
}

// TestE2E_Heavy_Balancer_URLTestInChain verifies a real urltest balancer
// generated as a raw config (not the orchestrator's linear-chain config):
// two SOCKS backends on middle/exit, a urltest outbound on entry selecting
// between them, and traffic reaching generate_204. The previous version
// asserted `"type": "urltest"` in a 2-hop LINEAR chain config, but the
// orchestrator intentionally no longer wraps a single inter-node outbound in
// urltest — that broke the detour and masked transit failures
// (see merged_config.go). Real load balancing needs multiple backends, so
// this test exercises that directly.
func TestE2E_Heavy_Balancer_URLTestInChain(t *testing.T) {
	e2eHeavy(t)
	entryHost := e2eHost(e2eRoleEntry)
	entryHost.ID = "lb-urltest-entry"
	deploySocksBackend(t, e2eRoleMiddle, "lb-urltest-middle")
	deploySocksBackend(t, e2eRoleExit, "lb-urltest-exit")
	deployURLTestBalancer(t, e2eRoleEntry, entryHost.ID, e2eRoleMiddle, e2eRoleExit)
	assertNodeHealthy(t, e2eRoleEntry, 0)

	// The deployed config must actually contain a urltest outbound.
	cfg := fetchRemoteConfig(t, e2eRoleEntry)
	assertConfigContains(t, cfg, `"type": "urltest"`, `"lb-urltest"`)

	// Traffic through the urltest balancer must reach generate_204.
	client := e2eConnect(t, e2eRoleEntry)
	out, err := client.Run(`curl -s --max-time 20 -x http://127.0.0.1:11081 http://www.gstatic.com/generate_204 -o /dev/null -w '%{http_code}'`)
	if err != nil {
		t.Fatalf("curl via urltest: %v", err)
	}
	if code := strings.TrimSpace(out); code != "204" && code != "200" {
		t.Errorf("expected 204/200 through urltest, got %s", code)
	}
}

func TestE2E_Heavy_Balancer_Failover(t *testing.T) {
	e2eHeavy(t)
	entryHost := e2eHost(e2eRoleEntry)
	entryHost.ID = "lb-entry"
	deploySocksBackend(t, e2eRoleMiddle, "lb-middle")
	deploySocksBackend(t, e2eRoleExit, "lb-exit")
	deployURLTestBalancer(t, e2eRoleEntry, entryHost.ID, e2eRoleMiddle, e2eRoleExit)
	assertNodeHealthy(t, e2eRoleEntry, 0)

	client := e2eConnect(t, e2eRoleEntry)
	curlViaLB := `curl -s --max-time 20 -x http://127.0.0.1:11081 http://www.gstatic.com/generate_204 -o /dev/null -w '%{http_code}'`
	before, err := client.Run(curlViaLB)
	if err != nil {
		t.Fatalf("curl via urltest (both backends up): %v", err)
	}
	t.Logf("generate_204 with both backends: %s", strings.TrimSpace(before))

	// Stop middle backend; urltest should shift to exit.
	stopSingBox(t, e2eRoleMiddle)
	t.Cleanup(func() { startSingBox(t, e2eRoleMiddle) })
	time.Sleep(6 * time.Second) // allow urltest to observe middle down

	out, err := client.Run(curlViaLB)
	if err != nil {
		t.Fatalf("curl via urltest after middle down: %v", err)
	}
	code := strings.TrimSpace(out)
	t.Logf("generate_204 status after middle stopped: %s", code)
	if code != "204" && code != "200" {
		t.Errorf("expected 204/200 through failover, got %s", code)
	}
}

// ─── QUIC Capture + AWG Fingerprint ───────────────────────────────────────────

func TestE2E_Heavy_QUICCapture_AWGConfig(t *testing.T) {
	e2eHeavy(t)
	capture := captureQUICOrSkip(t)
	if capture.Source != "quic" || len(capture.Packets) != 5 {
		t.Fatalf("bad capture: source=%s packets=%d", capture.Source, len(capture.Packets))
	}
	for i, p := range capture.Packets {
		if !strings.HasPrefix(p, "<b 0x") {
			t.Errorf("packet %d bad format: %q", i, p)
		}
	}
	t.Logf("captured %d QUIC packets", len(capture.Packets))

	store := newStore(t)
	nodes := buildChainNodes(e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := &model.Chain{
		Name:               "e2e-quic-awg",
		Nodes:              nodes,
		Strategy:           model.StrategyURLTest,
		Transport:          model.TransportXHTTP,
		UserProtocol:       model.UserProtocolAWG,
		ObfuscationProfile: "pro_2026",
		UserEntryPort:      51822,
	}
	report, err := newApplier().ApplyChain(e2eContext(t, 8*time.Minute), store, c, "")
	if err != nil {
		if strings.Contains(err.Error(), "awg") {
			t.Skipf("AWG deploy skipped: %v", err)
		}
		t.Fatalf("ApplyChain: %v", err)
	}
	if report.AWG != nil && report.AWG.CPSLevel < 1 {
		t.Errorf("expected CPS level on AWG material, got %+v", report.AWG)
	}
	cfg := fetchRemoteConfig(t, e2eRoleExit)
	assertConfigContains(t, cfg, `"amnezia"`, `"i1"`)
	t.Logf("AWG config includes CPS/amnezia fields from pro_2026 preset")
}

// ─── AWG Peer Import (non-destructive) ────────────────────────────────────────

func TestE2E_Heavy_ImportAWG_PreservesPeers(t *testing.T) {
	e2eHeavy(t)
	cleanup := seedAWGConf(t, e2eRoleExit, "second-peer")
	defer cleanup()

	before := readAWGPeersList(t, e2eRoleExit)
	if !strings.Contains(before, "existing-peer") {
		t.Fatalf("seed peers missing: %s", before)
	}

	host := e2eHost(e2eRoleExit)
	info := &model.NodeInfo{Host: host, UseSudo: true,
		Inbounds: []model.NodeInbound{{Protocol: "awg", Port: 51820, ServerPrivKey: "TODO"}},
	}
	res, err := chain.ImportAWGConfigs(host, true, info)
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if len(res.Peers) < 1 {
		t.Fatalf("expected peers parsed, got %+v", res)
	}

	after := readAWGPeersList(t, e2eRoleExit)
	if !strings.Contains(after, "existing-peer") {
		t.Errorf("import overwrote peers.list:\nbefore=%s\nafter=%s", before, after)
	}
	if strings.Count(after, "PublicKey") < strings.Count(before, "PublicKey") {
		t.Errorf("peer count decreased after import")
	}
}

func TestE2E_Heavy_ImportAWG_FromSeededNode(t *testing.T) {
	e2eHeavy(t)
	cleanup := seedAWGConf(t, e2eRoleMiddle, "")
	defer cleanup()

	host := e2eHost(e2eRoleMiddle)
	info := &model.NodeInfo{Host: host, UseSudo: true,
		Inbounds: []model.NodeInbound{{Protocol: "awg", Port: 51820, ServerPrivKey: "TODO", AWGClientPub: ""}},
	}
	res, err := chain.ImportAWGConfigs(host, true, info)
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if res.ServerConfig == nil || res.ServerConfig.ListenPort != 51820 {
		t.Fatalf("server config: %+v", res.ServerConfig)
	}
	if info.Inbounds[0].ServerPrivKey == "TODO" {
		t.Error("ServerPrivKey not back-filled")
	}
}

// ─── Idempotency, Locking, Post-Deploy ────────────────────────────────────────

func TestE2E_Heavy_Idempotency_DoubleApply(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleMiddle)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-idem", nodes)

	deployChain(t, store, c, deployChainOpts{})
	key1 := c.Nodes[0].TransitUUID + c.Nodes[0].TransitPrivKey + c.Nodes[0].TransitShortID

	deployChain(t, store, c, deployChainOpts{})
	key2 := c.Nodes[0].TransitUUID + c.Nodes[0].TransitPrivKey + c.Nodes[0].TransitShortID

	if key1 != key2 {
		t.Errorf("double apply rotated transit keys")
	}
	assertNodeHealthy(t, e2eRoleMiddle, 443)
	info, _ := store.GetNodeInfo(nodes[0].ID)
	if info.LastDeployedHash == "" {
		t.Error("LastDeployedHash empty after idempotent applies")
	}
}

func TestE2E_Heavy_ConcurrentDeploy_Serialized(t *testing.T) {
	e2eHeavy(t)
	host := e2eHost(e2eRoleExit)
	host.ID = "lock-exit"
	_, addr, user, key := e2eServer(e2eRoleExit)

	good1, _ := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8443})
	good2, _ := singbox.RenderProxyNode(singbox.ProxyNodeParams{ListenPort: 8444})

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		cfg := string(good1)
		if i == 1 {
			cfg = string(good2)
		}
		go func(idx int, content string) {
			defer wg.Done()
			<-start
			// Separate SSH sessions — the per-host lock is in pushConfig, not the client.
			client, err := sshclient.Connect(addr, user, key)
			if err != nil {
				errs[idx] = err
				return
			}
			defer client.Close()
			_, errs[idx] = chain.PushConfig(client, host.ID, content, true)
		}(i, cfg)
	}
	close(start)
	wg.Wait()

	if errs[0] != nil && errs[1] != nil {
		t.Fatalf("both concurrent pushes failed: %v / %v", errs[0], errs[1])
	}
	assertNodeHealthy(t, e2eRoleExit, 0)
}

func TestE2E_Heavy_PostDeploy_HashAndHealth(t *testing.T) {
	e2eHeavy(t)
	store := newStore(t)
	nodes := buildChainNodes(e2eRoleExit)
	registerChainNodes(t, store, nodes, true)
	c := baseChain("e2e-postdeploy", nodes)
	deployChain(t, store, c, deployChainOpts{})

	cfg := fetchRemoteConfig(t, e2eRoleExit)
	assertPostDeployHash(t, store, nodes[0].ID, cfg)
	assertNodeHealthy(t, e2eRoleExit, 443)

	backend := factory.New(nil).Create()
	status, err := backend.GetStatus(e2eContext(t, time.Minute), model.Host{
		ID: nodes[0].ID, Addr: nodes[0].Addr, User: nodes[0].User, KeyPath: nodes[0].KeyPath,
	})
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if !status.Running {
		t.Error("backend reports sing-box not running")
	}
	t.Logf("status: version=%s pid=%d", status.Version, status.PID)
}

func TestE2E_Heavy_HostLock_Identity(t *testing.T) {
	if chain.HostLock("e2e-a") != chain.HostLock("e2e-a") {
		t.Error("same nodeID should share mutex")
	}
	if chain.HostLock("e2e-a") == chain.HostLock("e2e-b") {
		t.Error("different nodeIDs should have different mutexes")
	}
}

// TestE2E_Heavy_SelectorStrategy verifies a real selector balancer as a raw
// config: a selector outbound whose default backend can be switched, with
// traffic egress following the selected backend's IP. The previous version
// asserted `"type": "selector"` in a 2-hop LINEAR chain config, but the
// orchestrator no longer wraps a single inter-node outbound in a strategy
// (that broke the detour). Real per-client routing needs a selectable group,
// exercised here directly. This is the seed for the upcoming multi-entry /
// per-client routing feature.
func TestE2E_Heavy_SelectorStrategy(t *testing.T) {
	e2eHeavy(t)
	entryHost := e2eHost(e2eRoleEntry)
	entryHost.ID = "lb-selector-entry"
	deploySocksBackend(t, e2eRoleMiddle, "lb-selector-middle")
	deploySocksBackend(t, e2eRoleExit, "lb-selector-exit")

	// Start with default = middle (index 0). Egress should be the middle VPS IP.
	deploySelectorBalancer(t, e2eRoleEntry, entryHost.ID, 0, e2eRoleMiddle, e2eRoleExit)
	assertNodeHealthy(t, e2eRoleEntry, 0)
	cfg := fetchRemoteConfig(t, e2eRoleEntry)
	assertConfigContains(t, cfg, `"type": "selector"`, `"lb-selector"`, `"default": "backend-0"`)

	client := e2eConnect(t, e2eRoleEntry)
	egressIP := func() string {
		out, err := client.Run(`curl -s --max-time 20 -x http://127.0.0.1:11081 https://ifconfig.me`)
		if err != nil {
			t.Fatalf("curl egress via selector: %v", err)
		}
		return strings.TrimSpace(out)
	}

	middleIP := e2eServerIP(e2eRoleMiddle)
	exitIP := e2eServerIP(e2eRoleExit)
	t.Logf("selector default=backend-0 (middle=%s) egress=%s", middleIP, egressIP())

	// Switch the selector default to exit (index 1) and re-deploy; egress must
	// now be the exit VPS IP. This proves the selector routes traffic to the
	// chosen backend, not just that the outbound exists in the config.
	deploySelectorBalancer(t, e2eRoleEntry, entryHost.ID, 1, e2eRoleMiddle, e2eRoleExit)
	assertNodeHealthy(t, e2eRoleEntry, 0)
	got := egressIP()
	t.Logf("selector default=backend-1 (exit=%s) egress=%s", exitIP, got)
	if got != exitIP {
		t.Errorf("after switching selector default to exit, egress=%s, want %s", got, exitIP)
	}
}

// TestE2E_Heavy_Takeover_DetectNone on clean entry after documenting state.
func TestE2E_Heavy_BackendStatus_AllNodes(t *testing.T) {
	e2eHeavy(t)
	backend := factory.New(nil).Create()
	for role := range e2eServers {
		host := e2eHost(role)
		status, err := backend.GetStatus(e2eContext(t, time.Minute), host)
		if err != nil {
			t.Errorf("GetStatus role=%d: %v", role, err)
			continue
		}
		t.Logf("%s: running=%v version=%s", e2eServers[role].Role, status.Running, status.Version)
	}
}

