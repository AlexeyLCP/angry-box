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