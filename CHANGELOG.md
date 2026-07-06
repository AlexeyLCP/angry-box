# Changelog

All notable changes to Angry-box are documented here. Versions follow a light
semver: patch (0.x.Y) for fixes/hardening within the v0.2 product focus, minor
(0.Y.0) for new protocols/features. The format is based on Keep a Changelog.

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