# Per-Protocol Presets + Custom Preset UI — Design Spec (Subproject C1)

**Date:** 2026-07-06
**Subproject:** C1 (of C1/C2 — see audit decomposition; A, B, C2 are DONE)
**Status:** Draft → pending user review

---

## Problem

Two complaints from the audit:

1. **"All obfuscation presets are mixed together, but each protocol should have its own (AWG own, xray own, etc.)"** — The dropdown for an inbound's obfuscation preset shows the same ~6 geopolitical "kitchen-sink" presets regardless of protocol. Root cause: `ConnectionPreset` (`internal/chain/presets.go:82`) bundles `*RealityPreset`/`*XHTTPPreset`/`*TUICPreset`/`*AWGPreset` + routing into every entry; the global flat registry (`presets.go:18`) has 6 built-ins, each carrying ALL protocol sections; `ListPresetsForProtocol` (`presets.go:153`) filters by "non-nil sub-struct pointer" but every preset has nearly all sections → returns nearly the full list for every protocol. The UI (`handleNodeInboundsForm` `nodes.go:401`) passes ALL presets to the template and relies on a client-side JS post-hoc filter (`filterPresetsForRow` `app.js:45`) that is effectively a no-op.

2. **"Unclear Profiles section — let's add custom preset creation there instead"** — `model.Profile`/`model.ClientAssignment` (`panel.go:261-282`) are confirmed DEAD in the deploy pipeline (applier/singbox never read them; only `profiles.go` UI + `store.go` CRUD touch them). The Profiles page (`/ui/profiles`, `profiles.go:17-155`) manages an orthogonal client-to-server-role assignment matrix; its name collides with `chain.ObfuscationProfile` (the live preset-name string on `model.Chain`). `CustomPresets json.RawMessage` on `PanelSettings` (`panel.go:88`) is write-only — no creation UI; only hand-editable via settings JSON.

---

## Audit Summary (facts from current code)

- `chain.ConnectionPreset` (`presets.go:82-101`): `Name, Description, *Reality, *XHTTP, *TUIC, *AWG, CPSLevel, AWGMimicry, Routing`. Per-protocol structs (`RealityPreset:22, XHTTPPreset:29, TUICPreset:49, AWGPreset:55`) are nested children, no top-level per-protocol preset type. `CPSLevel`/`AWGMimicry` are duplicated top-level vs `AWGPreset.CPSLevel/Mimicry`.
- Global `map[string]ConnectionPreset presets` (`presets.go:18`), one global default `defaultPresetName = "maximum_stealth_2026"` (`presets.go:189`), settable via `SetDefaultProfile` from `config.DefaultObfuscationProfile` (`config.go:24`).
- `default_presets.json` (213 lines, 6 presets): `russia_2026`, `iran_2026`, `china_2026`, `maximum_stealth_2026`, `pro_2026`, `xhttp_max_stealth_2026` — each carries reality + xhttp + awg + tuic + routing sections.
- `ListPresetsForProtocol(protocol)` (`presets.go:153`) + `presetSupportsProtocol` (`presets.go:166`) — no-op filter (every preset has all sections).
- `GetEffectivePreset(c)` (`presets.go:217`) resolves `c.ObfuscationProfile` → `GetPreset(name)` → `GetDefaultPreset()`. Used by `applier.go:197`, `merged_config.go:462` (`resolveChainPreset`).
- `handleNodeInboundsForm` (`nodes.go:401`): passes `chain.ListPresets()` (ALL) to template + `protocolPresetsJSON` (map[protocol]→[]names). Template (`nodes.templ:299`) renders all presets per row; `filterPresetsForRow` (`app.js:45`) rewrites dropdown client-side on protocol `onchange` + `htmx:afterSettle`.
- `CustomPresets json.RawMessage` (`panel.go:88`) — `chain.LoadPresets(customs)` (`presets.go:105`) merges external presets by name (external wins). Called from `nodes.go:31-36` (page render) + `nodes.go:408-413` (inbound form render) + `main.go:558` (boot). No creation UI anywhere.
- `model.Profile`/`ClientAssignment` dead in deploy; store CRUD at `store.go:764-913` (`SaveProfile/GetProfile/ListProfiles/DeleteProfile/SaveAssignment/ListAssignments/ListAssignmentsForProfile`); `storeFile.Profiles`/`.Assignments` slices. `internal/web/profiles.go:17-155` handlers (`handleProfiles/handleNewProfileForm/handleCreateProfile/handleEditProfileForm/handleUpdateProfile/handleDeleteProfile/handleCreateAssignment/handleDeleteAssignment`). Routes `/ui/profiles/*` (`server.go:249-256`). Nav item `base.templ:67`.
- `handleClients` (`profiles.go:159`) — the unified Clients CRUD page (subproject B) — lives in `profiles.go` and MUST be preserved when Profile handlers are removed.
- Per-inbound: only `NodeInbound.Obfuscation string` (preset NAME) stored (`panel.go:193`); no per-inbound params.
- TUIC (`AGENTS.md #6`) and Hysteria2 (`#11`) are FROZEN — per-protocol presets for them are not exposed in NEW-selection UI; existing chains referencing them keep resolving.

---

## Design (chosen approach: Protocol tag + strict dropdown filter + custom-preset CRUD replacing Profiles)

### Principles

- **Minimal blast radius.** Add a `Protocol` tag to `ConnectionPreset` and split the built-in presets into per-protocol variants. The applier (`GetEffectivePreset`/`resolveChainPreset`/`BuildRoutingSection`/`BuildAWGAmnezia`) is unchanged — it reads the same sub-structs by name. No per-protocol preset TYPES (would require rewriting all applier call sites).
- **Backward compat without migration.** The 6 legacy kitchen-sink presets stay registered under their original names with `Protocol == ""` (legacy/global). `GetPreset(name)` still resolves them so existing chains with `ObfuscationProfile = "russia_2026"` keep working. But `ListPresetsForProtocol(protocol)` returns ONLY presets with a matching non-empty `Protocol` tag — legacy presets are EXCLUDED from the dropdown. So: clean dropdowns for new inbounds, existing chains unbroken.
- **Server-side filtering.** `handleNodeInboundsForm` passes only the per-protocol preset list to the template; the client-side JS `filterPresetsForRow` is removed (the dropdown is already correct per row).
- **Custom-preset CRUD.** Replace the dead Profiles page with a Presets manager at `/ui/presets` (reuses the nav slot). Create/edit/delete custom presets (stored in `PanelSettings.CustomPresets`, merged into the registry via `LoadPresets`). A custom preset has a `Protocol` tag and only the corresponding protocol's section; the editor form is protocol-scoped (shows AWG fields only for AWG, Reality fields only for vless-reality, etc.).
- **Per-protocol default.** `GetDefaultPresetForProtocol(protocol)` returns a built-in per-protocol default (e.g. `maximum_stealth_2026_awg` for awg) when `c.ObfuscationProfile == ""`, instead of one global default. `GetEffectivePreset` uses it. `PanelSettings.DefaultPresetByProtocol` lets the operator override per protocol.
- **Delete `Profile`/`ClientAssignment`.** The structs, store slices, store CRUD, web handlers, routes, and nav are removed. `handleClients` (subproject B) is preserved (moved out of `profiles.go` into `clients.go` or stays in `profiles.go` renamed to `clients.go`; the file is renamed and Profile handlers deleted from it). No migration — the data is dead.

### Model + registry changes

1. **`ConnectionPreset`** (`presets.go:82`) — add `Protocol` field:
   ```go
   type ConnectionPreset struct {
       Name        string `json:"name"`
       Protocol    string `json:"protocol,omitempty"` // NEW: "awg"|"vless-reality"|"xhttp"|"tuic"|"" (legacy/global)
       Description string `json:"description"`
       Reality     *RealityPreset `json:"reality,omitempty"`
       XHTTP       *XHTTPPreset   `json:"xhttp,omitempty"`
       TUIC        *TUICPreset    `json:"tuic,omitempty"`
       AWG         *AWGPreset     `json:"awg,omitempty"`
       CPSLevel   int    `json:"cps_level,omitempty"`
       AWGMimicry string `json:"awg_mimicry,omitempty"`
       Routing struct { ... } `json:"routing,omitempty"`
   }
   ```
   `Protocol == ""` = legacy/global preset (resolvable by name, excluded from dropdowns).

2. **`default_presets.json`** — rewrite into per-protocol variants. Each variant keeps ONLY its protocol's section + the routing block (routing is protocol-agnostic, keep on all). Names: `<region>_<protocol>` (e.g. `russia_2026_awg`, `russia_2026_reality`, `russia_2026_xhttp`, `iran_2026_awg`, ...). The 6 legacy names (`russia_2026` etc.) are kept as `Protocol == ""` entries (so `GetPreset` still resolves existing chains) — but to avoid duplicating the kitchen-sink data, the legacy entries can be thin: keep them with all sections intact (they already exist in the file) and add the per-protocol variants alongside. Net: `default_presets.json` grows from 6 to ~20 entries (6 legacy + ~14 per-protocol variants). TUIC variants are included for completeness but `ListPresetsForProtocol("tuic")` results are NOT shown in the new-selection dropdown (frozen — the UI hides the protocol entirely for new selection per AGENTS.md #6).

3. **`ListPresetsForProtocol(protocol)`** (`presets.go:153`) — strict filter:
   ```go
   func ListPresetsForProtocol(protocol string) []string {
       presetsMu.RLock()
       defer presetsMu.RUnlock()
       names := make([]string, 0, len(presets))
       for name, p := range presets {
           if p.Protocol == protocol { // empty Protocol (legacy) is EXCLUDED
               names = append(names, name)
           }
       }
       sort.Strings(names)
       return names
   }
   ```
   `presetSupportsProtocol` is kept for backward-compat callers but no longer drives the dropdown. Legacy presets (`Protocol == ""`) are resolvable via `GetPreset` but never listed.

4. **Per-protocol default:**
   ```go
   func GetDefaultPresetForProtocol(protocol string) ConnectionPreset {
       if name := defaultPresetForProtocol(protocol); name != "" {
           if p, ok := GetPreset(name); ok { return p }
       }
       return GetDefaultPreset() // fallback to the global default (legacy)
   }
   ```
   `defaultPresetForProtocol(protocol)` returns `PanelSettings.DefaultPresetByProtocol[protocol]` if set, else a built-in: `"maximum_stealth_2026_awg"` for awg, `"maximum_stealth_2026_reality"` for vless-reality, `"maximum_stealth_2026_xhttp"` otherwise. `GetEffectivePreset(c)` (`presets.go:217`) changes its fallback from `GetDefaultPreset()` to `GetDefaultPresetForProtocol(string(c.UserProtocol))`.

5. **`PanelSettings.DefaultPresetByProtocol`** (`panel.go:72-80`) — add:
   ```go
   DefaultPresetByProtocol map[string]string `json:"default_preset_by_protocol,omitempty"`
   ```

### Custom-preset CRUD

- Custom presets live in `PanelSettings.CustomPresets` (json.RawMessage, already exists) as a `[]ConnectionPreset`. `LoadPresets(customs)` already merges them (external wins). The registry reload on every nodes-page/inbound-form render (`nodes.go:31,408`) stays — it's how custom presets enter the registry. (A future optimization can cache; out of scope.)
- New handlers (`internal/web/presets.go` NEW, or extend `profiles.go` after renaming):
  - `handlePresets` — list all presets (built-in + custom, tagged by Protocol), with a "Create custom preset" button.
  - `handleNewPresetForm` / `handleCreatePreset` — protocol select + protocol-scoped fields (AWG: jc/jmin/jmax/s1-s4/itime/h1-h4/cps_level/mimicry; Reality: server_names/fingerprints/short_id_len; XHTTP: methods/paths/hosts/headers/timeouts/padding/etc). Saves a `ConnectionPreset` with the chosen `Protocol` tag + only the corresponding section, appends to `CustomPresets`, calls `LoadPresets` to reload.
  - `handleEditPresetForm` / `handleUpdatePreset` — edit a custom preset by name (built-in presets are read-only: edit disabled / shows a "fork to custom" button).
  - `handleDeletePreset` — delete a custom preset by name (filter out of `CustomPresets`, reload). Refuse if a chain or inbound still references it (grep `ObfuscationProfile`/`NodeInbound.Obfuscation`).
- Routes (`internal/web/server.go`): replace `/ui/profiles/*` with `/ui/presets`, `/ui/presets/new`, `/ui/presets/{name}/edit`, `/ui/presets/{name}/delete`.
- Nav (`base.templ:67`): rename "Profiles" → "Presets" (`/ui/presets`).

### Inbound form — server-side per-protocol filter

`handleNodeInboundsForm` (`nodes.go:415`): instead of passing `chain.ListPresets()` (ALL) to the template, build the per-protocol map and pass ONLY per-protocol lists:
```go
protocolPresets := map[string][]string{
    "awg":           chain.ListPresetsForProtocol("awg"),
    "vless-reality": chain.ListPresetsForProtocol("vless-reality"),
    "xhttp":         chain.ListPresetsForProtocol("xhttp"),
    // tuic/hysteria2 frozen — not in the map (no new selection).
}
presetsJSON, _ := json.Marshal(protocolPresets)
```
The template renders an EMPTY dropdown per row initially (no presets baked in); the JS `filterPresetsForRow` POPULATES it from `protocolPresetsJSON` on protocol `onchange` + initial load. (Currently it filters a baked-in full list; now it populates from the per-protocol map.) This keeps the existing JS pattern but with clean per-protocol lists. The `presets []string` param to `NodeInboundsForm` becomes empty/unused (keep for signature compat or drop — drop and update the call site).

### Delete Profile / ClientAssignment

- `model.Profile` + `model.ClientAssignment` structs (`panel.go:261-282`) — DELETED.
- `storeFile.Profiles` / `.Assignments` fields (`store.go:45-46`) — DELETED.
- Store methods `SaveProfile/GetProfile/ListProfiles/DeleteProfile/SaveAssignment/ListAssignments/ListAssignmentsForProfile` (`store.go:764-913`) — DELETED.
- `internal/web/profiles.go` Profile/Assignment handlers (`:17-155`) — DELETED. `handleClients` (`:159`) is PRESERVED (move to a new `internal/web/clients.go` or keep in `profiles.go` renamed to `clients.go`; simplest: keep `profiles.go` file but delete the Profile handlers and rename the file's purpose in its header comment). Routes `/ui/profiles/*` (`server.go:249-256`) — DELETED, replaced by `/ui/presets/*`.
- No migration of Profile data (it's dead — no deploy consumer). Existing `store.json` files keep their `profiles`/`assignments` arrays harmlessly (Go ignores unknown JSON keys on deserialize, and the `storeFile` fields are gone so they're dropped on next save).

### i18n

New keys in BOTH en and ru:
- `Presets` / `Пресеты`
- `Create custom preset` / `Создать кастомный пресет`
- `Edit preset` / `Редактировать пресет`
- `Delete preset` / `Удалить пресет`
- `Custom presets` / `Кастомные пресеты`
- `Built-in presets` / `Встроенные пресеты`
- `Preset name` / `Имя пресета`
- `Protocol` (likely exists) / `Протокол`
- `Preset is in use (chain/inbound references it)` / `Пресет используется (на него ссылается цепь/inbound)`
- `Fork to custom` / `Скопировать в кастомный`
- field labels for AWG/Reality/XHTTP (some exist via the inbound form; reuse).

### Scope boundaries (NOT in C1)

- **Standalone AWG `NodeInbound` CPS material gap** (degenerate H1-H4, fresh I1-I5) — audit gap, not addressed here (chain-only path is the product focus).
- **C2 (QUIC capture UI)** — DONE, untouched.
- **TUIC/Hysteria2 new preset creation** — frozen; the preset manager allows editing existing TUIC/Hysteria2 custom presets but the protocol select for NEW custom presets hides them (frozen).
- **Routing section in custom presets** — out of scope for the v1 editor (custom presets are per-protocol obfuscation only; routing stays on built-in presets). A custom preset's `Routing` is empty. (Routing is disabled in merged config anyway per AGENTS.md #2.)

---

## Files to change (summary)

**Model:**
- `internal/chain/presets.go` — `ConnectionPreset.Protocol` field; `ListPresetsForProtocol` strict filter; `GetDefaultPresetForProtocol` + `defaultPresetForProtocol`; `GetEffectivePreset` uses per-protocol default.
- `internal/chain/default_presets.json` — add per-protocol variants (`_awg`/`_reality`/`_xhttp`); keep 6 legacy entries.
- `internal/domain/model/panel.go` — `PanelSettings.DefaultPresetByProtocol`; DELETE `Profile`/`ClientAssignment` structs.

**Store:**
- `internal/chain/store.go` — DELETE `SaveProfile/GetProfile/ListProfiles/DeleteProfile/SaveAssignment/ListAssignments/ListAssignmentsForProfile` + `storeFile.Profiles`/`.Assignments` fields.

**Web handlers:**
- `internal/web/profiles.go` — DELETE Profile/Assignment handlers (`:17-155`); KEEP `handleClients` (`:159`).
- `internal/web/presets.go` NEW — `handlePresets/handleNewPresetForm/handleCreatePreset/handleEditPresetForm/handleUpdatePreset/handleDeletePreset`.
- `internal/web/nodes.go` — `handleNodeInboundsForm` passes per-protocol `protocolPresets` map (not all presets).
- `internal/web/server.go` — replace `/ui/profiles/*` routes with `/ui/presets/*`.
- `internal/web/settings.go` — (optional) Settings page exposes `DefaultPresetByProtocol` radio/select.

**Templates:**
- `web/templates/profiles.templ` — DELETE (or replace with `presets.templ`).
- `web/templates/presets.templ` NEW — `PresetsPage` + `PresetForm` (protocol-scoped).
- `web/templates/nodes.templ` — `NodeInboundsForm` dropdown populated from `protocolPresetsJSON` only (no baked-in full list); `presets []string` param dropped.
- `web/templates/base.templ` — nav "Profiles" → "Presets" (`/ui/presets`).
- `web/static/js/app.js` — `filterPresetsForRow` populates from per-protocol map (not filters a baked list).
- Regenerated `*_templ.go`.

**i18n:**
- `internal/i18n/i18n.go` — new keys en+ru.

**Tests:**
- `internal/chain/presets_test.go` — `ListPresetsForProtocol` strict-filter test; `GetDefaultPresetForProtocol` test; legacy `GetPreset` still resolves.
- `internal/web/handlers_presets_test.go` NEW — CRUD custom preset; delete-in-use refusal.
- Update any test referencing Profile/ClientAssignment handlers (remove/migrate).

---

## Build sequence (high level — detailed plan via writing-plans skill)

1. Model: `ConnectionPreset.Protocol` + `PanelSettings.DefaultPresetByProtocol`; delete `Profile`/`ClientAssignment`.
2. `default_presets.json` per-protocol variants + keep legacy.
3. `ListPresetsForProtocol` strict filter + `GetDefaultPresetForProtocol` + `GetEffectivePreset` fallback.
4. Store: delete Profile/Assignment methods + storeFile fields.
5. Web: delete Profile handlers (keep `handleClients`); new preset CRUD handlers + routes + nav.
6. Inbound form: server-side per-protocol map; template + JS populate from map.
7. Templates: `presets.templ` PresetsPage + PresetForm; delete `profiles.templ`; nav rename.
8. i18n keys en+ru.
9. `templ generate` → `go build ./...` → `go test ./...` (skip TUIC/Hysteria2 per AGENTS.md #6/#11).
10. Manual smoke: create AWG inbound → dropdown shows only AWG presets; create a custom preset via /ui/presets → appears in the inbound dropdown for that protocol; existing chain with `ObfuscationProfile="russia_2026"` still resolves; delete a preset in use → refused; Profiles page gone, Presets page works.