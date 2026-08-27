package chain

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/awg/vpnuri"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestParseAndRenderAWGOutbound(t *testing.T) {
	conf := `[Interface]
PrivateKey = clientpriv
Address = 10.9.0.5/32
MTU = 1420
Jc = 4
Jmin = 10
Jmax = 50
S1 = 24
S2 = 24
S3 = 24
S4 = 24
H1 = 1
H2 = 2
H3 = 3
H4 = 4

[Peer]
PublicKey = serverpub
PresharedKey = psk1
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 9.9.9.9:51820
PersistentKeepalive = 25
`
	ob, err := ParseAWGOutboundConf(conf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ob.Endpoint != "9.9.9.9:51820" || ob.PrivateKey != "clientpriv" {
		t.Fatalf("parsed: %+v", ob)
	}
	ob.ID = "1"
	out := RenderAWGOutboundConf(*ob)
	if !strings.Contains(out, "Table = off") {
		t.Error("missing Table=off")
	}
	if strings.Contains(out, "DNS =") {
		t.Error("must not write DNS")
	}
	if strings.Contains(out, "I1 =") {
		t.Error("must not write I1")
	}
	if !strings.Contains(out, "PresharedKey = psk1") {
		t.Error("missing PSK")
	}
}

func TestParseAWGOutbound_VPNURI(t *testing.T) {
	conf := `[Interface]
PrivateKey = k
Address = 10.8.0.2/32

[Peer]
PublicKey = p
Endpoint = 1.2.3.4:1
`
	uri, err := vpnuri.EncodeConf(conf)
	if err != nil {
		t.Fatal(err)
	}
	ob, err := ParseAWGOutboundConf(uri)
	if err != nil {
		t.Fatal(err)
	}
	if ob.Endpoint != "1.2.3.4:1" {
		t.Errorf("endpoint %s", ob.Endpoint)
	}
}

func TestParseAllPeers(t *testing.T) {
	conf := `[Interface]
PrivateKey = s
ListenPort = 1

[Peer]
# alice
PublicKey = A
AllowedIPs = 10.8.0.2/32
PresharedKey = pskA

[Peer]
PublicKey = B
AllowedIPs = 10.8.0.3/32
`
	peers := parseAllPeers(conf)
	if len(peers) != 2 {
		t.Fatalf("got %d peers", len(peers))
	}
	if peers[0].Name != "alice" || peers[0].PublicKey != "A" || peers[0].PresharedKey != "pskA" {
		t.Errorf("peer0 %+v", peers[0])
	}
}

func TestImportAWG_Docker(t *testing.T) {
	dockerConf := `[Interface]
PrivateKey = dockpriv
Address = 10.8.1.1/24
ListenPort = 443
Jc = 4

[Peer]
# bob
PublicKey = bobpub
AllowedIPs = 10.8.1.2/32
`
	fake := newFakeSSH(
		fakeRule{substring: "cat /etc/amnezia/amneziawg/awg0.conf", out: ""},
		fakeRule{substring: "ls /etc/amnezia/amneziawg/awg-exit-", out: ""},
		fakeRule{substring: "cat /etc/amnezia/amneziawg/awg0-peers.list", out: ""},
		fakeRule{substring: "ls /etc/awg3/", out: ""},
		fakeRule{substring: "docker ps --format", out: "amnezia-awg\n"},
		fakeRule{substring: "docker exec amnezia-awg cat /opt/amnezia/awg/awg0.conf", out: dockerConf},
		fakeRule{substring: "", out: ""},
	)
	host := model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}
	res, err := ImportAWGConfigs(host, false, nil, &fakeConnector{client: fake})
	if err != nil {
		t.Fatal(err)
	}
	if res.ServerConfig == nil || res.ServerConfig.ListenPort != 443 {
		t.Fatalf("server: %+v", res.ServerConfig)
	}
	if len(res.Peers) != 1 || res.Peers[0].Name != "bob" {
		t.Fatalf("peers: %+v", res.Peers)
	}
	if len(res.StopTargets) == 0 || res.StopTargets[0] != "docker:amnezia-awg" {
		t.Fatalf("stop: %v", res.StopTargets)
	}
}
