# Patches — amnezia-box fork + amneziawg-go pin

Angry-box runs **amnezia-box** — our fork `AlexeyLCP/amnezia-box` (a fork of
`hoaxisr/amnezia-box`, which is sing-box 1.14 alpha). amnezia-box carries the
AWG 3.1 userspace endpoint (`type:"awg"`, amneziawg-go v3.1) + our ports from
sing-box-extended (mtproxy, fallback round-robin, TrustTunnel). There are NO `patches/`
applied at build time anymore — the ports are committed to the fork's tree, and
amneziawg-go is pinned in the fork's go.mod.

## What's in the fork (AlexeyLCP/amnezia-box)

- **AWG3 endpoint** (`type:"awg"`): amnezia-box's own feature — a sing-box
  endpoint that runs amneziawg-go in-process with FLAT obfuscation fields
  (jc/jmin/jmax/s1-s4/h1-h4/i1-i5 + AWG3 HeaderProtectionKey/
  ContentPaddingAddition/RekeyAfterTime). S1-S4 >= 12 when HPK is set
  (`HeaderCipherNonceSize=12`). Built by `scripts/build-singbox.sh`.
- **mtproxy** (our port from sing-box-extended): `option/mtproxy.go` +
  `protocol/mtproxy/` + `include/mtproxy{,_stub}.go` (build-tag `with_mtproxy`).
  Rename `ConnectionHandlerFuncEx → ConnectionHandlerFunc` (1.14 API). Dep:
  `dolonet/mtg-multi v1.8.0 → shtorm-7/mtg-multi v1.11.0-extended-1.0.0`
  (extended fork has `essentials.Dialer`/`DomainFrontingHost`/`UpdateUsers`).
  `TypeMTProxy` const + `registerMTProxyInbound` in `InboundRegistry()`.
- **fallback round-robin** (our port from sing-box-extended):
  `protocol/group/fallback.go` + the rr patch (`rrCounter` rotation, our prod
  strategy #18 "Round-robin (fallback)") + `FallbackOutboundOptions` +
  `TypeFallback` + `RegisterFallback` in `OutboundRegistry()`. Self-contained
  (only `OutboundGroup` + `outbound.Register`, no 1.14 adapter API bridging).

## amneziawg-go pin (in the fork's go.mod)

The fork's `go.mod` pins `github.com/amnezia-vpn/amneziawg-go/v3 => github.com/hoaxisr/amneziawg-go/v3 v3.1.0-awgm.1` (commit `ae4523cf`, module path `/v3` — AWG 3.1: RandomTrailers/DisableCookies). This commit has the
`InputPackets` API (`device.InputPacketRef` + `device.InputPackets()` in
`device/send.go`) that `transport/awg/port.go` depends on, plus the AWG3 UAPI
fields (`header_protection_key`/`content_padding_addition`/`rekey_after_time`),
the keepalive-under-content-padding fix and the `BatchSize()` batching fix.
We pin a pseudo-version (not a branch tag) — the API can change between commits.

## Version pin (THREE places — keep in sync)

The amnezia-box fork commit lives in THREE places. A bump MUST update all three:

1. `scripts/build-singbox.sh` — `ABX_REF` default (build time, full SHA).
2. `internal/backend/singbox/singbox.go` — `singBoxVersion` const (deploy time:
   the orchestrator builds the download URL + verifies the installed binary's
   tags via `isPatchedExtended` which matches `with_awg`). Plus
   `singBoxDownloadURLs` / `singBoxChecksums` (the tarball URL + sha256).
3. `internal/backend/singbox/patchcheck_test.go` — `patchcheckABXRef` const
   (full 40-char SHA, regression test). `amneziaWGGoVersion` /
   `patchcheckAWGGORef` are the amneziawg-go pin's mirror (the fork's go.mod is
   the source of truth; these consts exist for traceability + the version-match
   test catches a bump that forgets one place).

The `TestPatchcheckVersionsMatchSingBoxConst` test (runs under the `patchcheck`
build tag) asserts `singBoxVersion` is a prefix of `patchcheckABXRef` (git's
short SHA is 7+ chars), so a bump that forgets one place fails.

`deps/sing-box-<short-sha>-amnezia-linux-amd64.tar.gz` + `deps/checksums.txt` +
`singBoxChecksums` (singbox.go) are the binary artifacts pinned to that version
(the checksum is fail-closed at deploy via `checksumForArch`).

## Rebase procedure (on an amnezia-box bump)

When bumping the amnezia-box fork to a new commit (e.g. after merging an upstream
amnezia-box update or adding a port):

1. **Update the three pins** (#1 `ABX_REF`, #2 `singBoxVersion`+URLs+Checksums,
   #3 `patchcheckABXRef`) to the new commit SHA. If the fork's amneziawg-go pin
   changed, also update `amneziaWGGoVersion` + `patchcheckAWGGORef`.
2. **Run the patchcheck version-match test** (no network needed):
   ```
   go test -tags=patchcheck -run TestPatchcheckVersionsMatchSingBoxConst ./internal/backend/singbox/ -v
   go test -tags=patchcheck -run TestPatchcheckAWGGORefMatchesConst ./internal/backend/singbox/ -v
   ```
3. **Rebuild the binary** (`scripts/build-singbox.sh`) → produces a new
   `deps/sing-box-<short-sha>-amnezia-linux-<arch>.tar.gz`. Requires Go >= 1.26
   (mtg-multi needs go 1.26).
4. **Regenerate `deps/checksums.txt`** + update `singBoxChecksums` in `singbox.go`
   with the new sha256 per arch (fail-closed — a wrong checksum aborts the install).
5. **Publish the tarball** to the GitHub release the deploy code pins:
   `gh release upload v0.1.0 deps/sing-box-*-amnezia-*.tar.gz`.
6. **Commit** the updated pin sites + the new tarball + checksums.
7. **Verify on a test VPS (n1)**: install the new binary via the deploy path,
   confirm `sing-box version` reports the new SHA + `with_awg`+`with_mtproxy`
   tags, run a `type:"awg"` config through `sing-box check`, and run the kernel
   AWG + TUN-overlay e2e (handshake + egress) before shipping.

## Reality SNI / fingerprint drift

`internal/backend/singbox/roles.go` — `const defaultRealitySNI = "www.cloudflare.com"`.
Per-render overridable via `ProxyNodeParams.SNIDomain`, but there is NO
global/config-file override — it's a package const. On an upstream utls bump that
changes the TLS fingerprint shape, the hardcoded SNI may start getting cut by DPI.
Monitor this on bumps (the patchcheck test does NOT cover it — it's a runtime/DPI
concern, not a patch-applicability one).

## Where this is referenced

- `AGENTS.md` "amnezia-box" section — the base sing-box fork + what it carries.
- `docs/PROGRESS.md` §30 (AWG3 spike) + §31 (amnezia-box migration).
- `docs/cto-review-map.md` / `docs/cto-review-instruction.md` — file map
  (NOTE: these still reference the old sing-box-extended/patches/ stack
  historically; the amnezia-box migration supersedes them — update on next review).
- `internal/backend/singbox/singbox.go` — `singBoxVersion`,
  `singBoxDownloadURLs`, `singBoxChecksums`, `installPatchedBinary` (fail-closed
  checksum verify), `isPatchedExtended` (`with_awg` canary),
  `installAWGModule` (kernel module: PPA primary + bundled
  `deps/amneziawg-src.tar.gz` DKMS fallback — unchanged by the amnezia-box
  migration; the kernel AWG path is separate from the sing-box endpoint).