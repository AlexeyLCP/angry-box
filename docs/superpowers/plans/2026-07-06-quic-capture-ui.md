# QUIC Capture UI Implementation Plan (Subproject C2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the existing AWG QUIC-capture backend (`CaptureQUICSignature` + `EnsureChainAWGMaterial`) in the chain form UI: a CPS Mimicry select + Capture Domain input (AWG-only), a "Capture now" HTMX preview button, and a read-only capture-status block on the Edit form. No model or `internal/chain/` changes — web layer only.

**Architecture:** `handleCreateChain`/`handleUpdateChain` read + validate `awg_cps_mimicry`/`awg_cps_capture_domain` (with cache-reset on edit when the domain/mimicry changes); a new `handleCaptureQUICPreview` runs `chain.CaptureQUICSignature` live and returns an inline I1-I5 preview / failure warning (no save); `chains.templ` gains an AWG-CPS collapsible section (shown via JS when `user_protocol=="awg"`) + a `cpsStatus` component; i18n keys added en+ru.

**Tech Stack:** Go, HTMX + Templ + TailwindCSS/DaisyUI, i18n (`i18n.T(ctx, "key")` in both en/ru).

## Global Constraints

- **AGENTS.md is the law.** Re-read rules 1, 9.
- **Web-layer only.** Do NOT modify `model.Chain` fields, `internal/chain/awgcapture.go`, `awg_cps.go`, `applier.go`, or any backend capture logic. All those exist and work.
- **i18n:** every new user-facing string in BOTH `en` and `ru` blocks in `internal/i18n/i18n.go` (rule 1). Never hardcode English.
- **Build sequence:** after any `.templ` edit → `templ generate` → `go build ./...`. After any Go edit → `go build ./...`. Run `go test ./internal/web/` at the end of each task.
- **Frozen protocols:** TUIC (AGENTS.md #6) / Hysteria2 (#11) — do NOT touch. The AWG-CPS section renders ONLY for `user_protocol == "awg"`.
- **One commit per task.** Format: `feat(quic-capture): ...` / `fix(quic-capture): ...`, end with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- **Commit on `main` directly — do NOT create branches.**

---

## File structure

```
internal/web/htmlx.go            # +shortHex helper
internal/web/chains.go           # handleCreateChain/handleUpdateChain read+validate CPS; +handleCaptureQUICPreview
internal/web/server.go           # +POST /ui/chains/capture-preview route
internal/web/handlers_quiccapture_test.go  # NEW: handler tests
web/templates/chains.templ       # AWG-CPS section in NewChainForm + EditChainForm; +cpsStatus component
web/static/js/app.js            # toggle #awg-cps-section on user_protocol==awg
internal/i18n/i18n.go            # new keys en+ru
```

---

## Task 1 — `shortHex` helper

**Files:**
- Modify: `internal/web/htmlx.go`

**Interfaces:**
- Produces: `shortHex(s string, n int) string` — returns `s` if `len(s) <= n`, else `s[:n] + "…"`.

- [ ] **Step 1: Add the helper**

Append to `internal/web/htmlx.go` (after `escHTML`):

```go
// shortHex returns s if its length is within n bytes, else s truncated to n
// bytes followed by an ellipsis. Used to preview long hex strings (e.g. AWG
// CPS I1-I5 packets) inline without overflowing the UI. Byte-based truncation
// is safe here because the inputs are ASCII hex strings.
func shortHex(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: compiles.

- [ ] **Step 3: Commit**

```bash
git add internal/web/htmlx.go
git commit -m "feat(quic-capture): add shortHex helper for hex preview truncation"
```

---

## Task 2 — Handlers: read + validate + cache-reset + Capture preview

**Files:**
- Modify: `internal/web/chains.go` (handleCreateChain ~line 32, handleUpdateChain ~line 146; add handleCaptureQUICPreview)
- Modify: `internal/web/server.go` (add route)
- Create: `internal/web/handlers_quiccapture_test.go`

**Interfaces:**
- Consumes: `chain.NormalizeDomain(d) string`, `chain.IsValidDomain(d) bool`, `chain.CaptureQUICSignature(domain string, timeout time.Duration) chain.CaptureResult` (all exist). `chain.CaptureResult{OK bool, Source string, Packets []string, Warning string}`.
- Produces: `handleCaptureQUICPreview` handler; create/update chains read `awg_cps_mimicry` + `awg_cps_capture_domain`.

- [ ] **Step 1: Write the handler test file first (RED)**

Create `internal/web/handlers_quiccapture_test.go`:

```go
package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/alexeylcp/angry-box/internal/chain"
	"github.com/alexeylcp/angry-box/internal/domain/model"
)

func TestHandler_CreateChain_WithQuicLive(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":                  {"chain-q"},
		"nodes":                 {"n1"},
		"user_protocol":         {"awg"},
		"awg_cps_mimicry":       {"quic-live"},
		"awg_cps_capture_domain": {"disk.yandex.ru"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusOK)
	st := chain.NewStore(ts.storePath)
	c, err := st.GetChain("chain-q")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSMimicry != "quic-live" {
		t.Errorf("AWGCPSMimicry: got %q want quic-live", c.AWGCPSMimicry)
	}
	if c.AWGCPSCaptureDomain != "disk.yandex.ru" {
		t.Errorf("AWGCPSCaptureDomain: got %q want disk.yandex.ru", c.AWGCPSCaptureDomain)
	}
}

func TestHandler_CreateChain_InvalidMimicry(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":            {"chain-bad"},
		"nodes":           {"n1"},
		"user_protocol":   {"awg"},
		"awg_cps_mimicry": {"bogus"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_CreateChain_InvalidCaptureDomain(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	form := url.Values{
		"name":                  {"chain-bad2"},
		"nodes":                 {"n1"},
		"user_protocol":         {"awg"},
		"awg_cps_mimicry":       {"quic-live"},
		"awg_cps_capture_domain": {"not a domain!!!"},
	}
	w := ts.post("/ui/chains", form)
	ts.assertStatus(w, http.StatusBadRequest)
}

func TestHandler_UpdateChain_ResetsCaptureCacheOnDomainChange(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	// Seed a chain that already has a captured domain.
	if err := st.SaveChain(&model.Chain{
		Name:                  "chain-c",
		Nodes:                 []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}},
		Strategy:              model.StrategyURLTest,
		Transport:             model.TransportXHTTP,
		UserProtocol:          model.UserProtocolAWG,
		AWGCPSMimicry:         "quic-live",
		AWGCPSCaptureDomain:   "old.example.com",
		AWGCPSCapturedDomain:  "old.example.com",
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	form := url.Values{
		"strategy":               {"urltest"},
		"awg_cps_mimicry":        {"quic-live"},
		"awg_cps_capture_domain": {"new.example.com"},
	}
	w := ts.post("/ui/chains/chain-c/edit", form)
	ts.assertStatus(w, http.StatusOK)
	c, err := st.GetChain("chain-c")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSCaptureDomain != "new.example.com" {
		t.Errorf("CaptureDomain: got %q want new.example.com", c.AWGCPSCaptureDomain)
	}
	if c.AWGCPSCapturedDomain != "" {
		t.Errorf("CapturedDomain should be reset on domain change, got %q", c.AWGCPSCapturedDomain)
	}
}

func TestHandler_UpdateChain_ClearsCaptureWhenMimicryLeavesQuicLive(t *testing.T) {
	ts := newTestServer(t)
	ts.createNode("n1", "1.1.1.1:22")
	st := chain.NewStore(ts.storePath)
	if err := st.SaveChain(&model.Chain{
		Name:                 "chain-d",
		Nodes:                []model.ChainNode{{ID: "n1", Addr: "1.1.1.1:22"}},
		Strategy:             model.StrategyURLTest,
		Transport:            model.TransportXHTTP,
		UserProtocol:         model.UserProtocolAWG,
		AWGCPSMimicry:        "quic-live",
		AWGCPSCaptureDomain:  "disk.yandex.ru",
		AWGCPSCapturedDomain: "disk.yandex.ru",
	}); err != nil {
		t.Fatalf("SaveChain: %v", err)
	}
	form := url.Values{
		"strategy":         {"urltest"},
		"awg_cps_mimicry":  {"quic"},
	}
	w := ts.post("/ui/chains/chain-d/edit", form)
	ts.assertStatus(w, http.StatusOK)
	c, err := st.GetChain("chain-d")
	if err != nil {
		t.Fatalf("GetChain: %v", err)
	}
	if c.AWGCPSMimicry != "quic" {
		t.Errorf("Mimicry: got %q want quic", c.AWGCPSMimicry)
	}
	if c.AWGCPSCaptureDomain != "" {
		t.Errorf("CaptureDomain should be cleared when leaving quic-live, got %q", c.AWGCPSCaptureDomain)
	}
	if c.AWGCPSCapturedDomain != "" {
		t.Errorf("CapturedDomain should be cleared when leaving quic-live, got %q", c.AWGCPSCapturedDomain)
	}
}

func TestHandler_CapturePreview_RejectsEmptyDomain(t *testing.T) {
	ts := newTestServer(t)
	w := ts.post("/ui/chains/capture-preview", url.Values{})
	ts.assertStatus(w, http.StatusOK)
	ts.assertContains(w, "Invalid capture domain")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/web/ -run "TestHandler_CreateChain_WithQuicLive|TestHandler_CreateChain_InvalidMimicry|TestHandler_CreateChain_InvalidCaptureDomain|TestHandler_UpdateChain_Resets|TestHandler_UpdateChain_Clears|TestHandler_CapturePreview" -v`
Expected: FAIL (handlers don't read the new fields yet; `handleCaptureQUICPreview` undefined).

- [ ] **Step 3: Extend handleCreateChain**

In `internal/web/chains.go`, inside `handleCreateChain`, after the `userProto` validation block (after `if err := chain.ValidateChainUserProtocol(userProto); err != nil { ... }` ~line 60) and before `profile := ...` (~line 66), add:

```go
// AWG CPS mimicry + QUIC capture domain (chain-level override; preset only
// ever sets "quic", so "quic-live" must be chosen explicitly here).
awgCPSMimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
awgCPSCaptureDomain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
if userProto == model.UserProtocol("awg") {
	switch awgCPSMimicry {
	case "", "quic-live", "quic", "sip", "dns", "none":
		// ok
	default:
		http.Error(w, i18n.T(r.Context(), "Invalid CPS mimicry mode"), http.StatusBadRequest)
		return
	}
	if awgCPSMimicry == "quic-live" && awgCPSCaptureDomain != "" {
		awgCPSCaptureDomain = chain.NormalizeDomain(awgCPSCaptureDomain)
		if !chain.IsValidDomain(awgCPSCaptureDomain) {
			http.Error(w, i18n.T(r.Context(), "Invalid capture domain"), http.StatusBadRequest)
			return
		}
	}
} else {
	// Non-AWG: ignore any CPS fields silently (they shouldn't be sent).
	awgCPSMimicry = ""
	awgCPSCaptureDomain = ""
}
```

Then, in the `c := &model.Chain{...}` literal (around line 116), add the two fields:

```go
c := &model.Chain{
	Name:               name,
	Nodes:              nodes,
	Strategy:           model.Strategy(strategy),
	Transport:          transport,
	UserProtocol:        userProto,
	ObfuscationProfile: profile,
	AWGCPSMimicry:      awgCPSMimicry,
	AWGCPSCaptureDomain: awgCPSCaptureDomain,
}
```

- [ ] **Step 4: Extend handleUpdateChain**

In `handleUpdateChain`, after the `userProto` block (after the `c.UserProtocol = userProto` assignment, ~line 175) and before `c.ObfuscationProfile = ...` (~line 178), add:

```go
// AWG CPS mimicry + QUIC capture domain.
awgCPSMimicry := strings.TrimSpace(r.FormValue("awg_cps_mimicry"))
awgCPSCaptureDomain := strings.TrimSpace(r.FormValue("awg_cps_capture_domain"))
if c.UserProtocol == model.UserProtocol("awg") {
	switch awgCPSMimicry {
	case "", "quic-live", "quic", "sip", "dns", "none":
		// ok
	default:
		http.Error(w, i18n.T(r.Context(), "Invalid CPS mimicry mode"), http.StatusBadRequest)
		return
	}
	if awgCPSMimicry == "quic-live" && awgCPSCaptureDomain != "" {
		awgCPSCaptureDomain = chain.NormalizeDomain(awgCPSCaptureDomain)
		if !chain.IsValidDomain(awgCPSCaptureDomain) {
			http.Error(w, i18n.T(r.Context(), "Invalid capture domain"), http.StatusBadRequest)
			return
		}
	}
	// Cache reset: changing the capture domain invalidates the prior capture.
	if awgCPSCaptureDomain != c.AWGCPSCaptureDomain {
		c.AWGCPSCapturedDomain = ""
		c.AWGCPSCaptureFailedDomain = ""
	}
	// Leaving quic-live: capture is irrelevant, drop all capture fields.
	if awgCPSMimicry != "quic-live" {
		c.AWGCPSCaptureDomain = ""
		c.AWGCPSCapturedDomain = ""
		c.AWGCPSCaptureFailedDomain = ""
		c.AWGCPSMimicry = awgCPSMimicry
		return  // skip the assignment block below; capture domain stays ""
	}
	c.AWGCPSMimicry = awgCPSMimicry
	c.AWGCPSCaptureDomain = awgCPSCaptureDomain
} else {
	// Non-AWG chain: clear any stale CPS fields (e.g. protocol switched away
	// from AWG in this same edit).
	c.AWGCPSMimicry = ""
	c.AWGCPSCaptureDomain = ""
	c.AWGCPSCapturedDomain = ""
	c.AWGCPSCaptureFailedDomain = ""
}
```

**Important:** the `return` inside the `if awgCPSMimicry != "quic-live"` branch is WRONG — it would skip the rest of `handleUpdateChain` (nodes update, save, render). Fix: do NOT `return`; instead use an `if/else` that assigns without returning. Replace the `return` line above with nothing — restructure as:

```go
	if awgCPSMimicry != "quic-live" {
		c.AWGCPSCaptureDomain = ""
		c.AWGCPSCapturedDomain = ""
		c.AWGCPSCaptureFailedDomain = ""
	} else {
		c.AWGCPSCaptureDomain = awgCPSCaptureDomain
	}
	c.AWGCPSMimicry = awgCPSMimicry
} else {
	c.AWGCPSMimicry = ""
	c.AWGCPSCaptureDomain = ""
	c.AWGCPSCapturedDomain = ""
	c.AWGCPSCaptureFailedDomain = ""
}
```

(No `return`. The function continues to `c.ObfuscationProfile = ...`, node update, `SaveChain`, render.)

- [ ] **Step 5: Add handleCaptureQUICPreview**

Append to `internal/web/chains.go`:

```go
// handleCaptureQUICPreview runs chain.CaptureQUICSignature against the
// domain entered in the chain form and returns an inline I1-I5 preview (or a
// failure warning). It does NOT save anything — pure preview. The actual
// persist happens on ApplyChain via EnsureChainAWGMaterial.
func (s *Server) handleCaptureQUICPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, i18n.T(r.Context(), "bad form"), http.StatusBadRequest)
		return
	}
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
	s.render(w, r, &simpleHTML{html: `<div class="alert alert-warning"><div class="text-xs space-y-1"><div><strong>` + i18n.T(r.Context(), "Capture failed, fell back to synthesized QUIC packets") + `</strong></div><div>` + escHTML(res.Warning) + `</div></div></div>`})
}
```

Ensure `fmt` + `strings` + `chain` + `i18n` are imported in `chains.go` (they already are).

- [ ] **Step 6: Add the route**

In `internal/web/server.go`, in the Chains routes block (near `mux.HandleFunc("POST /ui/chains", ...)` ~line 200), add:

```go
mux.HandleFunc("POST /ui/chains/capture-preview", s.auth(s.handleCaptureQUICPreview))
```

- [ ] **Step 7: Run tests (GREEN)**

Run: `go test ./internal/web/ -run "TestHandler_CreateChain_WithQuicLive|TestHandler_CreateChain_InvalidMimicry|TestHandler_CreateChain_InvalidCaptureDomain|TestHandler_UpdateChain_Resets|TestHandler_UpdateChain_Clears|TestHandler_CapturePreview" -v`
Expected: PASS.

Run: `go test ./internal/web/`
Expected: PASS (full suite).

- [ ] **Step 8: Commit**

```bash
git add internal/web/chains.go internal/web/server.go internal/web/handlers_quiccapture_test.go
git commit -m "feat(quic-capture): chain handlers read/validate CPS fields + Capture preview endpoint"
```

---

## Task 3 — Templates: AWG-CPS section + cpsStatus + JS toggle

**Files:**
- Modify: `web/templates/chains.templ` (NewChainForm ~line 126, EditChainForm ~line 215; add cpsStatus component)
- Modify: `web/static/js/app.js` (toggle #awg-cps-section)

**Interfaces:**
- Consumes: Task 2 handlers + route. `model.Chain.AWGCPSMimicry/AWGCPSCaptureDomain/AWGCPSCapturedDomain/AWGCPSCaptureFailedDomain/AWGCPSI1..I5`.
- Produces: AWG-CPS form section in both chain forms; `cpsStatus` component; JS toggle.

- [ ] **Step 1: Add a reusable `awgCPSSection` templ component**

In `web/templates/chains.templ`, add a component that takes the current chain (nullable) and renders the CPS block. Place it above `NewChainForm`:

```go
// awgCPSSection renders the AWG CPS mimicry + QUIC capture-domain controls.
// c may be nil (new chain). Shown/hidden by JS based on user_protocol=="awg".
templ awgCPSSection(c *model.Chain) {
	<div id="awg-cps-section" class="space-y-3 hidden">
		<div class="form-control">
			<label class="label"><span class="label-text">{ i18n.T(ctx, "CPS Mimicry mode") }</span></label>
			<select name="awg_cps_mimicry" class="select select-bordered"
				onchange="var w=this.parentElement.parentElement.querySelector('.capture-domain-wrap');if(w){w.style.display=this.value==='quic-live'?'block':'none'}">
				<option value="" if c == nil || c.AWGCPSMimicry == "" { selected }>{ i18n.T(ctx, "Default (from preset)") }</option>
				<option value="quic-live" if c != nil && c.AWGCPSMimicry == "quic-live" { selected }>{ i18n.T(ctx, "quic-live (capture)") }</option>
				<option value="quic" if c != nil && c.AWGCPSMimicry == "quic" { selected }>{ i18n.T(ctx, "quic") }</option>
				<option value="sip" if c != nil && c.AWGCPSMimicry == "sip" { selected }>{ i18n.T(ctx, "sip") }</option>
				<option value="dns" if c != nil && c.AWGCPSMimicry == "dns" { selected }>{ i18n.T(ctx, "dns") }</option>
				<option value="none" if c != nil && c.AWGCPSMimicry == "none" { selected }>{ i18n.T(ctx, "none") }</option>
			</select>
		</div>
		<div class="capture-domain-wrap form-control" style={ templ.KV("display", "none") }>
			if c != nil && c.AWGCPSMimicry == "quic-live" {
				<div style="display:block"></div>
			}
		</div>
		<div class="capture-domain-wrap form-control" if c != nil && c.AWGCPSMimicry == "quic-live" { style="display:block" } else { style="display:none" }>
			<label class="label"><span class="label-text">{ i18n.T(ctx, "QUIC Capture Domain") }</span></label>
			<div class="join">
				<input type="text" name="awg_cps_capture_domain" class="input input-bordered join-item"
					if c != nil { value={ c.AWGCPSCaptureDomain } } placeholder="disk.yandex.ru" />
				<button type="button" class="btn join-item"
					hx-post="/ui/chains/capture-preview"
					hx-target="#capture-preview-result"
					hx-include="closest form">
					{ i18n.T(ctx, "Capture now") }
				</button>
			</div>
			<label class="label"><span class="label-text-alt text-base-content/50">{ i18n.T(ctx, "When set to quic-live, the orchestrator dials the domain over UDP 443, sends a real QUIC Initial, and captures the server's response packets as the AWG CPS silhouette (I1-I5). Falls back to synthesized QUIC packets if the domain is unreachable or doesn't speak QUIC. Capture runs once per domain and is cached; changing the domain re-captures.") }</span></label>
		</div>
		<div id="capture-preview-result" class="mt-2"></div>
		if c != nil && c.AWGCPSCapturedDomain != "" {
			@cpsStatus(c)
		}
	</div>
}
```

**Note on the double `.capture-domain-wrap`:** the first empty `div` above is a mistake — remove it. Keep ONLY ONE `.capture-domain-wrap` (the one with the input). The conditional `style` using `if/else` inside a templ attribute is the tricky part; templ supports `style={ ... }` but conditional attributes are better done with a plain `if` rendering one of two divs, OR using `templ.KV`. **Chosen clean approach:** render the capture-domain wrap always present in the DOM (so the input always exists for `hx-include`), but toggle visibility via the mimicry select's `onchange` AND the initial inline style based on the current mimicry. Use:

```go
<div class="capture-domain-wrap form-control">
	<label class="label"><span class="label-text">{ i18n.T(ctx, "QUIC Capture Domain") }</span></label>
	<div class="join">
		<input type="text" name="awg_cps_capture_domain" class="input input-bordered join-item"
			if c != nil { value={ c.AWGCPSCaptureDomain } } placeholder="disk.yandex.ru" />
		<button type="button" class="btn join-item"
			hx-post="/ui/chains/capture-preview"
			hx-target="#capture-preview-result"
			hx-include="closest form">
			{ i18n.T(ctx, "Capture now") }
		</button>
	</div>
	<label class="label"><span class="label-text-alt text-base-content/50">{ i18n.T(ctx, "When set to quic-live, ...") }</span></label>
</div>
```

And add an inline `<script>` at the END of `awgCPSSection` that sets the initial display based on the select's current value, plus the `onchange` on the select toggles it. Concretely, the select keeps the `onchange` from above, and the wrap's initial visibility is set by a tiny script:

```html
<script>
	(function(){
		var sec = document.currentScript.previousElementSibling.closest('#awg-cps-section');
		var sel = sec.querySelector('select[name="awg_cps_mimicry"]');
		var wrap = sec.querySelector('.capture-domain-wrap');
		if (wrap) { wrap.style.display = (sel && sel.value === 'quic-live') ? 'block' : 'none'; }
	})();
</script>
```

This avoids fighting templ's conditional-attribute syntax. (If the templ compiler rejects inline `<script>` with `{` braces, use a plain `<script>` with no templ interpolations inside — the script is pure JS, no Go templating. It is shown above with no `{ }` templ interpolations, so it's safe.)

- [ ] **Step 2: Add `cpsStatus` component**

In `web/templates/chains.templ`, add:

```go
// cpsStatus renders a read-only summary of the chain's persisted AWG CPS
// capture state (shown only on the Edit form for an existing chain).
templ cpsStatus(c *model.Chain) {
	<div class="alert alert-info">
		<div class="text-xs space-y-1">
			<div><strong>{ i18n.T(ctx, "Captured from") }:</strong> { c.AWGCPSCapturedDomain }</div>
			if c.AWGCPSCaptureFailedDomain != "" {
				<div class="text-warning"><strong>{ i18n.T(ctx, "Last capture failed for") }:</strong> { c.AWGCPSCaptureFailedDomain }</div>
			}
			if c.AWGCPSI1 != "" {
				<div><strong>I1-I5:</strong>
					<code class="text-[10px]">{ shortHex(c.AWGCPSI1, 40) }</code>
					<code class="text-[10px]">{ shortHex(c.AWGCPSI2, 40) }</code>
					<code class="text-[10px]">{ shortHex(c.AWGCPSI3, 40) }</code>
					<code class="text-[10px]">{ shortHex(c.AWGCPSI4, 40) }</code>
					<code class="text-[10px]">{ shortHex(c.AWGCPSI5, 40) }</code>
				</div>
			}
		</div>
	</div>
}
```

**Verify the exact field names** on `model.Chain`: the audit said `AWGCPSI1..I5`. Grep `AWGCPSI1` in `internal/domain/model/chain.go` to confirm the field names before referencing them in templ (templ won't compile if the field doesn't exist). If they are named differently (e.g. `AWGCPS_I1`), adjust.

- [ ] **Step 3: Insert `@awgCPSSection` into both forms**

In `NewChainForm`, after the Obfuscation Profile form-control (around line 170, before the Routing Strategy block), add:

```html
@awgCPSSection(nil)
```

In `EditChainForm`, in the corresponding location (after the profile select), add:

```html
@awgCPSSection(c)
```

(The `EditChainForm` signature is `EditChainForm(c *model.Chain, availableHosts []*model.Host, profiles []string)` — `c` is in scope.)

- [ ] **Step 4: JS toggle for #awg-cps-section**

The `user_protocol` select in both forms needs an `onchange` that shows/hides `#awg-cps-section` when `value == "awg"`. In `web/static/js/app.js`, add (or extend the existing DOMContentLoaded listener):

```js
// Show the AWG CPS section only when user_protocol == "awg".
function toggleAWGCPSSection() {
    document.querySelectorAll('select[name="user_protocol"]').forEach(function(sel) {
        var section = sel.closest('form').querySelector('#awg-cps-section');
        if (section) { section.style.display = (sel.value === 'awg') ? 'block' : 'none'; }
    });
}
document.addEventListener('DOMContentLoaded', toggleAWGCPSSection);
document.addEventListener('htmx:afterSettle', toggleAWGCPSSection);
```

And add `onchange="toggleAWGCPSSection()"` to BOTH `user_protocol` selects in `chains.templ` (lines ~155 and ~242). (The `#awg-cps-section` `hidden` class from Step 1 stays as the initial state; the JS toggles `style.display`.)

- [ ] **Step 5: templ generate + build + test**

Run: `templ generate`
Run: `go build ./...` → must compile. If templ fails on the inline `<script>` or conditional `style`, adjust per the notes above (the chosen approach uses plain `<script>` with no templ interpolations + JS-driven visibility, which templ accepts).
Run: `go test ./internal/web/` → PASS. (The handler tests from Task 2 don't assert on templates; template compilation is the gate here.)

- [ ] **Step 6: Commit**

```bash
git add web/templates/chains.templ web/templates/chains_templ.go web/static/js/app.js
git commit -m "feat(quic-capture): AWG-CPS section + cpsStatus in chain forms + JS toggle"
```

---

## Task 4 — i18n keys

**Files:**
- Modify: `internal/i18n/i18n.go`

- [ ] **Step 1: Add keys to en block**

At the end of the `en` block (before its closing `},`), add:

```
"CPS Mimicry mode": "CPS Mimicry mode",
"Default (from preset)": "Default (from preset)",
"quic-live (capture)": "quic-live (capture)",
"quic": "quic",
"sip": "sip",
"dns": "dns",
"none": "none",
"QUIC Capture Domain": "QUIC Capture Domain",
"Capture now": "Capture now",
"Captured from": "Captured from",
"Last capture failed for": "Last capture failed for",
"Capture failed, fell back to synthesized QUIC packets": "Capture failed, fell back to synthesized QUIC packets",
"Capture OK": "Capture OK",
"Invalid capture domain": "Invalid capture domain",
"Invalid CPS mimicry mode": "Invalid CPS mimicry mode",
"When set to quic-live, the orchestrator dials the domain over UDP 443, sends a real QUIC Initial, and captures the server's response packets as the AWG CPS silhouette (I1-I5). Falls back to synthesized QUIC packets if the domain is unreachable or doesn't speak QUIC. Capture runs once per domain and is cached; changing the domain re-captures.": "When set to quic-live, the orchestrator dials the domain over UDP 443, sends a real QUIC Initial, and captures the server's response packets as the AWG CPS silhouette (I1-I5). Falls back to synthesized QUIC packets if the domain is unreachable or doesn't speak QUIC. Capture runs once per domain and is cached; changing the domain re-captures.",
```

Grep first for each key to skip existing (`quic`/`sip`/`dns`/`none` may already exist — if so, do not re-add; duplicate map keys cause a build error). Only add keys that are genuinely absent.

- [ ] **Step 2: Add keys to ru block**

At the end of the `ru` block (before its closing `},`), add the same keys with ru values:

```
"CPS Mimicry mode": "Режим CPS Mimicry",
"Default (from preset)": "По умолчанию (из пресета)",
"quic-live (capture)": "quic-live (захват)",
"QUIC Capture Domain": "Домен захвата QUIC",
"Capture now": "Захватить сейчас",
"Captured from": "Захвачено с",
"Last capture failed for": "Последний захват не удался для",
"Capture failed, fell back to synthesized QUIC packets": "Захват не удался, используется синтезированный QUIC",
"Capture OK": "Захват успешен",
"Invalid capture domain": "Неверный домен захвата",
"Invalid CPS mimicry mode": "Неверный режим CPS mimicry",
"When set to quic-live, the orchestrator dials the domain over UDP 443, sends a real QUIC Initial, and captures the server's response packets as the AWG CPS silhouette (I1-I5). Falls back to synthesized QUIC packets if the domain is unreachable or doesn't speak QUIC. Capture runs once per domain and is cached; changing the domain re-captures.": "В режиме quic-live оркестратор соединяется с доменом по UDP 443, отправляет реальный QUIC Initial и захватывает пакеты ответа сервера как CPS-силуэт AWG (I1-I5). Если домен недоступен или не говорит на QUIC, используется синтезированный QUIC. Захват выполняется один раз для домена и кэшируется; смена домена запускает повторный захват.",
```

(Skip `quic`/`sip`/`dns`/`none` if they already exist in ru — those are protocol literals, same in both languages.)

- [ ] **Step 3: Build + test**

Run: `go build ./...` → compiles (no duplicate keys).
Run: `go test ./internal/web/` → PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/i18n/i18n.go
git commit -m "feat(quic-capture): i18n keys for AWG CPS UI (en+ru)"
```

---

## Task 5 — Full build + test + smoke checklist

- [ ] **Step 1**

Run:
```
templ generate
go build ./...
go test ./internal/web/ ./internal/chain/
```
Expected: all PASS, no TUIC/Hysteria2 touched.

- [ ] **Step 2: Manual smoke (user)**

1. Open "Create chain" → select `user_protocol=awg` → AWG-CPS section appears.
2. Set mimicry=`quic-live` → capture-domain input appears. Enter `disk.yandex.ru`, click "Capture now" → inline preview I1-I5 (success) or fallback warning.
3. Save the chain → `AWGCPSMimicry="quic-live"`, `AWGCPSCaptureDomain="disk.yandex.ru"` persisted (verify via store JSON or re-open Edit).
4. Apply the chain → `EnsureChainAWGMaterial` captures + persists `AWGCPSCapturedDomain`/`AWGCPSI1..I5`; EditChainForm shows `@cpsStatus` with captured-from + I1-I5.
5. Edit chain, change capture domain to `www.bing.com` → `AWGCPSCapturedDomain` reset (status clears) → Apply → re-captures new domain → status shows new capture.
6. Edit chain, switch mimicry to `quic` → capture fields cleared → Apply → `EnsureChainAWGMaterial` derives from preset (synthesized quic).
7. Invalid mimicry (e.g. via curl) → 400 "Invalid CPS mimicry mode". Invalid domain → 400 "Invalid capture domain".

- [ ] **Step 3: Final commit (if any fixes)**

```bash
git add -A
git commit -m "test(quic-capture): final build/test pass"
```

---

## Self-review checklist

- [ ] Spec coverage: Model (no change — confirmed) → Tasks reference existing fields; Handlers → Task 2; Templates → Task 3; i18n → Task 4; Build → Task 5.
- [ ] No `TODO`/`TBD`/`...` placeholders in code blocks (the Task 3 Step 1 "double div" warning is resolved in the chosen approach — single wrap + JS visibility).
- [ ] Field names verified: `model.Chain.AWGCPSMimicry/AWGCPSCaptureDomain/AWGCPSCapturedDomain/AWGCPSCaptureFailedDomain/AWGCPSI1..I5` — implementer greps `chain.go` before referencing in templ.
- [ ] `handleUpdateChain` has NO `return` inside the CPS block (the plan flags the wrong `return` and gives the corrected version — use the corrected one).
- [ ] No TUIC/Hysteria2 code touched.
- [ ] Each task ends with `go build ./...` and a commit.
- [ ] i18n keys in BOTH en and ru (skip existing literals).
- [ ] `templ generate` run after every `.templ` edit; generated `chains_templ.go` committed.

---

## Out of scope

- **C1 (per-protocol presets + custom-preset UI):** separate spec — the `ObfuscationProfile` dropdown and the dead Profiles page are untouched.
- **Standalone AWG `NodeInbound` CPS:** no chain to persist on; not addressed.
- **Backend capture logic changes:** none — `CaptureQUICSignature`/`EnsureChainAWGMaterial` are called as-is.