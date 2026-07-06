# Per-Protocol Presets + Custom Preset UI Implementation Plan (Subproject C1)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Each protocol sees only its own presets in the inbound dropdown (via a `Protocol` tag on `ConnectionPreset` + per-protocol built-in variants + strict server-side filter); users can create/edit/delete custom presets via a new `/ui/presets` page (replacing the dead Profiles page); `Profile`/`ClientAssignment` are deleted. Applier unchanged — it reads the same sub-structs by name.

**Architecture:** `ConnectionPreset` gains a `Protocol` field; `default_presets.json` gets per-protocol variants (`*_awg`/`*_reality`/`*_xhttp`) alongside the 6 legacy kitchen-sink entries (kept for backward compat — existing chains resolve by name); `ListPresetsForProtocol` filters strictly by the tag; `GetEffectivePreset` falls back to a per-protocol default; a new preset CRUD UI replaces Profiles; the inbound form passes only the per-protocol map to the template.

**Tech Stack:** Go, HTMX + Templ + TailwindCSS/DaisyUI, i18n.

## Global Constraints

- **AGENTS.md is the law.** Re-read rules 1, 2, 6, 9.
- **Applier unchanged.** Do NOT modify `applier.go`/`merged_config.go`/`awg_cps.go` preset-reading logic — it reads `preset.AWG`/`preset.Reality`/`preset.XHTTP`/`preset.TUIC` by name. The `Protocol` tag is metadata for the UI/registry only.
- **Backward compat.** The 6 legacy preset names (`russia_2026`, `iran_2026`, `china_2026`, `maximum_stealth_2026`, `pro_2026`, `xhttp_max_stealth_2026`) MUST stay registered (`Protocol == ""`) so `GetPreset(name)` resolves existing chains. Tests `TestDefaultPresetsLoaded` + `TestAWGPresetValuesFromProfiles` (`presets_test.go:9,103`) reference these — keep them passing.
- **i18n:** every new user-facing string in BOTH `en` and `ru` blocks.
- **Build sequence:** after any `.templ` edit → `templ generate` → `go build ./...`. After any Go edit → `go build ./...`. Run `go test ./internal/web/ ./internal/chain/` at the end of each task.
- **Frozen protocols:** TUIC (`AGENTS.md #6`) / Hysteria2 (`#11`) — the preset CRUD protocol-select hides them for NEW custom presets; existing TUIC custom presets remain editable. `ListPresetsForProtocol("tuic")` works (returns TUIC-tagged presets) but the inbound form for non-frozen protocols doesn't surface TUIC.
- **One commit per task.** Format: `feat(presets): ...` / `refactor(presets): ...`, trailer `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- **Commit on `main` directly — do NOT branch.**
- **`handleClients` (`profiles.go:159`) MUST be preserved** when Profile handlers are deleted.

---

## File structure

```
internal/chain/presets.go             # +Protocol field, strict ListPresetsForProtocol, per-protocol default, GetEffectivePreset fallback
internal/chain/default_presets.json   # +per-protocol variants (legacy kept)
internal/chain/presets_test.go        # +strict-filter + per-protocol-default tests
internal/domain/model/panel.go        # +PanelSettings.DefaultPresetByProtocol; DELETE Profile/ClientAssignment
internal/chain/store.go               # DELETE Profile/Assignment methods + storeFile fields
internal/web/profiles.go              # DELETE Profile handlers (keep handleClients)
internal/web/presets.go               # NEW: preset CRUD handlers
internal/web/nodes.go                 # handleNodeInboundsForm: per-protocol map only
internal/web/server.go                # routes: /ui/profiles/* -> /ui/presets/*
internal/web/handlers_presets_test.go # NEW
web/templates/profiles.templ          # DELETE
web/templates/presets.templ           # NEW: PresetsPage + PresetForm
web/templates/nodes.templ             # dropdown populated from per-protocol map
web/templates/base.templ              # nav: Profiles -> Presets
web/static/js/app.js                  # filterPresetsForRow populates from map (already does)
internal/i18n/i18n.go                 # new keys en+ru
```

---

## Task 1 — `ConnectionPreset.Protocol` + `PanelSettings.DefaultPresetByProtocol` + `default_presets.json` variants

**Files:**
- Modify: `internal/chain/presets.go:82` (ConnectionPreset)
- Modify: `internal/chain/default_presets.json`
- Modify: `internal/domain/model/panel.go` (PanelSettings)

**Interfaces:**
- Produces: `ConnectionPreset.Protocol string` (consumed by Task 2's strict filter); `PanelSettings.DefaultPresetByProtocol map[string]string` (consumed by Task 2's per-protocol default).

- [ ] **Step 1: Add `Protocol` field to `ConnectionPreset`**

In `internal/chain/presets.go`, add the field right after `Name`:
```go
type ConnectionPreset struct {
	Name        string `json:"name"`
	Protocol    string `json:"protocol,omitempty"` // "awg"|"vless-reality"|"xhttp"|"tuic"|"" (legacy/global — resolvable but excluded from dropdowns)
	Description string `json:"description"`
	Reality     *RealityPreset `json:"reality,omitempty"`
	XHTTP       *XHTTPPreset   `json:"xhttp,omitempty"`
	TUIC        *TUICPreset    `json:"tuic,omitempty"`
	AWG         *AWGPreset     `json:"awg,omitempty"`
	CPSLevel   int    `json:"cps_level,omitempty"`
	AWGMimicry string `json:"awg_mimicry,omitempty"`
	Routing struct {
		DirectGeoIP   []string `json:"direct_geoip,omitempty"`
		DirectGeoSite []string `json:"direct_geosite,omitempty"`
		DirectDomains []string `json:"direct_domains,omitempty"`
		BlockGeoSite  []string `json:"block_geosite,omitempty"`
	} `json:"routing,omitempty"`
}
```

- [ ] **Step 2: Add per-protocol variants to `default_presets.json`**

Keep the existing 6 legacy entries (`russia_2026` etc.) UNCHANGED (they have `Protocol == ""` implicitly — no `protocol` field in JSON → empty). ADD per-protocol variants. For each of `russia_2026`, `iran_2026`, `china_2026`, `maximum_stealth_2026`, `pro_2026`, add three variants: `<name>_awg`, `<name>_reality`, `<name>_xhttp`. Each variant:
  - `"name": "<name>_awg"` (etc.)
  - `"protocol": "awg"` (etc.)
  - `"description": "<original desc> (AWG variant)"` (etc.)
  - ONLY the matching section (`awg` for `_awg`, `reality` for `_reality`, `xhttp` for `_xhttp`) — copy the values from the legacy entry's same section.
  - NO `tuic` section on variants (TUIC frozen for new selection).
  - The `routing` block — OMIT on variants (routing is disabled in merged config per AGENTS.md #2; custom/built-in variants carry no routing).

For `xhttp_max_stealth_2026`: it's XHTTP-only already — add `"protocol": "xhttp"` to the EXISTING entry (don't rename it; just tag it) so it appears in the XHTTP dropdown. (It currently has xhttp + reality + awg sections; keep them, but tag protocol=xhttp so `ListPresetsForProtocol("xhttp")` returns it. The reality/awg sections on it become inert for the dropdown but harmless — the applier reads the xhttp section for XHTTP inbounds.)

Concrete: the JSON becomes an array of (6 legacy as-is) + (5 regions × 3 protocols = 15 variants) + `xhttp_max_stealth_2026` tagged `protocol:"xhttp"` = 22 entries. Use a JSON editor approach: read the current file, copy sections. The implementer writes valid JSON (no comments).

- [ ] **Step 3: Add `DefaultPresetByProtocol` to `PanelSettings`**

In `internal/domain/model/panel.go`, `PanelSettings` struct, add (after `CustomPresets`):
```go
DefaultPresetByProtocol map[string]string `json:"default_preset_by_protocol,omitempty"` // optional per-protocol default override
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: compiles. Existing presets tests still pass (legacy names intact). Run `go test ./internal/chain/ -run "TestDefaultPresetsLoaded|TestAWGPresetValuesFromProfiles|TestGetDefaultPreset|TestSetDefaultProfileAndGetEffective|TestLoadPresets" -v` → PASS (no behavior change yet; `Protocol` field is just metadata).

- [ ] **Step 5: Commit**

```bash
git add internal/chain/presets.go internal/chain/default_presets.json internal/domain/model/panel.go
git commit -m "feat(presets): ConnectionPreset.Protocol tag + per-protocol built-in variants + DefaultPresetByProtocol"
```

---

## Task 2 — Strict `ListPresetsForProtocol` + per-protocol default + `GetEffectivePreset` fallback (TDD)

**Files:**
- Modify: `internal/chain/presets.go:153` (ListPresetsForProtocol), `:217` (GetEffectivePreset); add `GetDefaultPresetForProtocol` + `defaultPresetForProtocol`
- Modify: `internal/chain/presets_test.go` (add tests; keep existing)

**Interfaces:**
- Produces: `ListPresetsForProtocol(protocol)` returns ONLY presets with `Protocol == protocol` (legacy `""` excluded); `GetDefaultPresetForProtocol(protocol) ConnectionPreset`; `GetEffectivePreset` falls back to per-protocol default.

- [ ] **Step 1: Write tests (RED)**

Append to `internal/chain/presets_test.go`:

```go
func TestListPresetsForProtocol_StrictFilter(t *testing.T) {
	awgNames := ListPresetsForProtocol("awg")
	for _, name := range awgNames {
		p, ok := GetPreset(name)
		if !ok {
			t.Errorf("ListPresetsForProtocol returned unknown preset %q", name)
			continue
		}
		if p.Protocol != "awg" {
			t.Errorf("preset %q listed for awg but has Protocol=%q (legacy/kitchen-sink should be excluded)", name, p.Protocol)
		}
	}
	// legacy kitchen-sink presets must NOT appear
	for _, legacy := range []string{"russia_2026", "maximum_stealth_2026"} {
		if containsStr(awgNames, legacy) {
			t.Errorf("legacy preset %q should NOT be listed for awg (Protocol=\"\")", legacy)
		}
	}
	// there should be at least one awg variant
	if len(awgNames) == 0 {
		t.Errorf("expected awg-tagged presets, got none")
	}
}

func TestListPresetsForProtocol_LegacyExcluded(t *testing.T) {
	for _, proto := range []string{"awg", "vless-reality", "xhttp"} {
		names := ListPresetsForProtocol(proto)
		for _, legacy := range []string{"russia_2026", "iran_2026", "china_2026", "maximum_stealth_2026", "pro_2026"} {
			if containsStr(names, legacy) {
				t.Errorf("legacy preset %q listed for protocol %q — should be excluded", legacy, proto)
			}
		}
	}
}

func TestGetDefaultPresetForProtocol(t *testing.T) {
	p := GetDefaultPresetForProtocol("awg")
	if p.Name == "" {
		t.Fatal("expected non-empty default preset for awg")
	}
	if p.Protocol != "awg" {
		t.Errorf("default-for-awg preset has Protocol=%q, want awg", p.Protocol)
	}
	// unknown protocol falls back to the global default
	p2 := GetDefaultPresetForProtocol("bogus")
	if p2.Name == "" {
		t.Error("expected fallback to global default for unknown protocol")
	}
}

func TestGetEffectivePreset_PerProtocolDefault(t *testing.T) {
	c := &model.Chain{UserProtocol: model.UserProtocolAWG} // no ObfuscationProfile
	eff := GetEffectivePreset(c)
	if eff.Name == "" {
		t.Fatal("expected non-empty effective preset")
	}
	// Should be an awg-tagged preset (the per-protocol default), not the global kitchen-sink
	if eff.Protocol != "awg" {
		t.Errorf("effective preset for AWG chain has Protocol=%q, want awg", eff.Protocol)
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
```

Run: `go test ./internal/chain/ -run "TestListPresetsForProtocol|TestGetDefaultPresetForProtocol|TestGetEffectivePreset_PerProtocolDefault" -v` → expect RED (current `ListPresetsForProtocol` uses `presetSupportsProtocol` which returns legacy presets; `GetDefaultPresetForProtocol` undefined; `GetEffectivePreset` falls back to global).

- [ ] **Step 2: Implement strict filter + per-protocol default**

In `internal/chain/presets.go`:

Replace `ListPresetsForProtocol`:
```go
// ListPresetsForProtocol returns preset names tagged with the given protocol.
// Legacy/global presets (Protocol == "") are EXCLUDED from the dropdown — they
// are resolvable by name (for existing chains) but not offered for new
// selection. The dropdown shows only protocol-scoped presets.
func ListPresetsForProtocol(protocol string) []string {
	presetsMu.RLock()
	defer presetsMu.RUnlock()
	names := make([]string, 0, len(presets))
	for name, p := range presets {
		if p.Protocol == protocol {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
```
Add `"sort"` to imports if missing.

Add the per-protocol default functions (after `GetDefaultPresetName`):
```go
// GetDefaultPresetForProtocol returns the default preset for a protocol. If
// PanelSettings.DefaultPresetByProtocol is set (via the caller — this function
// does not read the store), the caller should pass that name in via
// SetDefaultProfile; here we use a built-in per-protocol default that the
// caller can override. Falls back to the global default for unknown protocols.
func GetDefaultPresetForProtocol(protocol string) ConnectionPreset {
	if name := defaultPresetForProtocol(protocol); name != "" {
		if p, ok := GetPreset(name); ok {
			return p
		}
	}
	return GetDefaultPreset()
}

// defaultPresetForProtocol returns the built-in default preset name for a
// protocol. The caller (applier) may override via PanelSettings.
func defaultPresetForProtocol(protocol string) string {
	switch protocol {
	case "awg":
		return "maximum_stealth_2026_awg"
	case "vless-reality":
		return "maximum_stealth_2026_reality"
	case "xhttp", "":
		return "xhttp_max_stealth_2026"
	default:
		return "" // fall back to global default
	}
}
```

Change `GetEffectivePreset` (`presets.go:217`) to use the per-protocol fallback:
```go
func GetEffectivePreset(c *model.Chain) ConnectionPreset {
	if c != nil && c.ObfuscationProfile != "" {
		if p, ok := GetPreset(c.ObfuscationProfile); ok {
			return p
		}
	}
	if c != nil {
		return GetDefaultPresetForProtocol(string(c.UserProtocol))
	}
	return GetDefaultPreset()
}
```

NOTE: `GetDefaultPresetForProtocol` here uses the built-in default. The spec mentions `PanelSettings.DefaultPresetByProtocol` — wiring the store-level override into `GetEffectivePreset` requires passing the settings into the chain package, which crosses a layer boundary. **Decision:** keep `GetEffectivePreset` using the built-in per-protocol default (no store read); the `DefaultPresetByProtocol` settings field is consumed by a future task / Settings UI that calls `SetDefaultProfile`-style overrides. For C1, the built-in per-protocol default is sufficient (the user complaint is about mixed dropdowns, not default selection). `DefaultPresetByProtocol` field stays on the model (Task 1) but is not yet wired into `GetEffectivePreset` — document this as a follow-up in the spec/commit.

- [ ] **Step 3: Run tests (GREEN)**

`go test ./internal/chain/ -run "TestListPresetsForProtocol|TestGetDefaultPresetForProtocol|TestGetEffectivePreset_PerProtocolDefault" -v` → PASS.
`go test ./internal/chain/` → full suite PASS (existing `TestSetDefaultProfileAndGetEffective` sets `SetDefaultProfile("china_2026")` and expects `GetEffectivePreset(&Chain{})` to return `china_2026` — but now the fallback for an empty-`UserProtocol` chain goes through `defaultPresetForProtocol("")` = `xhttp_max_stealth_2026`, NOT the global `china_2026`. **This test will BREAK.** Fix it: update `TestSetDefaultProfileAndGetEffective` to assert on a chain with `UserProtocol` set, OR keep the global-default path for `UserProtocol == ""`. **Chosen:** in `GetEffectivePreset`, when `c.UserProtocol == ""` (legacy/empty), fall back to `GetDefaultPreset()` (global) — only use the per-protocol default when `UserProtocol` is non-empty. Update `GetEffectivePreset`:
```go
func GetEffectivePreset(c *model.Chain) ConnectionPreset {
	if c != nil && c.ObfuscationProfile != "" {
		if p, ok := GetPreset(c.ObfuscationProfile); ok {
			return p
		}
	}
	if c != nil && c.UserProtocol != "" {
		return GetDefaultPresetForProtocol(string(c.UserProtocol))
	}
	return GetDefaultPreset()
}
```
Re-run — `TestSetDefaultProfileAndGetEffective`'s `c2 := &model.Chain{}` has empty `UserProtocol` → returns `GetDefaultPreset()` = `china_2026` (set by the test) → passes.

- [ ] **Step 4: Build + full test**

`go build ./...` → compile.
`go test ./internal/chain/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chain/presets.go internal/chain/presets_test.go
git commit -m "feat(presets): strict ListPresetsForProtocol filter + per-protocol default + GetEffectivePreset fallback"
```

---

## Task 3 — Delete `Profile`/`ClientAssignment` model + store methods + storeFile fields

**Files:**
- Modify: `internal/domain/model/panel.go` (delete `Profile` + `ClientAssignment` structs)
- Modify: `internal/chain/store.go` (delete Profile/Assignment methods + `storeFile.Profiles`/`.Assignments` fields)

**Interfaces:**
- Consumes: Task 1 (no Profile dependency).
- Produces: no more `model.Profile`/`model.ClientAssignment` references in non-test code.

- [ ] **Step 1: Grep for non-test, non-store, non-profiles.go callers**

Run: `grep -rn "model.Profile\|model.ClientAssignment\|ListProfiles\|GetProfile\|SaveProfile\|DeleteProfile\|SaveAssignment\|ListAssignments\|DeleteAssignment" internal/ web/ --include="*.go" | grep -v "_test.go"`
Expected hits: `internal/chain/store.go` (methods), `internal/web/profiles.go` (handlers — deleted in Task 4), `internal/domain/model/panel.go` (struct defs). If anything else references them, STOP and report.

- [ ] **Step 2: Delete model structs**

In `internal/domain/model/panel.go`, delete the `Profile` struct (~lines 261-271) and `ClientAssignment` struct (~276-282) + their doc comments. Leave `RouteRule` etc. intact.

- [ ] **Step 3: Delete storeFile fields + store methods**

In `internal/chain/store.go`:
- Delete `storeFile.Profiles` field (line 140) + `storeFile.Assignments` field (line 141).
- Delete methods: `SaveProfile` (~764), `GetProfile` (~805), `ListProfiles` (~822), `DeleteProfile` (~837), `SaveAssignment` (~871), `ListAssignments` (~898), `ListAssignmentsForProfile` (~913), `DeleteAssignment` (~928).

- [ ] **Step 4: Build (expect breakage in profiles.go — fixed in Task 4)**

`go build ./...` → will FAIL in `internal/web/profiles.go` (references deleted `model.Profile`/`st.ListProfiles`/etc.). This is expected — Task 4 deletes those handlers. **Do NOT commit Task 3 alone.** Combine Task 3 + Task 4 into ONE commit (the build must be green at commit time).

- [ ] **Step 5: Hold commit until Task 4**

Proceed to Task 4; commit both together.

---

## Task 4 — Delete Profile web handlers + routes; keep `handleClients`

**Files:**
- Modify: `internal/web/profiles.go` (delete `:17-155` Profile/Assignment handlers; keep `handleClients` `:159`)
- Modify: `internal/web/server.go` (delete `/ui/profiles/*` routes)

- [ ] **Step 1: Delete Profile handlers from profiles.go**

In `internal/web/profiles.go`, delete: `handleProfiles`, `handleNewProfileForm`, `handleCreateProfile`, `handleEditProfileForm`, `handleUpdateProfile`, `handleDeleteProfile`, `handleCreateAssignment`, `handleDeleteAssignment` (lines 17-155). KEEP `handleClients` (line 159+) and its header comment. After deletion, check `profiles.go` imports — `fmt`, `strings`, `chain`, `model` may become unused if only `handleClients` remains. `handleClients` uses `st.ListUsers()`, `st.ListChains()`, `templates.Users` — likely keeps `chain` + `model` + `templates` + `i18n` + `net/http`. Remove unused imports (`fmt`, `strings` if no longer used). The file header comment "profiles + client assignments + unified clients page" → update to "unified clients page (presets moved to presets.go)".

- [ ] **Step 2: Delete /ui/profiles/* routes in server.go**

In `internal/web/server.go`, delete the Profiles routes block (~lines 249-256): `GET /ui/profiles`, `POST /ui/profiles`, `GET /ui/profiles/new`, `GET /ui/profiles/{id}/edit`, `POST /ui/profiles/{id}/edit`, `DELETE /ui/profiles/{id}`, `POST /ui/profiles/{id}/assignments`, `DELETE /ui/profiles/{id}/assignments/{aid}`. (The `/ui/presets/*` routes are added in Task 5.)

- [ ] **Step 3: Build**

`go build ./...` → must compile now (Profile model + handlers + routes all gone together). If a test references `handleProfiles`/`/ui/profiles` (grep `profiles` in `internal/web/*_test.go`), delete/migrate that test. `TestHandler_*Profile*` if any — remove (the feature is gone). `handleClients` tests (`TestHandler_ClientsPage_*` from subproject B) stay.

- [ ] **Step 4: Test**

`go test ./internal/web/ ./internal/chain/` → PASS.

- [ ] **Step 5: Commit (Task 3 + Task 4 together)**

```bash
git add internal/domain/model/panel.go internal/chain/store.go internal/web/profiles.go internal/web/server.go
git commit -m "refactor(presets): delete dead Profile/ClientAssignment (model + store + handlers + routes); keep handleClients"
```

---

## Task 5 — New preset CRUD handlers + routes + nav

**Files:**
- Create: `internal/web/presets.go` (NEW)
- Modify: `internal/web/server.go` (add `/ui/presets/*` routes)
- Create: `internal/web/handlers_presets_test.go` (NEW)

**Interfaces:**
- Consumes: `chain.LoadPresets`, `chain.ListPresets`, `chain.GetPreset`, `model.PanelSettings.CustomPresets`. `chain.ConnectionPreset` + sub-structs (Task 1).
- Produces: `handlePresets`, `handleNewPresetForm`, `handleCreatePreset`, `handleEditPresetForm`, `handleUpdatePreset`, `handleDeletePreset`.

- [ ] **Step 1: Write handler tests (RED)**

Create `internal/web/handlers_presets_test.go`:

```go
package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_PresetsPage_Renders(t *testing.T) {
	ts := newTestServer(t)
	w := ts.get("/ui/presets")
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Presets")
}

func TestHandler_CreateCustomPreset_AWG(t *testing.T) {
	ts := newTestServer(t)
	form := url.Values{
		"name":        {"my-awg"},
		"protocol":    {"awg"},
		"description": {"custom AWG"},
		"awg_jc":      {"120"},
		"awg_jmin":    {"50"},
		"awg_jmax":    {"1000"},
		"awg_s1":      {"15"},
		"awg_s2":      {"85"},
		"awg_h1":      {"164"},
		"awg_h2":      {"18"},
		"awg_h3":      {"59"},
		"awg_h4":      {"110"},
		"awg_cps_level": {"3"},
		"awg_mimicry":   {"quic"},
	}
	w := ts.post("/ui/presets", form)
	ts.assertStatus(w, http.StatusOK)
	// Verify it persisted in settings.CustomPresets and is resolvable.
	st := chain.NewStore(ts.storePath)
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if err := json.Unmarshal(settings.CustomPresets, &customs); err != nil {
		t.Fatalf("unmarshal CustomPresets: %v", err)
	}
	found := false
	for _, c := range customs {
		if c.Name == "my-awg" {
			found = true
			if c.Protocol != "awg" {
				t.Errorf("Protocol: got %q want awg", c.Protocol)
			}
			if c.AWG == nil || c.AWG.JC != 120 {
				t.Errorf("AWG.JC not saved: %+v", c.AWG)
			}
		}
	}
	if !found {
		t.Errorf("custom preset my-awg not persisted")
	}
	if _, ok := chain.GetPreset("my-awg"); !ok {
		t.Errorf("custom preset not resolvable via GetPreset")
	}
}

func TestHandler_DeletePreset_RefusesIfInUse(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	// Create a custom preset + a chain referencing it.
	_ = st.SaveSettings(&model.PanelSettings{CustomPresets: mustJSON([]chain.ConnectionPreset{{Name: "inuse", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})})
	_ = chain.LoadPresets([]chain.ConnectionPreset{{Name: "inuse", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})
	_ = st.SaveChain(&model.Chain{Name: "c1", Nodes: []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}}, UserProtocol: model.UserProtocolAWG, ObfuscationProfile: "inuse"})
	w := ts.delete("/ui/presets/inuse")
	ts.assertStatus(w, http.StatusConflict)
}

func TestHandler_DeletePreset_OK(t *testing.T) {
	ts := newTestServer(t)
	st := chain.NewStore(ts.storePath)
	_ = st.SaveSettings(&model.PanelSettings{CustomPresets: mustJSON([]chain.ConnectionPreset{{Name: "free", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})})
	_ = chain.LoadPresets([]chain.ConnectionPreset{{Name: "free", Protocol: "awg", AWG: &chain.AWGPreset{JC: 1}}})
	w := ts.delete("/ui/presets/free")
	ts.assertStatus(w, http.StatusOK)
}

func mustJSON(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
```

Run: `go test ./internal/web/ -run "TestHandler_PresetsPage|TestHandler_CreateCustomPreset|TestHandler_DeletePreset" -v` → RED (handlers undefined).

- [ ] **Step 2: Implement preset handlers**

Create `internal/web/presets.go`:

```go
package web

// presets.go — custom obfuscation preset CRUD (replaces the dead Profiles
// page). Built-in presets are read-only; custom presets live in
// PanelSettings.CustomPresets and are merged into the chain registry via
// LoadPresets.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
	"github.com/alexeylcp/angry-box/web/templates"
)

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	st := s.store()
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	// built-ins: list all, tagged by Protocol
	builtins := []chain.ConnectionPreset{}
	for _, name := range chain.ListPresets() {
		p, ok := chain.GetPreset(name)
		if !ok {
			continue
		}
		// skip custom ones (they're in customs list)
		if isCustom(customs, name) {
			continue
		}
		builtins = append(builtins, p)
	}
	s.render(w, r, templates.PresetsPage(builtins, customs))
}

func isCustom(customs []chain.ConnectionPreset, name string) bool {
	for _, c := range customs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) handleNewPresetForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, templates.PresetForm(nil))
}

func (s *Server) handleCreatePreset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	p := presetFromForm(r)
	if p.Name == "" {
		http.Error(w, i18n.T(r.Context(), "name required"), http.StatusBadRequest)
		return
	}
	if p.Protocol == "" {
		http.Error(w, i18n.T(r.Context(), "protocol required"), http.StatusBadRequest)
		return
	}
	if _, ok := chain.GetPreset(p.Name); ok {
		http.Error(w, i18n.T(r.Context(), "preset already exists"), http.StatusConflict)
		return
	}
	if err := s.saveCustomPreset(p, false); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "create", "preset", p.Name, chain.AuditPayload{"protocol": p.Protocol}, "operator")
	s.render(w, r, templates.PresetsPage(s.builtinsList(), s.customsList()))
}

func (s *Server) handleEditPresetForm(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, ok := chain.GetPreset(name)
	if !ok {
		http.Error(w, i18n.T(r.Context(), "not found"), http.StatusNotFound)
		return
	}
	if !isCustom(s.customsList(), name) {
		// built-in: read-only, but still show the form (fields disabled)
	}
	s.render(w, r, templates.PresetForm(&p))
}

func (s *Server) handleUpdatePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
	p := presetFromForm(r)
	if p.Name == "" {
		p.Name = name
	}
	if err := s.saveCustomPreset(p, true); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	chain.WriteAudit(s.store(), "update", "preset", p.Name, chain.AuditPayload{"protocol": p.Protocol}, "operator")
	s.render(w, r, templates.PresetsPage(s.builtinsList(), s.customsList()))
}

func (s *Server) handleDeletePreset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	st := s.store()
	// Refuse if a chain or inbound references it.
	if usedBy := presetInUse(st, name); usedBy != "" {
		http.Error(w, fmt.Sprintf(i18n.T(r.Context(), "Preset is in use (chain/inbound references it)"), usedBy), http.StatusConflict)
		return
	}
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	filtered := customs[:0]
	for _, c := range customs {
		if c.Name == name {
			continue
		}
		filtered = append(filtered, c)
	}
	if len(filtered) == 0 {
		settings.CustomPresets = nil
	} else {
		b, _ := json.Marshal(filtered)
		settings.CustomPresets = b
	}
	st.SaveSettings(settings)
	_ = chain.LoadPresets(filtered) // reload registry without the deleted one
	chain.WriteAudit(st, "delete", "preset", name, nil, "operator")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

// presetFromForm builds a ConnectionPreset from the form fields, scoped to the
// chosen protocol (only the matching sub-struct is populated).
func presetFromForm(r *http.Request) chain.ConnectionPreset {
	protocol := strings.TrimSpace(r.FormValue("protocol"))
	p := chain.ConnectionPreset{
		Name:        strings.TrimSpace(r.FormValue("name")),
		Protocol:    protocol,
		Description: strings.TrimSpace(r.FormValue("description")),
	}
	switch protocol {
	case "awg":
		p.AWG = &chain.AWGPreset{
			JC:       atoi(r.FormValue("awg_jc")),
			JMIN:     atoi(r.FormValue("awg_jmin")),
			JMAX:     atoi(r.FormValue("awg_jmax")),
			S1:       atoi(r.FormValue("awg_s1")),
			S2:       atoi(r.FormValue("awg_s2")),
			S3:       atoi(r.FormValue("awg_s3")),
			S4:       atoi(r.FormValue("awg_s4")),
			ITime:    atoi(r.FormValue("awg_itime")),
			H1:       atoi(r.FormValue("awg_h1")),
			H2:       atoi(r.FormValue("awg_h2")),
			H3:       atoi(r.FormValue("awg_h3")),
			H4:       atoi(r.FormValue("awg_h4")),
			CPSLevel: atoi(r.FormValue("awg_cps_level")),
			Mimicry:  strings.TrimSpace(r.FormValue("awg_mimicry")),
		}
	case "vless-reality":
		p.Reality = &chain.RealityPreset{
			ServerNames:  formList(r, "reality_server_names"),
			Fingerprints: formList(r, "reality_fingerprints"),
			ShortIDLen:   atoi(r.FormValue("reality_short_id_len")),
		}
	case "xhttp":
		p.XHTTP = &chain.XHTTPPreset{
			Methods:       formList(r, "xhttp_methods"),
			Paths:         formList(r, "xhttp_paths"),
			Hosts:         formList(r, "xhttp_hosts"),
			IdleTimeout:   strings.TrimSpace(r.FormValue("xhttp_idle_timeout")),
			PingTimeout:   strings.TrimSpace(r.FormValue("xhttp_ping_timeout")),
			PaddingBytes:  strings.TrimSpace(r.FormValue("xhttp_padding_bytes")),
			MaxConcurrency: strings.TrimSpace(r.FormValue("xhttp_max_concurrency")),
			UpstreamHost:  strings.TrimSpace(r.FormValue("xhttp_upstream_host")),
			DownstreamHost: strings.TrimSpace(r.FormValue("xhttp_downstream_host")),
		}
	}
	return p
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

func formList(r *http.Request, field string) []string {
	vals := r.Form[field]
	out := []string{}
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *Server) saveCustomPreset(p chain.ConnectionPreset, isUpdate bool) error {
	st := s.store()
	settings, _ := st.GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	replaced := false
	for i, c := range customs {
		if c.Name == p.Name {
			if !isUpdate {
				return fmt.Errorf("preset %q already exists", p.Name)
			}
			customs[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		customs = append(customs, p)
	}
	b, err := json.Marshal(customs)
	if err != nil {
		return err
	}
	settings.CustomPresets = b
	st.SaveSettings(settings)
	_ = chain.LoadPresets(customs) // reload registry
	return nil
}

func (s *Server) builtinsList() []chain.ConnectionPreset {
	out := []chain.ConnectionPreset{}
	customs := s.customsList()
	for _, name := range chain.ListPresets() {
		p, ok := chain.GetPreset(name)
		if !ok || isCustom(customs, name) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (s *Server) customsList() []chain.ConnectionPreset {
	settings, _ := s.store().GetSettings()
	var customs []chain.ConnectionPreset
	if len(settings.CustomPresets) > 0 {
		_ = json.Unmarshal(settings.CustomPresets, &customs)
	}
	return customs
}

// presetInUse returns a non-empty string describing the first chain/inbound
// referencing the preset, or "" if unused.
func presetInUse(st *chain.Store, name string) string {
	chains, _ := st.ListChains()
	for _, c := range chains {
		if c.ObfuscationProfile == name {
			return "chain:" + c.Name
		}
	}
	infos, _ := st.ListNodeInfos()
	for _, info := range infos {
		for _, ib := range info.Inbounds {
			if ib.Obfuscation == name {
				return "inbound:" + info.ID + "/" + ib.Tag
			}
		}
	}
	return ""
}
```

- [ ] **Step 3: Add routes**

In `internal/web/server.go`, add (where the Profiles routes were):
```go
mux.HandleFunc("GET /ui/presets", s.auth(s.handlePresets))
mux.HandleFunc("POST /ui/presets", s.auth(s.handleCreatePreset))
mux.HandleFunc("GET /ui/presets/new", s.auth(s.handleNewPresetForm))
mux.HandleFunc("GET /ui/presets/{name}/edit", s.auth(s.handleEditPresetForm))
mux.HandleFunc("POST /ui/presets/{name}/edit", s.auth(s.handleUpdatePreset))
mux.HandleFunc("DELETE /ui/presets/{name}", s.auth(s.handleDeletePreset))
```

- [ ] **Step 4: Build + test**

`go build ./...` → will fail because `templates.PresetsPage` + `templates.PresetForm` don't exist yet (Task 7). To keep this task independently buildable, create minimal stub templ components NOW in `web/templates/presets.templ`:
```go
package templates

import "github.com/alexeylcp/angry-box/internal/chain"

templ PresetsPage(builtins []chain.ConnectionPreset, customs []chain.ConnectionPreset) {
	<div>Presets page (stub)</div>
}
templ PresetForm(p *chain.ConnectionPreset) {
	<div>Preset form (stub)</div>
}
```
Run `templ generate` → `go build ./...` → compile.
Run `go test ./internal/web/ -run "TestHandler_PresetsPage|TestHandler_CreateCustomPreset|TestHandler_DeletePreset" -v` → the page-render test asserts `assertContains(w, "Presets")` — the stub renders "Presets page (stub)" which contains "Presets" → PASS. The create/delete tests assert on store state, not template → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/presets.go internal/web/handlers_presets_test.go internal/web/server.go web/templates/presets.templ web/templates/presets_templ.go
git commit -m "feat(presets): custom preset CRUD handlers + /ui/presets routes + stub templates"
```

---

## Task 6 — Inbound form: server-side per-protocol map

**Files:**
- Modify: `internal/web/nodes.go:401` (`handleNodeInboundsForm`)
- Modify: `web/templates/nodes.templ` (`NodeInboundsForm` — drop `presets []string` param; dropdown populated from `protocolPresetsJSON` only)
- Modify: `web/static/js/app.js` (`filterPresetsForRow` — already populates from the map; verify it handles an empty baked-in list)

**Interfaces:**
- Consumes: `chain.ListPresetsForProtocol` (Task 2 strict filter).
- Produces: per-protocol preset dropdowns in the inbound form.

- [ ] **Step 1: Change handleNodeInboundsForm to pass per-protocol map only**

In `internal/web/nodes.go`, `handleNodeInboundsForm` (~line 415), replace:
```go
presets := chain.ListPresets()
```
with (drop the `presets` param entirely; keep only the per-protocol map):
```go
protocolPresets := map[string][]string{
	"awg":           chain.ListPresetsForProtocol("awg"),
	"vless-reality": chain.ListPresetsForProtocol("vless-reality"),
	"xhttp":         chain.ListPresetsForProtocol("xhttp"),
	"shadowsocks":   chain.ListPresetsForProtocol("xhttp"), // SS uses XHTTP presets
	"trojan":        chain.ListPresetsForProtocol("xhttp"),
	"vmess":         chain.ListPresetsForProtocol("xhttp"),
	"telemt":        chain.ListPresetsForProtocol("xhttp"),
	// tuic/hysteria2 frozen — omitted from new-selection dropdowns
}
presetsJSON, _ := json.Marshal(protocolPresets)
s.render(w, r, templates.NodeInboundsForm(info, users, string(presetsJSON)))
```
Drop the `presets []string` argument from the `templates.NodeInboundsForm` call.

- [ ] **Step 2: Update NodeInboundsForm signature + template**

In `web/templates/nodes.templ`, change `NodeInboundsForm(info *model.NodeInfo, users []*model.User, presets []string, protocolPresetsJSON string)` → `NodeInboundsForm(info *model.NodeInfo, users []*model.User, protocolPresetsJSON string)`. Remove the `for _, p := range presets` loops that bake all preset names into the dropdown (lines ~359-364, ~427-432, ~504-509). The dropdown now starts EMPTY (only the "None" option baked in, or fully empty) and is populated by `filterPresetsForRow` from `protocolPresetsJSON` on load + protocol `onchange`.

- [ ] **Step 3: Verify filterPresetsForRow populates correctly**

`web/static/js/app.js:45` `filterPresetsForRow` already reads `protocol-presets-data` (the hidden span with `protocolPresetsJSON`) and populates the dropdown from `presetsMap[protocol]`. With the baked-in full list gone, this now populates from scratch. Verify it's called on initial load (htmx:afterSettle at `app.js:144`) so existing rows show their protocol's presets. If a row has a selected `obfuscation` value (existing inbound), the value must be preserved — `filterPresetsForRow` already does `if (found) presetSelect.value = currentValue;`. Confirm by reading the function. No JS change likely needed; just verify.

- [ ] **Step 4: templ generate + build + test**

`templ generate` → `go build ./...` → compile.
`go test ./internal/web/` → PASS. If a test references the old `presets` param of `NodeInboundsForm`, update it.

- [ ] **Step 5: Commit**

```bash
git add internal/web/nodes.go web/templates/nodes.templ web/templates/nodes_templ.go
git commit -m "feat(presets): inbound form dropdown populated from per-protocol map (server-side filter)"
```

---

## Task 7 — `presets.templ` full UI + delete `profiles.templ` + nav

**Files:**
- Modify: `web/templates/presets.templ` (replace stubs with full PresetsPage + PresetForm)
- Delete: `web/templates/profiles.templ` (if it exists)
- Modify: `web/templates/base.templ` (nav: Profiles → Presets)

- [ ] **Step 1: Full PresetsPage + PresetForm**

Replace the stubs in `web/templates/presets.templ` with:

```go
package templates

import (
	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
	"github.com/alexeylcp/angry-box/internal/i18n"
)

templ PresetsPage(builtins []chain.ConnectionPreset, customs []chain.ConnectionPreset) {
	<div class="space-y-4">
		<div class="flex items-center justify-between">
			<h2 class="text-2xl font-semibold">{ i18n.T(ctx, "Presets") }</h2>
			<button class="btn btn-primary btn-sm" hx-get="/ui/presets/new" hx-target="#modal-container">{ i18n.T(ctx, "Create custom preset") }</button>
		</div>
		<div class="card bg-base-100/50 border border-base-content/5">
			<div class="card-body">
				<h3 class="font-semibold">{ i18n.T(ctx, "Custom presets") }</h3>
				if len(customs) == 0 {
					<span class="text-sm text-base-content/50">{ i18n.T(ctx, "No custom presets.") }</span>
				}
				for _, c := range customs {
					<div class="flex items-center gap-2 border border-base-300 rounded-lg p-2">
						<div class="flex-1">
							<span class="font-mono text-sm">{ c.Name }</span>
							<span class="badge badge-xs ml-2">{ c.Protocol }</span>
							if c.Description != "" {
								<span class="text-xs text-base-content/50 ml-2">{ c.Description }</span>
							}
						</div>
						<button class="btn btn-ghost btn-xs" hx-get={ "/ui/presets/" + c.Name + "/edit" } hx-target="#modal-container">{ i18n.T(ctx, "Edit") }</button>
						<button class="btn btn-ghost btn-xs text-error" hx-delete={ "/ui/presets/" + c.Name } hx-confirm={ i18n.T(ctx, "Delete preset %s?") } hx-target="closest div" hx-swap="outerHTML">{ i18n.T(ctx, "Delete") }</button>
					</div>
				}
			</div>
		</div>
		<div class="card bg-base-100/50 border border-base-content/5">
			<div class="card-body">
				<h3 class="font-semibold">{ i18n.T(ctx, "Built-in presets") }</h3>
				<div class="overflow-x-auto">
					<table class="table table-sm">
						<thead><tr><th>{ i18n.T(ctx, "Name") }</th><th>{ i18n.T(ctx, "Protocol") }</th><th>{ i18n.T(ctx, "Description") }</th></tr></thead>
						<tbody>
							for _, b := range builtins {
								<tr>
									<td class="font-mono">{ b.Name }</td>
									<td>
										if b.Protocol == "" {
											<span class="badge badge-ghost badge-xs">{ i18n.T(ctx, "global") }</span>
										} else {
											<span class="badge badge-xs">{ b.Protocol }</span>
										}
									</td>
									<td class="text-xs text-base-content/60">{ b.Description }</td>
								</tr>
							}
						</tbody>
					</table>
				</div>
			</div>
		</div>
		<div id="modal-container"></div>
	</div>
}

templ PresetForm(p *chain.ConnectionPreset) {
	<dialog id="preset-modal" class="modal" open>
		<div class="modal-box">
			<h3 class="font-bold text-lg mb-4">
				if p != nil { { i18n.T(ctx, "Edit preset") } } else { { i18n.T(ctx, "Create custom preset") } }
			</h3>
			<form class="space-y-4"
				if p != nil {
					hx-post={ "/ui/presets/" + p.Name + "/edit" }
				} else {
					hx-post="/ui/presets"
				}
				hx-target="#modal-container"
				hx-swap="innerHTML">
				<div class="form-control">
					<label class="label"><span class="label-text">{ i18n.T(ctx, "Preset name") }</span></label>
					<input type="text" name="name" class="input input-bordered" required if p != nil { value={ p.Name } } />
				</div>
				<div class="form-control">
					<label class="label"><span class="label-text">{ i18n.T(ctx, "Protocol") }</span></label>
					<select name="protocol" class="select select-bordered" onchange="var f=this.closest('form');['.awg-fields','.reality-fields','.xhttp-fields'].forEach(function(c){f.querySelector(c).style.display='none'});var m={awg:'.awg-fields','vless-reality':'.reality-fields',xhttp:'.xhttp-fields'}[this.value];if(m){f.querySelector(m).style.display='block'}">
						<option value="" if p == nil || p.Protocol == "" { selected }>{ i18n.T(ctx, "Select protocol...") }</option>
						<option value="awg" if p != nil && p.Protocol == "awg" { selected }>AWG</option>
						<option value="vless-reality" if p != nil && p.Protocol == "vless-reality" { selected }>VLESS + Reality</option>
						<option value="xhttp" if p != nil && p.Protocol == "xhttp" { selected }>XHTTP</option>
					</select>
				</div>
				<div class="form-control">
					<label class="label"><span class="label-text">{ i18n.T(ctx, "Description") }</span></label>
					<input type="text" name="description" class="input input-bordered" if p != nil { value={ p.Description } } />
				</div>
				@awgFields(p)
				@realityFields(p)
				@xhttpFields(p)
				<div class="modal-action">
					<button type="button" class="btn" onclick="this.closest('dialog').close()">{ i18n.T(ctx, "Cancel") }</button>
					<button type="submit" class="btn btn-primary">{ i18n.T(ctx, "Save") }</button>
				</div>
			</form>
		</div>
		<form method="dialog" class="modal-backdrop"><button>{ i18n.T(ctx, "Close") }</button></form>
	</dialog>
}

templ awgFields(p *chain.ConnectionPreset) {
	<div class="awg-fields space-y-2" style={ templ.KV("display", "none") }>
		@presetNumRow(p, "awg_jc", "JC", presetInt(p, "awg", "JC"))
		@presetNumRow(p, "awg_jmin", "JMIN", presetInt(p, "awg", "JMIN"))
		@presetNumRow(p, "awg_jmax", "JMAX", presetInt(p, "awg", "JMAX"))
		@presetNumRow(p, "awg_s1", "S1", presetInt(p, "awg", "S1"))
		@presetNumRow(p, "awg_s2", "S2", presetInt(p, "awg", "S2"))
		@presetNumRow(p, "awg_s3", "S3", presetInt(p, "awg", "S3"))
		@presetNumRow(p, "awg_s4", "S4", presetInt(p, "awg", "S4"))
		@presetNumRow(p, "awg_itime", "ITime", presetInt(p, "awg", "ITime"))
		@presetNumRow(p, "awg_h1", "H1", presetInt(p, "awg", "H1"))
		@presetNumRow(p, "awg_h2", "H2", presetInt(p, "awg", "H2"))
		@presetNumRow(p, "awg_h3", "H3", presetInt(p, "awg", "H3"))
		@presetNumRow(p, "awg_h4", "H4", presetInt(p, "awg", "H4"))
		@presetNumRow(p, "awg_cps_level", "CPS Level", presetInt(p, "awg", "CPSLevel"))
		<div class="form-control">
			<label class="label"><span class="label-text">Mimicry</span></label>
			<select name="awg_mimicry" class="select select-bordered">
				<option value="" if p == nil || p.AWG == nil || p.AWG.Mimicry == "" { selected }></option>
				<option value="quic" if p != nil && p.AWG != nil && p.AWG.Mimicry == "quic" { selected }>quic</option>
				<option value="sip" if p != nil && p.AWG != nil && p.AWG.Mimicry == "sip" { selected }>sip</option>
				<option value="dns" if p != nil && p.AWG != nil && p.AWG.Mimicry == "dns" { selected }>dns</option>
				<option value="none" if p != nil && p.AWG != nil && p.AWG.Mimicry == "none" { selected }>none</option>
			</select>
		</div>
	</div>
}

templ realityFields(p *chain.ConnectionPreset) {
	<div class="reality-fields space-y-2" style={ templ.KV("display", "none") }>
		<div class="form-control">
			<label class="label"><span class="label-text">Server names (comma or repeated)</span></label>
			<input type="text" name="reality_server_names" class="input input-bordered" if p != nil && p.Reality != nil { value={ strings.Join(p.Reality.ServerNames, ", ") } } placeholder="discord.com, www.microsoft.com" />
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">Fingerprints</span></label>
			<input type="text" name="reality_fingerprints" class="input input-bordered" if p != nil && p.Reality != nil { value={ strings.Join(p.Reality.Fingerprints, ", ") } } placeholder="chrome, firefox" />
		</div>
		@presetNumRow(p, "reality_short_id_len", "Short ID len", presetInt(p, "reality", "ShortIDLen"))
	</div>
}

templ xhttpFields(p *chain.ConnectionPreset) {
	<div class="xhttp-fields space-y-2" style={ templ.KV("display", "none") }>
		<div class="form-control">
			<label class="label"><span class="label-text">Methods</span></label>
			<input type="text" name="xhttp_methods" class="input input-bordered" if p != nil && p.XHTTP != nil { value={ strings.Join(p.XHTTP.Methods, ", ") } } placeholder="POST" />
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">Paths</span></label>
			<input type="text" name="xhttp_paths" class="input input-bordered" if p != nil && p.XHTTP != nil { value={ strings.Join(p.XHTTP.Paths, ", ") } } placeholder="/api/v3/, /graphql" />
		</div>
		<div class="form-control">
			<label class="label"><span class="label-text">Hosts</span></label>
			<input type="text" name="xhttp_hosts" class="input input-bordered" if p != nil && p.XHTTP != nil { value={ strings.Join(p.XHTTP.Hosts, ", ") } } placeholder="discord.com" />
		</div>
		@presetNumRow(p, "xhttp_idle_timeout", "Idle timeout", 0)
		@presetNumRow(p, "xhttp_ping_timeout", "Ping timeout", 0)
	</div>
}

templ presetNumRow(p *chain.ConnectionPreset, name string, label string, value int) {
	<div class="form-control">
		<label class="label"><span class="label-text">{ label }</span></label>
		<input type="number" name={ name } class="input input-bordered" value={ fmt.Sprintf("%d", value) } />
	</div>
}
```

Add Go helper funcs at the top of the templ file (plain Go, package templates):
```go
func presetInt(p *chain.ConnectionPreset, section, field string) int {
	if p == nil {
		return 0
	}
	switch section {
	case "awg":
		if p.AWG == nil { return 0 }
		switch field {
		case "JC": return p.AWG.JC
		case "JMIN": return p.AWG.JMIN
		case "JMAX": return p.AWG.JMAX
		case "S1": return p.AWG.S1
		case "S2": return p.AWG.S2
		case "S3": return p.AWG.S3
		case "S4": return p.AWG.S4
		case "ITime": return p.AWG.ITime
		case "H1": return p.AWG.H1
		case "H2": return p.AWG.H2
		case "H3": return p.AWG.H3
		case "H4": return p.AWG.H4
		case "CPSLevel": return p.AWG.CPSLevel
		}
	case "reality":
		if p.Reality == nil { return 0 }
		if field == "ShortIDLen" { return p.Reality.ShortIDLen }
	}
	return 0
}
```
Imports needed: `fmt`, `strings`, `chain`, `model` (model may be unused — drop if so), `i18n`. Add to the templ file's import block. **Note:** templ files use `import (...)` at the top like Go; `fmt.Sprintf` and `strings.Join` are available.

**Reveal-on-protocol:** the form sections (`.awg-fields` etc.) start `display:none`; the protocol select `onchange` reveals the matching one. For an EDIT of an existing preset, the correct section must be shown on load — add an inline `<script>` at the end of `PresetForm` (or a `DOMContentLoaded`-style init in app.js) that, if `p != nil`, sets the matching section visible. Use the app.js fallback pattern (no inline `<script>` in templ if it rejects braces): add `initPresetFormSections()` to `app.js` triggered on `DOMContentLoaded` + `htmx:afterSettle` that finds `#preset-modal` and reveals the section matching the protocol select's value.

- [ ] **Step 2: Delete profiles.templ**

`git rm web/templates/profiles.templ` (if it exists; grep first). Also remove the generated `profiles_templ.go` if present.

- [ ] **Step 3: Nav rename in base.templ**

In `web/templates/base.templ:67`, change the "Profiles" nav item to "Presets" pointing to `/ui/presets`: `<li><a href="/ui/presets">{ i18n.T(ctx, "Presets") }</a></li>`. (Reuse the existing `Presets` i18n key from Task 8 — add it if absent.)

- [ ] **Step 4: app.js initPresetFormSections**

Add to `web/static/js/app.js`:
```js
function initPresetFormSections() {
	var modal = document.getElementById('preset-modal');
	if (!modal) return;
	var sel = modal.querySelector('select[name="protocol"]');
	if (!sel) return;
	var map = {awg:'.awg-fields','vless-reality':'.reality-fields',xhttp:'.xhttp-fields'};
	['.awg-fields','.reality-fields','.xhttp-fields'].forEach(function(c){
		var el = modal.querySelector(c);
		if (el) el.style.display = 'none';
	});
	var m = map[sel.value];
	if (m) { var el = modal.querySelector(m); if (el) el.style.display = 'block'; }
}
document.addEventListener('DOMContentLoaded', initPresetFormSections);
document.addEventListener('htmx:afterSettle', initPresetFormSections);
```

- [ ] **Step 5: templ generate + build + test**

`templ generate` → `go build ./...` → compile. Fix templ syntax issues (re-run `templ generate` after fixes).
`go test ./internal/web/` → PASS.

- [ ] **Step 6: Commit**

```bash
git add web/templates/presets.templ web/templates/presets_templ.go web/templates/base.templ web/templates/base_templ.go web/static/js/app.js
git rm -f web/templates/profiles.templ web/templates/profiles_templ.go 2>/dev/null || true
git commit -m "feat(presets): full PresetsPage + PresetForm UI; delete profiles.templ; nav Profiles->Presets"
```

---

## Task 8 — i18n keys + full build/test

**Files:**
- Modify: `internal/i18n/i18n.go`

- [ ] **Step 1: Add i18n keys en+ru**

Add to BOTH blocks (grep first to skip existing): `Presets`, `Create custom preset`, `Edit preset`, `Delete preset`, `Custom presets`, `Built-in presets`, `Preset name`, `Protocol` (likely exists), `Description` (likely exists), `No custom presets.`, `Select protocol...`, `global`, `preset already exists`, `name required`, `protocol required`, `Preset is in use (chain/inbound references it)`, `Delete preset %s?`.

en values = the key strings; ru values = natural translations (Пресеты / Создать кастомный пресет / Редактировать пресет / Удалить пресет / Кастомные пресеты / Встроенные пресеты / Имя пресета / Протокол / Описание / Нет кастомных пресетов. / Выберите протокол… / глобальный / Пресет уже существует / Имя обязательно / Протокол обязателен / Пресет используется (на него ссылается цепь/inbound) / Удалить пресет %s?).

- [ ] **Step 2: Full build + test**

```
templ generate
go build ./...
go test ./internal/web/ ./internal/chain/ ./internal/backend/singbox/ ./internal/takeover/
```
Expected: all PASS, no TUIC/Hysteria2 touched. `TestDefaultPresetsLoaded` + `TestAWGPresetValuesFromProfiles` still pass (legacy presets kept). The new `TestListPresetsForProtocol_*` + `TestGetDefaultPresetForProtocol` + `TestGetEffectivePreset_PerProtocolDefault` pass.

- [ ] **Step 3: Commit**

```bash
git add internal/i18n/i18n.go
git commit -m "feat(presets): i18n keys for preset CRUD UI (en+ru)"
```

- [ ] **Step 4: Final commit if any test fixes**

```bash
git add -A
git commit -m "test(presets): final build/test pass"
```

---

## Self-review checklist

- [ ] Spec coverage: Protocol tag → Task 1; strict filter + per-protocol default → Task 2; delete Profile/ClientAssignment → Tasks 3+4; custom-preset CRUD → Task 5; inbound per-protocol dropdown → Task 6; full UI → Task 7; i18n → Task 8.
- [ ] No `TODO`/`TBD`/`...` placeholders in code blocks (the `DefaultPresetByProtocol` wiring follow-up is documented, not a placeholder).
- [ ] Legacy preset names kept registered (`russia_2026` etc.) — `TestDefaultPresetsLoaded` + `TestAWGPresetValuesFromProfiles` pass.
- [ ] `GetEffectivePreset` falls back to global default when `UserProtocol == ""` (Task 2 Step 2 correction) — `TestSetDefaultProfileAndGetEffective` passes.
- [ ] `handleClients` preserved (Task 4).
- [ ] No TUIC/Hysteria2 new selection in the preset protocol-select (Task 7 Step 1 — only awg/vless-reality/xhttp options).
- [ ] Each task ends with `go build ./...` and a commit (Task 3+4 combined commit).
- [ ] i18n keys in BOTH en and ru.
- [ ] `templ generate` after every `.templ` edit; generated `*_templ.go` committed.

---

## Out of scope

- **`DefaultPresetByProtocol` wired into `GetEffectivePreset`** (reading PanelSettings from the chain package) — follow-up; the field is on the model but the built-in per-protocol default is used for now.
- **Standalone AWG `NodeInbound` CPS material gap** — not addressed.
- **Routing section in custom presets** — omitted (routing disabled in merged config per AGENTS.md #2).
- **TUIC/Hysteria2 custom-preset creation** — frozen.