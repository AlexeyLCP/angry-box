package vpnuri

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundtrip(t *testing.T) {
	conf := `[Interface]
Address = 10.8.0.2/32
PrivateKey = clientpriv
MTU = 1420
Jc = 4
S1 = 24
H1 = 1

[Peer]
PublicKey = serverpub
PresharedKey = pskvalue
AllowedIPs = 0.0.0.0/0, ::/0
Endpoint = 1.2.3.4:51820
PersistentKeepalive = 25
`
	uri, err := EncodeConf(conf)
	if err != nil {
		t.Fatalf("EncodeConf: %v", err)
	}
	if !strings.HasPrefix(uri, "vpn://") {
		t.Fatalf("prefix: %s", uri)
	}
	payload, err := Decode(uri)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	got, err := ConfFromPayload(payload)
	if err != nil {
		t.Fatalf("ConfFromPayload: %v", err)
	}
	if !strings.Contains(got, "PrivateKey = clientpriv") {
		t.Errorf("conf missing private key: %s", got)
	}
	if !strings.Contains(got, "Endpoint = 1.2.3.4:51820") {
		t.Errorf("conf missing endpoint: %s", got)
	}
}

func TestEncodeConfRejectsEmpty(t *testing.T) {
	if _, err := EncodeConf(""); err == nil {
		t.Fatal("expected error")
	}
	if _, err := EncodeConf("not a conf"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsAWGConf(t *testing.T) {
	if !IsAWGConf("[Interface]\n[Peer]\n") {
		t.Fatal("want true")
	}
	if IsAWGConf("vless://x") {
		t.Fatal("want false")
	}
}
