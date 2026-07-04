package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// TestRenderServerAWGConf_PlainWireGuard renders a server .conf with no
// amnezia obfuscation and verifies the [Interface]/[Peer] shape matches
// awg-quick expectations.
func TestRenderServerAWGConf_PlainWireGuard(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		TunnelAddress:    "10.8.0.1/24",
		Peers: []AWGServerPeer{
			{PublicKey: "PUB1", AllowedIPs: "10.8.0.2/32"},
			{PublicKey: "PUB2", AllowedIPs: "10.8.0.3/32"},
		},
	})
	if !strings.Contains(out, "[Interface]") {
		t.Fatal("missing [Interface]")
	}
	if !strings.Contains(out, "PrivateKey = SERVER_PRIV") {
		t.Error("missing server PrivateKey")
	}
	if !strings.Contains(out, "ListenPort = 51820") {
		t.Error("missing ListenPort")
	}
	if !strings.Contains(out, "Address = 10.8.0.1/24") {
		t.Error("missing server Address")
	}
	if !strings.Contains(out, "MTU = 1420") {
		t.Error("default MTU should be 1420")
	}
	peers := strings.Count(out, "[Peer]")
	if peers != 2 {
		t.Errorf("want 2 [Peer] sections, got %d", peers)
	}
	if !strings.Contains(out, "PublicKey = PUB1") || !strings.Contains(out, "PublicKey = PUB2") {
		t.Error("missing peer public keys")
	}
	if !strings.Contains(out, "AllowedIPs = 10.8.0.2/32") {
		t.Error("missing peer 1 AllowedIPs")
	}
	// No amnezia block → no Jc line.
	if strings.Contains(out, "Jc =") {
		t.Error("plain WG conf must not contain amnezia Jc")
	}
}

// TestRenderServerAWGConf_AmneziaBeforePeer verifies the critical format
// invariant: amnezia fields (Jc/Jmin/Jmax/S1-S4/H1-H4/I1-I5) sit INSIDE
// [Interface] BEFORE the first [Peer]. awg-quick passes the stripped .conf to
// `awg setconf`, which rejects amnezia fields after [Peer] with
// "Line unrecognized: Jc=...". This is the handshake-breaking bug fixed in
// commit 6f1a108's aftermath.
func TestRenderServerAWGConf_AmneziaBeforePeer(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		TunnelAddress:    "10.8.0.1/24",
		Amnezia: &config.AmneziaOptions{
			JC: 4, JMIN: 212, JMAX: 837,
			S1: 118, S2: 114, S3: 54, S4: 21,
			H1: "143219817-450506440", H2: "545807649-1006572806",
			H3: "1094138953-1444146798", H4: "1766806575-2013246654",
			I1: "<b 0x01>", I2: "<b 0x02>", I3: "<b 0x03>",
			I4: "<b 0x04>", I5: "<b 0x05>",
		},
		Peers: []AWGServerPeer{{PublicKey: "PUB1", AllowedIPs: "10.8.0.2/32"}},
	})
	peerIdx := strings.Index(out, "[Peer]")
	jcIdx := strings.Index(out, "Jc = 4")
	if jcIdx < 0 {
		t.Fatal("missing Jc line — amnezia block not emitted")
	}
	if peerIdx < 0 {
		t.Fatal("missing [Peer]")
	}
	if jcIdx > peerIdx {
		t.Errorf("amnezia Jc (idx %d) must come BEFORE [Peer] (idx %d) — awg setconf rejects amnezia after [Peer]", jcIdx, peerIdx)
	}
	for _, line := range []string{"Jmin = 212", "Jmax = 837", "S1 = 118", "S4 = 21", "H1 = 143219817-450506440", "I1 = <b 0x01>", "I5 = <b 0x05>"} {
		if !strings.Contains(out, line) {
			t.Errorf("missing amnezia line %q", line)
		}
	}
}

// TestRenderServerAWGConf_NoItime verifies Itime is NEVER written to the
// server .conf. awg setconf rejects "Itime" and sing-box UAPI rejects "itime"
// (runtime-breaking — AGENTS.md Known Issues, commit 6f1a108). Even if the
// AmneziaOptions carries ITime, the .conf must omit it.
func TestRenderServerAWGConf_NoItime(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		Amnezia: &config.AmneziaOptions{
			JC: 4, JMIN: 212, JMAX: 837, S1: 1, S2: 2, S3: 3, S4: 4,
			H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
			I1: "<b 0x01>", I2: "<b 0x02>", I3: "<b 0x03>", I4: "<b 0x04>", I5: "<b 0x05>",
			ITime: 50,
		},
		Peers: []AWGServerPeer{{PublicKey: "PUB1", AllowedIPs: "10.8.0.2/32"}},
	})
	if strings.Contains(out, "Itime") || strings.Contains(out, "itime") {
		t.Errorf("server .conf must NOT contain Itime (runtime-breaking): %s", out)
	}
}

// TestRenderServerAWGConf_SkipsUncredentialedPeer verifies a peer with an
// empty PublicKey is skipped rather than emitting a broken [Peer] section
// (an empty PublicKey would make awg setconf reject the whole .conf).
func TestRenderServerAWGConf_SkipsUncredentialedPeer(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		Peers: []AWGServerPeer{
			{PublicKey: "", AllowedIPs: "10.8.0.9/32"},
			{PublicKey: "PUB1", AllowedIPs: "10.8.0.2/32"},
		},
	})
	if strings.Count(out, "[Peer]") != 1 {
		t.Errorf("want 1 [Peer] (empty pubkey skipped), got %d", strings.Count(out, "[Peer]"))
	}
	if !strings.Contains(out, "PublicKey = PUB1") {
		t.Error("credentialed peer must be present")
	}
}

// TestRenderServerAWGConf_Defaults verifies address/MTU defaults apply when
// empty/zero, so a minimal params struct still produces a valid .conf.
func TestRenderServerAWGConf_Defaults(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		Peers:            []AWGServerPeer{{PublicKey: "PUB1"}},
	})
	if !strings.Contains(out, "Address = 10.8.0.1/24") {
		t.Error("default server address should be 10.8.0.1/24")
	}
	if !strings.Contains(out, "MTU = 1420") {
		t.Error("default MTU should be 1420")
	}
	if !strings.Contains(out, "AllowedIPs = 10.8.0.2/32") {
		t.Error("default peer AllowedIPs should be 10.8.0.2/32")
	}
	// No ListenPort line when port is zero (awg-quick picks ephemeral).
	if strings.Contains(out, "ListenPort = 0") {
		t.Error("zero ListenPort should be omitted, not written as 0")
	}
}

// TestRenderServerAWGConf_PostUpPostDown verifies that when TUNInterface is set
// (the kernel-AWG + sing-box-TUN-overlay architecture), PostUp/PostDown lines
// are emitted with iptables FORWARD rules between awg0 and the TUN. Without
// these the kernel's policy routing routes awg0→TUN but the FORWARD chain drops
// return traffic — egress through the tunnel silently fails (verified on the
// dns.idoctor.mom reference which has exactly these rules). PostUp/PostDown
// must sit in [Interface] before [Peer] (amnezia too).
func TestRenderServerAWGConf_PostUpPostDown(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		TUNInterface:     "sing-box-tun",
		Peers:            []AWGServerPeer{{PublicKey: "PUB1", AllowedIPs: "10.8.0.2/32"}},
	})
	if !strings.Contains(out, "PostUp = ") {
		t.Error("PostUp line missing when TUNInterface is set")
	}
	if !strings.Contains(out, "PostDown = ") {
		t.Error("PostDown line missing when TUNInterface is set")
	}
	// The FORWARD rules must reference both awg0 and the TUN interface.
	if !strings.Contains(out, "-i awg0 -o sing-box-tun -j ACCEPT") {
		t.Error("PostUp missing FORWARD awg0→sing-box-tun ACCEPT rule")
	}
	if !strings.Contains(out, "-i sing-box-tun -o awg0 -j ACCEPT") {
		t.Error("PostUp missing FORWARD sing-box-tun→awg0 ACCEPT rule")
	}
	// PostUp sets ip_forward=1 (belt-and-braces).
	if !strings.Contains(out, "ip_forward") {
		t.Error("PostUp should set ip_forward=1")
	}
	// PostUp/PostDown must sit BEFORE [Peer] (awg-quick passes [Interface]
	// fields to awg setconf which parses PostUp there; after [Peer] it fails).
	peerIdx := strings.Index(out, "[Peer]")
	postUpIdx := strings.Index(out, "PostUp = ")
	if peerIdx < 0 {
		t.Fatal("missing [Peer]")
	}
	if postUpIdx < 0 {
		t.Fatal("missing PostUp")
	}
	if postUpIdx > peerIdx {
		t.Errorf("PostUp (idx %d) must come BEFORE [Peer] (idx %d)", postUpIdx, peerIdx)
	}
}

// TestRenderServerAWGConf_NoPostUpWhenNoTUN verifies PostUp/PostDown are
// omitted when TUNInterface is empty (non-overlay use — plain AWG without
// sing-box, e.g. a standalone AWG node not yet wired to a TUN overlay).
func TestRenderServerAWGConf_NoPostUpWhenNoTUN(t *testing.T) {
	out := RenderServerAWGConf(AWGServerConfParams{
		ServerPrivateKey: "SERVER_PRIV",
		ListenPort:       51820,
		// TUNInterface empty
	})
	if strings.Contains(out, "PostUp") || strings.Contains(out, "PostDown") {
		t.Errorf("PostUp/PostDown must be omitted when TUNInterface is empty:\n%s", out)
	}
}

// ─── Exit tunnels (multi-exit balancer) ────────────────────────────────────

// TestRenderExitAWGConf verifies a balancer-side awg-exit-nX.conf: the client
// tunnel to one remote exit server. [Interface] carries the balancer's
// private key + inner IP + listen port + MTU 1280; [Peer] carries the exit
// server's public key + endpoint + AllowedIPs 0.0.0.0/0 + keepalive.
func TestRenderExitAWGConf(t *testing.T) {
	out := RenderExitAWGConf(ExitClientConfParams{
		InterfaceName:    "awg-exit-n1",
		ClientPrivateKey: "BAL_CLIENT_PRIV",
		ClientAddress:    "10.10.0.2/32",
		ClientListenPort: 51901,
		ExitPublicKey:    "EXIT_PUB",
		ExitEndpoint:     "144.31.224.212:52000",
	})
	if !strings.Contains(out, "Address = 10.10.0.2/32") {
		t.Error("missing client Address")
	}
	if !strings.Contains(out, "PrivateKey = BAL_CLIENT_PRIV") {
		t.Error("missing client PrivateKey")
	}
	if !strings.Contains(out, "ListenPort = 51901") {
		t.Error("missing fixed client ListenPort (NAT stability)")
	}
	if !strings.Contains(out, "MTU = 1280") {
		t.Error("exit link MTU should default to 1280")
	}
	if !strings.Contains(out, "PublicKey = EXIT_PUB") {
		t.Error("missing exit PublicKey")
	}
	if !strings.Contains(out, "Endpoint = 144.31.224.212:52000") {
		t.Error("missing exit Endpoint")
	}
	if !strings.Contains(out, "AllowedIPs = 0.0.0.0/0, ::/0") {
		t.Error("missing AllowedIPs 0.0.0.0/0")
	}
	if !strings.Contains(out, "PersistentKeepalive = 25") {
		t.Error("missing PersistentKeepalive (NAT'd VPS keepalive)")
	}
	// No amnezia by default on service tunnels.
	if strings.Contains(out, "Jc =") {
		t.Error("exit link must NOT carry amnezia by default (service tunnel between trusted servers)")
	}
	// amnezia, if any, must sit in [Interface] BEFORE [Peer] (same invariant as
	// RenderServerAWGConf — awg setconf rejects it after [Peer]).
	peerIdx := strings.Index(out, "[Peer]")
	if peerIdx < 0 {
		t.Fatal("missing [Peer]")
	}
}

// TestRenderExitAWGConf_AmneziaBeforePeer verifies that when amnezia IS enabled
// on an exit link, it lands in [Interface] before [Peer] (awg setconf rejects
// amnezia fields after [Peer]).
func TestRenderExitAWGConf_AmneziaBeforePeer(t *testing.T) {
	out := RenderExitAWGConf(ExitClientConfParams{
		ClientPrivateKey: "BAL_CLIENT_PRIV",
		ClientAddress:    "10.10.0.2/32",
		ExitPublicKey:    "EXIT_PUB",
		ExitEndpoint:     "1.2.3.4:52000",
		Amnezia: &config.AmneziaOptions{
			JC: 4, JMIN: 212, JMAX: 837, S1: 1, S2: 2, S3: 3, S4: 4,
			H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
		},
	})
	peerIdx := strings.Index(out, "[Peer]")
	jcIdx := strings.Index(out, "Jc = 4")
	if jcIdx < 0 {
		t.Fatal("amnezia Jc not emitted")
	}
	if peerIdx < 0 {
		t.Fatal("missing [Peer]")
	}
	if jcIdx > peerIdx {
		t.Errorf("amnezia Jc (idx %d) must come BEFORE [Peer] (idx %d)", jcIdx, peerIdx)
	}
	if strings.Contains(out, "Itime") {
		t.Error("Itime must never be written (runtime-breaking)")
	}
}

// TestRenderExitServerAWGConf verifies a Role=exit node's awg0.conf: the server
// end of an exit link, accepting the balancer's tunnel as its single peer.
func TestRenderExitServerAWGConf(t *testing.T) {
	out := RenderExitServerAWGConf(ExitServerConfParams{
		ServerPrivateKey:   "EXIT_SERVER_PRIV",
		ListenPort:         52000,
		TunnelAddress:      "10.11.0.1/24",
		BalancerPublicKey:  "BAL_CLIENT_PUB",
		BalancerAllowedIPs: "10.10.0.2/32",
	})
	if !strings.Contains(out, "Address = 10.11.0.1/24") {
		t.Error("missing exit server Address")
	}
	if !strings.Contains(out, "PrivateKey = EXIT_SERVER_PRIV") {
		t.Error("missing exit server PrivateKey")
	}
	if !strings.Contains(out, "ListenPort = 52000") {
		t.Error("missing exit server ListenPort")
	}
	if !strings.Contains(out, "MTU = 1420") {
		t.Error("exit server MTU should default to 1420")
	}
	if strings.Count(out, "[Peer]") != 1 {
		t.Errorf("exit server should have exactly 1 [Peer] (the balancer), got %d", strings.Count(out, "[Peer]"))
	}
	if !strings.Contains(out, "PublicKey = BAL_CLIENT_PUB") {
		t.Error("missing balancer PublicKey in [Peer]")
	}
	if !strings.Contains(out, "AllowedIPs = 10.10.0.2/32") {
		t.Error("missing balancer AllowedIPs")
	}
}

// TestRenderExitServerAWGConf_NoPeerWhenBalancerPubEmpty verifies a missing
// balancer public key omits the [Peer] section (a valid .conf the exit can
// boot standalone, ready to accept a peer once the balancer's key is known).
func TestRenderExitServerAWGConf_NoPeerWhenBalancerPubEmpty(t *testing.T) {
	out := RenderExitServerAWGConf(ExitServerConfParams{
		ServerPrivateKey: "EXIT_SERVER_PRIV",
		ListenPort:       52000,
	})
	if strings.Contains(out, "[Peer]") {
		t.Errorf("exit server with empty balancer pub must NOT emit [Peer]:\n%s", out)
	}
}

// TestSplitHostPort verifies the endpoint splitter used by RenderExitAWGConf
// tolerates bare hosts, host:port, and bracketed IPv6.
func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
	}{
		{"1.2.3.4:52000", "1.2.3.4", 52000},
		{"example.com:443", "example.com", 443},
		{"bare.host", "bare.host", 0},
		{"[::1]:51820", "::1", 51820},
	}
	for _, c := range cases {
		gotHost, gotPort := splitHostPort(c.in)
		if gotHost != c.wantHost || gotPort != c.wantPort {
			t.Errorf("splitHostPort(%q) = (%q, %d), want (%q, %d)", c.in, gotHost, gotPort, c.wantHost, c.wantPort)
		}
	}
}
