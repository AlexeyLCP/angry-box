# Changelog

All notable changes to Angry-box are documented here. Versions follow a light
semver: patch (0.x.Y) for fixes/hardening within the v0.2 product focus, minor
(0.Y.0) for new protocols/features. The format is based on Keep a Changelog.

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