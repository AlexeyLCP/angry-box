# SSH Key UX + Deploy Bug Fix — Design Spec

**Date:** 2026-07-04
**Subproject:** A (of A/B/C — see audit decomposition)
**Status:** Draft → pending user review

---

## Problem

Two complaints, one bug:

1. **Bug (blocker):** Deploying an inbound fails with `ssh connect: ssh: read key "": open : The system cannot find the file specified.` Root cause: `internal/web/nodes.go` `handleCreateNode`/`handleUpdateNode` read only the hidden textarea `keyPath`, ignoring the `<select name="ssh_key_id">` dropdown. The selected key is lost, `host.KeyPath` is saved empty, and `internal/ssh/client.go:84-88` calls `os.ReadFile("")` → error.
2. **Redundancy:** SSH key for a node is set in **3 places** (New Node, Edit Node, Capture Node) with inconsistent field names (`ssh_key_id` vs `ssh_key`), different server-side handling (only Capture reads its dropdown), and no shared logic.

---

## Audit Summary (facts from current code)

- `model.Host.KeyPath` (`internal/domain/model/backend.go:22`) stores one of: a filesystem path, a `password:<pass>` string, or an SSH-key registry ID (e.g. `key-auto-<unix>`, `system-<file>`). Resolved at connect time.
- `model.SSHKeyEntry` (`internal/domain/model/panel.go:83-89`): `ID, Name, KeyPath, KeyData, Source`. `Source` is one of `"stored"|"system"|"manual"` (string, no consts). Capture's `key-manual-*`/`key-auto-*` entries leave `Source` empty (`nodes.go:196,254`).
- `model.PanelSettings.SSHKeys` (`panel.go:72-80`) — registry of stored keys, persisted to `settings.json`.
- `detectSystemKeys()` (`internal/web/server.go:288-324`) — scans `~/.ssh/`, returns `[]SSHKeyEntry` with `Source:"system"`, `ID:"system-<file>"`.
- `mergeSSHKeys()` (`server.go:327-333`) — concatenates stored + system, no dedup.
- `Store.ResolveKey(keyID)` (`internal/chain/store.go:613-645`) — resolves registry ID → PEM content. Returns `("", false)` for empty ID. No default fallback.
- `handleCaptureNode` (`internal/web/nodes.go:168-299`) — reads `ssh_key`, `login_user`, `login_pass`, `auto_install_key`, `ssh_key_manual`. Auto-install (`nodes.go:236-279`) generates an ed25519 pair via `sshclient.GenerateSSHKeypair()`, calls `InstallPublicKey` to append to `~/.ssh/authorized_keys` on the remote, saves the private key to `settings.SSHKeys` as `key-auto-<unix>`, sets `host.KeyPath = keyID`.
- `handleCreateNode` (`nodes.go:51-95`) / `handleUpdateNode` (`nodes.go:111-152`) — read only `keyPath` textarea; **ignore `ssh_key_id`**. This is the bug.
- `Backend.GetStatus` (`internal/backend/singbox/singbox.go:714-747`) returns `Status{Running, Version, PID, Uptime, Error}` — no OS, no `sing-box installed` flag, no AWG kernel module status.
- `awgKernelModuleLoaded` (`singbox.go:440-446`) — private helper, `lsmod | grep amneziawg`. Used only inside `installAWGModule`.
- `InstallAWGModule`/`InstallAWGModuleWithOptions` (`singbox.go:425-438`) — called on **deploy** when `chain.UserProtocol==AWG || chain.Transport==AWG` (`applier.go:358,1848`), never on capture.
- No global "default key" concept anywhere in code/model.

---

## Design (chosen approach: Hybrid wizard)

### Principles

- **Registry is single source of truth.** A node stores `host.KeyPath` = a registry ID (or `password:<pass>`), never raw PEM.
- **One canonical entry point per lifecycle stage:**
  - Add node (new) = **Capture wizard** (verifies connectivity + installs key).
  - Edit node = metadata + key re-select (no password/textarea).
  - Manage registry = Settings → SSH Keys.
- **Reference-by-ID** (like Ansible vault / Teleport): keys live in the registry, nodes reference by ID. No per-node PEM re-entry.
- **Default key** as global fallback: if a node's `KeyPath` is empty at deploy time, applier resolves `PanelSettings.DefaultSSHKeyID`.

### Model changes

1. **`model.PanelSettings`** (`panel.go:72-80`) — add field:
   ```go
   DefaultSSHKeyID string `json:"default_ssh_key_id,omitempty"`
   ```

2. **`model.SSHKeyEntry`** (`panel.go:83-89`) — add `Source` consts near the struct (no `iota`, JSON strings):
   ```go
   const (
       SourceStored = "stored"
       SourceSystem = "system"
       SourceAuto   = "auto"
       SourceManual = "manual"
   )
   ```
   All entry points that create `SSHKeyEntry` MUST set `Source` (fixes `nodes.go:196,254` leaving it empty).

3. **`model.Status`** (`internal/domain/model/backend.go:72-78`) — add fields:
   ```go
   OS                 string
   SingBoxInstalled   bool
   AWGModuleInstalled bool
   ```

4. **`model.NodeMetrics`** (`panel.go:92-100`) — add persistent fields:
   ```go
   OS                 string
   SingBoxInstalled   bool
   AWGModuleInstalled bool
   ```

5. **`model.Host`** — no struct change. **Invariant (new):** no handler writes raw PEM to `Host.KeyPath`; only registry IDs or `password:<pass>`. The textarea `keyPath` path that allowed raw PEM is removed.

### Server-side handler changes

**Removed:** `handleCreateNode` (`nodes.go:51-95`) and its route `POST /ui/nodes`. The wizard POSTs to the existing `POST /ui/nodes/{id}/capture` route. For a **new** node, the user types a new `id` in the wizard form; `handleCaptureNode` detects "id not in store" → creates `Host`+`NodeInfo{Source:"captured"}` on successful connection. For an **existing** node (Re-capture), the same handler overwrites the stored `Host`/`NodeInfo`/`NodeMetrics`. No separate create route remains.

**`handleCaptureNode` (extended to the wizard, new + existing nodes):**
- Accepts `id` (new or existing). If new, creates `Host` + `NodeInfo{Source:"captured"}` after a successful connection.
- Reads: `ssh_key_id`, `ssh_key_manual`, `login_user`, `login_pass`, `auto_install_key`, plus `country`/`bandwidth` (new — currently capture doesn't take metadata). `source` is NOT read from the form in the wizard — it is forced to `"captured"` for new nodes (and stays `"captured"` on re-capture of an existing node). The `source` dropdown (ssh_key/password/captured) is removed from the wizard; it remains only in the simplified Edit form for legacy manual re-tagging.
- Field name unified to `ssh_key_id` everywhere (was `ssh_key` in Capture).
- **Validation (new, blocks the bug):** if `ssh_key_id` is empty AND `login_pass` is empty AND `ssh_key_manual` is empty → HTTP 400 "Choose a key or enter password". A node can no longer be saved with an empty `KeyPath`.
- Manual paste: when `ssh_key_id == "manual"` and `ssh_key_manual` non-empty → save as `key-manual-<unix>` in `settings.SSHKeys` with `Source: SourceManual`, set `ssh_key_id = keyID`.
- Auto-install logic (`nodes.go:236-279`) — preserved, but `Source: SourceAuto` set on the new `key-auto-*` entry.
- Sets `Source` on all newly created `SSHKeyEntry`.
- On success: saves `Host` (KeyPath = keyID or `password:<pass>`), `NodeInfo`, `NodeMetrics` (with the new OS/sing-box/AWG fields from extended `GetStatus`).

**`handleUpdateNode` (simplified):**
- Reads only: `ssh_key_id`, `country`, `bandwidth`, `source`. Removes `keyPath` textarea reading.
- `if ssh_key_id != "" { host.KeyPath = ssh_key_id }` — does NOT clear when empty (preserves current / lets applier fall back to default).
- No password, no manual textarea in Edit. If the key is broken, the user clicks **"Re-capture"** (`hx-get="/ui/nodes/{id}/capture"`) → wizard pre-filled on the existing id.

**`handleNodeCaptureForm`** (`nodes.go:301`) — must accept an existing id and pre-fill `addr`/`user` from the stored `Host` (for the Re-capture flow).

**Applier fallback (new helper):**
- Add `resolveHostKey(st Store, host *model.Host) *model.Host` used everywhere SSH is initiated: `applier.go:154,345,1815` and takeover (`takeover.go:70`, `detect.go:98`).
- Logic: if `host.KeyPath == ""` → load `settings.DefaultSSHKeyID`; if non-empty, set `host.KeyPath = settings.DefaultSSHKeyID`. Return the (possibly patched) host copy. Soft fallback — does not error if default is also empty (existing `ResolveKey("")` → false → `ssh.Connect` errors with a clearer message).
- This is the single chokepoint for the default-key policy; future stricter policies ("require key") plug in here.

### `Backend.GetStatus` extension (`singbox.go:714-747`)

Add three SSH probes after the existing ones:
- **OS:** `cat /etc/os-release 2>/dev/null | grep -E '^PRETTY_NAME=' | cut -d= -f2- | tr -d '"'` (fallback `lsb_release -ds 2>/dev/null`). Store in `Status.OS`.
- **SingBoxInstalled:** `command -v sing-box >/dev/null 2>&1 && echo yes || echo no` → bool. Independent of `Running` (a binary can be installed but stopped). Store in `Status.SingBoxInstalled`.
- **AWGModuleInstalled:** reuse the `lsmod | grep amneziawg` check. Add a small **public** method `isAWGKernelModuleLoaded(conn)` (or expose the existing `awgKernelModuleLoaded` logic as a method on the backend) and call it from `GetStatus`; do not duplicate the shell probe inline (keeps one source of truth for the probe). Store in `Status.AWGModuleInstalled`.

Port interface `Backend.GetStatus` (`ports/backend.go:24`) — signature unchanged; `Status` is extended in-place. Other callers unaffected (new fields default to zero).

`handleCaptureNode` persists the new fields into `NodeMetrics` (`nodes.go:283-292`).

### UI changes

**Dropdown key selector (one shared component, name `ssh_key_id` everywhere):**
- First option: if `DefaultSSHKeyID` set → `<option value="">Default ({defaultName})</option>`, else `<option value="">Select key...</option>`.
- `<optgroup label="Stored keys">` for `Source == stored/auto/manual` entries; `<optgroup label="System keys">` for `Source == system`. (Built from `mergeSSHKeys`, grouped by `Source`.)
- Each option label prefixed with a source badge: `[Stored] Home Server`, `[System] id_ed25519`, `[Auto] auto-34.40.120.7`.
- Last option: `<option value="manual">== Paste key manually ==</option>` → JS `onchange` reveals textarea `ssh_key_manual`.
- Rendered identically in the Capture wizard and the Edit form.

**Capture wizard (Add node = the only entry point for a new node):**
- Step 1 — Credentials (single HTMX POST → partial in `#capture-result`):
  ```
  Addr: [______]   User: [root]
  SSH key: [Stored] Home Server ▾   (optgroups + badges + Default option)
           (== Paste manually == → reveals ssh_key_manual textarea)
  Password (for first login): [______]
  ☑ Install public key for passwordless access
  [Verify & connect]
  ```
  On failure → inline error / TOFU HostKey modal (existing `HostKeyWarning`). Step 2 not shown.
- Step 2 — Confirmed (rendered after a successful `GetStatus`):
  ```
  System:
    OS: Debian 12
    sing-box: not installed   (or "installed, v1.13.14" if Version non-empty)
    AWG kernel module: not installed   (or "loaded")
  Metadata:
    Country: [__]   Bandwidth: [__]
    (Source is forced to "captured" — not user-selectable in the wizard.)
  [Save node]
  ```
  AWG module is **detected only**, not installed on capture — install remains a deploy-time action per AGENTS.md #11 (`applier.go:358`, gated on `UserProtocol==AWG || Transport==AWG`).

**Edit node form (simplified):**
- `addr`, `user` (readonly — change via Re-capture).
- `ssh_key_id` dropdown (shared component).
- `country`, `bandwidth`, `source`.
- **"Re-capture" button** → `hx-get="/ui/nodes/{id}/capture"` (opens the wizard pre-filled).
- Removed: `keyPath` textarea, password, manual paste (all live in the wizard / Settings).

**Settings → SSH Keys section (extended):**
- Existing: list of stored keys (delete button) + system keys (read-only, auto-detected badge) + "Add key" form (name + PEM textarea).
- New: **radio "Use as default"** next to each key → sets `PanelSettings.DefaultSSHKeyID` via `POST /ui/settings/default-key` (new handler).
- Source badges `[Stored]`/`[System]`/`[Auto]` shown next to each key (consistent with the dropdown).

### i18n

All new user-facing strings added to BOTH `en` and `ru` blocks in `internal/i18n/i18n.go`, and JS-side strings via `window.AB_I18N` + `abt("key")` where applicable:
- `Default SSH key`
- `Paste key manually`
- `Verify & connect`
- `Install public key for passwordless access`
- `Re-capture node`
- `Save node`
- `Choose a key or enter password` (validation error)
- `Stored keys` / `System keys` (optgroup labels)
- `[Stored]` / `[System]` / `[Auto]` badges (or translated equivalents)
- `Use as default`
- `OS` / `sing-box` / `AWG kernel module` / `not installed` / `installed` / `loaded`

Per AGENTS.md rule 1 — never hardcode English UI text.

---

## Out of scope (follow-ups / other subprojects)

- **B (Client unification):** merging `User` + `MtproxyUser`, removing the dead `Profiles` section, fixing "Create first user" from the inbound form. Separate spec.
- **C (Per-protocol presets + custom presets UI + QUIC capture UI):** splitting `ConnectionPreset` per protocol, replacing `Profiles` with custom-presets editor, adding `AWGCPSMimicry="quic-live"` + `AWGCPSCaptureDomain` fields to the chain form. Separate spec.
- Showing live status badges (OS/sing-box/AWG) in the **nodes list table** — optional follow-up after the model fields exist; not required for this spec.
- Stricter policy "require key, refuse deploy if empty" — the `resolveHostKey` helper is the future chokepoint; current spec uses soft fallback.

---

## Files to change (summary)

**Model:**
- `internal/domain/model/panel.go` — `PanelSettings.DefaultSSHKeyID`, `SSHKeyEntry` Source consts, `NodeMetrics.{OS,SingBoxInstalled,AWGModuleInstalled}`.
- `internal/domain/model/backend.go` — `Status.{OS,SingBoxInstalled,AWGModuleInstalled}`.

**Backend:**
- `internal/backend/singbox/singbox.go:714-747` — `GetStatus` extended probes; promote/reuse `awgKernelModuleLoaded`.

**Chain / SSH:**
- `internal/chain/applier.go` — `resolveHostKey` helper + callers (`:154,345,1815`).
- `internal/takeover/takeover.go:70`, `internal/takeover/detect.go:98` — use `resolveHostKey`.
- `internal/chain/store.go:613` — `ResolveKey` unchanged (already returns false on empty; default-key resolution moves to the applier helper, NOT into ResolveKey, to keep lock scope clean).

**Web handlers:**
- `internal/web/nodes.go` — remove `handleCreateNode`; extend `handleCaptureNode` (metadata fields, validation, Source on new entries, new-NodeInfo creation); simplify `handleUpdateNode`; `handleNodeCaptureForm` accepts existing id.
- `internal/web/settings.go` — `handleSetDefaultKey` (new) for the default-key radio.
- `internal/web/server.go` — route adjustments (remove `POST /ui/nodes` create path or repoint; add `POST /ui/settings/default-key`).

**Templates:**
- `web/templates/nodes.templ` — unified `ssh_key_id` dropdown component (optgroups + badges + Default option); Capture wizard 2-step flow; Edit form simplified + Re-capture button; remove standalone `NodeForm` New branch.
- `web/templates/settings.templ` — default-key radio + source badges.
- `web/templates/nodes_templ.go` / `settings_templ.go` — regenerated via `templ generate`.

**i18n:**
- `internal/i18n/i18n.go` — new keys in `en` and `ru`.

**JS:**
- `web/static/js/app.js` — manual-paste toggle for the unified dropdown (consolidate existing `filterPresetsForRow`-style handlers).

---

## Build sequence (high level — detailed plan via writing-plans skill)

1. Model + consts (`panel.go`, `backend.go`).
2. `GetStatus` extension (`singbox.go`) + `awgKernelModuleLoaded` promotion.
3. `resolveHostKey` helper + applier/takeover wiring (this alone fixes the deploy bug for nodes that already have a key).
4. Handler fixes: `handleCaptureNode` extension (validation, metadata, Source, new-node creation), `handleUpdateNode` simplification, remove `handleCreateNode`.
5. `handleSetDefaultKey` + Settings radio.
6. Templates: unified dropdown, wizard 2-step, simplified Edit, Re-capture button, Settings default radio + badges.
7. `templ generate` → `go build ./...` → `go test ./...` (skip TUIC/Hysteria2 per AGENTS.md #6/#11).
8. i18n keys.
9. Manual E2E smoke: add node via wizard (key + password+auto-install paths), edit (re-select key, re-capture), deploy inbound (no more empty-key error).