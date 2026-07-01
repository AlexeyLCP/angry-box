//go:build wsl_smoke

package wslsmoke

// wsl_import_nondestructive_test.go — verifies the AWG SSH importer's
// placeholder-only policy: it must back-fill TODO/empty fields but NEVER
// overwrite real operator-entered values. Uses realistic awg0.conf + awg-exit
// configs modelled on the production dns.idoctor.mom infrastructure.

import (
	"strings"
	"testing"
	"time"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// realisticAWG0Conf mirrors the real dns.idoctor.mom awg0.conf (server-side).
const realisticAWG0Conf = `[Interface]
PrivateKey = serverRealPrivKeyFromProductionABC123=
Address = 10.45.116.1/24
ListenPort = 55555
Jc = 4
Jmin = 212
Jmax = 837
S1 = 118
S2 = 114
S3 = 54
S4 = 21
H1 = 143219817-450506440
H2 = 545807649-1006572806
H3 = 1094138953-1444146798
H4 = 1766806575-2013246654
DNS = 1.1.1.1
PostUp = iptables -A FORWARD -i awg0 -o sing-box-tun -j ACCEPT
PostDown = iptables -D FORWARD -i awg0 -o sing-box-tun -j ACCEPT
`

// realisticExitConf mirrors a real awg-exit-n1.conf (exit node with amnezia + I1-I5).
const realisticExitConf = `[Interface]
PrivateKey = exitRealPrivKeyDEF456=
Address = 10.47.3.2/24
Jc = 15
Jmin = 210
Jmax = 549
S1 = 97
S2 = 22
S3 = 26
S4 = 11
H1 = 100-200
H2 = 300-400
H3 = 500-600
H4 = 700-800
I1 = <b 0xdeadbeef00112233>
I2 = <b 0xcafebabeddeeff00>
[Peer]
PublicKey = nuVfWcH17JlOzFXKJ261mYbtDpXotnR6HyofbOEIA0c=
Endpoint = 144.31.224.212:50074
PresharedKey = realPresharedKeyGHI789=
AllowedIPs = 0.0.0.0/0
`

// seedRealAWG writes the realistic awg0.conf + awg-exit-n1.conf to the WSL node.
func seedRealAWG(t *testing.T) {
	t.Helper()
	c := wslConnect(t)
	defer c.Close()
	seed := "set -e\nsudo mkdir -p /etc/amnezia/amneziawg\n" +
		"sudo tee /etc/amnezia/amneziawg/awg0.conf >/dev/null <<'EOF0'\n" + realisticAWG0Conf + "\nEOF0\n" +
		"sudo tee /etc/amnezia/amneziawg/awg-exit-n1.conf >/dev/null <<'EOF1'\n" + realisticExitConf + "\nEOF1\n" +
		"echo SEEDED_REAL"
	out := runOn(t, c, seed, 30*time.Second)
	if !strings.Contains(out, "SEEDED_REAL") {
		t.Fatalf("seed real AWG failed: %s", out)
	}
}

// TestWSL_ImportAWG_FillsPlaceholders verifies TODO/empty inbound fields get
// back-filled with the real imported key material.
func TestWSL_ImportAWG_FillsPlaceholders(t *testing.T) {
	seedRealAWG(t)
	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{
		{Protocol: "awg", ServerPrivKey: "TODO", AWGClientPub: "", AWGClientPriv: "TODO-fill"},
	}
	res, err := importAndCheck(t, info)
	if err != nil {
		t.Fatal(err)
	}
	// Server private key placeholder should now hold the real imported value.
	if info.Inbounds[0].ServerPrivKey == "TODO" || info.Inbounds[0].ServerPrivKey == "" {
		t.Errorf("placeholder ServerPrivKey not back-filled: %q", info.Inbounds[0].ServerPrivKey)
	}
	if info.Inbounds[0].ServerPrivKey != "serverRealPrivKeyFromProductionABC123=" {
		t.Errorf("ServerPrivKey mismatch: got %q", info.Inbounds[0].ServerPrivKey)
	}
	// Client pub (peer) back-filled from the exit node's PublicKey.
	if info.Inbounds[0].AWGClientPub != "nuVfWcH17JlOzFXKJ261mYbtDpXotnR6HyofbOEIA0c=" {
		t.Errorf("AWGClientPub not back-filled from exit PublicKey: %q", info.Inbounds[0].AWGClientPub)
	}
	t.Logf("placeholders back-filled: priv=%q clientpub=%q", info.Inbounds[0].ServerPrivKey, info.Inbounds[0].AWGClientPub)
	_ = res
}

// TestWSL_ImportAWG_DoesNotOverwriteRealValues is the critical non-destructive
// test: when the inbound already has real operator-entered key material, the
// importer must NOT overwrite it. This is what "doesn't break anything" means.
func TestWSL_ImportAWG_DoesNotOverwriteRealValues(t *testing.T) {
	seedRealAWG(t)
	originalPriv := "operatorEnteredPrivateKeyXYZ="
	originalClientPub := "operatorEnteredClientPubKeyUVW="
	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{
		{Protocol: "awg", ServerPrivKey: originalPriv, AWGClientPub: originalClientPub},
	}
	if _, err := importAndCheck(t, info); err != nil {
		t.Fatal(err)
	}
	// The real operator values must be untouched.
	if info.Inbounds[0].ServerPrivKey != originalPriv {
		t.Errorf("NON-DESTRUCTIVE VIOLATION: ServerPrivKey overwritten by import! was %q, now %q", originalPriv, info.Inbounds[0].ServerPrivKey)
	}
	if info.Inbounds[0].AWGClientPub != originalClientPub {
		t.Errorf("NON-DESTRUCTIVE VIOLATION: AWGClientPub overwritten by import! was %q, now %q", originalClientPub, info.Inbounds[0].AWGClientPub)
	}
	t.Logf("non-destructive OK: operator values preserved (priv=%q, clientpub=%q)", info.Inbounds[0].ServerPrivKey, info.Inbounds[0].AWGClientPub)
}

// TestWSL_ImportAWG_ParsesRealisticServerParams verifies the parser extracts
// the production-grade Jc/Jmin/Jmax/S1-S4/H1-H4 from a real awg0.conf shape.
func TestWSL_ImportAWG_ParsesRealisticServerParams(t *testing.T) {
	seedRealAWG(t)
	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{{Protocol: "awg", ServerPrivKey: "TODO", AWGClientPub: "TODO"}}
	res, err := importAndCheck(t, info)
	if err != nil {
		t.Fatal(err)
	}
	sc := res.ServerConfig
	if sc == nil {
		t.Fatal("server config not parsed")
	}
	if sc.ListenPort != 55555 {
		t.Errorf("ListenPort: got %d, want 55555", sc.ListenPort)
	}
	if sc.JC != 4 || sc.JMIN != 212 || sc.JMAX != 837 {
		t.Errorf("Jc/Jmin/Jmax: got %d/%d/%d, want 4/212/837", sc.JC, sc.JMIN, sc.JMAX)
	}
	if sc.S1 != 118 || sc.S2 != 114 || sc.S3 != 54 || sc.S4 != 21 {
		t.Errorf("S1-S4: got %d/%d/%d/%d, want 118/114/54/21", sc.S1, sc.S2, sc.S3, sc.S4)
	}
	if sc.H1 != "143219817-450506440" {
		t.Errorf("H1: got %s", sc.H1)
	}
	if sc.PostUp == "" || !strings.Contains(sc.PostUp, "FORWARD") {
		t.Errorf("PostUp not parsed: %q", sc.PostUp)
	}
	t.Logf("server params parsed OK: Jc=%d S1=%d H1=%s PostUp=%d chars", sc.JC, sc.S1, sc.H1, len(sc.PostUp))
}

// TestWSL_ImportAWG_ParsesExitNodeWithCPS verifies an exit-node conf with
// amnezia + I1-I5 is parsed (the production exit nodes carry CPS packets).
func TestWSL_ImportAWG_ParsesExitNodeWithCPS(t *testing.T) {
	seedRealAWG(t)
	info := &model.NodeInfo{Host: wslHost(t)}
	info.Inbounds = []model.NodeInbound{{Protocol: "awg", ServerPrivKey: "TODO", AWGClientPub: "TODO"}}
	res, err := importAndCheck(t, info)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.ExitNodes) != 1 {
		t.Fatalf("expected 1 exit node, got %d", len(res.ExitNodes))
	}
	ec := res.ExitNodes[0]
	if ec.Endpoint != "144.31.224.212:50074" {
		t.Errorf("exit Endpoint: got %s", ec.Endpoint)
	}
	if ec.PublicKey != "nuVfWcH17JlOzFXKJ261mYbtDpXotnR6HyofbOEIA0c=" {
		t.Errorf("exit PublicKey: got %s", ec.PublicKey)
	}
	if ec.PresharedKey != "realPresharedKeyGHI789=" {
		t.Errorf("exit PresharedKey: got %s", ec.PresharedKey)
	}
	// amnezia dict should carry Jc + I1 (CPS).
	if ec.Amnezia["Jc"] != "15" {
		t.Errorf("exit amnezia Jc: got %v", ec.Amnezia["Jc"])
	}
	if ec.Amnezia["I1"] == "" {
		t.Error("exit amnezia I1 (CPS) not parsed")
	}
	if !strings.HasPrefix(ec.Amnezia["I1"], "<b 0x") {
		t.Errorf("exit I1 not in <b 0x...> form: %q", ec.Amnezia["I1"])
	}
	t.Logf("exit node parsed OK: Endpoint=%s CPS I1=%q", ec.Endpoint, ec.Amnezia["I1"])
}

// importAndCheck runs the importer and asserts it returned no error + parsed a
// server config. Returns the result for further assertions.
func importAndCheck(t *testing.T, info *model.NodeInfo) (*chain.ImportResult, error) {
	t.Helper()
	res, err := chain.ImportAWGConfigs(info.Host, true, info)
	if err != nil {
		return nil, err
	}
	if res.ServerConfig == nil {
		return nil, &simpleErr{msg: "server config not parsed"}
	}
	return res, nil
}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }