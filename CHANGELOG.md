# Changelog

All notable changes to Angry-box are documented here. Versions follow a light
semver: patch (0.x.Y) for fixes/hardening within the v0.2 product focus, minor
(0.Y.0) for new protocols/features. The format is based on Keep a Changelog.

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