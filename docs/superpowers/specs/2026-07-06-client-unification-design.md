# Client Unification — Design Spec (Subproject B)

**Date:** 2026-07-06
**Subproject:** B (of A/B/C — see audit decomposition; A is DONE, C is separate)
**Status:** Draft → pending user review

---

## Problem

Four complaints from the audit, all client-model UX:

1. **Users and MTProxy-users diverged.** Two separate models (`model.User` vs `model.MtproxyUser`), two store slices, two handler files, two UI pages, two nav items. An operator must know "Telegram MTProxy client → MTProxy Users page; AWG/VLESS/TUIC client → Users page."
2. **"Clients" section is unclear.** It's a read-only merged view (`profiles.go:169` `handleClients`) duplicating rows from Users + MTProxy Users with no actions, no create, no config — a passive dashboard. Three nav items where one could suffice.
3. **Inbound form "Create first user" hijacks the modal.** The inbound form lives in `#modal-container`; the "Create first user" link (`nodes.templ:335,393,461`) ALSO targets `#modal-container`, so clicking it discards the inbound draft (protocol/port/obfuscation selections) and navigates to the unrelated user-create flow. The just-created user is NOT auto-assigned to the inbound being built.
4. **(From audit, out of scope but documented)** `NodeInbound.ForUsers` is only consumed by the AWG deploy path (`renderStandaloneAWG0Conf`); vless/tuic/xhttp standalone inbounds ignore it (emit a shared UUID). This is a per-client-routing gap for non-AWG standalone, NOT addressed by B (B is about unifying clients, not fixing standalone per-client routing).

---

## Audit Summary (facts from current code)

- `model.User` (`internal/domain/model/panel.go:9-64`): global identity. Fields: `ID, Name, Telegram, Email, ExpiresAt, Active, CreatedAt, Protocols []string, ImportedSecret/SecretType, ChainNames []string, ChainExit map[string]string, VLESSUUID, TUICUUID/Password, Hysteria2Password, AWGPrivateKey/PublicKey/Address`. Per-user AWG creds (`AWGAddress` = unique inner tunnel IP) drive per-client routing by `source_ip_cidr` (AGENTS.md #7).
- `model.MtproxyUser` (`panel.go:298-307`): per-node, single-protocol. Fields: `ID, NodeID, Name, SecretHex (32 hex), FakeTLSDomain (default disk.yandex.ru), OrderIndex, Enabled, CreatedAt`. No expiry, no chains, no per-protocol creds. `NodeID` scopes it to one node.
- Store: `storeFile.Users` (`store.go:40`) + `storeFile.MtproxyUsers` (`store.go:49`). `SaveUser/GetUser/ListUsers/DeleteUser` (`store.go:302-383`); `SaveMtproxyUser` (enforces unique `(NodeID, Name)`, `store.go:985`), `ListMtproxyUsers`, `ListMtproxyUsersForNode`, `DeleteMtproxyUser` (`store.go:968-1050`). No `GetMtproxyUser` — handlers use `findMtproxyUser` filtering `ListMtproxyUsers` (`mtproxy.go:154`).
- `chain.GenerateMTProxySecret()` (`cryptogen.go:155-159`) — 16 random bytes → 32 hex chars. `chain.MTProxyFullSecret(secretHex, domain)` (`cryptogen.go:161-169`) — `"ee"+secretHex+hex(domain)`.
- `buildMTProxyInbound(port, tag, users []*model.MtproxyUser)` (`internal/chain/mtproxy.go:26`) — each `u.Enabled` becomes a `config.MTProxyUser{Name, Secret}`; skips disabled + malformed secrets; returns nil if no users (sing-box rejects empty users[]).
- `mtproxyUsersForNode(users []*model.MtproxyUser) []*model.MtproxyUser` (`mtproxy.go:71`) — filters enabled + non-empty secret.
- `buildMergedNodeConfig(nodeInfo, nodeChains, usersByChain, usersByInbound, mtproxyUsers []*model.MtproxyUser)` (`merged_config.go:54,70`) — emits MTProxy inbound via `buildMTProxyInbound` for each `NodeInbound{Protocol:"mtproxy"}` (or a default 443 inbound if none). Filtered by `mtproxyUsersForNode`.
- `applier.go:327` — `mtproxyUsers, _ := store.ListMtproxyUsersForNode(node.ID)` feeds `buildMergedNodeConfig`.
- `mtproxy.go` handlers (`internal/web/mtproxy.go`): full CRUD; each mutation calls `chain.ScheduleAutoApply(u.NodeID, ...)` to re-deploy the affected node.
- `web/templates/mtproxy.templ`: `MtproxyUsers(users, nodes)` list + `MtproxyUserForm(u, nodes)` form. No Config/QR buttons (MTProxy clients have no client-link generation in UI).
- `web/templates/users.templ`: `Users(users, chains)` list (Config/QR/Edit/Delete) + `UserForm(u, chains)` (chains checkboxes, hidden `protocols=awg`).
- `web/templates/base.templ:66-69`: nav items Users / MTProxy Users / Clients / Profiles.
- `web/templates/nodes.templ:335,393,461`: "No users yet. / Create first user" (`hx-get="/ui/users/new" hx-target="#modal-container"` — hijacks the inbound modal).
- `model.NodeInbound.ForUsers []string` (`panel.go:184-215`) — inbound-owned user assignment. `usersByInboundMap` (`applier.go:1718-1754`) builds `map[tag][]User`; only consumed by AWG (`renderStandaloneAWG0Conf` `awg_deploy.go:219-256`). vless/tuic/xhttp standalone ignore it.
- Tests: `internal/chain/mtproxy_test.go` uses `[]*model.MtproxyUser{...}` with `SecretHex/FakeTLSDomain/Enabled`. `internal/web/handlers_mutation_test.go` has a `createMtproxyUser` helper (verify during planning).

---

## Design (chosen approach: Merge into one User)

### Principles

- **One client model.** `model.User` absorbs MTProxy credentials as an optional per-protocol credential block. `model.MtproxyUser` is deleted after a one-shot auto-migration.
- **One management page.** A single "Clients" nav item (`/ui/clients`) replaces Users + MTProxy Users + read-only Clients. Full CRUD: create/edit/delete/config/QR for all client types in one list.
- **Reference-by-assignment, not struct-scoping.** MTProxy per-node binding moves from `MtproxyUser.NodeID` (single) to `User.MTProxyNodes []string` (multi). AWG already works this way (one `AWGAddress` used on all nodes with an AWG inbound the user is assigned to).
- **Inline user creation stays OUT of the inbound modal.** The "Create first user" link is removed; replaced with a "no clients yet → open Clients page" hint + a "Refresh clients" HTMX button that re-renders only the user-checkbox block (not the whole inbound modal), so the draft is never lost.

### Model changes

1. **`model.User`** (`panel.go:9-64`) — add MTProxy credential block:

   ```go
   // MTProxy (Telegram FakeTLS) credentials. Optional — set when the user is
   // also an MTProxy client. Empty MTProxySecret = user is not an MTProxy
   // client on any node. MTProxyNodes lists the node IDs this user is an
   // MTProxy client on (replaces the old per-node MtproxyUser.NodeID).
   MTProxySecret     string   `json:"mtproxy_secret,omitempty"`      // 32 hex chars (16 random bytes)
   MTProxyDomain     string   `json:"mtproxy_domain,omitempty"`      // FakeTLS SNI, default "disk.yandex.ru"
   MTProxyOrderIndex int      `json:"mtproxy_order_index,omitempty"`
   MTProxyNodes      []string `json:"mtproxy_nodes,omitempty"`        // node IDs
   ```

   `Enabled` (MtproxyUser) maps to `User.Active` (existing field). `NodeID` (single) maps to `MTProxyNodes` (slice with one element for migrated records).

2. **`model.MtproxyUser`** (`panel.go:298-307`) — **deleted** after migration. The struct can remain defined temporarily during the migration commit (the migration helper references it), then removed in the cleanup commit. Net result: struct gone, `storeFile.MtproxyUsers` gone.

3. **Uniqueness.** The old `SaveMtproxyUser` enforced `unique (NodeID, Name)`. After merge:
   - `User.ID` stays globally unique (existing `SaveUser` upsert-by-ID).
   - `User.Name` — NOT required unique (matches existing User behavior; operators may have duplicate display names).
   - MTProxy uniqueness moves to `(MTProxyNodeID, MTProxySecret)` per node — a duplicate secret on the same node would collide in sing-box. Enforced in `SaveUser` when MTProxy fields are set: scan existing Users, for each node in `u.MTProxyNodes`, reject if another user has the same `MTProxySecret` AND the same node in its `MTProxyNodes`. This is weaker than `(NodeID, Name)` but Name-uniqueness had no technical meaning; secret-uniqueness is the real constraint.

### Store layer

1. **Migration (auto on load, one-shot).** In `NewStore`/`Load`, after `readStore`:
   - If `storeFile.MtproxyUsers` is non-empty → `migrateMtproxyUsers(storeFile)`.
   - **Backup first:** copy `store.json` → `store.json.premigrate-<unix>.bak` (only if no `.bak` exists yet for this run — guard against repeated backups on every start).
   - For each `MtproxyUser u`:
     - `ID`: if a `User` with the same ID already exists → `u.ID + "_mtp"`.
     - `Name`: if a `User` with the same Name already exists → `u.Name + " (MTProxy @" + u.NodeID + ")"`.
     - Build `&model.User{ID, Name, Active: u.Enabled, CreatedAt: u.CreatedAt, MTProxySecret: u.SecretHex, MTProxyDomain: u.FakeTLSDomain (default "disk.yandex.ru" if empty), MTProxyOrderIndex: u.OrderIndex, MTProxyNodes: []string{u.NodeID}}`.
     - Append to `storeFile.Users`.
   - Set `storeFile.MtproxyUsers = nil`.
   - `save()` immediately to persist the migration (one-shot; next load sees empty slice → no-op).
   - Idempotent: if `MtproxyUsers` is already empty, no work, no backup, no save.

2. **New User-methods for MTProxy:**
   ```go
   // ListMTProxyUsers returns all Users that have MTProxySecret set.
   func (s *Store) ListMTProxyUsers() []*model.User
   // ListMTProxyUsersForNode returns Users whose MTProxyNodes contains nodeID.
   func (s *Store) ListMTProxyUsersForNode(nodeID string) []*model.User
   ```
   Linear scan `ListUsers()` + filter. Replaces `ListMtproxyUsers` / `ListMtproxyUsersForNode`.

3. **Deleted store methods:** `SaveMtproxyUser`, `ListMtproxyUsers`, `ListMtproxyUsersForNode`, `DeleteMtproxyUser`. Callers migrate to `SaveUser`/`ListMTProxyUsers*`. `findMtproxyUser` (mtproxy.go) → replaced by `GetUser`.

4. **Uniqueness enforcement** in `SaveUser`: when `u.MTProxySecret != ""` and `len(u.MTProxyNodes) > 0`, scan existing users (excluding `u.ID` itself on update) for a user with the same secret AND an overlapping node in `MTProxyNodes`. On collision → return an error (`fmt.Errorf("mtproxy secret already used on node %s", nodeID)`). Handler renders this as a 400.

### Applier / merged_config / deploy path

1. **`internal/chain/mtproxy.go`** — `buildMTProxyInbound` and `mtproxyUsersForNode` change signature from `[]*model.MtproxyUser` to `[]*model.User`:
   - `buildMTProxyInbound(port, tag, users []*model.User)`: `u.Enabled` → `u.Active`; `u.SecretHex` → `u.MTProxySecret`; `u.FakeTLSDomain` → `u.MTProxyDomain`; `u.Name`/`u.ID` unchanged. `MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)` unchanged.
   - `mtproxyUsersForNode(users []*model.User) []*model.User`: filter `u.Active && u.MTProxySecret != ""`.

2. **`internal/chain/merged_config.go`** — `buildMergedNodeConfig` signature: `mtproxyUsers []*model.User` (was `[]*model.MtproxyUser`). Call sites `:54, :70` + the wrapper. Field access in the MTProxy branch (`:131,142,153`) unchanged logic, just typed `*model.User`.

3. **`internal/chain/applier.go:327`** — `mtproxyUsers, _ := store.ListMTProxyUsersForNode(node.ID)` (returns `[]*model.User`). Passed to `buildMergedNodeConfig`.

4. **`ScheduleAutoApply`** (currently in `mtproxy.go` handlers) moves into the User handlers (`users.go`). On `handleCreateUser`/`handleUpdateUser`/`handleDeleteUser`:
   - Compute the set of nodes to redeploy = union of (old `MTProxyNodes` before save) and (new `MTProxyNodes` after save). For create, old=∅; for delete, new=∅.
   - For each `nodeID` in the union → `chain.ScheduleAutoApply(nodeID, ...)`. This re-renders nodes where the client was removed too (diff-aware).
   - Only trigger if MTProxy fields are relevant (client has/has-any-had `MTProxySecret`); pure AWG/VLESS edits don't trigger MTProxy redeploys.

### Web handlers

1. **`internal/web/mtproxy.go` — DELETED.** All handlers removed: `handleMtproxyUsers`, `handleNewMtproxyUserForm`, `handleCreateMtproxyUser`, `handleEditMtproxyUserForm`, `handleUpdateMtproxyUser`, `handleDeleteMtproxyUser`, `handleGenerateMtproxySecret`, `findMtproxyUser`.

2. **`internal/web/users.go` — extended.**
   - `handleCreateUser`/`handleUpdateUser` read new form fields: `mtproxy_enabled` (checkbox), `mtproxy_secret`, `mtproxy_domain`, `mtproxy_order_index`, `mtproxy_nodes` (multi-select of node IDs). Validation: if `mtproxy_enabled` or `mtproxy_secret != ""` → secret must be 32 hex (or auto-generate via the Generate button); domain defaults to `disk.yandex.ru`. The uniqueness check happens in `SaveUser` (returns error → 400).
   - After `SaveUser`: compute old/new `MTProxyNodes` diff and `ScheduleAutoApply` for the union (see above).
   - New handler `handleGenerateMTProxySecret` (HTMX) at `POST /ui/users/generate-mtproxy-secret` — replaces `handleGenerateMtproxySecret`. Renders the same `<input name="mtproxy_secret" ...>` fragment (rename `secret_hex` → `mtproxy_secret`).

3. **`handleClients` (`internal/web/profiles.go:169`) — rewritten** from read-only aggregator to the full CRUD page. Loads `ListUsers()`, renders one table: Name (+ID), Protocols (badges: AWG if `AWGAddress`/assigned to chain with AWG; VLESS if `VLESSUUID`; TUIC if `TUICUUID` — but TUIC frozen, badge shown read-only; MTProxy if `MTProxySecret != ""`), Nodes (MTProxy nodes from `MTProxyNodes`; for others, the chains they're in), Expires, Status, Actions (Config/QR/Edit/Delete). "Add Client" button → `UserForm`. Optional type filter via GET `?type=mtproxy` etc.

4. **Routes (`server.go`):**
   - Remove all `/ui/mtproxy/*` routes (`:225-232`).
   - Remove `/ui/users` CRUD routes (`:215-223`)? — **Decision:** keep `/ui/users/*` routes working as a 301 redirect to `/ui/clients` for backward compat (bookmarks). The `handleCreateUser`/`handleUpdateUser`/etc. handlers STAY (they're used by the Clients page form POSTs), just the list/new routes redirect. Simpler: keep the mutation routes as-is (they're hit by the unified form), only the list page (`GET /ui/users`) → redirect to `/ui/clients`.
   - Add `POST /ui/users/generate-mtproxy-secret` (replaces `/ui/mtproxy/generate-secret`).

### `handleUserConfig` / `handleUserQR` (`users.go:239,549`)

Extended to generate MTProxy client links when `u.MTProxySecret != ""` and `len(u.MTProxyNodes) > 0`:
- For each node in `u.MTProxyNodes`: resolve the node's MTProxy inbound port (from `NodeInbound{Protocol:"mtproxy"}` on that node, or default 443), build the `tg://proxy?server=<host>&port=<port>&secret=<fullSecret>` link (and an HTTPS equivalent). `MTProxyFullSecret(u.MTProxySecret, u.MTProxyDomain)` assembles the secret.
- This is a **new capability** (audit noted MTProxy clients had no Config/QR). Added as part of unification so the unified Clients page offers Config/QR for every client type.

### Templates

1. **`web/templates/mtproxy.templ` — DELETED.**

2. **`web/templates/users.templ`** — `Users()` component renamed/stays but labeled "Clients" in nav. Table badges extended (MTProxy badge when `u.MTProxySecret != ""`). `UserForm` gains a collapsible "MTProxy (Telegram FakeTLS)" section:
   - Checkbox `mtproxy_enabled` (reveals the section via inline JS toggle).
   - Secret input + "Generate" HTMX button (`hx-post="/ui/users/generate-mtproxy-secret"` `hx-target="[name='mtproxy_secret']"` `hx-swap="outerHTML"`).
   - FakeTLS Domain input (default `disk.yandex.ru`).
   - OrderIndex number input.
   - Nodes multi-select (`name="mtproxy_nodes" multiple`) of all nodes.
   - If no nodes exist → hint "Add a node first" (MTProxy needs a node).

3. **`web/templates/nodes.templ`** — the three "Create first user" blocks (`:335, :393, :461`) replaced with:
   ```
   if len(users) == 0 {
       <span class="text-xs text-base-content/50">{ i18n.T(ctx, "No clients yet. Create one in the Clients page first.") }</span>
       <a href="/ui/clients" target="_blank" class="link link-primary text-xs">{ i18n.T(ctx, "Open Clients") }</a>
       <button type="button" class="btn btn-ghost btn-xs"
           hx-get={ "/ui/nodes/" + info.ID + "/inbounds" }
           hx-target={ "#inbound-users-" + idx }
           hx-select={ "#inbound-users-" + idx }>
           { i18n.T(ctx, "Refresh clients") }
       </button>
   }
   ```
   `hx-select` ensures only the per-row user-checkbox block re-renders — the protocol/port/obfuscation draft outside `#inbound-users-<idx>` is preserved. The `target="_blank"` link opens Clients in a new tab so the inbound modal stays open.

4. **`web/templates/base.templ:66-69`** — nav simplified: one "Clients" item (`/ui/clients`). Remove "Users", "MTProxy Users", "Clients" (read-only) → replaced by the single CRUD "Clients". Profiles nav stays (subproject C owns it).

### i18n

New keys added to BOTH `en` and `ru` blocks in `internal/i18n/i18n.go` (reuse existing where present — `Clients`, `Generate`, `MTProxy` likely exist):
- `MTProxy (Telegram FakeTLS)` / `MTProxy (Telegram FakeTLS)`
- `This client is also an MTProxy client` / `Этот клиент также MTProxy-клиент`
- `FakeTLS Domain` / `FakeTLS домен`
- `Order Index` / `Порядковый индекс`
- `MTProxy Nodes` / `Ноды MTProxy`
- `Add a node first` / `Сначала добавьте ноду`
- `No clients yet. Create one in the Clients page first.` / `Клиентов пока нет. Создайте на странице «Клиенты».`
- `Open Clients` / `Открыть Клиенты`
- `Refresh clients` / `Обновить клиентов`
- `Generate MTProxy Secret` / `Сгенерировать MTProxy-секрет`
- `mtproxy secret already used on node %s` / `mtproxy-секрет уже используется на ноде %s`

Per AGENTS.md rule 1 — never hardcode English UI text.

### Migration safety & rollback

- **Backup:** `store.json` → `store.json.premigrate-<unix>.bak` before migrating (guarded against repeated backups per run).
- **One-shot:** migration runs once; `MtproxyUsers` slice cleared and saved; subsequent loads no-op.
- **Rollback:** manual — restore the `.bak` file and downgrade the binary. No automatic rollback (the old code path is removed). This is explicit and documented.
- **Idempotent:** re-running the binary after migration is safe (empty slice → no work).

### Scope boundaries (NOT in B)

- **Profiles / ClientAssignments** (`panel.go:252-273`) — subproject C. Not deleted here. The Profiles nav item stays. `handleClients` does NOT show Profiles data (only Users).
- **AWG per-client routing** (`AWGAddress/PublicKey/PrivateKey`, `renderStandaloneAWG0Conf`) — unchanged.
- **`NodeInbound.ForUsers` semantics for non-AWG standalone** — the audit gap (vless/tuic/xhttp ignore ForUsers) is NOT fixed here. B unifies clients, not standalone per-client routing.
- **TUIC/Hysteria2** — frozen (AGENTS.md #6/#11). MTProxy is a product target (not frozen), actively reworked here.
- **Standalone per-client routing for MTProxy** — MTProxy per-client routing is by `auth_user` (the user's `Name`, per `mtproxy.go:8`), not source IP. This stays as-is; unification doesn't change MTProxy routing semantics.

---

## Files to change (summary)

**Model:**
- `internal/domain/model/panel.go` — `User` +MTProxy fields; `MtproxyUser` struct deleted (in cleanup commit).

**Store:**
- `internal/chain/store.go` — migration helper (`migrateMtproxyUsers`) + backup; `ListMTProxyUsers`/`ListMTProxyUsersForNode`; delete `SaveMtproxyUser`/`ListMtproxyUsers`/`ListMtproxyUsersForNode`/`DeleteMtproxyUser`; uniqueness check in `SaveUser`.

**Applier / config:**
- `internal/chain/mtproxy.go` — `buildMTProxyInbound`/`mtproxyUsersForNode` → `[]*model.User`.
- `internal/chain/merged_config.go` — `mtproxyUsers []*model.User` signature.
- `internal/chain/applier.go:327` — `ListMTProxyUsersForNode`.

**Web handlers:**
- `internal/web/mtproxy.go` — DELETED.
- `internal/web/users.go` — MTProxy form fields in create/update; `ScheduleAutoApply` diff; `handleGenerateMTProxySecret`; `handleUserConfig`/`handleUserQR` MTProxy links.
- `internal/web/profiles.go` — `handleClients` rewritten as CRUD page.
- `internal/web/server.go` — routes: remove `/ui/mtproxy/*`; redirect `/ui/users` → `/ui/clients`; add `POST /ui/users/generate-mtproxy-secret`.

**Templates:**
- `web/templates/mtproxy.templ` — DELETED.
- `web/templates/users.templ` — `UserForm` MTProxy section; table MTProxy badge.
- `web/templates/nodes.templ` — replace 3× "Create first user" with hint + Refresh button.
- `web/templates/base.templ` — nav → single "Clients".
- Regenerated `*_templ.go` via `templ generate`.

**i18n:**
- `internal/i18n/i18n.go` — new keys en+ru.

**Tests:**
- `internal/chain/mtproxy_test.go` — `[]*model.MtproxyUser` → `[]*model.User` with `MTProxySecret/MTProxyDomain/Active`.
- `internal/web/handlers_mutation_test.go` — `createMtproxyUser` helper (if present) → migrate to `createUser` with MTProxy fields.
- Any e2e test using `MtproxyUser` directly.

---

## Build sequence (high level — detailed plan via writing-plans skill)

1. Model: `User` +MTProxy fields (keep `MtproxyUser` struct temporarily for migration).
2. Store: migration + `ListMTProxyUsers*` + `SaveUser` uniqueness; delete old `Mtproxy*` methods.
3. Applier/merged_config/mtproxy.go: `[]*model.User`.
4. Handlers: delete `mtproxy.go`; extend `users.go`; rewrite `handleClients`; routes.
5. Templates: delete `mtproxy.templ`; extend `UserForm`; fix inbound form; nav.
6. i18n keys en+ru.
7. Delete `model.MtproxyUser` struct (cleanup commit).
8. Migrate tests (`mtproxy_test.go`, helpers, e2e).
9. `templ generate` → `go build ./...` → `go test ./...` (skip TUIC/Hysteria2 per AGENTS.md #6/#11).
10. Manual smoke: create AWG client, create MTProxy client (with multi-node), edit, config/QR for both, deploy, verify old `MtproxyUser` store migrates on first load.

---

## Out of scope (follow-ups / other subprojects)

- **C (per-protocol presets + custom presets UI + QUIC capture UI):** splitting `ConnectionPreset` per protocol, replacing dead Profiles with custom-presets editor, adding `AWGCPSMimicry="quic-live"` + `AWGCPSCaptureDomain` fields to the chain form. Separate spec.
- Standalone per-client routing for vless/tuic/xhttp (ForUsers gap) — follow-up.
- Showing live status (OS/sing-box/AWG) in the nodes list table — follow-up after subproject A's model fields.