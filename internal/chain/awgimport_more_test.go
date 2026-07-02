package chain

// awgimport_more_test.go — covers ImportAWGConfigs (SSH read of AWG configs)
// and backfillInbounds, against the chain-package fake SSH. CTO-review C3
// phase 5.

import (
	"errors"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// awg0ConfSample is a minimal valid awg0.conf server config.
const awg0ConfSample = `[Interface]
PrivateKey = serverPrivKeyHex
Address = 10.8.0.1/24
ListenPort = 51820
`

// awgExitConfSample is a minimal awg-exit-n1.conf.
const awgExitConfSample = `[Interface]
PrivateKey = exitPrivHex
Address = 10.9.0.1/24
ListenPort = 51821

[Peer]
PublicKey = exitPubHex
Endpoint = 1.2.3.4:51820
`

// awgPeersListSample is a minimal awg0-peers.list (ini-style [Peer] sections,
// matching parsePeersList's expected format).
const awgPeersListSample = `[Peer]
PublicKey = peerPubHex
AllowedIPs = 10.8.0.2/32
Name = alice
`

// TestImportAWGConfigs_HappyPath verifies awg0.conf + an exit conf + peers list
// + sing-box config are all parsed and imported.
func TestImportAWGConfigs_HappyPath(t *testing.T) {
	rules := []fakeRule{
		{substring: "cat /etc/amnezia/amneziawg/awg0.conf", out: awg0ConfSample},
		{substring: "ls /etc/amnezia/amneziawg/awg-exit-", out: "/etc/amnezia/amneziawg/awg-exit-n1.conf\n"},
		{substring: "cat /etc/amnezia/amneziawg/awg-exit-n1.conf", out: awgExitConfSample},
		{substring: "cat /etc/amnezia/amneziawg/awg0-peers.list", out: awgPeersListSample},
		{substring: "cat /usr/local/etc/sing-box/config.json", out: `{"inbounds":[]}`},
		{substring: "", out: ""},
	}
	fake := newFakeSSH(rules...)
	host := model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}
	res, err := ImportAWGConfigs(host, false, nil, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if res.ServerConfig == nil {
		t.Fatal("expected ServerConfig parsed")
	}
	if res.ServerConfig.ListenPort != 51820 {
		t.Errorf("ListenPort: got %d, want 51820", res.ServerConfig.ListenPort)
	}
	if !res.Imported["awg0_conf"] {
		t.Error("awg0_conf not marked imported")
	}
	if len(res.ExitNodes) != 1 {
		t.Errorf("ExitNodes: got %d, want 1", len(res.ExitNodes))
	}
	if !res.Imported["exit_nodes"] {
		t.Error("exit_nodes not marked imported")
	}
	if len(res.Peers) != 1 {
		t.Errorf("Peers: got %d, want 1", len(res.Peers))
	}
	if res.SingboxConfig == nil {
		t.Error("expected SingboxConfig parsed")
	}
}

// TestImportAWGConfigs_ConnectFails verifies a connect failure surfaces.
func TestImportAWGConfigs_ConnectFails(t *testing.T) {
	host := model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}
	_, err := ImportAWGConfigs(host, false, nil, failingConnector(errors.New("dial: refused")))
	if err == nil {
		t.Fatal("expected connect failure")
	}
	if !strings.Contains(err.Error(), "ssh connect") {
		t.Errorf("got %q, want ssh connect", err.Error())
	}
}

// TestImportAWGConfigs_EmptyHost verifies an empty remote (no configs) returns a
// result with no imports and no error.
func TestImportAWGConfigs_EmptyHost(t *testing.T) {
	fake := newFakeSSH(fakeRule{substring: "", out: ""})
	host := model.Host{ID: "h1", Addr: "1.2.3.4:22", User: "root", KeyPath: "/k"}
	res, err := ImportAWGConfigs(host, false, nil, &fakeConnector{client: fake})
	if err != nil {
		t.Fatalf("ImportAWGConfigs: %v", err)
	}
	if res.ServerConfig != nil {
		t.Error("expected no ServerConfig on empty host")
	}
	if len(res.Imported) != 0 {
		t.Errorf("expected no imports, got %v", res.Imported)
	}
}

// TestBackfillInbounds_FillsPlaceholders verifies a placeholder AWG inbound gets
// the server private key + client pub from the import result.
func TestBackfillInbounds_FillsPlaceholders(t *testing.T) {
	info := &model.NodeInfo{
		Inbounds: []model.NodeInbound{
			{Protocol: "awg", ServerPrivKey: "TODO", AWGClientPub: ""},
		},
	}
	res := &ImportResult{
		Imported: map[string]bool{},
		ServerConfig: &AwgServerConfig{PrivateKey: "realPriv"},
		ExitNodes: []AwgExitConfig{{PublicKey: "realClientPub"}},
	}
	summary := backfillInbounds(info, res)
	if info.Inbounds[0].ServerPrivKey != "realPriv" {
		t.Errorf("ServerPrivKey: got %q, want realPriv", info.Inbounds[0].ServerPrivKey)
	}
	if info.Inbounds[0].AWGClientPub != "realClientPub" {
		t.Errorf("AWGClientPub: got %q, want realClientPub", info.Inbounds[0].AWGClientPub)
	}
	if !res.Imported["db_updated"] {
		t.Error("expected db_updated flag")
	}
	if summary == "no changes needed" {
		t.Error("expected a change summary")
	}
}

// TestBackfillInbounds_NoChanges verifies a non-AWG inbound or already-filled
// fields report "no changes needed".
func TestBackfillInbounds_NoChanges(t *testing.T) {
	info := &model.NodeInfo{
		Inbounds: []model.NodeInbound{
			{Protocol: "vless", ServerPrivKey: "already-set", AWGClientPub: "already-set"},
		},
	}
	res := &ImportResult{Imported: map[string]bool{}}
	summary := backfillInbounds(info, res)
	if summary != "no changes needed" {
		t.Errorf("got %q, want no changes needed", summary)
	}
}

// TestBackfillInbounds_NonAWGSkipped verifies non-AWG inbounds are not touched.
func TestBackfillInbounds_NonAWGSkipped(t *testing.T) {
	info := &model.NodeInfo{
		Inbounds: []model.NodeInbound{
			{Protocol: "vless", ServerPrivKey: "TODO"},
		},
	}
	res := &ImportResult{
		Imported:     map[string]bool{},
		ServerConfig: &AwgServerConfig{PrivateKey: "realPriv"},
	}
	backfillInbounds(info, res)
	if info.Inbounds[0].ServerPrivKey != "TODO" {
		t.Errorf("vless inbound should not be backfilled, got %q", info.Inbounds[0].ServerPrivKey)
	}
}