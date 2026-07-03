package chain

// standalone_awg_test.go — verifies standalone AWG multi-peer (#5): when a
// standalone AWG inbound has assigned users (ForUsers) with per-user AWG creds,
// the rendered endpoint carries one WireGuard peer per user (PublicKey +
// AllowedIPs from the user), mirroring the chain user-entry path. Falls back to
// the legacy single-peer builder when no users qualify.

import (
	"encoding/json"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/singbox/config"
)

// TestBuildStandaloneInOut_AWG_MultiPeer verifies a standalone AWG inbound with
// two assigned users (both with AWG creds) renders a multi-peer endpoint.
func TestBuildStandaloneInOut_AWG_MultiPeer(t *testing.T) {
	ib := &model.NodeInbound{
		Protocol: "awg", Port: 51820, Tag: "sa-awg-test",
		ServerPrivKey: awgServerPriv,
	}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true, AWGPublicKey: "pub-alice", AWGAddress: "10.8.0.2/32"},
		{ID: "u2", Name: "bob", Active: true, AWGPublicKey: "pub-bob", AWGAddress: "10.8.0.3/32"},
	}
	byInbound := map[string][]model.User{"sa-awg-test": users}
	_, endpoints := buildStandaloneInOut(ib, "sa-awg-test", byInbound)
	if len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(endpoints))
	}
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(endpoints[0], &ep); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, endpoints[0])
	}
	if ep.Tag != "sa-awg-test" {
		t.Errorf("tag=%s, want sa-awg-test", ep.Tag)
	}
	if ep.ListenPort != 51820 {
		t.Errorf("listen_port=%d, want 51820", ep.ListenPort)
	}
	if len(ep.Peers) != 2 {
		t.Fatalf("want 2 peers (alice, bob), got %d: %+v", len(ep.Peers), ep.Peers)
	}
	want := map[string]string{"pub-alice": "10.8.0.2/32", "pub-bob": "10.8.0.3/32"}
	for _, p := range ep.Peers {
		exp, ok := want[p.PublicKey]
		if !ok {
			t.Errorf("unexpected peer pubkey %s", p.PublicKey)
			continue
		}
		if len(p.AllowedIPs) != 1 || p.AllowedIPs[0] != exp {
			t.Errorf("peer %s allowed_ips=%v, want [%s]", p.PublicKey, p.AllowedIPs, exp)
		}
	}
}

// TestBuildStandaloneInOut_AWG_NoUsers_FallsBackToSinglePeer — no assigned
// users (or users without AWG creds) -> legacy single-peer builder (placeholder
// peer, since ib.AWGClientPub is empty).
func TestBuildStandaloneInOut_AWG_NoUsers_FallsBackToSinglePeer(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "sa-awg-empty", ServerPrivKey: awgServerPriv}
	_, endpoints := buildStandaloneInOut(ib, "sa-awg-empty", nil)
	if len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint (legacy fallback), got %d", len(endpoints))
	}
	var ep config.WireGuardEndpoint
	if err := json.Unmarshal(endpoints[0], &ep); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Legacy builder produces a single peer (placeholder when AWGClientPub empty).
	if len(ep.Peers) != 1 {
		t.Fatalf("want 1 peer (legacy), got %d", len(ep.Peers))
	}
	if ep.Peers[0].PublicKey != "CLIENT_PUBLIC_KEY_HERE" {
		t.Errorf("placeholder peer pub=%s, want CLIENT_PUBLIC_KEY_HERE", ep.Peers[0].PublicKey)
	}
}

// TestBuildStandaloneInOut_AWG_UsersWithoutCreds_FallsBack — users assigned but
// none have AWGPublicKey/AWGAddress -> cannot be peers -> legacy single-peer.
func TestBuildStandaloneInOut_AWG_UsersWithoutCreds_FallsBack(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "sa-awg-nocreds", ServerPrivKey: awgServerPriv}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true}, // no AWG creds
		{ID: "u2", Name: "bob", Active: true, AWGPublicKey: "pub-bob"}, // no AWGAddress
	}
	byInbound := map[string][]model.User{"sa-awg-nocreds": users}
	_, endpoints := buildStandaloneInOut(ib, "sa-awg-nocreds", byInbound)
	if len(endpoints) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(endpoints))
	}
	var ep config.WireGuardEndpoint
	_ = json.Unmarshal(endpoints[0], &ep)
	if len(ep.Peers) != 1 {
		t.Fatalf("want 1 peer (legacy fallback, no qualified users), got %d", len(ep.Peers))
	}
}

// TestUsersByInboundMap verifies the users-by-inbound map resolves ForUsers to
// active users, skips inactive/expired, and ignores inbounds without a Tag.
func TestUsersByInboundMap(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir + "/store.json")
	if err := store.SaveUser(&model.User{ID: "u1", Name: "alice", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUser(&model.User{ID: "u2", Name: "bob", Active: false}); err != nil {
		t.Fatal(err) // inactive -> skipped
	}
	inbounds := []model.NodeInbound{
		{Protocol: "awg", Tag: "sa-1", ForUsers: []string{"u1", "u2", "missing"}},
		{Protocol: "awg", Tag: "sa-2", ForUsers: []string{"u2"}}, // only inactive
		{Protocol: "awg", ForUsers: []string{"u1"}},              // no Tag -> skipped
	}
	m := usersByInboundMap(store, inbounds)
	if m == nil {
		t.Fatal("want non-nil map")
	}
	if got := len(m["sa-1"]); got != 1 {
		t.Errorf("sa-1: want 1 active user (alice), got %d", got)
	}
	if got := len(m["sa-2"]); got != 0 {
		t.Errorf("sa-2: want 0 (only inactive bob), got %d", got)
	}
	if _, ok := m["sa-1"]; !ok {
		t.Error("sa-1 missing from map")
	}
}