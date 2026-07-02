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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
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

// genAWGKeypair returns a real (priv, pub) WireGuard keypair for test nodes.
func genAWGKeypair(t *testing.T) (string, string) {
	t.Helper()
	priv, pub, err := GenerateWireGuardKeypair()
	if err != nil {
		t.Fatalf("GenerateWireGuardKeypair: %v", err)
	}
	return priv, pub
}

// awgTransportChain builds a 3-node chain (entry -> middle -> exit) with AWG
// inter-node transport and real per-link WireGuard keys on each ChainNode, as
// ApplyChain would persist them. Each transit node gets a server keypair; each
// node with an outbound gets a client keypair + a 10.9.0.X/32 inner IP.
func awgTransportChain(t *testing.T) *model.Chain {
	t.Helper()
	entryClientPriv, _ := genAWGKeypair(t) // entry's client key (outbound to middle)
	_, entryClientPub := deriveClientPub(entryClientPriv, t)
	middleServerPriv, middleServerPub := genAWGKeypair(t)
	middleClientPriv, _ := genAWGKeypair(t) // middle's client key (outbound to exit)
	_, middleClientPub := deriveClientPub(middleClientPriv, t)
	exitServerPriv, exitServerPub := genAWGKeypair(t)
	return &model.Chain{
		Name:         "awg-transport",
		UserProtocol: model.UserProtocolVLESSReality, // user entry is NOT awg — proves transport is independent
		Transport:    model.TransportAWG,
		Nodes: []model.ChainNode{
			{
				ID: "entry", Addr: "entry.example.test:22", Role: model.NodeRoleEntry, Port: 443,
				TransitAWGClientPriv: entryClientPriv, TransitAWGClientPub: entryClientPub,
				TransitAWGAddress: "10.9.0.2/32",
			},
			{
				ID: "middle", Addr: "middle.example.test:22", Role: model.NodeRoleTransit, Port: 443,
				TransitAWGServerPriv: middleServerPriv, TransitAWGServerPub: middleServerPub,
				TransitAWGClientPriv: middleClientPriv, TransitAWGClientPub: middleClientPub,
				TransitAWGAddress: "10.9.0.3/32",
			},
			{
				ID: "exit", Addr: "exit.example.test:22", Port: 443,
				TransitAWGServerPriv: exitServerPriv, TransitAWGServerPub: exitServerPub,
			},
		},
	}
}

// deriveClientPub derives the public key from a private one (so the persisted
// pub matches the priv, as ApplyChain's GenerateWireGuardKeypair does).
func deriveClientPub(priv string, t *testing.T) (string, string) {
	t.Helper()
	pub, err := deriveWireGuardPublicFromPrivate(priv)
	if err != nil {
		t.Fatalf("deriveWireGuardPublicFromPrivate: %v", err)
	}
	return priv, pub
}

// TestAWGTransport_MergedConfig_SingBoxCheck renders a 3-node AWG-transport
// chain for the ENTRY node (which has the AWG outbound to middle + a VLESS
// user entry inbound) and runs sing-box check. Proves the inter-node AWG link
// (wireguard outbound + the chain assembling correctly) is a valid config.
func TestAWGTransport_MergedConfig_SingBoxCheck(t *testing.T) {
	c := awgTransportChain(t)
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "entry"}}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, nil)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The entry config must contain a wireguard OUTBOUND (the AWG transport
	// link to middle), not a VLESS Reality outbound.
	if !strings.Contains(string(cfgJSON), `"wireguard"`) {
		t.Fatalf("AWG-transport entry config has no wireguard outbound:\n%s", cfgJSON)
	}
	// Must NOT silently fall back to Reality for the transport outbound.
	if strings.Contains(string(cfgJSON), `"ch-awg-transport-out-`) && strings.Contains(string(cfgJSON), `"vless"`) {
		// vless is allowed for the user-entry inbound, but the transport-out
		// tag must be the awg one. Verify the awg outbound tag exists.
		if !strings.Contains(string(cfgJSON), `"ch-awg-transport-out-awg-middle"`) {
			t.Errorf("AWG transport outbound tag missing (silent Reality fallback?), config:\n%s", cfgJSON)
		}
	}
	singBoxCheck(t, cfgJSON)
}

// TestAWGTransport_MiddleNode_HasTransitEndpoint renders the MIDDLE node of an
// AWG-transport chain and verifies it has a wireguard ENDPOINT (the transit
// inbound listening for the entry's client) tagged ch-<chain>-transport-in,
// plus an AWG outbound to exit. Runs sing-box check.
func TestAWGTransport_MiddleNode_HasTransitEndpoint(t *testing.T) {
	c := awgTransportChain(t)
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "middle"}}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, nil)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Middle must carry the transport-in endpoint (wireguard) and the awg
	// outbound to exit.
	if !strings.Contains(string(cfgJSON), `"ch-awg-transport-transport-in"`) {
		t.Errorf("middle config missing transport-in tag, config:\n%s", cfgJSON)
	}
	if !strings.Contains(string(cfgJSON), `"ch-awg-transport-out-awg-exit"`) {
		t.Errorf("middle config missing awg outbound to exit, config:\n%s", cfgJSON)
	}
	singBoxCheck(t, cfgJSON)
}

// TestAWGTransport_NotRealityFallback asserts that selecting Transport=awg does
// NOT produce a VLESS Reality transport inbound (the pre-fix silent fallback).
// The transport-in tag must be carried by a wireguard endpoint, not a vless
// inbound with a reality block.
func TestAWGTransport_NotRealityFallback(t *testing.T) {
	c := awgTransportChain(t)
	nodeInfo := &model.NodeInfo{Host: model.Host{ID: "middle"}}
	cfg, _, err := buildMergedNodeConfig(nodeInfo, []*model.Chain{c}, nil)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	// Parse endpoints + inbounds to verify transport-in is a wireguard endpoint.
	var parsed struct {
		Endpoints []map[string]any `json:"endpoints"`
		Inbounds  []map[string]any `json:"inbounds"`
	}
	if err := json.Unmarshal(cfgJSON, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	foundWG := false
	for _, ep := range parsed.Endpoints {
		if ep["tag"] == "ch-awg-transport-transport-in" {
			if ep["type"] != "wireguard" {
				t.Errorf("transport-in endpoint type=%v, want wireguard", ep["type"])
			}
			foundWG = true
		}
	}
	if !foundWG {
		t.Errorf("no wireguard endpoint carrying the transport-in tag; endpoints=%v", parsed.Endpoints)
	}
	// And no vless inbound should carry the transport-in tag (the old fallback).
	for _, inb := range parsed.Inbounds {
		if inb["tag"] == "ch-awg-transport-transport-in" {
			t.Errorf("transport-in tag found on an inbound (Reality fallback): %v", inb)
		}
	}
}

// TestAWGTransport_Builders_Fields verifies buildAWGTransportInbound and
// buildAWGTransportOutbound produce the right WireGuard fields from ChainNode
// transit keys: server priv, peer pub = previous client pub, allowed_ips =
// previous client address; outbound server/port/local_addresses/peer pub.
func TestAWGTransport_Builders_Fields(t *testing.T) {
	preset := GetDefaultPreset()
	entryClientPriv, _ := genAWGKeypair(t)
	_, entryClientPub := deriveClientPub(entryClientPriv, t)
	middleServerPriv, middleServerPub := genAWGKeypair(t)
	entry := model.ChainNode{
		ID: "entry", Port: 443,
		TransitAWGClientPriv: entryClientPriv, TransitAWGClientPub: entryClientPub,
		TransitAWGAddress: "10.9.0.2/32",
	}
	middle := model.ChainNode{
		ID: "middle", Port: 443,
		TransitAWGServerPriv: middleServerPriv, TransitAWGServerPub: middleServerPub,
	}

	// Inbound on middle, peer = entry's client.
	inbJSON := buildAWGTransportInbound(&middle, &entry, "ch-t-in", &preset)
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(inbJSON, &ep); err != nil {
		t.Fatalf("unmarshal inbound: %v\n%s", err, inbJSON)
	}
	if ep.PrivateKey != middleServerPriv {
		t.Errorf("inbound private_key=%s, want middle server priv", ep.PrivateKey)
	}
	if ep.ListenPort != 443 {
		t.Errorf("inbound listen_port=%d, want 443", ep.ListenPort)
	}
	if len(ep.Peers) != 1 || ep.Peers[0].PublicKey != entryClientPub {
		t.Errorf("inbound peer pub=%v, want entry client pub %s", ep.Peers, entryClientPub)
	}
	if len(ep.Peers[0].AllowedIPs) != 1 || ep.Peers[0].AllowedIPs[0] != "10.9.0.2/32" {
		t.Errorf("inbound peer allowed_ips=%v, want [10.9.0.2/32]", ep.Peers[0].AllowedIPs)
	}
	if ep.System {
		t.Error("inbound system=true, want false (userspace)")
	}

	// Outbound (client endpoint) on entry, dialing middle. sing-box-extended
	// 1.13 has no wireguard outbound, so the client side is a WireGuard endpoint
	// with a peer that dials the next node.
	outJSON, err := buildAWGTransportOutbound(&entry, &middle, "middle.example.test", "ch-t-out", &preset)
	if err != nil {
		t.Fatalf("buildAWGTransportOutbound: %v", err)
	}
	var out config.WireGuardEndpoint
	if err := json.Unmarshal(outJSON, &out); err != nil {
		t.Fatalf("unmarshal outbound: %v\n%s", err, outJSON)
	}
	if out.PrivateKey != entryClientPriv {
		t.Errorf("outbound private_key mismatch")
	}
	if len(out.Address) != 1 || out.Address[0] != "10.9.0.2/32" {
		t.Errorf("outbound address=%v, want [10.9.0.2/32]", out.Address)
	}
	if len(out.Peers) != 1 {
		t.Fatalf("outbound want 1 peer, got %d", len(out.Peers))
	}
	p := out.Peers[0]
	if p.PublicKey != middleServerPub {
		t.Errorf("outbound peer public_key=%s, want middle server pub %s", p.PublicKey, middleServerPub)
	}
	if p.Address != "middle.example.test" || p.Port != 443 {
		t.Errorf("outbound peer address=%s port=%d, want middle.example.test:443", p.Address, p.Port)
	}
	if out.System {
		t.Error("outbound system=true, want false (userspace)")
	}
}

// TestAllocateAWGTransitIP verifies deterministic first-free allocation in the
// 10.9.0.0/24 inter-node subnet (separate from the user-entry 10.8.0.0/24).
func TestAllocateAWGTransitIP(t *testing.T) {
	if got := allocateAWGTransitIP(nil); got != "10.9.0.2/32" {
		t.Errorf("empty taken: got %s, want 10.9.0.2/32", got)
	}
	got := allocateAWGTransitIP([]string{"10.9.0.2/32", "10.9.0.3/32"})
	if got != "10.9.0.4/32" {
		t.Errorf("after 2,3: got %s, want 10.9.0.4/32", got)
	}
	// normalization: bare IP collides with /32 form
	got = allocateAWGTransitIP([]string{"10.9.0.2", "10.9.0.3/32"})
	if got != "10.9.0.4/32" {
		t.Errorf("normalized collision: got %s, want 10.9.0.4/32", got)
	}
}

// TestAWGTransport_OutboundTagMatchesRoute verifies the outbound tag emitted by
// buildChainRoleInOut matches what chainInterNodeOutboundTag returns (route
// rules reference the latter to steer traffic). A mismatch would break routing.
func TestAWGTransport_OutboundTagMatchesRoute(t *testing.T) {
	c := awgTransportChain(t)
	roles := resolveChainRoles("entry", []*model.Chain{c})
	if len(roles) != 1 {
		t.Fatalf("want 1 role, got %d", len(roles))
	}
	// Render entry config and extract the actual outbound tag.
	cfg, _, err := buildMergedNodeConfig(&model.NodeInfo{Host: model.Host{ID: "entry"}}, []*model.Chain{c}, nil)
	if err != nil {
		t.Fatalf("buildMergedNodeConfig: %v", err)
	}
	cfgJSON, _ := json.MarshalIndent(cfg, "", "  ")
	wantTag := chainInterNodeOutboundTag(&roles[0])
	if !strings.Contains(string(cfgJSON), fmt.Sprintf(`"tag": %q`, wantTag)) {
		t.Errorf("config missing outbound tag %q (route rules would miss it)\n%s", wantTag, cfgJSON)
	}
}