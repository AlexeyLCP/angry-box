package chain

// awg_takeover_users_test.go — tests for the AWG takeover peer → model.User
// materialization (AGENTS.md Known Issue #10: per-client source_ip_cidr routing
// on a takeover'd AWG inbound).

import (
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

// newTakeoverStore returns a fresh temp-backed store for materialization tests.
func newTakeoverStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir() + "/store.json")
}

// truncateConf clips a rendered conf for readable assertion failures.
func truncateConf(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n... [truncated]"
}

// TestMaterializeAWGPeersAsUsers verifies each imported peer becomes a model.User
// with the right AWGPublicKey + AWGAddress + Active + Protocols, and the IDs are
// returned for rollback.
func TestMaterializeAWGPeersAsUsers(t *testing.T) {
	st := newTakeoverStore(t)
	peers := []AwgPeerEntry{
		{Name: "alice", PublicKey: "ALICEPUB1234", AllowedIPs: "10.8.0.2/32"},
		{Name: "bob", PublicKey: "BOBPUB1234567", AllowedIPs: "10.8.0.3/32"},
	}
	ids, err := MaterializeAWGPeersAsUsers(st, "node-1", peers, "chain-x")
	if err != nil {
		t.Fatalf("MaterializeAWGPeersAsUsers: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 synthesized IDs, got %d", len(ids))
	}
	users, _ := st.ListUsers()
	if len(users) != 2 {
		t.Fatalf("want 2 users in store, got %d", len(users))
	}
	// Verify the materialized fields wire into the peer-render loops.
	byID := map[string]*model.User{}
	for _, u := range users {
		byID[u.ID] = u
	}
	u := byID[ids[0]]
	if u == nil {
		t.Fatal("synthesized user not found by returned ID")
	}
	if u.AWGPublicKey != "ALICEPUB1234" {
		t.Errorf("AWGPublicKey = %q, want ALICEPUB1234", u.AWGPublicKey)
	}
	if u.AWGAddress != "10.8.0.2/32" {
		t.Errorf("AWGAddress = %q, want 10.8.0.2/32", u.AWGAddress)
	}
	if !u.Active {
		t.Error("materialized user must be Active")
	}
	if len(u.Protocols) != 1 || u.Protocols[0] != "awg" {
		t.Errorf("Protocols = %v, want [awg]", u.Protocols)
	}
	if len(u.ChainNames) != 1 || u.ChainNames[0] != "chain-x" {
		t.Errorf("ChainNames = %v, want [chain-x]", u.ChainNames)
	}
	// ID is deterministic: takeover-<nodeID>-<pubKey[:8]> lowercased.
	wantIDPrefix := "takeover-node-1-alicepub"
	if !strings.HasPrefix(ids[0], wantIDPrefix) {
		t.Errorf("ID = %q, want prefix %q", ids[0], wantIDPrefix)
	}
}

// TestMaterialize_DedupByID verifies a collision on the synthesized ID with a
// DIFFERENT AWGPublicKey skips (does not clobber the existing real user).
func TestMaterialize_DedupByID(t *testing.T) {
	st := newTakeoverStore(t)
	// The synthesizer computes id = takeover-<nodeID>-<pubKeyPrefix(peerPub, 8)>.
	// Use a peer pubkey whose first 8 lowercased chars = "realpub1", so the
	// synthesized ID is "takeover-node-1-realpub1". Pre-seed a REAL user with
	// that exact ID but a different AWGPublicKey → the synthesizer must skip
	// (refuse to clobber a real user with a different pubkey).
	peerPub := "REALPUB1XYZ" // pubKeyPrefix(., 8) = "realpub1"
	synthID := "takeover-node-1-realpub1"
	st.SaveUser(&model.User{ID: synthID, Name: "real", AWGPublicKey: "DIFFERENTPUB", AWGAddress: "10.8.0.5/32", Active: true})

	peers := []AwgPeerEntry{{Name: "fake", PublicKey: peerPub, AllowedIPs: "10.8.0.6/32"}}
	ids, err := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("ID collision with different pubkey should skip, got %d created", len(ids))
	}
	users, _ := st.ListUsers()
	if len(users) != 1 {
		t.Errorf("real user must not be clobbered, got %d users", len(users))
	}
	if users[0].AWGPublicKey != "DIFFERENTPUB" {
		t.Errorf("real user pubkey overwritten: got %q, want DIFFERENTPUB", users[0].AWGPublicKey)
	}
}

// TestMaterialize_DedupByPubKey verifies an existing user with the SAME
// AWGPublicKey is skipped (the peer is already a managed user — don't
// duplicate it on the server conf).
func TestMaterialize_DedupByPubKey(t *testing.T) {
	st := newTakeoverStore(t)
	st.SaveUser(&model.User{ID: "existing-1", Name: "managed", AWGPublicKey: "SAMEPUB", AWGAddress: "10.8.0.7/32", Active: true})
	peers := []AwgPeerEntry{{Name: "dup", PublicKey: "SAMEPUB", AllowedIPs: "10.8.0.8/32"}}
	ids, err := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("same-pubkey peer should be skipped, got %d created", len(ids))
	}
	users, _ := st.ListUsers()
	if len(users) != 1 {
		t.Errorf("managed user must not be duplicated, got %d users", len(users))
	}
}

// TestMaterialize_Idempotent verifies re-running materialization on the same
// peers is a no-op (the synthesized users already exist with the same pubkey).
func TestMaterialize_Idempotent(t *testing.T) {
	st := newTakeoverStore(t)
	peers := []AwgPeerEntry{{Name: "alice", PublicKey: "ALICEPUB1234", AllowedIPs: "10.8.0.2/32"}}
	ids1, _ := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if len(ids1) != 1 {
		t.Fatalf("first run: want 1, got %d", len(ids1))
	}
	ids2, _ := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if len(ids2) != 0 {
		t.Errorf("second run should be no-op, got %d new", len(ids2))
	}
	users, _ := st.ListUsers()
	if len(users) != 1 {
		t.Errorf("idempotent: want 1 user, got %d", len(users))
	}
}

// TestDeleteSynthesizedAWGUsers verifies rollback symmetry: deleting the
// synthesized IDs removes them; already-deleted IDs are not fatal.
func TestDeleteSynthesizedAWGUsers(t *testing.T) {
	st := newTakeoverStore(t)
	peers := []AwgPeerEntry{
		{Name: "alice", PublicKey: "ALICEPUB1234", AllowedIPs: "10.8.0.2/32"},
		{Name: "bob", PublicKey: "BOBPUB1234567", AllowedIPs: "10.8.0.3/32"},
	}
	ids, _ := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if len(ids) != 2 {
		t.Fatalf("setup: want 2 ids, got %d", len(ids))
	}
	// Delete one, then both (second call hits already-deleted — best-effort).
	DeleteSynthesizedAWGUsers(st, ids[:1])
	if users, _ := st.ListUsers(); len(users) != 1 {
		t.Errorf("after deleting 1, want 1 user, got %d", len(users))
	}
	DeleteSynthesizedAWGUsers(st, ids) // ids[0] already deleted — not fatal
	if users, _ := st.ListUsers(); len(users) != 0 {
		t.Errorf("after deleting all, want 0 users, got %d", len(users))
	}
}

// TestMaterialize_EmptyPeers verifies no peers → no users, no error.
func TestMaterialize_EmptyPeers(t *testing.T) {
	st := newTakeoverStore(t)
	ids, err := MaterializeAWGPeersAsUsers(st, "node-1", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("empty peers: want 0 ids, got %d", len(ids))
	}
}

// TestMaterialize_SkipsEmptyPubKey verifies a peer without a PublicKey is
// skipped (not a usable WireGuard peer).
func TestMaterialize_SkipsEmptyPubKey(t *testing.T) {
	st := newTakeoverStore(t)
	peers := []AwgPeerEntry{
		{Name: "bad", PublicKey: "", AllowedIPs: "10.8.0.2/32"},
		{Name: "good", PublicKey: "GOODPUB12345", AllowedIPs: "10.8.0.3/32"},
	}
	ids, _ := MaterializeAWGPeersAsUsers(st, "node-1", peers, "")
	if len(ids) != 1 {
		t.Errorf("empty-pubkey peer should be skipped, got %d ids", len(ids))
	}
}

// TestAwgServerConfigToAmnezia verifies the flat AwgServerConfig →
// *config.AmneziaOptions adapter (Stage 3): every obfuscation field is carried
// across, and a no-obfuscation server (JC==0) yields nil (plain WG, no amnezia
// block in the rendered conf).
func TestAwgServerConfigToAmnezia(t *testing.T) {
	t.Run("obfuscated", func(t *testing.T) {
		s := &AwgServerConfig{
			JC: 7, JMIN: 50, JMAX: 500,
			S1: 1, S2: 2, S3: 3, S4: 4,
			H1: "100-200", H2: "300-400", H3: "500-600", H4: "700-800",
			I1: "i1", I2: "i2", I3: "i3", I4: "i4", I5: "i5",
		}
		got := AwgServerConfigToAmnezia(s)
		if got == nil {
			t.Fatal("expected non-nil AmneziaOptions for JC!=0")
		}
		if got.JC != 7 || got.JMIN != 50 || got.JMAX != 500 {
			t.Errorf("JC/JMIN/JMAX = %d/%d/%d, want 7/50/500", got.JC, got.JMIN, got.JMAX)
		}
		if got.S1 != 1 || got.S2 != 2 || got.S3 != 3 || got.S4 != 4 {
			t.Errorf("S1-S4 = %d/%d/%d/%d, want 1/2/3/4", got.S1, got.S2, got.S3, got.S4)
		}
		if got.H1 != "100-200" || got.H2 != "300-400" || got.H3 != "500-600" || got.H4 != "700-800" {
			t.Errorf("H1-H4 mismatch: %q/%q/%q/%q", got.H1, got.H2, got.H3, got.H4)
		}
		if got.I1 != "i1" || got.I2 != "i2" || got.I3 != "i3" || got.I4 != "i4" || got.I5 != "i5" {
			t.Errorf("I1-I5 mismatch: %q/%q/%q/%q/%q", got.I1, got.I2, got.I3, got.I4, got.I5)
		}
	})
	t.Run("plain_WG_returns_nil", func(t *testing.T) {
		if got := AwgServerConfigToAmnezia(&AwgServerConfig{JC: 0}); got != nil {
			t.Errorf("JC==0 must yield nil (plain WG), got %+v", got)
		}
		if got := AwgServerConfigToAmnezia(nil); got != nil {
			t.Errorf("nil server must yield nil, got %+v", got)
		}
	})
}

// TestRenderTakeoverAWGConf verifies the fresh awg0.conf rendered from the
// imported server + materialized users (Stage 3):
//   - the path + service target awg0 (the takeover keeps the kernel awg-quick@awg0
//     running, replacing its conf in place — NOT awg1);
//   - [Interface] carries the imported PrivateKey/ListenPort/Address + amnezia
//     (Jc/S1/H1) from the server config;
//   - one [Peer] per active materialized user with that user's pubkey +
//     AllowedIPs (rendered from model.User, NOT the raw AwgPeerEntry — so future
//     user add/remove re-renders correctly);
//   - inactive users / users missing creds are skipped.
func TestRenderTakeoverAWGConf(t *testing.T) {
	server := &AwgServerConfig{
		PrivateKey: "SERVERPRIVKEYBASE64==",
		ListenPort: 51820,
		Address:    "10.8.0.1/24",
		JC:         120, JMIN: 50, JMAX: 1000,
		S1: 115, S2: 45, S3: 22, S4: 12,
		H1: "166632330-364236334", H2: "601951717-1047176668",
		H3: "1104358138-1588356365", H4: "1638363297-2067218275",
		I1: "i1data", I2: "i2data", I3: "i3data", I4: "i4data", I5: "i5data",
	}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true, AWGPublicKey: "ALICEPUB", AWGAddress: "10.8.0.2/32"},
		{ID: "u2", Name: "bob", Active: true, AWGPublicKey: "BOBPUB", AWGAddress: "10.8.0.3/32"},
		// Skipped: inactive.
		{ID: "u3", Name: "carol", Active: false, AWGPublicKey: "CAROLPUB", AWGAddress: "10.8.0.4/32"},
		// Skipped: no pubkey.
		{ID: "u4", Name: "dave", Active: true, AWGPublicKey: "", AWGAddress: "10.8.0.5/32"},
		// Skipped: no address.
		{ID: "u5", Name: "eve", Active: true, AWGPublicKey: "EVEPUB", AWGAddress: ""},
	}
	f := RenderTakeoverAWGConf(server, users)
	if f.Path != awg0ConfPath {
		t.Errorf("Path = %q, want %q (takeover replaces awg0.conf in place)", f.Path, awg0ConfPath)
	}
	if f.ServiceName != "awg-quick@awg0" {
		t.Errorf("ServiceName = %q, want awg-quick@awg0", f.ServiceName)
	}
	c := f.Content
	lower := strings.ToLower(c)
	// [Interface] carries the imported server material.
	for _, want := range []string{"[interface]", "privatekey = serverprivkeybase64==", "listenport = 51820", "address = 10.8.0.1/24", "jc = 120", "s1 = 115", "h1 = 166632330-364236334"} {
		if !strings.Contains(lower, want) {
			t.Errorf("awg0.conf missing %q:\n%s", want, truncateConf(c, 2000))
		}
	}
	// Itime must NEVER be written (runtime-breaking — awg setconf / UAPI reject it).
	if strings.Contains(lower, "itime") {
		t.Errorf("awg0.conf must NOT contain Itime:\n%s", truncateConf(c, 1500))
	}
	// Two [Peer] sections (alice + bob); carol/dave/eve skipped.
	if got := strings.Count(lower, "[peer]"); got != 2 {
		t.Errorf("want 2 [Peer] sections (alice+bob), got %d\n%s", got, truncateConf(c, 2000))
	}
	if !strings.Contains(c, "ALICEPUB") || !strings.Contains(c, "10.8.0.2/32") {
		t.Errorf("alice peer (pubkey + AllowedIPs) missing:\n%s", truncateConf(c, 2000))
	}
	if !strings.Contains(c, "BOBPUB") || !strings.Contains(c, "10.8.0.3/32") {
		t.Errorf("bob peer (pubkey + AllowedIPs) missing:\n%s", truncateConf(c, 2000))
	}
	for _, bad := range []string{"CAROLPUB", "EVEPUB"} {
		if strings.Contains(c, bad) {
			t.Errorf("skipped user pubkey %q must NOT appear:\n%s", bad, truncateConf(c, 2000))
		}
	}
	// PostUp FORWARD rules between awg0 + the sing-box TUN overlay interface
	// must be present (the TUN overlay needs them to forward user traffic).
	if !strings.Contains(lower, "postup") {
		t.Errorf("awg0.conf missing PostUp (FORWARD rules for TUN overlay):\n%s", truncateConf(c, 1500))
	}
}

// TestRenderTakeoverAWGConf_PlainWG verifies a plain-WG takeover (JC==0, no
// amnezia) still renders a valid awg0.conf — just without the amnezia block.
func TestRenderTakeoverAWGConf_PlainWG(t *testing.T) {
	server := &AwgServerConfig{
		PrivateKey: "PLAINPRIVKEY==",
		ListenPort: 51820,
		Address:    "10.8.0.1/24",
		// JC==0 → plain WireGuard, no amnezia.
	}
	users := []model.User{
		{ID: "u1", Name: "alice", Active: true, AWGPublicKey: "ALICEPUB", AWGAddress: "10.8.0.2/32"},
	}
	f := RenderTakeoverAWGConf(server, users)
	lower := strings.ToLower(f.Content)
	if strings.Contains(lower, "jc =") || strings.Contains(lower, "s1 =") || strings.Contains(lower, "h1 =") {
		t.Errorf("plain-WG conf must NOT carry amnezia fields:\n%s", truncateConf(f.Content, 1500))
	}
	if !strings.Contains(lower, "[interface]") || !strings.Contains(lower, "[peer]") {
		t.Errorf("plain-WG conf must still have [Interface]+[Peer]:\n%s", truncateConf(f.Content, 1500))
	}
}

// TestRenderTakeoverAWGConf_DefaultAddress verifies a server with an empty
// Address falls back to 10.8.0.1/24 (the canonical user-entry subnet).
func TestRenderTakeoverAWGConf_DefaultAddress(t *testing.T) {
	server := &AwgServerConfig{PrivateKey: "K==", ListenPort: 51820, JC: 7}
	f := RenderTakeoverAWGConf(server, nil)
	if !strings.Contains(strings.ToLower(f.Content), "address = 10.8.0.1/24") {
		t.Errorf("empty Address must default to 10.8.0.1/24:\n%s", truncateConf(f.Content, 1500))
	}
}