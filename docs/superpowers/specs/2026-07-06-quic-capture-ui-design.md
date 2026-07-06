# QUIC Capture UI in Chain Form — Design Spec (Subproject C2)

**Date:** 2026-07-06
**Subproject:** C2 (of C1/C2 — see audit decomposition; A and B are DONE)
**Status:** Draft → pending user review

---

## Problem

One complaint from the audit: "can't find where/how to capture QUIC."

The backend is fully implemented and integrated (`CaptureQUICSignature` at `internal/chain/awgcapture.go:78`, called from `EnsureChainAWGMaterial` at `internal/chain/awg_cps.go:423`). The enabling fields exist on `model.Chain` (`AWGCPSMimicry` at `chain.go:117`, `AWGCPSCaptureDomain` at `chain.go:124`) and auto-persist. But the chain form UI (`web/templates/chains.templ` `NewChainForm`/`EditChainForm`) has **no** CPS mimicry selector and **no** capture-domain input; the create/update handlers (`internal/web/chains.go:32,146`) do not read these fields. So `quic-live` capture is unreachable from the panel — only by hand-editing the JSON store.

This is a **web-layer-only** gap. No model or backend changes are needed.

---

## Audit Summary (facts from current code)

- `model.Chain.AWGCPSMimicry string` (`chain.go:117`, `json:"awg_cps_mimicry,omitempty"`) — accepted values: `"quic-live"`, `"quic"`, `"sip"`, `"dns"`, `"none"`. `quic-live` is the live-capture branch (`awg_cps.go:472` `const mimicryQuicLive = "quic-live"`).
- `model.Chain.AWGCPSCaptureDomain string` (`chain.go:124`, `json:"awg_cps_capture_domain,omitempty"`) — the domain to capture the QUIC signature from.
- Companion cache fields (all on `model.Chain`): `AWGCPSCapturedDomain` (cache key, `chain.go:130`), `AWGCPSCaptureFailedDomain` (`chain.go:139`, suppresses re-dialing a flaky domain), `AWGCPSLevel` (`chain.go`), `AWGCPSI1..I5` (`chain.go:140-144`), `AWGH1..H4` (`chain.go:152-155`).
- `chain.CaptureQUICSignature(domain string, timeout time.Duration) CaptureResult` (`awgcapture.go:78`) — `CaptureResult{OK bool, Source string, Packets []string, Warning string}`. timeout<=0 → 5s default. Dials UDP 443, sends a real AEAD-encrypted QUIC Initial with SNI=domain, captures up to 5 response packets as I1-I5 hex strings. On failure returns `Source:"error"` + human `Warning` (no Go error).
- `chain.EnsureChainAWGMaterial(c *model.Chain, preset ConnectionPreset)` (`awg_cps.go:377`) — integration point. On `mimicry == "quic-live" && c.AWGCPSCaptureDomain != ""` (`awg_cps.go:423`): calls `CaptureQUICSignature`; on success persists `AWGCPSLevel`, `AWGCPSMimicry="quic-live"`, `AWGCPSCapturedDomain`, `AWGCPSI1..I5`, `AWGH1..H4`; on failure records `AWGCPSCaptureFailedDomain` and falls back to synthesized `"quic"` I1-I5 (so the chain never breaks). Called from `ApplyChain` at `applier.go:204`.
- `chain.NormalizeDomain(d string) string` (`awgcapture.go:49`) and `chain.IsValidDomain(d string) bool` (`awgcapture.go:70`) — validators exist.
- `chain.ChainAWGObfsMaterial(c)` (`awg_cps.go:477`) reconstructs the material; `BuildAWGAmnezia` → `BuildAmneziaSection` (`awg_cps.go:306`) emits the amnezia section with captured I1-I5 + H1-H4.
- No preset sets `"quic-live"` (`default_presets.json` all use `"quic"`); `quic-live` is a **chain-level override**, not expressible via the `ObfuscationProfile` (preset-name) dropdown.
- `web/templates/chains.templ`: `NewChainForm` (line 126), `EditChainForm` (line 215) — both receive `c *model.Chain` (nil for new). Grep `awg_cps_mimicry|AWGCPSMimicry|CaptureDomain|quic-live` in chains.templ → zero hits. No CPS section, no mimicry selector, no capture-domain input, no "Capture now" button, no status display.
- `internal/web/chains.go`: `handleCreateChain` (line 32) reads `name, strategy, transport, user_protocol, profile, nodes, entry_nodes` (lines 37-86) — NOT `awg_cps_mimicry`/`awg_cps_capture_domain`. `handleUpdateChain` (line 146) likewise reads only `strategy, transport, user_protocol, profile, nodes, entry_nodes` — does NOT touch `c.AWGCPSMimicry`/`c.AWGCPSCaptureDomain` (preserved only because the whole struct is re-saved). `handleNewChainForm`/`handleEditChainForm` (lines 25,133) render the templ with `hosts` and `profiles` — no CPS context (the templ reads `c` directly, so no extra plumbing needed).
- `chain.ValidateChainUserProtocol` (`chains.go:54`) already gates AWG.

---

## Design

### Principles

- **Web-layer only.** No model or `internal/chain/` changes. All fields + capture logic exist; this spec only surfaces them in the UI.
- **`quic-live` is a chain-level override.** It gets its own field (`awg_cps_mimicry`) next to the existing `profile` (preset-name) dropdown; the capture-domain input (`awg_cps_capture_domain`) is revealed only when mimicry == `quic-live`.
- **Preview before commit.** A "Capture now" HTMX button runs `CaptureQUICSignature` against the entered domain WITHOUT saving, returning an inline I1-I5 preview / failure warning. The capture persists on Save (via `EnsureChainAWGMaterial` during `ApplyChain`).
- **Failed capture is non-blocking.** Backend already falls back to synthesized `"quic"` I1-I5; the UI shows a warning. Saving a chain with `quic-live` + an unreachable domain is allowed (the capture retries on `ApplyChain`).
- **Read-only status for existing chains.** `EditChainForm` shows the persisted capture state (`AWGCPSCapturedDomain`, `AWGCPSCaptureFailedDomain`, `AWGCPSI1..I5`) so the operator sees what the chain is currently using.

### Handlers (`internal/web/chains.go`)

1. **`handleCreateChain`** — read two new form fields:
   ```go
   awgCPSMimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
   awgCPSCaptureDomain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
   ```
   After `user_protocol` validation, when `userProtocol == "awg"`:
   - Validate `awgCPSMimicry` is one of `""`, `"quic-live"`, `"quic"`, `"sip"`, `"dns"`, `"none"` (else 400 "Invalid CPS mimicry mode").
   - If `awgCPSMimicry == "quic-live"` and `awgCPSCaptureDomain != ""`: `awgCPSCaptureDomain = chain.NormalizeDomain(awgCPSCaptureDomain)`; if `!chain.IsValidDomain(awgCPSCaptureDomain)` → 400 "Invalid capture domain". (Empty domain with `quic-live` is allowed — `EnsureChainAWGMaterial` will fall back to synthesized.)
   - Assign `c.AWGCPSMimicry = awgCPSMimicry`, `c.AWGCPSCaptureDomain = awgCPSCaptureDomain`. The remaining CPS fields (`AWGCPSI1..I5`, `AWGH1..H4`, `AWGCPSCapturedDomain`, `AWGCPSLevel`) are generated by `EnsureChainAWGMaterial` on the first `ApplyChain`.

2. **`handleUpdateChain`** — read the same two fields, validate identically. Cache-reset logic on edit:
   - If the new `awgCPSCaptureDomain` differs from `c.AWGCPSCaptureDomain` → clear `c.AWGCPSCapturedDomain = ""` and `c.AWGCPSCaptureFailedDomain = ""` (so `EnsureChainAWGMaterial` re-captures on the new domain at the next `ApplyChain`).
   - If the new `awgCPSMimicry` is no longer `"quic-live"` → clear `c.AWGCPSCaptureDomain`, `c.AWGCPSCapturedDomain`, `c.AWGCPSCaptureFailedDomain` (capture is irrelevant; `EnsureChainAWGMaterial` will derive mimicry from the preset).
   - Assign `c.AWGCPSMimicry` + `c.AWGCPSCaptureDomain` after the resets.

3. **New `handleCaptureQUICPreview`** (`POST /ui/chains/capture-preview`):
   ```go
   func (s *Server) handleCaptureQUICPreview(w http.ResponseWriter, r *http.Request) {
       if err := r.ParseForm(); err != nil { http.Error(w, i18n.T(r.Context(), "bad form"), 400); return }
       domain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
       if domain == "" {
           s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid capture domain") + `</span></div>`})
           return
       }
       domain = chain.NormalizeDomain(domain)
       if !chain.IsValidDomain(domain) {
           s.render(w, r, &simpleHTML{html: `<div class="alert alert-error"><span>` + i18n.T(r.Context(), "Invalid capture domain") + `</span></div>`})
           return
       }
       res := chain.CaptureQUICSignature(domain, 0)
       if res.OK && len(res.Packets) >= 5 {
           // success preview: I1-I5 hex (truncated per line)
           var b strings.Builder
           b.WriteString(`<div class="alert alert-success"><div class="text-xs space-y-1"><div><strong>` + i18n.T(r.Context(), "Capture OK") + `</strong> — ` + escHTML(res.Source) + `</div>`)
           for i, p := range res.Packets {
               b.WriteString(fmt.Sprintf(`<div>I%d: <code>%s</code></div>`, i+1, escHTML(shortHex(p, 40))))
           }
           if res.Warning != "" {
               b.WriteString(`<div class="text-warning">` + escHTML(res.Warning) + `</div>`)
           }
           b.WriteString(`</div></div>`)
           s.render(w, r, &simpleHTML{html: b.String()})
           return
       }
       // failure: warning, non-blocking
       s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning"><div class="text-xs space-y-1"><div><strong>` + i18n.T(r.Context(), "Capture failed, fell back to synthesized QUIC packets") + `</strong></div>` + escHTML(res.Warning) + `</div></div>`})
   }
   ```
   `shortHex(s, n)` is a small helper (place in `htmlx.go` next to `escHTML`): returns `s` if `len <= n` else `s[:n] + "…"`. Pure preview — does NOT save anything.

### Routes (`internal/web/server.go`)

Add in the Chains routes block (near the existing `/ui/chains/*` routes):
```go
mux.HandleFunc("POST /ui/chains/capture-preview", s.auth(s.handleCaptureQUICPreview))
```

### Templates (`web/templates/chains.templ`)

Add an AWG-CPS collapsible section to BOTH `NewChainForm` and `EditChainForm`. It's shown/hidden via JS when `user_protocol == "awg"` (extend the existing `user_protocol` select `onchange`). The section contains:

- **CPS Mimicry select** (`name="awg_cps_mimicry"`): options `""` (Default from preset), `quic-live`, `quic`, `sip`, `dns`, `none`. `onchange` reveals the capture-domain wrap when `value == "quic-live"`.
- **Capture Domain input** (`name="awg_cps_capture_domain"`) + **Capture now** button (`hx-post="/ui/chains/capture-preview"` `hx-target="#capture-preview-result"` `hx-include="closest form"`). The wrap is hidden unless mimicry == `quic-live`.
- **Explainer** (i18n): the long string about UDP 443 / real QUIC Initial / fallback / cache.
- **Preview result target** (`<div id="capture-preview-result" class="mt-2"></div>`).
- **Status block** (EditChainForm only, via `@cpsStatus(c)` when `c != nil && c.AWGCPSCapturedDomain != ""`): read-only `AWGCPSCapturedDomain`, `AWGCPSCaptureFailedDomain` (warning line if non-empty), `AWGCPSI1..I5` (truncated hex).

New templ component `cpsStatus(c *model.Chain)` and a Go helper `shortHex` (in `htmlx.go`, usable from templ). The mimicry/capture-domain values for the form's `selected`/`value` come from `c` (nil-safe: `if c != nil { c.AWGCPSMimicry }` etc.).

JS (`web/static/js/app.js`): the existing `user_protocol` select `onchange` (or a new inline one) toggles `#awg-cps-section` visibility when `value == "awg"`. No client-side crypto.

### i18n (`internal/i18n/i18n.go`)

Add to BOTH `en` and `ru` blocks (grep first to avoid duplicates):
- `CPS Mimicry mode` / `Режим CPS Mimicry`
- `Default (from preset)` / `По умолчанию (из пресета)`
- `quic-live (capture)` / `quic-live (захват)`
- `QUIC Capture Domain` / `Домен захвата QUIC`
- `Capture now` / `Захватить сейчас`
- `Captured from` / `Захвачено с`
- `Last capture failed for` / `Последний захват не удался для`
- `Capture failed, fell back to synthesized QUIC packets` / `Захват не удался, используется синтезированный QUIC`
- `Capture OK` / `Захват успешен`
- `Invalid capture domain` / `Неверный домен захвата`
- `Invalid CPS mimicry mode` / `Неверный режим CPS mimicry`
- The long explainer (en + ru):
  - en: `When set to quic-live, the orchestrator dials the domain over UDP 443, sends a real QUIC Initial, and captures the server's response packets as the AWG CPS silhouette (I1-I5). Falls back to synthesized QUIC packets if the domain is unreachable or doesn't speak QUIC. Capture runs once per domain and is cached; changing the domain re-captures.`
  - ru: `В режиме quic-live оркестратор соединяется с доменом по UDP 443, отправляет реальный QUIC Initial и захватывает пакеты ответа сервера как CPS-силуэт AWG (I1-I5). Если домен недоступен или не говорит на QUIC, используется синтезированный QUIC. Захват выполняется один раз для домена и кэшируется; смена домена запускает повторный захват.`

### Scope boundaries (NOT in C2)

- **C1 (per-protocol presets + custom-preset UI):** separate spec. The `ObfuscationProfile` dropdown and the dead Profiles page are untouched here.
- **TUIC/Hysteria2:** frozen (AGENTS.md #6/#11). The AWG-CPS section only renders for `user_protocol == "awg"`.
- **Backend capture logic:** unchanged — `CaptureQUICSignature` and `EnsureChainAWGMaterial` are called as-is.
- **Standalone AWG `NodeInbound` CPS:** the standalone path has no chain to persist material on (audit gap); not addressed here (chain-only).

---

## Files to change (summary)

**Web handlers:**
- `internal/web/chains.go` — `handleCreateChain`/`handleUpdateChain` read + validate `awg_cps_mimicry`/`awg_cps_capture_domain` (with cache-reset on edit); new `handleCaptureQUICPreview`.
- `internal/web/server.go` — add `POST /ui/chains/capture-preview` route.
- `internal/web/htmlx.go` — `shortHex(s string, n int) string` helper.

**Templates:**
- `web/templates/chains.templ` — AWG-CPS section in `NewChainForm` + `EditChainForm`; new `cpsStatus` component.
- `web/static/js/app.js` — toggle `#awg-cps-section` on `user_protocol == "awg"`.

**i18n:**
- `internal/i18n/i18n.go` — new keys en+ru.

**No model or `internal/chain/` changes.**

---

## Build sequence (high level — detailed plan via writing-plans skill)

1. `shortHex` helper in `htmlx.go`.
2. Handlers: `handleCreateChain`/`handleUpdateChain` read + validate + cache-reset; new `handleCaptureQUICPreview`; route.
3. Templates: AWG-CPS section + `cpsStatus`; JS toggle.
4. i18n keys en+ru.
5. `templ generate` → `go build ./...` → `go test ./internal/web/`.
6. Manual smoke: create AWG chain with `quic-live` + `disk.yandex.ru`, click Capture now → preview I1-I5; Save → ApplyChain → verify `AWGCPSCapturedDomain`/`AWGCPSI1..I5` persisted; edit chain, change domain → status resets → re-apply captures new domain.