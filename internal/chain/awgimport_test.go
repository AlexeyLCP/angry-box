package chain

import "testing"

func TestParseAWGServerConf(t *testing.T) {
	content := `[Interface]
PrivateKey = serverprivkey123
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
DNS = 1.1.1.1
`
	sc, ok := parseAWGServerConf(content)
	if !ok {
		t.Fatal("expected server config (has ListenPort)")
	}
	if sc.ListenPort != 55555 {
		t.Errorf("ListenPort: got %d", sc.ListenPort)
	}
	if sc.PrivateKey != "serverprivkey123" {
		t.Errorf("PrivateKey: got %s", sc.PrivateKey)
	}
	if sc.JC != 7 || sc.JMIN != 50 || sc.JMAX != 500 {
		t.Errorf("Jc/Jmin/Jmax: got %d/%d/%d", sc.JC, sc.JMIN, sc.JMAX)
	}
	if sc.H1 != "100-200" {
		t.Errorf("H1: got %s", sc.H1)
	}
}

func TestParseAWGServerConf_NoListenPort_NotServer(t *testing.T) {
	content := `[Interface]
PrivateKey = clientpriv
Address = 10.8.0.2/32
`
	if _, ok := parseAWGServerConf(content); ok {
		t.Error("config without ListenPort should not parse as server config")
	}
}

func TestParseAWGExitConf(t *testing.T) {
	content := `[Interface]
PrivateKey = exitpriv
Address = 10.47.3.2/24
Jc = 4
Jmin = 50
Jmax = 837
[Peer]
PublicKey = exitpub
Endpoint = 1.2.3.4:51820
PresharedKey = psk
`
	ec, ok := parseAWGExitConf(content)
	if !ok {
		t.Fatal("expected exit config (has Endpoint/PublicKey)")
	}
	if ec.PrivateKey != "exitpriv" {
		t.Errorf("PrivateKey: got %s", ec.PrivateKey)
	}
	if ec.PublicKey != "exitpub" {
		t.Errorf("PublicKey: got %s", ec.PublicKey)
	}
	if ec.Endpoint != "1.2.3.4:51820" {
		t.Errorf("Endpoint: got %s", ec.Endpoint)
	}
	if ec.Amnezia["Jc"] != "4" {
		t.Errorf("amnezia Jc: got %v", ec.Amnezia["Jc"])
	}
}

func TestParsePeersList(t *testing.T) {
	content := `[Interface]
PrivateKey = serverpriv
Address = 10.45.116.1/24
[Peer]
PublicKey = peer1pub
AllowedIPs = 10.45.116.2/32
# Name=alice
[Peer]
PublicKey = peer2pub
AllowedIPs = 10.45.116.3/32
# Name=bob
`
	peers := parsePeersList(content)
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	if peers[0].PublicKey != "peer1pub" {
		t.Errorf("first peer PublicKey: got %s", peers[0].PublicKey)
	}
	if peers[0].Name != "alice" {
		t.Errorf("first peer Name: got %s", peers[0].Name)
	}
}