# Patches — sing-box-extended + wireguard-go

Angry-box runs a **patched** sing-box-extended (`1.13.14-extended-2.5.0-patched`)
+ a patched wireguard-go, NOT the upstream binaries. The patches live in
`patches/` and are applied at BUILD time (on a dev machine — weak VPSes never
compile Go, they download the prebuilt tarball from the project's GitHub deps).

This doc is the law for rebasing the patches on an upstream bump. The
`patchcheck` test (`internal/backend/singbox/patchcheck_test.go`) guards against
silent breakage.

## The two patches

### `patches/fallback-round-robin.patch`
- **Target:** `sing-box-extended` at tag `v1.13.14-extended-2.5.0`
  (`shtorm-7/sing-box-extended`).
- **What it does:** adds an `rrCounter` to `Fallback` in
  `protocol/group/fallback.go`, rotating the active outbound list in
  `DialContext` for per-connection round-robin across equal exit nodes. Without
  it the balancer's fallback group sticks on the first healthy exit (no
  rotation).
- **Apply:** `patch -p1` in the sing-box-extended repo root.

### `patches/wireguard-go-awg-overlap.patch`
- **Target:** `wireguard-go` at tag `v0.0.2-beta.1-extended-1.4.3`
  (`shtorm-7/wireguard-go`).
- **What it does:** fixes the `chacha20poly1305 "invalid buffer overlap of
  output and input"` panic in `device/send.go` `RoutineEncryption` under
  AmneziaWG obfuscation (userspace gVisor path). This is the panic that made
  user-facing AWG switch to kernel `awg-quick@awg0` + sing-box TUN-overlay
  (AGENTS.md #10/#11) — the patch keeps the userspace endpoint viable for
  inter-node AWG transit.
- **Apply:** `patch -p1` in the wireguard-go repo root.

## Build flow (`scripts/build-singbox.sh`)

Run on a dev machine (NOT a VPS). The flow:

1. `git clone --depth 1 --branch $SBX_VERSION shtorm-7/sing-box-extended.git`
2. `git clone --depth 1 --branch $WG_TAG shtorm-7/wireguard-go.git`
3. `( cd sing-box-extended && git checkout -- . && git apply patches/fallback-round-robin.patch )`
4. `( cd wireguard-go-patched && git checkout -- . && git apply patches/wireguard-go-awg-overlap.patch )`
5. `go mod edit -replace github.com/sagernet/wireguard-go=../wireguard-go-patched && go mod tidy`
6. `CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -ldflags "-s -w" -tags "$BUILD_TAGS"`
   (tags: `with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,
   with_clash_api,with_tailscale,with_masque,with_mtproxy,with_openvpn,
   with_trusttunnel,with_sudoku,with_snell,with_manager,with_profiler` —
   `with_admin_panel` omitted).
7. Packs `deps/sing-box-${VERSION_PLAIN}-patched-linux-${arch}.tar.gz` +
   writes `deps/checksums.txt` (sha256).

Defaults: `SBX_VERSION=v1.13.14-extended-2.5.0`, `WG_TAG=v0.0.2-beta.1-extended-1.4.3`
(overridable via env).

## Version pin (THREE places — keep in sync)

The pinned sing-box-extended version lives in THREE places. A bump MUST update
all three + pass the `patchcheck` test:

1. `scripts/build-singbox.sh` — `SBX_VERSION` / `WG_TAG` defaults (build time).
2. `internal/backend/singbox/singbox.go` — `singBoxVersion` const (deploy time:
   the orchestrator checks the installed binary's version against this).
3. `internal/backend/singbox/patchcheck_test.go` — `patchcheckSBXVersion` /
   `patchcheckWGTag` consts (regression test).

The `TestPatchcheckVersionsMatchSingBoxConst` test (runs under the `patchcheck`
build tag) asserts #2 == #3 (stripped of the leading `v`), so a bump that
forgets one place fails.

`deps/sing-box-${VERSION_PLAIN}-patched-linux-amd64.tar.gz` + `deps/checksums.txt`
+ `singBoxChecksums` (singbox.go) are the binary artifacts pinned to that version
(the checksum is fail-closed at deploy via `checksumForArch`).

## Rebase procedure (on an upstream bump)

When bumping sing-box-extended / wireguard-go to a new tag:

1. **Update the three pins** (#1, #2, #3 above) to the new version.
2. **Run the patchcheck test** (needs network + git):
   ```
   go test -tags=patchcheck -run TestPatches_ApplyCleanly ./internal/backend/singbox/ -v -timeout=300s
   ```
3. **If a patch fails to apply** (context drift — the upstream file changed
   around the patched lines):
   - Clone the new source: `git clone --branch <new-tag> shtorm-7/sing-box-extended.git`
   - Re-derive the patch against the new source: make the same logical change,
     `git diff > patches/<name>.patch`. Keep the patch minimal (only the
     logical change, no incidental hunks).
   - Re-run the patchcheck test until both patches apply cleanly.
4. **Rebuild the binary** (`scripts/build-singbox.sh`) → produces a new
   `deps/sing-box-<ver>-patched-linux-<arch>.tar.gz`.
5. **Regenerate `deps/checksums.txt`** + update `singBoxChecksums` in
   `singbox.go` with the new sha256 per arch (fail-closed at deploy — a wrong
   checksum aborts the install).
6. **Commit** the updated patches + the three pin sites + the new tarball +
   checksums + `deps/` artifacts.
7. **Reality-check** the Reality SNI (`defaultRealitySNI` in `roles.go` —
   currently `www.cloudflare.com`): an upstream utls/fingerprint bump can drift
   the TLS fingerprint; verify a REALITY handshake still passes on a test VPS
   before shipping.

## Reality SNI / fingerprint drift

`internal/backend/singbox/roles.go:41` — `const defaultRealitySNI =
"www.cloudflare.com"`. This is per-render overridable via `ProxyNodeParams.
SNIDomain`, but there is NO global/config-file override — it's a package const.
On an upstream utls bump that changes the TLS fingerprint shape, the hardcoded
SNI may start getting cut by DPI. Monitor this on bumps (the patchcheck test
does NOT cover it — it's a runtime/DPI concern, not a patch-applicability one).

## Where this is referenced

- `AGENTS.md` "sing-box-extended" section — the patched binary + module
  requirements.
- `docs/PROGRESS.md` — the kernel-AWG rework + why the wireguard-go patch
  matters (userspace amnezia panic).
- `docs/cto-review-map.md` — file map.
- `internal/backend/singbox/singbox.go` — `singBoxVersion`, `singBoxDownloadURLs`,
  `singBoxChecksums`, `installPatchedBinary` (fail-closed checksum verify),
  `installAWGModule` (PPA primary + bundled `deps/amneziawg-src.tar.gz` DKMS
  fallback).