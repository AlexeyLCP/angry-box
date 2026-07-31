package chain

// standalone_awg_test.go — verifies standalone AWG under the kernel-AWG
// architecture: the AWG server interface (awg0) is owned by the kernel
// (awg-quick@awg0), NOT a sing-box userspace endpoint. buildStandaloneInOut
// therefore emits NOTHING for an AWG inbound (the per-user peers live in the
// separately-pushed awg0.conf via RenderServerAWGConf). The sing-box TUN
// overlay that captures awg0 traffic is emitted at the node level by
// buildMergedNodeConfig (see awg_tun_overlay_test.go).

import (
	"encoding/json"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// TestBuildStandaloneInOut_AWG_EmitsNoUserspaceEndpoint verifies a standalone
// AWG inbound no longer produces a userspace WireGuard endpoint — the kernel
// owns awg0. The per-user peers are rendered into awg0.conf by
// RenderServerAWGConf (see awg_server_test.go), not the sing-box config.
func TestBuildStandaloneInOut_AWG_EmitsNoUserspaceEndpoint(t *testing.T) {
	ib := &model.NodeInbound{
		Protocol: "awg", Port: 51820, Tag: "sa-awg-test",
		ServerPrivKey: awgServerPriv,
	}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true, AWGPublicKey: "pub-alice", AWGAddress: "10.8.0.2/32"},
		{ID: "u2", Name: "bob", Active: true, AWGPublicKey: "pub-bob", AWGAddress: "10.8.0.3/32"},
	}
	byInbound := map[string][]model.User{"sa-awg-test": users}
	inbounds, endpoints := buildStandaloneInOut(ib, "sa-awg-test", byInbound, nil)
	if len(endpoints) != 0 {
		t.Fatalf("kernel-AWG must emit NO userspace endpoint, got %d: %s", len(endpoints), endpoints)
	}
	if len(inbounds) != 0 {
		t.Fatalf("kernel-AWG standalone must emit NO sing-box inbound (TUN overlay is node-level), got %d: %s", len(inbounds), inbounds)
	}
}

// TestBuildStandaloneInOut_AWG_NoUsers_EmitsNothing — no assigned users still
// emits nothing (the kernel awg0.conf carries a placeholder peer, not the
// sing-box config).
func TestBuildStandaloneInOut_AWG_NoUsers_EmitsNothing(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "sa-awg-empty", ServerPrivKey: awgServerPriv}
	inbounds, endpoints := buildStandaloneInOut(ib, "sa-awg-empty", nil, nil)
	if len(endpoints) != 0 || len(inbounds) != 0 {
		t.Fatalf("kernel-AWG with no users must emit nothing, got inbounds=%d endpoints=%d", len(inbounds), len(endpoints))
	}
}

// TestBuildStandaloneInOut_AWG_UsersWithoutCreds_EmitsNothing — users without
// AWG creds still emit nothing (the kernel owns the interface regardless).
func TestBuildStandaloneInOut_AWG_UsersWithoutCreds_EmitsNothing(t *testing.T) {
	ib := &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "sa-awg-nocreds", ServerPrivKey: awgServerPriv}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true},                        // no AWG creds
		{ID: "u2", Name: "bob", Active: true, AWGPublicKey: "pub-bob"}, // no AWGAddress
	}
	byInbound := map[string][]model.User{"sa-awg-nocreds": users}
	inbounds, endpoints := buildStandaloneInOut(ib, "sa-awg-nocreds", byInbound, nil)
	if len(endpoints) != 0 || len(inbounds) != 0 {
		t.Fatalf("kernel-AWG must emit nothing regardless of user creds, got inbounds=%d endpoints=%d", len(inbounds), len(endpoints))
	}
}

// TestBuildStandaloneInOut_AWG_NoUserspaceWGRegression is a belt-and-braces
// guard: across every AWG standalone scenario, the builder must NEVER emit a
// userspace wireguard endpoint/inbound (the chacha20poly1305 panic path).
func TestBuildStandaloneInOut_AWG_NoUserspaceWGRegression(t *testing.T) {
	scenarios := []struct {
		name  string
		ib    *model.NodeInbound
		users []model.User
	}{
		{"multi-peer", &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "t1", ServerPrivKey: awgServerPriv},
			[]model.User{{Name: "a", Active: true, AWGPublicKey: "pa", AWGAddress: "10.8.0.2/32"}}},
		{"empty", &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "t2", ServerPrivKey: awgServerPriv}, nil},
		{"nocreds", &model.NodeInbound{Protocol: "awg", Port: 51820, Tag: "t3", ServerPrivKey: awgServerPriv},
			[]model.User{{Name: "a", Active: true}}},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			byInbound := map[string][]model.User{sc.ib.Tag: sc.users}
			inbounds, endpoints := buildStandaloneInOut(sc.ib, sc.ib.Tag, byInbound, nil)
			for _, raw := range append(append([]json.RawMessage{}, inbounds...), endpoints...) {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil && m["type"] == "wireguard" {
					t.Errorf("%s: emitted userspace wireguard (panic path): %s", sc.name, string(raw))
				}
			}
		})
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
