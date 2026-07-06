# Changelog

All notable changes to Angry-box are documented here. Versions follow a light
semver: patch (0.x.Y) for fixes/hardening within the v0.2 product focus, minor
(0.Y.0) for new protocols/features. The format is based on Keep a Changelog.

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