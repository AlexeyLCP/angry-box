# Testing Plan — angry-box v0.1.0

Three test layers: **unit** (offline, fast, in CI), **WSL smoke** (real Ubuntu over SSH, no live VPS needed), and **E2E** (real GCloud VPS, manual/CI-on-demand).

---

## 0. WSL smoke run — v0.1.0 results (executed)

The WSL smoke suite (`internal/wslsmoke`, build tag `wsl_smoke`) drives the real app pipeline against an Ubuntu-24.04 WSL instance over SSH — no live VPS required. **Run via the app itself** (`Backend.DeployOpts`, `chain.PushConfigForTest`), no manual server steps. Setup + run instructions at the bottom of this section.

### Result: 10 PASS / 1 SKIP / 0 FAIL

| Test | Result | Note |
|---|---|---|
| TestWSL_SSHConnect | ✅ PASS | SSH + TOFU works against the WSL node |
| TestWSL_DeployPatchedBinary | ✅ PASS | App downloads patched tarball, installs `/usr/local/bin/sing-box`, writes systemd unit, starts service (minimal config) |
| TestWSL_ApplyRealityXHTTP | ✅ PASS | `RenderProxyNode` → `pushConfig` → `sing-box check` passes, service active |
| TestWSL_RollbackOnBadConfig | ✅ PASS | Bad config fails check, rollback restores previous (REALITY+XHTTP), service stays active |
| TestWSL_FirstDeployNoRollback | ✅ PASS | No prior config + bad config → error surfaced (no panic), rollback is a no-op |
| TestWSL_AWGKernelInstall | ✅ PASS | Install path ran; amneziawg-tools unavailable in WSL apt + Microsoft kernel can't insmod — documented limitation, not a failure |
| TestWSL_ImportAWGConfigs | ✅ PASS | Seeded awg0.conf + awg-exit-n1.conf → SSH import parsed ListenPort/Jc/S1 + exit PublicKey/Endpoint/amnezia, back-filled placeholder inbounds (`server_priv, client_pub`) |
| TestWSL_QUICCapture | ⏭️ SKIP | UDP/443 blocked in the test environment; `CaptureQUICSignature` returned `does not support QUIC` (expected skip, not a bug) |
| TestWSL_DeployStatusHash | ✅ PASS | `LastDeployedHash` recorded; different config → different hash → pending |
| TestWSL_ConfigPreview | ✅ PASS | `RenderMergedNodeConfig` → valid JSON |
| TestWSL_AutoApplyPerHostLock | ✅ PASS | Per-host `sync.Mutex` identity: same nodeID → same mutex, different → different |

### Bugs found by the smoke run and fixed (commit 5921a59)

1. **`sudo` + multiline scripts** — `sudo set -e\nmkdir...` ran `sudo set` as a binary lookup (`sudo: set: command not found`). Wrapped install/restart/verify/AWG pipelines in `sudo bash -c '...'`.
2. **`UploadText` deadlock/empty files** — writing to stdin before `session.Run` left `cat` reading an already-closed stdin (empty files) or blocked on a full pipe buffer. Now `session.Start(cmd)` first, then write + Close + `session.Wait`.
3. **XHTTP `headers` type** — sing-box-extended's V2RayXHTTP headers field is `map[string]string`, not `map[string][]string` → `cannot unmarshal array into ...headers of type string`. Fixed the generator.
4. **REALITY + `curve_preferences`/`ECH` conflict** — sing-box-extended rejects `curve preferences is unavailable in reality` and `Reality is conflict with ECH`. Removed both from the REALITY inbound (they are client-side / plain-TLS options).

Plus: `Backend.Deploy` now writes a minimal valid config.json so a fresh deploy starts instead of crashing on a missing config; `systemctl reset-failed` before restart; `pushConfig`/`performRollback`/`probeServiceUp` gained a `useSudo` parameter; CLI `deploy` gained `-sudo`/`-install-awg` flags and `ANGRY_BINARY_URL` env override.

### WSL smoke setup + run

One-time setup (see TESTING.md history / `wsl -d Ubuntu-24.04`):
```bash
# In WSL Ubuntu-24.04 (systemd enabled via /etc/wsl.conf [boot] systemd=true):
sudo apt-get install -y openssh-server python3
# Add the test public key to ~/.ssh/authorized_keys
# Serve the patched tarball from the repo deps/:
cd /mnt/c/.../angry-box/deps && python3 -m http.server 8000 &
```
Run from Windows:
```bash
WSL_TEST_HOST=127.0.0.1:22 WSL_TEST_USER=lcp WSL_TEST_KEY=$HOME/.ssh/angry-test-key \
  go test -tags wsl_smoke ./internal/wslsmoke/ -run WSL -v -timeout 600s
```
The `ANGRY_BINARY_URL` env (used by `Backend.Deploy`) points at the local HTTP server so the test doesn't hit GitHub.

---

## 1. Unit tests (run on every push)

Already implemented; 208+ tests across packages. Run:

```bash
go test ./...              # all unit tests
go vet ./...               # static analysis
go build ./...             # compile check
```

### Coverage by package

| Package | What's covered | File(s) |
|---|---|---|
| `internal/backend/singbox` | Role-based config generation (proxy_node structure + deterministic credentials, awg_balancer kernel path + no-amnezia, awg_hop userspace amnezia); CLI `config` paths (TUIC real inbound, AWG endpoint, default REALITY+XHTTP); all-profiles valid JSON | `roles_test.go`, `config_test.go` |
| `internal/chain` | AWG preset invariants (Jmin<Jmax, \|S1+56−S2\|≥10, H1-H4 quadrants, width≥1000) ×200 runs/profile; CPS `<b 0x...>` format + odd-hex padding; deploy hash stability + audit append/list/cap; Profile unique-name + cascade-delete; Assignment uniqueness; Links CRUD + unique; SaveNodePosition round-trip; spider sync (insert after fromNode); AWG import parsers (server/exit/peers); protocol presets catalog + obfuscation levels + routing presets; domain normalize/validate; capture invalid/empty; crypto generators | `awgpresets_gen_test.go`, `awgcps_format_test.go`, `deploystatus_test.go`, `spider_test.go`, `awgimport_test.go`, `protocolpresets_test.go` |
| `internal/ssh` | SSH keypair generation (ed25519, parseable, sign/verify); host-key error strings; password-prefix detection; port defaulting | `client_test.go` |
| `internal/config` | config loader defaults | `config_test.go` |

### What to add (planned unit tests)

- [ ] **`roles_test.go`**: golden-file comparison for proxy_node/awg_balancer/awg_hop rendered JSON (snapshot tests like `test_config_builder.py`).
- [ ] **`cryptogen_test.go`** (extract from protocolpresets_test): MTProxyFullSecret composition with real domain bytes, SS password key-length per cipher (table test).
- [ ] **`awgcapture_test.go`** (extract): capture against a localhost UDP echo mock (no real network) — verify packet count + `<b 0x...>` format.
- [ ] **`buildClientURI_test.go`**: golden strings for every share-link branch (vmess base64-JSON, trojan, ss, vless-reality-xhttp with encoding, mtproxy `tg://proxy`).
- [ ] **`autoapply_test.go`**: per-host lock serialisation (two concurrent ScheduleAutoApply to the same nodeID run sequentially); WaitAutoApply drains.

---

## 2. E2E tests (real VPS over SSH)

Existing e2e suite: `internal/chain/e2e_test.go` (14 tests, `//go:build e2e`). Run:

```bash
go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 300s
```

### Test servers (from AGENTS.md)
- GCloud project: `project-d4c6c72c-4f10-4288-902`
- `vps-de-test-1` — 34.40.120.7 (Debian 12, key: `google_compute_engine`)
- `vps-de-test-2` — 35.198.166.183 (Ubuntu 24.04, key: `id_ed25519`)
- `vps-de-test-3` — 35.198.100.1 (Ubuntu 24.04, key: `id_ed25519`)
- Auth: `gcloud auth login lucipoher@gmail.com`

### Existing E2E tests (verify they pass against v0.1.0)
- TestE2E_SSHConnect, TestE2E_SSHCommand, TestE2E_KnownHostsRoundTrip, TestE2E_KnownHosts_Normalization
- TestE2E_Deploy_AlreadyInstalled, TestE2E_BackendStatus
- TestE2E_ApplyChain_SingleNode_TUIC, TestE2E_ApplyChain_SingleNode_AWG
- TestE2E_MultiNodeChain, TestE2E_Rollback
- TestE2E_MergedConfigRoundTrip, TestE2E_WireGuardKeypair, TestE2E_StoreRealPath

**Action needed**: the existing suite was written for the pre-refactor API. It compiles (verified: `go vet -tags e2e ./internal/chain/` passes), but must be re-run against the live test VPS to confirm the new deploy path (backup→cert→upload→check→rollback→restart→health-probe) and the patched binary actually work end-to-end.

### Planned E2E tests (new, for v0.1.0 features)

- [ ] **E2E_Deploy_PatchedBinary**: deploy to a fresh VPS, verify `sing-box version` reports extended + the round-robin fallback works (4 exit nodes → ~25% each).
- [ ] **E2E_AWGKernelInstall**: `InstallAWGModule` on `vps-de-test-1`, verify `lsmod | grep amneziawg` and persistence files exist.
- [ ] **E2E_AWGHop_Userspace**: multi-hop chain with AWG as inter-node transport (userspace amnezia endpoint) — verifies the wireguard-go overlap patch actually fixes the panic on real traffic (was the original bug).
- [ ] **E2E_AWGBalancer_Kernel**: awg_balancer role — TUN + bind_interface to kernel awg-exit-* interfaces, confirm traffic egresses.
- [ ] **E2E_DeployStatus_Hash**: apply a config, verify `NodeInfo.LastDeployedHash` is set; mutate an inbound; verify `has_pending_changes=true` via `/ui/api/deploy-status`.
- [ ] **E2E_ImportAWG**: SSH-import from `vps-de-test-1` (after seeding an awg0.conf), verify placeholder back-fill.
- [ ] **E2E_QUICCapture**: `CaptureQUICSignature("www.cloudflare.com")` returns `ok=true, source="quic"` with 5 packets.
- [ ] **E2E_Spider_LinkSync**: create an edge via the spider API, verify both `ConnectionLink` persisted and `Chain.Nodes` ordered correctly (toNode after fromNode); delete the last edge of a node, verify it's removed from `Chain.Nodes`.
- [ ] **E2E_MTProxy**: deploy a node with mtproxy inbound, connect a Telegram client, verify handshake.
- [ ] **E2E_Rollback_FirstDeploy**: fresh node + invalid config → verify rollback is a no-op restore (no prior config) and the error is surfaced (not a panic).

---

## 3. CI (.github/workflows)

`.github/workflows/build.yml` and `release.yml` are committed. Ensure they run:
- `go build ./...`, `go vet ./...`, `go test ./...` (unit, no e2e tag) on every push/PR.
- Release workflow tags `v*` and publishes the binary (cross-compile amd64+arm64 when arm64 builder available).

**Action needed**: review the committed workflows match v0.1.0 (binary path, build tags, no `with_admin_panel`).

---

## 4. Manual smoke test (before announcing v0.1.0)

1. `go build ./cmd/angry-box && ./angry-box serve` — web UI opens at :8090.
2. Add a test node (vps-de-test-1) via the UI; click Install; verify the patched binary is downloaded + service starts.
3. Create a proxy_node inbound (VLESS REALITY+XHTTP) via the Inbounds form; Apply; `sing-box check` passes on the remote; a vless client connects.
4. Open the Spider page; drag a node; reload — position persists; pan/zoom works; create an edge; the chain.Nodes order reflects it.
5. Check Deploy Status page — node shows "applied"; mutate an inbound → shows "pending".
6. Audit page — entries for install/deploy/create-link appear.
7. AWG: create an awg_hop chain (multi-hop); apply; verify traffic flows (the overlap-fix is exercised).
8. `angry-box config -type user -protocol tuic` → real TUIC inbound JSON (not wireguard).

---

## Run order before tagging the next release
1. `go build ./... && go vet ./... && go test ./...` — all green.
2. `go test -tags e2e ./internal/chain/ -run TestE2E -v -timeout 600s` against test VPS.
3. Manual smoke test (section 4).
4. Only then bump version + tag.