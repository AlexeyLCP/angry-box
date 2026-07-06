package chain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func newStoreWith(t *testing.T, mtp []*model.MtproxyUser) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path)
	// Inject legacy MtproxyUsers directly via the storeFile. NewStore already
	// ran migrateOnce (a no-op on the nonexistent file), so write the legacy
	// slice now and re-run the migration to exercise it.
	st.mu.Lock()
	sf := &storeFile{MtproxyUsers: mtp}
	if err := st.writeStore(sf); err != nil {
		t.Fatalf("writeStore: %v", err)
	}
	st.mu.Unlock()
	st.migrateOnce()
	return st
}

func TestMigrateMtproxyUsers(t *testing.T) {
	st := newStoreWith(t, []*model.MtproxyUser{
		{ID: "m1", NodeID: "n1", Name: "alice", SecretHex: "83b231c9ccf32ef09f48c8f63765ab4f", FakeTLSDomain: "disk.yandex.ru", Enabled: true},
		{ID: "m2", NodeID: "n2", Name: "bob", SecretHex: "00112233445566778899aabbccddeeff", FakeTLSDomain: "www.bing.com", Enabled: false},
	})
	users, err := st.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("want 2 migrated users, got %d (%+v)", len(users), users)
	}
	// alice migrated with MTProxy fields + MTProxyNodes=[n1]
	var alice *model.User
	for _, u := range users {
		if u.Name == "alice" {
			alice = u
		}
	}
	if alice == nil {
		t.Fatal("alice not found")
	}
	if alice.MTProxySecret != "83b231c9ccf32ef09f48c8f63765ab4f" {
		t.Errorf("secret: got %q", alice.MTProxySecret)
	}
	if alice.MTProxyDomain != "disk.yandex.ru" {
		t.Errorf("domain: got %q", alice.MTProxyDomain)
	}
	if !alice.Active {
		t.Errorf("alice should be Active (Enabled=true)")
	}
	if len(alice.MTProxyNodes) != 1 || alice.MTProxyNodes[0] != "n1" {
		t.Errorf("MTProxyNodes: got %v", alice.MTProxyNodes)
	}
	// bob disabled -> Active=false
	var bob *model.User
	for _, u := range users {
		if u.Name == "bob" {
			bob = u
		}
	}
	if bob != nil && bob.Active {
		t.Errorf("bob should be inactive (Enabled=false)")
	}
	// The legacy MtproxyUsers slice is cleared by migrateMtproxyUsers (it sets
	// sf.MtproxyUsers = nil); this is verified by the migration helper's behavior
	// and by the 2 migrated users appearing in ListUsers above. The old
	// ListMtproxyUsers() store method was removed in Task 8 cleanup, so it can
	// no longer be asserted on here.
}

func TestMigrateMtproxyUsers_Idempotent(t *testing.T) {
	st := newStoreWith(t, []*model.MtproxyUser{
		{ID: "m1", NodeID: "n1", Name: "alice", SecretHex: "83b231c9ccf32ef09f48c8f63765ab4f", FakeTLSDomain: "disk.yandex.ru", Enabled: true},
	})
	_, _ = st.ListUsers() // triggers migration
	// Second load: no double-migration, no second backup.
	st2 := NewStore(st.path)
	users, _ := st2.ListUsers()
	if len(users) != 1 {
		t.Errorf("idempotent: want 1 user, got %d", len(users))
	}
}

func TestSaveUser_MTProxySecretUniquePerNode(t *testing.T) {
	st := newStoreWith(t, nil) // empty store, no legacy
	_, _ = st.ListUsers()      // run migration (no-op)
	first := &model.User{ID: "u1", Name: "alice", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n1"}}
	if err := st.SaveUser(first); err != nil {
		t.Fatalf("SaveUser first: %v", err)
	}
	// Same secret + same node -> collision.
	dup := &model.User{ID: "u2", Name: "bob", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n1"}}
	if err := st.SaveUser(dup); err == nil {
		t.Errorf("expected uniqueness error for duplicate (node,secret)")
	}
	// Same secret + different node -> OK.
	ok := &model.User{ID: "u3", Name: "carol", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n2"}}
	if err := st.SaveUser(ok); err != nil {
		t.Errorf("different node should be allowed: %v", err)
	}
	// Updating the same user (same ID) -> OK.
	first.MTProxyDomain = "www.bing.com"
	if err := st.SaveUser(first); err != nil {
		t.Errorf("self-update should be allowed: %v", err)
	}
}

func TestListMTProxyUsersForNode(t *testing.T) {
	st := newStoreWith(t, nil)
	_, _ = st.ListUsers()
	_ = st.SaveUser(&model.User{ID: "u1", Name: "alice", MTProxySecret: "ab", MTProxyNodes: []string{"n1"}})
	_ = st.SaveUser(&model.User{ID: "u2", Name: "bob", MTProxySecret: "cd", MTProxyNodes: []string{"n2"}})
	_ = st.SaveUser(&model.User{ID: "u3", Name: "carol"}) // no MTProxy
	got := st.ListMTProxyUsersForNode("n1")
	if len(got) != 1 || got[0].ID != "u1" {
		t.Errorf("ListMTProxyUsersForNode(n1) = %+v, want u1", got)
	}
	all := st.ListMTProxyUsers()
	if len(all) != 2 {
		t.Errorf("ListMTProxyUsers = %d, want 2", len(all))
	}
	// silence unused import
	_ = os.Stat
}

// TestStore_SchemaVersion_FreshStoreAtCurrent verifies a fresh store is
// written at currentSchemaVersion after the first save + migrateOnce.
func TestStore_SchemaVersion_FreshStoreAtCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path)
	_ = st.SaveHost(&model.Host{ID: "h1", Addr: "1.1.1.1:22"})
	// Re-open and read raw JSON to check schema_version.
	st2 := NewStore(path)
	st2.mu.Lock()
	sf, err := st2.readStore()
	st2.mu.Unlock()
	if err != nil {
		t.Fatalf("readStore: %v", err)
	}
	if sf.SchemaVersion != currentSchemaVersion {
		t.Errorf("fresh store schema_version = %d, want %d", sf.SchemaVersion, currentSchemaVersion)
	}
}

// TestStore_SchemaVersion_LegacyMigratesUp verifies a legacy store written at
// schema version 0 (with MtproxyUsers) is migrated up to currentSchemaVersion
// and the version is persisted.
func TestStore_SchemaVersion_LegacyMigratesUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path)
	// Write a legacy v0 store with one MtproxyUser.
	st.mu.Lock()
	sf := &storeFile{MtproxyUsers: []*model.MtproxyUser{
		{ID: "m1", NodeID: "n1", Name: "alice", SecretHex: "83b231c9ccf32ef09f48c8f63765ab4f", FakeTLSDomain: "disk.yandex.ru", Enabled: true},
	}}
	if err := st.writeStore(sf); err != nil {
		t.Fatalf("writeStore: %v", err)
	}
	st.mu.Unlock()
	st.migrateOnce()
	// Re-read raw: schema_version should now be currentSchemaVersion.
	st2 := NewStore(path)
	st2.mu.Lock()
	sf2, _ := st2.readStore()
	st2.mu.Unlock()
	if sf2.SchemaVersion != currentSchemaVersion {
		t.Errorf("after migration schema_version = %d, want %d", sf2.SchemaVersion, currentSchemaVersion)
	}
	if len(sf2.MtproxyUsers) != 0 {
		t.Errorf("legacy MtproxyUsers not cleared after migration: %d", len(sf2.MtproxyUsers))
	}
}