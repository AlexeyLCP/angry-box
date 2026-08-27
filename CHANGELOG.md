# Changelog

All notable changes to Angry-box are documented here. Versions follow a light
semver: patch (0.x.Y) for fixes/hardening within the v0.2 product focus, minor
(0.Y.0) for new protocols/features. The format is based on Keep a Changelog.

## [v0.8.31] — 2026-08-27

### Feature — LucX-port: naive/mieru/TrustTunnel, AWG 3.1, vpn://, Clash

Standalone NaiveProxy + Mieru + TrustTunnel inbounds (TLS, per-user creds, share URIs). Subscription: Amnezia `vpn://`, Clash Meta, HTML page. AWG PSK, live AWG speed, AWG outbound (`awgo-N`), docker/toolza import.

amnezia-box pin `922fc605` (hoaxisr AWG 3.1 + mtproxy + TrustTunnel `with_trusttunnel`). AWG version `3.1` (RandomTrailers). Kernel src `amneziawg-linux-kernel-module v3.1.20260827`.

**Pins:** `ABX_REF`/`singBoxVersion`=`922fc605`, `amneziaWGGoVersion`=`ae4523c`. Tarball `sing-box-922fc605-amnezia-linux-amd64.tar.gz` on release `v0.1.0`.

## [v0.8.30] — 2026-08-08

### Fix — double plus on Add buttons (SVG + "+ Add …" text)

Primary action buttons (Add User / Inbound / Create Chain) rendered both the
plus SVG icon and a label that already started with "+", producing "++ Add
User". Labels no longer include the leading plus when an icon is present.

**Files:** `web/templates/users.templ`, `inbounds.templ`, `chains.templ`,
`internal/i18n/i18n.go`, `internal/version/version.go`.

## [v0.8.29] — 2026-08-08

### Fix — dark theme dropdowns stay light; + Node opens bare unstyled page

1. **Theme picker / dropdowns white on graphite/night.** DaisyUI slots are OKLCH
   triples (`--b2: 23% 0.006 …`); markup and `--tn-*` aliases used them as
   colors (`background: var(--b2)`) → invalid CSS → browser default light
   surface. Fix: shared post-theme aliases resolve triples via `oklch(var(--*))`
   for `--tn-bg-*` / tints; theme dropdown + shell use `oklch(var(--b2))`;
   Lovable `.pill`/`.seg`/`.inp`/`.lvl` likewise.
2. **Dashboard "+ Node"** used `location.href=…/capture` full-page navigation.
   Capture form is a bare `<dialog>` (no Base/CSS/htmx) → unstyled page; Close
   left a blank white document. Fix: `onclick="addNodeOpenCapture()"` (HTMX into
   `#modal-container`, same as Nodes page). Quick-action buttons get explicit
   `hx-swap="innerHTML"`.

**Files:** `web/static/css/themes.css`, `app.css`, `web/templates/base.templ`,
`dashboard.templ` (+ generated), `internal/version/version.go`.

## [v0.8.28] — 2026-08-07

### Fix — `serve` rejected `--config` → systemd unit crash after install

install.sh v0.8.26+ writes `ExecStart=… serve --config /var/lib/angry-box/angry-box.toml`,
but the `serve` FlagSet did not declare `-config`. Result after fresh install:

```
flag provided but not defined: -config
angry-box.service: Failed with result 'exit-code'
```

Password was generated (and printed) then the process exited. Fix: declare
`-config` on the serve FlagSet; pre-parse accepts `-config` and `--config`.

**Files:** `cmd/angry-box/main.go`, `internal/version/version.go`.

## [v0.8.27] — 2026-08-07

### Fix — AmneziaWG PPA `NO_PUBKEY` on RU/Ubuntu 24.04 (stale list + keyserver blocked)

On RU nodes (and any host where gpg keyservers are firewalled) `install awg
module` still failed with:

```
NO_PUBKEY 4166F2C257290828
E: The repository '.../amnezia/ppa/ubuntu noble InRelease' is not signed
```

even after v0.8.19's modern-keyring path. Two remaining holes:

1. **First `apt-get update` ran under `set -e` BEFORE the key was fixed.** A
   leftover broken PPA list (lucx / ihor / empty keyring) made that update exit
   100 and aborted the whole script — DKMS fallback never ran.
2. **`gpg --keyserver` is often unreachable from RU.** Empty keyring +
   `signed-by=` → same NO_PUBKEY.

**Fix (`installAWGModule` shell):**
- Install the PPA key **before** any `apt-get update`: curl HTTP keyserver API →
  embedded armored key (offline) → gpg keyserver last resort; always
  `gpg --dearmor` into `/usr/share/keyrings/amnezia-archive-keyring.gpg`.
- Wipe legacy `amnezia*.list` / unsigned lines, rewrite
  `deb [signed-by=…] … $CODENAME main` (Debian codenames mapped to jammy).
- All `apt-get update/install` non-fatal so bundled DKMS still runs when PPA is
  unusable.

**Files:** `internal/backend/singbox/singbox.go`, `internal/version/version.go`.

## [v0.8.26] — 2026-08-07

### Fix — external login loop, install password UX, chain "+ Level", UI checkboxes

Tester report (SacredX) after v0.8.25 install:

1. **External Basic Auth "endless login", tunnel OK.** Missing credentials
   (browser challenge) counted toward the per-IP rate limit — external IP locked
   after ~5 page loads while `127.0.0.1` via SSH tunnel still worked. Fix: do
   not rate-limit bare challenges; clear failures on success; threshold 10.
2. **Password / config split-brain.** `DefaultConfigPath` was CWD-relative;
   systemd and a hand-launched `serve -listen 0.0.0.0:8090` minted different
   password hashes. Fix: absolute path (root → `/var/lib/angry-box/angry-box.toml`),
   explicit `--config` in unit/install, `Config.SavePath()`, one-time
   `initial-admin-password` file (0600). install.sh prints login/password at the
   END and documents tunnel vs public bind (no second bare `serve`).
3. **Chain editor "+ Уровень" closed the modal** instead of adding an exit level.
   Form `hx-on::after-request` closed on any child HTMX success. Fix: close only
   when `event.detail.elt===this`; `hx-disinherit="*"` on the add-level button.
4. **Unreadable Lovable checkboxes / "layered" RU text.** DaisyUI OKLCH triples
   used bare (`background: var(--p)`) → invalid CSS → transparent fills. Fix:
   `oklch(var(--*))` + visible tick on `.cb`; solid surfaces / hint line-height.
5. **XHTTP apply still hit awg-quick.** Merged deploy correctly keeps leftover
   AWG inbounds, but InstallAWGModule only looked at the current chain and the
   error was opaque. Fix: install module when rendered AWG files are non-empty;
   error appends why (`standalone inbound "…"`, other AWG chain, …).

**Files:** `internal/web/auth.go`, `authlimiter.go`, `settings.go`,
`internal/config/config.go`, `internal/chain/applier_build.go`, `awg_push.go`,
`scripts/install.sh`, `angry-box.service`, `web/static/css/app.css`,
`web/templates/chainlevels.templ` (+ generated), `internal/version/version.go`.

## [v0.8.25] — 2026-08-07

### Fix — takeover self-loop on empty sing-box scaffold after Deploy

Selecting all three post-capture options (Deploy sing-box + Install AWG +
Detect existing VPN) made angry-box install its own minimal scaffold
(`inbounds:[]`), then treat that scaffold as a foreign VPN. Convert failed
with `sing-box config had no convertible inbounds` and the UI showed a false
`rolled-back` / 0 inbounds.

**Fix (three layers):**
1. **Detect** skips empty/minimal/TUN-only sing-box configs as primary; foreign
   xray/AWG/MTProxy still win. Note explains the scaffold case.
2. **Capture** does not queue the empty Deploy when takeover is also ticked —
   takeover installs sing-box itself after converting the foreign VPN.
3. **Status**: empty → `"nothing"` (info alert, not error); pre-cutover convert
   fail → `"failed"` (not `"rolled-back"`). i18n: «Нечего перехватывать».

**Files:** `internal/takeover/detect.go`, `takeover.go`,
`detect_takeover_test.go`, `internal/web/nodes.go`, `takeover.go`,
`internal/i18n/i18n.go`, `docs/PROGRESS.md` (§47). Tests green.

## [v0.8.24] — 2026-08-07

### Chore — amnezia-box fork bumped to upstream 4bdfc140 (sing-box 1.14 beta) + amneziawg-go /v3

The `AlexeyLCP/amnezia-box` fork is rebased onto the upstream HEAD
(`hoaxisr/amnezia-box @ 4bdfc140`, 2026-08-05); our ports (mtproxy
`with_mtproxy` + fallback round-robin) are re-applied on top (fork commit
`3c554273`). The deployed sing-box binary changes accordingly.

**What the upstream bump brings:**
- sing-box base aligned to **v1.14.0-beta.4** API.
- **amneziawg-go → official AWG3 v3.0.20260805** (module path `/v3`,
  `hoaxisr/amneziawg-go/v3 @ e32b3b0`): keepalive-under-content-padding fix,
  `InputPackets` batched by `device.BatchSize()`.
- clashapi exposes real per-peer AWG handshake/tx/rx via UAPI.
- AWG UDP forwarder starts inside the gVisor tunnel; mieru 3.35.0 (+ UDP
  transport domain resolve, inbound `listen_ports`).

**Pins (three places per docs/PATCHES.md):** `ABX_REF` in
`scripts/build-singbox.sh` / `build-singbox-windows.sh` → `3c554273`;
`singBoxVersion` / `singBoxChecksums` / `amneziaWGGoVersion` in
`internal/backend/singbox/singbox.go`; `patchcheckABXRef` /
`patchcheckAWGGORef` in `patchcheck_test.go`. New tarball
`sing-box-3c554273-amnezia-linux-amd64.tar.gz` (sha256 `acaf995c…`)
published to the `v0.1.0` release the deploy path pins; `deps/checksums.txt`
regenerated.

**Behavior change found by the new base (test-only fix):** sing-box 1.14 beta
resolves AWG endpoint peer addresses during `sing-box check` — the fake
`*.example.test` hostnames in `awg_config_check_test.go` were replaced with
TEST-NET IPs (production peer addresses are always IPs; no product change).

**Verified:** `go build` / `go vet` clean; full `go test ./internal/...` green
including all real `sing-box check` tests against the new binary; patchcheck
version-match tests PASS; manual `sing-box check` accepts `type:"mtproxy"` +
`type:"fallback"`; `sing-box version` reports `with_awg,with_mtproxy` (the
`isPatchedExtended` canary). Live-node deploy verification pending (n1
reimaged, GCloud test VPS stopped) — PROGRESS §46.

## [v0.8.23] — 2026-08-07

### Feature — AWG 1.5 / 2.0 / 3.0 per-version config generation + AWG3 kernel/tools install (lucx-ui reference)

Closes the remaining gaps vs the lucx-ui AWG version switcher: each protocol
version emits its own field set, different versions coexist on one node, and
deploy upgrades the remote kernel module + tools when the node is not yet
AWG3-capable.

**Per-version field gates** (`AWGVersionAtLeast`, order 1.5 < 2 < 3):
- **1.5** — legacy: Jc/S1-S2, H1-H4 as single-int (awg-quick 1.x rejects `lo-hi` ranges); no S3/S4, no CPS I1-I5, no HPK.
- **2.0** — kernel + CPS default: +S3/S4, I1-I5, H1-H4 quadrant ranges.
- **3.0** — + HeaderProtectionKey / ContentPaddingAddition / RekeyAfterTime.

Server and client paths stay in parity (`writeAmneziaConfLines` skips S3/S4 on
1.5; client `.conf` gates S3/S4+I1-I5 to ≥2 and HPK/timers to ≥3). Standalone
v3 client conf now also emits the previously-missing HPK/CPM/RAT block.
Inter-node exit confs stay version `"2"`. Two inbounds on one node (v2 + v3)
render independent field sets from `ib.EffectiveAWGVersion()`.

**AWG3 install path** (`installAWGModule` + `detectKernelAWG3`):
- Kernel capability probe switched from modinfo major≥3 to a **functional
  kallsyms probe** (`awg_header_protection_set_key`) — upstream still stamps
  `PACKAGE_VERSION=1.0.0` into every dkms.conf, which broke version parsing.
  modinfo remains a fallback.
- Early-exit only when the node is already AWG3-capable (loaded + kallsyms +
  tools ≥ v3). A loaded-but-v1 node takes the upgrade path: DKMS install with
  `PACKAGE_VERSION` rewritten to `3.0-awg3`, then `rmmod`/`modprobe`.
- Tools < v3 (or missing): `git clone amneziawg-tools master` + `make install`.
  Prereqs gain `git libmnl-dev pkg-config`.

**Tests:** `awg_version_perversion_test.go` (ordering, H-shape, client field
set, server S3/S4 parity), tools-version parse tests. `go build` / `go vet` /
`go test ./internal/...` green. Live E2E install on n1 still pending (node
reimaged; GCloud test VPS stopped) — unit path is shipped.

**Files:** `internal/domain/model/awg_version.go`, `internal/chain/awgpresets_gen.go`,
`awg_cps.go`, `clientconfig.go`, `awg_server.go`, `awg_deploy.go`,
`awg3_capability.go`, `internal/backend/singbox/singbox.go`, `internal/web/users.go`,
`awg_version_perversion_test.go`, `awg_install_test.go`, `docs/PROGRESS.md` §45,
`internal/version/version.go` (v0.8.23).

### Feature — UI redesign: Lovable design system (Sand palette) fully shipped

Full presentation-layer migration off Tokyo Night onto the Lovable design
system. Handler/business logic untouched.

**Slice 1 — design tokens + shell:** new `web/static/css/themes.css` replaces
`tokyo-night.css` with four themes (`sand` default / `slate` / `graphite` /
`night`). DaisyUI semantic slots in OKLCH (required for `oklch(var(--p))` in
spider SVG + loading bar); `--tn-*` become aliases so existing `.tn-*`
components recolor without body edits. New component classes from the mockups
(`.st`/`.pill`/`.lvl`/`.seg`/`.inp`/`.btn-*`/`.nav-a`/…). `base.templ` shell +
theme dropdown; FOUC script migrates legacy theme names.

**Slice 2 — page markup:** all 11 templ pages reworked onto Lovable patterns
(inbounds AWG version segmented control, dashboard, nodes, chains, users,
presets, settings, spider panels, chainlevels/index/hosts). Zero remaining
`input-bordered`/`form-control`/`badge badge-*` form classes in templates.

**Files:** `web/static/css/themes.css`, `app.css`, `web/templates/*.templ`,
`web/static/js/app.js`, `docs/PROGRESS.md` §44. `go build` + `go test ./internal/web/` green.

## [v0.8.22] — 2026-07-31

### Feature — kernel-AWG3 render path (AWG 3.0 via kernel awg-quick + TUN-overlay, live-verified on n1)

The amnezia-box **kernel module gained native AWG 3.0 (header protection) support on 2026-07-30** — PR [#192](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module/pull/192) merged to `master` of `amneziawg-linux-kernel-module` (kernel `wg_device` struct now carries `header_protection` / `content_padding_addition` / `rekey_after_time/timeout` as `u16_range_t`; Sx≥12 validation landed 2026-07-31 in `7304fbf`/`ff0aa32`). This breaks the v0.8.10 absolute constraint "kernel rejects HPK → AWG3 is userspace-only": a v3 inbound can now render via the **kernel awg-quick + sing-box-TUN-overlay path** (the stable architecture AWG 1.5/2.0 use), with the userspace sing-box `type:"awg"` endpoint kept as a fallback for older nodes.

**Capability detection** (`internal/chain/awg3_capability.go`): `detectKernelAWG3` probes the node over SSH at deploy time for BOTH the kernel module version (modinfo ≥ 3.0) AND the amnezia-box-tools version (≥ v3.0.20260730 — the `HeaderProtectionKey` keyword support). Both are required: the kernel accepts the netlink attr, but awg-quick needs the userspace tool to parse the `.conf` keyword. The result is stamped on a runtime-only `NodeInfo.KernelAWG3Supported` field (`json:"-"`) — never persisted. ApplyChain probes in the pre-flight loop; ApplyMergedNode probes on the (single) deploy connection (preserving the 1-connection-per-deploy invariant).

**Kernel-AWG3 render path** (when `KernelAWG3Supported`): the v3 inbound renders via kernel awg-quick — `RenderServerAWGConf` emits `HeaderProtectionKey` / `ContentPaddingAddition` / `RekeyAfterTime` into `[Interface]` (`writeAWG3ConfLines`, hex-persisted HPK → base64 via the shared `awg3HPKHexToBase64`), the TUN overlay is enabled (`awgTUNOverlayNeeded`), and the leftover-unit teardown is skipped. When the flag is false, the v3 inbound falls back to the userspace sing-box endpoint (the v0.8.10 path) — stable, just without the kernel-overlay. The render branches gate on `kernelAWG3EnabledFor(nodeInfo)`.

**Kernel-AWG3 awg0.conf contract (live-verified on n1, `TestE2EAWG3_KernelConf` PASS):** HPK/CPM/RAT in `[Interface]` are accepted by `awg-quick up` end-to-end; `awg show` confirms `header protection key` / `content padding addition` / `rekey after time` applied. **Live-found kernel validations (preset-affecting):** (a) S1-S4 ALL must be ≥ 12 when HPK is set (`-EINVAL` otherwise — HeaderCipherNonceSize); (b) **H1-H4 must be UNIQUE among each other** (`-EINVAL` on duplicates — so the awg3 presets use `12/13/14/15`, not `12/12/12/12`); (c) the `.conf` HPK value is base64 (WireGuard-key form), not hex.

**deps:** `deps/amneziawg-src.tar.gz` repacked from `amneziawg-linux-kernel-module@master` (c78a89e, post-PR#192 + Sx≥12 fixes + netlink <6.7 compat). On n1 staged as DKMS `amneziawg/3.0.20260730` (module version `3.0.20260731-04`) + amnezia-box-tools v3.0.20260730.

**Files:** `internal/chain/awg3_capability.go` (new), `internal/chain/awg3_kernel_render_test.go` (new), `internal/chain/awg3_kernel_e2e_test.go` (new, tag `e2eawg3`), `internal/chain/awg_server.go`, `internal/chain/awg_cps.go`, `internal/chain/applier_build.go`, `internal/chain/awg_deploy.go`, `internal/chain/awg_tun_overlay.go`, `internal/chain/merged_config.go`, `internal/chain/default_presets.json` (H1-H4 unique), `internal/domain/model/panel.go` (`NodeInfo.KernelAWG3Supported`), `internal/version/version.go` (v0.8.22), `AGENTS.md` (#5/#10 revision), `docs/PROGRESS.md` (§43 slice 2 shipped), `deps/amneziawg-src.tar.gz.new`. `go build ./...` + `go vet ./...` + `go test ./internal/chain/... ./internal/domain/... ./internal/web/` green; `TestE2EAWG3_KernelConf` PASS on n1.

## [v0.8.21] — 2026-07-31

### Feature — AWG protocol version selector (1.5 / 2.0 / 3.0) + AWG 3.0 obfuscation presets + kernel-AWG3 horizon

**Context.** The AmneziaWG **kernel module gained native AWG 3.0 (header protection) support on 2026-07-30** — PR [#192 «feat: AmneziaWG 3.0»](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module/pull/192) merged to `master` of `amneziawg-linux-kernel-module` (kernel `wg_device` struct now carries `header_protection` / `content_padding_addition` / `rekey_after_time/timeout` as `u16_range_t`; Sx≥12 validation landed 2026-07-31 in `7304fbf`/`ff0aa32`). This breaks the old absolute constraint "kernel rejects HPK → AWG3 is userspace-only" (AGENTS #5/#10): a kernel-render path for AWG3 is now possible. That kernel-render + deps bump is **slice 2** (needs Linux/WSL build + live E2E on n1); this release is **slice 1** — the code, presets, and version taxonomy.

**Version taxonomy (now first-class in the model).** Before this, AWG3 was a boolean toggle (`AWG3Mode`, v0.8.10) with no notion of protocol versions. Added a canonical `AWGVersion` selector (`"1.5"` / `"2"` / `"3"`, `internal/domain/model/awg_version.go`): **1.5** = legacy AmneziaWG 1.x (Jc/S1-S2/fixed H1-H4, no CPS); **2.0** = the current kernel+CPS default (+S3-S4/I1-I5/H1-H4 ranges/Itime); **3.0** = +HeaderProtectionKey/ContentPaddingAddition/RekeyAfterTime. `EffectiveAWGVersion()` reconciles the new field with the legacy `AWG3Mode` bool (true → "3"), so **no store migration is needed** — old stores keep their userspace-AWG3 behavior. The UI dropdown on the inbound form (1.5 / 2.0 / 3.0) replaces the old `awg3_mode` checkbox (kept as a backward-compat fallback).

**AWG 3.0 obfuscation presets.** New `AWGPreset.Version` field + four `*_awg3` presets (`maximum_stealth_2026_awg3`, `russia/iran/china_2026_awg3`): HPK-on, S1-S4=24 (≥12 nonce-size, mandatory when HPK is set), **H1-H4 minimized to 12/12/12/12 as redundant under HPK** (header protection fast-encrypts the low-entropy header fields the H type-markers otherwise fingerprint — different mechanism, not fully removed, but the H-marker fingerprint criticality drops). CPS (I1-I5) and Jc stay (orthogonal to HPK). The dropdown groups them in a new `AWG · 3.0 (header protection)` optgroup FIRST (max stealth), ahead of Robust/Stealth/Reality/XHTTP.

**Preset↔version contract.** `PresetSupportsAWGVersion` + `resolveAWGPresetForVersion` (wired into `ResolveStandaloneAWGPreset`/`ResolveChainEntryPreset`): a v3 inbound can never silently render a v2-only preset whose S1-S4 may be < 12 → it falls back to the per-version default. All render-path checks switched from `ib.AWG3Mode` to `ib.EffectiveAWGVersion()=="3"` so both `AWGVersion="3"` (new path) and legacy `AWG3Mode=true` render the same userspace endpoint (slice 1 keeps AWG3 userspace; kernel-render is slice 2).

**Files:** `internal/domain/model/awg_version.go` (new), `internal/domain/model/inbound.go`, `internal/domain/model/panel.go`, `internal/chain/presets.go`, `internal/chain/default_presets.json`, `internal/chain/awg_inbound_material.go`, `internal/chain/awg_deploy.go`, `internal/chain/awg_tun_overlay.go`, `internal/chain/merged_config.go`, `internal/chain/inbound_source.go`, `internal/chain/awg_version_test.go` (new), `internal/chain/robust_presets_test.go`, `internal/web/inbounds.go`, `web/templates/inbounds.templ`, `web/templates/inbounds_templ.go`, `internal/i18n/i18n.go`, `internal/version/version.go` (v0.8.21), `AGENTS.md` (#5 revision), `docs/PROGRESS.md` (§43). `templ generate` + `go build ./...` + `go vet` + `go test ./internal/chain/... ./internal/domain/... ./internal/web/` green.

## [v0.8.20] — 2026-07-31

### Fix — stuck chain-entry AWG listen port (`listening port: 1`) + revision of AGENTS #17 (Jc=120 demoted to un-isolated hypothesis)

**Stuck-port bug.** A tester's AWG node stopped accepting clients after an AWG3 on→off toggle. `awg show awg0` reported `listening port: 1` (a bogus value — the real port is 8443/25086/51840). Re-applying the chain did not fix it ("история висит даже после нажатия применить"). Root cause: `ensureMaterializedEntryInbound` (`chain_entry_material.go`) updated keys/subnet/CPS material of an existing chain-entry inbound on re-apply but **never re-synced `ib.Port`** — unlike the standalone path (`profile_deploy.go:135`, which sets `ib.Port = prof.Port`). So any drifted port (1, a 0-leak, or a desync after `UserEntryPort` changed) stuck forever. The client dialed a port nothing listened on.

Fix: the update-branch now re-syncs `ib.Port = chainEntryPort(c, entry.ID)` (no-op for a healthy node — port already equals chainEntryPort; repairs a stuck one). Source is `chainEntryPort`, **not** `prof.Port` — chain-entry port is base + entry-node index, the profile is not the source of truth for the port here. Legacy non-levelized chains never reach this branch (`IsLevelized()` guard). Covered by `TestEnsureChainEntryMaterialization_PortResync`.

**Revision of AGENTS #17 (Jc=120).** The "preset-switch fixed it" observation was post hoc, not evidence: switching a preset triggers a full config regeneration + redeploy, which re-syncs the port/keys/material **regardless of Jc**. The three former "proofs" of #17 are all artifacts: (1) the n1→n2 Jc=120 handshake failure only reproduced in same-host-client topology (hairpin via the external IP; cross-machine §22.4 passes with Jc=120); (2) the "awg2 Jc=6 alive vs awg0 Jc=120 dead" A/B was a **stale phantom peer**, not a live client; (3) preset-switch = regeneration. Jc=120 is physically plausible but never isolated from split-brain (v0.8.18) / stuck-port (this release) / stale materialization on our stack. #17 is demoted from "confirmed" to "un-isolated hypothesis"; the rule is now: verify store-vs-`awg show` first (server port + pubkey vs client `Endpoint` + peer-key), and only if those match try `awg set <iface> jc 3`. Robust presets (v0.8.7) stay as a feature (operator's choice), not as a proven fix.

**Files:** `internal/chain/chain_entry_material.go` (port resync), `internal/chain/chain_entry_material_test.go` (+TestEnsureChainEntryMaterialization_PortResync), `AGENTS.md` #17 (rewritten), `docs/PROGRESS.md` (§22.3 / §35 / §39.1 revision notes + §41), `internal/version/version.go` (v0.8.20). `go build ./...` + `go test ./internal/chain` green.

## [v0.8.19] — 2026-07-30

### Fix — AmneziaWG PPA install: modern GPG keyring + distro codename (was `NO_PUBKEY` / unsigned repo on Ubuntu 24.04+)

On a fresh Ubuntu node, `angry-box deploy` installs the AmneziaWG kernel module via the `ppa:amnezia` PPA. The install script broke on Ubuntu 22.04+ (and 24.04/26.04) with `NO_PUBKEY 4166F2C257290828` / "repository is not signed":

- **Deprecated `apt-key` + short key id.** `apt-key` is deprecated since Ubuntu 22.04 and on 24.04+ it silently fails to import the key. The script also used the 8-hex tail `57290828` instead of the full fingerprint `4166F2C257290828`. Both → the PPA stayed unsigned → `apt-get update` failed.
- **Hardcoded `focal` codename.** The PPA line was always `.../ubuntu focal main` regardless of the actual distro, so on Ubuntu 24.04/26.04 the codename mismatched.
- **`set -e` + failed `apt-get update` aborted the whole install** before reaching the bundled-DKMS-from-source fallback that was already in the script — so the node never got the module either way.

Fix (`InstallAWGModuleWithClient` shell script):
- **Modern keyring:** import the full fingerprint `4166F2C257290828` via `gpg --keyserver` (port 80 first, hkps:443 fallback) → `/usr/share/keyrings/amnezia.gpg`, and reference it with `deb [signed-by=...]` so apt trusts only this PPA (no more global `apt-key`).
- **Distro codename from `/etc/os-release` `VERSION_CODENAME`** (focal/jammy/noble/...), falling back to `focal` only if unset.
- **`apt-get update || echo WARNING`** instead of letting it abort under `set -e` — the bundled-DKMS-from-source fallback now actually runs when the PPA is unreachable/unsigned.

This matches the upstream-known issue (amnezia-vpn/amneziawg-linux-kernel-module#133). Verified `go build ./...` + `go test ./internal/backend/singbox`.

## [v0.8.18] — 2026-07-30

### Fix — single-instance enforcement + canonical absolute store path (closes the two-daemon split-brain)

A tester's fleet had two `angry-box serve` processes running against **different** store files (systemd with cwd `/var/lib/angry-box`, plus a hand-launched one from `/root`). Because the store default was the **relative** `store.json` (CWD-dependent), each daemon used a different store → user keys drifted between them → the deployed node's AWG peer no longer matched the client config → "node won't connect". Two independent root causes are now closed:

- **Canonical absolute store default.** `config.DefaultStorePath()` (root-aware): running as root → `/var/lib/angry-box/store.json`; non-root → `$XDG_DATA_HOME/angry-box/` or `$HOME/.local/share/angry-box/`; Windows → `%APPDATA%/angry-box/`. Two `angry-box serve` from different directories now converge on the SAME file regardless of cwd. The directory is auto-created. The duplicate hardcoded `"store.json"` literal in `main.go` is gone (single source of truth via `config.DefaultStorePath()`). Operators can still override with `--file` / config `store_file`.
- **Single-instance lock.** `angry-box serve` takes an exclusive non-blocking lock (`flock` on Unix, `LockFileEx` on Windows) on a sibling `<store>.lock` file. A second instance against the same store is **refused** with an actionable error naming the holding PID: "angry-box already running (PID xxxxx), store locked: <path>. Stop the other instance, or run with a different --file." The lock auto-releases on exit/crash (no stale lock blocking restarts). Two instances with explicitly different `--file` paths still coexist.
- **Upgrade WARN (no auto-migrate).** If the canonical default store is empty/absent but a legacy CWD-relative `store.json` exists, serve logs a one-time WARNING telling the operator to copy the store + its `.key` to the canonical location (or run with `--file`). No auto-migration — the store is at-rest encrypted and moving it without its key is unsafe.
- `scripts/S99angry-box` `start()` now checks the PIDFILE and refuses to spawn a second daemon (the binary's flock is the real guard; this gives an immediate clear message at the init-script level).

**Migration for the tester (and anyone with the same split-brain):** stop both processes, copy the GOOD store to the canonical path, then run only one — `sudo systemctl restart angry-box` (not a hand-launched `serve`).

- New: `internal/config/config.go` `DefaultStorePath()`; `cmd/angry-box/instancelock.go` + `instancelock_unix.go` + `instancelock_windows.go` + `storepath.go`.
- Tests: `TestDefaultStorePath_Absolute`, `TestDefaultConfig` (StoreFile absolute), `TestAcquireInstanceLock_SecondIsRefused` / `_ReleaseAllowsReacquire` / `_DifferentStoresIndependent`.
- `go build ./...` green on Windows + `GOOS=linux`; `go test ./internal/config ./cmd/angry-box` green.

## [v0.8.17] — 2026-07-30

### Fix — preset dropdown grouped (Robust/Stealth) + Jc inline, so budget-VPS users don't pick the handshake-killing Jc=120 default blind

A tester on a budget VPS could not connect: the generated client config used the
global default preset `maximum_stealth_2026` (Jc=120), and Jc=120 kills the AWG
handshake on budget hostings (AGENTS #17 — proven live on the same node:
`awg0` Jc=120 → handshake=0, `awg2` Jc=6 → handshake ok + traffic). The chain
and inbound preset dropdowns listed preset names flat, with no indication of
which are handshake-safe (Robust, Jc≤10) vs max-DPI (Stealth, Jc=120), so the
operator picked blind.

- The preset dropdown on the **chain form** and the **inbound form** is now
  grouped with `<optgroup>`: "AWG · Robust (бюджетные VPS)" renders FIRST
  (recommended default for budget VPS), then "AWG · Stealth (Jc=120, premium)",
  "Reality", "XHTTP", "Все протоколы (Stealth)". Each AWG option shows its Jc
  inline (e.g. `russia_2026_awg_robust (Jc=5)`).
- A short hint under the dropdown explains the tradeoff (Robust = reliable
  handshake on budget VPS; Stealth = max anti-DPI on premium; AWG 3.0 mode adds
  header protection on top of any preset).
- The "global default" option is relabeled to make its Jc explicit
  ("Use global default (Jc=120, premium)").
- New: `chain.PresetOption` / `chain.PresetGroup` / `ListPresetsDetailed()` /
  `GroupPresets()` (internal/chain/presets.go) — UI-facing preset descriptors
  with protocol + Jc + robust flag + stable group order (Robust first).
- The global default preset is unchanged (`maximum_stealth_2026`, Jc=120) —
  switching it is a product decision (AGENTS #17); the fix surfaces the choice
  to the operator instead.
- Test: `TestGroupPresets_RobustBucketFirst` pins the grouping contract (robust
  bucket first, all robust presets present with Jc≤10, no Jc=120 leak).

Note: AWG 3.0 mode (v0.8.10) adds HPK/CPM/RAT on top of any preset; on a budget
VPS a robust preset + AWG3 is the safest combination.

## [v0.8.16] — 2026-07-30

### Fix — Automatic purge and filtering of orphan deleted NodeInfo records

- **Orphan NodeInfo purge on startup & query filtering.** `ListNodeInfos` and `computeDeployStatusRows` now strictly filter out orphan `NodeInfo` records whose `Host` has been deleted from the store. Added unconditional orphan purge on `openStore` startup so legacy stores upgraded at schema v3 automatically clean up deleted nodes from memory and disk.

## [v0.8.15] — 2026-07-30

### Fix — Automatic purge and filtering of orphan deleted NodeInfo records

- **Orphan NodeInfo purge on startup & query filtering.** `ListNodeInfos` and `computeDeployStatusRows` now strictly filter out orphan `NodeInfo` records whose `Host` has been deleted from the store. Added unconditional orphan purge on `openStore` startup so legacy stores upgraded at schema v3 automatically clean up deleted nodes from memory and disk.

## [v0.8.14] — 2026-07-30

### Fix — Apply button on Deploy Status page for pending nodes

- **Deploy Status Actions column.** Added an `Apply` ("Применить") button directly onto each row of the Deploy Status page (`/ui/deploy-status`). Nodes showing `pending` ("ожидает") can now be deployed in one click, updating `LastDeployedAt` and changing the status to green `applied` ("применен").

## [v0.8.13] — 2026-07-30

### Fix — AWG diagnostics auto-detection for active interfaces (e.g. awg2)

- **AWG diagnostics auto-detection.** `DiagnoseAWGNode` now auto-detects active AWG interfaces (`awg show interfaces` / `/etc/amnezia/amneziawg/*.conf`) when no explicit interface is passed, preventing false-positive failures on nodes running non-default interface names (e.g. `awg2`). Updated `sing-box-tun` check to a warning for AWG 3.0 / non-overlay nodes.

## [v0.8.12] — 2026-07-30

### Fix — AWG port 8443 clash on deploy + node status check UI polish

- **Kernel interface teardown & port release.** Added `ip link delete <iface>` to `teardownAWGInterfaces` and fallback `awg0` teardown in `AWGTeardownInterfaces` for nodes where kernel AWG is no longer active (e.g. converted to AWG 3.0 mode). Prevents stale kernel interfaces from holding UDP port 8443 and causing sing-box startup failures (`bind: address already in use`).
- **Node status check HTMX swap.** `handleHostStatus` now returns `NodeStatusCell` instead of replacing the table cell with a bare status badge, preserving the "Check" and "Mark Blocked" controls after manual status probes.
- **Node apply button visual layout.** Simplified the Apply button label on node rows to prevent text wrapping and visual breakage in DaisyUI table cells in Russian UI.

## [v0.8.11] — 2026-07-30

### Fix — AWG 3.0 chain entry never came up (four independent bugs)

A tester's AWG 3.0 chain entry had no handshake and no traffic. v0.8.10 verified
AWG3 live on n1 in the *standalone* shape, but the **chain-entry** path was
broken by four independent bugs — each fatal on its own. Diagnosed from node
output (`journalctl`, `awg show`, `ss -lunp`), not from reading code.

- **Port mismatch (fatal).** The userspace endpoint listened on the *chain's*
  user-entry port (`chainEntryPort`, 8443) while the client `.conf` and the
  kernel renderer both use the *materialized inbound's* port (25086) — nothing
  was listening where every client dialed. The AWG3 endpoint was the only
  renderer ignoring `ib.Port`. Now the inbound's port wins, with the chain port
  as fallback for un-migrated chains. The inbound tag is unchanged (route rules
  address it by tag).
- **Stale kernel unit kept the port (fatal).** `RenderNodeAWGConfs` correctly
  stopped *emitting* `awg0.conf` for AWG3 inbounds, but nothing ever stopped the
  `awg-quick@awg0` unit left running by the previous non-AWG3 deploy. It held
  the UDP port, so sing-box crash-looped: `endpoint/awg[ch-X-user-in]: unable to
  update bind: listen udp4 0.0.0.0:8443: bind: address already in use` →
  `FATAL start service`. New `AWGTeardownInterfaces` computes the units the node
  must no longer run, and the deploy disables them *before* pushing the sing-box
  config, inside the same host lock. An interface that is still rendered is
  never torn down (the node ran a legitimate second AWG interface with 3.16 GiB
  of live traffic); already-inactive units are a no-op; units that were active
  are restored if the sing-box push fails.
- **Server/client obfuscation divergence.** The server resolved its preset from
  the CHAIN while the client resolved it from the PROFILE — live divergence
  S1 15 vs 115, S2 85 vs 45, different H1. amnezia parameters must match exactly
  or the handshake cannot complete. A single `ResolveChainEntryPreset` is now
  used by all three render paths (AWG3 endpoint, kernel chain-entry conf, client
  `.conf`). The inbound's preset wins only when it actually names one — an empty
  `Obfuscation` keeps the chain's preset, so custom-preset chains don't silently
  degrade to the panel default and break already-connected clients.
- **Hardcoded server address.** The endpoint hardcoded `10.8.0.1/32`, ignoring
  the inbound's real subnet (`10.8.1.1/24`), so the server and its peers landed
  on different `/24`s. The address now derives from `AWGServerAddress`.

- Tests: 5 new render/teardown tests (`TestAWG3Mode_EndpointUsesInboundPort`,
  `_TeardownsKernelUnit`, `_ServerAddressFromInbound`,
  `TestChainEntryPreset_ServerClientMatch`,
  `_EmptyObfuscationKeepsChainPreset`) plus 3 deploy-push tests covering
  teardown ordering, rollback restore, and idempotency. Each was verified to
  fail with the *live* symptoms when its fix is reverted (port `8443 vs 25086`,
  `S1 115 vs 15`, `Jc 120 vs 5`, empty teardown set).
- The kernel path for non-AWG3 inbounds is untouched (AGENTS #10/#11), and no
  NAT/nftables rules were changed — a userspace AWG3 endpoint egresses through
  sing-box's own socket, so the user subnet needs no MASQUERADE.

## [v0.8.10] — 2026-07-29

### Feature — AWG 3.0 header-protection mode (opt-in per-inbound toggle, live-verified)

AWG 3.0 obfuscation — `HeaderProtectionKey` (32-byte ChaCha20, encrypts
handshake/cookie + 16-byte transport headers) + `ContentPaddingAddition`
(random transport-packet padding, lo-hi range) + `RekeyAfterTime` (sec range
instead of fixed WG constants). Reference generator: architect.vai-rice.space
(AWG 3.0/2.0/1.5/1.0 buttons). S1-S4 must be >= 12 when HPK is set
(HeaderCipherNonceSize=12) — raised automatically at render.

This is an **opt-in per-inbound toggle**. The default stays the kernel
`awg-quick@awg0` + sing-box TUN-overlay architecture (AGENTS #10/#11 — existing
deployments are untouched). When AWG 3.0 mode is ON, the user-entry renders as
a userspace sing-box `type:"awg"` endpoint with multi-peer (one peer per User) —
because AWG3 fields are userspace-only (the kernel amneziawg module rejects
`HeaderProtectionKey` in `setconf`).

**Live-verified on n1 (hard gate):** real client handshake through the in-process
endpoint (`awg show awg3c latest-handshakes` = now, bidirectional transfer) +
egress (`curl --interface awg3c ifconfig.me → 144.31.224.212`, server log
`endpoint/awg[awg3-in]: inbound from 10.8.0.2 → outbound/direct → ifconfig.me`).
Two UAPI client-config format bugs found and fixed: a blank line is the IPC
terminator (not a device↔peer separator), and amnezia/AWG3 fields are
device-level (parsed before the first `public_key=` line switches to peer-mode).

**Constraints (intentional):**
- User-facing entry ONLY — NOT inter-node chain transit (UI/hint enforces;
  multi-hop AWG3 is not supported).
- The client MUST be AWG3-capable: AmneziaWG Android/iOS/Windows app, or
  userspace `amneziawg-go`. Linux `awg-quick` does NOT parse HPK.
- AWG3-mode is orthogonal to the robust presets (v0.8.7): the preset chooses
  Jc/jmin/jmax/S/H/I/CPS, the toggle adds HPK/CPM/RAT on top.

- New fields on `InboundProfile` and `NodeInbound`: `AWG3Mode`, plus the
  persisted material `AWG3HeaderProtectionKey` (hex), `AWG3ContentPaddingAddition`,
  `AWG3RekeyAfterTime`. Material is generated once at deploy and persisted
  (`EnsureProfileAWGMaterial` / `EnsureInboundAWGMaterial`); toggling off keeps
  it dormant (preserved for a later re-enable, so clients don't break on
  off→on→off cycling).
- Render: `merged_config.go` emits the userspace endpoint for AWG3Mode inbounds;
  `awg_deploy.go` skips awg0/awg1.conf; `awg_tun_overlay.go` skips the overlay
  and `include_interface`. `clientconfig.go` writes HPK/CPM/RAT inline in
  `[Interface]` before `[Peer]` (matching the amneziawg-go UAPI ordering).
- UI: a checkbox "AWG 3.0 mode (header protection)" in the inbound form's AWG
  section, with an info note (requires an AWG3-capable client, S1-S4 >= 12
  automatic, user-facing entry only — not chain transit). i18n keys added
  (en/ru).
- Tests: 6 new unit tests (`TestAWG3Mode_RendersHPK`, `_S1S4RaisedTo12`,
  `_MultiPeer`, `_KernelPathSkipped`, `_NotRaisedWhenOff`, `TestAWG3ClientConf_HasHPK`)
  pin the render contract (HPK base64 round-trip, S raise, active-only peers,
  no kernel conf / no overlay, ordering in `[Interface]`). A generator test
  (`awg3gen` build tag) emits server+client configs for the live gate.

## [v0.8.9] — 2026-07-29

### Fix — store migration v2→v3 cleans up legacy orphan NodeInfos (deleted nodes still shown in Deploy Status)

v0.8.8 made `DeleteHost` cascade so new node deletions also drop the `NodeInfo`
and `Metrics` records. But stores upgraded from a pre-v0.8.8 build still carry
the legacy orphans — NodeInfo/Metrics rows whose Host was deleted before the
cascade existed. The Deploy Status page reads `ListNodeInfos()` (every NodeInfo,
no Host filter), so those orphans kept rendering as "deleted nodes still
hanging in deploy status" with no clickable row actions (reported by a user:
deleted two nodes, they vanished from the Nodes page but remained in Deploy
Status with nothing clickable).

- A new store migration (`migrateOrphanNodeInfos`, schema version 2→3) runs once
  at startup and drops NodeInfo + Metrics records whose Host no longer exists.
  Idempotent (no orphans = no-op, no backup, no write). A best-effort one-shot
  backup `store.json.preorphan.bak` is written before the first run. The count
  of dropped records is logged. Existing test fixtures were updated to add
  `SaveHost` for nodes they materialize (v3 law: a NodeInfo without a Host is an
  orphan; the migration enforces it).
- New tests `TestMigrateV3_OrphanNodeInfoCleanup` and
  `TestMigrateV3_NoOrphansIsNoOp`. Files: `internal/chain/store.go`,
  `internal/chain/migrate_v3_test.go`, `internal/chain/migrate_v2_test.go`,
  `internal/chain/misc_more_test.go`, `docs/PROGRESS.md` §37. `go build ./...` +
  `go test ./internal/chain ./internal/web -p 1` green.

## [v0.8.8] — 2026-07-29

### Fix — DeleteHost now cascades to NodeInfo + Metrics (orphan "deleted" nodes no longer listed)

`Store.DeleteHost` removed the host record but left its `NodeInfo` (including
materialized InboundProfile inbounds) and `Metrics` records behind. `ListNodeInfos`
returns every NodeInfo without filtering by an existing Host, so those orphan
records kept showing up as if the node still existed — in the Inbound create/edit
form's node dropdown and on the Inbounds page. An operator deleting a node from
the Nodes page saw it disappear there but reappear elsewhere ("nodes that were
already deleted are still hanging", "the old inbound won't delete"). The separate
`DeleteNodeInfo` helper (used by the P2b spare path) does drop NodeInfo + Metrics,
but the web delete handler called only `DeleteHost`, not `DeleteNodeInfo`.

- `DeleteHost` now drops the matching `NodeInfo` and `Metrics` inline, in the same
  locked section (it cannot call `DeleteNodeInfo` — that takes its own lock and
  would deadlock per AGENTS #2). `KnownHost` entries are left (the TOFU fingerprint
  is address-keyed, not id-keyed, so a rotated host may reuse an address). The
  `autorelocate` path (which calls `DeleteNodeInfo` then `DeleteHost`) is
  unaffected — the second removal is idempotent.
- New test `TestDeleteHost_CascadesNodeInfoAndMetrics`. Files: `internal/chain/store.go`,
  `internal/chain/store_test.go`, `docs/PROGRESS.md` §36.

### Feature — angry-box version shown in the UI

The build version previously lived only in the `main` package (`var version`,
ldflags-injected) and was shown just by `angry-box version` and the startup log —
the web UI could not import it (main → web import cycle). A new
`internal/version` package holds `Version` (ldflags-overridable, default
`v0.8.7`); `main`, the Makefile ldflags (`-X .../internal/version.Version`), and
the web templates all read the same value. The sidebar footer now displays the
version under "Angry-BOX • Orchestrator", and `GET /health` returns
`{"status":"ok","version":"vX.Y.Z"}`. Files: `internal/version/version.go`,
`cmd/angry-box/main.go`, `Makefile`, `web/templates/base.templ`,
`docs/PROGRESS.md` §36.

## [v0.8.7] — 2026-07-29

### Feature — robust AWG presets (low Jc) for budget VPS where the default won't handshake

All built-in AWG obfuscation presets (`russia/iran/china/maximum_stealth/pro_2026_awg`)
ship with `Jc=120` — a DPI-maximizing profile that floods 120 junk UDP packets
before every WireGuard handshake. On premium channels (GCloud etc.) this passes;
on **budget VPS** the hosting rate-limits/drops the UDP flood, and the handshake
init packet (1 of ~120) or the server's response (riding back inside the 120-
packet flood) gets dropped → the handshake never completes. This is documented
as AGENTS Known Issue #17 and was reported by a user: a client would not connect
to an AWG node on a budget VPS. `awg show` on the node was an A/B proof — two
AWG interfaces on the same node, same channel, same client: `awg2` (Jc=6) had a
fresh handshake + 46 MiB transferred, `awg0` (Jc=120) had no handshake at all.

Per AGENTS #17 the DPI presets are a product decision and are NOT changed. This
release adds **five paired robust presets** that keep a reliable handshake on
lossy/budget hostings at the cost of slightly weaker DPI masking:

- `russia_2026_awg_robust`, `iran_2026_awg_robust`, `china_2026_awg_robust`,
  `maximum_stealth_2026_awg_robust`, `pro_2026_awg_robust` — each mirrors its
  DPI counterpart but with `Jc=5, Jmin=3, Jmax=15` (minimal junk flood). The
  S1-S4 / H1-H4 / CPS / mimicry / i1_packet fields are copied from the original
  (they affect the DPI fingerprint, not handshake robustness). The user picks a
  preset matching their channel in the inbound UI dropdown.
- Files: `internal/chain/default_presets.json` (21 → 26 presets),
  `internal/chain/robust_presets_test.go` (verifies the robust presets load,
  are `protocol:"awg"`, have `Jc ≤ 10`, and appear in the AWG dropdown),
  `AGENTS.md` #17, `docs/PROGRESS.md` §35. `go build ./...` +
  `go test ./internal/chain` green.
- Workaround for an existing node (until a redeploy with a robust preset):
  `awg set <iface> jc 3` on BOTH the server and the client — the handshake
  completes immediately. A redeploy re-applies the preset's Jc, so switch the
  inbound/chain to a `*_awg_robust` preset to keep the fix.

## [v0.8.6] — 2026-07-29

### UI fix — «+ Add Inbound» button on the Inbounds tab did not open the modal

The "+ Add Inbound" button on the Inbounds page targets `#modal-container`
(`hx-get="/ui/inbounds/new" hx-target="#modal-container"`), but `InboundsPage`
did **not** define a `#modal-container` element — it exists in
chains/dashboard/nodes/presets, but not inbounds. HTMX could not find the
target, so the form never opened. Drilling into the "Presets" sub-tab made the
button start working, because the Presets page (swapped into
`#inbounds-tab-content`) brought its own `#modal-container` with it (reported
by a user: "on the Inbounds tab the add button doesn't work, but if you drill
into the Presets sub-tab the button starts working").

- Added `<div id="modal-container"></div>` to `InboundsPage`, placed **outside**
  `#inbounds-tab-content` so it survives the swap to the Presets sub-tab. Removed
  the now-duplicate `#modal-container` from `PresetsPage` (presets are only
  rendered via the inbounds tab swap, never standalone, so the wrapper's
  container is enough). One container, both Inbounds and Presets buttons swap
  into it.

### Feature — download a client config file from the Clients page

The client config view (`UserConfigView`) showed each chain/inbound config in a
read-only `<textarea>` with only a Copy button. Added a **Download** button next
to Copy that saves the config as a file via a client-side Blob (no backend
round-trip). The new `downloadUserConfig()` helper in `web/static/js/app.js`
reads the textarea, builds a Blob, and downloads it as
`<userName>-<chain>.conf`. The extension is chosen by content: a multi-line
AWG awg-quick config (containing `[Interface]`) keeps `.conf`; a single-line
share URI (`vless://`, `tg://`, `https://...`) becomes `.txt` so the OS opens
it with the right app. The filename is sanitized (path separators → `-`,
non-word chars → `_`). i18n key "Download" added to en/ru.

- Files: `web/templates/inbounds.templ`, `web/templates/presets.templ`,
  `web/templates/users.templ`, `web/static/js/app.js`, `internal/i18n/i18n.go`,
  `docs/PROGRESS.md` §34. `templ generate` + `go build ./...` +
  `go test ./internal/web -p 1` green.

## [v0.8.5] — 2026-07-29

### Installer fix — re-install now restarts the daemon (was `start`, a no-op on an active unit)

`scripts/install.sh`'s `start_service` always ran `systemctl start angry-box`.
On a re-install (the binary on disk is replaced while the service is already
running) `start` is a **no-op** — systemd does not restart an active unit, so
the freshly installed binary was never picked up and the **old** daemon kept
running in memory. The upgrade silently did not take effect: a user who ran
`install.sh` to go from v0.8.3 → v0.8.4 still had the v0.8.3 daemon until a
manual `systemctl restart angry-box` (reported by a user: after upgrading, the
"Diagnose" button on a correctly-saved node still failed with
`ssh: read key "..."` — the v0.8.3 daemon was serving the request).

- `start_service` now checks whether the unit is already active and runs
  `restart` (to load the new binary) on a re-install, `start` only on a fresh
  install. All three launch paths are covered: systemd system-mode
  (`systemctl is-active --quiet` → `restart`/`start`), systemd user-mode
  (`systemctl --user ...`), and Keenetic/NDMS `S99angry-box` init (PID-file +
  `kill -0` liveness → `restart`/`start`; the S99 script already supported
  `restart`). The "Service failed to start" warning + `status`/`journalctl`
  output is preserved for both systemd modes.
- Files: `scripts/install.sh` (`start_service`), `docs/PROGRESS.md` §33.
  `bash -n scripts/install.sh` syntax OK.

## [v0.8.4] — 2026-07-29

### UI fix — «Add Node» wizard opened without styles / form did not submit

The "Add Node" button on the Nodes page did a full-page browser navigation
(`location.href`) to `/ui/nodes/<gen-id>/capture`, but `handleNodeCaptureForm`
returns a **raw** `NodeCaptureForm` component with no Base layout — no Tailwind
CSS, no htmx.js. Every other wizard trigger (Edit / Relocate / Clone / Capture
of an existing node) uses an HTMX swap into `#modal-container`, where the
surrounding page already carries the CSS + htmx, so the raw component renders
correctly. A full-page GET, however, served a bare `<dialog open>` with no
styles, and the form's `hx-post` never fired (htmx not loaded) → the browser
fell back to a native POST → another raw success response → "the page refreshes
and nothing happens, no error" (reported by a user on v0.8.3: "when adding a
node via the UI the interface looks like CSS didn't load").

- The "Add Node" button now opens the capture wizard via
  `htmx.ajax('GET', '/ui/nodes/<id>/capture', {target:'#modal-container',
  swap:'innerHTML'})` (new `addNodeOpenCapture()` helper in
  `web/static/js/app.js`) — same target/swap as the row "Capture" button, so the
  layout (CSS + htmx) is preserved and the form's `hx-post` works. The client
  node-id generation (`n<timestamp><rand>`) is unchanged in logic, just moved
  into the JS helper.
- Files: `web/templates/nodes.templ`, `web/static/js/app.js`,
  `docs/PROGRESS.md` §32. `templ generate` + `go build ./...` +
  `go test ./internal/web -p 1` green.

## [v0.8.3] — 2026-07-29

### Installer fixes (hotfix)

Two `scripts/install.sh` bugs broke fresh installs of v0.8.2 (reported by a
user: `Failed to download .../angry-box-0.8.2-linux-amd64.tar.gz`, then
`Failed to enable unit: File ... already exists` and the install aborted
before printing the "installed successfully" instructions).

- **Asset name `v`-prefix:** the release workflow names assets
  `angry-box-vX.Y.Z-<target>.tar.gz` (and ipk `angry-box_vX.Y.Z_*.ipk`), but
  `install.sh` stripped the leading `v` and built `angry-box-0.8.2-...` → 404.
  Introduced a `TAG` (with leading `v`, normalized from `--version X.Y.Z` or the
  GitHub API `tag_name`) used for the download URL path + asset name; the bare
  `RESOLVED_VERSION` is kept only for display. The constructed URL
  `.../v0.8.2/angry-box-v0.8.2-linux-amd64.tar.gz` now matches the actual asset.
- **Idempotent `systemctl enable`:** `systemctl --user enable angry-box` (and
  the system-mode `systemctl enable`) returns non-zero + "Failed to enable unit:
  File ... already exists" when the unit is already enabled (a re-install over an
  existing setup). Under `set -e` that aborted the install AFTER the binary was
  already in place — the user saw only the enable error and never the
  "installed successfully" block (Quick start + `API: http://localhost:9080/health`).
  Both `enable` calls are now `2>/dev/null || true` — an already-enabled unit is
  treated as success.

No code changes; the v0.8.2 binary assets are unchanged. This is an installer-
only patch. Re-run `curl -fsSL https://raw.githubusercontent.com/AlexeyLCP/angry-box/main/scripts/install.sh | bash` to get the fixed installer (install.sh lives in the repo, not in release assets).

## [v0.8.2] — 2026-07-28

### Base sing-box migration: sing-box-extended → amnezia-box (AWG3)

The base sing-box is now **amnezia-box** (our fork `AlexeyLCP/amnezia-box`, a
fork of `hoaxisr/amnezia-box` = sing-box 1.14 alpha). amnezia-box carries the
**AWG3 userspace endpoint** `type:"awg"` (amneziawg-go `feat/awg3 @ fc48874`,
which has the InputPackets API `transport/awg/port.go` needs). This supersedes
the old `shtorm-7/sing-box-extended` 1.13.14 stack (the `patches/` directory +
the wireguard-go overlap fix are gone — AWG3 runs through amneziawg-go, not the
userspace wireguard-go path that panicked).

**Ports from sing-box-extended into the amnezia-box fork** (commit `acb804b`,
`AlexeyLCP/amnezia-box`):
- **mtproxy** (telemt, product focus): `option/mtproxy.go` + `protocol/mtproxy/`
  + `include/mtproxy{,_stub}.go` (build tag `with_mtproxy`). Rename
  `ConnectionHandlerFuncEx → ConnectionHandlerFunc` (1.14 API). Dep
  `dolonet/mtg-multi → shtorm-7/mtg-multi v1.11.0-extended-1.0.0` (extended fork
  has `essentials.Dialer`/`DomainFrontingHost`/`UpdateUsers`). go.mod bumped
  `go 1.25 → 1.26` (mtg-multi needs 1.26).
- **fallback round-robin** (prod strategy #18 "Round-robin (fallback)"):
  `protocol/group/fallback.go` + the rr patch (`rrCounter` rotation) +
  `FallbackOutboundOptions` + `TypeFallback` + `RegisterFallback`. Self-contained,
  no 1.14 adapter API bridging.

**AWG render migration (angry-box):** all sing-box JSON AWG emitters migrated
from `type:"wireguard"` + nested `amnezia:{}` to `type:"awg"` + FLAT obfuscation
fields (amnezia-box 1.14 shape): `AwgEndpointOptions` (jc/jmin/jmax/s1-s4/h1-h4/
i1-i5 flat + AWG3 `header_protection_key`/`content_padding_addition`/
`rekey_after_time`) + `AwgPeerOptions`. Touches production transit
(`buildAWGTransportInbound/Outbound`), legacy CLI (`RenderAWGHop`, `generateAWGUser`),
test-only builders, and the takeover reader (`case "awg"`). **Kernel awg-quick
`.conf` path (user-facing AWG servers, AGENTS #10/#11) is unchanged** — it is INI
text, not sing-box JSON; `AmneziaOptions` stays as the holder for that path.

**Build/deploy/detection rebased:**
- `scripts/build-singbox.sh` (+ windows): clones `AlexeyLCP/amnezia-box @ acb804b`,
  no wireguard-go clone/patches (amneziawg-go pinned in the fork's go.mod). Build
  tags `with_awg,with_mtproxy` (dropped the old sing-box-extended canary tags
  `with_masque/with_trusttunnel/with_sudoku/with_manager/with_profiler/with_snell`).
  Tarball `deps/sing-box-<sha>-amnezia-linux-<arch>.tar.gz` (published to the
  release the deploy code pins).
- `singBoxVersion = acb804b3` (short SHA of the fork commit), `singBoxDownloadURLs`
  + `singBoxChecksums` (fail-closed) point at the amnezia-box tarball.
  `isPatchedExtended` detection canary switched to `with_awg`+`with_mtproxy`.
  Standalone-daemon deploy (`installAmneziaWGGoBinary` etc.) removed — userspace
  AWG now runs as the sing-box `type:"awg"` endpoint in-process, not a separate
  binary. `amneziaWGGoVersion` kept as a traceability const.
- `patchcheck_test.go`: pins `patchcheckABXRef` (amnezia-box @ acb804b) +
  `patchcheckAWGGORef` (amneziawg-go awg3 @ fc48874); patch-applicability test
  removed (no `patches/` for amnezia-box). Version-match uses `HasPrefix`.

**AWG3 verified live on n1** (amneziawg-go `awg3 @ fc48874`): the AWG3 fields —
`header_protection_key` (HEX 32 bytes via UAPI; amnezia-box endpoint JSON takes
base64, decoded to hex for UAPI), `content_padding_addition` (UintRange lo-hi),
`rekey_after_time` (UintRange lo-hi, seconds), with **S1-S4 >= 12**
(`HeaderCipherNonceSize=12`) — produce a working handshake + egress (2 MB
through the tunnel, `curl ifconfig.me` → server IP). The amnezia-box sing-box
binary (Revision `acb804b36`, Tags `with_awg,with_mtproxy`) coexists with the
kernel `awg-quick@awg0` + sing-box TUN-overlay trial deploy (handshake + egress
verified). See `docs/PROGRESS.md` §31.

**Removed (obsolete):** `patches/` (`fallback-round-robin.patch`,
`wireguard-go-awg-overlap.patch`), `scripts/build-amneziawg-go.sh` (standalone
daemon, Stage 1 — superseded by the in-process `type:"awg"` endpoint),
`deps/amneziawg-go-898*.tar.gz` + checksums, the old
`deps/sing-box-1.13.14-extended-*.tar.gz`.

**Docs:** AGENTS.md "amnezia-box" section (replaces "sing-box-extended") +
Known Issues #5 (flat AWG fields on `type:"awg"`); `docs/PATCHES.md` rewritten
for the amnezia-box fork pin + rebase procedure; `docs/PROGRESS.md` §31.

**Note:** the AWG3 fields (HPK/CPM/RAT) are now carried by the `type:"awg"`
endpoint struct and verified to work, but angry-box does not yet generate/emit
them from the UI/backend (the render is shape-ready; UI controls + material
generation are a follow-up). The migration is the sing-box base swap + the flat
AWG shape; AWG3 obfuscation enablement per-chain is the next step.

## [v0.8.1] — 2026-07-20

### Live QUIC signature capture on the Inbounds page

- The AWG entry profile now owns the live QUIC capture (moved from the chain
  form): CPS mimicry select (default/quic-live/quic/sip/dns/none) + capture
  domain + "Capture now" preview + captured/failed status on edit.
- **One capture per profile+domain, shared by every node**: the captured
  I1-I5 + H1-H4 live on the profile; all materialized inbounds copy them (all
  nodes of a profile mimic the same domain). Synthesized CPS stays per-node
  when no capture domain is set.
- Cache semantics mirror the chain: success cached per domain (a domain
  change re-captures), the failed-domain marker suppresses re-dialing a flaky
  domain on every deploy, a capture failure falls back to synthesized packets
  (verified live: fallback end-to-end on n1; the capture algorithm itself
  answered by a real QUIC server from the test VPS).
- Fixes since v0.8.0: entry-subnet/user-address alignment (peer and interface
  always share a /24), profile-entry double-render guard.

## [v0.8.0] — 2026-07-19

### Information-architecture refactor: first-class Inbounds, chain Levels with balancing strategies, simplified Clients

The management model was rebuilt around a strict separation of concerns:
**Nodes = infrastructure, Inbounds = listeners, Chains = routes + balancing,
Clients = access**. Sidebar: Dashboard, Nodes, Inbounds, Chains, Clients,
Settings.

#### First-class inbound profiles (`/ui/inbounds`)
- An **InboundProfile** is a node-independent listener template (AWG /
  VLESS+REALITY / MTProxy) deployed onto nodes via checkboxes. One profile →
  many nodes; per-node credentials are generated exactly once and never
  rotated on re-save. Placement is derived from `NodeInbound.ProfileID`
  (single source of truth — no two-way association to drift).
- Explicit diff semantics on save: added node → fresh creds + deploy;
  removed node → listener dropped + re-deploy (refused while a chain
  references the profile there); changed port/preset → re-deploy only the
  nodes hosting it. Port conflicts are pre-flighted before anything mutates.
- Obfuscation **Presets** fold into the Inbounds page as a tab.

#### Chain levels + balancing strategies
- A chain is now an ordered list of **levels**; each level is a group of one
  or more nodes with a selection strategy toward the next level: **Round-robin
  (fallback)** (default — the patched, production-verified path), **urltest**,
  **failover**, **selector**. Topologies like `Entry → [Hop-1, Hop-2] →
  [Exit-1, Exit-2, Exit-3]` render as per-target outbounds wrapped in a
  sing-box group outbound. AWG inter-node transport stays linear (single-node
  levels) — grouped levels require XHTTP/Reality; the AWG multi-exit kernel
  balancer is unchanged.
- The **entry level references inbounds already deployed on its nodes**
  (`ChainNode.InboundRef`) — chains never create user-facing listeners
  anymore; entry credentials moved from chain fields into the materialized
  inbound (existing chains migrated with keys preserved — clients keep
  connecting).
- The spider-web editor folds into the Chains page as the **Topology** tab;
  levelized chains render their mesh as synthetic edges (topology is edited
  in the chain form).

#### Simplified Clients (Services removed)
- Client creation = **name + chains** (per-chain exit pin optional);
  credentials (AWG peer, VLESS UUID) derive automatically from the selected
  chains. Contacts/expiry/quota/MTProxy live behind one Advanced disclosure.
  The wizard and the Services catalog are gone (`PanelSettings.Services` kept
  dormant for backup compatibility).

#### Dashboard + sidebar
- Sidebar reduced to six sections. Dashboard gains quick actions
  (+Node/+Inbound/+Chain/+Client), a pending-changes card, a mini topology
  view, and the recent audit feed. Deploy Status / Audit / Status stay
  reachable from dashboard links.

#### Schema v2 migration (automatic, on first start)
- Standalone inbounds collapse into profiles by (protocol, port, preset)
  across nodes (every collapse audit-logged); every chain gets a
  `chain-entry-<name>` profile + a materialized entry inbound carrying the
  chain's EXISTING credentials (AWG keypair, CPS I1-I5/H1-H4, subnet, VLESS
  UUID) — byte-identical awg0.conf render before/after (render-equivalence
  test in CI); flat node lists become levels by role.

#### Live-verification fixes (verified end-to-end on n1: migration, deploy, handshake, tunnel egress)
- **Subnet alignment:** a profile materialized as standalone (10.8.1+) that
  becomes a chain entry now moves to the chain-entry subnet when free, and
  user peer IPs allocate in the entry inbound's /24 — peer and interface
  always share a subnet.
- **Double-render guard:** a profile referenced as a chain entry no longer
  renders twice (chain entry + standalone loops) — the duplicate listener on
  the same port failed deploys with a second awg-quick unit.

#### Multi-user VLESS
- Chain entries and standalone VLESS+Reality inbounds now render a per-user
  `users[]` (each client's own UUID) with the shared UUID kept first for
  backward compatibility.

## [v0.7.0] — 2026-07-19

### AWG operations: deep diagnostics, per-user traffic, NAT self-heal + router packages (Keenetic/OpenWrt)

#### AWG diagnostics (node row → Diagnose)
- Deep read-only probe of the AWG data plane (`chain.DiagnoseAWGNode`,
  `GET /ui/nodes/{id}/awg-diagnostics` → modal): systemd unit, interface UP,
  listen port, peers + handshake freshness, ip_forward, rp_filter, FORWARD
  awg0→sing-box-tun, iptables package, sing-box service, sing-box-tun. Every
  check carries the evidence read on the node. Where the health badge answers
  "is it up", this answers "why is egress broken".

#### Per-user AWG traffic accounting
- The background health loop folds kernel per-peer counters (`awg show
  transfer`, awg0+awg1) into cumulative per-user bytes (peer = user AWG
  identity). Handles interface-restart counter resets; unknown peers are
  baselined but not folded. Users table gets an "AWG traffic" column (↓/↑).

#### NAT self-heal
- Same health tick re-asserts vanished iptables rules: when the FORWARD
  awg0→sing-box-tun check fails (fail2ban/docker flushes kill egress silently),
  the node's on-disk PostUp is re-run (idempotent rules) + audit entry.

#### Router packages (CI/CD): Keenetic Entware + OpenWrt
- New `scripts/build-ipk.sh`: cross-compile with `-s -w` + UPX (`--best --lzma`),
  five targets — `mipsel-3.4-kn`, `mips-3.4-kn`, `aarch64-3.10-kn` (Keenetic
  Entware: S99 init + NDMS hook scripts) and `mipsel_24kc`,
  `aarch64_cortex-a53` (OpenWrt: procd init). Makefile `build-router-ipk`.
- Keenetic NDMS hooks (`/opt/etc/ndm/{iflayerchanged,ifcreated,ifdestroyed,
  ifipchanged}.d/50-angry-box.sh`) forward interface events to the panel's
  loopback-only endpoint `POST /api/hooks/ndm`.
- Release workflow: upx + qemu-user-static, smoke-tests every router binary
  under qemu (mipsel/mips/aarch64), uploads all five .ipk + checksums.
- Size: stripped binaries ~11–13 MiB (Makefile LDFLAGS previously lacked
  `-s -w`), UPX compresses ~3× further. Legacy `scripts/build-opkg.sh` removed.

## [v0.6.1] — 2026-07-19

### AWG ops hardening (LucX-UI ports): zero-downtime peer sync, proper standalone obfuscation

#### Live peer sync without awg-quick restart (LucX SyncPeers port)
- Deploys that only change the peer set (user add/remove — the most frequent
  operation) no longer restart `awg-quick@`: when the pushed conf's [Interface]
  section matches the on-disk one and the service is active, peers are applied
  live via `awg set` (add/update/remove) — existing clients never drop.
  [Interface] changes (keys/amnezia/PostUp) still take the restart path, and a
  failed sync falls back to restart.
- Order-bug found live during validation: the sync decision must compare
  BEFORE the conf overwrite, otherwise interface changes are silently skipped
  (the node keeps running the old config). Fixed + regression tests at the
  pushAWGConfs level.

#### Standalone AWG: persisted obfuscation material
- Standalone AWG servers rendered H1-H4 as degenerate zero-width "N-N" ranges
  (e.g. 1984-1984) — header-junk randomization off, fingerprintable. NodeInbound
  now persists proper quadrant H1-H4 + CPS I1-I5 (mirroring model.Chain):
  EnsureInboundAWGMaterial (deploy + lazy on client-conf render),
  ResolveStandaloneAWGPreset. Bonus fix: the standalone client conf always used
  the DEFAULT preset (silent mismatch on custom-preset inbounds).
- The applier now persists ensured per-inbound fields (UUID/keys/material) —
  they were in-memory only before.
- Live-verified n1→n2 (kernel 6.12): proper H ranges server↔client, handshake
  PASS, egress through the tunnel OK.

#### Deploy: Debian 13 prerequisites
- InstallAWGModule now installs `iptables nftables openresolv` (Debian 13
  doesn't ship iptables by default; awg-quick PostUp MASQUERADE/FORWARD fails
  without the shim — reproduced on n2).

## [v0.6.0] — 2026-07-18

### Product roadmap complete: egress verified, warm-pool auto-relocate

Minor bump: closes every item of the v0.6.0 roadmap (docs/PROGRESS.md §15).
P0a (egress proof), P2b (auto-relocate), P2c (CLI deprecate + mirror) landed
this cycle; P0b/P1a/P1b/P2a landed earlier in the cycle (users wizard +
Service model + subscription URL, node health state machine, clone node,
encrypted offsite backups — see git history §16–§20).

#### P0a — client-tunnel egress VERIFIED (kernel 6.12, real cross-machine)
- Full proof on the n1→n2 test pair (both Debian 13, kernel 6.12): orchestrator
  deploy (standalone AWG, ApplyMergedNode) on n2 + awg-quick client on n1 →
  `curl ifconfig.me` through the tunnel returns the server IP. A/B shows egress
  works with and without `auto_redirect` — the earlier empty-egress symptom was
  a same-host-client test-topology artifact, not a product bug.
- `auto_redirect` is now an opt-in harness: `AB_AWG_AUTO_REDIRECT=1` wires
  `AWGTUNOverlayParams.AutoRedirect` (previously declared but never populated).
- Reproducible driver: `cmd/awgtrial` (deploy + client .conf render).

#### kernel 6.12 (Debian 13) compatibility fixes
- **I1-I5 removed from server-side awg confs** (`RenderServerAWGConf`,
  `RenderExitServerAWGConf`): CPS packets are initiator-only (the module's
  receive path never reads ispecs), and `awg setconf` on kernel 6.12 rejects
  them in the conf body — awg-quick up rolled back the interface. Exit-link
  initiators (`RenderExitAWGConf`) now apply I1-I5 via a PostUp `awg set` UAPI
  line (accepted on all kernels). Client app confs are unchanged (AmneziaWG
  apps parse inline I1-I5 natively).
- Debian 13 node prerequisites documented: `apt install iptables nftables
  openresolv` (PostUp MASQUERADE/FORWARD need the iptables shim; auto_redirect
  needs nftables).
- Finding (Known Issue #17): the default preset's Jc=120 junk flood kills the
  handshake on lossy networks (budget hostings drop part of the flood,
  including the init). Workaround: lower `jc` (e.g. `awg set <iface> jc 3`).

#### P2b — warm-pool auto-relocate (opt-in, guardrails)
- When the health state machine moves a node to down/unreachable, the
  orchestrator can automatically relocate it onto a spare VPS via RelocateNode
  (identity preserved — keys/ports/transit material unchanged, clients not
  reconfigured).
- Guardrails: global `PanelSettings.AutoRelocate.Enabled` (default off) AND
  per-node `NodeInfo.AutoRelocate` (default off), cooldown per node (default
  6h), spare = `NodeInfo.Spare` with no chains/inbounds, blocked nodes never
  trigger. Every decision is audit-logged.
- UI: node edit checkboxes (Spare / Auto-relocate), Settings → Auto-relocate
  card (cooldown + global toggle). New `Store.DeleteNodeInfo`.
- Fix along the way: `handleUpdateNode` no longer wipes Inbounds/Takeover/
  P2b flags when editing a node (it rebuilt NodeInfo from scratch).

#### P2c — deploy hardening
- `ANGRY_BINARY_MIRRORS` (comma-separated): sing-box binary download now tries
  primary + mirror URLs in order, each verified against the pinned sha256
  (GitHub release assets were a SPOF, sometimes unreachable from RU networks).
- All binary URLs are validated before reaching a root shell (shell-injection
  hardening, same class as the earlier AWG tarball fix).
- Legacy CLI standalone-AWG path carries a deprecation warning pointing at the
  kernel-AWG deploy (web UI / apply-chain).

## [v0.5.0] — 2026-07-08

### Server backups + quick node relocation (chain auto-heal)

Minor bump: new operator features (backups + node relocation) + a latent
re-apply bugfix that AWG chains depended on.

#### ResolveNodes preserves all transit/exit/role fields (bugfix)
- `ResolveNodes` (`internal/chain/store.go`) rebuilt a fresh `ChainNode`
  copying only `Port + TransitPrivKey/ShortID/UUID + Inbounds`, dropping
  `Role, ExitTargets, TransitAWG*, ExitAWG*, ExitAWGLinks`. On the next
  `ApplyChain` after a process restart those AWG fields were empty → keys
  regenerated → inter-node AWG links broke (previous node's outbound
  `peer.PublicKey` no longer matched the new server pubkey; balancer
  `awg-exit-nX` no longer matched the exit's new server key). A latent
  re-apply bug for any AWG chain after an orchestrator restart, and it also
  blocked node relocation (which needs the same keys reused on the new VPS).
  Fix: copy the stored `ChainNode` wholesale, overwrite only the live-Host
  fields + Inbounds. Regression tests pin every persisted field survives.

#### Backups (full-panel + per-node export/import)
- `ExportStore`/`ImportStore`: whole-panel plaintext JSON backup (portable —
  not the on-disk encrypted form, so a backup restores on a different install
  without the same master key). `ImportStore` refuses a non-empty target
  without `force` (wipe protection), re-runs schema migrations on restore.
- `ExportNode`/`ImportNode`: one node's portable identity — Host + NodeInfo +
  the full `ChainNode` record (with all transit/exit material) for every
  chain it belongs to. `ImportNode` dedups by ID (refuses to reroute a live
  node without `force`), merges chain memberships by name, skips chains that
  do not exist on the target (a node backup alone never invents a half-chain).
- A backup envelope (`format=angry-box-store|angry-box-node`, `version`) makes
  a unified restore path auto-detect the backup kind via `DetectBackupFormat`.
- HTTP: `GET /ui/backup/store` + `GET /ui/backup/nodes/{id}` (download,
  404 unknown) + `POST /ui/backup/import` (auto-detect, `force=on`).
- UI: Settings → Backups section (Export panel + Import form); node row →
  Export button.
- CLI: `angry-box backup store [-o file]`, `angry-box backup node <id>
  [-o file]`, `angry-box restore <file> [--force]`.

#### Node relocation (auto-heal dependent chains)
- `RelocateNode` (`internal/chain/relocate.go`) moves a blocked node to a new
  VPS: updates Addr (+ optional User/KeyPath) in Host + NodeInfo + the
  `ChainNode` snapshot, keeping the ID + ALL transit/exit material unchanged,
  then re-applies every chain containing the node. `ApplyChain` re-deploys the
  node itself (onto the new VPS, reusing its persisted keys) AND every node
  whose config embeds the node's Addr — the previous hop (outbound dials
  `extractHost(N.Addr)`) + any balancer whose `ExitTargets` include N
  (`awg-exit-nX` endpoint embeds N.Addr). One call heals the whole affected
  topology; other nodes + existing clients are NOT reconfigured (same keys).
- HTTP: `POST /ui/nodes/{id}/relocate` (validates new_addr + SSH key id exists
  before mutation → `RelocateResult` per-chain report); `GET .../relocate`
  modal. UI: node row → Relocate button → modal. CLI: `angry-box relocate
  <node-id> --addr <new-ip:port> [--user <user>] [--key <key-id>]`.
- A failing chain re-apply is recorded, not fatal — the report carries
  per-chain success/error so the operator sees what healed and what to retry.

#### Tests + i18n
- ResolveNodes regression, backup roundtrip/dedup/force, relocate 3-place
  addr update + key-reuse + one-failure-does-not-abort, handler export/import
  + relocate error paths. i18n keys (en+ru, parity): Backups, Export panel,
  Import backup, Export node, Relocate node, Relocate to new VPS, New address,
  New SSH user/key (optional), Store/Node imported, Overwrite existing (force),
  the relocate help text.

See `docs/PROGRESS.md` §14.

## [v0.4.1] — 2026-07-08

### Fixes — UI bugs found by live E2E + i18n gaps + release body

#### Settings: language switch + duplicate "Add New SSH Key" + SSH key data-loss
- **Language switch was silently broken.** Root cause: the `SSHKeyList`
  component rendered its own `<form>` elements (add/import/test/delete) INSIDE
  the main settings `<form>`, but HTML forbids nested `<form>` — the browser
  closed the outer form at the first nested one, dropping the Save Settings
  button (and the language select's submit) out of the form, so Save Settings
  no-op'd and the language never changed. Fix: moved `#ssh-keys-list` + the
  add-key form OUTSIDE the main settings `<form>` in `settings.templ`. The main
  form now holds only plain inputs + the Save Settings submit; SSH keys are
  managed through their own `/ui/settings/ssh-keys*` endpoints. The same
  nesting had produced a second, duplicate "Add New SSH Key" form visible on
  the page — both fixed by the one move. Verified on a live browser.
- **SSH key data-loss.** `handleSaveSettings` rebuilt `settings.SSHKeys` from
  `ssh_key_name`/`ssh_key_path` form fields — a legacy pre-v0.2.5 KeyPath-based
  schema. After the redesign the main settings form no longer carries those
  fields (keys are added via `/ui/settings/ssh-keys` with PEM `key_data`), so
  this block clobbered `settings.SSHKeys` to an empty slice on every Save
  Settings — saving the language wiped all imported keys. Removed.
- Regression tests: `TestHandler_SettingsView_NoNestedFormsInMainForm` (pins
  the structure — heading ×1, Save Settings inside the main form,
  `#ssh-keys-list` after `</form>`), `TestHandler_SaveSettings_PreservesSSHKeys`
  (pre-seed key → save panel settings → key survives + language persists).

#### QUIC CPS capture panic on partial capture
- `CaptureQUICSignature` panicked on a partial QUIC capture
  (`awgcapture.go`): `packets[:captureMaxPkts]` (5) crashed with "slice bounds
  out of range" when the read loop returned fewer packets (timeout/loss). A
  live VPS hitting `one.one.one.one` returned 2 packets and crashed the
  orchestrator. Extracted the slice logic into `capturePacketsToCPS` with a
  `min(len, captureMaxPkts)` clamp — partial capture yields a partial CPS set,
  not a crash. Production `EnsureChainAWGMaterial` already requires `len >= 5`
  for live capture (else synthesized fallback), so partial captures route to
  fallback correctly. 4 unit tests (PartialNoPanic regression + Full +
  ClipsOversized + Empty).

#### Rollback test self-staging
- `TestE2E_Heavy_Rollback_OnBadConfig` failed on a fresh VPS:
  `performRollbackTest` called `PushConfig` directly (bypassing `Deploy`),
  which writes to `/etc/sing-box/config.json` but does not `mkdir` that dir.
  On a clean VPS the raw push failed with "No such file or directory" before
  the rollback path was reached. `performRollbackTest` now pre-stages via
  `DeployWithOptions` so the rollback fixture is self-contained and runs on a
  clean VPS.

#### i18n — untranslated UI elements
- 21 built-in preset descriptions (russia_2026, iran_2026, china_2026,
  maximum_stealth_2026, pro_2026, xhttp_max_stealth_2026 + their _awg/
  _reality/_xhttp variants) were always-English even in ru locale. Added
  `i18n.TPreset(ctx, name, fallback)` which looks up
  `preset.<name>.description` and falls back to the source string; the presets
  template now renders built-in descriptions through it. en+ru keys for all 21
  (parity test passes). Custom user presets keep their user-typed description.
- The license no-warranty clause in `settings.templ` was hardcoded English
  despite the i18n key existing — wrapped in `i18n.T` so it renders the ru
  translation ("ПРЕДОСТАВЛЯЕТСЯ «КАК ЕСТЬ»...").

#### Release workflow — per-version body
- The release workflow uploaded the full `CHANGELOG.md` as every release's
  body, so every release showed the entire history instead of its own slice.
  Added an "Extract this release's changelog section" step that pulls just the
  `## [<tag>]` section into `release-body.md` (awk `index()` match, with a
  full-file fallback for dev tags without a CHANGELOG entry); the
  `softprops/action-gh-release` step now uses `body_path: release-body.md`.

### Live E2E verification (fresh GCloud Debian 12 VPSes)
- 21 core E2E tests PASS: chain building (1/2/3-hop + topology change), VLESS+
  Reality+XHTTP transport, AWG kernel (single + 2-hop + userspace), balancer
  (urltest + failover), selector strategy (live egress IP switches middle↔exit),
  rollback, takeover, import AWG, idempotency, concurrency, hostlock, QUIC
  capture → AWG config. See `docs/PROGRESS.md` §13.4/§13.5.

## [v0.4.0] — 2026-07-07

### Architecture — multi-AWG-interface + takeover re-render + Reality SNI + tests

Minor bump: multi-AWG-interface (awg0/awg1) + takeover re-render are
architectural, but backward compatible (new fields omitempty, default behavior
unchanged — existing stores/deployments are forward-compatible).

#### Reality SNI configurable (AGENTS.md #10 / CTO-review §2)
- `PanelSettings.DefaultRealitySNI` — global default SNI for REALITY/TUIC
  inbounds when no preset specifies one. Empty = built-in const (no behavior
  change for existing stores). `chain.SetDefaultSNI` / `EffectiveDefaultSNI`
  accessors set at startup + on settings save. `ResolveServerName` + the
  singbox `RenderProxyNode` / `renderStandaloneFromParams` fallbacks now call
  `EffectiveDefaultSNI()` instead of the bare const — the operator's override
  applies everywhere via one central accessor. Settings UI input + i18n (en+ru).

#### Multi-AWG-interface awg0/awg1 (AGENTS.md Known Issue #10)
- A chain AWG entry (awg0, 10.8.0.1/24) + a standalone AWG inbound with a
  distinct subnet (`NodeInbound.AWGServerAddress`, e.g. 10.8.1.1/24) now
  COEXIST: the chain entry keeps awg0, the standalone deploys on a SECOND
  kernel AWG interface (awg1) with its own `awg-quick@awg1` unit + subnet +
  PostUp FORWARD rules. Previously (v0.3.1) the standalone was skipped with a
  loud warning; now it deploys.
- `AWGServerConfParams.InterfaceName` (default awg0) — `RenderServerAWGConf`
  PostUp/PostDown use `p.InterfaceName` for rp_filter + FORWARD rules, so awg1
  gets its own rules coexisting with awg0. `renderStandaloneAWGConf`
  parameterized by interface name. `tunIncludeInterfacesForNode(node, nodeInfo)`
  appends awg1 to the TUN overlay `include_interface` list when a standalone
  with a distinct subnet coexists with a chain entry. Tests: multi-interface
  render, InterfaceName PostUp, include list.

#### Takeover re-render fresh awg0.conf (AGENTS.md #10 follow-up)
- The AWG takeover now OWNS the kernel awg0.conf after success, instead of
  leaving the imported one untouched. A fresh `awg0.conf` is rendered from the
  imported server config + materialized `model.User` peers via
  `chain.RenderTakeoverAWGConf`, pushed atomically with the sing-box TUN-overlay
  config via the exported `chain.PushConfigWithAWG` (rollback of both on
  sing-box failure + synthesized-user cleanup). `chain.AwgServerConfigToAmnezia`
  adapts the imported server config to `*config.AmneziaOptions`.
- The takeover constructs a `model.NodeInbound` (Protocol=awg, ForUsers wired
  to the synthesized user IDs) so `usersByInboundMap` finds the peers on future
  `ApplyMergedNode` re-applies — per-client `source_ip_cidr` routing is
  available immediately, not deferred to a later re-apply. Materialization moved
  BEFORE the push (was step 8, now step 5) so the fresh conf can render peers.

#### Table-driven tests (CTO-review §13)
- `TestGenerateSSPassword` (replaces 3 funcs), `TestStore_GetNotFound`
  (replaces 4 funcs), `TestChecksumForArch` (replaces 3 funcs) — table-driven
  with `t.Run` subtests, matching the existing idiom.

#### Benchmarks (CTO-review §13)
- `BenchmarkSaveAuditLog` (jsonl O(1) append), `BenchmarkStoreReadStore` /
  `BenchmarkStoreWriteStore` (full-file marshal, 50-host store),
  `BenchmarkGenerateProxyPassword` (rejection sampling). `make bench` target.
  CI does not run `-bench` (zero risk).

#### Coverage baseline in CI
- `ci.yml` build-test job: a "Coverage summary" step prints
  `go tool cover -func=coverage.out` per-package so coverage regressions are
  visible in the CI log. `docs/COVERAGE.md` regenerated (2026-07-07): notable
  ssh 11.2% → 42.7% (pool tests), takeover 64.4% → 60.0% (re-render path added).

#### Verification
- `go build ./...` OK, `go vet ./...` clean, `go test ./...` 9 packages ok / 0
  FAIL, `e2e` + `wsl_smoke` compile-only green, `govulncheck`: no vulnerabilities,
  `templ generate`: no diff.

#### Known follow-ups (NOT in this release — deferred)
- per-client `source_ip_cidr` real-VPS verify (needs GCloud).
- `ip rule 10.8.0.0/24 → table 2022` (gated on a real-VPS verdict).
- deps/sing-box mirror (infra).
- CLI standalone-AWG kernel conversion (needs Host-shaped TUN renderer).

## [v0.3.2] — 2026-07-06

### UI redesign — Tokyo Night theme (ported from hoaxisr/awg-manager, MIT)

Full visual redesign under the Tokyo Night aesthetic. The stack is unchanged
(HTMX + Go Templ + DaisyUI v4 + Tailwind play CDN) — NO build step introduced.
Themes are hand-written CSS-override blocks mapping the Tokyo Night palette onto
DaisyUI v4 OKLCH CSS variables, so every DaisyUI component picks up the palette
automatically.

- **IBM Plex fonts** — 14 self-hosted woff2 (Sans 400/500/600/700 + Mono
  400/500/600, Latin + Cyrillic via unicode-range) in `web/static/fonts/`,
  embedded via `web/embed.go`. `web/static/css/fonts.css` @font-face. Body
  IBM Plex Sans 14px/1.5; `.font-mono`/code/pre → IBM Plex Mono.
- **3 selectable Tokyo Night themes** — `tokyonight` (dark, default),
  `tokyonight-day` (light), `tokyonight-storm` (dark variant, bluer bg). Each
  maps accent `#7aa2f7` + the Tokyo Night bg/text/border ramp + success/error/
  warning/info/broken onto DaisyUI v4 `--p/--b1/--b2/--b3/--bc/--n/--su/--er/
  --wa/--in` (OKLCH) + `--rounded-box 12px/--rounded-btn 6px`. Extra `--tn-*`
  custom props (tints, broken, latency, z-index, settings-gap) for `.tn-*`
  classes. `web/static/css/tokyo-night.css` (loaded after daisyui CDN).
- **Theme dropdown** — the header sun/moon toggle is now a DaisyUI dropdown with
  the 3 themes (each a button + color swatch). `app.js setTheme(name)` persists
  to localStorage; `toggleTheme()` kept as a dark/light shortcut. Migrates old
  `dark`/`light` localStorage values. `<meta name=theme-color>` syncs per theme;
  pre-paint script prevents FOUC. i18n keys (en+ru).
- **Component conventions** — `web/static/css/app.css` `.tn-*` classes:
  `.tn-card` (unifies the two inconsistent glass/plain card flavours — bg-200,
  1px border, 12px radius, shadow, hover); `.tn-table` (header bg-300 uppercase
  muted, rows bg-200, hover 70%-opacity wash, tunneled-row tint);
  `.tn-badge-*` (semantic tints 18%/borders 40%, broken 14%, pill + status-dot);
  `.input-bordered`/`.select-bordered`/`.textarea-bordered` overridden to the
  Tokyo Night inset look (bg-300 inside bg-200 cards, focus → accent border +
  ring); scrollbar re-tokened on `--tn-border`; settings-inset layout;
  theme-dropdown polish; z-index scale utilities.
- **Layout polish** — `<html data-theme="tokyonight">` default; `<meta
  name=theme-color #16161e>`; favicon.svg (Tokyo Night accent); scrollbar moved
  to app.css (theme-tokened, was hardcoded greys).
- **Page templates** — all glass cards + plain cards → `.tn-card` across
  dashboard/nodes/chains/users/settings/presets/spider/index; all `<table
  class="table…">` → `.tn-table`. Badges stay DaisyUI (theme swaps their
  `--su/--er/--wa/--in` colors). No logic changes — only class swaps; all
  i18n.T wrappers, hx-* attrs, modals preserved.
- **Attribution** — `docs/CREDITS.md` + `docs/LICENSES/hoaxisr-awg-manager-MIT.txt`
  (the source's MIT license preserved). Design tokens + IBM Plex fonts + component
  conventions ported; no Svelte/Skeleton components (different stack).

#### Verification
- `go build ./...` OK, `go vet ./...` clean, `go test ./...` 9 packages ok / 0
  FAIL, `e2e` + `wsl_smoke` compile-only green, `govulncheck`: no vulnerabilities,
  `templ generate`: no diff.

## [v0.3.1] — 2026-07-06

### Architecture follow-ups (deferred from v0.3.0) + UI fixes + patches safety

Patch bump: no protocol/store-schema changes that break clients. NodeInbound
gains an optional field (omitempty); TakeoverState gains an optional field;
audit log splits to a sibling file (legacy inline preserved read-only). All
backward compatible.

#### Audit log write-amplification split (CTO-review §12 D1)
- Every `SaveAuditLog` used to rewrite the ENTIRE store.json (O(file) per entry —
  write amplification growing with the store). Now appends ONE json line to a
  sibling `<store>.audit.jsonl` file under a separate `auditMu` (O(1) per entry,
  no readStore/writeStore of the main store). `ListAuditLogs` merges the legacy
  inline `storeFile.AuditLogs` (read-only, for pre-split stores) + the jsonl tail,
  dedupes by ID, returns newest-first capped to 5000. Periodic compaction
  (rewrite to last 5000 lines) when the jsonl exceeds 2×cap. Migration-safe:
  store.json's inline AuditLogs left untouched; a future schema bump can drain.
- Tests: `TestStore_AuditLog_WritesJsonlNotStore` (store.json mtime unchanged
  across appends while jsonl grows), `_MergesLegacyInline`, `_DedupByID`.

#### AWG per-inbound server IP allocation (AGENTS.md Known Issue #10)
- A node hosting BOTH a chain AWG entry (10.8.0.1/24) AND a standalone AWG
  inbound (also 10.8.0.1/24) collided on the single awg0 interface. The old
  `if len(files)==0` guard in `RenderNodeAWGConfs` silently dropped the
  standalone — the operator never saw the collision. Now:
  `model.NodeInbound.AWGServerAddress` (omitempty, default 10.8.0.1/24) is the
  per-inbound server tunnel address; `allocateAWGPeerIPInSubnet(prefix, taken)`
  allocates peers in the inbound's own /24; `RenderNodeAWGConfs` returns
  warnings (chain entry + standalone collide → loud warning + skip, not silent
  drop). applyMergedNodeLocked adds warnings to mergeReport.Warnings.
- Migration-safe: existing inbounds have empty AWGServerAddress → 10.8.0.1/24
  default → no behavior change for current stores.
- Multi-AWG-interface (awg1) on one node is the deferred follow-up (needs
  interface naming + include_interface list + awg-quick@awgN services).

#### Takeover'd AWG peers → model.User materialization (AGENTS.md #10)
- Takeover'd AWG imported the peers (PublicKey + AllowedIPs) but created no
  `model.User` records → `source_ip_cidr` per-client routing was unavailable on
  a takeover'd AWG inbound. Now `MaterializeAWGPeersAsUsers` (internal/chain/
  awg_takeover_users.go) creates a User per peer (deterministic ID
  `takeover-<nodeID>-<pubKeyPrefix8>`, dedup by ID + by AWGPublicKey, idempotent)
  on the takeover success path; IDs recorded on `TakeoverState.SynthesizedUserIDs`
  for rollback symmetry (`DeleteSynthesizedAWGUsers` on rollback). The kernel
  awg0.conf is left untouched; materialized users become visible in the UI and,
  on the next ApplyMergedNode re-apply with a real chain/inbound, render into a
  fresh awg0.conf (switching the takeover to pushing that fresh conf is a
  follow-up — materialization is the prerequisite).

#### SSH cross-deploy connection POOL (CTO-review §8 follow-up)
- The v0.3.0 intra-deploy collapse (3 dials/merged-deploy → 1) was the big win;
  this adds the second-order win — a node re-deployed by autoapply every ~5 min
  reuses its already-open connection instead of re-dialing. `internal/ssh/pool.go`
  `Pool` wraps `ports.SSHConnector`, keyed by (addr|user|keyPath); borrow-time
  Ping (keepalive@openssh.com via the new `ports.Pinger` capability) + stored
  known-host fingerprint re-check (TOFU staleness — operator key rotation caught
  cheaply) + key-resolution re-check (PEM change evicts). `pooledClient.Close()`
  returns to pool (not real close). Idle sweeper (60s, > 5min TTL); `pool.Close()`
  on graceful shutdown (after WaitAutoApply). Wired only at the composition root
  in serveCmd (NOT baked into DefaultConnector — keeps test dial counts stable).
- Tests: TestPool_ReusesConnection (1 dial), _EvictsOnPingFail,
  _EvictsOnHostKeyFingerprintChange, _CloseTearsDown, _IdleEviction.
- A true connection pool's residual TOFU risk (a pooled connection never
  re-runs the full HostKeyCallback — x/crypto/ssh doesn't expose the negotiated
  key post-dial) is mitigated by the stored-fingerprint re-check; a live MITM
  between pool-misses is bounded by the autoapply interval. Documented in
  internal/ssh/pool.go.

#### UI fixes (user-reported)
- **Language switch (ru) not applying:** `<html lang="en">` was hardcoded → now
  dynamic via `i18n.Lang(ctx)`. UI pages now send `Cache-Control: no-store` (auth
  middleware) so a post-save HX-Refresh reloads from the server, not a stale
  browser cache (the most likely root cause). TestHandler_SaveSettings_LanguagePersists
  saves language=ru, asserts HX-Refresh + re-GET shows the ru option selected + a
  ru i18n string.
- **User create import-secret block:** the secret_type dropdown offered
  AWG/TUIC/VLESS/SS/Trojan/VMess/Hysteria2 imports (complex, error-prone, not
  product targets; TUIC/Hysteria2 FROZEN). Now only None + Telemt (MTProto); a
  legacy non-telemt SecretType on an existing user shows as a disabled "Legacy
  import (edit-only)" option. handleCreateUser rejects a forged POST with a
  non-telemt type (400); handleUpdateUser preserves an existing legacy type
  (disabled option not submitted → empty form value must NOT wipe it), rejects
  switching TO a non-telemt type.

#### CLI standalone-AWG deprecate (AGENTS.md #11)
- `config --protocol awg` / `apply --protocol awg` now print a deprecation
  warning: the path uses the legacy userspace RenderAWGHop endpoint (diverges
  from the kernel-AWG rework the web UI deploys). Points operators to the web UI
  / apply-chain. Path still works (no break); conversion to kernel mode is a
  follow-up (needs a Host-shaped TUN-overlay renderer).

#### Patches rebasing safety (C2)
- `internal/backend/singbox/patchcheck_test.go` (`//go:build patchcheck`): a
  gated regression test that clones the pinned upstream sing-box-extended +
  wireguard-go at their tags and runs `git apply --check` on each patch. Fails
  loudly on context drift (an upstream bump that breaks patch applicability is
  caught BEFORE a broken tarball is built). `TestPatchcheckVersionsMatchSingBoxConst`
  asserts the patchcheck version consts match the deploy-time `singBoxVersion`
  const. CI gains a `patchcheck` job (network + git, ~300s, separate from
  build-test).
- `docs/PATCHES.md`: the law for rebasing — the two patches + upstream targets,
  the build-singbox.sh flow, the THREE-place version pin, the rebase procedure,
  the Reality SNI drift note. Pointer in AGENTS.md.

#### Verification
- `go build ./...` OK, `go vet ./...` clean, `go test ./...` 9 packages ok / 0
  FAIL, `e2e` + `wsl_smoke` compile-only green, `govulncheck`: no vulnerabilities,
  `templ generate`: no diff, `go vet -tags=patchcheck` clean.

#### Known follow-ups (NOT in this release — explicitly deferred)
- Multi-AWG-interface (awg1) on one node (A1 follow-up — interface naming +
  include_interface list + awg-quick@awgN services).
- Takeover re-render of a fresh awg0.conf from materialized users (A2 follow-up
  — the kernel awg0.conf is currently left untouched by takeover).
- per-client `source_ip_cidr` under TUN-overlay real-VPS verify (needs GCloud).
- deps/sing-box mirror/backup (infra).
- `ip rule 10.8.0.0/24 → table 2022` (gated on a real-VPS verdict).

## [v0.3.0] — 2026-07-06

### Architecture — CTO-review §4/§8 + CI release automation + operational debt

Minor bump: the deploy-path SSH plumbing, the config-struct refactor, and the
route split are architectural (not just fixes). No protocol changes, no store
schema change — configs and store.json from v0.2.5 are forward-compatible.

#### `buildMergedNodeConfig` 5-param → `MergedNodeConfigParams` struct (CTO-review §4)
- The 5-param signature (`nodeInfo, nodeChains, usersByChain, usersByInbound,
  mtproxyUsers`) was a 7-arg-smell: both production callers assembled params 3/4/5
  identically via `usersByChainMap` + `usersByInboundMap` + `ListMTProxyUsersForNode`.
- New `MergedNodeConfigParams` struct (mirrors existing `ProxyNodeParams` /
  `ClientConfigParams` idiom: optional user maps default to legacy single-user).
- `NewMergedNodeConfigParams(store, nodeInfo, chains)` constructor derives the
  three user collections from the store — single source of truth for the
  per-client routing plumbing. 11 test call sites updated.

#### `web/server.go` Register (~60 routes) split per resource (CTO-review §4)
- Register was a ~60-route monolith mixing every resource. Routes unchanged;
  only organization. Each resource handler file now owns a
  `register<Resource>Routes(mux)` method (11 files), called from a thin
  top-level Register that keeps only the static FS + `/` redirect.
- Routes grouped by path prefix; handlers stay where they are. Middleware
  (`s.auth`) uniform — no per-route variation.

#### SSH intra-deploy connection collapse (CTO-review §8)
- A merged deploy to one host opened **3 separate SSH connections**
  (applier client + `backend.DeployWithOptions` + `backend.InstallAWGModuleWithOptions`);
  ApplyChain per-node was ~4 (pre-flight + the 3). A true connection POOL was
  deferred (TOFU-staleness risk — a pooled connection never re-verifies the host
  key after dial; `x/crypto/ssh` doesn't expose the negotiated key cleanly).
- The right win is **plumbing**: a new optional `ports.ClientBackend` interface
  (`DeployOptsAndClient` + `InstallAWGModuleWithClient`) lets the chain/merged
  applier pass its already-open client into the backend methods so they don't
  dial. singbox.Backend implements it (compile-time assert); callers type-assert
  and fall back to the dialing variants for backends that don't.
- Result: merged deploy = 1 Connect (was 3); ApplyChain per-node = 2 (pre-flight
  + collapsed deploy, was ~4). Tests:
  `TestApplyMergedNode_OpensOneConnection`, `TestApplyChain_OpensOneConnectionPerNode`.
- The autoapply concurrency cap (v0.2.5) bounds the global fan-out; this collapse
  removes the intra-deploy 3× redundancy. A true cross-deploy connection POOL
  remains a v1.0 follow-up.

#### CI release automation (CTO-review §4 infra)
- New `.github/workflows/release.yml`: triggered by `v*` tag push, builds the
  full artifact set on ubuntu-latest (has `ar`/GNU `tar`/`gzip`) and uploads to
  a GitHub release via `softprops/action-gh-release@v2`. Produces the same
  artifact set as the manual v0.2.5 release: 4 linux tarballs + windows zip +
  2 .ipk + checksums-sha256.txt, with `git describe` version injection.
- Makefile fixes the audit found: removed dead duplicate opkg targets; fixed
  `build-arm64-opkg` arg order (`build-opkg.sh` signature is `<binary> <arch>
  <version> <outdir>` — was passing version as arch); dropped the broken
  `build-keenetic-opkg.sh` wrapper; restored `GOMIPS=softfloat` on the mipsel
  build (lost in the canonical block — Keenetic soft-float targets would SIGILL
  without it); added `-trimpath` to every cross-build; added
  `build-windows-amd64` target; `build-all` now produces 5 binaries.

#### Operational debt (5-min tweaks)
- **install.sh `--uninstall`** (AGENTS.md #6): wiped `store.json` (SSH private
  keys for the whole fleet + secrets + audit logs + the at-rest encryption key)
  with `rm -rf` and NO confirmation. Now prompts to back up to
  `~/angry-box-backup-<ts>.tar.gz` first (default Yes), then a separate [y/N]
  confirm before the rm (default No). Piped `curl|sh` exits 1 with
  manual-backup instructions.
- **AWG `I1Packet` override** (AGENTS.md Known Issue #10): the preset field was
  parsed but never emitted — the override never reached the rendered .conf.
  `BuildAmneziaSection` now applies it: `"quic-1200"` → 1200-byte QUIC Initial,
  `"dns-1232"` → 1232-byte DNS query, any other non-empty value → base64-decoded
  literal. Invalid literals fall back to the generated I1 (no panic).

#### Verification
- `go build ./...` OK, `go vet ./...` clean, `go test ./...` 9 packages ok /
  0 FAIL, `e2e` + `wsl_smoke` compile-only green, `govulncheck`: no
  vulnerabilities, `templ generate`: no diff.

#### Known follow-ups (NOT in this release — explicitly deferred)
- SSH connection POOL (reuse across distinct deploys with idle eviction + TOFU
  re-verify on borrow) — v1.0.
- AWG per-inbound server IP allocation (B.3 — model + migration).
- Takeover'd AWG peers → `model.User` materialization (B.4 — rollback symmetry).
- Audit log write-amplification split (B.1 — optimization; the 5000 cap is in place).
- `ip rule 10.8.0.0/24 → table 2022` (B.7 — gated on a real-VPS verdict).
- per-client `source_ip_cidr` under TUN-overlay real-VPS verify (needs GCloud).
- deps/sing-box mirror/backup (infra).

## [v0.2.5] — 2026-07-06

### Hardening — CTO-review cycle (all 10 Top-10 blockers closed properly)

This release closes every open item from the 9-agent CTO review. Several
blockers (panics in request path, layering split, context propagation,
autoapply cap) had been declared "closed" in the prior cycle but were not —
this release actually closes them, with tests.

#### No panics in the request / deploy path (CTO-review #3)
- `mustMarshal` (singbox) replaced by `marshal` returning `(json.RawMessage,
  error)`; propagated through `RenderProxyNode` / `RenderAWGBalancer` /
  `RenderAWGHop` and the standalone `GenerateConfig` path. A future
  un-marshalable struct field returns an error instead of crashing the
  orchestrator.
- `cryptogen.GenerateInboundTag` / `GenerateTUICPassword` /
  `GenerateProxyPassword` / `GenerateStableTUICUserCreds` / `EnsureUserCreds`
  now return `(value, error)`. Errors propagate through `ApplyChain`,
  `RenderClientConfig`, `buildChainRoleInOut` (becomes a deploy-failing
  roleError), `chainTUICUsers`, and the web handlers (HTTP 500 with an i18n
  message).
- External `_ = LoadPresets` (web/nodes.go, web/presets.go) now logs via
  `slog.Warn` instead of swallowing (AGENTS.md #6).
- The `recover()` middleware in `web/auth` remains as the safety net for any
  residual panic.

#### No silent failures (AGENTS.md #6)
- Takeover `rollbackToOldVPN`: `_ = RestoreFile` now propagated — a rollback
  failure surfaces and marks the result "failed-both" (was silently dropped,
  leaving the node in a broken state with no signal).
- Takeover `_ = SaveNodeInfo` ×4 → `slog.Warn`.
- Takeover `convert.go`: 12 `_ = json.Unmarshal` → `partialUnmarshal` helper
  with explicit rationale (foreign-config lenient extraction is by-design;
  callers' presence checks handle the no-match case).
- `store.go`: `_ = writeStore` (migrateOnce), `_ = os.WriteFile`
  (pre-migration `.bak`), `_ = SaveKnownHost` (TOFU) → all logged.

#### Sentinel errors wired into the legacy CLI deploy path
- `singbox.ApplyConfig` check/restart/health-probe-fail paths now wrap
  `ErrDeployFailed` (rollback OK → retry-able) or `ErrRollbackFailed`
  (rollback ALSO failed → node broken, manual intervention), matching
  `chain.pushConfig`. The health-probe `_ = performRollback` is now wrapped.
- New tests: `TestBackend_ApplyConfig_CheckFails` (ErrDeployFailed) and
  `TestBackend_ApplyConfig_CheckFails_RollbackAlsoFails` (ErrRollbackFailed).
- `TestPushConfig_CheckFails_RollbackAlsoFails` covers the chain path's
  rollback-also-failed branch (was previously untested — deferred item).

#### Config-generation / SSH-I/O layering (AGENTS.md #4, CTO-review §4)
- `applier.go` (1986 lines) split into `applier_build.go` (1739, pure
  config-gen + `ApplyChain` orchestrator) and `applier_push.go` (276, SSH
  I/O: `pushConfig` / `performRollback` / `probeServiceUp` /
  `ensureCertForTLSInbounds`). Config generation and remote I/O no longer
  mixed in one file. `AGENTS.md` Project Structure updated.

#### context.Context threaded into the SSH push (CTO-review #8)
- `pushConfig` / `pushConfigLocked` / `pushConfigWithAWG` / `probeServiceUp`
  / `ensureCertForTLSInbounds` / `ensureIPForward` / `pushAWGConfFile` /
  `enableAWGService` / `pushAWGConfs` / `awgConfDirExists` now take
  `ctx context.Context`; the `context.Background()` calls inside the deploy
  sequence are replaced with `ctx`. Exported wrappers (`PushConfig` /
  `ProbeServiceUp` / `DisableService` / `EnableService` / `RestoreFile` /
  `PushConfigForTest`) updated; all callers (ApplyChain,
  applyMergedNodeLocked, takeover, tests) pass `ctx` through. A cancelled UI
  deploy now cancels in-flight SSH commands instead of waiting out the
  timeout.

#### Autoapply concurrency cap (CTO-review §9)
- `ScheduleAutoApply` acquires a slot on a counting semaphore
  (`autoApplyMaxConcurrent = 8`) before the SSH deploy — a 100-node
  all-pending fleet now fans out to at most 8 concurrent SSH deploys, not
  100. `SetAutoApplyConcurrency(n)` override (operators/tests);
  `AutoApplyMaxConcurrent()` inspector. Covered by
  `TestScheduleAutoApply_ConcurrencyCap` (schedules 10 deploys against a
  blocking connector, asserts high-water ≤ cap and saturates the cap).
- A true SSH **connection pool** (reuse across deploys) is deferred to v1.0 —
  it requires redesigning the `SSHConnector` interface + lifecycle and the
  `defer client.Close()` pattern across ApplyChain / ApplyMergedNode /
  takeover, plus idle-eviction and host-key cache invalidation on TOFU
  change. The concurrency cap bounds the worst-case fan-out in the meantime.

#### Trust-host-key migration note (CTO-review §6)
- `NodeInfo.PendingHostKeyFingerprint` (added in the prior cycle) stores the
  actually-observed SSH fingerprint so `/trust` POSTs are verified against it
  (anti-MITM/CSRF). For live deployments upgraded from a pre-fix version, the
  field is empty on existing `NodeInfo` until the next capture — the normal
  capture → warning modal → trust flow is NOT broken. The only edge case is
  an already-open warning modal from the old version: re-capture (Status
  button) to repopulate the pending fingerprint. No schema migration needed
  (`omitempty` + zero-value = empty = refuse-to-trust-blindly = safe).

#### Verification
- `go build ./...` OK, `go vet ./...` clean, `go test ./...` 9 packages ok /
  0 FAIL, `e2e` + `wsl_smoke` compile-only green, `govulncheck`: no
  vulnerabilities, `templ generate`: no diff.

### Known follow-ups (NOT in this release — explicitly deferred)
- SSH connection pool (reuse across deploys) — v1.0, see above.
- `buildMergedNodeConfig` 7 params → config-struct.
- `web/server.go` Register ~60 routes → split by resource.
- per-client `source_ip_cidr` under TUN-overlay real-VPS verify (E2E skip
  stub; unit-tested, not live-verified).
- deps/sing-box mirror/backup (single GitHub release URL).
- AWG kernel module staging on test VPSes (prerequisite for per-client E2E).

## [v0.2.0] — 2026-07-04

Advanced 2026 Obfuscation + Working Base Stack. See the v0.2.0 release notes.

## [v0.1.0] — 2026-06-30

Rewrite (core + VPN-orchestrator features + spider graph).