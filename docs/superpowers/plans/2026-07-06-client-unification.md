# Client Unification Implementation Plan (Subproject B)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge `model.MtproxyUser` into `model.User` (MTProxy credentials as an optional block + `MTProxyNodes []string`), collapse the Users/MTProxy/Clients nav into one CRUD "Clients" page, and replace the inbound form's modal-hijacking "Create first user" link with a non-destructive hint + refresh button.

**Architecture:** `User` gains MTProxy-credential fields; `MtproxyUser` is migrated away (auto on store load, with `.bak` backup) then deleted. The MTProxy inbound renderer + merged-config builder switch from `[]*model.MtproxyUser` to `[]*model.User`. `scheduleAutoApplyForUser` extends to also redeploy nodes in `u.MTProxyNodes`. The inbound form stops hijacking `#modal-container` and instead offers a `hx-select`-scoped refresh of only the user-checkbox block.

**Tech Stack:** Go, HTMX + Templ + TailwindCSS/DaisyUI, i18n (`i18n.T(ctx, "key")` in both en/ru), sing-box-extended MTProxy inbound.

## Global Constraints

- **AGENTS.md is the law.** Re-read rules 1, 2, 6, 9 before starting.
- **i18n:** every new user-facing string goes into BOTH `en` and `ru` blocks in `internal/i18n/i18n.go` (rule 1). Never hardcode English UI text.
- **Build sequence:** after any `.templ` edit → `templ generate` → `go build ./...`. After any Go edit → `go build ./...`. Run `go test ./internal/web/ ./internal/chain/` at the end of each task that touches those packages.
- **Frozen protocols:** TUIC (AGENTS.md #6) and Hysteria2 (#11) — do NOT test/fix. MTProxy is a product target (NOT frozen) — actively reworked here.
- **No standalone per-client routing changes for non-AWG.** The `ForUsers`-ignored-by-vless/tuic/xhttp gap (audit Q7) is out of scope.
- **One commit per task** (or per coherent sub-step). Commit format: `feat(clients): ...` / `fix(clients): ...` / `refactor(clients): ...`, end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- **Migration is one-shot and idempotent.** `storeFile.MtproxyUsers` → migrated to `Users`, slice cleared, `.bak` written once. Re-running the binary after migration is a no-op.
- **`model.MtproxyUser` struct stays defined through Task 2 (migration), removed in Task 8 (cleanup).** Do not delete it before the migration helper is written and tested.
- **Profiles / ClientAssignments are NOT touched** (subproject C owns them).

---

## File structure (touched by this plan)

```
internal/domain/model/panel.go         # +User MTProxy fields; MtproxyUser deleted in cleanup
internal/chain/store.go                # migrateMtproxyUsers + backup; ListMTProxyUsers*; SaveUser uniqueness; delete Mtproxy* methods
internal/chain/store_test.go           # NEW: migration + uniqueness tests
internal/chain/mtproxy.go              # buildMTProxyInbound / mtproxyUsersForNode → []*model.User
internal/chain/mtproxy_test.go         # migrate tests to []*model.User
internal/chain/merged_config.go        # mtproxyUsers []*model.User signature
internal/chain/applier.go              # ListMTProxyUsersForNode
internal/web/mtproxy.go               # DELETED
internal/web/users.go                 # MTProxy form fields; scheduleAutoApplyForUser MTProxyNodes; handleGenerateMTProxySecret; handleUserConfig/QR MTProxy links
internal/web/profiles.go               # handleClients rewritten as CRUD page
internal/web/server.go                 # routes: remove /ui/mtproxy/*; redirect /ui/users; add generate-mtproxy-secret
internal/web/handlers_clients_test.go  # NEW: unified clients page tests
internal/i18n/i18n.go                  # new keys en+ru
web/templates/mtproxy.templ            # DELETED
web/templates/users.templ              # UserForm MTProxy section; table MTProxy badge
web/templates/nodes.templ              # replace 3× "Create first user"
web/templates/base.templ               # nav → single Clients
web/static/js/app.js                   # MTProxy section toggle (if needed)
```

---

## Task 1 — Model: add MTProxy fields to User

**Goal:** extend `model.User` with the MTProxy credential block. `MtproxyUser` struct stays (used by migration in Task 2). Build stays green.

**Files:**
- Modify: `internal/domain/model/panel.go:9-64` (User struct)

**Interfaces:**
- Produces: `model.User.MTProxySecret`, `User.MTProxyDomain`, `User.MTProxyOrderIndex`, `User.MTProxyNodes` — consumed by Task 2 (migration), Task 3 (renderer), Task 4 (handlers).

- [ ] **Step 1: Add the MTProxy block to User**

In `internal/domain/model/panel.go`, after the existing AWG-creds block (around line 53, after `AWGAddress`), add:

```go
// MTProxy (Telegram FakeTLS) credentials. Optional — set when the user is
// also an MTProxy client. Empty MTProxySecret = user is not an MTProxy
// client on any node. MTProxyNodes lists the node IDs this user is an
// MTProxy client on (replaces the old per-node MtproxyUser.NodeID).
MTProxySecret     string   `json:"mtproxy_secret,omitempty"`      // 32 hex chars (16 random bytes)
MTProxyDomain     string   `json:"mtproxy_domain,omitempty"`      // FakeTLS SNI, default "disk.yandex.ru"
MTProxyOrderIndex int      `json:"mtproxy_order_index,omitempty"`
MTProxyNodes      []string `json:"mtproxy_nodes,omitempty"`        // node IDs this user is an MTProxy client on
```

Keep the struct aligned with the surrounding field style (`json:"..."` tags, alignment).

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: compiles (new fields default to zero; no consumer changes yet).

- [ ] **Step 3: Commit**

```bash
git add internal/domain/model/panel.go
git commit -m "feat(clients): add MTProxy credential block to model.User"
```

(With trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` — add to every commit in this plan.)

---

## Task 2 — Store: migration + ListMTProxyUsers* + uniqueness + delete Mtproxy* methods

**Goal:** auto-migrate `MtproxyUsers` → `Users` on load (with backup), add the two new User-methods, enforce MTProxy-secret uniqueness per node in `SaveUser`, delete the old `Mtproxy*` store methods.

**Files:**
- Modify: `internal/chain/store.go`
- Create: `internal/chain/store_migration_test.go`

**Interfaces:**
- Consumes: `model.User.MTProxy*` (Task 1), existing `storeFile.MtproxyUsers` slice + `model.MtproxyUser` struct.
- Produces: `(*Store).migrateMtproxyUsers(sf *storeFile)`, `(*Store).ListMTProxyUsers() []*model.User`, `(*Store).ListMTProxyUsersForNode(nodeID string) []*model.User`, `SaveUser` uniqueness check returning error on `(MTProxyNodeID, MTProxySecret)` collision.

- [ ] **Step 1: Write the migration + uniqueness tests (RED)**

Create `internal/chain/store_migration_test.go`:

```go
package chain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func newStoreWith(t *testing.T, mtp []*model.MtproxyUser) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	st := NewStore(path)
	// Inject legacy MtproxyUsers directly via the storeFile before Load migrates.
	// We write a raw JSON file so NewStore's Load runs the migration.
	st.mu.Lock()
	sf := &storeFile{MtproxyUsers: mtp}
	if err := st.writeStore(sf); err != nil {
		t.Fatalf("writeStore: %v", err)
	}
	st.mu.Unlock()
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
	// MtproxyUsers slice cleared + a .bak file written
	if mtp, _ := st.ListMtproxyUsers(); len(mtp) != 0 {
		t.Errorf("legacy slice not cleared: %d", len(mtp))
	}
	// (ListMtproxyUsers is removed in a later step of this task; here it still exists
	// temporarily — if it's already removed, drop this assertion.)
}

func TestMigrateMtproxyUsers_Idempotent(t *testing.T) {
	st := newStoreWith(t, []*model.MtproxyUser{
		{ID: "m1", NodeID: "n1", Name: "alice", SecretHex: "83b231c9ccf32ef09f48c8f63765ab4f", FakeTLSDomain: "disk.yandex.ru", Enabled: true},
	})
	_ = st.ListUsers() // triggers migration
	bak := strings.TrimSuffix(st.storePath, ".json") + ".prebmigrate.bak" // placeholder name
	_ = bak
	// Second load: no double-migration, no second backup.
	st2 := NewStore(st.storePath)
	users, _ := st2.ListUsers()
	if len(users) != 1 {
		t.Errorf("idempotent: want 1 user, got %d", len(users))
	}
}

func TestSaveUser_MTProxySecretUniquePerNode(t *testing.T) {
	st := newStoreWith(t, nil) // empty store, no legacy
	_ = st.ListUsers()          // run migration (no-op)
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
	_ = st.ListUsers()
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
```

Note: the test references `storeFile`, `writeStore`, `readStore`, `ListMtproxyUsers` which exist in `store.go`. The `.bak` filename is set by the migration helper — adjust the test's placeholder to match Step 2's actual filename once chosen (see below). The `Idempotent` test's `bak` line is illustrative; you may simplify it to just assert the user count stays 1.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/chain/ -run "TestMigrate|TestSaveUser_MTProxy|TestListMTProxyUsersForNode" -v`
Expected: FAIL (no `migrateMtproxyUsers`, no `ListMTProxyUsers*`, `SaveUser` has no uniqueness check).

- [ ] **Step 3: Implement migration helper + ListMTProxyUsers* + uniqueness**

In `internal/chain/store.go`:

3a. Add a migration helper (place near `NewStore`/the `Load`/constructor):

```go
// migrateMtproxyUsers converts legacy storeFile.MtproxyUsers into Users with
// MTProxy* fields. Idempotent: no-op when MtproxyUsers is empty. Writes a
// one-shot .bak backup before the first migration.
func (s *Store) migrateMtproxyUsers(sf *storeFile) error {
	if len(sf.MtproxyUsers) == 0 {
		return nil
	}
	// One-shot backup (only if not already backed up this run).
	bakPath := s.storePath + ".prebmigrate.bak"
	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		if data, err := os.ReadFile(s.storePath); err == nil {
			_ = os.WriteFile(bakPath, data, 0o600)
		}
	}
	existingNames := map[string]bool{}
	existingIDs := map[string]bool{}
	for _, u := range sf.Users {
		existingNames[u.Name] = true
		existingIDs[u.ID] = true
	}
	for _, m := range sf.MtproxyUsers {
		id := m.ID
		if existingIDs[id] {
			id = m.ID + "_mtp"
		}
		name := m.Name
		if existingNames[name] {
			name = m.Name + " (MTProxy @" + m.NodeID + ")"
		}
		domain := m.FakeTLSDomain
		if domain == "" {
			domain = "disk.yandex.ru"
		}
		sf.Users = append(sf.Users, &model.User{
			ID:                id,
			Name:              name,
			Active:            m.Enabled,
			CreatedAt:         m.CreatedAt,
			MTProxySecret:     m.SecretHex,
			MTProxyDomain:     domain,
			MTProxyOrderIndex: m.OrderIndex,
			MTProxyNodes:      []string{m.NodeID},
		})
		existingNames[name] = true
		existingIDs[id] = true
	}
	sf.MtproxyUsers = nil
	return nil
}
```

3b. Call the migration in the store's load path. Find where `readStore`/`writeStore` are used in `NewStore` (or the first load). The cleanest hook: add a `Load()` method (if not present) or call migration inside `NewStore` after constructing the store. Look at `NewStore` — if it doesn't load the file eagerly, add an explicit `s.migrate()` call that reads the store, migrates, and writes back if changed. Concretely, add to `NewStore` (after `s.storePath` is set):

```go
// One-shot legacy MtproxyUser → User migration (idempotent).
func (s *Store) migrateOnce() {
	s.mu.Lock()
	defer s.mu.Unlock()
	sf, err := s.readStore()
	if err != nil || len(sf.MtproxyUsers) == 0 {
		return
	}
	if err := s.migrateMtproxyUsers(sf); err != nil {
		return
	}
	_ = s.writeStore(sf)
}
```

And call `s.migrateOnce()` at the end of `NewStore` (add `func NewStore(path string) *Store { ... s.migrateOnce(); return s }`). Ensure `NewStore` returns `*Store`.

3c. Add the two new User-methods:

```go
// ListMTProxyUsers returns all Users that have MTProxySecret set.
func (s *Store) ListMTProxyUsers() []*model.User {
	all, _ := s.ListUsers()
	out := make([]*model.User, 0, len(all))
	for _, u := range all {
		if u.MTProxySecret != "" {
			out = append(out, u)
		}
	}
	return out
}

// ListMTProxyUsersForNode returns Users whose MTProxyNodes contains nodeID.
func (s *Store) ListMTProxyUsersForNode(nodeID string) []*model.User {
	all, _ := s.ListUsers()
	out := make([]*model.User, 0)
	for _, u := range all {
		for _, n := range u.MTProxyNodes {
			if n == nodeID {
				out = append(out, u)
				break
			}
		}
	}
	return out
}
```

3d. Add MTProxy-secret uniqueness to `SaveUser`. Find `SaveUser` (`store.go:302`), and after the existing insert/replace logic (or just before `writeStore`), add:

```go
// MTProxy secret uniqueness: reject if another user shares the same secret AND
// an overlapping MTProxyNode. Self (same ID on update) is allowed.
if u.MTProxySecret != "" && len(u.MTProxyNodes) > 0 {
	for _, ex := range sf.Users {
		if ex.ID == u.ID {
			continue
		}
		if ex.MTProxySecret != u.MTProxySecret {
			continue
		}
		for _, n := range u.MTProxyNodes {
			for _, en := range ex.MTProxyNodes {
				if en == n {
					return fmt.Errorf("store: mtproxy secret already used on node %s", n)
				}
			}
		}
	}
}
```

Place this check BEFORE the `writeStore(sf)` call inside `SaveUser`'s locked section. Make sure `sf` is the freshly-read storeFile (the existing `SaveUser` reads `sf` via `readStore` at its top — use that `sf`).

- [ ] **Step 4: Delete the old Mtproxy* store methods**

Remove from `internal/chain/store.go`: `SaveMtproxyUser`, `ListMtproxyUsers`, `ListMtproxyUsersForNode`, `DeleteMtproxyUser` (lines ~965-1050). Keep `storeFile.MtproxyUsers` field for now (migration reads it; will be removed in Task 8 cleanup). Keep `model.MtproxyUser` struct (referenced by migration + tests through Task 2).

Update the `TestMigrateMtproxyUsers` assertion that calls `st.ListMtproxyUsers()` — since you just deleted that method, change that assertion to read the storeFile directly via a test helper or drop it (the slice is cleared by `migrateMtproxyUsers` setting `sf.MtproxyUsers = nil`). Simplest: remove those 3 lines (the `if mtp, _ := st.ListMtproxyUsers(); ...` block) and instead assert idempotency via the second-load user count (already covered by `TestMigrateMtproxyUsers_Idempotent`).

- [ ] **Step 5: Run tests to verify they pass (GREEN)**

Run: `go test ./internal/chain/ -run "TestMigrate|TestSaveUser_MTProxy|TestListMTProxyUsersForNode" -v`
Expected: PASS.

Run: `go build ./...`
Expected: compile errors ONLY in callers of the deleted `Mtproxy*` methods (`internal/web/mtproxy.go`, `internal/chain/applier.go:327`, `internal/chain/merged_config.go`, `internal/chain/mtproxy.go`, `internal/web/profiles.go:173`). These are fixed in Task 3+ — leave them broken only if you commit Task 2 in isolation, but since Task 3 follows immediately, the build will be green after Task 3. **Decision:** do NOT commit Task 2 alone if it breaks the build — combine Task 2 + Task 3 into one commit, OR keep the deleted methods as thin wrappers delegating to User methods (temporary) so the build stays green. **Chosen approach:** keep the build green by NOT deleting the methods in Step 4 yet — instead, in Step 4, leave the old methods in place (they still compile, just unused after Task 3 rewires callers). Delete them in Task 8 cleanup. So: **skip Step 4's deletion**; keep `SaveMtproxyUser` etc. for now. Update the test file: keep the `ListMtproxyUsers()` assertion (it still works).

Revised Step 4: **Do NOT delete the old methods yet.** The migration test's `ListMtproxyUsers()` assertion stays valid. Deletion moves to Task 8.

- [ ] **Step 6: Commit**

```bash
git add internal/chain/store.go internal/chain/store_migration_test.go
git commit -m "feat(clients): auto-migrate MtproxyUsers to User + ListMTProxyUsers* + secret uniqueness"
```

---

## Task 3 — Applier / merged_config / mtproxy.go: switch to []*model.User

**Goal:** the MTProxy inbound renderer + merged-config builder consume `[]*model.User` instead of `[]*model.MtproxyUser`.

**Files:**
- Modify: `internal/chain/mtproxy.go` (buildMTProxyInbound, mtproxyUsersForNode)
- Modify: `internal/chain/merged_config.go` (buildMergedNodeConfig signature + callers)
- Modify: `internal/chain/applier.go:327` (ListMTProxyUsersForNode)

**Interfaces:**
- Consumes: `model.User.MTProxySecret/MTProxyDomain/Active` (Task 1), `Store.ListMTProxyUsersForNode` (Task 2).
- Produces: `buildMTProxyInbound(port int, tag string, users []*model.User) json.RawMessage`, `mtproxyUsersForNode(users []*model.User) []*model.User`, `buildMergedNodeConfig(..., mtproxyUsers []*model.User)`.

- [ ] **Step 1: Rewrite mtproxy.go**

In `internal/chain/mtproxy.go`, change both functions to `[]*model.User`:

```go
// buildMTProxyInbound renders a sing-box MTProxy inbound from the node's MTProxy
// users. Each active user becomes a users[] entry with the extended "ee"+hex
// secret. Returns nil (no inbound) when there are no active users with a
// non-empty secret.
func buildMTProxyInbound(port int, tag string, users []*model.User) json.RawMessage {
	mtUsers := make([]config.MTProxyUser, 0, len(users))
	for _, u := range users {
		if !u.Active {
			continue
		}
		secret, err := MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)
		if err != nil {
			continue
		}
		name := u.Name
		if name == "" {
			name = u.ID
		}
		mtUsers = append(mtUsers, config.MTProxyUser{Name: name, Secret: secret})
	}
	if len(mtUsers) == 0 {
		return nil
	}
	inb := config.MTProxyInbound{
		Type:                        "mtproxy",
		Tag:                         tag,
		Listen:                      "0.0.0.0",
		ListenPort:                  port,
		Concurrency:                 8192,
		DomainFrontingPort:          443,
		DomainFrontingProxyProtocol: false,
		PreferIP:                    "prefer-ipv4",
		AutoUpdate:                  true,
		AllowFallbackOnUnknownDC:    false,
		TolerateTimeSkewness:        "3s",
		IdleTimeout:                 "5m",
		HandshakeTimeout:            "10s",
		Users:                       mtUsers,
	}
	data, _ := json.Marshal(inb)
	return data
}

// mtproxyUsersForNode filters users down to active ones with a non-empty
// MTProxySecret. Returns nil when none qualify.
func mtproxyUsersForNode(users []*model.User) []*model.User {
	if len(users) == 0 {
		return nil
	}
	out := make([]*model.User, 0, len(users))
	for _, u := range users {
		if u.Active && u.MTProxySecret != "" {
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
```

- [ ] **Step 2: Update merged_config.go signature**

In `internal/chain/merged_config.go`, change every `mtproxyUsers []*model.MtproxyUser` to `mtproxyUsers []*model.User`. There are occurrences at the `buildMergedNodeConfig` signature (line ~54, ~70) and any wrapper (the `RenderMerged...` helper at ~46). Grep `MtproxyUser` in merged_config.go and replace all with `User` in the mtproxy-users parameter. The body that calls `mtproxyUsersForNode(mtproxyUsers)` and `buildMTProxyInbound(...)` is unchanged (the functions now take `[]*model.User`).

- [ ] **Step 3: Update applier.go:327**

In `internal/chain/applier.go`, change:
```go
mtproxyUsers, _ := store.ListMtproxyUsersForNode(node.ID)
```
to:
```go
mtproxyUsers := store.ListMTProxyUsersForNode(node.ID)
```
(`ListMTProxyUsersForNode` returns `[]*model.User`, no error — Task 2.) The variable is passed to `buildMergedNodeConfig` which now expects `[]*model.User` — type matches.

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: compiles. If `internal/web/profiles.go:173` (`mtp, _ := st.ListMtproxyUsers()`) or `internal/web/mtproxy.go` still compile (they do — old methods kept per Task 2 revised Step 4), fine. `handleClients` is rewritten in Task 5; `mtproxy.go` deleted in Task 6.

- [ ] **Step 5: Migrate mtproxy_test.go**

In `internal/chain/mtproxy_test.go`, replace every `[]*model.MtproxyUser{...}` with `[]*model.User{...}` using the new field names. The three test functions (`TestBuildMTProxyInbound`, `TestBuildMTProxyInbound_NoEnabledUsersReturnsNil`, `TestBuildMergedNodeConfig_MTProxyStandalone`, `TestBuildMergedNodeConfig_MTProxyChainEntry`) become:

```go
users := []*model.User{
	{ID: "u1", Name: "alice", MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", Active: true},
	{ID: "u2", Name: "bob", MTProxySecret: "00112233445566778899aabbccddeeff", MTProxyDomain: "www.bing.com", Active: true},
	{ID: "u3", Name: "off", MTProxySecret: "abc", MTProxyDomain: "x.com", Active: false},
}
```
And the `NoEnabledUsersReturnsNil` cases:
```go
{"all-disabled", []*model.User{{Name: "x", MTProxySecret: "ab", Active: false}}},
{"no-secret", []*model.User{{Name: "x", MTProxySecret: "", Active: true, MTProxyDomain: "d.com"}}},
```

- [ ] **Step 6: Run tests**

Run: `go test ./internal/chain/ -run "TestBuildMTProxy|TestBuildMergedNodeConfig_MTProxy" -v`
Expected: PASS.

Run: `go test ./internal/chain/`
Expected: PASS (full chain suite).

- [ ] **Step 7: Commit**

```bash
git add internal/chain/mtproxy.go internal/chain/merged_config.go internal/chain/applier.go internal/chain/mtproxy_test.go
git commit -m "refactor(clients): MTProxy inbound renderer + merged-config use []*model.User"
```

---

## Task 4 — Handlers: extend users.go (MTProxy fields + auto-apply + generate secret + config/QR links)

**Goal:** the User create/update handlers read MTProxy form fields; `scheduleAutoApplyForUser` covers `MTProxyNodes`; a Generate-secret endpoint; `handleUserConfig`/`handleUserQR` emit MTProxy links.

**Files:**
- Modify: `internal/web/users.go` (handleCreateUser, handleUpdateUser, scheduleAutoApplyForUser, handleUserConfig, handleUserQR; add handleGenerateMTProxySecret)
- Modify: `internal/web/server.go` (add route `POST /ui/users/generate-mtproxy-secret`)

**Interfaces:**
- Consumes: `model.User.MTProxy*` (Task 1), `Store.SaveUser` uniqueness (Task 2), `chain.GenerateMTProxySecret` (exists in cryptogen.go:155), `chain.MTProxyFullSecret` (exists).
- Produces: `handleGenerateMTProxySecret` handler; extended `scheduleAutoApplyForUser` covering `u.MTProxyNodes`; `handleUserConfig` MTProxy link generation.

- [ ] **Step 1: Read MTProxy fields in handleCreateUser**

In `internal/web/users.go`, inside `handleCreateUser` (after `secretType := ...` around line 56), add:

```go
mtproxyEnabled := r.FormValue("mtproxy_enabled") == "on"
mtproxySecret := strings.TrimSpace(r.FormValue("mtproxy_secret"))
mtproxyDomain := strings.TrimSpace(r.FormValue("mtproxy_domain"))
mtproxyOrderStr := strings.TrimSpace(r.FormValue("mtproxy_order_index"))
mtproxyNodes := r.Form["mtproxy_nodes"]
```

After building `u` and before `chain.EnsureUserCreds(u)`, add:

```go
if mtproxyEnabled || mtproxySecret != "" {
	u.MTProxySecret = mtproxySecret
	if mtproxyDomain == "" {
		mtproxyDomain = "disk.yandex.ru"
	}
	u.MTProxyDomain = mtproxyDomain
	if n, _ := strconv.Atoi(mtproxyOrderStr); n != 0 {
		u.MTProxyOrderIndex = n
	}
	u.MTProxyNodes = mtproxyNodes
}
```

Add `"strconv"` to imports if not present.

- [ ] **Step 2: Read MTProxy fields in handleUpdateUser**

In `handleUpdateUser` (after `u.SecretType = ...` around line 146), add the same reads + assignment (overwrite `u.MTProxySecret/Domain/OrderIndex/MTProxyNodes` from the form; if `mtproxy_enabled` is off and `mtproxy_secret` is empty, clear the MTProxy fields: `u.MTProxySecret = ""; u.MTProxyDomain = ""; u.MTProxyOrderIndex = 0; u.MTProxyNodes = nil`).

- [ ] **Step 3: Handle SaveUser uniqueness error in both handlers**

`SaveUser` now returns an error on `(node,secret)` collision. In both `handleCreateUser` and `handleUpdateUser`, change the `SaveUser` call to surface a 400 on this error:

```go
if err := st.SaveUser(u); err != nil {
	if strings.Contains(err.Error(), "mtproxy secret already used") {
		http.Error(w, i18n.T(r.Context(), "mtproxy secret already used on node %s"), http.StatusBadRequest)
		return
	}
	http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "save: %v"), err), http.StatusInternalServerError)
	return
}
```

(The i18n key with `%s` is interpolated by the handler via `fmt.Sprintf` if you want the node id in the message — but `http.Error` + i18n.T returns the template string; acceptable to show the generic message. Keep it simple: render the i18n key string as the message; the operator sees "mtproxy secret already used on node %s" which is clear enough, OR better — pass the node: change to `http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "mtproxy secret already used on node %s"), nodeID), 400)`. Extract nodeID from `err.Error()` if needed, or just render the generic 400 with the i18n key. **Chosen:** render the i18n key string verbatim — simpler and the 400 status is the signal.)

- [ ] **Step 4: Extend scheduleAutoApplyForUser for MTProxyNodes**

In `internal/web/users.go`, find `scheduleAutoApplyForUser` (line ~192). After the existing standalone-inbounds loop (the `for _, node := range nodes` block ending around line ~215), add:

```go
// MTProxy nodes: redeploy every node this user is an MTProxy client on
// (and, on update/delete, nodes it used to be on — the caller passes the
// post-save user; for delete the user still carries its MTProxyNodes).
for _, n := range u.MTProxyNodes {
	chain.ScheduleAutoApply(n, reason+":mtproxy")
}
```

- [ ] **Step 5: Add handleGenerateMTProxySecret**

Append to `internal/web/users.go`:

```go
// handleGenerateMTProxySecret renders an <input> prefilled with a fresh 32-hex
// MTProxy secret. HTMX swaps the empty secret field with this fragment.
func (s *Server) handleGenerateMTProxySecret(w http.ResponseWriter, r *http.Request) {
	secret := chain.GenerateMTProxySecret()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<input type="text" name="mtproxy_secret" class="input input-bordered join-item font-mono" value="%s" maxlength="32" />`, secret)
}
```

- [ ] **Step 6: Add route**

In `internal/web/server.go`, in the Users routes block (around line 217-222), add:
```go
mux.HandleFunc("POST /ui/users/generate-mtproxy-secret", s.auth(s.handleGenerateMTProxySecret))
```

- [ ] **Step 7: handleUserConfig MTProxy links**

In `handleUserConfig` (`users.go:239`), after the existing protocol-link generation, add an MTProxy block. Find where the function builds the list of links/configs (it iterates chains + standalone inbounds). Add, before the final render:

```go
// MTProxy client links: one per node in MTProxyNodes that has an mtproxy
// inbound (or the default 443). Build tg:// + https proxy links.
if u.MTProxySecret != "" && len(u.MTProxyNodes) > 0 {
	for _, nodeID := range u.MTProxyNodes {
		host, err := st.GetHost(nodeID)
		if err != nil {
			continue
		}
		addr := host.Addr
		if i := strings.Index(addr, ":"); i > 0 {
			addr = addr[:i] // strip SSH port — MTProxy uses its own port
		}
		port := 443
		// Look for an mtproxy inbound on this node to use its port.
		if info, err := st.GetNodeInfo(nodeID); err == nil {
			for _, ib := range info.Inbounds {
				if ib.Protocol == "mtproxy" && ib.Port > 0 {
					port = ib.Port
					break
				}
			}
		}
		fullSecret, err := chain.MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)
		if err != nil {
			continue
		}
		// Append to the existing links structure — match how handleUserConfig
		// accumulates links (look at the surrounding code for the exact slice
		// variable name, e.g. `links = append(links, ...)` or a builder).
		// Use the same pattern; here shown as a pseudo-append:
		mtpLink := fmt.Sprintf("tg://proxy?server=%s&port=%d&secret=%s", addr, port, fullSecret)
		// TODO: integrate into the existing links slice / template payload.
		_ = mtpLink
	}
}
```

**Note for the implementer:** the exact integration depends on how `handleUserConfig` structures its output (it may return a `templates.UserConfig(...)` or build HTML). Read the function body and append the MTProxy link(s) using the same pattern (e.g. add to the `links` slice or the config payload struct). Do NOT leave the `_ = mtpLink` placeholder — wire it into the actual output. If `handleUserConfig` renders a template that takes a slice of links, append `mtpLink` to that slice. The test in Task 5 will assert the link appears for an MTProxy user.

- [ ] **Step 8: Build + run existing tests**

Run: `go build ./...`
Run: `go test ./internal/web/`
Expected: PASS (existing tests; new fields default to zero, no test asserts on MTProxy form fields yet — Task 5 adds them).

- [ ] **Step 9: Commit**

```bash
git add internal/web/users.go internal/web/server.go
git commit -m "feat(clients): User handlers read MTProxy fields, auto-apply MTProxyNodes, generate secret, MTProxy config links"
```

---

## Task 5 — handleClients rewritten as CRUD page + Clients tests

**Goal:** `/ui/clients` becomes the single management page (was read-only).

**Files:**
- Modify: `internal/web/profiles.go:169` (handleClients)
- Create: `internal/web/handlers_clients_test.go`

**Interfaces:**
- Consumes: `Store.ListUsers()` (returns `[]*model.User` including migrated MTProxy users), `templates.Users`/`templates.UserForm` (existing, extended in Task 7).
- Produces: a CRUD `handleClients` rendering `templates.Users(users, chains)` (the existing Users-list component, renamed-labeled in Task 7) with an "Add Client" button.

- [ ] **Step 1: Rewrite handleClients**

In `internal/web/profiles.go`, replace the body of `handleClients` (lines 169-216) with:

```go
func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	users, _ := st.ListUsers()
	chains, _ := st.ListChains()
	if users == nil {
		users = []*model.User{}
	}
	s.render(w, r, templates.ClientsPage(users, chains))
}
```

Delete the `unifiedClientRow` struct (lines 158-165) and the `simpleHTML`-based rendering — replaced by the `ClientsPage` template (Task 7). If `templates.ClientsPage` does not exist yet (Task 7 creates it), temporarily render `templates.Users(users, chains)` here and rename in Task 7. **Chosen:** render `templates.Users(users, chains)` for now; Task 7 renames `Users` → `ClientsPage` (or adds a wrapper). Keep the build green.

- [ ] **Step 2: Write clients tests**

Create `internal/web/handlers_clients_test.go`:

```go
package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_ClientsPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	// Create a user via the helper.
	ts.createUser("u1", "alice")
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "alice")
}

func TestHandler_ClientsPage_ShowsMTProxyBadge(t *testing.T) {
	ts := newTestServer(t)
	// Inject an MTProxy user directly via the store.
	st := chain.NewStore(ts.storePath)
	_ = st.SaveUser(&model.User{ID: "m1", Name: "mtp-alice", Active: true, MTProxySecret: "83b231c9ccf32ef09f48c8f63765ab4f", MTProxyDomain: "disk.yandex.ru", MTProxyNodes: []string{"n1"}})
	w := ts.get("/ui/clients")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "mtp-alice")
}

func TestHandler_CreateUser_WithMTProxy(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"id":             {"u1"},
		"name":           {"alice"},
		"mtproxy_enabled": {"on"},
		"mtproxy_secret": {"83b231c9ccf32ef09f48c8f63765ab4f"},
		"mtproxy_domain": {"disk.yandex.ru"},
		"mtproxy_nodes":  {"n1"},
	}
	w := ts.post("/ui/users", form)
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	u, err := st.GetUser("u1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if u.MTProxySecret != "83b231c9ccf32ef09f48c8f63765ab4f" {
		t.Errorf("MTProxySecret not saved: %q", u.MTProxySecret)
	}
	if len(u.MTProxyNodes) != 1 || u.MTProxyNodes[0] != "n1" {
		t.Errorf("MTProxyNodes: %v", u.MTProxyNodes)
	}
}

func TestHandler_CreateUser_RejectsDuplicateMTProxySecret(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	first := url.Values{"id": {"u1"}, "name": {"alice"}, "mtproxy_enabled": {"on"}, "mtproxy_secret": {"83b231c9ccf32ef09f48c8f63765ab4f"}, "mtproxy_domain": {"disk.yandex.ru"}, "mtproxy_nodes": {"n1"}}
	if w := ts.post("/ui/users", first); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body.String())
	}
	dup := url.Values{"id": {"u2"}, "name": {"bob"}, "mtproxy_enabled": {"on"}, "mtproxy_secret": {"83b231c9ccf32ef09f48c8f63765ab4f"}, "mtproxy_domain": {"disk.yandex.ru"}, "mtproxy_nodes": {"n1"}}
	w := ts.post("/ui/users", dup)
	ts.assertStatus(w, http.StatusBadRequest)
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/web/ -run "TestHandler_ClientsPage|TestHandler_CreateUser_WithMTProxy|TestHandler_CreateUser_RejectsDuplicate" -v`
Expected: PASS (the `createUser` helper exists in `handlers_mutation_test.go:24` and POSTs to `/ui/users`; the MTProxy test uses a full form).

Run: `go test ./internal/web/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/web/profiles.go internal/web/handlers_clients_test.go
git commit -m "feat(clients): /ui/clients rewritten as the single CRUD clients page"
```

---

## Task 6 — Delete mtproxy.go handlers + routes; redirect /ui/users

**Goal:** remove the old MTProxy CRUD handlers and their routes; redirect the old Users list URL to Clients.

**Files:**
- Delete: `internal/web/mtproxy.go`
- Modify: `internal/web/server.go` (routes)
- Modify: `internal/web/profiles.go` (if it references deleted helpers)

**Interfaces:**
- Consumes: Task 4 (User handlers cover MTProxy), Task 5 (Clients page).
- Produces: `/ui/mtproxy/*` routes removed; `/ui/users` (GET list) → 301 to `/ui/clients`; `/ui/mtproxy` → 301 to `/ui/clients`.

- [ ] **Step 1: Delete internal/web/mtproxy.go**

```bash
git rm internal/web/mtproxy.go
```

- [ ] **Step 2: Remove mtproxy routes + redirect /ui/users**

In `internal/web/server.go`, remove the MTProxy routes block (lines ~225-232):
```go
mux.HandleFunc("GET /ui/mtproxy", s.auth(s.handleMtproxyUsers))
mux.HandleFunc("POST /ui/mtproxy", s.auth(s.handleCreateMtproxyUser))
mux.HandleFunc("GET /ui/mtproxy/new", s.auth(s.handleNewMtproxyUserForm))
mux.HandleFunc("GET /ui/mtproxy/{id}/edit", s.auth(s.handleEditMtproxyUserForm))
mux.HandleFunc("POST /ui/mtproxy/{id}/edit", s.auth(s.handleUpdateMtproxyUser))
mux.HandleFunc("DELETE /ui/mtproxy/{id}", s.auth(s.handleDeleteMtproxyUser))
mux.HandleFunc("GET /ui/mtproxy/generate-secret", s.auth(s.handleGenerateMtproxySecret))
```

Change the `GET /ui/users` route to redirect to `/ui/clients`:
```go
mux.HandleFunc("GET /ui/users", func(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/ui/clients", http.StatusMovedPermanently)
})
```
(Keep `POST /ui/users` (create), `GET /ui/users/new`, `GET /ui/users/{id}/edit`, `POST /ui/users/{id}/edit`, `DELETE /ui/users/{id}`, `GET /ui/users/{id}/config`, `GET /ui/users/{id}/qr` — these are hit by the Clients page form/row actions.)

- [ ] **Step 3: Fix any references to deleted mtproxy helpers**

Grep `findMtproxyUser\|handleMtproxy` in `internal/web/` — should be zero after deleting mtproxy.go. If `profiles.go` or `handleClients` referenced `ListMtproxyUsers`, it was rewritten in Task 5 (now uses `ListUsers`). Build to confirm.

- [ ] **Step 4: Build + test**

Run: `go build ./...`
Run: `go test ./internal/web/`
Expected: PASS. If a test asserts on `/ui/mtproxy` routes (grep `mtproxy` in `*_test.go`), migrate it to `/ui/users` or `/ui/clients` (the MTProxy test cases in Task 5 cover the new path).

- [ ] **Step 5: Commit**

```bash
git add internal/web/server.go internal/web/profiles.go
git commit -m "refactor(clients): delete mtproxy.go handlers + routes; redirect /ui/users to /ui/clients"
```

---

## Task 7 — Templates: UserForm MTProxy section + Clients page + inbound form fix + nav

**Goal:** the UI for all of the above. Run `templ generate` after EVERY `.templ` edit.

**Files:**
- Modify: `web/templates/users.templ` (UserForm MTProxy section, table badge, rename Users→ClientsPage or add wrapper)
- Modify: `web/templates/nodes.templ` (3× "Create first user" → hint + refresh)
- Modify: `web/templates/base.templ` (nav → single Clients)
- Delete: `web/templates/mtproxy.templ`
- Modify: `internal/i18n/i18n.go` (new keys en+ru)

**Interfaces:**
- Consumes: Task 4 (generate-mtproxy-secret route), Task 5 (Clients page handler), Task 6 (routes).
- Produces: `templates.ClientsPage` (or renamed `Users`), MTProxy form section, refreshed inbound user-block, single-Clients nav.

- [ ] **Step 1: UserForm MTProxy section in users.templ**

In `web/templates/users.templ`, inside `UserForm(u, chains)` (around line 150), after the chains-checkbox block and before the modal-action, add a collapsible MTProxy section:

```html
<div class="collapse collapse-arrow border border-base-300 rounded-lg mt-2">
	<input type="checkbox" name="mtproxy_enabled" class="peer" if u != nil && u.MTProxySecret != "" { checked } />
	<div class="collapse-title font-medium">
		{ i18n.T(ctx, "MTProxy (Telegram FakeTLS)") }
	</div>
	<div class="collapse-content space-y-3">
		<div class="join">
			<input type="text" name="mtproxy_secret" class="input input-bordered join-item font-mono"
				value={ if u != nil { u.MTProxySecret } } maxlength="32" placeholder={ i18n.T(ctx, "Generate MTProxy Secret") } />
			<button type="button" class="btn join-item"
				hx-post="/ui/users/generate-mtproxy-secret"
				hx-target="[name='mtproxy_secret']"
				hx-swap="outerHTML">
				{ i18n.T(ctx, "Generate") }
			</button>
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">{ i18n.T(ctx, "FakeTLS Domain") }</span></label>
			<input type="text" name="mtproxy_domain" class="input input-bordered" value={ if u != nil && u.MTProxyDomain != "" { u.MTProxyDomain } else { "disk.yandex.ru" } } />
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">{ i18n.T(ctx, "Order Index") }</span></label>
			<input type="number" name="mtproxy_order_index" class="input input-bordered" value={ if u != nil { strconv.Itoa(u.MTProxyOrderIndex) } } />
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">{ i18n.T(ctx, "MTProxy Nodes") }</span></label>
			<select name="mtproxy_nodes" multiple class="select select-bordered" size="4">
				@nodeOptionsForMTProxy(chains, u)
			</select>
		</div>
	</div>
</div>
```

Add a helper `templ nodeOptionsForMTProxy(chains []*model.Chain, u *model.User)` that lists all nodes from all chains (dedup by ID) as `<option value={node.ID} selected={contains(u.MTProxyNodes, node.ID)}>{node.ID}</option>`. If no nodes exist, render `<option disabled>{ i18n.T(ctx, "Add a node first") }</option>`. (Reuse `contains` helper if it exists in the templates package; otherwise inline a small Go helper function next to the templ function — templ supports plain Go funcs alongside.) `strconv` import: templ files can call `strconv.Itoa` if imported at the top of the file — add `"strconv"` to the imports block of `users.templ`.

- [ ] **Step 2: MTProxy badge in the clients table**

In `web/templates/users.templ`, the `Users(users, chains)` table row (the `UserRow` component or the row inside `Users`), add an MTProxy badge:

```html
if u.MTProxySecret != "" {
	<span class="badge badge-warning badge-xs">{ i18n.T(ctx, "MTProxy") }</span>
}
```
Place it next to the existing protocol badges.

- [ ] **Step 3: Rename / wrap for ClientsPage**

Either rename `Users()` → `ClientsPage()` in `users.templ`, or add a thin wrapper:
```go
templ ClientsPage(users []*model.User, chains []*model.Chain) {
	@Users(users, chains)
}
```
Update `handleClients` (Task 5) to call `templates.ClientsPage(users, chains)` instead of `templates.Users(...)`. (If you renamed, update the one call site.)

- [ ] **Step 4: Fix the inbound form "Create first user"**

In `web/templates/nodes.templ`, replace the three "Create first user" blocks (lines ~335-337, ~393-394, ~461-462 — each is a `<span>No users yet.</span><a hx-get="/ui/users/new" ...>Create first user</a>` pair) with:

```html
if len(users) == 0 {
	<div id={ fmt.Sprintf("inbound-users-%d", idx) } class="text-xs space-y-1">
		<span class="text-base-content/50">{ i18n.T(ctx, "No clients yet. Create one in the Clients page first.") }</span>
		<a href="/ui/clients" target="_blank" class="link link-primary">{ i18n.T(ctx, "Open Clients") }</a>
		<button type="button" class="btn btn-ghost btn-xs"
			hx-get={ fmt.Sprintf("/ui/nodes/%s/inbounds", info.ID) }
			hx-target={ fmt.Sprintf("#inbound-users-%d", idx) }
			hx-select={ fmt.Sprintf("#inbound-users-%d", idx) }>
			{ i18n.T(ctx, "Refresh clients") }
		</button>
	</div>
}
```

(`idx` is the existing loop variable in `NodeInboundsForm`; confirm its name by reading the template. `info.ID` is the node id. The `hx-select` ensures only this per-row user block re-renders — the protocol/port/obfuscation draft outside `#inbound-users-<idx>` is preserved.) For the non-empty case, keep the existing user-checkbox list but wrap it in `<div id={ fmt.Sprintf("inbound-users-%d", idx) }>` so the refresh target exists.

- [ ] **Step 5: Nav → single Clients**

In `web/templates/base.templ:66-69`, remove the "Users", "MTProxy Users", and "Clients" (read-only) nav items and replace with one:
```html
<li><a href="/ui/clients" class={ templ.KV("active", isActive("/ui/clients")) }>{ i18n.T(ctx, "Clients") }</a></li>
```
Keep the "Profiles" nav item (subproject C owns it). Match the existing nav-active pattern (`isActive` or similar — read the surrounding nav items).

- [ ] **Step 6: Delete mtproxy.templ**

```bash
git rm web/templates/mtproxy.templ
```

- [ ] **Step 7: i18n keys**

In `internal/i18n/i18n.go`, add to BOTH `en` and `ru` blocks (reuse existing where present — `Clients`, `Generate`, `MTProxy` likely exist; grep first):

en:
```
"MTProxy (Telegram FakeTLS)": "MTProxy (Telegram FakeTLS)",
"This client is also an MTProxy client": "This client is also an MTProxy client",
"FakeTLS Domain": "FakeTLS Domain",
"Order Index": "Order Index",
"MTProxy Nodes": "MTProxy Nodes",
"Add a node first": "Add a node first",
"No clients yet. Create one in the Clients page first.": "No clients yet. Create one in the Clients page first.",
"Open Clients": "Open Clients",
"Refresh clients": "Refresh clients",
"Generate MTProxy Secret": "Generate MTProxy Secret",
"mtproxy secret already used on node %s": "mtproxy secret already used on node %s",
```

ru:
```
"MTProxy (Telegram FakeTLS)": "MTProxy (Telegram FakeTLS)",
"This client is also an MTProxy client": "Этот клиент также MTProxy-клиент",
"FakeTLS Domain": "FakeTLS домен",
"Order Index": "Порядковый индекс",
"MTProxy Nodes": "Ноды MTProxy",
"Add a node first": "Сначала добавьте ноду",
"No clients yet. Create one in the Clients page first.": "Клиентов пока нет. Создайте на странице «Клиенты».",
"Open Clients": "Открыть Клиенты",
"Refresh clients": "Обновить клиентов",
"Generate MTProxy Secret": "Сгенерировать MTProxy-секрет",
"mtproxy secret already used on node %s": "mtproxy-секрет уже используется на ноде %s",
```

- [ ] **Step 8: templ generate + build + test**

Run: `templ generate`
Run: `go build ./...`
Run: `go test ./internal/web/ ./internal/chain/`
Expected: PASS. Fix any template-test that asserted on the old "Create first user" link (grep `Create first user\|users/new` in `*_test.go`).

- [ ] **Step 9: Commit**

```bash
git add -A
git commit -m "feat(clients): unified Clients UI — UserForm MTProxy section, inbound refresh, single Clients nav, i18n"
```

---

## Task 8 — Cleanup: delete model.MtproxyUser + old store methods + storeFile field

**Goal:** remove the now-unused `MtproxyUser` struct, the old `Mtproxy*` store methods, and the `storeFile.MtproxyUsers` field. Migration code keeps working because it's already run by the time anyone loads a migrated store (and for a fresh store, `MtproxyUsers` is nil → no-op).

**Files:**
- Modify: `internal/domain/model/panel.go` (delete `MtproxyUser` struct)
- Modify: `internal/chain/store.go` (delete `SaveMtproxyUser`/`ListMtproxyUsers`/`ListMtproxyUsersForNode`/`DeleteMtproxyUser`, delete `storeFile.MtproxyUsers` field)
- Modify: `internal/chain/store_migration_test.go` (it references `model.MtproxyUser` to seed the legacy slice — this must change)

**Interfaces:**
- Consumes: Task 2 (migration helper), Task 3 (callers no longer use `[]*model.MtproxyUser`), Task 6 (handlers deleted).
- Produces: no more `model.MtproxyUser` references in non-test code.

- [ ] **Step 1: Update the migration test to not reference model.MtproxyUser**

The test `newStoreWith` seeds `storeFile.MtproxyUsers` with `[]*model.MtproxyUser`. Since the struct is being deleted, change the test to write raw JSON with a `mtproxy_users` key (so `migrateMtproxyUsers` still has a slice to read on load) OR keep `model.MtproxyUser` defined in a test-only file. **Chosen:** keep a minimal legacy struct IN THE TEST FILE only (`store_migration_test.go`) for seeding, OR write raw JSON. Simplest: write raw JSON in `newStoreWith`:

```go
func newStoreWith(t *testing.T, legacyJSON string) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.json")
	// Write a store file with a legacy mtproxy_users slice.
	raw := `{"users":[],"mtproxy_users":` + legacyJSON + `}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return NewStore(path) // NewStore runs migrateOnce
}
```

Then update each test call: `newStoreWith(t, `[{"id":"m1","node_id":"n1","name":"alice","secret_hex":"83b231c9ccf32ef09f48c8f63765ab4f","fake_tls_domain":"disk.yandex.ru","enabled":true}]`)`. Verify the JSON field names against `model.MtproxyUser` json tags BEFORE deleting the struct (the struct has `json:"secret_hex"` etc. — check `panel.go:298-307`). The migration helper reads `sf.MtproxyUsers` which deserializes via those tags; once the struct is gone, the `storeFile.MtproxyUsers` field is gone too, so the migration helper must change to read the raw JSON directly. **This is the crux of Task 8.**

**Revised approach for Task 8:** The migration runs on store LOAD via `readStore` which deserializes `storeFile`. If we delete `storeFile.MtproxyUsers`, the migration can't read the legacy slice. **Decision: keep `storeFile.MtproxyUsers` field and `model.MtproxyUser` struct defined (in panel.go) ONLY as the deserialization target for migration, but mark them clearly as legacy.** Actually the cleanest: keep `model.MtproxyUser` and `storeFile.MtproxyUsers` in the codebase permanently as the migration input shape (they cost nothing — a struct + a slice field), and the migration drains the slice. Deleting them gains little and complicates the migration. **Final decision:** **DO NOT delete `model.MtproxyUser` or `storeFile.MtproxyUsers`.** Skip Task 8 entirely — keep them as the migration input shape. The old store CRUD methods (`SaveMtproxyUser` etc.) ARE deleted (no callers after Task 3+6) — that's the real cleanup.

- [ ] **Step 1 (revised): Delete only the old store CRUD methods**

In `internal/chain/store.go`, delete: `SaveMtproxyUser`, `ListMtproxyUsers`, `ListMtproxyUsersForNode`, `DeleteMtproxyUser` (lines ~965-1050). Keep `storeFile.MtproxyUsers` field (migration reads it) and `model.MtproxyUser` struct (deserialization target). The migration helper `migrateMtproxyUsers` is the only reader of `sf.MtproxyUsers`.

Update `store_migration_test.go`: the `TestMigrateMtproxyUsers` assertion that called `st.ListMtproxyUsers()` (already removed in Task 2 revised Step 4? — verify; if still present, remove it since the method is now deleted). The test seeds via the struct (`storeFile{MtproxyUsers: mtp}`) — keep `model.MtproxyUser` for that seeding (it's still defined). Build green.

- [ ] **Step 2: Build + test**

Run: `go build ./...`
Run: `go test ./internal/chain/ ./internal/web/`
Expected: PASS. If any non-test code still calls the deleted methods, fix it (there should be none after Task 3+6).

- [ ] **Step 3: Commit**

```bash
git add internal/chain/store.go internal/chain/store_migration_test.go
git commit -m "refactor(clients): delete old Mtproxy* store CRUD methods (migration helper is the only legacy reader)"
```

---

## Task 9 — Full build + test + manual smoke checklist

- [ ] **Step 1**

Run:
```
templ generate
go build ./...
go test ./internal/web/ ./internal/chain/ ./internal/backend/singbox/ ./internal/takeover/
```
Expected: all PASS, no TUIC/Hysteria2 tests touched.

- [ ] **Step 2: Manual smoke (user runs these against real VPSes)**

1. **Migration:** with an existing `store.json` containing MTProxy users, start the binary. Verify `store.json.prebmigrate.bak` is created, `store.json` now has those users in the `users` array with `mtproxy_*` fields, and `mtproxy_users` is `[]` or absent. Verify clients appear on `/ui/clients`.
2. **Create AWG client** on the Clients page → saves, AWG creds allocated, deploy works (existing flow).
3. **Create MTProxy client** on the Clients page (open MTProxy section, generate secret, pick a node) → saves with `MTProxyNodes=[nodeID]`, node auto-redeploys, MTProxy inbound appears in the node config.
4. **Edit MTProxy client** (change node, remove a node) → old+new nodes redeploy (diff via `scheduleAutoApplyForUser`).
5. **Config/QR for MTProxy client** → MTProxy `tg://proxy?...` link appears.
6. **Duplicate secret on same node** → 400 "mtproxy secret already used".
7. **Inbound form with no users** → shows "No clients yet / Open Clients / Refresh clients" (no modal hijack). Create a client in a new tab, click Refresh → checkbox list updates without losing the protocol/port draft.
8. **Nav** → single "Clients" item; `/ui/users` redirects to `/ui/clients`; `/ui/mtproxy` 404s (route removed) — acceptable.

- [ ] **Step 3: Final commit (if any test fixes)**

```bash
git add -A
git commit -m "test(clients): final build/test pass"
```

---

## Self-review checklist (run before marking plan done)

- [ ] Every spec section maps to ≥1 task. (Spec §"Model changes" → Task 1; §"Store layer" → Task 2; §"Applier/merged_config" → Task 3; §"Web handlers" → Task 4 + 5 + 6; §"handleUserConfig/QR" → Task 4 Step 7; §"Templates" → Task 7; §"i18n" → Task 7 Step 7; §"Migration safety" → Task 2; §"Build sequence" → Task 9.)
- [ ] No `TODO`/`TBD`/`...` placeholders left in plan code blocks (the one `// TODO: integrate` in Task 4 Step 7 must be replaced with the actual integration — flagged for the implementer; if it survives, that's a plan failure — fix it during implementation by reading `handleUserConfig` and wiring the link into the existing output slice).
- [ ] Type names match: `[]*model.User` (not `[]*model.MtproxyUser`) in Tasks 3+; `Store.ListMTProxyUsersForNode` returns `[]*model.User` (no error); `SaveUser` returns error on uniqueness.
- [ ] No TUIC/Hysteria2 code touched.
- [ ] Each task ends with `go build ./...` and a commit.
- [ ] i18n keys land in BOTH en and ru.
- [ ] `model.MtproxyUser` struct + `storeFile.MtproxyUsers` field are KEPT (Task 8 revised) as the migration input shape — not deleted.
- [ ] Old `Mtproxy*` store CRUD methods deleted in Task 8.

---

## Out of scope (deferred)

- Subproject C (per-protocol presets, custom-presets editor, QUIC capture UI, Profiles removal).
- Standalone per-client routing for vless/tuic/xhttp (`ForUsers` gap).
- Showing live status (OS/sing-box/AWG) in the nodes list table.