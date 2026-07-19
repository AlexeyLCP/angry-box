package chain

// awg_peersync_test.go — live peer sync (LucX SyncPeers port): restart is
// skipped when only the peer set changes; interface changes still restart.

import (
	"context"
	"strings"
	"testing"
)

const peersyncServerConf = `[Interface]
Address = 10.8.0.1/24
ListenPort = 51820
PrivateKey = SRVPRIV
MTU = 1420
Jc = 120
Jmin = 50
Jmax = 1000
S1 = 115
S2 = 45
S3 = 22
S4 = 12
H1 = 1984-1984
H2 = 2048-2048
H3 = 4096-4096
H4 = 8192-8192
PostUp = echo 1 > /proc/sys/net/ipv4/ip_forward

[Peer]
PublicKey = PUB_ALICE
AllowedIPs = 10.8.0.2/32
`

func TestSplitAWGConf(t *testing.T) {
	iface, peers := splitAWGConf(peersyncServerConf)
	if len(peers) != 1 || peers[0].PublicKey != "PUB_ALICE" || peers[0].AllowedIPs != "10.8.0.2/32" {
		t.Fatalf("bad peers: %+v", peers)
	}
	for _, want := range []string{"ListenPort = 51820", "H1 = 1984-1984", "PostUp = echo 1"} {
		if !strings.Contains(iface, want) {
			t.Errorf("iface section missing %q:\n%s", want, iface)
		}
	}
	if strings.Contains(iface, "[Peer]") || strings.Contains(iface, "PUB_ALICE") {
		t.Errorf("iface section must not contain peer data:\n%s", iface)
	}
	// Normalization: blank lines and section headers are cosmetic.
	variant := strings.Replace(peersyncServerConf, "MTU = 1420\n", "MTU = 1420\n\n", 1)
	iface2, _ := splitAWGConf(variant)
	if iface != iface2 {
		t.Error("blank lines must not change the normalized interface section")
	}
}

func TestTryPeerSync_LiveSyncNoRestart(t *testing.T) {
	newConf := strings.Replace(peersyncServerConf,
		"[Peer]\nPublicKey = PUB_ALICE\nAllowedIPs = 10.8.0.2/32\n",
		"[Peer]\nPublicKey = PUB_ALICE\nAllowedIPs = 10.8.0.2/32\n\n[Peer]\nPublicKey = PUB_BOB\nAllowedIPs = 10.8.0.3/32\n", 1)
	client := newFakeSSH(
		fakeRule{substring: "systemctl is-active awg-quick@awg0", out: "active\n"},
		fakeRule{substring: "cat /etc/amnezia/amneziawg/awg0.conf", out: peersyncServerConf},
		fakeRule{substring: "awg show awg0 peers", out: "PUB_ALICE\nPUB_CAROL\n"},
	)
	file := AWGConfFile{Path: "/etc/amnezia/amneziawg/awg0.conf", ServiceName: "awg-quick@awg0", Content: newConf}
	if !tryPeerSync(context.Background(), client, file, false) {
		t.Fatal("expected live peer sync to apply")
	}
	joined := strings.Join(client.Commands(), "\n")
	if !strings.Contains(joined, "awg set awg0 peer PUB_ALICE allowed-ips 10.8.0.2/32") {
		t.Error("missing re-assert of existing peer ALICE:\n" + joined)
	}
	if !strings.Contains(joined, "awg set awg0 peer PUB_BOB allowed-ips 10.8.0.3/32") {
		t.Error("missing add of new peer BOB:\n" + joined)
	}
	if !strings.Contains(joined, "awg set awg0 peer PUB_CAROL remove") {
		t.Error("missing remove of stale peer CAROL:\n" + joined)
	}
	if strings.Contains(joined, "systemctl restart") {
		t.Error("restart must NOT run on the peer-sync path:\n" + joined)
	}
}

func TestTryPeerSync_InterfaceChangeRestarts(t *testing.T) {
	// Same peers but a changed [Interface] field (Jc) → sync must refuse so
	// the caller takes the restart path (kernel can't re-read interface fields).
	changed := strings.Replace(peersyncServerConf, "Jc = 120", "Jc = 8", 1)
	client := newFakeSSH(
		fakeRule{substring: "systemctl is-active awg-quick@awg0", out: "active\n"},
		fakeRule{substring: "cat /etc/amnezia/amneziawg/awg0.conf", out: peersyncServerConf},
	)
	file := AWGConfFile{Path: "/etc/amnezia/amneziawg/awg0.conf", ServiceName: "awg-quick@awg0", Content: changed}
	if tryPeerSync(context.Background(), client, file, false) {
		t.Fatal("interface change must refuse peer sync (restart path)")
	}
	if strings.Contains(strings.Join(client.Commands(), "\n"), "awg set awg0 peer") {
		t.Error("no peer commands may run when the interface changed")
	}
}

func TestTryPeerSync_ServiceDownRestarts(t *testing.T) {
	client := newFakeSSH(
		fakeRule{substring: "systemctl is-active awg-quick@awg0", out: "inactive\n"},
	)
	file := AWGConfFile{Path: "/etc/amnezia/amneziawg/awg0.conf", ServiceName: "awg-quick@awg0", Content: peersyncServerConf}
	if tryPeerSync(context.Background(), client, file, false) {
		t.Fatal("inactive service must refuse peer sync (restart path)")
	}
}

// pushAWGConfs-level regression tests for the compare-before-write ordering
// (live-found 2026-07-19): the peer-sync decision MUST read the CURRENT
// on-disk conf; comparing after the overwrite always yields "identical" and
// the interface change would never restart (node silently runs old config).

func pushAWGConfsRules(diskConf string) []fakeRule {
	return []fakeRule{
		{substring: "echo UP || echo DOWN", out: "UP"},    // probeServiceUp (post-restart)
		{substring: "systemctl is-active", out: "active\n"}, // serviceActive (peer sync)
		{substring: "cat /etc/amnezia/amneziawg/awg0.conf", out: diskConf},
		{substring: "awg show awg0 peers", out: "PUB_ALICE\n"},
		{substring: "sing-box-orch-backup", out: "/tmp/bak/config.json.bak"},
		{substring: "mkdir -p /etc/amnezia/amneziawg", out: ""},
		{substring: "sysctl", out: ""},
		{substring: "", out: ""},
	}
}

func TestPushAWGConfs_PeerOnlyChangeSyncsLive(t *testing.T) {
	newConf := strings.Replace(peersyncServerConf,
		"[Peer]\nPublicKey = PUB_ALICE\nAllowedIPs = 10.8.0.2/32\n",
		"[Peer]\nPublicKey = PUB_ALICE\nAllowedIPs = 10.8.0.2/32\n\n[Peer]\nPublicKey = PUB_BOB\nAllowedIPs = 10.8.0.3/32\n", 1)
	client := newFakeSSH(pushAWGConfsRules(peersyncServerConf)...)
	_, err := pushAWGConfs(context.Background(), client, []AWGConfFile{
		{Path: "/etc/amnezia/amneziawg/awg0.conf", ServiceName: "awg-quick@awg0", Content: newConf},
	}, false)
	if err != nil {
		t.Fatalf("pushAWGConfs: %v", err)
	}
	joined := strings.Join(client.Commands(), "\n")
	if strings.Contains(joined, "restart awg-quick@awg0") {
		t.Error("peer-only change must NOT restart awg-quick:\n" + joined)
	}
	if !strings.Contains(joined, "awg set awg0 peer PUB_BOB allowed-ips 10.8.0.3/32") {
		t.Error("new peer BOB must be added via awg set:\n" + joined)
	}
	// The new conf file must still be written (source of truth on disk).
	found := false
	for _, u := range client.Uploads() {
		if strings.Contains(u.Content, "PUB_BOB") {
			found = true
		}
	}
	if !found {
		t.Error("conf file with the new peer must still be uploaded")
	}
}

func TestPushAWGConfs_InterfaceChangeRestarts(t *testing.T) {
	changed := strings.Replace(peersyncServerConf, "Jc = 120", "Jc = 8", 1)
	client := newFakeSSH(pushAWGConfsRules(peersyncServerConf)...)
	_, err := pushAWGConfs(context.Background(), client, []AWGConfFile{
		{Path: "/etc/amnezia/amneziawg/awg0.conf", ServiceName: "awg-quick@awg0", Content: changed},
	}, false)
	if err != nil {
		t.Fatalf("pushAWGConfs: %v", err)
	}
	if !client.SawCommand("restart awg-quick@awg0") {
		joined := strings.Join(client.Commands(), "\n")
		t.Error("interface change (Jc) MUST restart awg-quick — the pre-write compare catches it:\n" + joined)
	}
}
